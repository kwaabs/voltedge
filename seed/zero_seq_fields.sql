-- seed/zero_seq_fields.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as seed/load_flow_fields.sql and
-- seed/transformer_model_fields.sql (which this extends). Ground-fault Slice 3:
-- the nullable numeric fields the single-line-to-ground calc reads for real
-- zero-sequence impedance, the zero-sequence counterparts of the positive-
-- sequence inputs those two seeds already added (api/internal/powerflow
-- resolve.go / groundfault.go read these field NAMES directly).
--
-- All NULLABLE with NO subtype rules, the same graceful-degradation discipline
-- every other engine input follows: an un-surveyed unit simply falls back to the
-- SLG calc's documented defaults (a transformer's Z0 taken equal to its
-- positive-sequence %Z, a source's Z0 equal to its positive-sequence impedance)
-- and the per-node result is flagged zero_seq_estimated = true. A network that
-- hasn't captured zero-sequence nameplate data must stay runnable.
--
--   * DIST_XFMR.z0_pct / POWER_XFMR.z0_pct -- nameplate zero-sequence impedance
--     as a percent on the unit's own rating base (rated_kva / rated_mva), the
--     zero-sequence counterpart of impedance_pct. Referred to the secondary and
--     split into R/X by the same x_r_ratio the positive-sequence %Z uses. Unset
--     => the SLG calc uses Z0 = Z1 (estimated).
--   * DIST_XFMR.source_r0_ohm / .source_x0_ohm -- the start node's own Thevenin
--     ZERO-sequence source impedance, read ONLY when an SLG run is started from
--     it, the zero-sequence counterpart of source_r_ohm / source_x_ohm. Placed on
--     DIST_XFMR to mirror where those positive-sequence source fields already
--     live; unset => the SLG calc uses the positive-sequence source impedance
--     (estimated).
--
-- Depends on the DIST_XFMR seed (seed/connectivity_example.sql), the POWER_XFMR
-- seed (seed/power_transformer_example.sql), and migration 0071 (the
-- material_specs zero-sequence columns the line side of this slice reads). Reuses
-- the 'electrical' field_group idempotently so this seed stands alone.
--
-- Apply with psql, then recompile the touched classes:
--   task seed-zero-seq-fields   (or the psql -f invocation directly)
--   task compile CODE=DIST_XFMR
--   task compile CODE=POWER_XFMR
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + the two compiles + an SLG
-- re-check with real z0 data are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1)
ON CONFLICT (code) DO NOTHING;

-- z0_pct on both transformer classes (rating base differs -- kVA vs MVA -- but
-- the field is a percent, so it's the same field on both).
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'z0_pct', 'numeric', NULL, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code IN ('DIST_XFMR', 'POWER_XFMR')
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- source_r0_ohm / source_x0_ohm on DIST_XFMR (start-only), mirroring where
-- source_r_ohm / source_x_ohm already sit (seed/load_flow_fields.sql).
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, NULL, fg.id, true
FROM metadata.feature_classes fc
JOIN (VALUES
    ('source_r0_ohm', 'numeric'),
    ('source_x0_ohm', 'numeric')
) AS v(name, data_type) ON true
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'DIST_XFMR'
ON CONFLICT (feature_class_id, name) DO NOTHING;
