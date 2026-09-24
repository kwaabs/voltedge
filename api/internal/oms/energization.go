package oms

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/trace"
	"github.com/uptrace/bun"
)

// EnergizationState is an incident's LIVE de-energization: what is cut off from
// supply RIGHT NOW given the network's current switch positions -- as opposed
// to AffectedArea's frozen "who was out when we confirmed it". It is what makes
// the switching order behave like a real OMS: as the crew ticks off open/close
// steps (which flip device_state, see SetStepExecuted), this set grows when the
// device is opened and shrinks to empty when it's closed again, tracking the
// actual network rather than a snapshot.
//
// DeviceState is the blamed device's live position (OPEN/CLOSED), omitted when
// the device models none. ServicePoints is the point geometry of every
// currently-dead service point below the device (for the map); Customers totals
// their customers_served. Service is the Phase 62 consumption rollup of that
// same dead cone -- load + customers split by restoration priority, so the
// console can show "N critical customers off" (rank <= P2), not just a raw
// total. All are empty/zero whenever the device is closed (supply is on) or the
// incident carries no device.
type EnergizationState struct {
	IncidentID    string                 `json:"incident_id"`
	DeviceClass   *string                `json:"device_class"`
	DeviceID      *string                `json:"device_id"`
	DeviceState   *string                `json:"device_state,omitempty"`
	ServicePoints []AffectedServicePoint `json:"service_points"`
	Customers     int                    `json:"customers"`
	Service       *trace.ServiceRollup   `json:"service,omitempty"`
}

// IncidentEnergization resolves an incident's LIVE de-energized cone from the
// current position of its blamed device. Only an OPEN device de-energizes its
// downstream cone; a closed (or un-modelled) device is passing power, so the
// live set is empty -- which is exactly how executing the restore/close step
// clears the outage on the map. The downstream walk is barrier-respecting
// (trace.DeEnergizedCone), so any further open device below -- a sectionalizing
// or back-feed step already executed -- shrinks the cone too (partial
// restoration). An incident with no device yields an empty (non-null) set.
func IncidentEnergization(ctx context.Context, db bun.IDB, id string) (*EnergizationState, error) {
	inc, err := GetIncident(ctx, db, id)
	if err != nil {
		return nil, err
	}

	out := &EnergizationState{
		IncidentID:    id,
		DeviceClass:   inc.PredictedDeviceClass,
		DeviceID:      inc.PredictedDeviceID,
		ServicePoints: []AffectedServicePoint{},
	}
	if inc.PredictedDeviceClass == nil || inc.PredictedDeviceID == nil {
		return out, nil
	}

	state, hasState, err := deviceLiveState(ctx, db, *inc.PredictedDeviceClass, *inc.PredictedDeviceID)
	if err != nil {
		return nil, err
	}
	if hasState {
		out.DeviceState = &state
	}
	// Supply is on unless the device is genuinely open right now.
	if !hasState || state != deviceStateOpen {
		return out, nil
	}

	device := trace.FeatureRef{FeatureClass: *inc.PredictedDeviceClass, FeatureID: *inc.PredictedDeviceID}
	cone, err := trace.DeEnergizedCone(ctx, db, device)
	if err != nil {
		return nil, err
	}
	// Every load-leaf CLASS in the dead cone (Phase 63: SERVICE_POINT and METER).
	// The PRIMARY-only rule is applied downstream -- SummarizeService excludes a
	// CHECK/role-unset meter from the counts and AffectedSupplyGeometry excludes
	// it from the map -- so passing all meters here can't over-count or over-draw.
	spRefs := make([]trace.FeatureRef, 0, len(cone))
	for _, f := range cone {
		if f.FeatureClass == ServicePointClass || f.FeatureClass == MeterClass {
			spRefs = append(spRefs, f)
		}
	}

	sps, err := AffectedSupplyGeometry(ctx, db, spRefs)
	if err != nil {
		return nil, err
	}
	out.ServicePoints = sps

	// Phase 62: roll the dead cone up by restoration tier through the shared
	// trace engine -- one definition of "critical load" across trace and OMS.
	// Its total customer count (coalesce(customers_served, 1) per SP) is the
	// same figure the old dedicated SUM produced, so it doubles as Customers.
	rollup, err := trace.SummarizeService(ctx, db, spRefs)
	if err != nil {
		return nil, fmt.Errorf("oms: rolling up de-energized load for incident %s: %w", id, err)
	}
	out.Service = rollup
	out.Customers = rollup.Customers
	return out, nil
}

// deviceLiveState reads a device's live device_state attribute. ok is false --
// with no error -- when the class models no device_state field, the value is
// unset, or the device has since been purged: all "no live position to read",
// treated the same as a closed device by the caller rather than a failure.
func deviceLiveState(ctx context.Context, db bun.IDB, class, id string) (state string, ok bool, err error) {
	loaded, err := metadata.Load(ctx, db, class)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	if loaded.FieldByName(deviceStateField) == nil {
		return "", false, nil
	}
	row, err := crud.GetFeature(ctx, db, loaded, id)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return "", false, nil
		}
		return "", false, err
	}
	attrs, _ := row["attributes"].(map[string]any)
	s, _ := attrs[deviceStateField].(string)
	if s == "" {
		return "", false, nil
	}
	return s, true, nil
}
