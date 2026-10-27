// Package assembly is the GENERAL "place one anchor feature and get its whole
// pre-wired kit" endpoint (Phase 57). It generalises the two earlier fan-outs --
// rmu (a star: N ways to one shared bus) and packagedsub (a chain: each item to
// the previous one) -- so a single engine also covers a BRANCH, where items wire
// back to different earlier features (the pole-mounted transformer: the arrester
// taps the transformer primary, the LV board hangs off the secondary), and where
// the anchor holds the kit by an arbitrary relationship kind (spatial_support for
// a pole, which is not a container) or by nothing at all (the URD pillar, merely
// adjacent to its transformer).
//
// Like rmu/packagedsub it owns no feature-class-specific logic and no new write
// primitive: it orchestrates the shared crud.CreateFeature (the anchor and each
// component), relationship.EstablishRelationship (each item to its structural
// parent, kind from the template) and connectivity.EstablishConnection (each item
// to its electrical target) inside one transaction, driven entirely by the
// metadata.assembly_templates recipe (migration 0093). Nothing here hardcodes a
// class code, component set, relationship kind or terminal -- they all come from
// the template, so a new kit (recloser, capacitor bank, BESS) is a pure seed.
package assembly

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/relationship"
	"github.com/uptrace/bun"
)

// anchorPosition is the conceptual position of the anchor feature. Template items
// number from 1; a support_parent_position or wire_to_position of 0 (or, for the
// structural parent, nil) refers to the anchor.
const anchorPosition = 0

// InvalidError is a client-fixable problem (missing anchor class, missing config,
// a config with no template, missing geometry) -- distinct from a plain error so
// the HTTP layer maps it to 400 rather than 500, the same split rmu/packagedsub/
// crud draw. Callers test with IsInvalid.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalidf(format string, args ...any) error {
	return &InvalidError{msg: fmt.Sprintf(format, args...)}
}

// IsInvalid reports whether err is (or wraps) an InvalidError.
func IsInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

// CreateAssemblyInput is the composite request: which anchor class to create, the
// template Config (its code within that class -- e.g. POLE_MOUNT_STD), where on
// the map (Geometry), and the anchor's own Subtype/Name/attributes exactly as a
// plain create takes them. Config is a plain code, NOT an attribute on the anchor:
// the anchor (a POLE, a DIST_XFMR) has no config field, so the caller names the
// recipe directly. A template may override Subtype (anchor_subtype_code).
type CreateAssemblyInput struct {
	AnchorFeatureClass string
	Config             string
	Subtype            *int
	Name               *string
	Geometry           json.RawMessage
	TerritoryID        *string
	Attributes         map[string]any
}

// CreatedComponent is one fanned-out component in the result: its class, the
// created feature envelope, the id of the connection wiring it to its electrical
// target (empty when the item has none), the id of the structural relationship to
// its parent (empty when the item has none), and where it sits on the future mimic.
type CreatedComponent struct {
	FeatureClass          string         `json:"feature_class"`
	Feature               map[string]any `json:"feature"`
	ConnectionID          string         `json:"connection_id,omitempty"`
	SupportRelationshipID string         `json:"support_relationship_id,omitempty"`
	OffsetX               float64        `json:"offset_x"`
	OffsetY               float64        `json:"offset_y"`
	Rotation              float64        `json:"rotation"`
}

// CreateAssemblyResult reports the whole kit: the placed anchor and every component
// created, wired to its structural parent and electrical target.
type CreateAssemblyResult struct {
	Anchor     map[string]any     `json:"anchor"`
	Components []CreatedComponent `json:"components"`
}

// featureRef is a created feature keyed by its template position (0 = anchor) so a
// later item can name it as a structural parent or wiring target.
type featureRef struct {
	class string
	id    string
}

