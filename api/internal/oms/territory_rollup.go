package oms

// territory_rollup.go rolls an incident's affected supplies up the geographic
// hierarchy (utility -> region -> district -> area -> plot, migrations/0027), so
// the console can answer "how many customers are out in each DISTRICT / REGION",
// not just a flat SP count. Every geometric feature carries a territory_id (the
// deepest territory containing it); walking its ancestor chain groups it under
// each level above. The territory tables are small, so the whole tree is loaded
// once and walked in memory -- no recursive SQL. Kept here rather than importing
// api/internal/territory, the same decoupling oms keeps from faults/wam.

import (
	"context"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/trace"
	"github.com/uptrace/bun"
)

// affectedSupply is one counted supply with its FROZEN/LIVE customer count --
// the numbers that feed the affected total, so the territory rollup can never
// disagree with it.
type affectedSupply struct {
	Class     string
	ID        string
	Customers int
}

// AffectedTerritoryRow is one territory's share of an incident's affected set at
// a given level.
type AffectedTerritoryRow struct {
	TerritoryID   string `json:"territory_id"`
	TerritoryName string `json:"territory_name"`
	ServicePoints int    `json:"service_points"`
	Customers     int    `json:"customers"`
}

// AffectedTerritoryLevel is the affected set grouped by the territories at one
// hierarchy level (e.g. all regions, or all districts), biggest-first.
type AffectedTerritoryLevel struct {
	LevelCode string                 `json:"level_code"`
	LevelName string                 `json:"level_name"`
	LevelRank int                    `json:"level_rank"`
	Rows      []AffectedTerritoryRow `json:"rows"`
}

// territoryRollup groups the affected supplies by the territories above them at
// each level. A supply with no territory_id (unassigned) contributes nowhere; a
// level with no assigned supply is omitted. Levels come back ordered shallow ->
// deep (rank ascending), rows within a level ordered by customers desc.
func territoryRollup(ctx context.Context, db bun.IDB, supplies []affectedSupply) ([]AffectedTerritoryLevel, error) {
	if len(supplies) == 0 {
		return []AffectedTerritoryLevel{}, nil
	}

	refs := make([]trace.FeatureRef, len(supplies))
	for i, s := range supplies {
		refs[i] = trace.FeatureRef{FeatureClass: s.Class, FeatureID: s.ID}
	}
	terrByKey, err := supplyTerritoryIDs(ctx, db, refs)
	if err != nil {
		return nil, err
	}

	// Load the (small) territory tree and the level catalog once.
	var terrs []struct {
		ID       string  `bun:"id"`
		ParentID *string `bun:"parent_id"`
		LevelID  string  `bun:"level_id"`
		Name     string  `bun:"name"`
	}
	if err := db.NewSelect().
		ColumnExpr("id, parent_id, level_id, name").
		TableExpr("gis.territories").
		Scan(ctx, &terrs); err != nil {
		return nil, fmt.Errorf("oms: loading territory tree: %w", err)
	}
	if len(terrs) == 0 {
		return []AffectedTerritoryLevel{}, nil
	}
	var levels []struct {
		ID   string `bun:"id"`
		Code string `bun:"code"`
		Name string `bun:"name"`
		Rank int    `bun:"rank"`
	}
	if err := db.NewSelect().
		ColumnExpr("id, code, name, rank").
		TableExpr("gis.territory_levels").
		Scan(ctx, &levels); err != nil {
		return nil, fmt.Errorf("oms: loading territory levels: %w", err)
	}

	type terrNode = struct {
		ID       string
		ParentID *string
		LevelID  string
		Name     string
	}
	terrByID := make(map[string]terrNode, len(terrs))
	for _, t := range terrs {
		terrByID[t.ID] = terrNode(t)
	}
	levelByID := make(map[string]struct {
		Code string
		Name string
		Rank int
	}, len(levels))
	for _, l := range levels {
		levelByID[l.ID] = struct {
			Code string
			Name string
			Rank int
		}{l.Code, l.Name, l.Rank}
	}

	// Aggregate: levelID -> territoryID -> row.
	agg := map[string]map[string]*AffectedTerritoryRow{}
	for _, s := range supplies {
		tidPtr := terrByKey[s.Class+":"+s.ID]
		if tidPtr == nil || *tidPtr == "" {
			continue // unassigned supply contributes to no territory
		}
		cur := *tidPtr
		seen := map[string]bool{} // cycle guard for a malformed tree
		seenLevel := map[string]bool{}
		for cur != "" && !seen[cur] {
			seen[cur] = true
			t, ok := terrByID[cur]
			if !ok {
				break
			}
			// A supply counts once per LEVEL (its single ancestor at that level),
			// so region and district totals each sum to the incident total.
			if !seenLevel[t.LevelID] {
				seenLevel[t.LevelID] = true
				byTerr := agg[t.LevelID]
				if byTerr == nil {
					byTerr = map[string]*AffectedTerritoryRow{}
					agg[t.LevelID] = byTerr
				}
				row := byTerr[cur]
				if row == nil {
					row = &AffectedTerritoryRow{TerritoryID: cur, TerritoryName: t.Name}
					byTerr[cur] = row
				}
				row.ServicePoints++
				row.Customers += s.Customers
			}
			if t.ParentID == nil {
				break
			}
			cur = *t.ParentID
		}
	}

	out := make([]AffectedTerritoryLevel, 0, len(agg))
	for levelID, byTerr := range agg {
		lvl := levelByID[levelID]
		rows := make([]AffectedTerritoryRow, 0, len(byTerr))
		for _, r := range byTerr {
			rows = append(rows, *r)
		}
		sortTerritoryRows(rows)
		out = append(out, AffectedTerritoryLevel{
			LevelCode: lvl.Code,
			LevelName: lvl.Name,
			LevelRank: lvl.Rank,
			Rows:      rows,
		})
	}
	sortTerritoryLevels(out)
	return out, nil
}

