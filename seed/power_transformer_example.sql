-- seed/power_transformer_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- POWER_XFMR, Slice 1: a substation power transformer as a first-class junction
-- class, distinct from the pole/pad DIST_XFMR. Where DIST_XFMR steps MV down to
-- LV service, POWER_XFMR is the bulk transformer at a primary substation (e.g.
-- 33/11 kV, 132/33 kV) that can have two OR three windings.
--
-- WHY A SEPARATE CLASS, NOT A DIST_XFMR SUBTYPE: the winding count changes the
-- *terminals* (a three-winding unit has a real third connection point, the
-- tertiary), and the nameplate is quoted in MVA, not kVA -- structural
-- differences a subtype can't express. Modelled as its own class with the
-- winding count as the subtype axis.
--
-- FREE TWO-WINDING LOAD FLOW: api/internal/powerflow is field-name-driven, not
-- class-gated -- resolveTransformerModel reads primary_voltage / secondary_voltage
-- / impedance_pct / x_r_ratio by NAME off any junction node, and
-- resolveTransformerRating reads the nameplate rating. So by carrying those same
-- field names, POWER_XFMR is swept as a real two-winding, voltage-transforming,
-- impedance-dropping node with a loading %, with zero engine change beyond
-- teaching resolveTransformerRating to also read rated_mva (×1000 -> kVA). The
-- HV<->LV path is what the sweep models today; the tertiary is recorded now and
-- brought into load flow in Slice 2 (tertiary_voltage / z_hv_tv_pct / z_lv_tv_pct
-- are seeded here so the survey data exists, but the sweep ignores them for now).
--
-- SUBTYPE-SCOPED TERMINALS (new pattern here -- every prior seed left terminals
-- subtype-agnostic with subtype_id NULL): the two subtypes genuinely differ in
-- their connection points, so terminals are scoped to the subtype
-- (metadata.terminals.subtype_id, most-specific-wins in terminalsForSubtype):
--   * two_winding   -> HV (primary), LV (secondary), N (ground)
--   * three_winding -> HV (primary), LV (secondary), TV (tertiary), N (ground)
-- The tertiary role requires migrations/0070 (widened terminals.role CHECK) to
-- be applied first, or the TV terminal INSERT is rejected by the CHECK.
--
-- TAP CHANGER NOW DRIVES THE LOAD FLOW (Slice 3): tap_position / tap_step_pct
-- shift the effective LV turns ratio in the sweep (tapFactor in resolve.go,
-- 1 + position*step/100), so a tapped transformer's reported LV voltage tracks
-- the live tap. tap_min / tap_max stay validation-only (they bound the position
-- at data entry, the engine applies whatever is set). VECTOR GROUP REMAINS
-- RECORD-ONLY: the load flow is radial/positive-sequence, so a vector-group
-- phase shift has nothing to act on until a meshed/parallel study exists.
--
-- Depends on seed/connectivity_example.sql (MV_CONDUCTOR + VOLTAGE_CLASS /
-- CONDITION_RATING domains) and on migrations/0070. Reuses the 'electrical' and
-- 'maintenance' field_groups (re-asserted idempotently so this seed stands
-- alone). Extends VOLTAGE_CLASS with HV/EHV classes and adds a VECTOR_GROUP
-- domain.
--
-- Apply with psql after seed/connectivity_example.sql and migrations/0070, then
-- compile the class:
--   task seed-power-transformer   (or the psql -f invocation directly)
--   task compile CODE=POWER_XFMR
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + the compile + a load-flow
-- re-check across a POWER_XFMR are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('maintenance', 'Maintenance', 2)
ON CONFLICT (code) DO NOTHING;