// CreateAssembly creates one anchor feature plus the kit its template lists, wiring
// each component to its structural parent (relationship, kind from the template)
// and to its electrical target (connectivity) -- everything in one transaction, so
// a failure anywhere leaves no half-built kit behind. The component set, subtypes,
// default attributes, relationship kinds, wiring terminals and mimic offsets all
// come from metadata.assembly_templates for the (anchor class, config); the actual
// writes reuse the shared crud.CreateFeature, relationship.EstablishRelationship
// and connectivity.EstablishConnection untouched. The acting user rides ctx (all
// three primitives read auth.ActorID themselves).
func CreateAssembly(ctx context.Context, db *bun.DB, in CreateAssemblyInput) (*CreateAssemblyResult, error) {
	if in.AnchorFeatureClass == "" {
		return nil, invalidf("anchor_feature_class is required")
	}
	if in.Config == "" {
		return nil, invalidf("config is required -- it selects the kit template")
	}
	if len(in.Geometry) == 0 {
		return nil, invalidf("geometry is required -- an assembly is placed at a map location")
	}

	anchorLoaded, err := metadata.Load(ctx, db, in.AnchorFeatureClass)
	if err != nil {
		return nil, invalidf("anchor feature class %q not found", in.AnchorFeatureClass)
	}

	tmpl, err := metadata.LoadAssemblyTemplate(ctx, db, in.AnchorFeatureClass, in.Config)
	if err != nil {
		return nil, err
	}
	if tmpl == nil || len(tmpl.Items) == 0 {
		return nil, invalidf("no assembly template defined for %s config %q", in.AnchorFeatureClass, in.Config)
	}

	// Pre-load each distinct component class's metadata once (a kit may repeat a
	// class), before opening the transaction: read-only lookups don't belong in it.
	compMeta := map[string]*metadata.Loaded{}
	for _, it := range tmpl.Items {
		if _, ok := compMeta[it.ComponentFeatureClass]; ok {
			continue
		}
		loaded, err := metadata.Load(ctx, db, it.ComponentFeatureClass)
		if err != nil {
			return nil, invalidf("component feature class %q in template not found", it.ComponentFeatureClass)
		}
		compMeta[it.ComponentFeatureClass] = loaded
	}

	// A template may force the anchor subtype (e.g. a URD unit is pad_mounted);
	// otherwise the placing user's choice stands.
	anchorSubtype := in.Subtype
	if tmpl.AnchorSubtypeCode != nil {
		anchorSubtype = tmpl.AnchorSubtypeCode
	}

	result := &CreateAssemblyResult{}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		return fanOut(ctx, tx, in, anchorLoaded, tmpl, compMeta, anchorSubtype, result)
	}); err != nil {
		return nil, err
	}
	return result, nil
}

