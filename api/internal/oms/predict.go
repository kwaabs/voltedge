package oms

import (
	"context"
	"sort"

	"github.com/kwaabs/voltedge/api/internal/trace"
	"github.com/uptrace/bun"
)

// The feature classes the impact trace counts as a "customer supply". Phase 62
// counted SERVICE_POINT alone; Phase 63 ("the network ends at the meter") made
// the PRIMARY METER a load-bearing leaf too, so a de-energized cone's affected
// supplies span both -- SERVICE_POINT for unmetered load, the PRIMARY meter for
// metered load. customers_served on each is the per-supply customer count (NULL
// read as 1). The counting rule (a meter counts only when meter_role=PRIMARY)
// lives in the trace package (trace.DownstreamLoadLeaves / SummarizeService), so
// OMS never re-implements it; these consts are only for building refs and the
// geometry UNION.
const (
	ServicePointClass = "SERVICE_POINT"
	MeterClass        = "METER"

	// meterRolePrimary mirrors the METER_ROLE value from seed/meter_load_leaf.sql
	// (kept local -- trace owns the counting rule; OMS needs the literal only for
	// the geometry UNION's meter_role filter, so a CHECK meter is never drawn).
	meterRolePrimary = "PRIMARY"
)

// ProtectiveDeviceClasses is the set of feature classes the prediction engine
// treats as fault-clearing devices: the upstream walk from a calling service
// point stops-rolling-up at the nearest of these, and that device is blamed for
// the outage. Kept a package var (not a const set) so it can be overridden in a
// deployment whose network models protection differently -- the same "domain
// signal hardcoded in one obvious place" precedent as trace/isolate.go's
// criticalFacilityClass. MV_SWITCH is deliberately excluded: a plain switch is a
// sectionaliser, not a protective device, so it does not clear a fault.
var ProtectiveDeviceClasses = []string{"MV_FUSE", "MV_RECLOSER", "MV_BREAKER"}

// CallLocation is one open trouble call that has a resolved service point --
// the only calls that feed the rollup (SP-optional intake means address-only
// calls wait for manual association / snap first). CallID is the
// oms.trouble_calls id; ServicePointID is the gis.service_point feature id.
type CallLocation struct {
	CallID         string
	ServicePointID string
}

// PredictedIncident is the engine's proposal for one outage: the protective
// device it blames, the calls it explains, and the downstream impact. It is a
// pure computation -- persisting it as an oms.incidents row (and clearing the
// calls) is the REST layer's job (Slice 1c). Device is nil when a call's
// service point has no protective device upstream (fed straight off an
// unmodelled source); such calls still cluster into one incident a human can
// attribute manually.
type PredictedIncident struct {
	Device                *trace.FeatureRef `json:"device"`
	CallIDs               []string          `json:"call_ids"`
	ServicePointIDs       []string          `json:"service_point_ids"`
	AffectedServicePoints int               `json:"affected_service_points"`
	AffectedCustomers     int               `json:"affected_customers"`
	Confidence            float64           `json:"confidence"`
}

// PredictIncidents rolls a batch of open, SP-resolved trouble calls up into
// predicted incidents under the single-fault hypothesis: each call is walked
// upstream to the nearest protective device, calls sharing a device are grouped
// into one incident, and that device's full downstream cone gives the affected
// counts. Calls whose service points roll up to different devices become
// separate incidents (disjoint clusters); nested/overlapping real faults are
// left for the operator to split or merge -- v1 blames the nearest device only.
//
// Confidence for a device-backed incident is the fraction of the device's
// downstream service points that actually reported (distinct calling SPs /
// affected SPs), capped at 1.0: three of three supplies calling is near
// certain, one of forty is weak. A device with no downstream SPs, or the
// no-device cluster, gets 0.
//
// Incidents are returned in first-call order (deterministic); the no-device
// cluster, if any, sorts by where its first call fell in the batch.
func PredictIncidents(ctx context.Context, db bun.IDB, calls []CallLocation) ([]PredictedIncident, error) {
	const noDevice = "" // group key for calls with no upstream protective device

	type cluster struct {
		device   *trace.FeatureRef
		callIDs  []string
		spSet    map[string]bool
		spOrder  []string
	}

	order := make([]string, 0, len(calls)) // group keys, first-seen order
	groups := make(map[string]*cluster, len(calls))

	for _, call := range calls {
		spRef := trace.FeatureRef{FeatureClass: ServicePointClass, FeatureID: call.ServicePointID}
		path, err := trace.UpstreamFeaturesOfClass(ctx, db, spRef, ProtectiveDeviceClasses)
		if err != nil {
			return nil, err
		}

		var device *trace.FeatureRef
		key := noDevice
		if len(path) > 0 {
			d := path[0] // nearest protective device
			device = &d
			key = d.FeatureClass + "|" + d.FeatureID
		}

		g, ok := groups[key]
		if !ok {
			g = &cluster{device: device, spSet: map[string]bool{}}
			groups[key] = g
			order = append(order, key)
		}
		g.callIDs = append(g.callIDs, call.CallID)
		if !g.spSet[call.ServicePointID] {
			g.spSet[call.ServicePointID] = true
			g.spOrder = append(g.spOrder, call.ServicePointID)
		}
	}

	out := make([]PredictedIncident, 0, len(order))
	for _, key := range order {
		g := groups[key]
		inc := PredictedIncident{
			Device:          g.device,
			CallIDs:         g.callIDs,
			ServicePointIDs: g.spOrder,
		}
		if g.device != nil {
			spCount, customers, err := AffectedCustomers(ctx, db, *g.device)
			if err != nil {
				return nil, err
			}
			inc.AffectedServicePoints = spCount
			inc.AffectedCustomers = customers
			if spCount > 0 {
				conf := float64(len(g.spOrder)) / float64(spCount)
				if conf > 1.0 {
					conf = 1.0
				}
				inc.Confidence = conf
			}
		}
		out = append(out, inc)
	}
	return out, nil
}

