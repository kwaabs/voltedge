package crud

import (
	"context"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// Delete is two operations, not one, same reasoning as publish/unpublish
// being separate from create/update: a soft delete (deleted_at set) is
// always safe to call and always reversible via restore, so it's the
// only thing DELETE /{code}/{id} does. Permanently removing a row
// (purge) is a distinct, separately-routed operation gated on the row
// already being soft-deleted -- a single wrong call can flip deleted_at,
// but it can never purge a live row outright, since purge always has a
// soft-delete in between it and a row that still matters.

// DeleteFeature is the exported entry point for a soft delete, mirroring
// CreateFeature/UpdateFeature so callers outside this package (the field
// tool's POST /sync delete op) have one function to replay a delete through.
// It's a thin pass-through to softDeleteFeature: same idempotency, same
// deleted_at = now() write, same returned envelope. The web CRUD handler
// keeps calling softDeleteFeature directly (it's in-package).
func DeleteFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string) (map[string]any, error) {
	return softDeleteFeature(ctx, db, loaded, id)
}

// softDeleteFeature marks a feature deleted_at = now(), without touching
// its status -- a published row can be deleted without unpublishing
// first, same as unpublish needs no completeness gate: deleted-ness and
// draft/published are orthogonal questions. Idempotent -- deleting an
// already-deleted row just returns its current state, matching
// publish/unpublish's idempotency shape.
func softDeleteFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string) (map[string]any, error) {
	current, err := getFeature(ctx, db, loaded, id)
	if err != nil {
		return nil, err
	}
	// Phase 58 spatial write enforcement: a scoped user may not delete a feature
	// outside their territories. Surfaced as not-found, exactly as a GET of the
	// same feature would be, so a delete reveals no more than a read.
	if err := enforceReadScope(ctx, db, loaded, current); err != nil {
		return nil, err
	}
	// Private-drafts (Phase 61): a peer can't delete a private draft they can't
	// see. Not-found, matching read and the territory gate above.
	if err := enforceDraftScope(ctx, db, current); err != nil {
		return nil, err
	}
	if current["deleted_at"] != nil {
		return current, nil
	}
	// Private-drafts Phase B: a scoped author retiring an already-public feature
	// stages the delete privately -- the base row stays live for everyone else
	// until the author applies it. A never-published draft (Phase A private) is
	// deleted in place, and an apply-in-progress bypasses this (see stagingActive).
	if author, staging, err := stagingActive(ctx, db, current); err != nil {
		return nil, err
	} else if staging {
		return stagePrivateDelete(ctx, db, loaded, author, id, current)
	}
	return setDeletedAt(ctx, db, loaded, id, true)
}

// restoreFeature undoes a soft delete. No gate -- reverting deleted_at to
// NULL can't violate anything a NULL deleted_at didn't already allow.
// Idempotent the same way as softDeleteFeature.
func restoreFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string) (map[string]any, error) {
	current, err := getFeature(ctx, db, loaded, id)
	if err != nil {
		return nil, err
	}
	// Phase 58 spatial write enforcement: restore is an edit too -- a scoped user
	// can't restore a feature outside their territories. Not-found, matching read.
	if err := enforceReadScope(ctx, db, loaded, current); err != nil {
		return nil, err
	}
	// Private-drafts (Phase 61): restore is an edit -- a peer can't restore a
	// private draft they can't see. Not-found, matching read.
	if err := enforceDraftScope(ctx, db, current); err != nil {
		return nil, err
	}
	if current["deleted_at"] == nil {
		return current, nil
	}
	return setDeletedAt(ctx, db, loaded, id, false)
}

// hardDeleteFeature physically removes a feature row. Requires the row
// to already be soft-deleted -- see the package comment above for why.
// Returns nothing on success; there's no row left to hand back.
func hardDeleteFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string) error {
	current, err := getFeature(ctx, db, loaded, id)
	if err != nil {
		return err
	}
	// Phase 58 spatial write enforcement: a scoped user may not purge a feature
	// outside their territories. Not-found, matching read (and the soft-delete
	// gate above), so a purge attempt reveals no more than a GET would.
	if err := enforceReadScope(ctx, db, loaded, current); err != nil {
		return err
	}
	// Private-drafts (Phase 61): a peer can't purge a private draft they can't
	// see. Not-found, matching read and the soft-delete gate.
	if err := enforceDraftScope(ctx, db, current); err != nil {
		return err
	}
	if current["deleted_at"] == nil {
		return invalid("feature %s must be soft-deleted before it can be purged", id)
	}

	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return err
	}

	result, err := db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE id = ?", table), id)
	if err != nil {
		return fmt.Errorf("crud: purge: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("crud: purge: %w", err)
	}
	if n == 0 {
		return notFound("feature %s not found", id)
	}
	// Audit trail (Phase 41): the row is gone, so there's no post-write state to
	// record -- snapshot the pre-delete `current` (fetched above) instead, so the
	// history preserves what the feature looked like at the moment it was purged.
	// gis.feature_history has no FK to the feature table, so this row outlives it.
	if err := recordFeatureHistory(ctx, db, loaded, historyPurge, current); err != nil {
		return fmt.Errorf("crud: purge: %w", err)
	}
	return nil
}

// setDeletedAt is softDeleteFeature/restoreFeature's shared write. deleted
// picks the literal assigned to deleted_at -- both branches are fixed
// constants chosen by this internal bool, never by request input, so
// building the SET clause with fmt.Sprintf here carries no injection risk.
func setDeletedAt(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string, deleted bool) (map[string]any, error) {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return nil, err
	}

	assignment := "deleted_at = NULL"
	if deleted {
		assignment = "deleted_at = now()"
	}

	// A soft delete / restore is an edit too, so it stamps the actor into
	// updated_by alongside updated_at (Slice E).
	query := fmt.Sprintf(
		"UPDATE %s SET %s, updated_at = now(), updated_by = ? WHERE id = ? RETURNING %s",
		table, assignment, strings.Join(selectColumns(loaded), ", "),
	)
	rows, err := db.QueryContext(ctx, query, actorArg(ctx), id)
	if err != nil {
		return nil, fmt.Errorf("crud: set deleted_at: %w", err)
	}
	results, err := scanRows(rows)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, notFound("feature %s not found", id)
	}
	out, err := formatRow(loaded, results[0])
	if err != nil {
		return nil, err
	}
	// Audit trail (Phase 41): record the soft delete / restore by direction.
	// softDeleteFeature/restoreFeature short-circuit an already-in-that-state
	// row before reaching here, so a repeated delete or restore records nothing.
	action := historyRestore
	if deleted {
		action = historyDelete
	}
	if err := recordFeatureHistory(ctx, db, loaded, action, out); err != nil {
		return nil, err
	}
	return out, nil
}
