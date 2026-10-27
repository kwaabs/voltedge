package oms

// area_impact.go is the network-wide AREA IMPACT: how many customers are off
// supply RIGHT NOW rolled up the geographic hierarchy (region → district → …),
// across ALL open incidents at once -- the control-room "where is it worst" view,
// the sibling of the per-incident affected-area rollup. It reuses
// IncidentAffectedArea per open incident (so frozen/live fallback, meter
// handling and the territory walk are all shared, never re-implemented) and sums
// the results. Open incidents are few, so the N affected-area computations are
// fine; the console caches the result with a short staleTime.

import (
	"context"
	"fmt"
	"sort"

	"github.com/uptrace/bun"
)

// AreaImpactRow is one territory's share of the live network-wide impact, plus
// how many open incidents touch it.
type AreaImpactRow struct {
	TerritoryID   string `json:"territory_id"`
	TerritoryName string `json:"territory_name"`
	Customers     int    `json:"customers"`
	ServicePoints int    `json:"service_points"`
	Incidents     int    `json:"incidents"`
}

// AreaImpactLevel is the impact grouped by the territories at one hierarchy level.
type AreaImpactLevel struct {
	LevelCode string          `json:"level_code"`
	LevelName string          `json:"level_name"`
	LevelRank int             `json:"level_rank"`
	Rows      []AreaImpactRow `json:"rows"`
}

// AreaImpact is the network-wide outage impact: the totals across all open
// incidents plus the geographic breakdown. Unassigned is the customers/SPs on
// incidents that carry no territory (so Levels never silently drop them).
type AreaImpact struct {
	OpenIncidents      int               `json:"open_incidents"`
	TotalCustomers     int               `json:"total_customers"`
	TotalServicePoints int               `json:"total_service_points"`
	Unassigned         AreaImpactRow     `json:"unassigned"`
	Levels             []AreaImpactLevel `json:"levels"`
}

