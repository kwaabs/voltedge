package schematic

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/uptrace/bun"
)

// This file builds the EPHEMERAL one-line: a temporary, abstract single-line view
// of a feature set (a map selection or the current viewport). Nothing is
// persisted -- the layout is computed on the fly and returned as data for the
// client to render and export (DXF/PDF). It reuses the schematic graph (BuildView
// / BuildViewForRefs) plus a layered topological layout (sources at top, flow
// down by connectivity depth), replacing the old persisted "generate a diagram"
// path entirely.

// Layout constants in abstract one-line units (the client scales to fit).
const (
	oneLineXSpacing     = 110.0 // horizontal gap between siblings in a layer
	oneLineYSpacing     = 90.0  // vertical gap between connectivity depths
	oneLineComponentGap = 160.0 // horizontal gap between disconnected sub-networks
)

// FeatureRef identifies one feature in a one-line request (a selected feature).
type FeatureRef struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
}

// OneLineNode is a point feature placed on the one-line, at abstract (X,Y).
type OneLineNode struct {
	FeatureClass string   `json:"feature_class"`
	FeatureID    string   `json:"feature_id"`
	Subtype      *int     `json:"subtype,omitempty"`
	X            float64  `json:"x"`
	Y            float64  `json:"y"`
	DeviceState  *string  `json:"device_state,omitempty"`
	CircuitRef   *string  `json:"circuit_ref,omitempty"`
	RatedKVA     *float64 `json:"rated_kva,omitempty"`
	DemandKW     *float64 `json:"demand_kw,omitempty"`
}

// OneLineConnector is a wire on the one-line: a conductor (carries its class/id
// so the client can colour it by symbology and shade it when de-energised) or a
// plain direct link (no feature). Path is an abstract [[x,y],...] polyline.
type OneLineConnector struct {
	FeatureClass string  `json:"feature_class,omitempty"`
	FeatureID    string  `json:"feature_id,omitempty"`
	Subtype      *int    `json:"subtype,omitempty"`
	CircuitRef   *string `json:"circuit_ref,omitempty"`
	Network      *string `json:"network,omitempty"`
	// Endpoints are the point-feature node ids this wire joins (its path runs
	// between them). The client uses them to re-route the wire live when a node is
	// dragged in the editable one-line.
	Endpoints []string    `json:"endpoints,omitempty"`
	Path      [][]float64 `json:"path"`
}

// OneLine is the whole ephemeral one-line payload.
type OneLine struct {
	Nodes      []OneLineNode      `json:"nodes"`
	Connectors []OneLineConnector `json:"connectors"`
	Truncated  bool               `json:"truncated"`
	NodeCount  int                `json:"node_count"`
}

// isConductorNode reports whether a node is a conductor -- a wire drawn BETWEEN
// its endpoints rather than a placed symbol. Keyed on network_role ('edge'), NOT
// geometry type, so a geometry-less conductor (geometry_optional, geometry_type
// 'none') is still treated as a wire the one-line draws, not a floating dot.
func isConductorNode(n *Node) bool { return n.NetworkRole == "edge" }

