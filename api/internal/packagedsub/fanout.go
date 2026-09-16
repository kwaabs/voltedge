// Package packagedsub is the composite "place one packaged (movable) substation
// and get its whole pre-wired internal kit" endpoint (Phase 56). Like the RMU
// endpoint (api/internal/rmu) it owns no feature-class-specific logic and no new
// write primitive: it orchestrates the existing shared crud.CreateFeature (the
// enclosure and each internal component), relationship.EstablishRelationship
// (enclosure -> each component, containment) and connectivity.EstablishConnection
// (each component to the previous one) inside a single transaction, driven
// entirely by the metadata.packaged_substation_templates recipe (migration 0092).
//
// The ONE difference from the RMU fan-out is topology: an RMU is a STAR (every
// switched way wires to one shared internal bus), a packaged substation is a
// series CHAIN (MV switch -> transformer -> LV board). So instead of wiring each
// item to a common bus, this fan-out wires each item past the first to the
// PREVIOUS item, using the two terminals the template names per item
// (upstream_terminal on the previous component, own_terminal on this one).
// Nothing here hardcodes a class code, component set or terminal -- they all come
// from the template, the same way the RMU and generic CRUD engines read metadata.
package packagedsub

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

// packageConfigField is the attribute a placed packaged substation carries its
// configuration in -- the field the Slice 1 catalog seeded on PACKAGED_SUBSTATION
// (package_config, pointing at the PACKAGED_SUB_CONFIG domain). Its coded value
// (COMPACT, ...) selects the chain template to fan out, exactly as an RMU's
// rmu_config selects its way template.
const packageConfigField = "package_config"

// containmentKind pins the relationship kind for every link this endpoint writes
// (enclosure -> each component). All three pairings were seeded as containment in
// Slice 1; passing the kind explicitly disambiguates cleanly.
const containmentKind = "containment"

// InvalidError is a client-fixable problem (a missing config, a class with no
// template, missing geometry) -- distinct from a plain error so the HTTP layer
// maps it to 400 rather than 500, the same split rmu / crud draw. Callers test
// with IsInvalid.
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

// CreatePackagedSubInput is the composite request: which packaged-substation
// class to create, where on the map (Geometry), the permanent-vs-mobile Subtype,
// and the unit's own attributes. The configuration -- the packageConfigField
// value inside Attributes -- picks the chain template. The class is named by
// code (not hardcoded) so a future second packaged class needs no Go change.
type CreatePackagedSubInput struct {
	PackagedFeatureClass string
	Subtype              *int
	Name                 *string
	Geometry             json.RawMessage
	TerritoryID          *string
	Attributes           map[string]any
}

// CreatedComponent is one fanned-out internal component in the result: its class,
// the created feature envelope, the id of the connection wiring it to the
// previous component (empty for the first component, which has no upstream), and
// where it sits on the future mimic.
type CreatedComponent struct {
	FeatureClass string         `json:"feature_class"`
	Feature      map[string]any `json:"feature"`
	ConnectionID string         `json:"connection_id,omitempty"`
	OffsetX      float64        `json:"offset_x"`
	OffsetY      float64        `json:"offset_y"`
	Rotation     float64        `json:"rotation"`
}

// CreatePackagedSubResult reports the whole assembly: the placed enclosure and
// every internal component created, wired into the enclosure by containment and
// into the chain by connectivity.
type CreatePackagedSubResult struct {
	PackagedSubstation map[string]any     `json:"packaged_substation"`
	Components         []CreatedComponent `json:"components"`
}

