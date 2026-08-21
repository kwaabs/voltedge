package connectivity

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// This file builds the "suggested" half of the "suggested vs. established"
// split docs/metadata-core.md describes (and this package's doc comment
// quotes): given one feature, it proposes ranked candidate features it could
// legally connect to, without writing anything. It is the engine behind the
// Phase 18 MCP suggest_connections tool. The physical write path
// (establishConnection) is untouched; this reuses that path's own zero-trust
// oracle (LoadNetworkRules metadata + idMatches/ruleSpecificity) so a
// suggestion is rule-legal by construction, not a second guess at the rules.
//
// What it does NOT pre-check: a phase-gated rule (phase_rule =
// "phases_intersect") still validates live phase data at establish time, and
// database constraints are only enforced on the actual insert. A suggestion is
// therefore "rule-legal and nearby", not a guaranteed establish -- exactly the
// candidate-not-fact contract the architecture calls for.

const (
	// suggestDefaultLimit / suggestMaxLimit bound how many candidates a single
	// call returns, so a dense area never floods the caller.
	suggestDefaultLimit = 10
	suggestMaxLimit     = 50
	// suggestDefaultRadiusMeters is the search radius for the proximity ranking
	// when both feature classes are geometric -- generous enough to cover the
	// span between a conductor and the poles/transformers it realistically ties
	// to, without proposing the whole dataset. Callers may override it.
	suggestDefaultRadiusMeters = 1000.0
)

// SuggestInput identifies the feature to suggest connections for, plus optional
// bounds. A zero Limit/RadiusMeters falls back to the defaults above.
type SuggestInput struct {
	FeatureClass string
	FeatureID    string
	Limit        int
	RadiusMeters float64
}

// SuggestedFeature is a candidate feature a suggestion points at.
type SuggestedFeature struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
	Name         string `json:"name,omitempty"`
	Subtype      *int   `json:"subtype,omitempty"`
}

// ConnectionSuggestion is one ranked candidate connection: the feature to
// connect to, the terminals the authorizing rule implies on each side (nil when
// the rule wildcards them), the rule that makes it legal, and the straight-line
// distance in meters (nil when either class is non-geometric).
type ConnectionSuggestion struct {
	Feature         SuggestedFeature `json:"feature"`
	SourceTerminal  *string          `json:"source_terminal,omitempty"`
	Terminal        *string          `json:"terminal,omitempty"`
	NetworkRuleID   string           `json:"network_rule_id"`
	NetworkRuleName string           `json:"network_rule_name"`
	DistanceMeters  *float64         `json:"distance_meters,omitempty"`
}

// targetPattern is one legal connection shape derived from a valid network rule
// where the source feature sits on one side: it names the other side's class
// and (optional) subtype/terminal, plus the terminal implied back on the source
// side, all as metadata ids. Many patterns can point at the same target class
// (a wildcard rule and a subtype-pinned rule both applying) -- the most
// specific one that fits a concrete candidate wins, mirroring matchRule.
type targetPattern struct {
	rule             *metadata.NetworkRule
	targetClassID    string
	targetSubtypeID  *string
	targetTerminalID *string
	sourceTerminalID *string
}