// BuildOneLine lays a schematic graph out as an abstract one-line. The view is
// the graph for a bbox (BuildView) or a selection (BuildViewForRefs).
func BuildOneLine(view *View) *OneLine {
	if view.Truncated {
		return &OneLine{Truncated: true, NodeCount: view.NodeCount}
	}

	// Drop non-electrical features (poles, furniture: network_role 'none'). They
	// carry no connectivity, so a topological one-line can only float them as
	// unlabeled dots -- noise. Everything electrical (edge/junction) stays.
	electrical := &View{Edges: view.Edges, Positions: view.Positions}
	electrical.Nodes = make([]Node, 0, len(view.Nodes))
	for _, n := range view.Nodes {
		if n.NetworkRole == "none" {
			continue
		}
		electrical.Nodes = append(electrical.Nodes, n)
	}
	view = electrical

	nodeByID := make(map[string]*Node, len(view.Nodes))
	for i := range view.Nodes {
		nodeByID[view.Nodes[i].FeatureID] = &view.Nodes[i]
	}

	positions, conductors, directEdges := layoutNetwork(view)

	out := &OneLine{NodeCount: len(view.Nodes)}

	// Point features -> nodes (conductors are drawn as wires, not symbols).
	for i := range view.Nodes {
		n := &view.Nodes[i]
		if isConductorNode(n) {
			continue
		}
		p, ok := positions[n.FeatureID]
		if !ok {
			continue
		}
		out.Nodes = append(out.Nodes, OneLineNode{
			FeatureClass: n.FeatureClass,
			FeatureID:    n.FeatureID,
			Subtype:      n.Subtype,
			X:            p.x,
			Y:            p.y,
			DeviceState:  n.DeviceState,
			CircuitRef:   n.CircuitRef,
			RatedKVA:     n.RatedKVA,
			DemandKW:     n.DemandKW,
		})
	}

	// Parallel conductors (two+ runs between the SAME pair of features -- redundant
	// feeders, a cable + its parallel) would otherwise draw exactly on top of each
	// other. Count them per endpoint-pair so each can be fanned to one side.
	pairKey := func(a, b point) string {
		if a.x > b.x || (a.x == b.x && a.y > b.y) {
			a, b = b, a
		}
		return fmt.Sprintf("%.1f,%.1f|%.1f,%.1f", a.x, a.y, b.x, b.y)
	}
	pairTotal := map[string]int{}
	for _, c := range conductors {
		if len(c.ends) == 2 {
			pairTotal[pairKey(c.ends[0], c.ends[1])]++
		}
	}
	pairSeen := map[string]int{}

	// Conductors -> wires along their endpoints (a bus polyline for a multi-tap
	// conductor, an elbow for a 2-end one, fanned apart when parallel).
	for _, c := range conductors {
		var path [][]float64
		switch {
		case len(c.ends) > 2:
			path = busPathPoints(c.ends)
		case len(c.ends) == 2:
			k := pairKey(c.ends[0], c.ends[1])
			if n := pairTotal[k]; n > 1 {
				idx := pairSeen[k]
				pairSeen[k]++
				// Fan symmetrically about the straight run, ~a third of a column apart.
				offset := (float64(idx) - float64(n-1)/2) * oneLineXSpacing * 0.35
				path = parallelPath(c.ends[0], c.ends[1], offset)
			} else {
				path = elbowPathPoints(c.ends[0], c.ends[1])
			}
		case len(c.ends) == 1:
			// Only one endpoint is in the set -- the conductor runs OFF the diagram
			// (a long MV feeder to an out-of-view/unselected substation). Draw a
			// short stub down from that endpoint so the conductor still shows,
			// leaving, rather than vanishing.
			e := c.ends[0]
			path = [][]float64{{e.x, e.y}, {e.x, e.y + oneLineYSpacing*0.8}}
		default:
			continue
		}
		conn := OneLineConnector{FeatureClass: c.class, FeatureID: c.id, Path: path, Endpoints: c.endIDs}
		if node := nodeByID[c.id]; node != nil {
			conn.Subtype = node.Subtype
			conn.CircuitRef = node.CircuitRef
			conn.Network = node.Network
		}
		out.Connectors = append(out.Connectors, conn)
	}

	// Direct device-to-device links (no conductor) -> plain connectors.
	for _, e := range directEdges {
		pa, okA := positions[e.from]
		pb, okB := positions[e.to]
		if !okA || !okB {
			continue
		}
		out.Connectors = append(out.Connectors, OneLineConnector{Path: elbowPathPoints(pa, pb), Endpoints: []string{e.from, e.to}})
	}

	return out
}

