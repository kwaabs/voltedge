-- seed/mv_metering_cubicle_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase 43, Slice 2 (MV metering cubicle). See docs/phase-43-mv-metering.md.
--
-- MV_METERING_CUBICLE -- the physical enclosure at a customer's MV intake that
-- houses the metering plant: the meter (MV_METER) and the instrument
-- transformers that feed it (MV_CT, MV_VT). It is the MV analogue of
-- METER_CHAMBER (seed/lv_metering_example.sql) and reuses that machinery
-- byte-for-byte:
--   * network_role 'container' -- NOT a traced node, no terminals. It groups
--     its plant by the `containment` relationship, exactly like METER_CHAMBER
--     contains its busbar + meters. (The electrical delivery point remains the
--     MV_METERING_POINT network leaf from Slice 1 -- the cubicle is the box, the
--     metering point is the connection; the cubicle does NOT contain the point,
--     just as METER_CHAMBER does not contain SERVICE_POINT.)
--   * geometry 'point' -- it has a real location; a map point you click to open
--     its schematic (the METER_CHAMBER shape, NOT the geometry-less
--     MV_SWITCHBOARD that must be placed on a parent one-line first).
--   * is_diagram_container = true -- it OWNS a METERING_SCHEMATIC diagram: a
--     mimic of the meter wired to its CT/VT. Restricted by the per-(container x
--     diagram type) allow-list (migration 0068) to exactly {MV_METER, MV_CT,
--     MV_VT} -- seeded at the foot of this file.
--
-- Depends on seed/mv_metering_example.sql (MV_METER) and
-- seed/mv_measuring_example.sql (MV_CT, MV_VT) having been applied -- this file
-- adds one container class, its containment relationship_classes to those three,
-- and the diagram type + allow-list. No new relationship kind (containment
-- already exists). Domains / field_groups re-asserted idempotently.
--
-- Apply with psql, then compile the one new class:
--   task seed-mv-metering-cubicle   (or the psql -f invocation directly)
--   task compile CODE=MV_METERING_CUBICLE
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply, the compile, and a
-- create-cubicle + open-its-METERING_SCHEMATIC + place-meter/CT/VT check are
-- still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('service', 'Service', 1),
    ('maintenance', 'Maintenance', 2)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('CONDITION_RATING', 'Condition rating', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ---------------------------------------------------- MV_METERING_CUBICLE --
-- Container asset -- network_role 'container' (no terminals). geometry 'point'
-- (locatable; clicked to open its schematic). The containment
-- relationship_classes below tie it to its meter + CT + VT.
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('MV_METERING_CUBICLE', 'MV Metering Cubicle', 'point', 4326, 'container', 'draft', true)
ON CONFLICT (code) DO NOTHING;

-- Reconcile the diagram-container flag on an ALREADY-SEEDED database: the INSERT
-- above is ON CONFLICT DO NOTHING, so on a DB where this class predates this
-- edit the new is_diagram_container never takes. Idempotent; a no-op on a fresh
-- install where the INSERT already set it. (Same guard METER_CHAMBER uses.)
UPDATE metadata.feature_classes SET is_diagram_container = true
 WHERE code = 'MV_METERING_CUBICLE' AND is_diagram_container IS DISTINCT FROM true;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'indoor'), (2, 'outdoor_kiosk')) AS v(code, name) ON true
WHERE fc.code = 'MV_METERING_CUBICLE'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- cubicle_ref is the asset tag (nullable -- may be unlabelled); condition_rating
-- matches every other physical asset class. Mirrors METER_CHAMBER's fields.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('cubicle_ref',      'text', NULL,               'service',     true),
    ('condition_rating', 'text', 'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'MV_METERING_CUBICLE'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_METERING_CUBICLE')
  AND f.name = 'condition_rating'
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- No terminals: network_role 'container'.

-- --------------------------------------------------- relationship_classes --
-- The cubicle CONTAINS (spatial) its meter + instrument transformers; one
-- cubicle, many of each, so one_to_many. Guarded on the (origin, destination,
-- kind) via NOT EXISTS. Exact analogue of METER_CHAMBER contains {LV_BUSBAR,
-- METER}.
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'MV_METERING_CUBICLE contains ' || dst.code, cub.id, dst.id, 'containment', 'one_to_many'
FROM metadata.feature_classes cub
JOIN metadata.feature_classes dst ON dst.code IN ('MV_METER', 'MV_CT', 'MV_VT')
WHERE cub.code = 'MV_METERING_CUBICLE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = cub.id AND destination_feature_class_id = dst.id AND kind = 'containment'
  );

-- --------------------------------------------------- metering diagram type --
-- METERING_SCHEMATIC on the ordinary coded DIAGRAM_TYPE domain, distinct from
-- the substation-yard ONE_LINE (diagram_example.sql), the CHAMBER_SCHEMATIC
-- (lv_metering_example.sql), and the BOARD_SCHEMATIC (mv_switchgear_panel).
-- sort_order 4 follows those three. Re-asserted idempotently so this seed is
-- self-sufficient whether or not the earlier diagram seeds ran.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('DIAGRAM_TYPE', 'Diagram type', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, 'METERING_SCHEMATIC', 'MV metering schematic', 4
FROM metadata.domains d
WHERE d.code = 'DIAGRAM_TYPE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.domain_values
    WHERE domain_id = d.id AND code = 'METERING_SCHEMATIC'
  );

-- ------------------------------------------- diagram placement allow-list --
-- Restrict the (MV_METERING_CUBICLE x METERING_SCHEMATIC) pairing to exactly
-- the metering plant: the meter and its two instrument transformers. Because
-- this pairing now has rows, api/internal/diagram placeFeature rejects any other
-- class on it -- even a globally is_diagram_placeable one (migration 0068). A
-- pairing left with NO rows is unconstrained, so no other diagram is affected.
-- Guarded on the (container, type, class) unique index so re-applying is a no-op.
INSERT INTO metadata.diagram_placement_rules (container_feature_class_id, diagram_type_value_id, feature_class_id)
SELECT cub.id, dv.id, allowed.id
FROM metadata.feature_classes cub
CROSS JOIN metadata.domain_values dv
JOIN metadata.domains d ON d.id = dv.domain_id AND d.code = 'DIAGRAM_TYPE'
JOIN metadata.feature_classes allowed ON allowed.code IN ('MV_METER', 'MV_CT', 'MV_VT')
WHERE cub.code = 'MV_METERING_CUBICLE'
  AND dv.code = 'METERING_SCHEMATIC'
ON CONFLICT (container_feature_class_id, diagram_type_value_id, feature_class_id) DO NOTHING;
