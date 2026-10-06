package crud

import (
	"context"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// Bulk class wipe -- the "nuke" operation behind the Features browser -- is the
// set-scale twin of the single-feature delete/purge in delete.go, and it keeps
// the exact same two-step, soft-then-purge shape for the exact same reason: a
// soft delete is always reversible, so wiping a class first only flips
// deleted_at on every live row (WipeClass), and the irreversible physical
// removal is a separate, separately-authorized call (PurgeDeletedClass) that
// only ever touches rows a soft-delete already sits in front of. One mis-click
// can empty a class off the map, but it can never erase it outright.
//
// Two invariants carry over from the single-row path unchanged:
//
//   - Territory scope (Phase 58). Every row is enumerated through the same
//     readScope/scopeWhere filter the list endpoint uses, so a scoped admin
//     wipes only their territory's rows and an unscoped admin (the usual case,
//     since the two admin tiers hold territories:unscoped) wipes the whole
//     class. The per-row helpers re-check scope underneath as a belt-and-braces
//     guard.
//   - Audit history outlives the rows. Each purge records a `purge` history row
//     from the pre-delete snapshot (gis.feature_history has no FK to the feature
//     table), exactly as hardDeleteFeature does one row at a time.
//
// Authorization is one level above features:write: the handler gates these on
// auth.PermFeaturesWipe (admin / break-glass only) before the engine runs. See
// migration 0116 for why bulk erasure is its own capability.

// ClassCounts is the confirm-dialog fuel: how many rows a wipe or purge would
// touch, scoped to the acting user. Live is the soft-delete target count
// (deleted_at IS NULL); Deleted is the purge target count (already soft-deleted).
type ClassCounts struct {
	Live    int `json:"live"`
	Deleted int `json:"deleted"`
}

// CountClassFeatures returns the in-scope live and soft-deleted row counts for a
// class, so the UI can show "this will soft-delete N features" and "this will
// permanently purge M deleted features" before the user confirms.
func CountClassFeatures(ctx context.Context, db bun.IDB, loaded *metadata.Loaded) (ClassCounts, error) {
	var counts ClassCounts
	live, err := countScoped(ctx, db, loaded, false)
	if err != nil {
		return counts, err
	}
	deleted, err := countScoped(ctx, db, loaded, true)
	if err != nil {
		return counts, err
	}
	counts.Live = live
	counts.Deleted = deleted
	return counts, nil
}

// WipeClass soft-deletes every live, in-scope feature in the class. It reuses
// the single-row softDeleteFeature per id so every gate (territory scope,
// private-draft privacy, Phase-B staging) and every side effect (deleted_at,
// updated_by stamp, `delete` history row) is identical to deleting the features
// one by one from the panel -- there is no bulk shortcut that skips them. The
// whole sweep runs in one transaction: either all live rows flip or none do.
// Returns the number of rows soft-deleted.
func WipeClass(ctx context.Context, db *bun.DB, loaded *metadata.Loaded) (int, error) {
	ids, err := scopedIDs(ctx, db, loaded, false)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	var wiped int
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, id := range ids {
			// softDeleteFeature is idempotent and re-checks scope/draft privacy;
			// a row that slipped out of scope between enumeration and here is
			// surfaced as not-found, which we skip rather than abort the sweep.
			if _, err := softDeleteFeature(ctx, tx, loaded, id); err != nil {
				if isNotFoundError(err) {
					continue
				}
				return err
			}
			wiped++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return wiped, nil
}

// PurgeDeletedClass permanently removes every soft-deleted, in-scope feature in
// the class and cleans up the polymorphic references that would otherwise be
// orphaned. It runs entirely in one transaction, looping hardDeleteFeature per
// id. Each call physically deletes the row (firing the per-class registry-sync
// trigger -> runtime.feature_registry -> runtime.asset_tags cascade), cleans the
// FK-less references to it via the shared cascadeCleanReferences (connectivity,
// relationships, external links, pending edits, diagram placements; diagram
// elements are UNBOUND, not deleted, so a hand-drawn glyph survives losing its
// feature; the derived circuit source-ref is cleared), and records a `purge`
// history row from the pre-delete snapshot. Because hardDeleteFeature is called
// with this tx (not a *bun.DB), it runs inline here rather than opening a nested
// transaction, so the whole class purge still commits or rolls back as one.
//
// Operational and audit records (faults, WAM defects, OMS, gps captures,
// feature_history) are deliberately preserved -- they outlive the asset on
// purpose. Diagram *instances* (a container feature's own diagram) are left
// untouched: purging one contained feature must never nuke someone's whole
// diagram. Returns the number of rows purged.
func PurgeDeletedClass(ctx context.Context, db *bun.DB, loaded *metadata.Loaded) (int, error) {
	ids, err := scopedIDs(ctx, db, loaded, true)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	var purged int
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, id := range ids {
			// hardDeleteFeature requires the row to still be soft-deleted and
			// re-checks scope/draft privacy; skip anything restored or moved out
			// of scope between enumeration and here rather than aborting. Each call
			// cascades its own id's references (cascadeCleanReferences) within this
			// tx, so no separate batched pre-pass is needed.
			if err := hardDeleteFeature(ctx, tx, loaded, id); err != nil {
				if isNotFoundError(err) || isValidationError(err) {
					continue
				}
				return err
			}
			purged++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return purged, nil
}

// cascadeCleanReferences removes the polymorphic, FK-less references to the
// purged feature ids from the topology and editing tables. Each table stores a
// feature id as a bare uuid with no foreign key, so nothing else cleans these --
// without this pass a purge leaves dangling connectivity edges, relationships,
// external links, pending private edits, and diagram placements pointing at rows
// that no longer exist. Ids are compared as text so the caller can pass the
// string ids it enumerated. All statements are parameterized (bun.In), never
// interpolated.
//
// Takes a bun.IDB (not a bun.Tx) so BOTH purge entry points share it: the bulk
// PurgeDeletedClass (passing its outer tx) and the single-row hardDeleteFeature
// (passing its own per-purge tx). Keeping one cleanup definition is deliberate --
// the single-row path used to carry its own, narrower copy that cleaned only
// connectivity + relationships, so a purge from the feature panel orphaned
// external links, pending edits, diagram placements/elements, and circuit
// source-refs that the bulk path cleaned. One function means they can't drift again.
func cascadeCleanReferences(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, ids []string) error {
	// Connectivity edges on either endpoint.
	if _, err := db.ExecContext(ctx,
		`DELETE FROM runtime.feature_connectivity
		 WHERE from_feature_id::text IN (?) OR to_feature_id::text IN (?)`,
		bun.In(ids), bun.In(ids)); err != nil {
		return fmt.Errorf("crud: purge cascade (connectivity): %w", err)
	}
	// Relationships on either endpoint.
	if _, err := db.ExecContext(ctx,
		`DELETE FROM runtime.feature_relationships
		 WHERE origin_feature_id::text IN (?) OR destination_feature_id::text IN (?)`,
		bun.In(ids), bun.In(ids)); err != nil {
		return fmt.Errorf("crud: purge cascade (relationships): %w", err)
	}
	// External links owned by the feature.
	if _, err := db.ExecContext(ctx,
		`DELETE FROM runtime.feature_external_links WHERE feature_id::text IN (?)`,
		bun.In(ids)); err != nil {
		return fmt.Errorf("crud: purge cascade (external links): %w", err)
	}
	// Pending private edits staged against the feature (Phase 61).
	if _, err := db.ExecContext(ctx,
		`DELETE FROM runtime.feature_pending_edits WHERE feature_id::text IN (?)`,
		bun.In(ids)); err != nil {
		return fmt.Errorf("crud: purge cascade (pending edits): %w", err)
	}
	// Diagram placements -- the feature's presence on a diagram.
	if _, err := db.ExecContext(ctx,
		`DELETE FROM runtime.diagram_placements WHERE feature_id::text IN (?)`,
		bun.In(ids)); err != nil {
		return fmt.Errorf("crud: purge cascade (diagram placements): %w", err)
	}
	// Diagram elements bound to the feature are unbound, not deleted, so a
	// hand-drawn glyph outlives the asset it referenced.
	if _, err := db.ExecContext(ctx,
		`UPDATE runtime.diagram_elements SET bound_id = NULL WHERE bound_id::text IN (?)`,
		bun.In(ids)); err != nil {
		return fmt.Errorf("crud: purge cascade (diagram elements): %w", err)
	}
	// Derived circuit source-ref -- clear the denormalized pointer where a circuit
	// was traced back to one of these features (source_feature_class scopes it to
	// this class so we never null another class's ref that happens to share a uuid).
	if _, err := db.ExecContext(ctx,
		`UPDATE gis.circuits SET source_feature_class = NULL, source_feature_id = NULL
		 WHERE source_feature_class = ? AND source_feature_id::text IN (?)`,
		loaded.FeatureClass.Code, bun.In(ids)); err != nil {
		return fmt.Errorf("crud: purge cascade (circuit source ref): %w", err)
	}
	return nil
}

// scopedIDs enumerates the ids of a class's rows constrained to the acting
// user's territory scope. deleted picks which set: false = live rows
// (deleted_at IS NULL, the soft-delete targets), true = already soft-deleted
// rows (the purge targets). It is the set-scale counterpart to the single-row
// getFeature+enforceReadScope pair -- the same scopeWhere fragment the list
// endpoint applies, so bulk operations wipe exactly the rows the user can see.
func scopedIDs(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, deleted bool) ([]string, error) {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return nil, err
	}
	nullTest := "deleted_at IS NULL"
	if deleted {
		nullTest = "deleted_at IS NOT NULL"
	}
	query := fmt.Sprintf("SELECT id::text FROM %s WHERE %s", table, nullTest)

	scopeIDs, apply, err := readScope(ctx, db, loaded)
	if err != nil {
		return nil, err
	}
	var args []any
	if clause, scopeArgs := scopeWhere(scopeIDs, apply); clause != "" {
		query += " AND " + clause
		args = scopeArgs
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("crud: enumerate class ids: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("crud: enumerate class ids: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// countScoped is scopedIDs' aggregate form -- a COUNT(*) under the same
// deleted/scope predicate -- so the confirm dialog can show totals without
// pulling every id back.
func countScoped(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, deleted bool) (int, error) {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return 0, err
	}
	nullTest := "deleted_at IS NULL"
	if deleted {
		nullTest = "deleted_at IS NOT NULL"
	}
	query := fmt.Sprintf("SELECT count(*) FROM %s WHERE %s", table, nullTest)

	scopeIDs, apply, err := readScope(ctx, db, loaded)
	if err != nil {
		return 0, err
	}
	var args []any
	if clause, scopeArgs := scopeWhere(scopeIDs, apply); clause != "" {
		query += " AND " + clause
		args = scopeArgs
	}

	var n int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("crud: count class features: %w", err)
	}
	return n, nil
}