-- Domains: reuse VOLTAGE_CLASS + CONDITION_RATING, add VECTOR_GROUP.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('VOLTAGE_CLASS', 'Voltage class', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded'),
    ('VECTOR_GROUP', 'Transformer vector group', 'coded')
ON CONFLICT (code) DO NOTHING;

-- Extend VOLTAGE_CLASS with the HV/EHV classes a power transformer's HV side
-- sits at. parseVoltageClassCode parses any NkV generically, so the engine
-- needs no change -- these just make the coded values selectable. 161 kV / 330
-- kV are the Ghanaian transmission voltages (GRIDCo); 66 kV / 132 kV are common
-- sub-transmission classes. sort_order continues after 11KV(1)/33KV(2).
INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('VOLTAGE_CLASS', '66KV',  '66 kV',  3),
    ('VOLTAGE_CLASS', '132KV', '132 kV', 4),
    ('VOLTAGE_CLASS', '161KV', '161 kV', 5),
    ('VOLTAGE_CLASS', '330KV', '330 kV', 6),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3),
    ('VECTOR_GROUP', 'DYN11', 'Dyn11', 1),
    ('VECTOR_GROUP', 'DYN1',  'Dyn1',  2),
    ('VECTOR_GROUP', 'YND11', 'YNd11', 3),
    ('VECTOR_GROUP', 'YND1',  'YNd1',  4),
    ('VECTOR_GROUP', 'YNYN0', 'YNyn0', 5),
    ('VECTOR_GROUP', 'DD0',   'Dd0',   6),
    ('VECTOR_GROUP', 'YY0',   'Yy0',   7)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- POWER_XFMR -- junction. geometry_optional + is_diagram_placeable (same
-- reasoning as the MV plant classes in seed/mv_switching_example.sql: a
-- substation transformer may be placed geographically, on a one-line diagram,
-- or both). Subtypes are the winding count -- the axis that changes the
-- terminals and the load-flow behaviour.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('POWER_XFMR', 'Power Transformer', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

-- subtypes.code is a small integer (Esri/G-Tech numeric subtype key,
-- migrations/0008); the machine-readable string lives in name. Two-winding = 1,
-- three-winding = 2.
INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES
    (1, 'two_winding'),
    (2, 'three_winding')
) AS v(code, name) ON true
WHERE fc.code = 'POWER_XFMR'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- Fields. primary_voltage is the one required electrical field (coded HV class);
-- every model/record field is nullable so an un-surveyed unit degrades
-- gracefully (the load-flow engine already treats absent secondary_voltage as
-- "pass-through", absent impedance_pct as "ideal", etc.).
--   Load-flow (HV<->LV, active this pass):
--     primary_voltage   coded VOLTAGE_CLASS  -- HV nominal (e.g. 132KV)
--     secondary_voltage numeric              -- LV nominal volts (e.g. 33000)
--     impedance_pct     numeric              -- nameplate %Z, HV-LV
--     x_r_ratio         numeric              -- X/R of that impedance
--     rated_mva         numeric              -- nameplate MVA (engine ->kVA)
--   Tertiary (recorded now, load flow in Slice 2):
--     tertiary_voltage  numeric              -- TV nominal volts
--     z_hv_tv_pct       numeric              -- %Z HV-TV
--     z_lv_tv_pct       numeric              -- %Z LV-TV
--   Tap changer (load flow, Slice 3 -- shifts effective LV ratio):
--     tap_position      numeric  -- current tap step (0 = nominal)
--     tap_step_pct      numeric  -- % voltage change per tap
--     tap_min           numeric  -- lowest tap position (validation only)
--     tap_max           numeric  -- highest tap position (validation only)
--   Record-only (no engine action yet):
--     vector_group      coded VECTOR_GROUP
--   Maintenance:
--     condition_rating  coded CONDITION_RATING
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('primary_voltage',   'text',    'VOLTAGE_CLASS',    'electrical',  false),
    ('secondary_voltage', 'numeric', NULL,               'electrical',  true),
    ('impedance_pct',     'numeric', NULL,               'electrical',  true),
    ('x_r_ratio',         'numeric', NULL,               'electrical',  true),
    ('rated_mva',         'numeric', NULL,               'electrical',  true),
    ('tertiary_voltage',  'numeric', NULL,               'electrical',  true),
    ('z_hv_tv_pct',       'numeric', NULL,               'electrical',  true),
    ('z_lv_tv_pct',       'numeric', NULL,               'electrical',  true),
    ('vector_group',      'text',    'VECTOR_GROUP',     'electrical',  true),
    ('tap_position',      'numeric', NULL,               'electrical',  true),
    ('tap_step_pct',      'numeric', NULL,               'electrical',  true),
    ('tap_min',           'numeric', NULL,               'electrical',  true),
    ('tap_max',           'numeric', NULL,               'electrical',  true),
    ('condition_rating',  'text',    'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'POWER_XFMR'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- Both subtypes require the same two fields at form level (primary_voltage +
-- condition_rating) -- the DIST_XFMR required set. The three-winding-specific
-- fields are left optional even on three_winding: a survey that hasn't captured
-- tertiary nameplate data must not be un-enterable.
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'POWER_XFMR')
  AND f.name IN ('primary_voltage', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- Subtype-scoped terminals. Each subtype's HV/LV/N (and TV on three_winding)
-- is its own row -- the UNIQUE (feature_class_id, subtype_id, code) key admits
-- the same code under different subtypes. TV requires migrations/0070.
INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT s.feature_class_id, s.id, v.code, v.role, v.phase_capable
FROM metadata.subtypes s
JOIN (VALUES
    ('two_winding',   'HV', 'primary',   true),
    ('two_winding',   'LV', 'secondary', true),
    ('two_winding',   'N',  'ground',    false),
    ('three_winding', 'HV', 'primary',   true),
    ('three_winding', 'LV', 'secondary', true),
    ('three_winding', 'TV', 'tertiary',  true),
    ('three_winding', 'N',  'ground',    false)
) AS v(subtype_name, code, role, phase_capable) ON v.subtype_name = s.name
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'POWER_XFMR')
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- network_rules -- the directed shape that lets a DOWNSTREAM trace and a load
-- flow walk HV -> transformer -> LV/TV:
--   MV_CONDUCTOR -> POWER_XFMR HV   (feeder into the HV winding; voltage-matched)
--   POWER_XFMR LV -> MV_CONDUCTOR   (LV winding out to conductor)
--   POWER_XFMR TV -> MV_CONDUCTOR   (tertiary winding out to conductor)
-- Because terminals are subtype-scoped, each rule generates one row per subtype
-- that has the terminal (HV/LV on both, TV on three_winding only) -- the join
-- to metadata.terminals by code does that automatically. No unique key on
-- network_rules, so each is guarded WHERE NOT EXISTS (same as every other seed).
-- The HV-in rule carries the voltage-match phase_rule DIST_XFMR uses; the
-- winding-out rules leave phase_rule NULL (a device hop, not a phase-matched
-- splice), matching the MV switching devices.
-- ---------------------------------------------------------------------------

