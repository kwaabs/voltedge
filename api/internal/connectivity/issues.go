package connectivity

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// issues.go is Step 2 of incremental validation: it turns the connectivity audit's
// throwaway report (audit.go) into a PERSISTENT, SELF-PRUNING queue backed by
// ops.validation_issues (migration 0137). The two moving parts:
//
//   - ReconcileIssues upserts each finding by a stable fingerprint, so re-running the
//     audit over a feature does not duplicate its issue -- it updates it. And any
//     previously-open issue that TOUCHES the re-validated scope but is no longer in the
//     findings is AUTO-RESOLVED -- so when someone fixes a feature, its issue leaves the
//     queue with zero human action. That is what keeps the queue small at scale instead
//     of a 200k-finding pile.
//   - ListIssues / UpdateIssueStatus are the triage surface: read the live queue
//     (severity-sorted), and acknowledge / resolve / suppress one.

// ValidationIssue is one row of ops.validation_issues. The genuinely-nullable columns
// (a coincidence has no connection_id; an unfixed issue has no resolved_at) are
// pointers so a NULL scans cleanly; from_id/to_id are always written by reconcile.
type ValidationIssue struct {
	bun.BaseModel `bun:"table:ops.validation_issues"`

	ID             string     `bun:"id,pk" json:"id"`
	Fingerprint    string     `bun:"fingerprint" json:"-"`
	Kind           string     `bun:"kind" json:"kind"`
	Severity       string     `bun:"severity" json:"severity"`
	Status         string     `bun:"status" json:"status"`
	Message        string     `bun:"message" json:"message"`
	FromClass      string     `bun:"from_class" json:"from_class,omitempty"`
	FromID         string     `bun:"from_id" json:"from_id,omitempty"`
	ToClass        string     `bun:"to_class" json:"to_class,omitempty"`
	ToID           string     `bun:"to_id" json:"to_id,omitempty"`
	ConnectionID   *string    `bun:"connection_id" json:"connection_id,omitempty"`
	DistanceM      *float64   `bun:"distance_m" json:"distance_meters,omitempty"`
	TerritoryID    *string    `bun:"territory_id" json:"territory_id,omitempty"`
	FirstSeenAt    time.Time  `bun:"first_seen_at" json:"first_seen_at"`
	LastSeenAt     time.Time  `bun:"last_seen_at" json:"last_seen_at"`
	ResolvedAt     *time.Time `bun:"resolved_at" json:"resolved_at,omitempty"`
	AcknowledgedBy *string    `bun:"acknowledged_by" json:"acknowledged_by,omitempty"`
	AcknowledgedAt *time.Time `bun:"acknowledged_at" json:"acknowledged_at,omitempty"`
}

// fingerprintFor is the stable upsert key for a finding, so the same problem maps to
// the same row across runs. Orphans are keyed by the connectivity/relationship row id
// they carry; a coincidence (which carries no connection id) is keyed by its two
// feature ids sorted, so (A,B) and (B,A) are one issue.
func fingerprintFor(f AuditFinding) string {
	if f.Kind == "coincident_unconnected" {
		a, b := f.FromID, f.ToID
		if a > b {
			a, b = b, a
		}
		return "cu:" + a + "|" + b
	}
	return f.Kind + ":" + f.ConnectionID
}

// ReconcileResult reports what a reconcile did.
type ReconcileResult struct {
	Upserted           int  `json:"upserted"`
	Resolved           int  `json:"auto_resolved"`
	ScopedToFeatures   int  `json:"scoped_to_features"`
	AutoResolveSkipped bool `json:"auto_resolve_skipped"`
}

