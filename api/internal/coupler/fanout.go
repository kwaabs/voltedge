// Package coupler is the composite "place one bus coupler / bus tie" endpoint
// (Phase Bus-coupler Slice 2). A bus coupler is not a feature class of its own:
// it is electrically an MV_BREAKER wired between two MV_BUSBAR instances through
// the busbar network rules C (`bus T2 -> switch T1`) and D (`switch T2 -> bus
// T1`) already seeded by seed/mv_busbar_example.sql. So, like rmu / panel /
// duct, this package owns no feature-class-specific logic and no new write
// primitive: it orchestrates the shared crud.CreateFeature (the breaker) and
// connectivity.EstablishConnection (breaker <-> each bus) inside ONE
// transaction, so a failure anywhere leaves no half-wired coupler behind.
//
// What makes it its own tiny package rather than a plain create is the two-ended
// wiring: a coupler is meaningless unless the breaker actually joins two named
// buses, and doing that atomically -- create the breaker, wire Bus A -> breaker
// (rule C), wire breaker -> Bus B (rule D) -- is exactly the RunInTx fan-out
// spine rmu uses, minus the internal bus/way template (a coupler's two buses
// already exist; it only bridges them). The switch_function/normal_state fields
// (Slice 1, seed/bus_coupler_fields.sql) carry the semantics; this slice sets
// switch_function=BUS_COUPLER by default so a placed coupler is self-labelling.
//
// Breaker-only by design: a real coupler bay often has flanking isolators, but
// no switch-to-switch network rule exists (only device<->conductor and
// bus<->device), so inline isolators would need new rules -- deferred. Nothing
// here hardcodes a class code beyond the MV_BREAKER default, which the caller
// may override.
package coupler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// Field / value / terminal constants. switchFunctionField + couplerFunctionValue
// come from Slice 1's field-pack (seed/bus_coupler_fields.sql) -- setting the
// field is what makes a plain breaker read as a coupler. busFromTerminal /
// busToTerminal / breakerLineTerminal / breakerLoadTerminal name the four
// PINNED terminals in busbar rules C and D: rule C is `bus.T2 -> breaker.T1`,
// rule D is `breaker.T2 -> bus.T1`, so every endpoint terminal must be stated
// explicitly (a wildcard would not match the pinned rule).
const (
	switchFunctionField = "switch_function"
	couplerFunctionValue = "BUS_COUPLER"

	busFromTerminal     = "T2" // Bus A's tap that feeds the breaker line side (rule C from)
	breakerLineTerminal = "T1" // breaker line side, fed by Bus A (rule C to)
	breakerLoadTerminal = "T2" // breaker load side, onto Bus B (rule D from)
	busToTerminal       = "T1" // Bus B's incoming way the breaker lands on (rule D to)
)

// InvalidError is a client-fixable problem (missing geometry, a missing/blank
// bus reference, both buses the same feature) -- distinct from a plain error so
// the HTTP layer maps it to 400 rather than 500, the same split rmu / crud /
// circuit draw. Callers test with IsInvalid.
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

// BusRef names one bus the coupler bridges: its feature class code + id. The two
// buses may be different classes (nothing here assumes MV_BUSBAR), as long as a
// network rule authorizes the bus<->breaker connection -- which is what makes a
// bad pick a clean connectivity rejection rather than a silent success.
type BusRef struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
}

// CreateCouplerInput is the composite request: which breaker class to create,
// where on the map, the breaker's own attributes (switch_function defaults to
// BUS_COUPLER if unset, but the caller may pass BUS_SECTION for a section
// switch), and the two buses to bridge. Subtype/Name/TerritoryID are the
// breaker's, exactly as a plain create takes them.
type CreateCouplerInput struct {
	BreakerFeatureClass string
	Subtype             *int
	Name                *string
	Geometry            json.RawMessage
	TerritoryID         *string
	Attributes          map[string]any
	BusA                BusRef
	BusB                BusRef
}

// CreateCouplerResult reports the placed coupler: the created breaker envelope
// (same shape a create returns) and the ids of the two connections wiring it to
// Bus A (rule C) and Bus B (rule D).
type CreateCouplerResult struct {
	Breaker     map[string]any `json:"breaker"`
	ConnectionA string         `json:"connection_a"`
	ConnectionB string         `json:"connection_b"`
}