// CreatePackagedSubstation creates one packaged substation plus the chain of
// internal components its configuration template lists (MV switch -> transformer
// -> LV board), wires all the containment (enclosure -> each component) AND the
// internal connectivity (each component to the previous one) -- everything in one
// transaction, so a failure anywhere leaves no half-built unit behind. The
// component set, subtypes, default attributes, mimic offsets and each item's two
// wiring terminals all come from metadata.packaged_substation_templates for the
// (class, config); the actual writes reuse the shared crud.CreateFeature,
// relationship.EstablishRelationship and connectivity.EstablishConnection
// untouched. The acting user rides ctx (all three primitives read auth.ActorID
// themselves).
func CreatePackagedSubstation(ctx context.Context, db *bun.DB, in CreatePackagedSubInput) (*CreatePackagedSubResult, error) {
	if in.PackagedFeatureClass == "" {
		return nil, invalidf("packaged_feature_class is required")
	}
	if len(in.Geometry) == 0 {
		return nil, invalidf("geometry is required -- a packaged substation is a map-placed unit")
	}

	config, _ := in.Attributes[packageConfigField].(string)
	if config == "" {
		return nil, invalidf("packaged substation attribute %q is required -- it selects the fan-out template", packageConfigField)
	}

	pkgLoaded, err := metadata.Load(ctx, db, in.PackagedFeatureClass)
	if err != nil {
		return nil, invalidf("packaged substation feature class %q not found", in.PackagedFeatureClass)
	}

	tmpl, err := metadata.LoadPackagedSubTemplate(ctx, db, in.PackagedFeatureClass, config)
	if err != nil {
		return nil, err
	}
	if tmpl == nil || len(tmpl.Items) == 0 {
		return nil, invalidf("no packaged substation template defined for %s config %q", in.PackagedFeatureClass, config)
	}

	// Pre-load each distinct component class's metadata once (a chain may repeat a
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

	result := &CreatePackagedSubResult{}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 1. the enclosure itself -- the one feature carrying map geometry (and
		// hence a territory); the internal components are geometry-optional and
		// created without.
		pkgEnv, err := crud.CreateFeature(ctx, tx, pkgLoaded, crud.CreateFeatureInput{
			Subtype:     in.Subtype,
			Name:        in.Name,
			Geometry:    in.Geometry,
			TerritoryID: in.TerritoryID,
			Attributes:  in.Attributes,
		})
		if err != nil {
			return err
		}
		pkgID, err := featureID(pkgEnv)
		if err != nil {
			return err
		}
		result.PackagedSubstation = pkgEnv

		// 2. each templated component, in chain order: create it, enclosure
		// contains component, and (for every item past the first) wire it to the
		// PREVIOUS component so a trace can walk switch -> transformer -> board and
		// an OPEN switch halts the walk.
		var (
			prevClass string
			prevID    string
			havePrev  bool
		)
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
			if _, err := relationship.EstablishRelationship(ctx, tx, containment(
				in.PackagedFeatureClass, pkgID, it.ComponentFeatureClass, compID,
			)); err != nil {
				return err
			}

			created := CreatedComponent{
				FeatureClass: it.ComponentFeatureClass,
				Feature:      env,
				OffsetX:      it.OffsetX,
				OffsetY:      it.OffsetY,
				Rotation:     it.Rotation,
			}
			if havePrev {
				conn, err := connectivity.EstablishConnection(ctx, tx, connectivity.EstablishConnectionInput{
					From: connectivity.Endpoint{FeatureClass: prevClass, FeatureID: prevID, Terminal: it.UpstreamTerminal},
					To:   connectivity.Endpoint{FeatureClass: it.ComponentFeatureClass, FeatureID: compID, Terminal: it.OwnTerminal},
				})
				if err != nil {
					return fmt.Errorf("wiring %s to %s: %w", it.ComponentFeatureClass, prevClass, err)
				}
				created.ConnectionID = conn.ID
			}
			result.Components = append(result.Components, created)

			prevClass, prevID, havePrev = it.ComponentFeatureClass, compID, true
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

// containment builds an EstablishRelationshipInput for a parent-contains-child
// link, pinning the kind so the pairing resolves unambiguously.
func containment(parentClass, parentID, childClass, childID string) relationship.EstablishRelationshipInput {
	kind := containmentKind
	return relationship.EstablishRelationshipInput{
		Origin:      relationship.FeatureRef{FeatureClass: parentClass, FeatureID: parentID},
		Destination: relationship.FeatureRef{FeatureClass: childClass, FeatureID: childID},
		Kind:        &kind,
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
		return "", fmt.Errorf("packagedsub: created feature envelope has no string id (got %T)", env["id"])
	}
}