// SuggestConnections proposes ranked candidate features the given feature could
// legally connect to. It reads only -- no establish, no write. Candidates are
// legal per an active network_rules row, not already connected to the source,
// not soft-deleted, not the source itself, and (when both classes are
// geometric) within the search radius, returned nearest-first.
func SuggestConnections(ctx context.Context, db bun.IDB, in SuggestInput) ([]ConnectionSuggestion, error) {
	limit := in.Limit
	if limit <= 0 {
		limit = suggestDefaultLimit
	}
	if limit > suggestMaxLimit {
		limit = suggestMaxLimit
	}
	radius := in.RadiusMeters
	if radius <= 0 {
		radius = suggestDefaultRadiusMeters
	}

	source, err := resolveEndpoint(ctx, db, endpointRef{FeatureClass: in.FeatureClass, FeatureID: in.FeatureID})
	if err != nil {
		return nil, err
	}
	sourceClassID := source.loaded.FeatureClass.ID

	rules, err := metadata.LoadNetworkRulesForClass(ctx, db, sourceClassID)
	if err != nil {
		return nil, err
	}

	// Turn every valid rule where the source sits on one side (with the source
	// side's subtype, if the rule pins one, actually matching this feature) into
	// a target pattern. A rule can match in both orientations for a self-rule.
	patterns := patternsForSource(rules, sourceClassID, source.subtypeID)
	if len(patterns) == 0 {
		return []ConnectionSuggestion{}, nil
	}

	existing, err := collectExistingConnections(ctx, db, sourceClassID, in.FeatureID)
	if err != nil {
		return nil, err
	}

	// Group patterns by target class so each target table is scanned once.
	byClass := map[string][]targetPattern{}
	order := []string{}
	for _, p := range patterns {
		if _, seen := byClass[p.targetClassID]; !seen {
			order = append(order, p.targetClassID)
		}
		byClass[p.targetClassID] = append(byClass[p.targetClassID], p)
	}

	classCodes, err := loadFeatureClassCodes(ctx, db, order)
	if err != nil {
		return nil, err
	}

	var out []ConnectionSuggestion
	for _, classID := range order {
		code := classCodes[classID]
		if code == "" {
			continue
		}
		target, err := metadata.Load(ctx, db, code)
		if err != nil {
			return nil, fmt.Errorf("connectivity: suggest: loading target class %q: %w", code, err)
		}
		if target.FeatureClass.PhysicalTable == nil {
			continue // never compiled -- no table to draw candidates from
		}

		selfID := ""
		if classID == sourceClassID {
			selfID = in.FeatureID
		}
		rows, err := nearbyCandidates(ctx, db, source.loaded.FeatureClass, target.FeatureClass, len(target.Subtypes) > 0, in.FeatureID, selfID, existing[classID], radius, limit)
		if err != nil {
			return nil, err
		}

		for _, row := range rows {
			p := bestPattern(byClass[classID], target, row.subtype)
			if p == nil {
				continue // candidate's subtype isn't covered by any applicable rule
			}
			out = append(out, ConnectionSuggestion{
				Feature: SuggestedFeature{
					FeatureClass: code,
					FeatureID:    row.id,
					Name:         row.name,
					Subtype:      row.subtype,
				},
				SourceTerminal:  terminalCode(source.loaded, p.sourceTerminalID),
				Terminal:        terminalCode(target, p.targetTerminalID),
				NetworkRuleID:   p.rule.ID,
				NetworkRuleName: p.rule.Name,
				DistanceMeters:  row.distance,
			})
		}
	}

	rankSuggestions(out)
	if len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []ConnectionSuggestion{}
	}
	return out, nil
}

// patternsForSource derives the legal target patterns from the valid rules that
// mention the source class, honoring the source side's subtype pin the same way
// ruleApplies does (a rule pinned to a subtype the source doesn't have can't
// apply). Invalid (is_valid = false) rules are skipped -- we only ever suggest
// connections a real establish would accept.
func patternsForSource(rules []metadata.NetworkRule, sourceClassID string, sourceSubtypeID *string) []targetPattern {
	var patterns []targetPattern
	for i := range rules {
		r := &rules[i]
		if !r.IsValid {
			continue
		}
		// Source as the rule's "from" side -> candidate is the "to" side.
		if r.FromFeatureClassID == sourceClassID && idMatches(r.FromSubtypeID, sourceSubtypeID) {
			patterns = append(patterns, targetPattern{
				rule: r, targetClassID: r.ToFeatureClassID,
				targetSubtypeID: r.ToSubtypeID, targetTerminalID: r.ToTerminalID,
				sourceTerminalID: r.FromTerminalID,
			})
		}
		// Source as the rule's "to" side -> candidate is the "from" side. Both
		// fire for a self-rule (from and to are the same class); that's correct.
		if r.ToFeatureClassID == sourceClassID && idMatches(r.ToSubtypeID, sourceSubtypeID) {
			patterns = append(patterns, targetPattern{
				rule: r, targetClassID: r.FromFeatureClassID,
				targetSubtypeID: r.FromSubtypeID, targetTerminalID: r.FromTerminalID,
				sourceTerminalID: r.ToTerminalID,
			})
		}
	}
	return patterns
}

// bestPattern picks, among the patterns for a candidate's class, the most
// specific one whose target-subtype pin the candidate actually satisfies -- the
// same most-specific-wins tie-break matchRule uses. Returns nil when the
// candidate's subtype is covered by no applicable pattern.
func bestPattern(patterns []targetPattern, target *metadata.Loaded, candidateSubtype *int) *targetPattern {
	var candidateSubtypeID *string
	if candidateSubtype != nil {
		if st := target.SubtypeByCode(*candidateSubtype); st != nil {
			candidateSubtypeID = &st.ID
		}
	}

	var best *targetPattern
	bestScore := -1
	for i := range patterns {
		p := &patterns[i]
		if !idMatches(p.targetSubtypeID, candidateSubtypeID) {
			continue
		}
		if score := ruleSpecificity(p.rule); score > bestScore {
			best, bestScore = p, score
		}
	}
	return best
}

// terminalCode resolves a terminal id to its human code for the response, or
// nil when the rule wildcarded the terminal (id == nil).
func terminalCode(loaded *metadata.Loaded, terminalID *string) *string {
	if terminalID == nil {
		return nil
	}
	if t := loaded.TerminalByID(*terminalID); t != nil {
		code := t.Code
		return &code
	}
	return nil
}

// candidateRow is one row from nearbyCandidates.
type candidateRow struct {
	id       string
	name     string
	subtype  *int
	distance *float64
}

