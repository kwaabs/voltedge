package relationship

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// This file builds the "suggested" half of the suggested-vs-established split
// for relationships: given one feature, it proposes ranked candidate features
// it could legally relate to, without writing anything. It is the engine behind
// the Phase 18 MCP suggest_relationships tool, deliberately parallel to
// connectivity/suggest.go (relationship_classes has no terminals and no
// is_valid flag, but does have a cardinality rule to honor). It reuses the
// establish path's own oracle -- LoadRelationshipClasses metadata and
// checkCardinality -- so every candidate returned is one a real establish would
// accept at the moment of suggestion; only the best-effort plausibility warning
// and DB constraints are left to establish time.

const (
	suggestDefaultLimit        = 10
	suggestMaxLimit            = 50
	suggestDefaultRadiusMeters = 1000.0
)

// SuggestInput identifies the feature to suggest relationships for, plus
// optional bounds; a zero Limit/RadiusMeters falls back to the defaults.
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

// RelationshipSuggestion is one ranked candidate relationship: the feature to
// relate to, the role that candidate would take (origin or destination -- the
// opposite of the source's role in the relationship class), the class that
// makes it legal with its kind/cardinality, and the straight-line distance in
// meters (nil when either class is non-geometric).
type RelationshipSuggestion struct {
	Feature               SuggestedFeature `json:"feature"`
	Role                  string           `json:"role"`
	RelationshipClassID   string           `json:"relationship_class_id"`
	RelationshipClassName string           `json:"relationship_class_name"`
	Kind                  string           `json:"kind"`
	Cardinality           string           `json:"cardinality"`
	DistanceMeters        *float64         `json:"distance_meters,omitempty"`
}

// classPattern is one legal relationship shape for the source: a
// relationship_classes row plus which class the candidate comes from and the
// role it would take. sourceIsOrigin records the source's own role, so the
// cardinality check gets its (origin, destination) arguments the right way
// round.
type classPattern struct {
	class          *metadata.RelationshipClass
	candidateClass string
	candidateRole  string
	sourceIsOrigin bool
}

// SuggestRelationships proposes ranked candidate features the given feature
// could legally relate to. Read-only. Candidates are legal per a
// relationship_classes row, satisfy that class's cardinality, aren't already
// related to the source, aren't soft-deleted, aren't the source itself, and
// (when both classes are geometric) fall within the radius, nearest-first.
func SuggestRelationships(ctx context.Context, db bun.IDB, in SuggestInput) ([]RelationshipSuggestion, error) {
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

	source, err := resolveFeature(ctx, db, featureRef{FeatureClass: in.FeatureClass, FeatureID: in.FeatureID})
	if err != nil {
		return nil, err
	}
	sourceClassID := source.loaded.FeatureClass.ID

	classes, err := metadata.LoadRelationshipClassesForClass(ctx, db, sourceClassID)
	if err != nil {
		return nil, err
	}

	patterns := patternsForSource(classes, sourceClassID)
	if len(patterns) == 0 {
		return []RelationshipSuggestion{}, nil
	}

	existing, err := collectExistingRelationships(ctx, db, sourceClassID, in.FeatureID)
	if err != nil {
		return nil, err
	}

	byClass := map[string][]classPattern{}
	order := []string{}
	for _, p := range patterns {
		if _, seen := byClass[p.candidateClass]; !seen {
			order = append(order, p.candidateClass)
		}
		byClass[p.candidateClass] = append(byClass[p.candidateClass], p)
	}

	classCodes, err := loadFeatureClassCodes(ctx, db, order)
	if err != nil {
		return nil, err
	}

	var out []RelationshipSuggestion
	for _, classID := range order {
		code := classCodes[classID]
		if code == "" {
			continue
		}
		target, err := metadata.Load(ctx, db, code)
		if err != nil {
			return nil, fmt.Errorf("relationship: suggest: loading candidate class %q: %w", code, err)
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
			for _, p := range byClass[classID] {
				// Honor the class's cardinality exactly as establish would,
				// getting (origin, destination) the right way round.
				originID, destID := in.FeatureID, row.id
				if !p.sourceIsOrigin {
					originID, destID = row.id, in.FeatureID
				}
				if err := checkCardinality(ctx, db, p.class, originID, destID); err != nil {
					continue // this class's cap is exhausted for this candidate
				}
				out = append(out, RelationshipSuggestion{
					Feature: SuggestedFeature{
						FeatureClass: code,
						FeatureID:    row.id,
						Name:         row.name,
						Subtype:      row.subtype,
					},
					Role:                  p.candidateRole,
					RelationshipClassID:   p.class.ID,
					RelationshipClassName: p.class.Name,
					Kind:                  p.class.Kind,
					Cardinality:           p.class.Cardinality,
					DistanceMeters:        row.distance,
				})
			}
		}
	}

	rankSuggestions(out)
	if len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []RelationshipSuggestion{}
	}
	return out, nil
}

