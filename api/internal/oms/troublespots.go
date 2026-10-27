package oms

// troublespots.go is the GEOGRAPHIC lens on open trouble calls (OMS v2): cluster
// the located, open calls by proximity into "trouble spots" a crew can be sent to
// RIGHT NOW -- before (or without) prediction finding a device. This is the
// degraded-mode detector for the un-telemetered network: calls tell you WHERE
// something is wrong (a cluster), and the network later tells you how big (a
// device incident). It fixes prediction's weakness that every no-device call is
// lumped into ONE "unattributed" incident regardless of where it is: here, three
// clusters in three neighbourhoods are three separate, dispatchable spots.
//
// Clustering is single-linkage (union-find) by great-circle distance: any two
// calls within eps metres join the same spot, so a street of reports chains into
// one spot. Open located calls are few, so the O(n^2) pairing is cheap and done
// in Go (precise metres) rather than ST_ClusterDBSCAN (degree-unit eps).

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/uptrace/bun"
)

// DefaultTroubleSpotEpsM is the join distance for clustering open calls into a
// trouble spot: calls within this many metres of each other are one spot. ~250 m
// groups a street/block of reports without merging separate neighbourhoods.
const DefaultTroubleSpotEpsM = 250.0

// TroubleSpot is one geographic cluster of open trouble calls -- a place a crew
// can be dispatched to. CallIDs are the member calls; ServicePointIDs the distinct
// resolved supplies (for the affected estimate on dispatch). Centroid + RadiusM
// locate and size it; TerritoryID/Name is its dominant territory.
type TroubleSpot struct {
	CallIDs          []string   `json:"call_ids"`
	ServicePointIDs  []string   `json:"service_point_ids"`
	CallCount        int        `json:"call_count"`
	CentroidLng      float64    `json:"centroid_lng"`
	CentroidLat      float64    `json:"centroid_lat"`
	RadiusM          float64    `json:"radius_m"`
	TerritoryID      *string    `json:"territory_id"`
	TerritoryName    *string    `json:"territory_name"`
	DominantCallType string     `json:"dominant_call_type"`
	HasAMI           bool       `json:"has_ami"`
	EarliestReported time.Time  `json:"earliest_reported_at"`
	LatestReported   time.Time  `json:"latest_reported_at"`
}

type openCall struct {
	ID         string    `bun:"id"`
	SP         *string   `bun:"service_point_id"`
	CallerRef  *string   `bun:"caller_ref"`
	CallType   string    `bun:"call_type"`
	ReportedAt time.Time `bun:"reported_at"`
	Lng        float64   `bun:"lng"`
	Lat        float64   `bun:"lat"`
	Cluster    int       `bun:"cluster"`
}

// ClusterTroubleSpots clusters the open, LOCATED trouble calls (those with a
// point) into trouble spots within epsM metres, biggest-first. An address-only
// call with no point can't be placed, so it is excluded (worked from the call
// queue instead). epsM <= 0 uses the default.
//
// Storm-scale (blocker #3): the clustering is done IN THE DATABASE by
// ST_ClusterDBSCAN (R-tree backed, over the GiST index on location), not the old
// O(n^2) pairwise Go walk that fell over at a storm's call volume. minpoints=1
// makes it single-linkage connected components (a lone call is its own 1-call
// spot) -- the same grouping as before. eps is in degrees (epsM converted at the
// equator); a coarse approximation that ignores a degree of longitude narrowing
// with latitude, which is fine for grouping a street of reports.
func ClusterTroubleSpots(ctx context.Context, db bun.IDB, epsM float64) ([]TroubleSpot, error) {
	if epsM <= 0 {
		epsM = DefaultTroubleSpotEpsM
	}
	epsDeg := epsM / 111320.0

	var calls []openCall
	if err := db.NewSelect().
		ColumnExpr("id, service_point_id, caller_ref, call_type, reported_at").
		ColumnExpr("ST_X(location) AS lng").
		ColumnExpr("ST_Y(location) AS lat").
		ColumnExpr("ST_ClusterDBSCAN(location, ?, 1) OVER () AS cluster", epsDeg).
		TableExpr("oms.trouble_calls").
		Where("status = ?", CallStatusOpen).
		Where("location IS NOT NULL").
		Scan(ctx, &calls); err != nil {
		return nil, fmt.Errorf("oms: clustering open calls: %w", err)
	}
	if len(calls) == 0 {
		return []TroubleSpot{}, nil
	}

	// Group by the DB-assigned cluster id, first-seen order preserved.
	groups := map[int][]openCall{}
	order := make([]int, 0)
	for _, c := range calls {
		if _, ok := groups[c.Cluster]; !ok {
			order = append(order, c.Cluster)
		}
		groups[c.Cluster] = append(groups[c.Cluster], c)
	}

	spots := make([]TroubleSpot, 0, len(groups))
	for _, cid := range order {
		spots = append(spots, buildSpot(groups[cid]))
	}

	// Dominant territory per spot, from its resolved SPs.
	if err := fillSpotTerritories(ctx, db, spots); err != nil {
		return nil, err
	}

	// Biggest (most calls) first, then oldest -- "what needs a crew now".
	sort.SliceStable(spots, func(i, j int) bool {
		if spots[i].CallCount != spots[j].CallCount {
			return spots[i].CallCount > spots[j].CallCount
		}
		return spots[i].EarliestReported.Before(spots[j].EarliestReported)
	})
	return spots, nil
}

