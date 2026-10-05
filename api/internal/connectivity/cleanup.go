package connectivity

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// cleanup.go is the deliberate, destructive counterpart to the read-only audit:
// it removes the orphaned_connectivity rows the audit reports -- connections
// pointing at a feature that is soft-deleted or no longer exists. A connection
// to a feature that is gone is a meaningless fact, so clearing it is safe; but
// because it is a hard delete (feature_connectivity has no soft-delete), it is
// gated on connectivity:commit, not the read gate the audit uses.
//
// Reversibility note: a soft-deleted feature CAN be restored, and removing its
// connections here means it would come back UNWIRED. That is the intended
// "clear the dead weight" semantics for features you've decided are truly dead.
// New permanent orphans are prevented at the source -- crud.PurgeFeature revokes
// a feature's connectivity when it is purged (the terminal, non-reversible
// delete), so a soft delete keeps its wiring (recoverable) while a purge cleans
// up after itself.

// CleanupOrphanedConnections hard-deletes every connectivity row that references
// a feature which is not currently live (soft-deleted, purged, or never
// existed). Returns the number of rows removed.
//
// The predicate mirrors the audit's orphan scan: a feature is "live" iff
// runtime.feature_registry holds a row for it with deleted_at IS NULL, so a
// feature_id absent from that set is either soft-deleted (registry row with
// deleted_at set) or gone (no registry row at all).
func CleanupOrphanedConnections(ctx context.Context, db bun.IDB) (int, error) {
	res, err := db.ExecContext(ctx, `
		DELETE FROM runtime.feature_connectivity c
		WHERE c.from_feature_id NOT IN (SELECT feature_id FROM runtime.feature_registry WHERE deleted_at IS NULL)
		   OR c.to_feature_id   NOT IN (SELECT feature_id FROM runtime.feature_registry WHERE deleted_at IS NULL)`)
	if err != nil {
		return 0, fmt.Errorf("connectivity: cleanup orphaned connections: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("connectivity: cleanup orphaned connections: %w", err)
	}
	return int(n), nil
}

// CleanupOrphanedRelationships is the relationship-graph counterpart of
// CleanupOrphanedConnections: it hard-deletes every runtime.feature_relationships
// row that references a feature which is not currently live (soft-deleted,
// purged, or never existed). Returns the number of rows removed. Same predicate
// and the same connectivity:commit gating via the shared cleanup endpoint.
func CleanupOrphanedRelationships(ctx context.Context, db bun.IDB) (int, error) {
	res, err := db.ExecContext(ctx, `
		DELETE FROM runtime.feature_relationships r
		WHERE r.origin_feature_id      NOT IN (SELECT feature_id FROM runtime.feature_registry WHERE deleted_at IS NULL)
		   OR r.destination_feature_id NOT IN (SELECT feature_id FROM runtime.feature_registry WHERE deleted_at IS NULL)`)
	if err != nil {
		return 0, fmt.Errorf("connectivity: cleanup orphaned relationships: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("connectivity: cleanup orphaned relationships: %w", err)
	}
	return int(n), nil
}
