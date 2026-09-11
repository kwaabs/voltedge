// Package duct is the composite "create one cable duct and fan out its ways"
// endpoint (Phase 44 Slice 4 -- cable management). It owns no feature-class-specific
// logic and no new write primitive: it orchestrates the existing shared
// crud.CreateFeature (the duct + each DUCT_WAY child) and
// relationship.EstablishRelationship (one containment link per way) inside a single
// transaction. Unlike panel/rmu it is NOT template-driven -- every way is identical,
// so the fan-out count comes straight off the duct's own way_count attribute, and
// each child is a plain DUCT_WAY carrying only its 1..N way_number.
package duct

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/relationship"
	"github.com/uptrace/bun"
)

// ductWayClass is the child class fanned out under a duct. wayNumberField is the
// bore-position attribute stamped on each; wayCountField is the duct's own field
// that says how many to make. containmentKind pins the duct -> way link.
const (
	ductWayClass    = "DUCT_WAY"
	wayNumberField  = "way_number"
	wayCountField   = "way_count"
	containmentKind = "containment"
	// maxWays caps the fan-out so a fat-fingered way_count can't spawn a runaway
	// number of child features in one transaction. 96 is comfortably above any
	// real multi-way duct (a big multiduct is ~24 ways).
	maxWays = 96
)

// InvalidError is a client-fixable problem (a missing duct class, a way_count out of
// range) -- distinct from a plain error so the HTTP layer maps it to 400 rather than
// 500, the same split panel/rmu/crud draw. Callers test with IsInvalid.
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

// CreateDuctInput is the composite request: which duct class to create, its geometry
// (a duct is a line, so geometry is required) and its own attributes. way_count
// inside Attributes drives how many DUCT_WAY children fan out. Subtype/Name/
// TerritoryID are the duct's, exactly as a plain create takes them.
type CreateDuctInput struct {
	DuctFeatureClass string
	Subtype          *int
	Name             *string
	Geometry         json.RawMessage
	TerritoryID      *string
	Attributes       map[string]any
}

// CreatedWay is one fanned-out way in the result: its class, the created feature
// envelope (same shape a create returns), and its 1-based bore position.
type CreatedWay struct {
	FeatureClass string         `json:"feature_class"`
	Feature      map[string]any `json:"feature"`
	WayNumber    int            `json:"way_number"`
}

// CreateDuctResult reports the whole assembly: the duct and every way created and
// contained in it.
type CreateDuctResult struct {
	Duct map[string]any `json:"duct"`
	Ways []CreatedWay   `json:"ways"`
}

// CreateDuct creates one cable duct plus the N identical DUCT_WAY children its
// way_count calls for, and wires the containment (duct -> each way) -- everything in
// one transaction, so a failure anywhere leaves no half-built duct behind. The
// actual writes reuse the shared crud.CreateFeature and
// relationship.EstablishRelationship untouched. The acting user rides ctx (both
// primitives read auth.ActorID themselves).
func CreateDuct(ctx context.Context, db *bun.DB, in CreateDuctInput) (*CreateDuctResult, error) {
	if in.DuctFeatureClass == "" {
		return nil, invalidf("duct_feature_class is required")
	}
	if len(in.Geometry) == 0 {
		return nil, invalidf("geometry is required -- a cable duct is a line feature")
	}

	wayCount, ok := coerceInt(in.Attributes[wayCountField])
	if !ok {
		return nil, invalidf("duct attribute %q (integer) is required -- it sets how many ways to fan out", wayCountField)
	}
	if wayCount < 1 {
		return nil, invalidf("%q must be at least 1 (got %d)", wayCountField, wayCount)
	}
	if wayCount > maxWays {
		return nil, invalidf("%q must be at most %d (got %d)", wayCountField, maxWays, wayCount)
	}

	ductLoaded, err := metadata.Load(ctx, db, in.DuctFeatureClass)
	if err != nil {
		return nil, invalidf("duct feature class %q not found", in.DuctFeatureClass)
	}
	wayLoaded, err := metadata.Load(ctx, db, ductWayClass)
	if err != nil {
		return nil, invalidf("way feature class %q not found -- is Phase 44 Slice 4 seeded and compiled?", ductWayClass)
	}

	result := &CreateDuctResult{}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 1. the duct itself -- the one feature carrying map geometry (and territory).
		ductEnv, err := crud.CreateFeature(ctx, tx, ductLoaded, crud.CreateFeatureInput{
			Subtype:     in.Subtype,
			Name:        in.Name,
			Geometry:    in.Geometry,
			TerritoryID: in.TerritoryID,
			Attributes:  in.Attributes,
		})
		if err != nil {
			return err
		}
		ductID, err := featureID(ductEnv)
		if err != nil {
			return err
		}
		result.Duct = ductEnv

		// 2. one DUCT_WAY per bore, each carrying only its 1-based way_number, then
		//    duct contains way. Ways are geometry-less (geometry_type='none'), so no
		//    geometry/territory is passed.
		for i := 1; i <= wayCount; i++ {
			wayEnv, err := crud.CreateFeature(ctx, tx, wayLoaded, crud.CreateFeatureInput{
				Attributes: map[string]any{wayNumberField: i},
			})
			if err != nil {
				return fmt.Errorf("creating way %d: %w", i, err)
			}
			wayID, err := featureID(wayEnv)
			if err != nil {
				return err
			}
			if _, err := relationship.EstablishRelationship(ctx, tx, containment(
				in.DuctFeatureClass, ductID, ductWayClass, wayID,
			)); err != nil {
				return err
			}
			result.Ways = append(result.Ways, CreatedWay{
				FeatureClass: ductWayClass,
				Feature:      wayEnv,
				WayNumber:    i,
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

// containment builds an EstablishRelationshipInput for a parent-contains-child link,
// pinning the kind so the pairing resolves unambiguously.
func containment(parentClass, parentID, childClass, childID string) relationship.EstablishRelationshipInput {
	kind := containmentKind
	return relationship.EstablishRelationshipInput{
		Origin:      relationship.FeatureRef{FeatureClass: parentClass, FeatureID: parentID},
		Destination: relationship.FeatureRef{FeatureClass: childClass, FeatureID: childID},
		Kind:        &kind,
	}
}

// featureID pulls the created feature's id out of the crud envelope as a string.
// The id column comes back either already a string or as raw bytes depending on the
// driver's scan, so both are handled rather than assuming one.
func featureID(env map[string]any) (string, error) {
	switch v := env["id"].(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		return "", fmt.Errorf("duct: created feature envelope has no string id (got %T)", env["id"])
	}
}

// coerceInt narrows a JSON-decoded attribute value to an int. A number decoded from
// a request body arrives as float64; a programmatic caller may pass a real int. Any
// other type (or a missing key) reports ok=false so the caller can 400 cleanly.
func coerceInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int64:
		return int(n), true
	case int32:
		return int(n), true
	case int:
		return n, true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}
