// Package alarms is the operator ALARM ANNUNCIATOR -- a prioritised, acknowledgeable
// worklist of conditions needing attention (ops.alarms, migration 0198). It is the
// ADMS control-room discipline the event logs around it lack: a trip/lockout in the
// device-operation log is HISTORY; an off-normal device is a report; an alarm is a
// thing an operator must SEE and ACKNOWLEDGE, ranked by severity and self-pruning.
//
// Two shapes share the store (see the migration): EVENT alarms (raised active=false,
// cleared by acknowledgement) and STANDING alarms (active while the condition holds,
// cleared by a reconcile). The annunciator (List with an empty Status) shows an alarm
// while it is active OR unacknowledged and not shelved -- so a transient event is not
// missed. Sources raise through Raise / ReconcileStanding; operators Acknowledge /
// Shelve. The store is source-agnostic: deviceops wires the device-op + abnormal-config
// feeds (it imports this package, not the reverse).
package alarms

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// Severities, in rank order (critical highest). The CHECK in migration 0198 mirrors these.
const (
	SeverityCritical = "critical"
	SeverityHigh     = "high"
	SeverityMedium   = "medium"
	SeverityLow      = "low"
)

// Alarm is one row of ops.alarms. AcknowledgedByName is scan-only, filled by the
// list read's LEFT JOIN so the annunciator shows "ack'd by Ama" without admin:users.
type Alarm struct {
	bun.BaseModel `bun:"table:ops.alarms,alias:al"`

	ID          string `bun:"id,pk" json:"id"`
	Fingerprint string `bun:"fingerprint" json:"-"`
	Source      string `bun:"source" json:"source"`
	Category    string `bun:"category" json:"category"`
	Severity    string `bun:"severity" json:"severity"`
	Message     string `bun:"message" json:"message"`

	DeviceClass *string `bun:"device_class" json:"device_class,omitempty"`
	DeviceID    *string `bun:"device_id" json:"device_id,omitempty"`

	Active          bool       `bun:"active" json:"active"`
	OccurrenceCount int        `bun:"occurrence_count" json:"occurrence_count"`
	RaisedAt        time.Time  `bun:"raised_at" json:"raised_at"`
	LastRaisedAt    time.Time  `bun:"last_raised_at" json:"last_raised_at"`
	ClearedAt       *time.Time `bun:"cleared_at" json:"cleared_at,omitempty"`
	AcknowledgedAt  *time.Time `bun:"acknowledged_at" json:"acknowledged_at,omitempty"`
	AcknowledgedBy  *string    `bun:"acknowledged_by" json:"acknowledged_by,omitempty"`
	ShelvedUntil    *time.Time `bun:"shelved_until" json:"shelved_until,omitempty"`
	ShelveReason    *string    `bun:"shelve_reason" json:"shelve_reason,omitempty"`
	ShelvedBy       *string    `bun:"shelved_by" json:"shelved_by,omitempty"`

	// created_at/updated_at exist on ops.alarms (migration 0198). They must have
	// fields here because the list read selects `al.*`, and bun errors on a returned
	// column with no destination (no WithDiscardUnknownColumns is configured) -- their
	// absence is what 500'd GET /alarms.
	CreatedAt time.Time `bun:"created_at" json:"-"`
	UpdatedAt time.Time `bun:"updated_at" json:"-"`

	AcknowledgedByName *string `bun:"acknowledged_by_name,scanonly" json:"acknowledged_by_name,omitempty"`
}

// RaiseInput is one alarm to raise or update. Standing=true marks a condition that
// persists (active); false is a point-in-time event (shown until acknowledged).
type RaiseInput struct {
	Fingerprint string
	Source      string
	Category    string
	Severity    string
	Message     string
	DeviceClass string
	DeviceID    string
	Standing    bool
}

// Raise upserts one alarm by fingerprint: a new condition inserts, a recurrence
// bumps the count and -- if the condition had cleared since -- re-alarms (clears the
// prior acknowledgement). Used for one-off EVENT alarms; standing feeds use
// ReconcileStanding so resolved conditions auto-clear.
func Raise(ctx context.Context, db bun.IDB, in RaiseInput) error {
	return raiseOn(ctx, db, in)
}

