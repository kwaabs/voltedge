package admin

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// reorderFields rewrites a feature class's per-field display order (migration
// 0146 sort_order) to exactly the order given. `order` must be a PERMUTATION of
// the class's current active field names -- every active field named once, no
// strangers -- so the resulting order is unambiguous and total. It's rejected
// otherwise rather than silently leaving some fields un-ranked.
//
// Pure UI metadata: the form reads sort_order live (metadata.Load), so this
// takes effect immediately with NO recompile and no change to the physical
// gis.* table.
func reorderFields(ctx context.Context, db *bun.DB, featureClassCode string, order []string) error {
	if len(order) == 0 {
		return invalid("order is required (the field names in display order)")
	}
	// Reject duplicates up front -- a repeated name can't be a valid permutation
	// and would otherwise make the "same length" check pass against a missing one.
	seen := make(map[string]bool, len(order))
	for _, name := range order {
		if seen[name] {
			return invalid("field %q appears more than once in the order", name)
		}
		seen[name] = true
	}

	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		fc := new(metadata.FeatureClass)
		if err := tx.NewSelect().Model(fc).Where("code = ?", featureClassCode).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("feature class %q does not exist", featureClassCode)
			}
			return fmt.Errorf("loading feature class %q: %w", featureClassCode, err)
		}

		// The class's current active fields -- the exact set the order must cover
		// (active only, matching what the form and metadata.Load show).
		var current []metadata.Field
		if err := tx.NewSelect().Model(&current).
			Column("id", "name").
			Where("feature_class_id = ?", fc.ID).
			Where("status = ?", "active").
			Scan(ctx); err != nil {
			return fmt.Errorf("loading fields for %q: %w", featureClassCode, err)
		}

		idByName := make(map[string]string, len(current))
		for _, f := range current {
			idByName[f.Name] = f.ID
		}
		if len(order) != len(current) {
			return invalid("order must list all %d active fields of %q exactly once (got %d)",
				len(current), featureClassCode, len(order))
		}
		for _, name := range order {
			if _, ok := idByName[name]; !ok {
				return invalid("%q is not an active field of %q", name, featureClassCode)
			}
		}

		// Assign 1-based ordinals in the requested order.
		for i, name := range order {
			if _, err := tx.NewUpdate().Model((*metadata.Field)(nil)).
				Set("sort_order = ?", i+1).
				Where("id = ?", idByName[name]).
				Exec(ctx); err != nil {
				return fmt.Errorf("updating order of field %q: %w", name, err)
			}
		}
		return nil
	})
}
