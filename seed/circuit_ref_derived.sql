-- seed/circuit_ref_derived.sql
-- Circuit-membership consolidation: make circuit_ref a DERIVED, read-only mirror
-- of the circuit's name instead of a hand-entered field.
--
-- Background: circuit_ref started life (Phase 35) as a hand-typed double-circuit
-- tag. Phase 37 added circuit_id (a proper catalog reference) + assign-by-trace,
-- which ALSO stamps circuit_ref = the circuit's name. That left two writers on
-- one column: the next assign-by-trace silently overwrites (or NULLs) any hand
-- value on the full-sync clear+stamp (api/internal/circuit/assign.go) -- latent
-- data loss. A double circuit is already told apart by its two runs having
-- distinct circuit_id, so the hand tag is redundant. The fix: single writer.
--
-- This flips the EXISTING circuit_ref field rows to derived-only:
--   * is_computed = true          -> CRUD rejects any direct set
--                                    (api/internal/crud/validate.go), while the
--                                    raw bulk write in assign.go is unaffected.
--   * visible_on_create = false   -> hidden from the create form
--   * visible_on_update = false   -> hidden from the edit form
-- The attribute panel still SHOWS the value (it renders stored attributes, not
-- form-visible ones) and the map label {circuit_ref} still renders it, so the
-- derived circuit name stays visible everywhere it was -- users just can't type
-- it. Fresh installs get this straight from seed/circuit_ref_example.sql's INSERT.
--
-- Metadata-only, no recompile needed (is_computed / visible_* never touch DDL --
-- the physical column already exists). Idempotent (re-running is a no-op once the
-- flags are set). Restart / reload the API so the metadata cache picks it up.
--   task seed-circuit-ref-derived
--
-- Depends on seed/circuit_ref_example.sql having created the field first.

UPDATE metadata.fields f
SET is_computed        = true,
    visible_on_create  = false,
    visible_on_update  = false
FROM metadata.feature_classes fc
WHERE f.feature_class_id = fc.id
  AND f.name = 'circuit_ref'
  AND fc.code IN ('MV_CONDUCTOR', 'LV_CONDUCTOR')
  AND (f.is_computed = false OR f.visible_on_create = true OR f.visible_on_update = true);
