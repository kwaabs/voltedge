package diagram

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// This file is the assembly auto-diagram builder: the server-side equivalent of
// a user opening a fresh diagram, applying a template, and hand-binding every
// typed slot to a real feature -- done in one call with zero interaction, so a
// fanned-out assembly (RMU, packaged substation, pole/pad kit, switchgear panel)
// comes with a ready-to-read single-line instead of an empty canvas.
//
// It lives INSIDE the diagram package on purpose: it reuses the package's own
// unexported operations (createInstance / applyTemplate / placeFeature /
// removeElement) and typed structs directly, so the fan-out engines depend only
// on this one exported entry point (they can't reach the private ops, and these
// are the same code paths the REST handlers drive).

// AssemblyDiagramType is the DIAGRAM_TYPE code every assembly single-line is
// created under. ONE_LINE is unconstrained by any placement allow-list for the
// assembly containers, so every fanned-out component class is admissible.
const AssemblyDiagramType = "ONE_LINE"

// AssemblyComponent is one real feature the builder will try to bind into a
// matching template slot -- identified the same way a placement is, by class
// code + feature id. The fan-out engines already hand these back for every
// component they create.
type AssemblyComponent struct {
	FeatureClass string
	FeatureID    string
}

// GenerateAssemblyDiagramInput is everything the builder needs to stand up one
// populated single-line.
type GenerateAssemblyDiagramInput struct {
	// Container is the feature the diagram is rooted on. It must be
	// is_diagram_container = true -- RMU / PACKAGED_SUBSTATION are already
	// containers; POLE / DIST_XFMR / MV_SWITCHGEAR_PANEL are flipped by
	// seed/assembly_diagram_templates.sql so their kits get a diagram too.
	ContainerClass string
	ContainerID    string
	// DiagramType is the DIAGRAM_TYPE code the instance is created under --
	// ONE_LINE for every assembly single-line (unconstrained by any placement
	// allow-list for these containers, so every component class is admissible).
	DiagramType string
	// Name is the instance's display name (nil = unnamed).
	Name *string
	// TemplateName names the runtime.diagram_templates row whose furniture +
	// typed slots define the layout. Its slots are auto-bound to Components by
	// class; the furniture (bus/riser lines, labels) is kept as drawn. The
	// instance records this template's id (migration 0122) so it can be
	// regenerated.
	TemplateName string
	// ReplaceInstanceID, when set, is an existing diagram instance deleted (with
	// its placements + furniture, ON DELETE CASCADE) inside the SAME transaction
	// as the rebuild -- so regenerate is atomic: the old single-line survives
	// untouched if the rebuild fails, and no window shows two diagrams for one
	// container. Empty on a fresh build.
	ReplaceInstanceID string
	// Components are the fan-out's real internal features. The assembly anchor
	// may be included harmlessly -- it is bound only when a slot of its class
	// exists (e.g. the pad-mount transformer fills the URD template's DIST_XFMR
	// slot; an RMU/panel/pole anchor matches no slot and is ignored). Order
	// within one class is the bind order (slot 1 gets component 1, ...).
	Components []AssemblyComponent
}

// GenerateAssemblyDiagramResult reports what the builder did, so a caller can
// log a partial build (some component a placement rejected, or a template slot
// left with no component).
type GenerateAssemblyDiagramResult struct {
	InstanceID string `json:"instance_id"`
	Placed     int    `json:"placed"`
	Unbound    int    `json:"unbound"` // template slots with no matching component
	Skipped    int    `json:"skipped"` // components a placement rejected (allow-list / shape)
}

