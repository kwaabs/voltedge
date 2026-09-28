package diagram

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// promoteElement turns a stencil on a diagram -- a typed `slot` or a decorative
// line/bus/symbol -- into a real, permanent feature in one atomic step (the
// admin-only, deliberate version of Phase 25 promotion). It runs the whole
// swap in a single transaction so the diagram can never end up half-promoted:
//
//  1. load the element (scoped to this diagram),
//  2. resolve the feature class it stands in for -- the stencil's own mapping
//     when it has one, else the caller's chosen class (see resolvePromoteClass),
//  3. create the feature (crud.CreateFeature; geometry-less, diagram-native),
//  4. place it where the stencil sat, carrying the stencil's own path so a
//     line-shaped promotion keeps its drawn polyline, and
//  5. delete the stencil element so the marker is swapped for the feature,
//     never doubled.
//
// The new feature is deliberately left unwired -- the admin connects it
// afterward with the normal connect tool. Wiring is not inferred here (there is
// no unambiguous, non-fragile way to pick a target for a generic stencil).
//
// The metadata:write gate is enforced by the route mount (handler.go), not
// here: this is the same discipline every other write in the package follows,
// leaving authorization to the middleware and legality to the engine.
func promoteElement(ctx context.Context, db *bun.DB, diagramInstanceID, elementID string, req promoteRequest) (*promoteResponse, error) {
	var resp *promoteResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		el := new(DiagramElement)
		if err := tx.NewSelect().Model(el).
			Where("id = ? AND diagram_instance_id = ?", elementID, diagramInstanceID).
			Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return notFound("element %s not found on diagram %s", elementID, diagramInstanceID)
			}
			return fmt.Errorf("diagram: promote element: %w", err)
		}

		classCode, err := resolvePromoteClass(ctx, tx, el, req)
		if err != nil {
			return err
		}
		loaded, err := loadFeatureClass(ctx, tx, classCode)
		if err != nil {
			return err
		}

		// Create the feature with no geometry: a promotion target is a
		// diagram-native class (geometry_type 'none' for a stencil glyph, or a
		// geometry_optional line like MV_CONDUCTOR placed diagram-only), so it
		// carries no map geom and belongs to no territory -- CreateFeature's own
		// geometry-less branch handles both.
		created, err := crud.CreateFeature(ctx, tx, loaded, crud.CreateFeatureInput{
			Subtype:    req.Subtype,
			Name:       req.Name,
			Attributes: req.Attributes,
		})
		if err != nil {
			return err
		}
		featureID, _ := created["id"].(string)
		if featureID == "" {
			return fmt.Errorf("diagram: promote element: created feature has no id")
		}

		// Place the new feature exactly where the stencil sat -- same call
		// bindSlot/placeFeatureHandler use, so is_diagram_placeable, the
		// per-pairing allow-list, and the diagram-shape path check all apply. A
		// stencil with a drawn polyline (a cable/bus slot) hands its path through,
		// so a line promotion keeps its full length.
		placement, err := placeFeature(ctx, tx, diagramInstanceID, placeRequest{
			FeatureClass: classCode,
			FeatureID:    featureID,
			X:            el.X,
			Y:            el.Y,
			Rotation:     el.Rotation,
			Path:         el.Path,
		})
		if err != nil {
			return err
		}

		if err := removeElement(ctx, tx, diagramInstanceID, elementID); err != nil {
			return err
		}

		resp = &promoteResponse{
			FeatureClass: classCode,
			FeatureID:    featureID,
			Placement:    *placement,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// resolvePromoteClass decides which feature class an element promotes into.
// The stencil's own mapping always wins so a mapped stencil can never be
// redirected; only when the element carries no mapping do we honour the
// caller's chosen class (req.FeatureClass), the picker path for promoting
// generic decoration. Whatever class we land on still has to clear
// placeFeature's is_diagram_placeable / allow-list / path-shape gates, so an
// unfit choice is rejected there, not trusted here.
func resolvePromoteClass(ctx context.Context, db bun.IDB, el *DiagramElement, req promoteRequest) (string, error) {
	baked, err := bakedPromoteClass(ctx, db, el)
	if err != nil {
		return "", err
	}
	if baked != "" {
		return baked, nil
	}
	if req.FeatureClass != nil && *req.FeatureClass != "" {
		return *req.FeatureClass, nil
	}
	if el.Kind == "slot" {
		return "", invalid("this slot has no slot_class and no class was chosen, so there's nothing to promote it to")
	}
	return "", invalid("this element isn't linked to a feature class and no class was chosen, so there's nothing to promote it to")
}

// bakedPromoteClass returns the feature class a stencil intrinsically stands
// for, or "" when it carries none (pure decoration). There are three mappings,
// mirroring the canvas's own bakedPromoteClass (diagram-canvas.tsx):
//
//   - a typed `slot` promotes to its slot_class;
//   - a decorative line/bus/symbol that carries an explicit style.promoteClass
//     (e.g. a template busbar tagged MV_BUSBAR) uses it;
//   - a stamped symbol falls back to its stencil's promote_class
//     (metadata.diagram_stencils, keyed by style.iconRef).
func bakedPromoteClass(ctx context.Context, db bun.IDB, el *DiagramElement) (string, error) {
	if el.Kind == "slot" {
		if el.SlotClass != nil && *el.SlotClass != "" {
			return *el.SlotClass, nil
		}
		return "", nil
	}

	var style struct {
		PromoteClass string `json:"promoteClass"`
		IconRef      string `json:"iconRef"`
	}
	if len(el.Style) > 0 {
		// Style has already passed validateStyle on write, so a decode error here
		// only means an absent field -- treat as unmapped.
		_ = json.Unmarshal(el.Style, &style)
	}
	if style.PromoteClass != "" {
		return style.PromoteClass, nil
	}
	if style.IconRef != "" {
		stencil := new(metadata.DiagramStencil)
		err := db.NewSelect().Model(stencil).Where("icon_ref = ?", style.IconRef).Scan(ctx)
		switch {
		case err == nil:
			if stencil.PromoteClass != nil && *stencil.PromoteClass != "" {
				return *stencil.PromoteClass, nil
			}
		case errors.Is(err, sql.ErrNoRows):
			// No stencil row for this glyph -- treat as unmapped.
		default:
			return "", fmt.Errorf("diagram: resolving stencil promote_class: %w", err)
		}
	}
	return "", nil
}
