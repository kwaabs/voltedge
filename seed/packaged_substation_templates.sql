-- seed/packaged_substation_templates.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files. Phase 56, Slice 2:
-- the per-configuration chain recipes behind the composite "place one packaged
-- substation and get its whole pre-wired kit" endpoint (api/internal/packagedsub).
-- Depends on migration 0092 (packaged_substation_templates / _items), on
-- seed/packaged_substation_example.sql (Slice 1: the PACKAGED_SUBSTATION class +
-- the PACKAGED_SUB_CONFIG-backed package_config field + the internal network
-- legs), on seed/mv_lv_switch_example.sql (MV_SWITCH), seed/connectivity_example
-- .sql (DIST_XFMR) and seed/lv_metering_example.sql (LV_BUSBAR) having been
-- applied and compiled.
--
-- A template is keyed by (packaged class, config). The config codes are the
-- PACKAGED_SUB_CONFIG values (COMPACT, ...) the package_config field points at.
-- One item = one internal component the recipe fans out, IN CHAIN ORDER (unlike
-- the RMU's star, where every way wires to one shared bus). Each item past the
-- first wires to the PREVIOUS item: connectivity(prev.upstream_terminal <->
-- this.own_terminal). Default coded attribute values give each fanned-out
-- component what its required fields need so create-time validation passes; the
-- crew edits the real values (voltage, phase, actual state) afterward.
--
-- The COMPACT chain (a classic secondary substation):
--   pos 1  MV_SWITCH  (subtype 1 standard)          -- the MV incomer/ring switch
--   pos 2  DIST_XFMR  (subtype 2 pad_mounted)        -- wired MV_SWITCH.T2 <-> its T1
--   pos 3  LV_BUSBAR  (subtype 1 lv_distribution_board) -- wired DIST_XFMR.T2 <-> its T1
-- so a downstream trace walks (external MV cable) -> switch -> transformer -> LV
-- board -> (external LV feeders), and an OPEN MV switch halts it at the switch.
--
-- offset_x / offset_y stack the three down the future mimic (cosmetic only, not
-- electrical data). Default attributes are the required-field minimums for each
-- component's class: MV_SWITCH device_state=OPEN + condition_rating=GOOD +
-- is_fused=false; DIST_XFMR primary_voltage=11KV + condition_rating=GOOD;
-- LV_BUSBAR phase=L1L2L3N -- all known domain values (VOLTAGE_CLASS / PHASE /
-- CONDITION_RATING / DEVICE_STATE).
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL is
-- reviewed statically -- a live psql apply + a build-a-unit call through
-- POST /packaged-substations are still owed to call it verified.

-- ---------------------------------------------------------------------------
-- Templates: one row per config for the PACKAGED_SUBSTATION class. Idempotent
-- on the (packaged class, config) unique key.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.packaged_substation_templates (packaged_feature_class_id, config_value_id, name)
SELECT pfc.id, cv.id, v.name
FROM metadata.feature_classes pfc
JOIN (VALUES
    ('COMPACT', 'Compact secondary substation (MV switch + transformer + LV board)')
) AS v(config_code, name) ON true
JOIN metadata.domains cd ON cd.code = 'PACKAGED_SUB_CONFIG'
JOIN metadata.domain_values cv ON cv.domain_id = cd.id AND cv.code = v.config_code
WHERE pfc.code = 'PACKAGED_SUBSTATION'
ON CONFLICT (packaged_feature_class_id, config_value_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Items: one row per component per config, in chain order. component_code names
-- the class; subtype_code its subtype; own_terminal / upstream_terminal are the
-- two ends of the leg wiring this component to the previous one (both NULL for
-- position 1 -- the incomer has no upstream). attributes are built per-component
-- below by a CASE on component_code so each carries exactly its required-field
-- defaults. Idempotent on the (template, position) unique key.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.packaged_substation_items
    (template_id, component_feature_class_id, subtype_code, attributes, own_terminal, upstream_terminal, position, offset_x, offset_y, rotation)
SELECT pt.id,
       comp.id,
       v.subtype_code,
       CASE v.component_code
         WHEN 'MV_SWITCH' THEN jsonb_build_object('device_state', 'OPEN', 'condition_rating', 'GOOD', 'is_fused', false)
         WHEN 'DIST_XFMR' THEN jsonb_build_object('primary_voltage', '11KV', 'condition_rating', 'GOOD')
         WHEN 'LV_BUSBAR' THEN jsonb_build_object('phase', 'L1L2L3N')
         ELSE '{}'::jsonb
       END,
       v.own_terminal,
       v.upstream_terminal,
       v.position, v.offset_x, v.offset_y, 0
FROM metadata.feature_classes pfc
JOIN (VALUES
    -- config,   component,     subtype, own,  upstream, position, off_x, off_y
    ('COMPACT', 'MV_SWITCH',   1,       NULL, NULL, 1,   0.0,   0.0),
    ('COMPACT', 'DIST_XFMR',   2,       'T1', 'T2', 2,   0.0,  80.0),
    ('COMPACT', 'LV_BUSBAR',   1,       'T1', 'T2', 3,   0.0, 160.0)
) AS v(config_code, component_code, subtype_code, own_terminal, upstream_terminal, position, offset_x, offset_y) ON true
JOIN metadata.domains cd ON cd.code = 'PACKAGED_SUB_CONFIG'
JOIN metadata.domain_values cv ON cv.domain_id = cd.id AND cv.code = v.config_code
JOIN metadata.packaged_substation_templates pt ON pt.packaged_feature_class_id = pfc.id AND pt.config_value_id = cv.id
JOIN metadata.feature_classes comp ON comp.code = v.component_code
WHERE pfc.code = 'PACKAGED_SUBSTATION'
ON CONFLICT (template_id, position) DO NOTHING;
