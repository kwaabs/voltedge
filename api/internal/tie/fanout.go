// Package tie is the composite "create one feeder-to-feeder tie switch"
// endpoint (Phase 50 Slice 2, Part A). A tie is not a feature class of its own:
// it is electrically an MV_SWITCH (or any switch class the caller picks) wired
// between two MV_CONDUCTOR feeders through the switch network rules already
// seeded by seed/mv_lv_switch_example.sql (rule 1: `conductor -> switch.T1`,
// rule 3: `switch.T2 -> conductor`). So, exactly like the bus coupler it is
// modelled on (api/internal/coupler), this package owns no feature-class-specific
// logic and no new write primitive: it orchestrates the shared crud.CreateFeature
// (the switch) and connectivity.EstablishConnection (switch <-> each feeder)
// inside ONE transaction, so a failure anywhere leaves no half-wired tie behind.
//
// What makes it its own package rather than a plain create is (a) the two-ended
// wiring -- a tie is meaningless unless the switch actually joins two feeders,
// atomically -- and (b) the posture it forces: a tie is a NORMALLY-OPEN point
// (a NOP). Where a coupler bridges two BUSES (busbar rules C/D), a tie bridges
// two CONDUCTORS (switch rules 1/3), and where a coupler labels itself with
// switch_function=BUS_COUPLER, a tie labels itself with normal_state=
// NORMALLY_OPEN (Phase 48's declared-open-point field, seed/switch_normal_state_
// fields.sql) AND is born device_state=OPEN, so the freshly placed tie is an
// open standby path -- the thing the N-1 contingency analysis (Part B) looks for
// as a candidate restoration tie. Both defaults are non-clobbering: an explicit
// value in Attributes wins, so a caller who wants a normally-closed section tie,
// or one born closed, can say so.
//
// Switch-only by design, same as the coupler: a real tie point may sit between
// flanking isolators, but the switch<->conductor rules wire a single device
// between two feeders, which is the whole tie. Nothing here hardcodes a class
// code beyond the MV_SWITCH default, which the caller may override with any
// switch class the network rules authorize.
package tie

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

// Field / value / terminal constants. normalStateField + nopValue come from
// Phase 48's field-pack (seed/switch_normal_state_fields.sql) -- setting
// normal_state=NORMALLY_OPEN is what makes a plain switch read as a declared
// open point. deviceStateField + openValue come from the switch example seed;
// forcing device_state=OPEN is what makes the placed tie a live trace barrier
// (only device_state=OPEN and operational_state=ISOLATED/RETIRED stop a trace --
// normal_state is declared design posture and does not, by itself, block flow).
// feederLineTerminal / feederLoadTerminal name the two PINNED switch terminals
// in rules 1 and 3: rule 1 is `conductor -> switch.T1` (the switch line side),
// rule 3 is `switch.T2 -> conductor` (the switch load side). The conductor-side
// terminal is a wildcard in both rules (from_terminal / to_terminal null), so
// the feeder endpoints carry no terminal.
const (
	normalStateField = "normal_state"
	nopValue         = "NORMALLY_OPEN"
	deviceStateField = "device_state"
	openValue        = "OPEN"

	feederLineTerminal = "T1" // switch line side, fed by feeder A (rule 1 to)
	feederLoadTerminal = "T2" // switch load side, onto feeder B (rule 3 from)

	defaultSwitchClass = "MV_SWITCH"
)

// InvalidError is a client-fixable problem (missing geometry, a missing/blank
// feeder reference, both feeders the same feature) -- distinct from a plain
// error so the HTTP layer maps it to 400 rather than 500, the same split the
// coupler / rmu / crud draw. Callers test with IsInvalid.
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

// FeederRef names one conductor the tie joins: its feature class code + id. The
// two feeders may be different classes (nothing here assumes MV_CONDUCTOR) as
// long as a network rule authorizes the conductor<->switch connection -- which
// is what makes a bad pick a clean connectivity rejection rather than a silent
// success.
type FeederRef struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
}

// CreateTieInput is the composite request: which switch class to create
// (defaults to MV_SWITCH), where on the map, the switch's own attributes
// (normal_state defaults to NORMALLY_OPEN and device_state to OPEN if unset, but
// the caller may override either), and the two feeders to join. Subtype/Name/
// TerritoryID are the switch's, exactly as a plain create takes them.
type CreateTieInput struct {
	SwitchFeatureClass string
	Subtype            *int
	Name               *string
	Geometry           json.RawMessage
	TerritoryID        *string
	Attributes         map[string]any
	FeederA            FeederRef
	FeederB            FeederRef
}

// CreateTieResult reports the placed tie: the created switch envelope (same
// shape a create returns) and the ids of the two connections wiring it to
// feeder A (rule 1) and feeder B (rule 3).
type CreateTieResult struct {
	Switch      map[string]any `json:"switch"`
	ConnectionA string         `json:"connection_a"`
	ConnectionB string         `json:"connection_b"`
}