// CreateCoupler creates one breaker and wires it between two existing buses in a
// single transaction: Bus A.T2 -> breaker.T1 (rule C) and breaker.T2 -> Bus B.T1
// (rule D). It sets switch_function=BUS_COUPLER when the caller left it unset so
// the coupler is self-labelling, but never overrides an explicit value (e.g. a
// BUS_SECTION section switch). The breaker's required fields (subtype,
// device_state, condition_rating) ride in Attributes exactly as a plain create;
// a missing one surfaces as a crud validation error (400). The two connections
// go through the shared connectivity.EstablishConnection untouched -- so a bus
// pick with no authorizing network rule is rejected there, mapped to 400 by the
// HTTP layer (unlike rmu's engine-internal wiring, a coupler's buses are
// user-picked, so a rule miss is the caller's to fix). The acting user rides ctx.
func CreateCoupler(ctx context.Context, db *bun.DB, in CreateCouplerInput) (*CreateCouplerResult, error) {
	breakerClass := in.BreakerFeatureClass
	if breakerClass == "" {
		return nil, invalidf("breaker_feature_class is required")
	}
	if len(in.Geometry) == 0 {
		return nil, invalidf("geometry is required -- a bus coupler is a map-placed switch")
	}
	if in.BusA.FeatureClass == "" || in.BusA.FeatureID == "" {
		return nil, invalidf("bus_a (feature_class + feature_id) is required")
	}
	if in.BusB.FeatureClass == "" || in.BusB.FeatureID == "" {
		return nil, invalidf("bus_b (feature_class + feature_id) is required")
	}
	if in.BusA.FeatureID == in.BusB.FeatureID {
		return nil, invalidf("bus_a and bus_b must be different features -- a coupler bridges two buses")
	}

	loaded, err := metadata.Load(ctx, db, breakerClass)
	if err != nil {
		return nil, invalidf("breaker feature class %q not found", breakerClass)
	}

	// Default switch_function to BUS_COUPLER without clobbering an explicit
	// value (BUS_SECTION for a section switch). Copy the map so we never mutate
	// the caller's.
	attrs := map[string]any{}
	for k, v := range in.Attributes {
		attrs[k] = v
	}
	if fn, _ := attrs[switchFunctionField].(string); fn == "" {
		attrs[switchFunctionField] = couplerFunctionValue
	}

	result := &CreateCouplerResult{}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 1. the breaker -- the one map-placed feature (and hence a territory).
		breakerEnv, err := crud.CreateFeature(ctx, tx, loaded, crud.CreateFeatureInput{
			Subtype:     in.Subtype,
			Name:        in.Name,
			Geometry:    in.Geometry,
			TerritoryID: in.TerritoryID,
			Attributes:  attrs,
		})
		if err != nil {
			return err
		}
		breakerID, err := featureID(breakerEnv)
		if err != nil {
			return err
		}
		result.Breaker = breakerEnv

		// 2. Bus A -> breaker line side (rule C: bus.T2 -> breaker.T1).
		fromA, toA := busFromTerminal, breakerLineTerminal
		connA, err := connectivity.EstablishConnection(ctx, tx, connectivity.EstablishConnectionInput{
			From: connectivity.Endpoint{FeatureClass: in.BusA.FeatureClass, FeatureID: in.BusA.FeatureID, Terminal: &fromA},
			To:   connectivity.Endpoint{FeatureClass: breakerClass, FeatureID: breakerID, Terminal: &toA},
		})
		if err != nil {
			return fmt.Errorf("wiring bus_a %s.%s -> %s.%s: %w", in.BusA.FeatureClass, fromA, breakerClass, toA, err)
		}
		result.ConnectionA = connA.ID

		// 3. breaker load side -> Bus B (rule D: breaker.T2 -> bus.T1).
		fromB, toB := breakerLoadTerminal, busToTerminal
		connB, err := connectivity.EstablishConnection(ctx, tx, connectivity.EstablishConnectionInput{
			From: connectivity.Endpoint{FeatureClass: breakerClass, FeatureID: breakerID, Terminal: &fromB},
			To:   connectivity.Endpoint{FeatureClass: in.BusB.FeatureClass, FeatureID: in.BusB.FeatureID, Terminal: &toB},
		})
		if err != nil {
			return fmt.Errorf("wiring %s.%s -> bus_b %s.%s: %w", breakerClass, fromB, in.BusB.FeatureClass, toB, err)
		}
		result.ConnectionB = connB.ID
		return nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

// featureID pulls the created feature's id out of the crud envelope as a string
// -- string or raw bytes depending on the driver's scan, so both are handled
// (the same helper shape rmu / duct use).
func featureID(env map[string]any) (string, error) {
	switch v := env["id"].(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		return "", fmt.Errorf("coupler: created feature envelope has no string id (got %T)", env["id"])
	}
}
