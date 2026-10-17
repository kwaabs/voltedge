package crud

import (
	"context"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/drafts"
	"github.com/uptrace/bun"
)

// activity.go is the REACTIVE accountability view over gis.feature_history -- the
// "what did this user change, in this window" question, as opposed to the
// per-feature timeline (history.go) which answers "what happened to THIS feature".
// Same append-only source, sliced by actor + time instead of by feature, so a
// compliance / review lookup ("show me everything Dana did this afternoon", "every
// delete last week") does not need to walk features one by one.
//
// It is read-only and carries the SAME private-drafts visibility filter the
// per-feature timeline uses (staging verbs stay inside their author's chain), so the
// cross-feature slice never leaks a peer's private work.

// ActivityEntry is one change in the cross-feature activity log: who, when, what
// action, and which feature (class + id, so a client can locate it). The full
// post-change snapshot is deliberately omitted -- an activity log is who/what/when;
// drill into the per-feature history (GET /{code}/{id}/history) for the state.
type ActivityEntry struct {
	ID               int64     `bun:"id" json:"id"`
	FeatureClassCode string    `bun:"feature_class_code" json:"feature_class_code"`
	FeatureID        string    `bun:"feature_id" json:"feature_id"`
	Action           string    `bun:"action" json:"action"`
	ActorID          *string   `bun:"actor_id" json:"actor_id"`
	ActorName        *string   `bun:"actor_name" json:"actor_name"`
	ActorEmail       *string   `bun:"actor_email" json:"actor_email"`
	At               time.Time `bun:"at" json:"at"`
}

// ActivityFilter narrows the activity log. All fields are optional; a bare call
// returns the most recent changes system-wide.
type ActivityFilter struct {
	ActorID      string // a user id -- the "who"
	FeatureClass string // a feature class code
	Action       string // create | update | publish | delete | ...
	From         *time.Time
	To           *time.Time
	Limit        int
}

// listActivity returns the activity log newest first, bounded by Limit (default
// 500, max 1000). It applies the same private-drafts visibility gate as the
// per-feature timeline, so a staging-verb row surfaces only when its author is in
// the viewer's visible set.
func listActivity(ctx context.Context, db bun.IDB, f ActivityFilter) ([]ActivityEntry, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 500
	}

	authors, apply, err := drafts.VisibleAuthors(ctx, db)
	if err != nil {
		return nil, err
	}

	q := db.NewSelect().
		ColumnExpr("h.id, h.feature_class_code, h.feature_id, h.action, h.actor_id, u.display_name AS actor_name, u.email AS actor_email, h.at").
		TableExpr("gis.feature_history AS h").
		Join("LEFT JOIN iam.users AS u ON u.id = h.actor_id")

	if f.ActorID != "" {
		q = q.Where("h.actor_id::text = ?", f.ActorID)
	}
	if f.FeatureClass != "" {
		q = q.Where("h.feature_class_code = ?", f.FeatureClass)
	}
	if f.Action != "" {
		q = q.Where("h.action = ?", f.Action)
	}
	if f.From != nil {
		q = q.Where("h.at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("h.at < ?", *f.To)
	}

	// Private-drafts: keep a staging row only when its actor is visible; keep every
	// non-staging row. Mirrors listFeatureHistory. An empty author set (the gate off
	// / no visible authors) reduces to "hide all staging", which bun.In handles.
	if apply {
		q = q.Where("(h.action NOT IN (?) OR h.actor_id::text IN (?))", bun.In(stagingActions), bun.In(authors))
	}

	entries := []ActivityEntry{}
	if err := q.OrderExpr("h.at DESC, h.id DESC").Limit(limit).Scan(ctx, &entries); err != nil {
		return nil, fmt.Errorf("crud: listing activity: %w", err)
	}
	return entries, nil
}
