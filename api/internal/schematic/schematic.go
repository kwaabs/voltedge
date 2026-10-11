// Package schematic builds the data behind the map's on-screen "Schematic
// view" (the operations one-line overlay): for a map viewport it returns every
// in-view feature as a node at a representative geographic point, plus the
// connectivity edges among them, plus the positions of any out-of-view features
// those edges reach (so the client can draw off-page stubs in the right
// direction). It is a READ-ONLY, viewport-scoped projection of data the map
// already shows -- it writes nothing and runs no analysis. The three S1 overlay
// attributes (device_state, circuit_ref, rated_kva/demand_kw) ride along on each
// node when the class carries them; energisation (trace) and loading (load-flow)
// are deferred to later slices.
package schematic

import (
	"context"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// DefaultMaxNodes caps how many in-view features a single schematic request will
// render. The schematic is a readability tool, not a bulk export: past a few
// hundred symbols a geographic one-line is unreadable anyway, and -- more
// importantly -- a PARTIAL schematic is misleading (a dropped feature silently
// breaks the connectivity the view exists to show), so the view fails loud
// (Truncated=true, no nodes) rather than ship a lie. The client's scale gate
// normally keeps requests well under this; this is the server-side backstop
// (and the ceiling on a hostile whole-world bbox). Override with
// SCHEMATIC_MAX_NODES.
const DefaultMaxNodes = 800

// schematicAttrCols are the S1 overlay fields read off a node when its class
// carries them as a real column: live switch state, derived circuit label, and
// the raw rating/demand numbers. Detected per table via information_schema so a
// class without a given column simply omits it (no SQL error) -- most classes
// carry none of these.
var schematicAttrCols = []string{"device_state", "circuit_ref", "rated_kva", "demand_kw"}

// optionalCols is every column the per-class read probes for before selecting
// it: subtype (only present when the class has subtypes) plus the overlay attrs.
var optionalCols = append([]string{"subtype"}, schematicAttrCols...)

// BBox is a lon/lat (EPSG:4326) viewport, the same shape the map publishes to
// its view store and the same the geographic export takes (crud.ExportBBox).
type BBox struct {
	MinX, MinY, MaxX, MaxY float64
}

// Node is one in-view feature, drawn as a symbol at (Lon,Lat). GeometryType and
// NetworkRole let the client decide how to render it -- notably collapsing a
// line/edge conductor into a single connector between its two neighbours rather
// than drawing it as a standalone node. The overlay attributes are pointers so
// an absent column serializes as omitted, not a zero value.
type Node struct {
	FeatureClass string   `json:"feature_class"`
	FeatureID    string   `json:"feature_id"`
	Subtype      *int     `json:"subtype,omitempty"`
	Lon          float64  `json:"lon"`
	Lat          float64  `json:"lat"`
	GeometryType string   `json:"geometry_type"`
	NetworkRole  string   `json:"network_role"`
	// FunctionalRole / TraceTier are class-level (migrations 0091 / 0126), carried
	// so a consumer that lays the graph out topologically can root at sources
	// (functional_role='source') and order by tier (lower = closer to the source).
	FunctionalRole *string `json:"functional_role,omitempty"`
	TraceTier      *int    `json:"trace_tier,omitempty"`
	// Network is the class's nominal voltage namespace ("MV"/"LV", migration 0064),
	// carried so a one-line can distinguish MV from LV conductors by weight/colour.
	Network     *string `json:"network,omitempty"`
	DeviceState *string `json:"device_state,omitempty"`
	CircuitRef   *string  `json:"circuit_ref,omitempty"`
	RatedKVA     *float64 `json:"rated_kva,omitempty"`
	DemandKW     *float64 `json:"demand_kw,omitempty"`
}

// Edge is one connectivity relationship between two features, by feature id. The
// client resolves each end to a Position (in Positions) to draw the connector.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Position is where a feature sits, for either end of an edge. InView is false
// for a feature an edge reaches that lies outside the viewport -- the client
// draws that end as an off-page stub toward (Lon,Lat).
type Position struct {
	FeatureClass string  `json:"feature_class"`
	Lon          float64 `json:"lon"`
	Lat          float64 `json:"lat"`
	InView       bool    `json:"in_view"`
}

// View is the whole schematic payload for one viewport.
type View struct {
	Nodes     []Node              `json:"nodes"`
	Edges     []Edge              `json:"edges"`
	Positions map[string]Position `json:"positions"`
	Truncated bool                `json:"truncated"`
	NodeCount int                 `json:"node_count"`
}

// classMeta is the per-class projection BuildView needs, assembled once from the
// metadata catalog so the per-class reads carry no further metadata round trips.
type classMeta struct {
	id           string
	code         string
	table        string // qualified, e.g. "gis.mv_switch" -- trusted (compiler-generated)
	tableName    string // bare, e.g. "mv_switch" -- for the information_schema probe
	geometryType   string
	networkRole    string
	functionalRole *string
	traceTier      *int
	network        *string
	srid           int
	optional       map[string]bool // which optionalCols this table actually has
}

// BuildView assembles the schematic for one viewport. maxNodes <= 0 falls back
// to DefaultMaxNodes. When the in-view feature count exceeds the cap it returns
// a Truncated view with no nodes/edges rather than a misleading partial one.
func BuildView(ctx context.Context, db bun.IDB, bbox BBox, maxNodes int) (*View, error) {
	if maxNodes <= 0 {
		maxNodes = DefaultMaxNodes
	}

	// The same territory + private-draft filters the map's tiles apply, resolved
	// once for the whole request so the schematic never surfaces a feature the map
	// would hide from this user.
	scope, err := resolveScope(ctx, db)
	if err != nil {
		return nil, err
	}

	classes, err := geometricClasses(ctx, db)
	if err != nil {
		return nil, err
	}
	// Index geometric classes by id so a connectivity row (which carries class
	// ids) resolves to a table. A connectivity endpoint in a geometry-less class
	// simply has no entry here -- its position can't be drawn, so its edge is
	// dropped later, which is correct for a geographic schematic.
	byID := make(map[string]*classMeta, len(classes))
	for i := range classes {
		byID[classes[i].id] = &classes[i]
	}

	// Read every geometric class's in-view features, accumulating nodes and a
	// position index. A running cap check fails loud the moment the viewport is
	// too dense to draw honestly.
	nodes := make([]Node, 0, 256)
	positions := make(map[string]Position)
	for i := range classes {
		cm := &classes[i]
		classNodes, err := readClassNodes(ctx, db, cm, bbox, maxNodes+1, scope)
		if err != nil {
			return nil, err
		}
		for _, n := range classNodes {
			nodes = append(nodes, n)
			positions[n.FeatureID] = Position{FeatureClass: n.FeatureClass, Lon: n.Lon, Lat: n.Lat, InView: true}
			if len(nodes) > maxNodes {
				return &View{Nodes: nil, Edges: nil, Positions: nil, Truncated: true, NodeCount: len(nodes)}, nil
			}
		}
	}

	if len(nodes) == 0 {
		return &View{Nodes: []Node{}, Edges: []Edge{}, Positions: map[string]Position{}, NodeCount: 0}, nil
	}

	inViewIDs := make([]string, 0, len(nodes))
	for _, n := range nodes {
		inViewIDs = append(inViewIDs, n.FeatureID)
	}

	edges, farByClass, err := readEdges(ctx, db, inViewIDs, positions)
	if err != nil {
		return nil, err
	}

	// Resolve the positions of out-of-view features the edges reach, per class, so
	// the client can aim each off-page stub. A far feature that's been deleted (no
	// row) simply yields no position; the edge that needed it is dropped below.
	for classID, ids := range farByClass {
		cm := byID[classID]
		if cm == nil {
			continue
		}
		farPos, err := readPositions(ctx, db, cm, ids, scope)
		if err != nil {
			return nil, err
		}
		for id, p := range farPos {
			positions[id] = p
		}
	}

	// Keep only edges whose BOTH ends resolved to a position -- an edge to a
	// deleted or geometry-less feature can't be drawn.
	drawable := make([]Edge, 0, len(edges))
	for _, e := range edges {
		if _, ok := positions[e.From]; !ok {
			continue
		}
		if _, ok := positions[e.To]; !ok {
			continue
		}
		drawable = append(drawable, e)
	}

	return &View{Nodes: nodes, Edges: drawable, Positions: positions, NodeCount: len(nodes)}, nil
}

// geometricClasses returns the compiled, geometry-bearing feature classes with
// the per-table optional-column map already filled in.
func geometricClasses(ctx context.Context, db bun.IDB) ([]classMeta, error) {
	return classMetas(ctx, db, false)
}

// allClasses is geometricClasses PLUS geometry-less classes -- for the topological
// one-line, which is wired purely by connectivity and so must include geometry-
// less features (busbars, bays, geometry-optional conductors) that connectivity
// flows through. The bbox read (readClassNodes, ST_PointOnSurface) is never used
// on these -- the one-line reads them by id (readClassNodesByIDs), which needs no
// geometry.
func allClasses(ctx context.Context, db bun.IDB) ([]classMeta, error) {
	return classMetas(ctx, db, true)
}

func classMetas(ctx context.Context, db bun.IDB, includeGeomless bool) ([]classMeta, error) {
	fcs, err := metadata.ListFeatureClasses(ctx, db)
	if err != nil {
		return nil, err
	}

	out := make([]classMeta, 0, len(fcs))
	names := make([]string, 0, len(fcs))
	for i := range fcs {
		fc := fcs[i]
		if fc.PhysicalTable == nil || (!includeGeomless && fc.GeometryType == "none") {
			continue
		}
		srid := 4326
		if fc.SRID != nil {
			srid = *fc.SRID
		}
		table := *fc.PhysicalTable
		bare := table
		if idx := strings.LastIndex(table, "."); idx >= 0 {
			bare = table[idx+1:]
		}
		out = append(out, classMeta{
			id:           fc.ID,
			code:         fc.Code,
			table:        table,
			tableName:    bare,
			geometryType:   fc.GeometryType,
			networkRole:    fc.NetworkRole,
			functionalRole: fc.FunctionalRole,
			traceTier:      fc.TraceTier,
			network:        fc.Network,
			srid:           srid,
			optional:       map[string]bool{},
		})
		names = append(names, bare)
	}

	cols, err := optionalColumns(ctx, db, names)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if set, ok := cols[out[i].tableName]; ok {
			out[i].optional = set
		}
	}
	return out, nil
}