// ComputeAreaImpact rolls every OPEN incident's impact up the geographic
// hierarchy. Storm-scale (blocker #3): it does this in a FIXED number of queries
// regardless of incident count -- one for the open incidents (totals), one
// batched join of all their frozen interruptions to territories, and the small
// territory tree once -- rather than the old N-per-incident affected-area
// computations that fell over at a storm's incident count. A confirmed incident
// contributes its precise frozen per-supply territories; a still-predicted
// (unfrozen) incident contributes coarsely at its dominant territory.
func ComputeAreaImpact(ctx context.Context, db bun.IDB) (*AreaImpact, error) {
	// 1. The open incidents (totals + per-incident dominant territory for the
	//    unfrozen ones).
	var incs []struct {
		ID          string  `bun:"id"`
		TerritoryID *string `bun:"territory_id"`
		Customers   *int    `bun:"affected_customers"`
		SPs         *int    `bun:"affected_service_points"`
	}
	if err := db.NewSelect().
		ColumnExpr("id, territory_id, affected_customers, affected_service_points").
		TableExpr("oms.incidents").
		Where("restored_at IS NULL").
		Where("status <> ?", StatusCancelled).
		Scan(ctx, &incs); err != nil {
		return nil, fmt.Errorf("oms: loading open incidents for area impact: %w", err)
	}
	out := &AreaImpact{OpenIncidents: len(incs), Levels: []AreaImpactLevel{}}
	for _, i := range incs {
		out.TotalCustomers += valOrZero(i.Customers)
		out.TotalServicePoints += valOrZero(i.SPs)
	}
	if len(incs) == 0 {
		out.Unassigned = AreaImpactRow{TerritoryName: "Unassigned"}
		return out, nil
	}

	// 2. One batched join: every open incident's frozen interruptions → territory.
	var frozen []struct {
		IncidentID  string  `bun:"incident_id"`
		TerritoryID *string `bun:"territory_id"`
		Customers   int     `bun:"customers"`
	}
	if err := db.NewSelect().
		ColumnExpr("ci.incident_id AS incident_id").
		ColumnExpr("COALESCE(sp.territory_id, m.territory_id) AS territory_id").
		ColumnExpr("ci.customers AS customers").
		TableExpr("oms.customer_interruptions AS ci").
		Join("JOIN oms.incidents AS i ON i.id = ci.incident_id AND i.restored_at IS NULL AND i.status <> ?", StatusCancelled).
		Join("LEFT JOIN gis.service_point AS sp ON ci.feature_class = 'SERVICE_POINT' AND sp.id = ci.service_point_id").
		Join("LEFT JOIN gis.meter AS m ON ci.feature_class = 'METER' AND m.id = ci.service_point_id").
		Scan(ctx, &frozen); err != nil {
		return nil, fmt.Errorf("oms: loading area-impact interruptions: %w", err)
	}

	// 3. The (small) territory tree + level catalog, once.
	var terrs []struct {
		ID       string  `bun:"id"`
		ParentID *string `bun:"parent_id"`
		LevelID  string  `bun:"level_id"`
		Name     string  `bun:"name"`
	}
	if err := db.NewSelect().ColumnExpr("id, parent_id, level_id, name").TableExpr("gis.territories").Scan(ctx, &terrs); err != nil {
		return nil, fmt.Errorf("oms: loading territory tree: %w", err)
	}
	var levels []struct {
		ID   string `bun:"id"`
		Code string `bun:"code"`
		Name string `bun:"name"`
		Rank int    `bun:"rank"`
	}
	if err := db.NewSelect().ColumnExpr("id, code, name, rank").TableExpr("gis.territory_levels").Scan(ctx, &levels); err != nil {
		return nil, fmt.Errorf("oms: loading territory levels: %w", err)
	}
	type terrNode struct {
		ParentID *string
		LevelID  string
		Name     string
	}
	terrByID := make(map[string]terrNode, len(terrs))
	for _, t := range terrs {
		terrByID[t.ID] = terrNode{ParentID: t.ParentID, LevelID: t.LevelID, Name: t.Name}
	}
	type levelMeta struct {
		Code string
		Name string
		Rank int
	}
	levelByID := make(map[string]levelMeta, len(levels))
	for _, l := range levels {
		levelByID[l.ID] = levelMeta{Code: l.Code, Name: l.Name, Rank: l.Rank}
	}

	// 4. Roll each contribution up its ancestor chain, once per level.
	type cell struct {
		row       *AreaImpactRow
		incidents map[string]bool
	}
	agg := map[string]map[string]*cell{} // levelID → territoryID → cell
	roll := func(incidentID string, tid *string, customers, sps int) {
		if tid == nil || *tid == "" {
			return
		}
		cur := *tid
		seen := map[string]bool{}
		seenLevel := map[string]bool{}
		for cur != "" && !seen[cur] {
			seen[cur] = true
			t, ok := terrByID[cur]
			if !ok {
				break
			}
			if !seenLevel[t.LevelID] {
				seenLevel[t.LevelID] = true
				byTerr := agg[t.LevelID]
				if byTerr == nil {
					byTerr = map[string]*cell{}
					agg[t.LevelID] = byTerr
				}
				cl := byTerr[cur]
				if cl == nil {
					cl = &cell{row: &AreaImpactRow{TerritoryID: cur, TerritoryName: t.Name}, incidents: map[string]bool{}}
					byTerr[cur] = cl
				}
				cl.row.Customers += customers
				cl.row.ServicePoints += sps
				cl.incidents[incidentID] = true
			}
			if t.ParentID == nil {
				break
			}
			cur = *t.ParentID
		}
	}

	frozenIncidents := map[string]bool{}
	for _, f := range frozen {
		frozenIncidents[f.IncidentID] = true
		roll(f.IncidentID, f.TerritoryID, f.Customers, 1) // one SP per interruption row
	}
	// Unfrozen (still-predicted) open incidents: coarse contribution at their
	// dominant territory, so they aren't missing from the picture.
	for _, i := range incs {
		if frozenIncidents[i.ID] {
			continue
		}
		roll(i.ID, i.TerritoryID, valOrZero(i.Customers), valOrZero(i.SPs))
	}

	// 5. Ordered output + the unassigned remainder (from the shallowest level).
	minRank := int(^uint(0) >> 1)
	var minRankLevel string
	for levelID, byTerr := range agg {
		meta := levelByID[levelID]
		rows := make([]AreaImpactRow, 0, len(byTerr))
		for _, cl := range byTerr {
			cl.row.Incidents = len(cl.incidents)
			rows = append(rows, *cl.row)
		}
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Customers != rows[j].Customers {
				return rows[i].Customers > rows[j].Customers
			}
			return rows[i].TerritoryName < rows[j].TerritoryName
		})
		out.Levels = append(out.Levels, AreaImpactLevel{LevelCode: meta.Code, LevelName: meta.Name, LevelRank: meta.Rank, Rows: rows})
		if meta.Rank < minRank {
			minRank, minRankLevel = meta.Rank, levelID
		}
	}
	sort.SliceStable(out.Levels, func(i, j int) bool { return out.Levels[i].LevelRank < out.Levels[j].LevelRank })

	if minRankLevel != "" {
		assignedCust, assignedSP := 0, 0
		for _, cl := range agg[minRankLevel] {
			assignedCust += cl.row.Customers
			assignedSP += cl.row.ServicePoints
		}
		out.Unassigned = AreaImpactRow{
			TerritoryName: "Unassigned",
			Customers:     maxZero(out.TotalCustomers - assignedCust),
			ServicePoints: maxZero(out.TotalServicePoints - assignedSP),
		}
	} else {
		out.Unassigned = AreaImpactRow{TerritoryName: "Unassigned", Customers: out.TotalCustomers, ServicePoints: out.TotalServicePoints}
	}
	return out, nil
}

func valOrZero(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func maxZero(n int) int {
	if n < 0 {
		return 0
	}
	return n
}