func raiseOn(ctx context.Context, db bun.IDB, in RaiseInput) error {
	if in.Severity == "" {
		in.Severity = SeverityMedium
	}
	if in.Category == "" {
		in.Category = "general"
	}
	const q = `
INSERT INTO ops.alarms (fingerprint, source, category, severity, message, device_class, device_id, active)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (fingerprint) DO UPDATE SET
  source           = EXCLUDED.source,
  category         = EXCLUDED.category,
  severity         = EXCLUDED.severity,
  message          = EXCLUDED.message,
  device_class     = EXCLUDED.device_class,
  device_id        = EXCLUDED.device_id,
  active           = EXCLUDED.active,
  occurrence_count = ops.alarms.occurrence_count + 1,
  last_raised_at   = now(),
  -- a re-raised standing alarm is live again; an event keeps whatever cleared_at it had.
  cleared_at       = CASE WHEN EXCLUDED.active THEN NULL ELSE ops.alarms.cleared_at END,
  -- re-alarm: a condition that had CLEARED (was inactive) and recurs is unacknowledged
  -- again; one still active keeps its acknowledgement so it is not nagged every tick.
  acknowledged_at  = CASE WHEN ops.alarms.active THEN ops.alarms.acknowledged_at ELSE NULL END,
  acknowledged_by  = CASE WHEN ops.alarms.active THEN ops.alarms.acknowledged_by ELSE NULL END,
  updated_at       = now()`
	if _, err := db.ExecContext(ctx, q, in.Fingerprint, in.Source, in.Category, in.Severity, in.Message,
		nullStr(in.DeviceClass), nullStr(in.DeviceID), in.Standing); err != nil {
		return fmt.Errorf("alarms: raising %s: %w", in.Fingerprint, err)
	}
	return nil
}

// ReconcileStanding makes the stored STANDING alarms for one source exactly match the
// current set: raise/update each present condition (active), then clear (active=false,
// cleared_at set) any still-active standing alarm of this source no longer present --
// the self-pruning the validation-issues queue uses, so a resolved off-normal device
// leaves the annunciator with no human action. Runs in one transaction.
func ReconcileStanding(ctx context.Context, db *bun.DB, source string, items []RaiseInput) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		fps := make([]string, 0, len(items))
		for _, it := range items {
			it.Source = source
			it.Standing = true
			if err := raiseOn(ctx, tx, it); err != nil {
				return err
			}
			fps = append(fps, it.Fingerprint)
		}
		q := tx.NewUpdate().Model((*Alarm)(nil)).
			Set("active = false").
			Set("cleared_at = now()").
			Set("updated_at = now()").
			Where("source = ?", source).
			Where("active = ?", true)
		if len(fps) > 0 {
			q = q.Where("fingerprint NOT IN (?)", bun.In(fps))
		}
		if _, err := q.Exec(ctx); err != nil {
			return fmt.Errorf("alarms: clearing resolved %s alarms: %w", source, err)
		}
		return nil
	})
}

// ClearByFingerprint marks an alarm returned-to-normal (active=false) by its
// fingerprint -- the clear an external source (a SCADA alarm feed) signals for a point
// it raised. Idempotent: a clear for an unknown or already-inactive fingerprint is a
// no-op, not an error (a re-delivered RTN is common).
func ClearByFingerprint(ctx context.Context, db bun.IDB, fingerprint string) error {
	if _, err := db.NewUpdate().Model((*Alarm)(nil)).
		Set("active = false").
		Set("cleared_at = now()").
		Set("updated_at = now()").
		Where("fingerprint = ?", fingerprint).
		Where("active = ?", true).
		Exec(ctx); err != nil {
		return fmt.Errorf("alarms: clearing %s: %w", fingerprint, err)
	}
	return nil
}

// Filter narrows List. An empty Status returns the LIVE annunciator (active OR
// unacknowledged, not shelved); "shelved"/"cleared"/"all" return those sets.
type Filter struct {
	Status   string
	Severity string
	Source   string
	Limit    int
}

// List returns the alarm queue, severity-first then most-recently-raised. The default
// (empty Status) is the live annunciator an operator works.
func List(ctx context.Context, db bun.IDB, f Filter) ([]Alarm, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	out := []Alarm{}
	q := db.NewSelect().Model(&out).
		ColumnExpr("al.*").
		ColumnExpr("ack.display_name AS acknowledged_by_name").
		Join("LEFT JOIN iam.users AS ack ON ack.id::text = al.acknowledged_by::text")

	switch f.Status {
	case "", "live":
		q = q.Where("(al.active OR al.acknowledged_at IS NULL)").
			Where("(al.shelved_until IS NULL OR al.shelved_until <= now())")
	case "shelved":
		q = q.Where("al.shelved_until IS NOT NULL AND al.shelved_until > now()")
	case "cleared":
		q = q.Where("NOT al.active AND al.acknowledged_at IS NOT NULL")
	case "all":
		// no status filter
	default:
		return nil, invalidf("status must be one of live/shelved/cleared/all")
	}
	if f.Severity != "" {
		switch f.Severity {
		case SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow:
			q = q.Where("al.severity = ?", f.Severity)
		default:
			return nil, invalidf("severity must be one of critical/high/medium/low")
		}
	}
	if f.Source != "" {
		q = q.Where("al.source = ?", f.Source)
	}
	q = q.OrderExpr(severityRankExpr).OrderExpr("al.last_raised_at DESC").Limit(limit)
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("alarms: listing: %w", err)
	}
	return out, nil
}