// BuildViewForRefs reads the schematic graph for an explicit feature set (a map
// selection): the specified features plus the connectivity among them. Unlike
// BuildView it needs no bbox and resolves no far-endpoint positions (the one-line
// only wires features within the set). Geometry-less classes in the selection are
// skipped (the schematic graph is built from geometric classes).
func BuildViewForRefs(ctx context.Context, db bun.IDB, refs []FeatureRef, maxNodes int) (*View, error) {
	if maxNodes <= 0 {
		maxNodes = DefaultMaxNodes
	}
	if len(refs) == 0 {
		return &View{Nodes: []Node{}, Edges: []Edge{}, Positions: map[string]Position{}, NodeCount: 0}, nil
	}

	scope, err := resolveScope(ctx, db)
	if err != nil {
		return nil, err
	}
	classes, err := allClasses(ctx, db)
	if err != nil {
		return nil, err
	}
	cmByCode := make(map[string]*classMeta, len(classes))
	for i := range classes {
		cmByCode[classes[i].code] = &classes[i]
	}

	byClass := map[string][]string{}
	for _, r := range refs {
		if r.FeatureClass == "" || r.FeatureID == "" {
			continue
		}
		byClass[r.FeatureClass] = append(byClass[r.FeatureClass], r.FeatureID)
	}

	nodes := make([]Node, 0, len(refs))
	codes := make([]string, 0, len(byClass))
	for code := range byClass {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		cm := cmByCode[code]
		if cm == nil {
			continue // geometry-less or unknown class -- not in the schematic graph
		}
		classNodes, err := readClassNodesByIDs(ctx, db, cm, byClass[code], scope)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, classNodes...)
		if len(nodes) > maxNodes {
			return &View{Truncated: true, NodeCount: len(nodes)}, nil
		}
	}

	if len(nodes) == 0 {
		return &View{Nodes: []Node{}, Edges: []Edge{}, Positions: map[string]Position{}, NodeCount: 0}, nil
	}

	// Grow the set so the one-line is CONNECTED rather than fragmented: pull in the
	// wires joining the selected devices and those wires' endpoints (see
	// expandForOneLine). Without this, selecting devices but not the conductor
	// between them shows them as isolated dots.
	cmByID := make(map[string]*classMeta, len(classes))
	for i := range classes {
		cmByID[classes[i].id] = &classes[i]
	}
	nodes, err = expandForOneLine(ctx, db, nodes, cmByID, scope, maxNodes)
	if err != nil {
		return nil, err
	}

	positions := make(map[string]Position, len(nodes))
	inViewIDs := make([]string, 0, len(nodes))
	for _, n := range nodes {
		positions[n.FeatureID] = Position{FeatureClass: n.FeatureClass, InView: true}
		inViewIDs = append(inViewIDs, n.FeatureID)
	}

	// Edges among the selected features only. readEdges also groups far endpoints,
	// which the one-line ignores (it wires features within the set), so drop them.
	edges, _, err := readEdges(ctx, db, inViewIDs, positions)
	if err != nil {
		return nil, err
	}
	inSet := make(map[string]bool, len(inViewIDs))
	for _, id := range inViewIDs {
		inSet[id] = true
	}
	kept := make([]Edge, 0, len(edges))
	for _, e := range edges {
		if inSet[e.From] && inSet[e.To] {
			kept = append(kept, e)
		}
	}

	return &View{Nodes: nodes, Edges: kept, Positions: positions, NodeCount: len(nodes)}, nil
}

