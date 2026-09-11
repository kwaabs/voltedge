-- seed/mv_conductor_specs.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files. Phase 28, Slice 1b:
-- give MV_CONDUCTOR real per-km impedance so the load-flow sweep computes actual
-- MV-side voltage drop and loading (docs/phase-28-mv-lv-load-flow.md), closing
-- the gap the base CIM seed already flags (cim_mapping_example.sql:113-115).
--
-- The gap is deeper than "MV has no material_specs rows": MV_CONDUCTOR today
-- carries ONLY voltage_class + condition_rating (connectivity_example.sql) --
-- no material-linked field at all, so resolveConductorImpedance finds nothing
-- and every MV branch drops zero. This seed adds the whole chain LV_CONDUCTOR
-- already has (seed/lv_conductors_example.sql:92-165):
--   1. metadata.material_specs -- real MV conductor catalog entries.
--   2. a coded, material-backed MV_LINE_SPEC domain.
--   3. a conductor_spec field on MV_CONDUCTOR keyed to that domain.
--   4. the ACLineSegment.r/.x CIM mapping for MV (mirrors LV).
--
-- Non-breaking choice: unlike LV_CONDUCTOR.conductor_spec (required on every
-- subtype), MV_CONDUCTOR.conductor_spec is added NULLABLE with NO
-- subtype_field_rules -- so existing MV conductor features stay valid and the
-- sweep degrades gracefully (an MV branch with conductor_spec unset drops zero,
-- same as before) rather than making every existing feature fail validation.
-- resolveConductorImpedance resolves via the domain->material_spec link, not the
-- field name, so it picks this up automatically once a value is set.
--
-- Depends on seed/connectivity_example.sql (MV_CONDUCTOR) and migration 0021
-- (material_specs + domain_values.material_spec_id). Reuses the 'electrical'
-- field_group idempotently so this seed stands alone.
--
-- Apply with psql, then recompile the touched class:
--   task seed-mv-conductor-specs   (or the psql -f invocation directly)
--   task compile CODE=MV_CONDUCTOR
--
-- Static-review note: shell here can't spawn (bash.exe EPERM); a live psql
-- apply + the compile + a load-flow re-check are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1)
ON CONFLICT (code) DO NOTHING;

-- 1. material_specs -- MV conductor catalog. Bare overhead ACSR/AAAC
-- (category 'conductor') carry the higher reactance (~0.35 ohm/km) of wide
-- phase spacing; MV XLPE cable (category 'cable') carries the much lower
-- reactance (~0.1 ohm/km) of close conductor geometry. Values are
-- planning-grade per-km figures at rated temperature; thermal columns
-- (migration 0022) are left NULL (no IEEE-738 ampacity in scope this phase).
-- resistance_zero_ohm_per_km / reactance_zero_ohm_per_km (migration 0071) are
-- illustrative planning-grade zero-sequence figures, so the ground-fault SLG calc
-- uses real r0/x0 instead of its 3×-positive-sequence fallback and reports the
-- result as non-estimated. Transparent rule of thumb applied here: overhead r0 =
-- r1 + 0.5 (an earth-return resistance adder), x0 = 3.3×x1 (the larger earth-
-- return loop); cable r0 = r1 + 0.3, x0 = 3.0×x1. Real survey figures replace
-- these per material.
INSERT INTO metadata.material_specs (code, name, category, resistance_ohm_per_km, reactance_ohm_per_km, resistance_zero_ohm_per_km, reactance_zero_ohm_per_km, ampacity_amps, diameter_mm) VALUES
    ('ACSR_50',     'ACSR 50mm² (overhead MV)',   'conductor', 0.5740, 0.3944, 1.0740, 1.3015, 170, 9.0),
    ('ACSR_100',    'ACSR 100mm² (overhead MV)',  'conductor', 0.3060, 0.3706, 0.8060, 1.2230, 270, 12.5),
    ('ACSR_150',    'ACSR 150mm² (overhead MV)',  'conductor', 0.2160, 0.3559, 0.7160, 1.1745, 350, 15.5),
    ('AAAC_100',    'AAAC 100mm² (overhead MV)',  'conductor', 0.3225, 0.3500, 0.8225, 1.1550, 285, 12.5),
    ('MV_XLPE_95',  'XLPE 95mm² (MV underground)','cable',     0.3200, 0.1200, 0.6200, 0.3600, 265, 28.0),
    ('MV_XLPE_240', 'XLPE 240mm² (MV underground)','cable',    0.1250, 0.1050, 0.4250, 0.3150, 445, 38.0)