// fanOut performs the whole kit creation inside a caller-supplied transaction: the
// anchor, then every component wired to its structural parent and electrical
// target. Extracted from CreateAssembly so the authoring dry-run (authoring.go)
// can run the REAL fan-out in a rolled-back transaction to validate a kit before
// it is saved -- no parallel, less-trustworthy validation path. result is filled
// as it goes (the dry-run discards it).
func fanOut(ctx context.Context, tx bun.Tx, in CreateAssemblyInput, anchorLoaded *metadata.Loaded, tmpl *metadata.AssemblyTemplateResolved, compMeta map[string]*metadata.Loaded, anchorSubtype *int, result *CreateAssemblyResult) error {
	// 1. the anchor itself -- the one feature carrying map geometry (and hence a
	// territory); the kit components are geometry-optional and created without.
	anchorEnv, err := crud.CreateFeature(ctx, tx, anchorLoaded, crud.CreateFeatureInput{
		Subtype:     anchorSubtype,
		Name:        in.Name,
		Geometry:    in.Geometry,
		TerritoryID: in.TerritoryID,
		Attributes:  in.Attributes,
	})
	if err != nil {
		return err
	}
	anchorID, err := featureID(anchorEnv)
	if err != nil {
		return err
	}
	result.Anchor = anchorEnv

	// byPosition[0] is the anchor; each item records itself so a later item can
	// name it as a structural parent or wiring target. The template guarantees
	// targets reference position 0 or an EARLIER item, so this single forward
	// pass resolves every reference.
	byPosition := map[int]featureRef{anchorPosition: {class: in.AnchorFeatureClass, id: anchorID}}

	for _, it := range tmpl.Items {
		env, err := crud.CreateFeature(ctx, tx, compMeta[it.ComponentFeatureClass], crud.CreateFeatureInput{
			Subtype:    it.SubtypeCode,
			Attributes: it.Attributes,
		})
		if err != nil {
			return fmt.Errorf("creating templated %s: %w", it.ComponentFeatureClass, err)
		}
		compID, err := featureID(env)
		if err != nil {
			return err
		}
		byPosition[it.Position] = featureRef{class: it.ComponentFeatureClass, id: compID}

		created := CreatedComponent{
			FeatureClass: it.ComponentFeatureClass,
			Feature:      env,
			OffsetX:      it.OffsetX,
			OffsetY:      it.OffsetY,
			Rotation:     it.Rotation,
		}

		// Structural link: this item to its parent (default the anchor), kind
		// from the template (e.g. spatial_support). Absent for items with no
		// structural relationship (a nil SupportKind).
		if it.SupportKind != nil {
			parentPos := anchorPosition
			if it.SupportParentPosition != nil {
				parentPos = *it.SupportParentPosition
			}
			parent, ok := byPosition[parentPos]
			if !ok {
				return fmt.Errorf("assembly: %s support_parent_position %d not yet created (template out of order)", it.ComponentFeatureClass, parentPos)
			}
			rel, err := relationship.EstablishRelationship(ctx, tx, relationshipInput(
				*it.SupportKind, parent.class, parent.id, it.ComponentFeatureClass, compID,
			))
			if err != nil {
				return err
			}
			created.SupportRelationshipID = rel.ID
		}

		// Electrical link: this item to its wiring target (0 = anchor). Absent
		// for an externally-fed item (a nil WireToPosition, e.g. the cutout that
		// takes its feed from the MV line placed separately).
		if it.WireToPosition != nil {
			target, ok := byPosition[*it.WireToPosition]
			if !ok {
				return fmt.Errorf("assembly: %s wire_to_position %d not yet created (template out of order)", it.ComponentFeatureClass, *it.WireToPosition)
			}
			conn, err := connectivity.EstablishConnection(ctx, tx, connectivity.EstablishConnectionInput{
				From: connectivity.Endpoint{FeatureClass: target.class, FeatureID: target.id, Terminal: it.UpstreamTerminal},
				To:   connectivity.Endpoint{FeatureClass: it.ComponentFeatureClass, FeatureID: compID, Terminal: it.OwnTerminal},
			})
			if err != nil {
				return fmt.Errorf("wiring %s to %s: %w", it.ComponentFeatureClass, target.class, err)
			}
			created.ConnectionID = conn.ID
		}

		result.Components = append(result.Components, created)
	}
	return nil
}

// relationshipInput builds an EstablishRelationshipInput for a parent-to-child link
// with the given kind (spatial_support, containment, ...), pinning the kind so the
// pairing resolves unambiguously.
func relationshipInput(kind, parentClass, parentID, childClass, childID string) relationship.EstablishRelationshipInput {
	k := kind
	return relationship.EstablishRelationshipInput{
		Origin:      relationship.FeatureRef{FeatureClass: parentClass, FeatureID: parentID},
		Destination: relationship.FeatureRef{FeatureClass: childClass, FeatureID: childID},
		Kind:        &k,
	}
}

// featureID pulls the created feature's id out of the crud envelope as a string.
// The id column comes back either already a string or as raw bytes depending on
// the driver's scan, so both are handled rather than assuming one.
func featureID(env map[string]any) (string, error) {
	switch v := env["id"].(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		return "", fmt.Errorf("assembly: created feature envelope has no string id (got %T)", env["id"])
	}
}