// pullNeighbours reads the feature_connectivity neighbours of seedIDs (electrical
// features only -- network_role != 'none', so poles/furniture never get pulled in)
// that aren't already in `have`. budget caps how many it returns.
func pullNeighbours(ctx context.Context, db bun.IDB, seedIDs []string, have map[string]bool, cmByID map[string]*classMeta, scope readScope, budget int) ([]Node, error) {
	if budget <= 0 || len(seedIDs) == 0 {
		return nil, nil
	}
	seedSet := make(map[string]bool, len(seedIDs))
	for _, id := range seedIDs {
		seedSet[id] = true
	}

	var rows []connEdge
	if err := db.NewSelect().Model(&rows).
		Where("from_feature_id IN (?) OR to_feature_id IN (?)", bun.In(seedIDs), bun.In(seedIDs)).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("schematic: pull neighbours: %w", err)
	}

	seen := map[string]bool{}
	byCode := map[string][]string{}
	cmByCode := map[string]*classMeta{}
	for _, r := range rows {
		var nbrID, nbrClassID string
		switch {
		case seedSet[r.FromFeatureID] && !seedSet[r.ToFeatureID]:
			nbrID, nbrClassID = r.ToFeatureID, r.ToFeatureClassID
		case seedSet[r.ToFeatureID] && !seedSet[r.FromFeatureID]:
			nbrID, nbrClassID = r.FromFeatureID, r.FromFeatureClassID
		default:
			continue
		}
		if have[nbrID] || seen[nbrID] {
			continue
		}
		cm := cmByID[nbrClassID]
		if cm == nil || cm.networkRole == "none" {
			continue
		}
		seen[nbrID] = true
		cmByCode[cm.code] = cm
		byCode[cm.code] = append(byCode[cm.code], nbrID)
	}

	out := []Node{}
	codes := make([]string, 0, len(byCode))
	for code := range byCode {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	for _, code := range codes {
		ns, err := readClassNodesByIDs(ctx, db, cmByCode[code], byCode[code], scope)
		if err != nil {
			return nil, err
		}
		for _, n := range ns {
			if len(out) >= budget {
				return out, nil
			}
			out = append(out, n)
		}
	}
	return out, nil
}

// expandForOneLine grows the node set to the FULL connected component(s) of the
// selection/view -- a bounded breadth-first walk over feature_connectivity, exactly
// how the trace engine reaches a whole feeder. Without this the one-line draws only
// the induced subgraph of the selection, which is routinely disconnected whenever an
// intermediate wiring feature (a conductor, a busbar) wasn't itself selected -- the
// real cause of the fragmentation. Bounded by maxNodes; poles/furniture aren't
// walked into (see pullNeighbours). Genuinely-separate feeders stay separate (no
// edge between them), which is correct.
func expandForOneLine(ctx context.Context, db bun.IDB, nodes []Node, cmByID map[string]*classMeta, scope readScope, maxNodes int) ([]Node, error) {
	have := make(map[string]bool, len(nodes))
	frontier := make([]string, 0, len(nodes))
	for i := range nodes {
		have[nodes[i].FeatureID] = true
		frontier = append(frontier, nodes[i].FeatureID)
	}

	for len(frontier) > 0 {
		budget := maxNodes - len(nodes)
		if budget <= 0 {
			break
		}
		extra, err := pullNeighbours(ctx, db, frontier, have, cmByID, scope, budget)
		if err != nil {
			return nil, err
		}
		if len(extra) == 0 {
			break
		}
		frontier = frontier[:0]
		for _, n := range extra {
			if have[n.FeatureID] {
				continue
			}
			have[n.FeatureID] = true
			nodes = append(nodes, n)
			frontier = append(frontier, n.FeatureID)
		}
	}
	return nodes, nil
}