// nearbyCandidates returns active, non-deleted features of the target class
// that aren't the source and aren't already connected to it. When both classes
// are geometric it ranks them by real-world distance to the source (PostGIS
// ST_Distance over geography, so meters regardless of SRID) within the radius;
// otherwise there is no spatial signal, so it returns them name-ordered with a
// nil distance. The physical table names come from trusted metadata
// (FeatureClass.PhysicalTable), the same source plausibility.go interpolates.
func nearbyCandidates(ctx context.Context, db bun.IDB, sourceClass, targetClass *metadata.FeatureClass, targetHasSubtype bool, sourceID, selfID string, exclude map[string]bool, radius float64, limit int) ([]candidateRow, error) {
	targetTable := *targetClass.PhysicalTable

	// subtypeExpr selects the candidate's subtype code when the class has one,
	// else a typed NULL so the scan target is uniform.
	subtypeExpr := "NULL::int"
	if targetHasSubtype {
		subtypeExpr = "t.subtype"
	}

	spatial := sourceClass.GeometryType != "none" && targetClass.GeometryType != "none"

	var (
		query string
		args  []any
	)
	if spatial {
		sourceTable := *sourceClass.PhysicalTable
		query = fmt.Sprintf(
			`SELECT t.id, t.name, %s,
			        ST_Distance(ST_Transform(t.geom, 4326)::geography, s.g) AS dist
			 FROM %s t
			 CROSS JOIN (SELECT ST_Transform(geom, 4326)::geography AS g FROM %s WHERE id = ?) s
			 WHERE t.deleted_at IS NULL AND t.geom IS NOT NULL
			   AND ST_DWithin(ST_Transform(t.geom, 4326)::geography, s.g, ?)`,
			subtypeExpr, targetTable, sourceTable,
		)
		args = append(args, sourceID, radius)
	} else {
		query = fmt.Sprintf(
			`SELECT t.id, t.name, %s, NULL::float8 AS dist
			 FROM %s t
			 WHERE t.deleted_at IS NULL`,
			subtypeExpr, targetTable,
		)
	}

	if selfID != "" {
		query += " AND t.id <> ?"
		args = append(args, selfID)
	}
	if ids := keysOf(exclude); len(ids) > 0 {
		query += " AND t.id NOT IN (" + placeholders(len(ids)) + ")"
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if spatial {
		query += " ORDER BY dist ASC"
	} else {
		query += " ORDER BY t.name ASC"
	}
	query += " LIMIT ?"
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("connectivity: suggest: querying %s: %w", targetTable, err)
	}
	defer rows.Close()

	var out []candidateRow
	for rows.Next() {
		var (
			id, name string
			subtype  sql.NullInt64
			dist     sql.NullFloat64
		)
		if err := rows.Scan(&id, &name, &subtype, &dist); err != nil {
			return nil, fmt.Errorf("connectivity: suggest: scanning %s: %w", targetTable, err)
		}
		row := candidateRow{id: id, name: name}
		if subtype.Valid {
			code := int(subtype.Int64)
			row.subtype = &code
		}
		if dist.Valid {
			d := dist.Float64
			row.distance = &d
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// collectExistingConnections returns, per feature-class id, the set of feature
// ids already connected to the source on either side -- so a suggestion never
// re-proposes a connection that already exists.
func collectExistingConnections(ctx context.Context, db bun.IDB, sourceClassID, sourceID string) (map[string]map[string]bool, error) {
	var rows []Connectivity
	if err := db.NewSelect().Model(&rows).
		Where("(from_feature_id = ? AND from_feature_class_id = ?) OR (to_feature_id = ? AND to_feature_class_id = ?)",
			sourceID, sourceClassID, sourceID, sourceClassID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("connectivity: suggest: existing connections: %w", err)
	}

	out := map[string]map[string]bool{}
	add := func(classID, featureID string) {
		if out[classID] == nil {
			out[classID] = map[string]bool{}
		}
		out[classID][featureID] = true
	}
	for _, r := range rows {
		// Record the endpoint that isn't the source.
		if r.FromFeatureID == sourceID && r.FromFeatureClassID == sourceClassID {
			add(r.ToFeatureClassID, r.ToFeatureID)
		} else {
			add(r.FromFeatureClassID, r.FromFeatureID)
		}
	}
	return out, nil
}

// rankSuggestions orders suggestions nearest-first, with distance-less
// (non-geometric) candidates after all measured ones, then by feature name for
// a stable order.
func rankSuggestions(s []ConnectionSuggestion) {
	sort.SliceStable(s, func(i, j int) bool {
		di, dj := s[i].DistanceMeters, s[j].DistanceMeters
		switch {
		case di != nil && dj != nil && *di != *dj:
			return *di < *dj
		case di != nil && dj == nil:
			return true
		case di == nil && dj != nil:
			return false
		default:
			return s[i].Feature.Name < s[j].Feature.Name
		}
	})
}

func keysOf(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, 0, n*2-1)
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '?')
	}
	return string(b)
}