// patternsForSource turns every relationship class mentioning the source class
// into a candidate pattern. A class fires once per side the source is on -- both
// for a self-referential class (origin and destination are the same class).
func patternsForSource(classes []metadata.RelationshipClass, sourceClassID string) []classPattern {
	var patterns []classPattern
	for i := range classes {
		c := &classes[i]
		if c.OriginFeatureClassID == sourceClassID {
			patterns = append(patterns, classPattern{
				class: c, candidateClass: c.DestinationFeatureClassID,
				candidateRole: "destination", sourceIsOrigin: true,
			})
		}
		if c.DestinationFeatureClassID == sourceClassID {
			patterns = append(patterns, classPattern{
				class: c, candidateClass: c.OriginFeatureClassID,
				candidateRole: "origin", sourceIsOrigin: false,
			})
		}
	}
	return patterns
}

// candidateRow is one row from nearbyCandidates.
type candidateRow struct {
	id       string
	name     string
	subtype  *int
	distance *float64
}

// nearbyCandidates returns active, non-deleted features of the candidate class
// that aren't the source and aren't already related to it, ranked by distance
// when both classes are geometric (else name-ordered, nil distance). Parallel
// to connectivity/suggest.go's copy -- the two packages are deliberately not
// shared. Table names come from trusted metadata (FeatureClass.PhysicalTable).
func nearbyCandidates(ctx context.Context, db bun.IDB, sourceClass, targetClass *metadata.FeatureClass, targetHasSubtype bool, sourceID, selfID string, exclude map[string]bool, radius float64, limit int) ([]candidateRow, error) {
	targetTable := *targetClass.PhysicalTable

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
		return nil, fmt.Errorf("relationship: suggest: querying %s: %w", targetTable, err)
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
			return nil, fmt.Errorf("relationship: suggest: scanning %s: %w", targetTable, err)
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

// collectExistingRelationships returns, per feature-class id, the set of feature
// ids already related to the source on either side.
func collectExistingRelationships(ctx context.Context, db bun.IDB, sourceClassID, sourceID string) (map[string]map[string]bool, error) {
	var rows []FeatureRelationship
	if err := db.NewSelect().Model(&rows).
		Where("(origin_feature_id = ? AND origin_feature_class_id = ?) OR (destination_feature_id = ? AND destination_feature_class_id = ?)",
			sourceID, sourceClassID, sourceID, sourceClassID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("relationship: suggest: existing relationships: %w", err)
	}

	out := map[string]map[string]bool{}
	add := func(classID, featureID string) {
		if out[classID] == nil {
			out[classID] = map[string]bool{}
		}
		out[classID][featureID] = true
	}
	for _, r := range rows {
		if r.OriginFeatureID == sourceID && r.OriginFeatureClassID == sourceClassID {
			add(r.DestinationFeatureClassID, r.DestinationFeatureID)
		} else {
			add(r.OriginFeatureClassID, r.OriginFeatureID)
		}
	}
	return out, nil
}

// rankSuggestions orders suggestions nearest-first, distance-less candidates
// last, then by feature name for stability.
func rankSuggestions(s []RelationshipSuggestion) {
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
