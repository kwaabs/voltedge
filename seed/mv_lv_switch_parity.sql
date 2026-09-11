-- seed/mv_lv_switch_parity.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files.
--
-- Brings the two generic switches (seed/mv_lv_switch_example.sql: MV_SWITCH,
-- LV_SWITCH) up to the FULL cross-cutting footprint DIST_SWITCH already carries.
-- Every one of these is seeded elsewhere with `WHERE fc.code = 'DIST_SWITCH'`, so
-- none of it reaches the successor classes automatically -- and TWO of them
-- (circuit_id, operational_state) are physical columns whose absence would
-- silently DROP membership + lifecycle data when DIST_SWITCH features are
-- migrated into gis.mv_switch. This file must therefore be applied AND both
-- classes recompiled BEFORE the destructive DIST_SWITCH -> MV_SWITCH migration.
--
-- Seven parity concerns, each mirroring its source seed exactly:
--   1. network stamp ('MV'/'LV') + circuit_id field  (seed/circuit_membership_example.sql)
--   2. operational_state lifecycle field             (seed/operational_state_example.sql)
--   3. snapping rule: conductor -> switch            (seed/snapping_rules_example.sql rule 2)
--   4. protection_trip: PROTECTIVE_RELAY trips switch (seed/mv_relay_example.sql)
--   5. map symbology: default + device_state=OPEN     (seed/symbology_example.sql)
--   6. diagram symbology: ansi-disconnect glyph        (seed/diagram_symbology.sql)
--   7. CIM class + Switch.normalOpen attribute mapping (seed/cim_mapping_example.sql)
--
-- The recloser has no parity here on purpose: DIST_SWITCH's automatic_recloser
-- subtype is being dropped, not migrated (a real MV_RECLOSER class is deferred to
-- its own future phase), so none of the recloser-scoped rows (CIM Recloser
-- override, ansi-load-break diagram override) are reproduced.
--
-- LV note: protection_trip is MV-only (a PROTECTIVE_RELAY protects the MV feeder,
-- so it trips MV_SWITCH, never LV_SWITCH) -- matching DIST_SWITCH, which was
-- MV-side. circuit_id/operational_state/snapping/symbology/CIM apply to both.
--
-- Every statement is idempotent (ON CONFLICT / NOT EXISTS) and every JOIN finds
-- nothing (inserts nothing) if a dependency class/domain/glyph is absent, so this
-- is safe to run repeatedly and in any order relative to the base seeds -- though
-- for the full effect it wants, applied AFTER: seed/mv_lv_switch_example.sql,
-- seed/circuit_membership_example.sql (network col migration 0064 + circuit_id
-- machinery), seed/operational_state_example.sql (OPERATIONAL_STATE domain +
-- 'operational' group), seed/snapping_rules_example.sql (SNAP_TARGET_TYPE
-- domain), seed/mv_relay_example.sql (PROTECTIVE_RELAY + migration 0075 kind
-- CHECK), seed/ansi_stencils.sql (ansi-disconnect glyph), and
-- seed/cim_mapping_example.sql (cim_profiles).
--
-- Recompile BOTH switches after applying so the two new physical columns land
-- (adding a nullable column is non-destructive ALTER TABLE ADD COLUMN):
--   task seed-mv-lv-switch-parity
--   task compile CODE=MV_SWITCH
--   task compile CODE=LV_SWITCH
-- (snapping_rules / relationship_classes / symbology_rules / cim_* rows have no
-- physical footprint -- nothing to compile for those.)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + the two `task compile` runs are
-- still owed to call it verified.