// CreateTie creates one switch and wires it between two existing feeders in a
// single transaction: feeder A -> switch.T1 (rule 1) and switch.T2 -> feeder B
// (rule 3). It defaults normal_state=NORMALLY_OPEN and device_state=OPEN when
// the caller left them unset, so a freshly placed tie is a declared, live-open
// standby path -- but never overrides an explicit value (a normally-closed
// section tie, or one born closed). The switch's required fields (subtype,
// condition_rating, and device_state -- which this defaults) ride in Attributes
// exactly as a plain create; a missing one surfaces as a crud validation error
// (400). The two connections go through the shared connectivity.EstablishConnection
// untouched -- so a feeder pick with no authorizing network rule is rejected
// there, mapped to 400 by the HTTP layer (like a coupler, a tie's two feeders
// are user-picked, so a rule miss is the caller's to fix). The acting user rides
// ctx.
func CreateTie(ctx context.Context, db *bun.DB, in CreateTieInput) (*CreateTieResult, error) {
	switchClass := in.SwitchFeatureClass
	if switchClass == "" {
		switchClass = defaultSwitchClass
	}
	if len(in.Geometry) == 0 {
		return nil, invalidf("geometry is required -- a tie is a map-placed switch")
	}
	if in.FeederA.FeatureClass == "" || in.FeederA.FeatureID == "" {
		return nil, invalidf("feeder_a (feature_class + feature_id) is required")
	}
	if in.FeederB.FeatureClass == "" || in.FeederB.FeatureID == "" {
		return nil, invalidf("feeder_b (feature_class + feature_id) is required")
	}
	if in.FeederA.FeatureID == in.FeederB.FeatureID {
		return nil, invalidf("feeder_a and feeder_b must be different features -- a tie joins two feeders")
	}

	loaded, err := metadata.Load(ctx, db, switchClass)
	if err != nil {
		return nil, invalidf("switch feature class %q not found", switchClass)
	}

	// Default normal_state=NORMALLY_OPEN and device_state=OPEN without clobbering
	// an explicit value. Copy the map so we never mutate the caller's.
	attrs := map[string]any{}
	for k, v := range in.Attributes {
		attrs[k] = v
	}
	if ns, _ := attrs[normalStateField].(string); ns == "" {
		attrs[normalStateField] = nopValue
	}
	if ds, _ := attrs[deviceStateField].(string); ds == "" {
		attrs[deviceStateField] = openValue
	}

	result := &CreateTieResult{}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// 1. the switch -- the one map-placed feature (and hence a territory).
		switchEnv, err := crud.CreateFeature(ctx, tx, loaded, crud.CreateFeatureInput{
			Subtype:     in.Subtype,
			Name:        in.Name,
			Geometry:    in.Geometry,
			TerritoryID: in.TerritoryID,
			Attributes:  attrs,
		})
		if err != nil {
			return err
		}
		switchID, err := featureID(switchEnv)
		if err != nil {
			return err
		}
		result.Switch = switchEnv

		// 2. feeder A -> switch line side (rule 1: conductor -> switch.T1). The
		// conductor side is a wildcard terminal (nil), matching the rule.
		toA := feederLineTerminal
		connA, err := connectivity.EstablishConnection(ctx, tx, connectivity.EstablishConnectionInput{
			From: connectivity.Endpoint{FeatureClass: in.FeederA.FeatureClass, FeatureID: in.FeederA.FeatureID, Terminal: nil},
			To:   connectivity.Endpoint{FeatureClass: switchClass, FeatureID: switchID, Terminal: &toA},
		})
		if err != nil {
			return fmt.Errorf("wiring feeder_a %s -> %s.%s: %w", in.FeederA.FeatureClass, switchClass, toA, err)
		}
		result.ConnectionA = connA.ID

		// 3. switch load side -> feeder B (rule 3: switch.T2 -> conductor). The
		// conductor side is again a wildcard terminal (nil).
		fromB := feederLoadTerminal
		connB, err := connectivity.EstablishConnection(ctx, tx, connectivity.EstablishConnectionInput{
			From: connectivity.Endpoint{FeatureClass: switchClass, FeatureID: switchID, Terminal: &fromB},
			To:   connectivity.Endpoint{FeatureClass: in.FeederB.FeatureClass, FeatureID: in.FeederB.FeatureID, Terminal: nil},
		})
		if err != nil {
			return fmt.Errorf("wiring %s.%s -> feeder_b %s: %w", switchClass, fromB, in.FeederB.FeatureClass, err)
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
// (the same helper shape coupler / rmu / duct use).
func featureID(env map[string]any) (string, error) {
	switch v := env["id"].(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	default:
		return "", fmt.Errorf("tie: created feature envelope has no string id (got %T)", env["id"])
	}
}
