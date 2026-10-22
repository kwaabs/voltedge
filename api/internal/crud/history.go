package crud

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/drafts"
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
	// EditSessionID groups a batch of edits made in one edit-mode session (migration
	// 0158). Nil for a write made outside edit mode or by a non-UI caller.
	EditSessionID any             `bun:"edit_session_id"`
	Snapshot      json.RawMessage `bun:"snapshot,type:jsonb"`
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
	// Private-drafts Phase B: a private edit/delete of an already-published
	// feature lands in staging (runtime.feature_pending_edits), not the base gis
	// row, so the base writers above never fire for it. These verbs keep the
	// audit trail complete anyway -- staging a private change records the
	// would-be state, discarding records the withdrawal, and applying then emits
	// the ordinary historyUpdate/historyDelete as the change lands on the base
	// row. So a feature's timeline reads faithfully: ... -> stage_edit -> update,
	// with no blind spot for work done privately.
	historyStageEdit   = "stage_edit"
	historyStageDelete = "stage_delete"
	historyDiscard     = "discard"
)

// HistoryRelate / HistoryUnrelate are the action verbs for a structural/spatial
// relationship being established or revoked. Exported (unlike the verbs above)
// because the write originates in the relationship package, not here: a
// relationship change never touches either feature's own gis row, so without an
// explicit audit entry the feature it attaches to (the pole a lamp rides, say)
// shows nothing in its History. They are NOT staging verbs, so they surface in
// every view the public verbs do -- the per-feature timeline, the activity log,
// and the edit-session roll-up.
const (
	HistoryRelate   = "relate"
	HistoryUnrelate = "unrelate"
)

// RecordRelationshipEvent appends one feature_history audit row on featureID
// (class classCode) for a relationship change touching it -- the exported seam
// the relationship package uses so the owning writer of the history table stays
// here. action is HistoryRelate or HistoryUnrelate; snapshot carries the
// relationship context (the other feature, the class, the kind) so the timeline
// reads as "linked to <X>". Actor and edit-session are read from ctx exactly as
// recordHistory does, so a relate made in an edit session is grouped with the
// rest of that session's changes. The caller invokes this best-effort AFTER the
// relationship itself is committed, so a history failure never undoes the change.
func RecordRelationshipEvent(ctx context.Context, db bun.IDB, classCode, featureID, action string, snapshot map[string]any) error {
	return recordHistory(ctx, db, classCode, featureID, action, snapshot)
}

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
		EditSessionID:    editSessionArg(ctx),
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

// stagingActions are the Phase B verbs whose history rows are private to their
// actor's chain: a private edit/delete/discard is unpublished work that no peer
// should see in another user's timeline. They're filtered out of the timeline
// exactly as the pending overlay and Phase A draft filter hide the change
// itself -- see the filter in listFeatureHistory.
var stagingActions = []string{historyStageEdit, historyStageDelete, historyDiscard}

// listFeatureHistory returns one feature's change timeline, newest first.
//
// Private-drafts Phase B: the staging verbs (stage_edit/stage_delete/discard)
// record work an author did privately over an already-public feature, so they
// must not leak into a peer's view of the timeline. They're kept only when the
// row's actor is in the viewer's draft-visible set (the viewer or a subordinate,
// the same set that governs Phase A draft visibility); when the rollout gate is
// off or the viewer is an admin / break-glass account, VisibleAuthors doesn't
// apply and the full timeline -- staging rows included -- is returned unchanged.
func listFeatureHistory(ctx context.Context, db bun.IDB, classCode, featureID string) ([]historyEntry, error) {
	authors, apply, err := drafts.VisibleAuthors(ctx, db)
	if err != nil {
		return nil, err
	}

	q := db.NewSelect().
		ColumnExpr("h.id, h.action, h.actor_id, u.display_name AS actor_name, u.email AS actor_email, h.at, h.snapshot").
		TableExpr("gis.feature_history AS h").
		Join("LEFT JOIN iam.users AS u ON u.id = h.actor_id").
		Where("h.feature_class_code = ?", classCode).
		Where("h.feature_id = ?", featureID)

	if apply {
		// A staging row survives only when its actor is in the visible set;
		// every non-staging row is unconditionally kept. actor_id::text so the
		// uuid column compares against the string ids VisibleAuthors returns.
		placeholders := make([]string, len(authors))
		args := make([]any, 0, len(authors)+1)
		args = append(args, bun.In(stagingActions))
		for i := range authors {
			placeholders[i] = "?"
			args = append(args, authors[i])
		}
		q = q.Where(
			"(h.action NOT IN (?) OR h.actor_id::text IN ("+strings.Join(placeholders, ", ")+"))",
			args...,
		)
	}

	var entries []historyEntry
	if err := q.OrderExpr("h.at DESC, h.id DESC").Scan(ctx, &entries); err != nil {
		return nil, fmt.Errorf("crud: list history: %w", err)
	}
	return entries, nil
}
