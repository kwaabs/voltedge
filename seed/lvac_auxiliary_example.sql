-- seed/lvac_auxiliary_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
--
-- STATION AC-AUXILIARY / LVAC LAYER: the piece of a HV/MV substation that the
-- yard + control-room survey was missing. The yard equipment (POWER_XFMR,
-- MV_BUSBAR, switching) is modelled; the control room's secondary layer is
-- modelled on the DC side (STATION_BATTERY/BATTERY_CHARGER/CONTROL_IED, see
-- seed/pnc_control_devices_example.sql). What had NO representation was the
-- station's own AC low-voltage services: the station-service ("auxiliary")
-- transformer that steps a MV tap down to 400/230 V, and the LVAC board it
-- feeds -- the source of supply for the chargers, cooling fans, tap-changer
-- motors, lighting and small power inside the station. This seed adds that as
-- a REAL, TRACEABLE AC subsystem (Option B), not a pair of inventory records:
--
--   * AUX_TRANSFORMER -- the station-service transformer, an INLINE junction.
--     T1 (primary, MV in) / T2 (secondary, LV out). It carries the same
--     load-flow field NAMES as POWER_XFMR/DIST_XFMR (primary_voltage /
--     secondary_voltage / impedance_pct / rated_kva), so the field-name-driven
--     powerflow engine (api/internal/powerflow -- resolveTransformerModel /
--     resolveTransformerRating read by NAME off any junction, class-agnostic)
--     sweeps it as a real step-down node with a loading %, zero engine change.
--     A station aux transformer IS a small power transformer, so it maps to CIM
--     PowerTransformer (foot of file).
--
--   * LVAC_BOARD -- the station AC low-voltage board (the "LVAC panel"), an
--     inline junction. T1 (incoming) / T2 the equipotential fan-out side that
--     supplies the station's AC auxiliary circuits -- modelled exactly like the
--     LV_BUSBAR distribution board (one T2 tap, N feeders each a distinct
--     feature_connectivity row; see seed/lv_metering_example.sql decision 2).
--     Maps to CIM BusbarSection.
--
-- WHY TRACEABLE (Option B), NOT geometry-'none' inventory rows: making these
-- real junctions means a downstream trace and a load flow walk
-- MV bus -> AUX_TRANSFORMER -> LVAC_BOARD -> auxiliary feeders, so "what feeds
-- the battery charger / the cooling fans / the tap-changer motor" is answerable
-- from the network, and de-energising the MV tap correctly shows the station's
-- own AC services as de-energised. The DC-auxiliary classes stay geometry/
-- network 'none' (they're behind the charger, which is fed FROM this board) --
-- wiring the charger's AC-in edge to the LVAC board is a deliberate later edge
-- (decision (i)): the charger is network_role 'none' today, so it cannot yet be
-- a trace node; this seed stops at the board's T2 fan-out.
--
-- MODELLING NOTES (recorded so they're not silent):
--
--   1. Single subtype 'standard' on both classes -- a station typically has one
--      or two aux transformers and one or two LVAC boards of one kind; the
--      variety is captured in fields (rating, phase config), not a subtype axis
--      that would change the terminals. Terminals are therefore subtype-agnostic
--      (subtype_id NULL), the common pattern -- unlike POWER_XFMR whose winding
--      count genuinely changes its terminals.
--
--   2. AUX_TRANSFORMER primary is fed two ways, both seeded: off the MV bus
--      (MV_BUSBAR T2 -> AUX_TRANSFORMER T1, the usual station-service tap, a
--      device hop so phase_rule NULL) OR off a MV feeder directly
--      (MV_CONDUCTOR -> AUX_TRANSFORMER T1, voltage-matched like POWER_XFMR's
--      HV-in rule). primary_voltage reuses VOLTAGE_CLASS (11KV/33KV already
--      seeded by connectivity_example) -- no new domain values.
--
--   3. LVAC_BOARD T2 fans out to LV_CONDUCTOR (the station's AC auxiliary
--      wiring runs). One rule scales to any number of auxiliary circuits, the
--      LV_BUSBAR T2 -> METER analog. The charger/fan/motor loads hang off those
--      runs; wiring named LV loads is future work.
--
-- Depends on seed/connectivity_example.sql (MV_CONDUCTOR, LV_CONDUCTOR,
-- VOLTAGE_CLASS / CONDITION_RATING / PHASE domains), seed/mv_busbar_example.sql
-- (MV_BUSBAR + its T2 tap), and seed/substation_yard_example.sql (SUBSTATION
-- container) having been applied. The domains + field_groups are re-asserted
-- idempotently so this seed stands alone regardless of order. The CIM mapping
-- rows at the foot additionally need migration 0034 (cim_* tables); if it is
-- absent those INSERTs are simply skipped by the guards.
--
-- Apply with psql after the seeds above, then compile the two new classes:
--   task seed-lvac-auxiliary   (or the psql -f invocation directly)
--   task compile CODE=AUX_TRANSFORMER
--   task compile CODE=LVAC_BOARD
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + the two `task compile` runs +
-- a downstream-trace + load-flow re-check across an AUX_TRANSFORMER -> LVAC_BOARD
-- chain are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('maintenance', 'Maintenance', 2),
    ('service', 'Service', 1)
ON CONFLICT (code) DO NOTHING;

-- Domains: reuse VOLTAGE_CLASS + CONDITION_RATING + PHASE -- normally seeded
-- upstream, re-asserted idempotently so the fields below always have a domain.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('VOLTAGE_CLASS', 'Voltage class', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded'),
    ('PHASE', 'Phase', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('VOLTAGE_CLASS', '11KV', '11 kV', 1),
    ('VOLTAGE_CLASS', '33KV', '33 kV', 2),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3),
    ('PHASE', 'L1N',     'L1 + N (1-phase)',       5),
    ('PHASE', 'L1L2L3N', 'L1 L2 L3 + N (3-phase)', 8)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ------------------------------------------------- AUX_TRANSFORMER --
-- The station-service transformer -- an inline junction, MV in (T1) / LV out
-- (T2). geometry_optional + is_diagram_placeable (same reasoning as the MV
-- plant classes: it may be placed geographically, on a one-line, or both).
-- Single 'standard' subtype; the variety is in the fields, not the terminals.
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('AUX_TRANSFORMER', 'Station Auxiliary Transformer', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'standard')) AS v(code, name) ON true
WHERE fc.code = 'AUX_TRANSFORMER'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- Fields. primary_voltage is the one required electrical field (coded MV
-- class); the rest of the load-flow nameplate is nullable so an un-surveyed
-- unit degrades gracefully (the engine treats absent secondary_voltage as
-- pass-through, absent impedance_pct as ideal). Field NAMES match
-- POWER_XFMR/DIST_XFMR so the field-name-driven sweep picks them up unchanged.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('primary_voltage',   'text',    'VOLTAGE_CLASS',    'electrical',  false),
    ('secondary_voltage', 'numeric', NULL,               'electrical',  true),
    ('impedance_pct',     'numeric', NULL,               'electrical',  true),
    ('rated_kva',         'numeric', NULL,               'electrical',  true),
    ('condition_rating',  'text',    'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'AUX_TRANSFORMER'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'AUX_TRANSFORMER')
  AND f.name IN ('primary_voltage', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary',   true),
    ('T2', 'secondary', true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'AUX_TRANSFORMER'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ------------------------------------------------------- LVAC_BOARD --
-- The station AC low-voltage board -- an inline junction. T1 incoming, T2 the
-- equipotential fan-out side that supplies the station's AC auxiliary circuits,
-- modelled exactly like the LV_BUSBAR distribution board (one T2 tap, N feeders).
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('LVAC_BOARD', 'Station LVAC Board', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'standard')) AS v(code, name) ON true
WHERE fc.code = 'LVAC_BOARD'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- board_ref is the asset tag (nullable -- may be unlabelled); phase is the
-- board's configuration (1ph or 3ph, reusing PHASE); incoming_rating_a +
-- num_ways are survey data; condition_rating matches every physical asset.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('board_ref',        'text',    NULL,               'service',     true),
    ('phase',            'text',    'PHASE',            'electrical',  false),
    ('incoming_rating_a','numeric', NULL,               'electrical',  true),
    ('num_ways',         'numeric', NULL,               'electrical',  true),
    ('condition_rating', 'text',    'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'LVAC_BOARD'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'LVAC_BOARD')
  AND f.name IN ('phase', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'other',   true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'LVAC_BOARD'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- network_rules -- the directed shape that lets a DOWNSTREAM trace and a load
-- flow walk MV bus/feeder -> aux transformer -> LVAC board -> auxiliary runs.
-- No unique key on network_rules, so each is guarded WHERE NOT EXISTS (same as
-- every other seed). Device hops leave phase_rule NULL; the MV-feeder-in rule
-- carries the voltage-match phase_rule POWER_XFMR uses.
-- ---------------------------------------------------------------------------

-- Station-service tap off the MV bus (the usual arrangement) -- the bar's T2
-- fan-out into the aux transformer's MV winding (T1). Device hop, phase_rule NULL.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_BUSBAR T2 -> AUX_TRANSFORMER T1', b.id, t2.id, xf.id, t1.id, NULL, true
FROM metadata.feature_classes b CROSS JOIN metadata.feature_classes xf
JOIN metadata.terminals t2 ON t2.feature_class_id = b.id AND t2.code = 'T2'
JOIN metadata.terminals t1 ON t1.feature_class_id = xf.id AND t1.code = 'T1'
WHERE b.code = 'MV_BUSBAR' AND xf.code = 'AUX_TRANSFORMER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = b.id AND from_terminal_id = t2.id AND to_feature_class_id = xf.id AND to_terminal_id = t1.id
  );

-- Direct feeder tap off a MV conductor (voltage-matched, like POWER_XFMR HV-in).
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> AUX_TRANSFORMER T1', mv.id, xf.id, t1.id, 'from.voltage_class = to.primary_voltage', true
FROM metadata.feature_classes mv CROSS JOIN metadata.feature_classes xf
JOIN metadata.terminals t1 ON t1.feature_class_id = xf.id AND t1.code = 'T1'
WHERE mv.code = 'MV_CONDUCTOR' AND xf.code = 'AUX_TRANSFORMER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = xf.id AND to_terminal_id = t1.id
  );

-- The aux transformer's LV winding (T2) into the LVAC board's incoming way (T1).
-- Device hop, phase_rule NULL. This is what makes the board's supply traceable.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'AUX_TRANSFORMER T2 -> LVAC_BOARD T1', xf.id, t2.id, bd.id, t1.id, NULL, true
FROM metadata.feature_classes xf CROSS JOIN metadata.feature_classes bd
JOIN metadata.terminals t2 ON t2.feature_class_id = xf.id AND t2.code = 'T2'
JOIN metadata.terminals t1 ON t1.feature_class_id = bd.id AND t1.code = 'T1'
WHERE xf.code = 'AUX_TRANSFORMER' AND bd.code = 'LVAC_BOARD'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = xf.id AND from_terminal_id = t2.id AND to_feature_class_id = bd.id AND to_terminal_id = t1.id
  );

-- The board's T2 fans out to the station's AC auxiliary wiring runs -- ONE rule,
-- any number of auxiliary circuits (the LV_BUSBAR T2 -> METER analog). Device
-- hop, phase_rule NULL.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT 'LVAC_BOARD T2 -> LV_CONDUCTOR', bd.id, t2.id, lv.id, NULL, true
FROM metadata.feature_classes bd CROSS JOIN metadata.feature_classes lv
JOIN metadata.terminals t2 ON t2.feature_class_id = bd.id AND t2.code = 'T2'
WHERE bd.code = 'LVAC_BOARD' AND lv.code = 'LV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = bd.id AND from_terminal_id = t2.id AND to_feature_class_id = lv.id
  );

-- ---------------------------------------------------------------------------
-- relationship_classes -- containment (spatial), metadata only, guarded on the
-- UNIQUE name via NOT EXISTS. The substation CONTAINS its aux transformer(s) and
-- its LVAC board(s); one station, many, so one_to_many. Orthogonal to the
-- electrical connectivity above (Phase 47 tenet: containment and connectivity
-- are independent graphs).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'SUBSTATION contains AUX_TRANSFORMER', sub.id, xf.id, 'containment', 'one_to_many'
FROM metadata.feature_classes sub, metadata.feature_classes xf
WHERE sub.code = 'SUBSTATION' AND xf.code = 'AUX_TRANSFORMER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = sub.id AND destination_feature_class_id = xf.id AND kind = 'containment'
  );

INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'SUBSTATION contains LVAC_BOARD', sub.id, bd.id, 'containment', 'one_to_many'
FROM metadata.feature_classes sub, metadata.feature_classes bd
WHERE sub.code = 'SUBSTATION' AND bd.code = 'LVAC_BOARD'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = sub.id AND destination_feature_class_id = bd.id AND kind = 'containment'
  );

