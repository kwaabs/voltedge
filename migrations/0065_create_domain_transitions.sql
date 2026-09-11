-- 0065_create_domain_transitions.sql
-- Purpose: Phase 38 Slice 2 -- a coded domain's optional state machine. One row
-- = one legal edge: a field backed by `domain_id` may change its value from
-- `from_value_id` to `to_value_id`, and no other move between two set values is
-- allowed. This is what turns operational_state (PLANNED -> IN_SERVICE ->
-- ISOLATED -> ... -> RETIRED, docs/phase-38-operational-state.md) from a free
-- coded pick into an enforced lifecycle, without hardcoding one class's field
-- anywhere in Go.
--
-- Scoped to the DOMAIN, not a field or feature class, on purpose: the legal
-- transitions are a property of OPERATIONAL_STATE itself, so every class that
-- carries an operational_state field (23 of them, seed/operational_state_example
-- .sql) inherits the exact same graph from this one place. Any coded domain can
-- carry a transition graph the same way; almost none will.
--
-- Backward-compatible by absence: a domain with no rows here is unconstrained,
-- which is every existing coded domain (device_state, phase, ...). The CRUD
-- update path (api/internal/crud) only enforces edges when a domain actually has
-- some, so this table changes nothing until seed/operational_state_transitions
-- .sql populates the OPERATIONAL_STATE edges. Enforcement is update-only:
-- creating a feature declares an initial value (no "from"), and clearing a value
-- back to NULL un-declares it -- neither is a transition between two set states.

CREATE TABLE metadata.domain_transitions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    domain_id     uuid NOT NULL REFERENCES metadata.domains(id) ON DELETE CASCADE,
    from_value_id uuid NOT NULL REFERENCES metadata.domain_values(id) ON DELETE CASCADE,
    to_value_id   uuid NOT NULL REFERENCES metadata.domain_values(id) ON DELETE CASCADE,
    created_at    timestamptz NOT NULL DEFAULT now(),
    -- A self-edge (from = to) is meaningless: "no change" is always allowed by
    -- the engine regardless, so storing one would just be noise.
    CHECK (from_value_id <> to_value_id)
);

-- One edge per (domain, from, to) -- a duplicate row is a data-entry mistake,
-- not a second, distinct transition. Doubles as the ON CONFLICT target the seed
-- relies on to stay idempotent.
CREATE UNIQUE INDEX domain_transitions_edge_key
    ON metadata.domain_transitions (domain_id, from_value_id, to_value_id);

-- metadata.Load fetches every edge for a coded domain it touches, keyed by
-- domain (api/internal/metadata/load.go).
CREATE INDEX domain_transitions_domain_idx
    ON metadata.domain_transitions (domain_id);

COMMENT ON TABLE metadata.domain_transitions IS 'Optional state machine for a coded domain: one row per legal from->to edge. A domain with no rows is unconstrained. Enforced on feature update in api/internal/crud; wired for OPERATIONAL_STATE by Phase 38 Slice 2.';