// CloseConductorEndpoints pulls the point endpoints of a view's conductors that
// fell outside the view into its Nodes, so a long conductor crossing the viewport
// still connects on the one-line (the view's edges already reach those endpoints).
// For the bbox path; the selection path (BuildViewForRefs) closes inline.
func CloseConductorEndpoints(ctx context.Context, db bun.IDB, view *View, maxNodes int) error {
	if view == nil || view.Truncated || len(view.Nodes) == 0 {
		return nil
	}
	scope, err := resolveScope(ctx, db)
	if err != nil {
		return err
	}
	classes, err := allClasses(ctx, db)
	if err != nil {
		return err
	}
	cmByID := make(map[string]*classMeta, len(classes))
	for i := range classes {
		cmByID[classes[i].id] = &classes[i]
	}
	expanded, err := expandForOneLine(ctx, db, view.Nodes, cmByID, scope, maxNodes)
	if err != nil {
		return err
	}
	view.Nodes = expanded

	// BuildView computed edges for the ORIGINAL in-view ids only, so edges between
	// newly-pulled nodes are missing. Recompute edges + positions over the full
	// expanded set (the same induced-subgraph read BuildViewForRefs does).
	ids := make([]string, len(expanded))
	pos := make(map[string]Position, len(expanded))
	for i := range expanded {
		ids[i] = expanded[i].FeatureID
		pos[expanded[i].FeatureID] = Position{FeatureClass: expanded[i].FeatureClass, InView: true}
	}
	edges, _, err := readEdges(ctx, db, ids, pos)
	if err != nil {
		return err
	}
	inSet := make(map[string]bool, len(ids))
	for _, id := range ids {
		inSet[id] = true
	}
	kept := make([]Edge, 0, len(edges))
	for _, e := range edges {
		if inSet[e.From] && inSet[e.To] {
			kept = append(kept, e)
		}
	}
	view.Edges = kept
	view.Positions = pos
	return nil
}