-- Feeder into the HV winding (voltage-matched).
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> POWER_XFMR HV (' || s.name || ')', mv.id, xf.id, t.id,
       'from.voltage_class = to.primary_voltage', true
FROM metadata.feature_classes mv
JOIN metadata.feature_classes xf ON xf.code = 'POWER_XFMR'
JOIN metadata.subtypes s ON s.feature_class_id = xf.id
JOIN metadata.terminals t ON t.feature_class_id = xf.id AND t.subtype_id = s.id AND t.code = 'HV'
WHERE mv.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = xf.id AND to_terminal_id = t.id
  );

-- LV winding out to conductor (transformer as the FROM side, so DOWNSTREAM walks through).
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT 'POWER_XFMR LV (' || s.name || ') -> MV_CONDUCTOR', xf.id, t.id, mv.id, NULL, true
FROM metadata.feature_classes mv
JOIN metadata.feature_classes xf ON xf.code = 'POWER_XFMR'
JOIN metadata.subtypes s ON s.feature_class_id = xf.id
JOIN metadata.terminals t ON t.feature_class_id = xf.id AND t.subtype_id = s.id AND t.code = 'LV'
WHERE mv.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = xf.id AND from_terminal_id = t.id AND to_feature_class_id = mv.id
  );

-- Tertiary winding out to conductor (three_winding only -- the join to a TV
-- terminal simply finds no row for two_winding).
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, phase_rule, is_valid)
SELECT 'POWER_XFMR TV (' || s.name || ') -> MV_CONDUCTOR', xf.id, t.id, mv.id, NULL, true
FROM metadata.feature_classes mv
JOIN metadata.feature_classes xf ON xf.code = 'POWER_XFMR'
JOIN metadata.subtypes s ON s.feature_class_id = xf.id
JOIN metadata.terminals t ON t.feature_class_id = xf.id AND t.subtype_id = s.id AND t.code = 'TV'
WHERE mv.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = xf.id AND from_terminal_id = t.id AND to_feature_class_id = mv.id
  );

-- ---------------------------------------------------------------------------
-- Symbology -- the mandatory default map rule (docs/metadata-core.md: every
-- class needs one subtype/domain-null rule before publish). A square, like
-- DIST_XFMR, but a distinct colour (teal vs blue) so the bulk transformer reads
-- differently from the distribution one on the map. The ANSI two-/three-winding
-- one-line stencil (diagram render_context) is a deliberate follow-up, not part
-- of Slice 1 -- until then a placed POWER_XFMR uses the default diagram node.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "square", "color": "#0f766e"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'POWER_XFMR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );
