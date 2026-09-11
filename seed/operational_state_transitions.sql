-- seed/operational_state_transitions.sql
-- Phase 38 (operational lifecycle), Slice 2.
--
-- Turns the OPERATIONAL_STATE coded domain (seed/operational_state_example.sql)
-- into an ENFORCED state machine by authoring its legal transition edges into
-- metadata.domain_transitions (migration 0065). Once these rows exist, the CRUD
-- update path (api/internal/crud) rejects any operational_state change that
-- isn't one of these edges with a 400 -- on every class that carries the field,
-- and through both the REST PATCH and the MCP update_feature tool, because both
-- share crud.UpdateFeature.
--
-- The legal graph (docs/phase-38-operational-state.md):
--
--   PLANNED ─▶ IN_SERVICE ─▶ ISOLATED ─▶ IN_SERVICE   (re-energise after tag-out)
--                          │
--   IN_SERVICE / ISOLATED ─┴▶ RETIRED   (terminal -- no edge leaves RETIRED)
--   PLANNED ───────────────▶ RETIRED    (cancelled before it was ever built)
--
-- What's deliberately NOT an edge, and why the engine still allows it:
--   * no self-edges (PLANNED->PLANNED): "no change" is always fine, and a CHECK
--     on the table forbids storing one anyway;
--   * NULL -> any value: setting operational_state for the first time (an older
--     row, or a class that just gained the field) is a first declaration, not a
--     transition, so TransitionAllowed lets it through regardless of this graph;
--   * any value -> NULL: clearing the field un-declares it, also always allowed.
-- Enforcement only bites on a move between two *set* states.
--
-- Metadata-only, no compile step (these are pure lookup rows, no physical-table
-- footprint). Idempotent on the (domain, from, to) unique index:
--   task seed-operational-state-transitions
-- Safe to apply before or after the backfill; it constrains future edits only,
-- never rewrites existing values.

INSERT INTO metadata.domain_transitions (domain_id, from_value_id, to_value_id)
SELECT d.id, fv.id, tv.id
FROM metadata.domains d
JOIN metadata.domain_values fv ON fv.domain_id = d.id
JOIN metadata.domain_values tv ON tv.domain_id = d.id
JOIN (VALUES
    ('PLANNED',    'IN_SERVICE'),  -- commissioned into service
    ('PLANNED',    'RETIRED'),     -- cancelled before it was built
    ('IN_SERVICE', 'ISOLATED'),    -- tagged out / de-energised for work
    ('IN_SERVICE', 'RETIRED'),     -- decommissioned straight from service
    ('ISOLATED',   'IN_SERVICE'),  -- re-energised after the tag-out clears
    ('ISOLATED',   'RETIRED')      -- decommissioned while isolated
) AS e(from_code, to_code) ON fv.code = e.from_code AND tv.code = e.to_code
WHERE d.code = 'OPERATIONAL_STATE'
ON CONFLICT (domain_id, from_value_id, to_value_id) DO NOTHING;
