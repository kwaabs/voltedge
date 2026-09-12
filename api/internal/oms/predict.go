package oms

import (
	"context"

	"github.com/kwaabs/voltedge/api/internal/trace"
	"github.com/uptrace/bun"
)

// ServicePointClass is the feature class the impact trace counts as a "customer
// supply". customers_served on each such feature is the per-SP customer count
// (a NULL is read as 1); see seed/service_point_example.sql.
const ServicePointClass = "SERVICE_POINT"

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

// DownstreamServicePointIDs returns the ids of every service point in the
// downstream cone of a device -- the set ConfirmIncident freezes as per-SP
// interruption rows. Same barrier-free/topological basis as AffectedCustomers.
func DownstreamServicePointIDs(ctx context.Context, db bun.IDB, deviceClass, deviceID string) ([]string, error) {
	device := trace.FeatureRef{FeatureClass: deviceClass, FeatureID: deviceID}
	sps, err := trace.DownstreamFeaturesOfClass(ctx, db, device, []string{ServicePointClass})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(sps))
	for i, sp := range sps {
		ids[i] = sp.FeatureID
	}
	return ids, nil
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

// ServicePointGeometry fetches the id/name/point of each given service point
// (skipping any that are deleted or carry no geometry). Order is by name so the
// overlay is stable; an empty id list returns an empty slice without a query.
func ServicePointGeometry(ctx context.Context, db bun.IDB, ids []string) ([]AffectedServicePoint, error) {
	if len(ids) == 0 {
		return []AffectedServicePoint{}, nil
	}
	var rows []AffectedServicePoint
	err := db.NewSelect().
		ColumnExpr("id, name").
		ColumnExpr("ST_X(geom) AS longitude").
		ColumnExpr("ST_Y(geom) AS latitude").
		TableExpr("gis.service_point").
		Where("id IN (?)", bun.In(ids)).
		Where("deleted_at IS NULL").
		Where("geom IS NOT NULL").
		OrderExpr("name").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []AffectedServicePoint{}
	}
	return rows, nil
}

// AffectedCustomers traces the full downstream cone of a device to the service
// points it feeds and totals their customers: the SP count and the sum of
// customers_served (a NULL counted as 1). Barrier-free/topological -- it counts
// everything electrically below the device regardless of live switch state,
// the deliberately conservative v1 answer (a later slice can subtract sections
// isolated by open downstream switches). Requires customers_served to have been
// compiled onto gis.service_point (seed/service_point_example.sql).
func AffectedCustomers(ctx context.Context, db bun.IDB, device trace.FeatureRef) (servicePoints int, customers int, err error) {
	sps, err := trace.DownstreamFeaturesOfClass(ctx, db, device, []string{ServicePointClass})
	if err != nil {
		return 0, 0, err
	}
	if len(sps) == 0 {
		return 0, 0, nil
	}

	ids := make([]string, len(sps))
	for i, sp := range sps {
		ids[i] = sp.FeatureID
	}

	var counts struct {
		N         int `bun:"n"`
		Customers int `bun:"customers"`
	}
	err = db.NewSelect().
		ColumnExpr("count(*) AS n").
		ColumnExpr("coalesce(sum(coalesce(customers_served, 1)), 0) AS customers").
		TableExpr("gis.service_point").
		Where("id IN (?)", bun.In(ids)).
		Where("deleted_at IS NULL").
		Scan(ctx, &counts)
	if err != nil {
		return 0, 0, err
	}
	return counts.N, counts.Customers, nil
}
