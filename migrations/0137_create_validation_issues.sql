-- 0137_create_validation_issues.sql
-- Purpose: the persistent, SELF-PRUNING data-quality issue queue behind incremental
-- validation. The connectivity audit (api/internal/connectivity/audit.go) finds
-- divergences -- orphaned connectivity, coincident-but-unconnected features, orphaned
-- relationships. Until now it produced a THROWAWAY report, regenerated whole each run;
-- at scale (hundreds of editors over days) a whole-database report is an unreviewable
-- pile ("200k changes, who audits them?").
--
-- This table turns that report into a MANAGED QUEUE. Each finding is upserted by a stable
-- `fingerprint`, so re-validating a feature RE-OPENS or UPDATES its issue instead of
-- duplicating it; and a finding that no longer reproduces within a re-validated scope is
-- AUTO-RESOLVED -- so a fixed problem leaves the queue with zero human action, and only
-- live issues persist. A human then triages a small, severity-sorted list of OPEN issues,
-- not a flood of raw changes. Reconcile + list logic in api/internal/connectivity/issues.go.

CREATE TABLE ops.validation_issues (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Stable identity of the finding, so a re-run upserts the SAME row rather than
    -- duplicating. Derived from the finding kind + the thing it describes: the
    -- connectivity/relationship row id for an orphan, the sorted feature-id pair for a
    -- coincidence. UNIQUE -- this is the upsert key.
    fingerprint     text NOT NULL UNIQUE,

    kind            text NOT NULL,   -- orphaned_connectivity | coincident_unconnected | orphaned_relationship
    severity        text NOT NULL CHECK (severity IN ('error','warning')),

    -- Lifecycle: open (live, needs attention) -> acknowledged (someone is on it) -> resolved
    -- (fixed; set by auto-resolve or by hand); or suppressed (an accepted / wont-fix finding
    -- that must NOT re-surface or be auto-touched). Auto-resolve only ever moves open/
    -- acknowledged -> resolved; it never disturbs suppressed.
    status          text NOT NULL DEFAULT 'open'
        CHECK (status IN ('open','acknowledged','resolved','suppressed')),

    message         text NOT NULL,

    -- The two features (or feature + connection) involved -- the same shape the audit
    -- finding carries, so a client can locate and fix the problem.
    from_class      text,
    from_id         uuid,
    to_class        text,
    to_id           uuid,
    connection_id   uuid,             -- the orphaned connectivity/relationship row id, when applicable
    distance_m      double precision, -- the coincidence distance in metres, when applicable

    -- For routing the queue to the owning team. Best-effort; may be null (populated by a
    -- later slice that derives it from an involved feature's territory).
    territory_id    uuid,

    first_seen_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    resolved_at     timestamptz,
    acknowledged_by uuid,
    acknowledged_at timestamptz,

    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE ops.validation_issues IS 'Self-pruning data-quality issue queue (api/internal/connectivity/issues.go). Each connectivity-audit finding is upserted by a stable fingerprint; re-validating a scope auto-resolves findings that no longer reproduce, so fixed problems leave the queue without human action. Turns the throwaway whole-DB audit report into a managed, severity-sorted triage queue.';
COMMENT ON COLUMN ops.validation_issues.fingerprint IS 'Stable upsert key: kind + the finding''s identity (orphan connection/relationship row id, or the sorted feature-id pair for a coincidence). Re-runs upsert the same row.';
COMMENT ON COLUMN ops.validation_issues.status IS 'open -> acknowledged -> resolved, or suppressed. Auto-resolve moves only open/acknowledged -> resolved; suppressed is never auto-touched.';

-- The triage queue: live issues only, errors first, most-recent first.
CREATE INDEX validation_issues_open_idx
    ON ops.validation_issues (severity, last_seen_at DESC)
    WHERE status IN ('open','acknowledged');

-- Auto-resolve/scope lookups match a re-validated feature set against an endpoint id.
CREATE INDEX validation_issues_from_open_idx
    ON ops.validation_issues (from_id)
    WHERE status IN ('open','acknowledged');
CREATE INDEX validation_issues_to_open_idx
    ON ops.validation_issues (to_id)
    WHERE status IN ('open','acknowledged');