// readClassNodesByIDs reads a specific set of one class's features as schematic
// nodes (the selection counterpart of readClassNodes' bbox read). No geometry is
// needed -- the one-line is topological -- so it reads only the class/attrs, with
// the same territory + draft scope.
func readClassNodesByIDs(ctx context.Context, db bun.IDB, cm *classMeta, ids []string, scope readScope) ([]Node, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	cols := []string{"id::text AS id"}
	for _, c := range optionalCols {
		if cm.optional[c] {
			cols = append(cols, quoteIdent(c))
		}
	}

	args := make([]any, 0, len(ids)+4)
	placeholders := make([]string, len(ids))
	for i := range ids {
		placeholders[i] = "?"
		args = append(args, ids[i])
	}
	scopeSQL := scope.rawClause(&args, cm.geometryType != "none") // geometry-less tables have no territory_id
	query := fmt.Sprintf(
		"SELECT %s FROM %s WHERE deleted_at IS NULL AND id IN (%s)%s",
		strings.Join(cols, ", "), cm.table, strings.Join(placeholders, ", "), scopeSQL,
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("schematic: reading %s by id: %w", cm.code, err)
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
		n := Node{
			FeatureClass:   cm.code,
			FeatureID:      toString(row["id"]),
			GeometryType:   cm.geometryType,
			NetworkRole:    cm.networkRole,
			FunctionalRole: cm.functionalRole,
			TraceTier:      cm.traceTier,
			Network:        cm.network,
			DeviceState:    toStrPtr(row["device_state"]),
			CircuitRef:     toStrPtr(row["circuit_ref"]),
			RatedKVA:       toFloatPtr(row["rated_kva"]),
			DemandKW:       toFloatPtr(row["demand_kw"]),
		}
		if v, ok := toInt(row["subtype"]); ok {
			n.Subtype = &v
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// --- layout ----------------------------------------------------------------

type point struct{ x, y float64 }

type edge struct{ from, to string }

// conductorPlace is one conductor (a line feature) laid along its endpoints.
type conductorPlace struct {
	class string
	id    string
	ends  []point
	// endIDs are the point-feature node ids at each end, in the same order as
	// ends. The client re-routes the wire from these when a node is dragged.
	endIDs []string
}

// layoutNetwork lays the schematic graph out by topology: point features into
// layers by connectivity depth (sources at top), conductors along their
// endpoints, direct links between the rest. Per connected sub-network, side by
// side. Conductors are collapsed to edges for LAYOUT (a switch--conductor--busbar
// chain puts switch and busbar one depth apart) but returned as wires to draw.
func layoutNetwork(view *View) (map[string]point, []conductorPlace, []edge) {
	isPoint := map[string]bool{}
	isConductor := map[string]bool{}
	nodeByID := map[string]*Node{}
	for i := range view.Nodes {
		n := &view.Nodes[i]
		nodeByID[n.FeatureID] = n
		if isConductorNode(n) {
			isConductor[n.FeatureID] = true
		} else {
			isPoint[n.FeatureID] = true
		}
	}

	adj := map[string]map[string]bool{}
	addAdj := func(a, b string) {
		if a == b {
			return
		}
		if adj[a] == nil {
			adj[a] = map[string]bool{}
		}
		if adj[b] == nil {
			adj[b] = map[string]bool{}
		}
		adj[a][b] = true
		adj[b][a] = true
	}
	conductorNbrs := map[string][]string{}
	directPairs := map[string]bool{}
	recordDirect := func(a, b string) {
		if a > b {
			a, b = b, a
		}
		directPairs[a+"|"+b] = true
	}
	for _, e := range view.Edges {
		switch {
		case isPoint[e.From] && isPoint[e.To]:
			addAdj(e.From, e.To)
			recordDirect(e.From, e.To)
		case isConductor[e.From] && isPoint[e.To]:
			conductorNbrs[e.From] = append(conductorNbrs[e.From], e.To)
		case isPoint[e.From] && isConductor[e.To]:
			conductorNbrs[e.To] = append(conductorNbrs[e.To], e.From)
		}
	}
	for _, nbrs := range conductorNbrs {
		for i := 0; i < len(nbrs); i++ {
			for j := i + 1; j < len(nbrs); j++ {
				addAdj(nbrs[i], nbrs[j])
			}
		}
	}

	pointIDs := make([]string, 0, len(isPoint))
	for id := range isPoint {
		pointIDs = append(pointIDs, id)
		if adj[id] == nil {
			adj[id] = map[string]bool{}
		}
	}
	sort.Strings(pointIDs)

	// Lay each disconnected sub-network out in its own local space, then SHELF-PACK
	// them into a grid aimed at a roughly landscape box -- rather than a single
	// left-to-right strip. A one-line often pulls in several unconnected bits (a
	// stray RMU, an off-feeder meter); strung in one row they make an extremely wide
	// sheet that the PDF fit then shrinks to near-nothing (symbols drop below the
	// renderer's resolution, the legend vanishes). Packing to rows keeps the whole
	// thing compact and legible on the page.
	type placedComp struct {
		local map[string]point
		w, h  float64
	}
	visited := map[string]bool{}
	comps := make([]placedComp, 0)
	totalArea := 0.0
	for _, root := range pointIDs {
		if visited[root] {
			continue
		}
		comp := componentNodes(root, adj, visited)
		local, w, h := layoutComponent(comp, adj, nodeByID)
		comps = append(comps, placedComp{local: local, w: w, h: h})
		totalArea += (w + oneLineComponentGap) * (h + oneLineYSpacing)
	}

	// Target row width for a ~landscape aspect; a single component always gets its
	// own row (the first in a row is placed regardless of width).
	targetW := math.Sqrt(totalArea * 1.6)
	positions := map[string]point{}
	rowX, rowY, rowH := 0.0, 0.0, 0.0
	for _, c := range comps {
		if rowX > 0 && rowX+c.w > targetW {
			rowX = 0
			rowY += rowH + oneLineComponentGap
			rowH = 0
		}
		for id, p := range c.local {
			positions[id] = point{x: rowX + p.x, y: rowY + p.y}
		}
		rowX += c.w + oneLineComponentGap
		if c.h > rowH {
			rowH = c.h
		}
	}

	condIDs := make([]string, 0, len(conductorNbrs))
	for id := range conductorNbrs {
		condIDs = append(condIDs, id)
	}
	sort.Strings(condIDs)
	conductors := make([]conductorPlace, 0, len(condIDs))
	for _, condID := range condIDs {
		node := nodeByID[condID]
		if node == nil {
			continue
		}
		ends := make([]point, 0, 4)
		endIDs := make([]string, 0, 4)
		for _, n := range conductorNbrs[condID] {
			if p, ok := positions[n]; ok {
				ends = append(ends, p)
				endIDs = append(endIDs, n)
			}
		}
		if len(ends) == 0 {
			continue
		}
		conductors = append(conductors, conductorPlace{class: node.FeatureClass, id: condID, ends: ends, endIDs: endIDs})
	}

	dkeys := make([]string, 0, len(directPairs))
	for k := range directPairs {
		dkeys = append(dkeys, k)
	}
	sort.Strings(dkeys)
	directEdges := make([]edge, 0, len(dkeys))
	for _, key := range dkeys {
		parts := strings.SplitN(key, "|", 2)
		if len(parts) != 2 {
			continue
		}
		directEdges = append(directEdges, edge{from: parts[0], to: parts[1]})
	}

	return positions, conductors, directEdges
}

func componentNodes(root string, adj map[string]map[string]bool, visited map[string]bool) []string {
	out := []string{}
	queue := []string{root}
	visited[root] = true
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		out = append(out, cur)
		for _, n := range sortedKeys(adj[cur]) {
			if !visited[n] {
				visited[n] = true
				queue = append(queue, n)
			}
		}
	}
	return out
}

// layoutComponent lays one connected sub-network out as a tidy tree: a spanning
// tree rooted at the source, with each parent centred over its children (y =
// depth). Cross edges (loops, a multi-tap conductor's extra taps) aren't tree
// edges -- they're just drawn as connectors between the positions. This puts
// children UNDER their parent rather than centring each depth-level on its own,
// which is what made the earlier layout look scrambled.
// layoutComponent lays one component out into a FRESH LOCAL coordinate space
// (origin 0,0) and returns that map plus the component's width and height, so the
// caller can shelf-pack components into a grid rather than a single wide strip. See
// chooseRoot for the root choice; the tidy-tree shape is unchanged.
func layoutComponent(comp []string, adj map[string]map[string]bool, nodeByID map[string]*Node) (map[string]point, float64, float64) {
	root := chooseRoot(comp, adj, nodeByID)

	// Spanning tree via BFS: a stable child order (sorted) keeps the layout
	// deterministic.
	children := map[string][]string{}
	visited := map[string]bool{root: true}
	queue := []string{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, n := range sortedKeys(adj[cur]) {
			if !visited[n] {
				visited[n] = true
				children[cur] = append(children[cur], n)
				queue = append(queue, n)
			}
		}
	}

	// Tidy-tree x assignment: leaves take successive columns left-to-right; a
	// parent sits at the mean of its children. Recursion depth is bounded by the
	// tree height, fine for a feeder.
	local := map[string]point{}
	nextX := 0.0
	maxDepth := 0
	var place func(id string, depth int)
	place = func(id string, depth int) {
		if depth > maxDepth {
			maxDepth = depth
		}
		y := float64(depth) * oneLineYSpacing
		kids := children[id]
		if len(kids) == 0 {
			local[id] = point{x: nextX, y: y}
			nextX += oneLineXSpacing
			return
		}
		for _, c := range kids {
			place(c, depth+1)
		}
		sum := 0.0
		for _, c := range kids {
			sum += local[c].x
		}
		local[id] = point{x: sum / float64(len(kids)), y: y}
	}
	place(root, 0)

	width := nextX - oneLineXSpacing
	if width < 0 {
		width = 0
	}
	height := float64(maxDepth) * oneLineYSpacing
	return local, width, height
}

// chooseRoot picks the sub-network's top node (the feeder head). A feeder head is
// a DEAD-END on the source side -- a degree-1 leaf whose trace_tier is the lowest
// (closest to the grid). Crucially it is NOT simply the lowest-tier node overall:
// an interior MV switch often has a lower tier than the source transformer, and
// rooting there branches the feeder both ways and looks wrong. So:
//  1. a declared source (functional_role='source'),
//  2. the lowest-trace_tier LEAF (degree 1) -- the source dead-end, not a load,
//  3. (a pure loop with no leaves) the lowest-trace_tier node, least-connected,
// each with lowest id as the final deterministic tiebreak.
func chooseRoot(comp []string, adj map[string]map[string]bool, nodeByID map[string]*Node) string {
	ids := append([]string(nil), comp...)
	sort.Strings(ids)

	for _, id := range ids {
		if n := nodeByID[id]; n != nil && n.FunctionalRole != nil && *n.FunctionalRole == "source" {
			return id
		}
	}

	const noTier = 1 << 30
	tierOf := func(id string) int {
		if n := nodeByID[id]; n != nil && n.TraceTier != nil {
			return *n.TraceTier
		}
		return noTier
	}

	// Lowest-tier leaf (the source dead-end, not a load leaf).
	best, bestTier := "", noTier
	for _, id := range ids {
		if len(adj[id]) != 1 {
			continue
		}
		if t := tierOf(id); best == "" || t < bestTier {
			best, bestTier = id, t
		}
	}
	if best != "" {
		return best
	}

	// No leaves (a closed loop): lowest tier, then least-connected.
	bestDeg := 1 << 30
	for _, id := range ids {
		t, deg := tierOf(id), len(adj[id])
		if best == "" || t < bestTier || (t == bestTier && deg < bestDeg) {
			best, bestTier, bestDeg = id, t, deg
		}
	}
	return best
}

// busPathPoints chains a multi-tap conductor's endpoints into one polyline (top
// to bottom, then left to right) -- a readable bus visiting every feature it
// serves.
func busPathPoints(ends []point) [][]float64 {
	sorted := append([]point(nil), ends...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].y != sorted[j].y {
			return sorted[i].y < sorted[j].y
		}
		return sorted[i].x < sorted[j].x
	})
	// An ORTHOGONAL staircase through the endpoints -- never a diagonal: between
	// consecutive points drop vertically to the next point's level, then across.
	out := [][]float64{{sorted[0].x, sorted[0].y}}
	for i := 1; i < len(sorted); i++ {
		prev, cur := sorted[i-1], sorted[i]
		if prev.x != cur.x && prev.y != cur.y {
			out = append(out, []float64{prev.x, cur.y})
		}
		out = append(out, []float64{cur.x, cur.y})
	}
	return out
}