// buildSpot aggregates one cluster's member calls into a TroubleSpot (centroid,
// radius, call-type, AMI flag, distinct SPs, report window). Non-nil CallIDs /
// ServicePointIDs so the JSON carries [] not null for a spot with no resolved SP.
func buildSpot(members []openCall) TroubleSpot {
	spot := TroubleSpot{CallCount: len(members), CallIDs: []string{}, ServicePointIDs: []string{}}
	typeCount := map[string]int{}
	spSet := map[string]bool{}
	var sumLng, sumLat float64
	for _, c := range members {
		spot.CallIDs = append(spot.CallIDs, c.ID)
		sumLng += c.Lng
		sumLat += c.Lat
		typeCount[c.CallType]++
		if c.CallerRef != nil && *c.CallerRef == lastGaspCallerRef {
			spot.HasAMI = true // an AMI last-gasp corroborates this spot
		}
		if c.SP != nil && *c.SP != "" && !spSet[*c.SP] {
			spSet[*c.SP] = true
			spot.ServicePointIDs = append(spot.ServicePointIDs, *c.SP)
		}
		if spot.EarliestReported.IsZero() || c.ReportedAt.Before(spot.EarliestReported) {
			spot.EarliestReported = c.ReportedAt
		}
		if c.ReportedAt.After(spot.LatestReported) {
			spot.LatestReported = c.ReportedAt
		}
	}
	n := float64(len(members))
	spot.CentroidLng = sumLng / n
	spot.CentroidLat = sumLat / n
	for _, c := range members {
		if d := haversineM(spot.CentroidLat, spot.CentroidLng, c.Lat, c.Lng); d > spot.RadiusM {
			spot.RadiusM = d
		}
	}
	spot.DominantCallType = dominantKey(typeCount)
	return spot
}

// fillSpotTerritories stamps each spot's dominant territory (from its SPs) and the
// territory's name. Spots with no resolved SP get none.
func fillSpotTerritories(ctx context.Context, db bun.IDB, spots []TroubleSpot) error {
	nameByID := map[string]string{}
	for i := range spots {
		if len(spots[i].ServicePointIDs) == 0 {
			continue
		}
		supplies := make([]affectedSupply, len(spots[i].ServicePointIDs))
		for k, id := range spots[i].ServicePointIDs {
			supplies[k] = affectedSupply{Class: ServicePointClass, ID: id, Customers: 1}
		}
		tid, err := dominantTerritoryID(ctx, db, supplies)
		if err != nil {
			return err
		}
		spots[i].TerritoryID = tid
		if tid != nil {
			nameByID[*tid] = ""
		}
	}
	if len(nameByID) == 0 {
		return nil
	}
	ids := make([]string, 0, len(nameByID))
	for id := range nameByID {
		ids = append(ids, id)
	}
	var rows []struct {
		ID   string `bun:"id"`
		Name string `bun:"name"`
	}
	if err := db.NewSelect().
		ColumnExpr("id, name").
		TableExpr("gis.territories").
		Where("id IN (?)", bun.In(ids)).
		Scan(ctx, &rows); err != nil {
		return fmt.Errorf("oms: resolving spot territory names: %w", err)
	}
	for _, r := range rows {
		nameByID[r.ID] = r.Name
	}
	for i := range spots {
		if spots[i].TerritoryID != nil {
			if n := nameByID[*spots[i].TerritoryID]; n != "" {
				name := n
				spots[i].TerritoryName = &name
			}
		}
	}
	return nil
}

func dominantKey(counts map[string]int) string {
	best := ""
	bestN := -1
	for k, n := range counts {
		if n > bestN || (n == bestN && k < best) {
			best, bestN = k, n
		}
	}
	return best
}

// haversineM is the great-circle distance in metres between two WGS84 points.
func haversineM(lat1, lng1, lat2, lng2 float64) float64 {
	const r = 6371000.0 // mean earth radius, metres
	p1 := lat1 * math.Pi / 180
	p2 := lat2 * math.Pi / 180
	dp := (lat2 - lat1) * math.Pi / 180
	dl := (lng2 - lng1) * math.Pi / 180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return r * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
