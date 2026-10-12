package crud

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/drafts"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/uptrace/bun"
)

// maxAggregateGroups caps how many GROUP BY rows an aggregate returns -- a
// generous ceiling (a real "count by <field>" against a bounded-cardinality
// column never approaches it), there only so a group-by on a high-cardinality
// column (say a free-text name) can't hand back an unbounded result set. The
// Total in the response is always the true count across every group, so a
// capped Groups list never misrepresents the whole.
const maxAggregateGroups = 1000

// aggSQL maps the secondary aggregate function names the API accepts to their
// SQL. count isn't here -- a count always comes back, so an explicit "count"
// needs no extra column (see aggregateExpr).
var aggSQL = map[string]string{"sum": "SUM", "avg": "AVG", "min": "MIN", "max": "MAX"}

// normalizeScalar mirrors scanRows' []byte -> string fix for a single scanned
// value: pgdriver hands some column types back as []byte when scanned into a
// generic any, which encoding/json would base64-encode. A group-by value is
// scanned generically (its type is only known at runtime), so it needs the same
// normalization before it goes into the JSON response.
func normalizeScalar(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

// aggregateExpr builds the SQL for the optional secondary aggregate, or ""
// when none was asked for (or an explicit "count", which is always returned
// anyway). The field is whitelisted through columnDataType and constrained to
// numeric/integer -- a sum/avg/min/max over text or a flag is almost always a
// mistake worth a 400. The result is cast to double precision so every function
// scans back as a float8 regardless of the column's own type (integer min/max,
// numeric sum), keeping the scan side uniform.
func aggregateExpr(loaded *metadata.Loaded, spec *aggregateSpec) (string, error) {
	if spec == nil {
		return "", nil
	}
	fn := strings.ToLower(strings.TrimSpace(spec.Fn))
	if fn == "" || fn == "count" {
		return "", nil
	}
	sqlFn, ok := aggSQL[fn]
	if !ok {
		return "", invalid("unknown aggregate function %q", spec.Fn)
	}
	dataType, ok := columnDataType(loaded, spec.Field)
	if !ok {
		return "", invalid("unknown or non-aggregatable field %q on feature class %q", spec.Field, loaded.FeatureClass.Code)
	}
	if dataType != "numeric" && dataType != "integer" {
		return "", invalid("aggregate %q needs a numeric field, but %q is a %s field", fn, spec.Field, dataType)
	}
	return fmt.Sprintf("(%s(%s))::double precision", sqlFn, quoteIdent(spec.Field)), nil
}

// spatialScopeWhere turns the request's optional spatial scope into a WHERE
// fragment plus its bind args, or ("", nil, nil) when there's no scope. The
// territory shape expands the given territory to its subtree (DescendantIDs,
// matching the deepest-territory-wins stamping that a plain id match would miss)
// and filters territory_id against the set; an unknown territory yields "1 = 0"
// (match nothing) rather than silently dropping the constraint. The radius shape
// is a metric ST_DWithin against the class geometry transformed to WGS84
// geography, the same construction connectivity/relationship proximity uses. A
// geometry-less class can satisfy neither and 400s.
func spatialScopeWhere(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, scope *querySpatialScope) (string, []any, error) {
	if scope == nil {
		return "", nil, nil
	}
	kind := strings.ToLower(strings.TrimSpace(scope.Kind))
	if kind == "" || kind == "none" {
		return "", nil, nil
	}

	// The circuit scope is NON-spatial -- it narrows by the Phase-37 circuit_id
	// membership column, not geometry -- so it is handled before the geometry guard
	// the two spatial shapes require. A class that carries no circuit_id column
	// (most don't) matches nothing, the same "1 = 0" the unknown-territory case uses:
	// in an all-classes breakdown a non-circuit class then contributes zero rather
	// than erroring the whole tally.
	if kind == "circuit" {
		if strings.TrimSpace(scope.CircuitID) == "" {
			return "", nil, invalid("a circuit scope needs a circuit_id")
		}
		if _, ok := columnDataType(loaded, "circuit_id"); !ok {
			return "1 = 0", nil, nil
		}
		return "circuit_id::text = ?", []any{strings.TrimSpace(scope.CircuitID)}, nil
	}

	if loaded.FeatureClass.GeometryType == "none" {
		return "", nil, invalid("feature class %q has no geometry, so it can't be filtered by %s", loaded.FeatureClass.Code, kind)
	}

	switch kind {
	case "territory":
		if strings.TrimSpace(scope.TerritoryID) == "" {
			return "", nil, invalid("a territory scope needs a territory_id")
		}
		ids, err := territory.DescendantIDs(ctx, db, []string{scope.TerritoryID})
		if err != nil {
			return "", nil, err
		}
		if len(ids) == 0 {
			// Unknown territory -> match nothing, not everything.
			return "1 = 0", nil, nil
		}
		placeholders := make([]string, len(ids))
		args := make([]any, len(ids))
		for i := range ids {
			placeholders[i] = "?"
			args[i] = ids[i]
		}
		return "territory_id::text IN (" + strings.Join(placeholders, ", ") + ")", args, nil

	case "radius":
		if len(scope.Center) != 2 {
			return "", nil, invalid("a radius scope needs a center of [longitude, latitude]")
		}
		if scope.RadiusM <= 0 {
			return "", nil, invalid("a radius scope needs a positive radius_m")
		}
		lon, lat := scope.Center[0], scope.Center[1]
		if lon < -180 || lon > 180 || lat < -90 || lat > 90 {
			return "", nil, invalid("a radius center must have longitude in [-180, 180] and latitude in [-90, 90]")
		}
		// geom is in the class SRID; transform to 4326 geography so the radius is
		// true metres, matching connectivity/suggest.go's proximity predicate. The
		// geom IS NOT NULL guard covers a geometry_optional class with unplaced rows.
		clause := "geom IS NOT NULL AND ST_DWithin(ST_Transform(geom, 4326)::geography, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?)"
		return clause, []any{lon, lat, scope.RadiusM}, nil

	default:
		return "", nil, invalid("unknown scope kind %q", scope.Kind)
	}
}

// aggregateWhere assembles the shared WHERE for an aggregate: the always-on
// soft-delete guard, the user's own filter tree (buildGroup, the same builder
// /query uses), the Phase 58 territory read-enforcement predicate, the
// Private-drafts visibility predicate, and the optional spatial scope. It's the
// single narrowing applied identically to both the per-group query and the
// true-total query, so a Total can never disagree with the sum of its groups.
//
// Note the row-level private pending-edit overlay (overlayFeatures) is NOT
// applied here: it rewrites individual attribute values / hides a privately
// deleted row, which a coarse COUNT/GROUP BY can't meaningfully reflect. An
// aggregate counts the public rows a scoped user may see, which is the right
// denominator for the analysis panel.
func aggregateWhere(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, req aggregateRequest) (string, []any, error) {
	where := []string{"deleted_at IS NULL"}
	args := make([]any, 0, len(req.Conditions)+4)

	// The user's filter tree -- request is itself the root group, same as /query.
	root := queryGroup{Match: req.Match, Conditions: req.Conditions, Groups: req.Groups}
	condSQL, condArgs, err := buildGroup(loaded, root, 0)
	if err != nil {
		return "", nil, err
	}
	if condSQL != "" {
		where = append(where, condSQL)
		args = append(args, condArgs...)
	}

	// Phase 58 spatial read enforcement (a no-op unless enforcement is on and the
	// user is scoped; fail-closed for a scoped-to-nothing user).
	scopeIDs, scopeApply, err := readScope(ctx, db, loaded)
	if err != nil {
		return "", nil, err
	}
	if clause, scopeArgs := scopeWhere(scopeIDs, scopeApply); clause != "" {
		where = append(where, clause)
		args = append(args, scopeArgs...)
	}

	// Private-drafts (Phase 61): hide never-published features from anyone but
	// their author and management chain.
	draftAuthors, draftApply, err := drafts.VisibleAuthors(ctx, db)
	if err != nil {
		return "", nil, err
	}
	if clause, draftArgs := drafts.WhereClause(draftAuthors, draftApply, ""); clause != "" {
		where = append(where, clause)
		args = append(args, draftArgs...)
	}

	// The user's optional spatial scope (territory / radius).
	spatialSQL, spatialArgs, err := spatialScopeWhere(ctx, db, loaded, req.Scope)
	if err != nil {
		return "", nil, err
	}
	if spatialSQL != "" {
		where = append(where, spatialSQL)
		args = append(args, spatialArgs...)
	}

	return strings.Join(where, " AND "), args, nil
}

// aggregateFeatures runs a structured aggregate query: the same filter/scope
// narrowing as a query, folded into counts. With no GroupBy it returns one
// overall Total (and the overall secondary aggregate if asked); with a GroupBy
// it returns one bucket per distinct value ordered by count, plus a separately
// computed true Total so a Limit-capped Groups list never understates the whole.
func aggregateFeatures(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, req aggregateRequest) (*aggregateResponse, error) {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return nil, err
	}

	whereSQL, args, err := aggregateWhere(ctx, db, loaded, req)
	if err != nil {
		return nil, err
	}

	aggExpr, err := aggregateExpr(loaded, req.Aggregate)
	if err != nil {
		return nil, err
	}

	// No group_by: a single overall total (plus the overall aggregate if any).
	if strings.TrimSpace(req.GroupBy) == "" {
		selectExpr := "COUNT(*)"
		if aggExpr != "" {
			selectExpr += ", " + aggExpr
		}
		q := fmt.Sprintf("SELECT %s FROM %s WHERE %s", selectExpr, table, whereSQL)
		row := db.QueryRowContext(ctx, q, args...)

		resp := &aggregateResponse{Groups: []aggregateGroup{}}
		if aggExpr != "" {
			var agg sql.NullFloat64
			if err := row.Scan(&resp.Total, &agg); err != nil {
				return nil, fmt.Errorf("crud: aggregate: %w", err)
			}
			if agg.Valid {
				v := agg.Float64
				resp.Agg = &v
			}
		} else if err := row.Scan(&resp.Total); err != nil {
			return nil, fmt.Errorf("crud: aggregate: %w", err)
		}
		return resp, nil
	}

	// group_by present: validate and quote the column (the whitelist that keeps a
	// user-supplied field name out of the SQL unchecked), then GROUP BY it.
	if _, ok := columnDataType(loaded, req.GroupBy); !ok {
		return nil, invalid("unknown or non-groupable field %q on feature class %q", req.GroupBy, loaded.FeatureClass.Code)
	}
	groupCol := quoteIdent(req.GroupBy)

	limit := maxAggregateGroups
	if req.Limit > 0 && req.Limit < maxAggregateGroups {
		limit = req.Limit
	}

	selectExpr := groupCol + " AS grp, COUNT(*) AS grp_count"
	if aggExpr != "" {
		selectExpr += ", " + aggExpr + " AS grp_agg"
	}
	// Biggest buckets first; grp as a stable tiebreak (NULLS LAST so an
	// unset-value bucket sorts to the end rather than jumping the list).
	q := fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s GROUP BY %s ORDER BY grp_count DESC, %s ASC NULLS LAST LIMIT ?",
		selectExpr, table, whereSQL, groupCol, groupCol,
	)
	groupArgs := append(append([]any{}, args...), limit)

	rows, err := db.QueryContext(ctx, q, groupArgs...)
	if err != nil {
		return nil, fmt.Errorf("crud: aggregate: %w", err)
	}
	defer rows.Close()

	resp := &aggregateResponse{Groups: []aggregateGroup{}}
	for rows.Next() {
		var gval any
		g := aggregateGroup{}
		if aggExpr != "" {
			var agg sql.NullFloat64
			if err := rows.Scan(&gval, &g.Count, &agg); err != nil {
				return nil, fmt.Errorf("crud: aggregate scan: %w", err)
			}
			if agg.Valid {
				v := agg.Float64
				g.Agg = &v
			}
		} else if err := rows.Scan(&gval, &g.Count); err != nil {
			return nil, fmt.Errorf("crud: aggregate scan: %w", err)
		}
		g.Value = normalizeScalar(gval)
		resp.Groups = append(resp.Groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("crud: aggregate rows: %w", err)
	}

	// True total across every group, computed separately so a Limit-capped Groups
	// list doesn't understate it (summing the returned counts would).
	totalQ := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s", table, whereSQL)
	if err := db.QueryRowContext(ctx, totalQ, args...).Scan(&resp.Total); err != nil {
		return nil, fmt.Errorf("crud: aggregate total: %w", err)
	}
	return resp, nil
}