// optionalColumns probes information_schema once for which of optionalCols each
// gis table carries, so a per-class read selects only columns that exist.
func optionalColumns(ctx context.Context, db bun.IDB, tableNames []string) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	if len(tableNames) == 0 {
		return out, nil
	}
	var rows []struct {
		TableName  string `bun:"table_name"`
		ColumnName string `bun:"column_name"`
	}
	err := db.NewSelect().
		ColumnExpr("table_name").ColumnExpr("column_name").
		TableExpr("information_schema.columns").
		Where("table_schema = ?", "gis").
		Where("table_name IN (?)", bun.In(tableNames)).
		Where("column_name IN (?)", bun.In(optionalCols)).
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("schematic: probing columns: %w", err)
	}
	for _, r := range rows {
		set := out[r.TableName]
		if set == nil {
			set = map[string]bool{}
			out[r.TableName] = set
		}
		set[r.ColumnName] = true
	}
	return out, nil
}

// readClassNodes reads one class's in-view features as schematic nodes. limit
// bounds the per-class read (the caller enforces the global cap on top). The
// representative point is ST_PointOnSurface -- guaranteed ON the geometry for a
// point, line or polygon, so a conductor's node sits on the wire, not off it.
// scope AND-s in the territory + draft predicates after the bbox filter.
func readClassNodes(ctx context.Context, db bun.IDB, cm *classMeta, bbox BBox, limit int, scope readScope) ([]Node, error) {
	cols := []string{
		"id::text AS id",
		"ST_X(ST_PointOnSurface(geom)) AS lon",
		"ST_Y(ST_PointOnSurface(geom)) AS lat",
	}
	for _, c := range optionalCols {
		if cm.optional[c] {
			cols = append(cols, quoteIdent(c))
		}
	}

	// Args in placeholder order: the four bbox corners, then the scope binds the
	// rawClause appends. srid and limit are trusted ints, spliced with %d.
	args := []any{bbox.MinX, bbox.MinY, bbox.MaxX, bbox.MaxY}
	scopeSQL := scope.rawClause(&args, true) // readClassNodes is geometric (has territory_id)
	query := fmt.Sprintf(
		"SELECT %s FROM %s WHERE deleted_at IS NULL AND geom IS NOT NULL "+
			"AND ST_Intersects(geom, ST_Transform(ST_MakeEnvelope(?, ?, ?, ?, 4326), %d))%s LIMIT %d",
		strings.Join(cols, ", "), cm.table, cm.srid, scopeSQL, limit,
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("schematic: reading %s: %w", cm.code, err)
	}
	defer rows.Close()

	colNames, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("schematic: columns for %s: %w", cm.code, err)
	}

	out := []Node{}
	for rows.Next() {
		vals := make([]any, len(colNames))
		ptrs := make([]any, len(colNames))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("schematic: scanning %s: %w", cm.code, err)
		}
		row := map[string]any{}
		for i, c := range colNames {
			row[c] = vals[i]
		}

		lon, okLon := toFloat(row["lon"])
		lat, okLat := toFloat(row["lat"])
		if !okLon || !okLat {
			// A geometry ST_PointOnSurface couldn't reduce to a point (shouldn't
			// happen for valid point/line/polygon geom) -- skip rather than place
			// it at 0,0.
			continue
		}
		n := Node{
			FeatureClass: cm.code,
			FeatureID:    toString(row["id"]),
			Lon:          lon,
			Lat:          lat,
			GeometryType:   cm.geometryType,
			NetworkRole:    cm.networkRole,
			FunctionalRole: cm.functionalRole,
			TraceTier:      cm.traceTier,
			Network:        cm.network,
			DeviceState:    toStrPtr(row["device_state"]),
			CircuitRef:   toStrPtr(row["circuit_ref"]),
			RatedKVA:     toFloatPtr(row["rated_kva"]),
			DemandKW:     toFloatPtr(row["demand_kw"]),
		}
		if v, ok := toInt(row["subtype"]); ok {
			n.Subtype = &v
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// connEdge is a minimal view of runtime.feature_connectivity for the schematic
// -- just the two endpoints' class ids and feature ids, enough to draw a line.
type connEdge struct {
	bun.BaseModel `bun:"table:runtime.feature_connectivity"`

	FromFeatureClassID string `bun:"from_feature_class_id"`
	FromFeatureID      string `bun:"from_feature_id"`
	ToFeatureClassID   string `bun:"to_feature_class_id"`
	ToFeatureID        string `bun:"to_feature_id"`
}

// readEdges loads every connectivity row touching an in-view feature on either
// side, returns them as Edges, and groups the out-of-view endpoint feature ids
// by class id so the caller can resolve their positions in one read per class.
func readEdges(ctx context.Context, db bun.IDB, inViewIDs []string, positions map[string]Position) ([]Edge, map[string][]string, error) {
	var rows []connEdge
	if err := db.NewSelect().Model(&rows).
		Where("from_feature_id IN (?) OR to_feature_id IN (?)", bun.In(inViewIDs), bun.In(inViewIDs)).
		Scan(ctx); err != nil {
		return nil, nil, fmt.Errorf("schematic: reading edges: %w", err)
	}

	edges := make([]Edge, 0, len(rows))
	farSeen := map[string]bool{}
	farByClass := map[string][]string{}
	note := func(classID, featureID string) {
		if _, ok := positions[featureID]; ok {
			return // already have it (in view)
		}
		if farSeen[featureID] {
			return
		}
		farSeen[featureID] = true
		farByClass[classID] = append(farByClass[classID], featureID)
	}
	for _, r := range rows {
		edges = append(edges, Edge{From: r.FromFeatureID, To: r.ToFeatureID})
		note(r.FromFeatureClassID, r.FromFeatureID)
		note(r.ToFeatureClassID, r.ToFeatureID)
	}
	return edges, farByClass, nil
}

// readPositions resolves the representative points of a specific set of features
// in one class -- the out-of-view edge endpoints. Deleted / geometry-less rows
// simply don't come back, so their edges get dropped by the caller.
func readPositions(ctx context.Context, db bun.IDB, cm *classMeta, ids []string, scope readScope) (map[string]Position, error) {
	out := map[string]Position{}
	if len(ids) == 0 {
		return out, nil
	}
	var recs []struct {
		ID  string  `bun:"id"`
		Lon float64 `bun:"lon"`
		Lat float64 `bun:"lat"`
	}
	// The query builder (not a raw QueryContext) is what makes bun.In expand the
	// id set to placeholders. TableExpr takes the compiler-generated qualified
	// table name verbatim -- it is never request-derived. The same scope the
	// in-view read used is AND-ed here too, so an out-of-scope edge endpoint
	// resolves to no position and its edge is dropped (no leaking its location via
	// an off-page stub).
	q := db.NewSelect().
		ColumnExpr("id::text AS id").
		ColumnExpr("ST_X(ST_PointOnSurface(geom)) AS lon").
		ColumnExpr("ST_Y(ST_PointOnSurface(geom)) AS lat").
		TableExpr(cm.table).
		Where("deleted_at IS NULL").
		Where("geom IS NOT NULL").
		Where("id IN (?)", bun.In(ids))
	q = scope.apply(q)
	if err := q.Scan(ctx, &recs); err != nil {
		return nil, fmt.Errorf("schematic: reading positions for %s: %w", cm.code, err)
	}
	for _, r := range recs {
		out[r.ID] = Position{FeatureClass: cm.code, Lon: r.Lon, Lat: r.Lat, InView: false}
	}
	return out, nil
}