// parallelPath routes a connector from a to b bowed sideways by offset (a
// perpendicular shift of the midpoint), so two+ parallel conductors between the
// same pair of features fan apart into distinct, individually-clickable wires
// instead of stacking on one line. offset 0 is a plain elbow.
func parallelPath(a, b point, offset float64) [][]float64 {
	if offset == 0 {
		return elbowPathPoints(a, b)
	}
	dx, dy := b.x-a.x, b.y-a.y
	length := math.Hypot(dx, dy)
	if length == 0 {
		return [][]float64{{a.x, a.y}, {b.x, b.y}}
	}
	// Unit perpendicular to a->b.
	px, py := -dy/length, dx/length
	mx := (a.x+b.x)/2 + px*offset
	my := (a.y+b.y)/2 + py*offset
	return [][]float64{{a.x, a.y}, {mx, my}, {b.x, b.y}}
}

// elbowPathPoints routes a 2-end connector as an orthogonal elbow (a straight
// drop when the ends share an x).
func elbowPathPoints(a, b point) [][]float64 {
	if a.x == b.x {
		return [][]float64{{a.x, a.y}, {b.x, b.y}}
	}
	ym := (a.y + b.y) / 2
	return [][]float64{{a.x, a.y}, {a.x, ym}, {b.x, ym}, {b.x, b.y}}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
