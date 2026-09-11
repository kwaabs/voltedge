package crud

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// featureHistory mirrors gis.feature_history (migration 0067): one append-only
// row per feature write, storing the full post-change state as a jsonb
// snapshot. Written from the low-level writers (insertFeature, updateFeature,
// setStatus, setDeletedAt, hardDeleteFeature) in the same bun.IDB the feature
// write used -- so when the caller supplies a transaction (the MCP tools and
// the field-sync replay do), the history row commits atomically with the write;
// on the plain REST path (no outer tx) it's a second statement on the same
// connection. Either way an audit row is written for every change made through
// the API.
type featureHistory struct {
	bun.BaseModel `bun:"table:gis.feature_history"`

	ID               int64           `bun:"id,pk,autoincrement"`
	FeatureClassCode string          `bun:"feature_class_code"`
	FeatureID        string          `bun:"feature_id"`
	Action           string          `bun:"action"`
	ActorID          any             `bun:"actor_id"` // uuid string, or nil for a system write
	Snapshot         json.RawMessage `bun:"snapshot,type:jsonb"`
}

// The action verbs written to gis.feature_history.action. One per distinct
// write the CRUD engine performs, so a reader can tell a publish from an
// attribute edit from a delete without diffing snapshots.
const (
	historyCreate    = "create"
	historyUpdate    = "update"
	historyPublish   = "publish"
	historyUnpublish = "unpublish"
	historyDelete    = "delete"
	historyRestore   = "restore"
	historyPurge     = "purge"
)

// recordHistory appends one audit row for a feature write. snapshot is the
// formatRow-shaped map the writer is about to return (the full post-change
// state); actor is read from ctx exactly as the created_by/updated_by stamp is.
// An error here fails the enclosing write -- a silent gap would defeat the
// point of an audit trail, and inside a transaction it rolls the write back.
func recordHistory(ctx context.Context, db bun.IDB, classCode, featureID, action string, snapshot map[string]any) error {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("crud: marshal history snapshot: %w", err)
	}
	_, err = db.NewInsert().Model(&featureHistory{
		FeatureClassCode: classCode,
		FeatureID:        featureID,
		Action:           action,
		ActorID:          actorArg(ctx),
		Snapshot:         raw,
	}).Exec(ctx)
	if err != nil {
		return fmt.Errorf("crud: record history: %w", err)
	}
	return nil
}

// recordFeatureHistory is the convenience wrapper every writer calls: it reads
// the feature id straight out of the formatRow-shaped snapshot (post-write for
// create/update/publish/unpublish/delete/restore, pre-delete for purge) and the
// class code off loaded, so callers never have to thread an id that might be a
// *string, a generated uuid, or absent. The id normalizes to a string in
// scanRows, so the type assertion holds for every real feature row.
func recordFeatureHistory(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, action string, snapshot map[string]any) error {
	id, _ := snapshot["id"].(string)
	return recordHistory(ctx, db, loaded.FeatureClass.Code, id, action, snapshot)
}

// historyEntry is one row of a feature's timeline as the API returns it. The
// actor is resolved to a name/email via a LEFT JOIN to iam.users (both nil for
// a system write or a since-deleted user), so the client can show "who" without
// a second lookup. Snapshot is the full post-change feature state, passed
// straight through as the stored jsonb.
type historyEntry struct {
	ID         int64           `bun:"id" json:"id"`
	Action     string          `bun:"action" json:"action"`
	ActorID    *string         `bun:"actor_id" json:"actor_id"`
	ActorName  *string         `bun:"actor_name" json:"actor_name"`
	ActorEmail *string         `bun:"actor_email" json:"actor_email"`
	At         time.Time       `bun:"at" json:"at"`
	Snapshot   json.RawMessage `bun:"snapshot" json:"snapshot"`
}

// listFeatureHistory returns one feature's change timeline, newest first.
func listFeatureHistory(ctx context.Context, db bun.IDB, classCode, featureID string) ([]historyEntry, error) {
	var entries []historyEntry
	err := db.NewSelect().
		ColumnExpr("h.id, h.action, h.actor_id, u.display_name AS actor_name, u.email AS actor_email, h.at, h.snapshot").
		TableExpr("gis.feature_history AS h").
		Join("LEFT JOIN iam.users AS u ON u.id = h.actor_id").
		Where("h.feature_class_code = ?", classCode).
		Where("h.feature_id = ?", featureID).
		OrderExpr("h.at DESC, h.id DESC").
		Scan(ctx, &entries)
	if err != nil {
		return nil, fmt.Errorf("crud: list history: %w", err)
	}
	return entries, nil
}
