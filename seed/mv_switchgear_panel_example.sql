-- seed/mv_switchgear_panel_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase 42, Slice 1 (indoor MV switchgear board + panels -- the catalog).
--
-- Models the INDOOR metal-clad MV switchgear lineup: a row of cubicles
-- ("panels"), each one a switching position on a shared busbar, each carrying
-- a stack of devices we already have (breaker + disconnectors + earth switch +
-- CT + VT + relay). This is distinct from the OUTDOOR standalone ring main
-- unit (a map-placed junction, a separate later feature): a switchboard is
-- defined by being INSIDE a substation, so it is a contained, geometry-less
-- assembly, not a network node on the map.
--
-- Three new classes, plus the containment + diagram wiring that ties them into
-- the existing yard -> plant machinery. NO engine touch: the load-flow / trace
-- / fault code never sees any of this -- it is asset structure + drawing only.
--
--   * MV_SWITCHBOARD -- the board / lineup. Geometry-less (no independent map
--     footprint -- it lives inside the yard), network_role 'container', and
--     is_diagram_container = true: it OWNS the board single-line mimic (the new
--     BOARD_SCHEMATIC diagram type). This is the drawing an operator opens to
--     see the shared busbar with every feeder hanging off it. Contained by
--     SUBSTATION, the same slot BAY occupies (bay_containment_example.sql).
--
--   * MV_SWITCHGEAR_PANEL -- one cubicle / switching position. Geometry-less,
--     network_role 'container' (it groups a circuit's devices), carries a
--     panel_role field on the EXISTING BAY_ROLE domain (INCOMER / FEEDER /
--     BUS_COUPLER / BUS_SECTION / TRANSFORMER -- a 1:1 fit, so no duplicate
--     domain; a fresh PANEL_ROLE is a one-line field repoint later if indoor
--     panels ever diverge from outdoor yard bays). is_diagram_placeable = true:
--     a panel is a labelled column on the board mimic.
--
--   * MV_EARTH_SWITCH -- NEW device. We had MV_ISOLATOR / MV_SECTIONALIZER but
--     no earthing switch; a switchgear panel has one per way. Modelled like the
--     other Phase 27 MV plant (geometry_optional point, is_diagram_placeable)
--     BUT network_role = 'none' for now -- it is recorded + drawn only, not a
--     traced series node. Making a CLOSED earth switch a real ground / trace
--     barrier is a natural later tie-in to the safety-trace + ground-fault work
--     ([[phase-ground-fault]]), deliberately out of scope here. device_state is
--     on the shared DEVICE_STATE domain -- an earth switch is normally OPEN.
--
-- CONTAINMENT (metadata.relationship_classes, legal pairings only -- no
-- instances; those live in runtime.feature_relationships, written via the API):
--
--     SUBSTATION -> MV_SWITCHBOARD -> MV_SWITCHGEAR_PANEL -> {breaker,
--         isolator, sectionalizer, fuse, arrester, earth switch, CT, VT, relay}
--
-- The device set the panel may contain mirrors BAY's (bay_containment_example
-- .sql) plus the new earth switch. A device class having two legal containment
-- parents (BAY and MV_SWITCHGEAR_PANEL) is fine -- a given device INSTANCE is
-- contained by exactly one at runtime. The shared bus (MV_BUSBAR) is NOT
-- panel-contained: like the yard's bus it spans every panel, so it stays
-- placed straight on the board mimic, not owned by one column.
--
-- THE BOARD MIMIC -- reuses the METER_CHAMBER machinery byte-for-byte
-- (lv_metering_example.sql): a BOARD_SCHEMATIC value on the DIAGRAM_TYPE domain,
-- + a diagram_placement_rules allow-list (migration 0068) restricting the
-- (MV_SWITCHBOARD x BOARD_SCHEMATIC) pairing to exactly the bus + the panel
-- frame + the device classes that make up a column. Any pairing with rows is
-- opt-in restricted; every other diagram is unaffected.
--
-- NO TEMPLATE HERE. This slice lets you build a panel by hand (create panel,
-- create each device, wire containment, place on the mimic) end-to-end, proving
-- the container / diagram / containment shape. The one-action "place a FEEDER
-- panel and fan out its standard device set" is Slice 2 (metadata.panel_templates
-- + a composite create endpoint).
--
-- Depends on: seed/substation_yard_example.sql (SUBSTATION),
-- seed/mv_switching_example.sql + mv_protection_example.sql + mv_measuring_example
-- .sql (the MV device classes + DEVICE_STATE/CONDITION_RATING/VOLTAGE_CLASS
-- domains), seed/protective_relay_example.sql (PROTECTIVE_RELAY),
-- seed/mv_busbar_example.sql (MV_BUSBAR), and migrations 0001-0068 (diagram
-- placement rules). Any device class not yet seeded is simply skipped by the
-- JOINs, so this file is order-independent against them. All domains /
-- field_groups are re-asserted idempotently so it stands alone.
--
-- Apply with psql, then compile the three new classes (all three get a table --
-- a 'none'-geometry class still compiles, same as BAY):
--   task seed-mv-switchgear-panels   (or the psql -f invocation directly)
--   task compile CODE=MV_EARTH_SWITCH
--   task compile CODE=MV_SWITCHBOARD
--   task compile CODE=MV_SWITCHGEAR_PANEL
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + the three `task compile` runs
-- + a build-a-panel-by-hand-on-the-board-mimic check are still owed to call it
-- verified.