-- ===========================================================================
-- 1. Circuit membership -- network stamp + circuit_id field.
--    (seed/circuit_membership_example.sql: MV_SWITCH is MV like DIST_SWITCH was;
--    LV_SWITCH is LV. The circuit_id field mirrors that seed's `WHERE network IS
--    NOT NULL` add, but scoped explicitly to the two switches for clarity.)
-- ===========================================================================
UPDATE metadata.feature_classes SET network = 'MV' WHERE code = 'MV_SWITCH';
UPDATE metadata.feature_classes SET network = 'LV' WHERE code = 'LV_SWITCH';

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'circuit_id', 'uuid', NULL, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code IN ('MV_SWITCH', 'LV_SWITCH')
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- ===========================================================================
-- 2. operational_state lifecycle field (nullable text, coded OPERATIONAL_STATE,
--    'operational' group) -- mirrors seed/operational_state_example.sql's field
--    add. No subtype_field_rules row (lifecycle is optional; "born PLANNED" is a
--    form default, not a DB constraint). A no-op if that seed's domain/group
--    aren't present yet.
-- ===========================================================================
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'operational_state', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d       ON d.code  = 'OPERATIONAL_STATE'
JOIN metadata.field_groups fg ON fg.code = 'operational'
WHERE fc.code IN ('MV_SWITCH', 'LV_SWITCH')
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- ===========================================================================
-- 3. Snapping: digitizing a conductor, snap to its switch (either side -- no
--    specific terminal, picked at connect time). Mirrors rule 2 of
--    seed/snapping_rules_example.sql (MV_CONDUCTOR -> DIST_SWITCH): point_feature,
--    tolerance 0.5m, priority 1, optional. One INSERT per (conductor, switch)
--    pair; guarded WHERE NOT EXISTS keyed on from/to class + NULL terminal +
--    target type, exactly as that seed does. A no-op if SNAP_TARGET_TYPE isn't
--    seeded yet.
-- ===========================================================================
INSERT INTO metadata.snapping_rules (from_feature_class_id, to_feature_class_id, to_terminal_id, target_type_value_id, tolerance_m, priority, is_required)
SELECT frm.id, tgt.id, NULL, tt.id, 0.5, 1, false
FROM (VALUES ('MV_CONDUCTOR', 'MV_SWITCH'), ('LV_CONDUCTOR', 'LV_SWITCH')) AS pair(cond_code, switch_code)
JOIN metadata.feature_classes frm ON frm.code = pair.cond_code
JOIN metadata.feature_classes tgt ON tgt.code = pair.switch_code
JOIN metadata.domain_values tt ON tt.code = 'point_feature'
  AND tt.domain_id = (SELECT id FROM metadata.domains WHERE code = 'SNAP_TARGET_TYPE')
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.snapping_rules sr
    WHERE sr.from_feature_class_id = frm.id AND sr.to_feature_class_id = tgt.id
      AND sr.to_terminal_id IS NULL
      AND sr.target_type_value_id = tt.id
  );

-- ===========================================================================
-- 4. protection_trip -- PROTECTIVE_RELAY operates MV_SWITCH (the non-fused,
--    relay-tripped, breaker-like case; a fused switch grades off its own
--    curve_ref, no relay). Mirrors seed/mv_relay_example.sql's DIST_SWITCH row.
--    MV_SWITCH only -- LV has no relay protection. A no-op if PROTECTIVE_RELAY
--    isn't seeded (or migration 0075's kind CHECK isn't applied -- then the
--    INSERT would fail the CHECK, same precondition mv_relay_example.sql states).
-- ===========================================================================
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'PROTECTIVE_RELAY trips ' || dev.code, relay.id, dev.id, 'protection_trip', 'many_to_many'
FROM metadata.feature_classes relay
JOIN metadata.feature_classes dev ON dev.code = 'MV_SWITCH'
WHERE relay.code = 'PROTECTIVE_RELAY'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = relay.id AND destination_feature_class_id = dev.id AND kind = 'protection_trip'
  );

-- ===========================================================================
-- 5. Map symbology -- mandatory default (solid triangle) + a device_state=OPEN
--    override (hollow triangle, red). Mirrors seed/symbology_example.sql's
--    DIST_SWITCH pair. MV_SWITCH keeps DIST_SWITCH's green (#16a34a) so migrated
--    features look identical; LV_SWITCH gets a distinct teal (#0d9488). OPEN is
--    red for both (the strong "abnormal state" convention).
-- ===========================================================================
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', v.style::jsonb
FROM (VALUES
    ('MV_SWITCH', '{"icon_ref": "triangle", "color": "#16a34a"}'),
    ('LV_SWITCH', '{"icon_ref": "triangle", "color": "#0d9488"}')
) AS v(code, style)
JOIN metadata.feature_classes fc ON fc.code = v.code
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );

INSERT INTO metadata.symbology_rules (feature_class_id, domain_value_id, render_context, style_def)
SELECT sw.id, dv.id, 'map', '{"icon_ref": "triangle-outline", "color": "#dc2626"}'::jsonb
FROM metadata.feature_classes sw
JOIN metadata.fields f ON f.feature_class_id = sw.id AND f.name = 'device_state'
JOIN metadata.domain_values dv ON dv.domain_id = f.domain_id AND dv.code = 'OPEN'
WHERE sw.code IN ('MV_SWITCH', 'LV_SWITCH')
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = sw.id AND subtype_id IS NULL AND domain_value_id = dv.id AND render_context = 'map'
  );

-- ===========================================================================
-- 6. Diagram symbology -- ansi-disconnect glyph, one 'diagram' rule per switch.
--    Mirrors seed/diagram_symbology.sql's DIST_SWITCH default (each keeps its map
--    colour). A no-op if the ansi-disconnect glyph (seed/ansi_stencils.sql) isn't
--    present -- the rule still inserts (icon_ref is just a string), it simply
--    renders nothing until the glyph exists, same as any diagram rule.
-- ===========================================================================
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', v.style::jsonb
FROM (VALUES
    ('MV_SWITCH', '{"icon_ref": "ansi-disconnect", "color": "#16a34a"}'),
    ('LV_SWITCH', '{"icon_ref": "ansi-disconnect", "color": "#0d9488"}')
) AS v(code, style)
JOIN metadata.feature_classes fc ON fc.code = v.code
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );

-- ===========================================================================
-- 7. CIM mapping -- class default (LoadBreakSwitch, conducting_junction) + the
--    Switch.normalOpen boolean attribute from device_state. Mirrors
--    seed/cim_mapping_example.sql's DIST_SWITCH rows (minus the recloser subtype
--    override). A no-op if the CIM tables/profile aren't seeded.
-- ===========================================================================
INSERT INTO metadata.cim_class_mappings (feature_class_id, subtype_id, cim_class, cim_kind)
SELECT fc.id, NULL, 'LoadBreakSwitch', 'conducting_junction'
FROM metadata.feature_classes fc
WHERE fc.code IN ('MV_SWITCH', 'LV_SWITCH')
  AND NOT EXISTS (
    SELECT 1 FROM metadata.cim_class_mappings m
    WHERE m.feature_class_id = fc.id AND m.subtype_id IS NULL
  );

INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform, value_map, unit)
SELECT f.id, 'Switch.normalOpen', 'boolean', '{"OPEN":true,"CLOSED":false}'::jsonb, NULL
FROM metadata.fields f
JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
WHERE fc.code IN ('MV_SWITCH', 'LV_SWITCH') AND f.name = 'device_state'
ON CONFLICT (field_id, cim_attribute) DO NOTHING;
