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
	if current["deleted_at"] != nil {
		return current, nil
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
	return formatRow(loaded, results[0])
}