-- --------------------------------------------------------------- groups --
INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('switchgear', 'Switchgear', 1),
    ('electrical', 'Electrical', 1),
    ('maintenance', 'Maintenance', 2)
ON CONFLICT (code) DO NOTHING;

-- -------------------------------------------------------------- domains --
-- All re-asserted idempotently so this seed is self-sufficient regardless of
-- order. BAY_ROLE is reused verbatim for panel_role; DEVICE_STATE /
-- CONDITION_RATING / VOLTAGE_CLASS come from the Phase 27 MV seeds.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('BAY_ROLE', 'Bay role', 'coded'),
    ('DEVICE_STATE', 'Device state', 'coded'),
    ('VOLTAGE_CLASS', 'Voltage class', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded'),
    ('DIAGRAM_TYPE', 'Diagram type', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('BAY_ROLE', 'INCOMER', 'Incomer', 1),
    ('BAY_ROLE', 'FEEDER', 'Outgoing feeder', 2),
    ('BAY_ROLE', 'BUS_COUPLER', 'Bus coupler', 3),
    ('BAY_ROLE', 'BUS_SECTION', 'Bus section', 4),
    ('BAY_ROLE', 'TRANSFORMER', 'Transformer', 5),
    ('DEVICE_STATE', 'CLOSED', 'Closed', 1),
    ('DEVICE_STATE', 'OPEN', 'Open', 2),
    ('VOLTAGE_CLASS', '11KV', '11 kV', 1),
    ('VOLTAGE_CLASS', '33KV', '33 kV', 2),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- =========================================================================
-- MV_EARTH_SWITCH -- new earthing-switch device.
-- geometry_optional point + is_diagram_placeable (Phase 27 map-or-diagram-or-
-- both shape) but network_role 'none' (recorded/drawn only this slice -- see
-- header). network 'MV'. Subtypes name the earthing arrangement.
-- =========================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional, network)
VALUES ('MV_EARTH_SWITCH', 'MV Earth Switch', 'point', 4326, 'none', 'draft', false, true, true, 'MV')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'manual'), (2, 'fault_making')) AS v(code, name) ON true
WHERE fc.code = 'MV_EARTH_SWITCH'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('device_state', 'text', 'DEVICE_STATE', 'electrical', false),
    ('condition_rating', 'text', 'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_EARTH_SWITCH'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_EARTH_SWITCH')
  AND f.name IN ('device_state', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- =========================================================================
-- MV_SWITCHBOARD -- the indoor board / lineup. geometry 'none' (lives inside
-- the yard, no map footprint -- like BAY), network_role 'container'.
-- BOTH diagram flags true:
--   * is_diagram_container = true -- it OWNS the BOARD_SCHEMATIC mimic.
--   * is_diagram_placeable = true -- it is ALSO a node on its parent's diagram
--     (the substation-yard ONE_LINE). This second flag is what makes the board
--     reachable at all: unlike METER_CHAMBER (a map point you click to open its
--     schematic), the board is geometry-less, so with no map footprint the only
--     way to select it and open its BOARD_SCHEMATIC is to place it on the yard
--     one-line first. A class being container + placeable at once is legal --
--     the two flags answer different questions (who may OWN a diagram vs who may
--     be PLACED on one). network 'MV'. Subtypes name the switchgear technology.
-- =========================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, network)
VALUES ('MV_SWITCHBOARD', 'MV Switchboard', 'none', NULL, 'container', 'draft', true, true, 'MV')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'metal_clad'), (2, 'gas_insulated')) AS v(code, name) ON true
WHERE fc.code = 'MV_SWITCHBOARD'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- board_ref is the asset tag (nullable -- may be unlabelled); voltage_class
-- is the required identifier; room is the free-text location (e.g. "33 kV
-- Room") so two boards in separate switch rooms are told apart without a
-- container class -- the board is the grouping unit, the room is just where it
-- physically sits (nullable, no coded domain, rooms are free-form);
-- condition_rating optional on a logical assembly.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('voltage_class', 'text', 'VOLTAGE_CLASS', 'switchgear', false),
    ('board_ref', 'text', NULL, 'switchgear', true),
    ('room', 'text', NULL, 'switchgear', true),
    ('condition_rating', 'text', 'CONDITION_RATING', 'maintenance', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_SWITCHBOARD'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_SWITCHBOARD')
  AND f.name = 'voltage_class'
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- =========================================================================
-- MV_SWITCHGEAR_PANEL -- one cubicle / switching position. geometry 'none'
-- (contained by the board, placed on the mimic -- no map footprint),
-- network_role 'container' (groups the panel's devices), is_diagram_placeable
-- = true (a labelled column on the board mimic). panel_role reuses BAY_ROLE.
-- network 'MV'. One 'standard' subtype -- the functional variant is the
-- panel_role FIELD, the same call BAY made.
-- =========================================================================
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, network)
VALUES ('MV_SWITCHGEAR_PANEL', 'MV Switchgear Panel', 'none', NULL, 'container', 'draft', false, true, 'MV')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, 1, 'standard'
FROM metadata.feature_classes fc
WHERE fc.code = 'MV_SWITCHGEAR_PANEL'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- panel_role + voltage_class are the required identifiers of the panel's
-- circuit; panel_ref is the optional cubicle tag; condition_rating optional.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('panel_role', 'text', 'BAY_ROLE', 'switchgear', false),
    ('voltage_class', 'text', 'VOLTAGE_CLASS', 'switchgear', false),
    ('panel_ref', 'text', NULL, 'switchgear', true),
    ('condition_rating', 'text', 'CONDITION_RATING', 'maintenance', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_SWITCHGEAR_PANEL'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_SWITCHGEAR_PANEL')
  AND f.name IN ('panel_role', 'voltage_class')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- -------------------------------------------------- reconcile flags/network --
-- On a DB where any of the three classes predate this seed, the ON CONFLICT
-- (code) DO NOTHING INSERTs above are skipped, so the diagram flags + network
-- would sit at their column defaults. These idempotent UPDATEs bring an
-- already-seeded row up to date (no-op on a fresh install). Same pattern as
-- lv_metering_example.sql's diagram-flag reconcile.
UPDATE metadata.feature_classes SET is_diagram_container = true
 WHERE code = 'MV_SWITCHBOARD' AND is_diagram_container IS DISTINCT FROM true;
UPDATE metadata.feature_classes SET is_diagram_placeable = true
 WHERE code IN ('MV_SWITCHBOARD', 'MV_SWITCHGEAR_PANEL', 'MV_EARTH_SWITCH') AND is_diagram_placeable IS DISTINCT FROM true;
UPDATE metadata.feature_classes SET network = 'MV'
 WHERE code IN ('MV_SWITCHBOARD', 'MV_SWITCHGEAR_PANEL', 'MV_EARTH_SWITCH') AND network IS DISTINCT FROM 'MV';

-- =========================================================================
-- CONTAINMENT relationship_classes -- legal pairings only (no instances),
-- guarded on (origin, destination, kind) via NOT EXISTS. Classes not yet
-- seeded are skipped by the JOIN.
-- =========================================================================

-- SUBSTATION contains MV_SWITCHBOARD (one_to_many).
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'SUBSTATION contains MV_SWITCHBOARD', yard.id, board.id, 'containment', 'one_to_many'
FROM metadata.feature_classes yard, metadata.feature_classes board
WHERE yard.code = 'SUBSTATION' AND board.code = 'MV_SWITCHBOARD'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = yard.id AND destination_feature_class_id = board.id AND kind = 'containment'
  );

