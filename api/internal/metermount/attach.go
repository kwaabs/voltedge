// Package metermount is the small composite action behind "attach a meter to a
// service point": it creates a geometry-less METER, links it to the service
// point structurally (SERVICE_POINT supports METER), and wires it to that SP
// (METER T2 -> SERVICE_POINT T1). The mirror of api/internal/streetlight, with
// SERVICE_POINT as the host instead of POLE -- but simpler, because the meter's
// wiring target is the host itself (deterministic), not a nearby conductor found
// by a proximity search.
//
// Data model (seed/meter_service_point_mounted.sql): METER geometry_optional (a
// SP-mounted meter rides its service point, no own geometry -- a surveyed meter
// keeps its point, per instance) + a SERVICE_POINT supports METER relationship
// class. The feed rule METER T2 -> SERVICE_POINT T1 already ships
// (seed/lv_metering_example.sql).
//
// LOAD / COUNTING: the service point stays the counted supply -- a meter wired to
// a SERVICE_POINT is a DEVICE OF it, not a second leaf. The trace still conducts
// THROUGH the meter (LV -> meter -> SP), but trace/service.go
// (metersFeedingServicePoint) skips an SP-attached meter in the rollup, so a
// PRIMARY meter and its SP never double-count -- no SP retirement, no load move.
// The meter's reading is a device record; the SP carries the authoritative
// load/customers. (A standalone meter with no SP -- MV/network metering -- still
// counts itself, unchanged.)
package metermount

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/relationship"
)

const (
	meterClass        = "METER"
	servicePointClass = "SERVICE_POINT"
	supportKind       = "spatial_support"
	meterLoadTerminal = "T2" // the meter's load side; the network rule is METER T2 -> SERVICE_POINT T1
	spTerminal        = "T1"
)

// AttachInput identifies the service point and the meter to put on it.
type AttachInput struct {
	ServicePointFeatureClass string         // the host class (normally SERVICE_POINT)
	ServicePointFeatureID    string         // the existing service point to attach to
	SubtypeCode              *int           // the meter subtype, if the class defines any
	Attributes               map[string]any // the meter's fields (meter_role, load, ...)
	// CanWire gates the wire-up on the caller holding connectivity:commit. False ->
	// attach only (create + the support link), left for manual wiring -- the same
	// split streetlight draws (EstablishConnection has no RBAC gate of its own).
	CanWire bool
}

// AttachResult is the outcome: the created meter, its support link, whether it
// was wired to the SP, and any load-reconciliation note.
type AttachResult struct {
	Meter        map[string]any                   `json:"meter"`
	Relationship *relationship.RelationshipResult `json:"relationship"`
	Wired        bool                             `json:"wired"`
	Connection   *connectivity.ConnectionResult   `json:"connection,omitempty"`
	WireNote     string                           `json:"wire_note,omitempty"`
}

// AttachMeter creates the geometry-less meter and links it to the service point
// (atomically), then wires it to that SP (best-effort, gated on connectivity:
// commit). The create + support link commit together -- a meter that couldn't be
// linked to its SP must not exist. The wire runs after that commit: a wiring
// failure leaves the meter attached-but-unwired with a note, never undoes the
// attachment.
func AttachMeter(ctx context.Context, db *bun.DB, in AttachInput) (*AttachResult, error) {
	spClass := in.ServicePointFeatureClass
	if spClass == "" {
		spClass = servicePointClass
	}
	if in.ServicePointFeatureID == "" {
		return nil, invalidf("service_point_feature_id is required")
	}
	meterLoaded, err := metadata.Load(ctx, db, meterClass)
	if err != nil {
		return nil, fmt.Errorf("metermount: loading %s: %w", meterClass, err)
	}

	var meter map[string]any
	var meterID string
	var rel *relationship.RelationshipResult

	// 1. create the geometry-less meter + the support link, atomically.
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		env, err := crud.CreateFeature(ctx, tx, meterLoaded, crud.CreateFeatureInput{
			Subtype:    in.SubtypeCode,
			Attributes: in.Attributes,
			// No Geometry -> geometry-less (METER is geometry_optional). The meter
			// has no independent location; it rides its service point.
		})
		if err != nil {
			return err
		}
		meter = env
		id, ok := env["id"].(string)
		if !ok {
			return fmt.Errorf("metermount: created meter has no id")
		}
		meterID = id

		r, err := relationship.EstablishRelationship(ctx, tx, relationship.EstablishRelationshipInput{
			Origin:      relationship.FeatureRef{FeatureClass: spClass, FeatureID: in.ServicePointFeatureID},
			Destination: relationship.FeatureRef{FeatureClass: meterClass, FeatureID: meterID},
			Kind:        strptr(supportKind),
		})
		if err != nil {
			return err
		}
		rel = r
		return nil
	}); err != nil {
		return nil, err
	}

	result := &AttachResult{Meter: meter, Relationship: rel}

	// 2. wire the meter to its service point (METER T2 -> SERVICE_POINT T1).
	// Deterministic -- the host IS the target, no proximity search -- but still
	// gated and best-effort: a caller who may attach but not commit connections
	// leaves the meter unwired for a crew to wire.
	if !in.CanWire {
		result.WireNote = "attached but not wired -- needs connectivity:commit to wire the meter to its service point"
	} else if conn, err := connectivity.EstablishConnection(ctx, db, connectivity.EstablishConnectionInput{
		From: connectivity.Endpoint{FeatureClass: meterClass, FeatureID: meterID, Terminal: strptr(meterLoadTerminal)},
		To:   connectivity.Endpoint{FeatureClass: spClass, FeatureID: in.ServicePointFeatureID, Terminal: strptr(spTerminal)},
	}); err != nil {
		result.WireNote = "attached; couldn't auto-wire the meter to its service point (" + err.Error() + ") -- wire it manually"
	} else {
		result.Wired = true
		result.Connection = conn
	}

	return result, nil
}

func strptr(s string) *string { return &s }