// AggregateScope is the exported spatial-scope shape AggregateFeatures accepts --
// the same territory/radius narrowing the REST body carries as querySpatialScope,
// re-declared with exported fields so the agenttools front door can build one
// without reaching into this package's wire types. Kind is "none"/"territory"/
// "radius"/"circuit"; TerritoryID applies to territory, Center ([lon, lat]) +
// RadiusM (true metres) to radius, CircuitID to circuit.
type AggregateScope struct {
	Kind        string
	TerritoryID string
	Center      []float64
	RadiusM     float64
	CircuitID   string
}

// AggregateOptions is the exported request for AggregateFeatures, the aggregate
// engine's non-REST door (the MCP/assistant tool). It exposes the aggregation
// (count / count-by-field / an optional numeric sum-avg-min-max) and the spatial
// scope, but not the raw AND/OR filter tree the REST body carries -- that tree
// has no MCP surface today (search_features exposes only name/status), so the
// aggregate tool stays consistent with it. An empty GroupBy yields one overall
// total; AggFn "" or "count" means no secondary aggregate.
type AggregateOptions struct {
	GroupBy  string
	AggFn    string
	AggField string
	Scope    *AggregateScope
	Limit    int
}

// AggregateFeatures is the exported entry point onto the same aggregate engine
// the REST /aggregate route runs, for the agenttools MCP/assistant door. It
// translates the exported options into the internal request the engine consumes
// and returns the same response shape (boxed as any so the unexported response
// type doesn't leak into this signature). Every narrowing the REST path applies
// -- soft-delete, Phase 58 territory read-enforcement, private-drafts visibility,
// and the spatial scope -- is applied identically, since it shares aggregateWhere.
func AggregateFeatures(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, opts AggregateOptions) (any, error) {
	req := aggregateRequest{
		GroupBy: opts.GroupBy,
		Limit:   opts.Limit,
	}
	if strings.TrimSpace(opts.AggFn) != "" {
		req.Aggregate = &aggregateSpec{Fn: opts.AggFn, Field: opts.AggField}
	}
	if opts.Scope != nil {
		req.Scope = &querySpatialScope{
			Kind:        opts.Scope.Kind,
			TerritoryID: opts.Scope.TerritoryID,
			Center:      opts.Scope.Center,
			RadiusM:     opts.Scope.RadiusM,
			CircuitID:   opts.Scope.CircuitID,
		}
	}
	return aggregateFeatures(ctx, db, loaded, req)
}