-- MV_SWITCHBOARD contains MV_SWITCHGEAR_PANEL (one_to_many).
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'MV_SWITCHBOARD contains MV_SWITCHGEAR_PANEL', board.id, panel.id, 'containment', 'one_to_many'
FROM metadata.feature_classes board, metadata.feature_classes panel
WHERE board.code = 'MV_SWITCHBOARD' AND panel.code = 'MV_SWITCHGEAR_PANEL'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = board.id AND destination_feature_class_id = panel.id AND kind = 'containment'
  );

-- MV_SWITCHGEAR_PANEL contains each per-circuit device class (one_to_many).
-- Mirrors BAY's contained set (bay_containment_example.sql) + the new earth
-- switch. One set-generating INSERT; guarded on (origin, destination, kind).
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'MV_SWITCHGEAR_PANEL contains ' || dst.code, panel.id, dst.id, 'containment', 'one_to_many'
FROM metadata.feature_classes panel
JOIN metadata.feature_classes dst ON dst.code IN (
    'MV_BREAKER', 'MV_ISOLATOR', 'MV_SECTIONALIZER',
    'MV_FUSE', 'MV_ARRESTER', 'MV_EARTH_SWITCH', 'MV_CT', 'MV_VT', 'PROTECTIVE_RELAY'
)
WHERE panel.code = 'MV_SWITCHGEAR_PANEL'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = panel.id AND destination_feature_class_id = dst.id AND kind = 'containment'
  );