// GenerateAssemblyDiagram builds one ready-to-read single-line for an assembly:
// it creates the diagram instance on the container, applies the named template
// (its bus/riser/label furniture plus one typed slot per expected component),
// then binds each slot to a real fan-out component of the same class -- placing
// the feature at the slot's own coordinates/path and consuming the slot so only
// the real feature renders there.
//
// It owns its own transaction, so the whole diagram is atomic AND isolated from
// the fan-out's already-committed writes: a diagram failure never rolls back a
// placed asset (the caller runs this best-effort). Per-component placement
// rejections that are the data's to fix -- a class the container's placement
// allow-list doesn't admit, a path that doesn't match the class's diagram shape
// -- are counted and skipped (they are pre-insert checks, so they don't poison
// the transaction), leaving that slot unbound for a human rather than losing the
// whole single-line. Any other error aborts the build.
func GenerateAssemblyDiagram(ctx context.Context, db *bun.DB, in GenerateAssemblyDiagramInput) (*GenerateAssemblyDiagramResult, error) {
	if in.ContainerClass == "" || in.ContainerID == "" {
		return nil, fmt.Errorf("diagram: generate: container class and id are required")
	}
	if in.TemplateName == "" {
		return nil, fmt.Errorf("diagram: generate: template name is required")
	}

	res := &GenerateAssemblyDiagramResult{}
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 0. on regenerate, drop the old instance FIRST (cascades to its
		// placements + furniture) so the rebuild replaces it atomically -- if
		// anything below fails, the whole tx rolls back and the old diagram
		// stands.
		if in.ReplaceInstanceID != "" {
			if err := deleteInstance(ctx, tx, in.ReplaceInstanceID); err != nil {
				return fmt.Errorf("diagram: generate: replace instance: %w", err)
			}
		}

		// 1. resolve the layout template by name -- before the instance, so the
		// instance can be stamped with the template's id for later regenerate.
		tmpl := new(DiagramTemplate)
		if err := tx.NewSelect().Model(tmpl).Where("name = ?", in.TemplateName).Limit(1).Scan(ctx); err != nil {
			return fmt.Errorf("diagram: generate: template %q not found: %w", in.TemplateName, err)
		}

		// 2. the instance, rooted on the container (gated on is_diagram_container),
		// remembering which template built it.
		inst, err := createInstance(ctx, tx, createInstanceRequest{
			Container:        featureRef{FeatureClass: in.ContainerClass, FeatureID: in.ContainerID},
			DiagramType:      in.DiagramType,
			Name:             in.Name,
			SourceTemplateID: &tmpl.ID,
		})
		if err != nil {
			return fmt.Errorf("diagram: generate: create instance: %w", err)
		}
		res.InstanceID = inst.ID

		// 3. apply it -- inserts the furniture and the unbound typed slots, and
		// hands back every element (with its id/kind/slot_class/coords) so we can
		// bind the slots without a re-list.
		elements, err := applyTemplate(ctx, tx, inst.ID, tmpl.ID)
		if err != nil {
			return fmt.Errorf("diagram: generate: apply template: %w", err)
		}

		// 4. bucket the components by class, FIFO within a class.
		byClass := map[string][]AssemblyComponent{}
		for _, c := range in.Components {
			byClass[c.FeatureClass] = append(byClass[c.FeatureClass], c)
		}

		// 5. bind each typed slot to the next unused component of its class.
		for _, e := range elements {
			if e.Kind != "slot" || e.SlotClass == nil {
				continue
			}
			queue := byClass[*e.SlotClass]
			if len(queue) == 0 {
				res.Unbound++
				continue
			}
			comp := queue[0]
			byClass[*e.SlotClass] = queue[1:]

			if _, err := placeFeature(ctx, tx, inst.ID, placeRequest{
				FeatureClass: comp.FeatureClass,
				FeatureID:    comp.FeatureID,
				X:            e.X,
				Y:            e.Y,
				Rotation:     e.Rotation,
				Path:         e.Path,
			}); err != nil {
				if isValidationError(err) {
					// A placement the data must fix (allow-list / placeable / path
					// shape) -- pre-insert, so the tx is intact. Leave the slot and
					// move on; the returned component is simply put back nowhere.
					res.Skipped++
					continue
				}
				return fmt.Errorf("diagram: generate: place %s: %w", comp.FeatureClass, err)
			}
			// The slot is now bound -- drop it so only the placed feature renders.
			if err := removeElement(ctx, tx, inst.ID, e.ID); err != nil {
				return fmt.Errorf("diagram: generate: consume slot: %w", err)
			}
			res.Placed++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// RegenerateAssemblyDiagram rebuilds an auto-generated assembly single-line from
// scratch: it re-applies the exact template the diagram was built from (recorded
// on the instance, migration 0122) and re-binds the same set of components, in
// place of the old instance (atomically replaced). It is how a user rebuilds a
// single-line whose layout drifted -- furniture nudged, a slot deleted, the
// template itself improved and reseeded -- back to the canonical layout.
//
// The two halves of "what to rebuild" both come from the existing instance, so
// nothing is guessed:
//   - the LAYOUT is the instance's source_template_id (the template that made it);
//   - the MEMBERS are the instance's own placements (every feature currently on
//     it) -- authoritative for every engine, including kits like the URD pillar
//     whose components attach to the assembly by no structural relationship at all.
//
// A diagram with no source template (a hand-built one, or an instance created
// before this feature) is not regenerable -- there is no canonical layout to
// return to -- and is rejected with a validation error. The rebuild reuses
// GenerateAssemblyDiagram with ReplaceInstanceID set, so the old single-line is
// untouched if anything fails.
func RegenerateAssemblyDiagram(ctx context.Context, db *bun.DB, instanceID string) (*GenerateAssemblyDiagramResult, error) {
	inst := new(DiagramInstance)
	if err := db.NewSelect().Model(inst).Where("id = ?", instanceID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("diagram instance %s not found", instanceID)
		}
		return nil, fmt.Errorf("diagram: regenerate: load instance: %w", err)
	}
	if inst.SourceTemplateID == nil {
		return nil, invalid("diagram %s was not auto-generated from a template, so there is nothing to regenerate", instanceID)
	}

	// The layout: the template that built it. Resolve its name so the rebuild
	// goes through the same GenerateAssemblyDiagram path as a fresh build.
	tmpl := new(DiagramTemplate)
	if err := db.NewSelect().Model(tmpl).Where("id = ?", *inst.SourceTemplateID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalid("the template this diagram was built from no longer exists, so it cannot be regenerated")
		}
		return nil, fmt.Errorf("diagram: regenerate: load template: %w", err)
	}

	// The container class code + diagram type code, resolved the same way a GET
	// returns them.
	formatted, err := formatInstance(ctx, db, inst)
	if err != nil {
		return nil, err
	}

	// The members: every feature currently placed on the instance.
	placements, err := listPlacements(ctx, db, instanceID)
	if err != nil {
		return nil, err
	}
	components := make([]AssemblyComponent, 0, len(placements))
	for _, p := range placements {
		components = append(components, AssemblyComponent{
			FeatureClass: p.FeatureClass,
			FeatureID:    p.FeatureID,
		})
	}

	return GenerateAssemblyDiagram(ctx, db, GenerateAssemblyDiagramInput{
		ContainerClass:    formatted.Container.FeatureClass,
		ContainerID:       formatted.Container.FeatureID,
		DiagramType:       formatted.DiagramType,
		Name:              formatted.Name,
		TemplateName:      tmpl.Name,
		Components:        components,
		ReplaceInstanceID: instanceID,
	})
}