// severityRankExpr orders critical -> low; shared by List so the annunciator and any
// future read agree on priority.
const severityRankExpr = "CASE al.severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END"

// Counts is the annunciator badge summary.
type Counts struct {
	Total          int `json:"total"`          // live alarms
	Unacknowledged int `json:"unacknowledged"` // live + not yet acknowledged
	Critical       int `json:"critical"`       // live + critical severity
}

// CountActive totals the live annunciator for the toolbar badge.
func CountActive(ctx context.Context, db bun.IDB) (Counts, error) {
	var c Counts
	const q = `SELECT
  COUNT(*) AS total,
  COUNT(*) FILTER (WHERE acknowledged_at IS NULL) AS unacknowledged,
  COUNT(*) FILTER (WHERE severity = 'critical') AS critical
FROM ops.alarms
WHERE (active OR acknowledged_at IS NULL)
  AND (shelved_until IS NULL OR shelved_until <= now())`
	if err := db.NewRaw(q).Scan(ctx, &c); err != nil {
		return c, fmt.Errorf("alarms: counting active: %w", err)
	}
	return c, nil
}

// Acknowledge stamps who saw the alarm. An acknowledged event alarm (inactive) leaves
// the annunciator; an acknowledged standing alarm stays until its condition clears.
func Acknowledge(ctx context.Context, db bun.IDB, id, actor string) error {
	return update(ctx, db, id, func(q *bun.UpdateQuery) *bun.UpdateQuery {
		return q.Set("acknowledged_at = now()").Set("acknowledged_by = ?", nullUUID(actor))
	})
}

// Shelve suppresses an alarm from the live annunciator until `until` (a nuisance or
// known condition an operator is deferring), recording why and who.
func Shelve(ctx context.Context, db bun.IDB, id string, until time.Time, reason, actor string) error {
	if !until.After(time.Now()) {
		return invalidf("shelve-until must be in the future")
	}
	return update(ctx, db, id, func(q *bun.UpdateQuery) *bun.UpdateQuery {
		return q.Set("shelved_until = ?", until).Set("shelve_reason = ?", nullStr(reason)).Set("shelved_by = ?", nullUUID(actor))
	})
}

// Unshelve returns a shelved alarm to the live annunciator immediately.
func Unshelve(ctx context.Context, db bun.IDB, id string) error {
	return update(ctx, db, id, func(q *bun.UpdateQuery) *bun.UpdateQuery {
		return q.Set("shelved_until = NULL").Set("shelve_reason = NULL").Set("shelved_by = NULL")
	})
}

func update(ctx context.Context, db bun.IDB, id string, set func(*bun.UpdateQuery) *bun.UpdateQuery) error {
	q := set(db.NewUpdate().Model((*Alarm)(nil)).Where("id = ?", id).Set("updated_at = now()"))
	r, err := q.Exec(ctx)
	if err != nil {
		return fmt.Errorf("alarms: updating %s: %w", id, err)
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return notFound("alarm %s not found", id)
	}
	return nil
}

// --- standing-source reconcile injection -----------------------------------
//
// The abnormal-configuration standing feed lives in deviceops (it reuses
// deviceops.Abnormal), and deviceops imports THIS package. To let the /alarms/reconcile
// endpoint trigger that scan without alarms importing deviceops (a cycle), main.go
// injects the reconciler here, the same pattern export.SetBaselineStore uses.

var standingReconciler func(ctx context.Context, db *bun.DB) error

// SetStandingReconciler registers the standing-alarm reconcile (wired in main.go to
// deviceops.ReconcileAbnormalAlarms). Nil-safe: ReconcileNow is a no-op if unset.
func SetStandingReconciler(fn func(ctx context.Context, db *bun.DB) error) { standingReconciler = fn }

// ReconcileNow runs the registered standing-alarm reconcile (the manual "rescan" the
// annunciator offers, and the initial scan). Returns nil when no reconciler is wired.
func ReconcileNow(ctx context.Context, db *bun.DB) error {
	if standingReconciler == nil {
		return nil
	}
	return standingReconciler(ctx, db)
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
