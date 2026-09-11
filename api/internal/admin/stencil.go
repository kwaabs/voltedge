package admin

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// listDiagramStencils is the admin write-side view of the stencil catalog
// (metadata.diagram_stencils) -- every stencil with its current promote_class
// mapping, so the editor can show what each glyph promotes into and change it.
// Same rows the canvas palette reads over the public GET /symbology/stencils
// (features:read); this list is metadata:read-gated with the rest of /admin,
// and ordered the same way (category, sort_order, icon_ref).
func listDiagramStencils(ctx context.Context, db *bun.DB) ([]diagramStencilResponse, error) {
	stencils, err := metadata.ListDiagramStencils(ctx, db)
	if err != nil {
		return nil, err
	}
	out := make([]diagramStencilResponse, len(stencils))
	for i, s := range stencils {
		out[i] = diagramStencilResponse{
			IconRef:      s.IconRef,
			Label:        s.Label,
			Category:     s.Category,
			SortOrder:    s.SortOrder,
			PromoteClass: s.PromoteClass,
		}
	}
	return out, nil
}

// updateDiagramStencil sets or clears a stencil's promote_class -- the feature
// class a stamped symbol of this stencil can be double-clicked to promote into
// on a diagram (Phase 25, docs/phase-25-stencil-promotion.md). promote_class is
// a code, not an FK (the same "code, not FK" choice as the column itself and
// diagram_elements.slot_class), so the target is validated here rather than by
// a database constraint: it must be an existing feature class whose
// geometry_type is 'none'. That restriction is the heart of promotion -- a
// promoted symbol becomes a geometry-less, diagram-native feature (there's no
// way to give a spatial class real map geometry from inside a diagram), exactly
// the same rule the digitize tool already enforces. The class's
// is_diagram_placeable flag is a separate placement gate edited on the Feature
// Classes page; a non-placeable target is accepted here (so a mapping can be
// authored before the flag is flipped, in either order), but promotion of it
// will fail until that flag is set -- the editor surfaces that as a warning.
func updateDiagramStencil(ctx context.Context, db *bun.DB, iconRef string, req updateDiagramStencilRequest) (*diagramStencilResponse, error) {
	if req.PromoteClass == nil {
		return nil, invalid("request must set promote_class (\"\" to clear it, or a feature class code to set it)")
	}

	var resp diagramStencilResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		s := new(metadata.DiagramStencil)
		if err := tx.NewSelect().Model(s).Where("icon_ref = ?", iconRef).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("stencil %q does not exist", iconRef)
			}
			return fmt.Errorf("loading stencil %q: %w", iconRef, err)
		}

		if *req.PromoteClass == "" {
			s.PromoteClass = nil
		} else {
			fc := new(metadata.FeatureClass)
			if err := tx.NewSelect().Model(fc).Where("code = ?", *req.PromoteClass).Scan(ctx); err != nil {
				if err == sql.ErrNoRows {
					return invalid("promote_class %q does not exist -- name an existing feature class", *req.PromoteClass)
				}
				return fmt.Errorf("resolving feature class %q: %w", *req.PromoteClass, err)
			}
			if fc.GeometryType != "none" {
				return invalid("promote_class %q has geometry_type %q -- only geometry_type='none' (diagram-native) classes can be promotion targets", *req.PromoteClass, fc.GeometryType)
			}
			s.PromoteClass = &fc.Code
		}

		if _, err := tx.NewUpdate().Model(s).Column("promote_class").Where("icon_ref = ?", iconRef).Exec(ctx); err != nil {
			return fmt.Errorf("updating stencil %q: %w", iconRef, err)
		}

		resp = diagramStencilResponse{
			IconRef:      s.IconRef,
			Label:        s.Label,
			Category:     s.Category,
			SortOrder:    s.SortOrder,
			PromoteClass: s.PromoteClass,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
