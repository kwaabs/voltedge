-- seed/phase_model_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase 9, Slice 0 (the phase model). This is the *data-model* half of
-- Slice 0: the PHASE coded domain the system has never had, plus a
-- nullable `phase` field on the phase-bearing classes that exist today
-- (MV_CONDUCTOR, DIST_XFMR). The *engine* half -- teaching the
-- connect/trace matcher to actually evaluate network_rules.phase_rule as
-- a phase-set intersection -- is a separate change in
-- api/internal/connectivity (see docs/phase-9-lv-service-delivery.md's
-- Slice 0 detail). Nothing here evaluates a phase rule yet; these rows
-- only make phase *expressible* and visible in the schema/CRUD.
--
-- Why nullable and additive, not required: adding a required field to an
-- already-compiled class with existing feature rows would fail the CRUD
-- completeness gate and demand a recompile + backfill (and the compiler
-- has no field-drop/retype path -- the standing open item). A nullable
-- coded field is a purely additive column: existing MV_CONDUCTOR /
-- DIST_XFMR rows stay valid, and the field can be tightened to required
-- later once real phased data is backfilled. So this seed is safe to
-- apply on top of an existing connectivity seed.
--
-- Depends on seed/connectivity_example.sql (MV_CONDUCTOR, DIST_XFMR, and
-- the 'electrical' field_group) having been applied. Re-compile both
-- classes afterward so the new nullable column reaches the physical
-- tables:
--   task seed-phase-model   (or the psql -f invocation directly)
--   task compile CODE=MV_CONDUCTOR
--   task compile CODE=DIST_XFMR
--
-- Static-review note: as with every artifact in this repo right now, the
-- shell here can't spawn (bash.exe EPERM), so this SQL has been reviewed
-- statically -- a live psql apply + `task compile` + a schema check are
-- still owed before it can be called verified.

-- PHASE -- the coded domain the phase model hangs off. Values cover the
-- three phases and neutral as bare conductors/terminals carry them
-- (L1/L2/L3/N), the single-phase-plus-neutral connections a real service
-- takes (L1N/L2N/L3N), and the full three-phase four-wire set an LV main
-- or an MV run carries (L1L2L3N). This is an ordinary coded domain --
-- authorable through the domains admin page, no new mechanism -- so the
-- exact value set stays easy to extend (e.g. a phase-to-phase 2-wire
-- L1L2) without a schema change.

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('PHASE', 'Phase', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('PHASE', 'L1',      'L1',                     1),
    ('PHASE', 'L2',      'L2',                     2),
    ('PHASE', 'L3',      'L3',                     3),
    ('PHASE', 'N',       'Neutral',                4),
    ('PHASE', 'L1N',     'L1 + N (1-phase)',       5),
    ('PHASE', 'L2N',     'L2 + N (1-phase)',       6),
    ('PHASE', 'L3N',     'L3 + N (1-phase)',       7),
    ('PHASE', 'L1L2L3N', 'L1 L2 L3 + N (3-phase)', 8)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- The 'electrical' field_group is normally created by
-- seed/connectivity_example.sql; re-assert it idempotently so this seed
-- is self-sufficient if applied in isolation.
INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1)
ON CONFLICT (code) DO NOTHING;

-- A nullable `phase` field on each phase-bearing class that exists today.
-- Coded field: data_type 'text' + a PHASE domain_id, exactly like the
-- existing voltage_class/condition_rating fields -- the compiler and CRUD
-- engine need nothing new. MV_CONDUCTOR (the edge) and DIST_XFMR (the
-- junction) are the only phase-bearing classes seeded so far; the LV
-- conductors/meters that also carry phase arrive in later slices and get
-- the same field there.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'phase', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d ON d.code = 'PHASE'
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code IN ('MV_CONDUCTOR', 'DIST_XFMR')
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- No subtype_field_rules row: the field is left globally nullable rather
-- than pinned required for any subtype, matching the additive intent
-- above. Requiredness is a later tightening once phased data exists.