// DownstreamSupplies returns every counting load leaf (SERVICE_POINT or PRIMARY
// METER) in the downstream cone of a device, each as a trace.ServicePoint line
// carrying its class+id and frozen customer count -- the set ConfirmIncident
// freezes as interruption rows and IncidentAffectedArea falls back to for a
// still-predicted incident. Same barrier-free/topological basis as
// AffectedCustomers, through the one shared predicate in the trace package.
func DownstreamSupplies(ctx context.Context, db bun.IDB, deviceClass, deviceID string) ([]trace.ServicePoint, error) {
	device := trace.FeatureRef{FeatureClass: deviceClass, FeatureID: deviceID}
	points, _, err := trace.DownstreamLoadLeaves(ctx, db, device)
	return points, err
}

// AffectedServicePoint is one service point in an incident's impact area, with
// the point geometry the map overlay draws. Longitude/Latitude are WGS84
// (EPSG:4326), read via ST_X/ST_Y -- the same scanonly projection the trouble
// call and territory readers use (the geometry column is never auto-selected).
type AffectedServicePoint struct {
	ID        string  `json:"id" bun:"id"`
	Name      string  `json:"name" bun:"name"`
	Longitude float64 `json:"longitude" bun:"longitude"`
	Latitude  float64 `json:"latitude" bun:"latitude"`
}

// AffectedSupplyGeometry fetches the id/name/point of each given supply for the
// map overlay, drawing SERVICE_POINTs and PRIMARY METERs alike (Phase 63) --
// each ref routed to its own gis table by feature_class, skipping any that are
// deleted or carry no geometry. Meters are additionally filtered to
// meter_role='PRIMARY', so a CHECK meter that slipped into the ref set is never
// drawn (nor counted -- the rollup excludes it the same way), keeping the map
// and the counts on the same supplies. Rows are name-ordered in Go so the
// overlay is stable across the two tables; an empty ref list short-circuits.
func AffectedSupplyGeometry(ctx context.Context, db bun.IDB, refs []trace.FeatureRef) ([]AffectedServicePoint, error) {
	if len(refs) == 0 {
		return []AffectedServicePoint{}, nil
	}
	var spIDs, meterIDs []string
	for _, r := range refs {
		switch r.FeatureClass {
		case ServicePointClass:
			spIDs = append(spIDs, r.FeatureID)
		case MeterClass:
			meterIDs = append(meterIDs, r.FeatureID)
		}
	}

	rows := make([]AffectedServicePoint, 0, len(refs))
	if len(spIDs) > 0 {
		var sp []AffectedServicePoint
		if err := db.NewSelect().
			ColumnExpr("id, name").
			ColumnExpr("ST_X(geom) AS longitude").
			ColumnExpr("ST_Y(geom) AS latitude").
			TableExpr("gis.service_point").
			Where("id IN (?)", bun.In(spIDs)).
			Where("deleted_at IS NULL").
			Where("geom IS NOT NULL").
			Scan(ctx, &sp); err != nil {
			return nil, err
		}
		rows = append(rows, sp...)
	}
	if len(meterIDs) > 0 {
		var m []AffectedServicePoint
		if err := db.NewSelect().
			ColumnExpr("id, name").
			ColumnExpr("ST_X(geom) AS longitude").
			ColumnExpr("ST_Y(geom) AS latitude").
			TableExpr("gis.meter").
			Where("id IN (?)", bun.In(meterIDs)).
			Where("meter_role = ?", meterRolePrimary).
			Where("deleted_at IS NULL").
			Where("geom IS NOT NULL").
			Scan(ctx, &m); err != nil {
			return nil, err
		}
		rows = append(rows, m...)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

// AffectedCustomers traces the full downstream cone of a device to the supplies
// it feeds and totals their customers: the affected-supply count and the sum of
// customers_served (a NULL counted as 1) over every counting load leaf -- a
// SERVICE_POINT or a PRIMARY METER (Phase 63). Barrier-free/topological through
// the one shared trace predicate (trace.DownstreamLoadLeaves), so it counts
// everything electrically below the device regardless of live switch state --
// the deliberately conservative v1 answer (a later slice can subtract sections
// isolated by open downstream switches) -- and can never drift from what a
// downstream service trace reports for the same device.
func AffectedCustomers(ctx context.Context, db bun.IDB, device trace.FeatureRef) (servicePoints int, customers int, err error) {
	_, rollup, err := trace.DownstreamLoadLeaves(ctx, db, device)
	if err != nil {
		return 0, 0, err
	}
	if rollup == nil {
		return 0, 0, nil
	}
	return rollup.ServicePoints, rollup.Customers, nil
}