-- ---------------------------------------------------------------------------
-- Symbology -- the mandatory default map rule (docs/metadata-core.md: every
-- class needs one subtype/domain-null rule before publish). A square for the aux
-- transformer, like the other transformers, but a distinct amber colour so the
-- station-service unit reads differently from the bulk (teal) and distribution
-- (blue) transformers. The LVAC board gets its own olive square. Diagram
-- stencils are a deliberate follow-up (same as POWER_XFMR Slice 1) -- until then
-- a placed feature uses the default diagram node.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "square", "color": "#b45309"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'AUX_TRANSFORMER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "square", "color": "#4d7c0f"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'LVAC_BOARD'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

-- ---------------------------------------------------------------------------
-- CIM/CDPSM export mapping (Phase 16) -- so these two classes are NOT silently
-- dropped from a CIM export. A station-service transformer is a CIM
-- PowerTransformer; the LVAC board is a CIM BusbarSection -- the same
-- conducting_junction kind DIST_XFMR / LV_BUSBAR use (see
-- seed/cim_mapping_example.sql). Class-default rows (subtype_id NULL), guarded
-- with NOT EXISTS because UNIQUE (feature_class_id, subtype_id) treats NULL
-- subtype_ids as DISTINCT so ON CONFLICT would never fire and would duplicate.
-- Depends on migration 0034 (cim_class_mappings); if that table is absent this
-- INSERT errors -- run it only on a DB with the CIM tables (the standard dev DB
-- has them). Harmless to re-run.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.cim_class_mappings (feature_class_id, subtype_id, cim_class, cim_kind)
SELECT fc.id, NULL, v.cim_class, v.cim_kind
FROM metadata.feature_classes fc
JOIN (VALUES
    ('AUX_TRANSFORMER', 'PowerTransformer', 'conducting_junction'),
    ('LVAC_BOARD',      'BusbarSection',    'conducting_junction')
) AS v(fc_code, cim_class, cim_kind) ON v.fc_code = fc.code
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.cim_class_mappings m
    WHERE m.feature_class_id = fc.id AND m.subtype_id IS NULL
);