ON CONFLICT (code) DO NOTHING;

-- Backfill the zero-sequence columns on a database where these six catalog rows
-- were already seeded before migration 0071 (the INSERT above does nothing on an
-- existing code). Idempotent -- sets each row to the same illustrative figures.
UPDATE metadata.material_specs AS ms
SET resistance_zero_ohm_per_km = v.r0,
    reactance_zero_ohm_per_km  = v.x0
FROM (VALUES
    ('ACSR_50',     1.0740, 1.3015),
    ('ACSR_100',    0.8060, 1.2230),
    ('ACSR_150',    0.7160, 1.1745),
    ('AAAC_100',    0.8225, 1.1550),
    ('MV_XLPE_95',  0.6200, 0.3600),
    ('MV_XLPE_240', 0.4250, 0.3150)
) AS v(code, r0, x0)
WHERE ms.code = v.code
  AND (ms.resistance_zero_ohm_per_km IS NULL OR ms.reactance_zero_ohm_per_km IS NULL);

-- 2. MV_LINE_SPEC -- the coded domain MV_CONDUCTOR.conductor_spec keys off.
-- Each value is material-backed: material_spec_id links it to the catalog row
-- above (value code = material_specs.code by convention), so selecting a
-- conductor size resolves straight to its numbers -- same pattern as
-- LV_CABLE_SPEC.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('MV_LINE_SPEC', 'MV conductor spec', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order, material_spec_id)
SELECT d.id, v.code, v.label, v.sort_order, ms.id
FROM metadata.domains d
JOIN (VALUES
    ('ACSR_50',     'ACSR 50mm²',      1),
    ('ACSR_100',    'ACSR 100mm²',     2),
    ('ACSR_150',    'ACSR 150mm²',     3),
    ('AAAC_100',    'AAAC 100mm²',     4),
    ('MV_XLPE_95',  'XLPE 95mm²',      5),
    ('MV_XLPE_240', 'XLPE 240mm²',     6)
) AS v(code, label, sort_order) ON true
LEFT JOIN metadata.material_specs ms ON ms.code = v.code
WHERE d.code = 'MV_LINE_SPEC'
ON CONFLICT (domain_id, code) DO NOTHING;

-- 3. conductor_spec on MV_CONDUCTOR -- nullable, no subtype rule (see header).
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'conductor_spec', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d ON d.code = 'MV_LINE_SPEC'
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'MV_CONDUCTOR'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- 4. CIM: MV_CONDUCTOR.conductor_spec -> ACLineSegment.r / .x (material_spec
-- transform), the same two rows LV_CONDUCTOR already has
-- (cim_mapping_example.sql:125-126). Closes that seed's documented MV-impedance
-- gap so an MV conductor exports real r/x, not just a length. Idempotent:
-- UNIQUE (field_id, cim_attribute) is fully non-NULL.
INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform, unit)
SELECT f.id, v.cim_attribute, 'material_spec', v.unit
FROM metadata.fields f
JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
JOIN (VALUES
    ('ACLineSegment.r', 'ohm'),
    ('ACLineSegment.x', 'ohm')
) AS v(cim_attribute, unit) ON true
WHERE fc.code = 'MV_CONDUCTOR' AND f.name = 'conductor_spec'
ON CONFLICT (field_id, cim_attribute) DO NOTHING;