-- ------------------------------------------------ board diagram type --
-- BOARD_SCHEMATIC on the ordinary coded DIAGRAM_TYPE domain, distinct from the
-- substation-yard ONE_LINE (diagram_example.sql) and the CHAMBER_SCHEMATIC
-- (lv_metering_example.sql). sort_order 3 follows those two.
INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, 'BOARD_SCHEMATIC', 'Switchboard single-line', 3
FROM metadata.domains d
WHERE d.code = 'DIAGRAM_TYPE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.domain_values
    WHERE domain_id = d.id AND code = 'BOARD_SCHEMATIC'
  );

-- ------------------------------------------- diagram placement allow-list --
-- Restrict the (MV_SWITCHBOARD x BOARD_SCHEMATIC) pairing to exactly what makes
-- up the board single-line: the shared bus, the panel column frame, and the
-- device classes that fill a column. Because this pairing now has rows,
-- api/internal/diagram placeFeature rejects any other class on it even if
-- globally placeable (migration 0068). Every other diagram (no rows) is
-- unaffected. Guarded on the (container, type, class) unique index. Any device
-- class not yet seeded is skipped by the JOIN.
INSERT INTO metadata.diagram_placement_rules (container_feature_class_id, diagram_type_value_id, feature_class_id)
SELECT board.id, dv.id, allowed.id
FROM metadata.feature_classes board
CROSS JOIN metadata.domain_values dv
JOIN metadata.domains d ON d.id = dv.domain_id AND d.code = 'DIAGRAM_TYPE'
JOIN metadata.feature_classes allowed ON allowed.code IN (
    'MV_BUSBAR', 'MV_SWITCHGEAR_PANEL',
    'MV_BREAKER', 'MV_ISOLATOR', 'MV_SECTIONALIZER',
    'MV_FUSE', 'MV_ARRESTER', 'MV_EARTH_SWITCH', 'MV_CT', 'MV_VT', 'PROTECTIVE_RELAY'
)
WHERE board.code = 'MV_SWITCHBOARD'
  AND dv.code = 'BOARD_SCHEMATIC'
ON CONFLICT (container_feature_class_id, diagram_type_value_id, feature_class_id) DO NOTHING;