// ReconcileIssues persists an audit report into the issue queue: upsert every finding,
// then auto-resolve the open issues in the audited scope that no longer reproduce. It
// runs in one transaction so the queue never reflects a half-applied run.
//
// Auto-resolve is SKIPPED when the report was truncated (findings capped), because a
// missing fingerprint could mean "fixed" OR "fell off the capped list" -- resolving on
// a truncated run would wrongly close live issues. A SCOPED run (a handful of features)
// never truncates, so the incremental path always auto-resolves; a whole-DB run that
// hits the cap upserts what it saw and resolves nothing, which is the safe choice.
func ReconcileIssues(ctx context.Context, db *bun.DB, report *AuditReport, scope []string) (*ReconcileResult, error) {
	res := &ReconcileResult{ScopedToFeatures: len(scope)}

	const upsert = `
INSERT INTO ops.validation_issues
  (fingerprint, kind, severity, status, message, from_class, from_id, to_class, to_id, connection_id, distance_m)
VALUES (?, ?, ?, 'open', ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (fingerprint) DO UPDATE SET
  severity      = EXCLUDED.severity,
  message       = EXCLUDED.message,
  from_class    = EXCLUDED.from_class,
  from_id       = EXCLUDED.from_id,
  to_class      = EXCLUDED.to_class,
  to_id         = EXCLUDED.to_id,
  connection_id = EXCLUDED.connection_id,
  distance_m    = EXCLUDED.distance_m,
  last_seen_at  = now(),
  -- a resolved issue that reappears re-opens; acknowledged/suppressed are left as-is.
  status        = CASE WHEN ops.validation_issues.status = 'resolved' THEN 'open' ELSE ops.validation_issues.status END,
  resolved_at   = CASE WHEN ops.validation_issues.status = 'resolved' THEN NULL ELSE ops.validation_issues.resolved_at END,
  updated_at    = now()`

	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		fps := make([]string, 0, len(report.Findings))
		for _, f := range report.Findings {
			fp := fingerprintFor(f)
			fps = append(fps, fp)
			if _, err := tx.ExecContext(ctx, upsert,
				fp, f.Kind, f.Severity, f.Message,
				f.FromClass, nullUUID(f.FromID), f.ToClass, nullUUID(f.ToID),
				nullUUID(f.ConnectionID), f.DistanceMeters); err != nil {
				return fmt.Errorf("connectivity: reconcile upsert: %w", err)
			}
			res.Upserted++
		}

		if report.Summary.Truncated {
			res.AutoResolveSkipped = true
			return nil
		}

		// Open/acknowledged issues within the audited scope that are no longer in the
		// current findings are fixed -> resolve. A whole-DB run (empty scope) reconciles
		// the whole queue; a scoped run only its features' issues.
		q := `UPDATE ops.validation_issues SET status = 'resolved', resolved_at = now(), updated_at = now()
		      WHERE status IN ('open','acknowledged')`
		var args []any
		if len(scope) > 0 {
			q += " AND (from_id::text IN (?) OR to_id::text IN (?))"
			args = append(args, bun.In(scope), bun.In(scope))
		}
		if len(fps) > 0 {
			q += " AND fingerprint NOT IN (?)"
			args = append(args, bun.In(fps))
		}
		r, err := tx.ExecContext(ctx, q, args...)
		if err != nil {
			return fmt.Errorf("connectivity: reconcile auto-resolve: %w", err)
		}
		if n, err := r.RowsAffected(); err == nil {
			res.Resolved = int(n)
		}
		return nil
	})
	return res, err
}

// IssueFilter narrows ListIssues. An empty Status returns the LIVE queue
// (open + acknowledged); an explicit status returns just that one.
type IssueFilter struct {
	Status   string
	Severity string
	Kind     string
	Limit    int
}

// ListIssues returns the issue queue, most-recently-seen first, errors before
// warnings. The default (empty Status) is the live queue a triager works.
func ListIssues(ctx context.Context, db bun.IDB, f IssueFilter) ([]ValidationIssue, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	issues := []ValidationIssue{}
	q := db.NewSelect().Model(&issues)
	switch f.Status {
	case "":
		q = q.Where("status IN ('open','acknowledged')")
	case "open", "acknowledged", "resolved", "suppressed":
		q = q.Where("status = ?", f.Status)
	default:
		return nil, invalid("status must be one of open/acknowledged/resolved/suppressed")
	}
	if f.Severity == "error" || f.Severity == "warning" {
		q = q.Where("severity = ?", f.Severity)
	}
	if f.Kind != "" {
		q = q.Where("kind = ?", f.Kind)
	}
	// errors first, then most-recently-seen.
	q = q.OrderExpr("(severity = 'error') DESC").Order("last_seen_at DESC").Limit(limit)
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("connectivity: listing validation issues: %w", err)
	}
	return issues, nil
}

// UpdateIssueStatus is the triage action: move one issue to acknowledged / resolved /
// suppressed (or back to open), stamping who acknowledged it. A no-op update (unknown
// id) is a 404.
func UpdateIssueStatus(ctx context.Context, db bun.IDB, id, status, actor string) error {
	switch status {
	case "open", "acknowledged", "resolved", "suppressed":
	default:
		return invalid("status must be one of open/acknowledged/resolved/suppressed")
	}
	q := db.NewUpdate().Model((*ValidationIssue)(nil)).
		Where("id = ?", id).
		Set("status = ?", status).
		Set("updated_at = now()")
	switch status {
	case "acknowledged":
		q = q.Set("acknowledged_by = ?", nullUUID(actor)).Set("acknowledged_at = now()")
	case "resolved":
		q = q.Set("resolved_at = now()")
	case "open":
		q = q.Set("resolved_at = NULL")
	}
	r, err := q.Exec(ctx)
	if err != nil {
		return fmt.Errorf("connectivity: updating validation issue: %w", err)
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return notFound("validation issue %s not found", id)
	}
	return nil
}

// nullUUID returns nil for an empty id so it writes SQL NULL into a uuid column
// (an empty string would be an invalid uuid), else the id itself.
func nullUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}
