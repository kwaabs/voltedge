-- seed/substation_yard_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as seed/pole_example.sql.
-- Demo metadata for SUBSTATION -- a polygon feature class, the
-- first one in this repo. Every other seeded feature class so far is
-- point (POLE, DIST_XFMR) or line (MV_CONDUCTOR); this exists so the
-- map's draw tool (docs/phase-3-create-feature.md) has all three
-- geometry types to draw and test against. It is the top-level container
-- of a substation: SUBSTATION -> BAY -> plant (seed/bay_*), plus
-- SUBSTATION_PANEL (seed/substation_panel_*) on the control-building side.
--
-- NOTE: the class was renamed SUBSTATION_YARD -> SUBSTATION in Phase 47 Slice 2
-- (docs/phase-47-substation-site-model.md, migration 0085). This file keeps its
-- historical name so the seed history stays stable, but it now seeds code
-- 'SUBSTATION'. On a database that already carried yard rows, migration 0085 does
-- the data-preserving rename; this seed covers a fresh install.
--
-- Carries the yard's own identity: enclosure_type, condition_rating, and
-- (added 2026-09-10, the "finish don't overhaul" pass) the voltage
-- transformation primary_voltage / secondary_voltage -- what makes it a
-- "33/11 kV substation" rather than an anonymous fenced polygon.
--
-- Apply with psql after migrations 0001-0016 have run, e.g.:
--   task seed-substation-yard
-- then compile it (adds the primary_voltage/secondary_voltage columns on
-- an existing gis.substation -- additive, existing rows keep NULL):
--   task compile CODE=SUBSTATION

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('site', 'Site', 1)
ON CONFLICT (code) DO NOTHING;

-- VOLTAGE_CLASS is shared with BAY (seed/bay_example.sql) and the connectivity
-- seeds; re-asserted defensively here so this file stays self-contained and
-- order-independent (same pattern used for CONDITION_RATING). The extra levels
-- (400 V / 66 kV / 132 kV) are additive -- ON CONFLICT DO NOTHING never disturbs
-- the values another seed already inserted, it only fills in the missing ones.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('YARD_ENCLOSURE', 'Yard enclosure', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded'),
    ('VOLTAGE_CLASS', 'Voltage class', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('YARD_ENCLOSURE', 'FENCED', 'Fenced', 1),
    ('YARD_ENCLOSURE', 'WALLED', 'Walled', 2),
    ('YARD_ENCLOSURE', 'OPEN', 'Open', 3),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3),
    ('VOLTAGE_CLASS', '400V', '400 V', 0),
    ('VOLTAGE_CLASS', '11KV', '11 kV', 1),
    ('VOLTAGE_CLASS', '33KV', '33 kV', 2),
    ('VOLTAGE_CLASS', '66KV', '66 kV', 3),
    ('VOLTAGE_CLASS', '132KV', '132 kV', 4)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- network_role 'none' -- a substation yard is a site boundary, not a
-- network participant; nothing here connects through terminals the way
-- DIST_XFMR does.
--
-- Guarded so it fires ONLY when NEITHER code exists yet. A plain
-- `ON CONFLICT (code) DO NOTHING` on 'SUBSTATION' would still insert a SECOND,
-- empty class row when run against a not-yet-migrated database that still holds
-- the old 'SUBSTATION_YARD' row (the conflict target is 'SUBSTATION', which isn't
-- there yet). That duplicate then collides with migration 0085's rename. Skipping
-- when 'SUBSTATION_YARD' is present leaves the rename (migration 0085) as the sole
-- author of the transition on live DBs; on a fresh DB neither code exists, so this
-- inserts SUBSTATION cleanly. Migration 0085 also self-heals a duplicate that a
-- pre-guard seed run already created.
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
SELECT 'SUBSTATION', 'Substation', 'polygon', 4326, 'none', 'draft', false
WHERE NOT EXISTS (
    SELECT 1 FROM metadata.feature_classes WHERE code IN ('SUBSTATION', 'SUBSTATION_YARD')
);

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'distribution_substation'), (2, 'transmission_substation')) AS v(code, name) ON true
WHERE fc.code = 'SUBSTATION'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- primary_voltage / secondary_voltage record the substation's voltage
-- transformation -- its headline identity (a "33/11 kV substation"). Both draw
-- on the shared VOLTAGE_CLASS domain. primary_voltage is required (every
-- substation has an incoming voltage); secondary_voltage is nullable because a
-- pure switching substation transforms nothing (one voltage, no secondary).
-- Additive fields -- the compiler adds two nullable columns on recompile;
-- existing rows keep NULL and are unaffected (required is enforced on
-- create/update via subtype_field_rules, not as a DB NOT NULL).
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('enclosure_type', 'text', 'YARD_ENCLOSURE', 'site', false),
    ('condition_rating', 'text', 'CONDITION_RATING', 'site', false),
    ('primary_voltage', 'text', 'VOLTAGE_CLASS', 'site', false),
    ('secondary_voltage', 'text', 'VOLTAGE_CLASS', 'site', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'SUBSTATION'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'SUBSTATION')
  AND f.name IN ('enclosure_type', 'condition_rating', 'primary_voltage')
ON CONFLICT (subtype_id, field_id) DO NOTHING;