// territorySubtreeIDs returns the given territory's id plus every descendant's id
// (0169), so a board filter on a region catches all its districts' incidents. The
// territory tree is small, so it is loaded and walked in memory (no recursive SQL).
func territorySubtreeIDs(ctx context.Context, db bun.IDB, rootID string) ([]string, error) {
	var terrs []struct {
		ID       string  `bun:"id"`
		ParentID *string `bun:"parent_id"`
	}
	if err := db.NewSelect().
		ColumnExpr("id, parent_id").
		TableExpr("gis.territories").
		Scan(ctx, &terrs); err != nil {
		return nil, fmt.Errorf("oms: loading territory tree: %w", err)
	}
	children := map[string][]string{}
	for _, t := range terrs {
		if t.ParentID != nil {
			children[*t.ParentID] = append(children[*t.ParentID], t.ID)
		}
	}
	out := []string{rootID}
	seen := map[string]bool{rootID: true}
	queue := []string{rootID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, ch := range children[cur] {
			if !seen[ch] {
				seen[ch] = true
				out = append(out, ch)
				queue = append(queue, ch)
			}
		}
	}
	return out, nil
}

// dominantTerritoryID returns the deepest territory holding the most affected
// customers across the given supplies -- the incident's PRIMARY territory stamp
// (0169). Each supply votes its customer count (a null/0 counted as 1) for its
// own deepest territory; the winner (ties broken by id for determinism) is
// returned. Nil when no supply carries a territory. Used by the raising paths to
// denormalise a scope/rollup key onto the incident without inverting the model.
func dominantTerritoryID(ctx context.Context, db bun.IDB, supplies []affectedSupply) (*string, error) {
	if len(supplies) == 0 {
		return nil, nil
	}
	refs := make([]trace.FeatureRef, len(supplies))
	for i, s := range supplies {
		refs[i] = trace.FeatureRef{FeatureClass: s.Class, FeatureID: s.ID}
	}
	terrByKey, err := supplyTerritoryIDs(ctx, db, refs)
	if err != nil {
		return nil, err
	}
	tally := map[string]int{}
	for _, s := range supplies {
		tid := terrByKey[s.Class+":"+s.ID]
		if tid == nil || *tid == "" {
			continue
		}
		c := s.Customers
		if c <= 0 {
			c = 1
		}
		tally[*tid] += c
	}
	best := ""
	bestN := -1
	for tid, n := range tally {
		if n > bestN || (n == bestN && tid < best) {
			best, bestN = tid, n
		}
	}
	if best == "" {
		return nil, nil
	}
	return &best, nil
}

// supplyTerritoryIDs returns each supply's territory_id (deepest containing
// territory), keyed by "class:id". Deleted supplies are NOT filtered out -- a SP
// deleted after the outage still carries the territory it was interrupted in, so
// the rollup stays consistent with the frozen affected count. A missing/null
// territory_id yields a nil entry (unassigned).
func supplyTerritoryIDs(ctx context.Context, db bun.IDB, refs []trace.FeatureRef) (map[string]*string, error) {
	var spIDs, meterIDs []string
	for _, r := range refs {
		switch r.FeatureClass {
		case ServicePointClass:
			spIDs = append(spIDs, r.FeatureID)
		case MeterClass:
			meterIDs = append(meterIDs, r.FeatureID)
		}
	}
	out := make(map[string]*string, len(refs))

	fetch := func(class, table string, ids []string) error {
		if len(ids) == 0 {
			return nil
		}
		var rows []struct {
			ID          string  `bun:"id"`
			TerritoryID *string `bun:"territory_id"`
		}
		if err := db.NewSelect().
			ColumnExpr("id, territory_id").
			TableExpr(table).
			Where("id IN (?)", bun.In(ids)).
			Scan(ctx, &rows); err != nil {
			return fmt.Errorf("oms: loading territories for %s: %w", class, err)
		}
		for _, row := range rows {
			out[class+":"+row.ID] = row.TerritoryID
		}
		return nil
	}

	if err := fetch(ServicePointClass, "gis.service_point", spIDs); err != nil {
		return nil, err
	}
	if err := fetch(MeterClass, "gis.meter", meterIDs); err != nil {
		return nil, err
	}
	return out, nil
}

func sortTerritoryRows(rows []AffectedTerritoryRow) {
	// customers desc, then name for a stable tiebreak.
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			a, b := rows[j-1], rows[j]
			less := a.Customers < b.Customers || (a.Customers == b.Customers && a.TerritoryName > b.TerritoryName)
			if !less {
				break
			}
			rows[j-1], rows[j] = rows[j], rows[j-1]
		}
	}
}

func sortTerritoryLevels(levels []AffectedTerritoryLevel) {
	// rank ascending (shallow -> deep: region before district).
	for i := 1; i < len(levels); i++ {
		for j := i; j > 0 && levels[j-1].LevelRank > levels[j].LevelRank; j-- {
			levels[j-1], levels[j] = levels[j], levels[j-1]
		}
	}
}
