package assetlife

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// LifecycleEvent is one transition row of ops.lifecycle_events (migration 0184),
// with the actor's display name resolved for the ledger view.
type LifecycleEvent struct {
	ID               string    `bun:"id" json:"id"`
	FeatureClassCode string    `bun:"feature_class_code" json:"feature_class_code"`
	FeatureID        string    `bun:"feature_id" json:"feature_id"`
	FromStatus       *string   `bun:"from_status" json:"from_status"`
	ToStatus         string    `bun:"to_status" json:"to_status"`
	ActorID          *string   `bun:"actor_id" json:"actor_id"`
	ActorName        *string   `bun:"actor_name" json:"actor_name"`
	OccurredAt       time.Time `bun:"occurred_at" json:"occurred_at"`
}

// LedgerFilter narrows the lifecycle-events ledger. Empty fields are ignored.
type LedgerFilter struct {
	ToStatus     string
	FeatureClass string
	Since        *time.Time
	Until        *time.Time
	Limit        int
}

// ListLifecycleEvents returns lifecycle transitions newest-first for the filter,
// with the acting user's display name joined in. Capped (default 500, max 1000) so
// the "what changed this quarter" board stays a bounded read.
func ListLifecycleEvents(ctx context.Context, db bun.IDB, f LedgerFilter) ([]LifecycleEvent, error) {
	var events []LifecycleEvent
	q := db.NewSelect().
		TableExpr("ops.lifecycle_events AS e").
		ColumnExpr("e.id::text AS id").
		ColumnExpr("e.feature_class_code").
		ColumnExpr("e.feature_id::text AS feature_id").
		ColumnExpr("e.from_status").
		ColumnExpr("e.to_status").
		ColumnExpr("e.actor_id::text AS actor_id").
		ColumnExpr("u.display_name AS actor_name").
		ColumnExpr("e.occurred_at").
		Join("LEFT JOIN iam.users AS u ON u.id = e.actor_id").
		Order("e.occurred_at DESC")

	if s := strings.TrimSpace(f.ToStatus); s != "" {
		q = q.Where("e.to_status = ?", s)
	}
	if c := strings.TrimSpace(f.FeatureClass); c != "" {
		q = q.Where("e.feature_class_code = ?", c)
	}
	if f.Since != nil {
		q = q.Where("e.occurred_at >= ?", *f.Since)
	}
	if f.Until != nil {
		q = q.Where("e.occurred_at <= ?", *f.Until)
	}

	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	q = q.Limit(limit)

	if err := q.Scan(ctx, &events); err != nil {
		return nil, fmt.Errorf("assetlife: listing lifecycle events: %w", err)
	}
	return events, nil
}
