package powerflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// node identifies one feature by its feature class id and feature id --
// same identity api/internal/trace's own node uses, for the same
// reason: runtime.feature_connectivity itself stores ids, so resolving
// a code only ever happens at the request/response boundary.
type node struct {
	classID   string
	featureID string
}

// edgeRow is a read-only projection of runtime.feature_connectivity --
// this package's own copy of api/internal/trace's edgeRow, same
// reasoning that package gives for not sharing connectivity's model.
type edgeRow struct {
	bun.BaseModel `bun:"table:runtime.feature_connectivity,alias:feature_connectivity"`

	FromFeatureClassID string  `bun:"from_feature_class_id"`
	FromFeatureID      string  `bun:"from_feature_id"`
	FromTerminalID     *string `bun:"from_terminal_id"`
	ToFeatureClassID   string  `bun:"to_feature_class_id"`
	ToFeatureID        string  `bun:"to_feature_id"`
}

// terminalRoleRow is a minimal read-only projection of metadata.terminals --
// just enough to resolve the winding role (primary/secondary/tertiary/...) of a
// terminal id seen on a downstream edge, so the forward sweep can tell a
// three-winding transformer's LV children from its TV children.
type terminalRoleRow struct {
	bun.BaseModel `bun:"table:metadata.terminals,alias:terminals"`

	ID   string `bun:"id"`
	Role string `bun:"role"`
}

// resolveNode validates a featureRef -- same shape as api/internal/trace's
// resolveNode: feature class exists, feature row exists, not soft
// deleted.
func resolveNode(ctx context.Context, db bun.IDB, ref featureRef) (node, error) {
	loaded, err := metadata.Load(ctx, db, ref.FeatureClass)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return node{}, notFound("feature class %q does not exist", ref.FeatureClass)
		}
		return node{}, err
	}

	row, err := crud.GetFeature(ctx, db, loaded, ref.FeatureID)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return node{}, notFound("feature %s (%s) not found", ref.FeatureID, ref.FeatureClass)
		}
		return node{}, err
	}
	if row["deleted_at"] != nil {
		return node{}, invalid("feature %s (%s) is deleted", ref.FeatureID, ref.FeatureClass)
	}

	return node{classID: loaded.FeatureClass.ID, featureID: ref.FeatureID}, nil
}

// barrierCheck mirrors api/internal/trace's own type -- see that
// package's graph.go for the full reasoning. This engine always walks
// DOWNSTREAM and always respects open devices (a load flow over a
// de-energized branch isn't meaningful), so unlike trace's
// resolveBarriers this one has no "don't even query" early-out.
type barrierCheck struct {
	fieldName   string
	haltsOnCode string
}

// loadFlowBarriers loads DOWNSTREAM_TRACE's own trace_barrier_rules --
// deliberately reusing that trace_configurations row rather than
// inventing a parallel one. DOWNSTREAM_TRACE already covers exactly the
// switching devices a downstream walk needs to stop at
// (DIST_SWITCH/SERVICE_CUTOUT/LV_LINK_BOX, all on device_state = OPEN),
// so a second, load-flow-specific set would just be the same three
// rows duplicated under a different trace_configuration_id.
func loadFlowBarriers(ctx context.Context, db bun.IDB) (map[string]barrierCheck, error) {
	tc, err := metadata.LoadTraceConfiguration(ctx, db, "DOWNSTREAM_TRACE")
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// No DOWNSTREAM_TRACE configuration seeded at all -- walk with
			// no barriers rather than fail outright; a load flow over an
			// unseeded barrier set is still meaningful, just optimistic
			// about open devices it can't detect.
			return map[string]barrierCheck{}, nil
		}
		return nil, err
	}

	rules, err := metadata.LoadTraceBarrierRules(ctx, db, tc.ID)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return map[string]barrierCheck{}, nil
	}

	fieldIDs := make([]string, len(rules))
	valueIDs := make([]string, len(rules))
	for i, r := range rules {
		fieldIDs[i] = r.FieldID
		valueIDs[i] = r.HaltsOnValueID
	}

	var fields []metadata.Field
	if err := db.NewSelect().Model(&fields).Where("id IN (?)", bun.In(fieldIDs)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("powerflow: loading barrier fields: %w", err)
	}
	fieldNames := make(map[string]string, len(fields))
	for _, f := range fields {
		fieldNames[f.ID] = f.Name
	}

	var values []metadata.DomainValue
	if err := db.NewSelect().Model(&values).Where("id IN (?)", bun.In(valueIDs)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("powerflow: loading barrier halt values: %w", err)
	}
	valueCodes := make(map[string]string, len(values))
	for _, v := range values {
		valueCodes[v.ID] = v.Code
	}

	out := make(map[string]barrierCheck, len(rules))
	for _, r := range rules {
		out[r.FeatureClassID] = barrierCheck{fieldName: fieldNames[r.FieldID], haltsOnCode: valueCodes[r.HaltsOnValueID]}
	}
	return out, nil
}

// checkBarrier mirrors api/internal/trace's own checkBarrier exactly --
// see that package's doc comment for the "checked live, never baked
// into the graph" reasoning. loader resolves and caches a Loaded per
// feature class encountered, same as trace's featureClassLoader.
func checkBarrier(ctx context.Context, db bun.IDB, n node, barriers map[string]barrierCheck, loader *featureClassLoader, closed map[node]bool) (bool, error) {
	// A node the caller has hypothetically closed (a contingency study closing a
	// normally-open tie to backfeed an outaged section) is never a barrier,
	// regardless of its live device_state -- this is the one place the N-1
	// analysis overrides the on-the-ground open state without touching the
	// committed connectivity.
	if closed[n] {
		return false, nil
	}
	check, ok := barriers[n.classID]
	if !ok {
		return false, nil
	}

	loaded, err := loader.loadedFor(ctx, db, n.classID)
	if err != nil {
		return false, err
	}
	row, err := crud.GetFeature(ctx, db, loaded, n.featureID)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return false, nil
		}
		return false, err
	}

	attrs, _ := row["attributes"].(map[string]any)
	value, _ := attrs[check.fieldName].(string)
	return value == check.haltsOnCode, nil
}

// featureClassLoader mirrors api/internal/trace's own -- lazily
// resolves and caches a *metadata.Loaded per feature class id
// encountered during one run.
type featureClassLoader struct {
	codeByID   map[string]string
	loadedByID map[string]*metadata.Loaded
}

func newFeatureClassLoader() *featureClassLoader {
	return &featureClassLoader{codeByID: map[string]string{}, loadedByID: map[string]*metadata.Loaded{}}
}

func (l *featureClassLoader) loadedFor(ctx context.Context, db bun.IDB, classID string) (*metadata.Loaded, error) {
	if loaded, ok := l.loadedByID[classID]; ok {
		return loaded, nil
	}

	code, ok := l.codeByID[classID]
	if !ok {
		var fc metadata.FeatureClass
		if err := db.NewSelect().Model(&fc).Where("id = ?", classID).Scan(ctx); err != nil {
			return nil, fmt.Errorf("powerflow: resolving feature class %s: %w", classID, err)
		}
		code = fc.Code
		l.codeByID[classID] = code
	}

	loaded, err := metadata.Load(ctx, db, code)
	if err != nil {
		return nil, fmt.Errorf("powerflow: loading %s: %w", code, err)
	}
	l.loadedByID[classID] = loaded
	return loaded, nil
}

// downstreamEdge is one downstream step: the reached node plus the terminal on
// n's (the sending) side of the edge that reached it. fromTerminalID is "" when
// the sending side has no terminal (an edge/conductor, most containers) -- only
// a junction with terminals, like a transformer, carries one, and that is
// exactly the case the sweep needs it for (which winding a child hangs off).
type downstreamEdge struct {
	to             node
	fromTerminalID string
}

// downstreamNeighbors returns every node directly reachable from n by
// following an edge where n is the "from" side -- this engine only
// ever walks downstream (a load flow starts at a source and follows
// power away from it), unlike trace's neighborsOf which branches on a
// configurable direction. The sending-side terminal comes back with each
// neighbor so buildTree can record which winding of a transformer a child left
// through.
func downstreamNeighbors(ctx context.Context, db bun.IDB, n node) ([]downstreamEdge, error) {
	var rows []edgeRow
	if err := db.NewSelect().Model(&rows).
		Where("from_feature_id = ? AND from_feature_class_id = ?", n.featureID, n.classID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("powerflow: loading neighbors: %w", err)
	}
	out := make([]downstreamEdge, 0, len(rows))
	for _, r := range rows {
		terminalID := ""
		if r.FromTerminalID != nil {
			terminalID = *r.FromTerminalID
		}
		out = append(out, downstreamEdge{
			to:             node{classID: r.ToFeatureClassID, featureID: r.ToFeatureID},
			fromTerminalID: terminalID,
		})
	}
	return out, nil
}

// maxSweepNodes mirrors api/internal/trace's own maxTraceHops -- a
// defensive valve against a pathological data bug, not a real design
// limit.
const maxSweepNodes = 2000

// tree is a load flow's whole topology: BFS discovery order (root
// first, used for the forward sweep), each node's parent (nil/zero for
// the root), and each node's direct children (used for the backward
// sweep's demand accumulation).
type tree struct {
	order    []node
	parent   map[node]node
	children map[node][]node
	// childWinding is the winding role (primary/secondary/tertiary/ground/other,
	// or "" for a terminal-less sending side) of the terminal each child left its
	// parent through -- resolved from the discovering edge's from_terminal_id.
	// Only meaningful for a child of a transformer; used by the sweep to route a
	// three-winding transformer's children to the LV vs TV output.
	childWinding map[node]string
}

// meshBreak is one loop-closing edge found while building the tree: an edge
// from an already-in-tree node (closer) to another already-visited node
// (existing) that, if added, would give existing a second parent. The
// weakly-meshed load flow (mesh.go) records these instead of failing, then
// closes each loop by compensation; the radial-only callers never see them
// (they pass breaks = nil and buildTree rejects the mesh as before).
type meshBreak struct {
	closer   node // the in-tree "from" node whose extra edge closed the loop
	existing node // the already-visited "to" node the extra edge reached
}

// buildTree walks downstream from start, honoring live barriers
// exactly like api/internal/trace's bfs. When breaks is nil it -- unlike a
// plain membership BFS -- rejects outright the moment a second, independent
// path reaches an already-visited node: a load-flow sweep's backward/
// forward passes both assume a single unambiguous parent per node
// (Kersting's method is defined over a tree), a node reached two ways
// is a mesh/loop, and rather than silently picking one path (which would
// just be wrong) it fails loudly and names the offending node.
//
// When breaks is non-nil (the weakly-meshed load flow, POST
// /analysis/meshed-load-flow), a second path to an already-visited node is
// not fatal: the loop-closing edge is recorded in *breaks and skipped,
// leaving a radial spanning tree the ordinary sweep still solves, and
// mesh.go then closes each recorded loop by compensation. Every other caller
// passes nil and keeps the strict radial-only contract byte for byte.
func buildTree(ctx context.Context, db bun.IDB, start node, barriers map[string]barrierCheck, loader *featureClassLoader, closed map[node]bool, breaks *[]meshBreak) (*tree, []node, error) {
	visited := map[node]bool{start: true}
	parent := map[node]node{}
	children := map[node][]node{}
	childTerminal := map[node]string{}
	queue := []node{start}
	order := []node{start}
	var barrierHits []node

	for len(queue) > 0 && len(order) < maxSweepNodes {
		current := queue[0]
		queue = queue[1:]

		isBarrier, err := checkBarrier(ctx, db, current, barriers, loader, closed)
		if err != nil {
			return nil, nil, err
		}
		if isBarrier {
			barrierHits = append(barrierHits, current)
			continue
		}

		edges, err := downstreamNeighbors(ctx, db, current)
		if err != nil {
			return nil, nil, err
		}
		for _, e := range edges {
			n := e.to
			if visited[n] {
				if breaks != nil {
					// Weakly-meshed run: record the loop-closing edge and keep the
					// spanning tree radial by not re-parenting n. existing keeps its
					// first-discovered parent; mesh.go compensates the loop.
					*breaks = append(*breaks, meshBreak{closer: current, existing: n})
					continue
				}
				return nil, nil, invalid(
					"the network downstream of the start feature is not radial (loop/mesh detected at feature class id %s, feature %s reached by more than one path) -- load flow requires a tree, run LOOP_CHECK_TRACE first to locate and eliminate the mesh",
					n.classID, n.featureID,
				)
			}
			visited[n] = true
			parent[n] = current
			children[current] = append(children[current], n)
			childTerminal[n] = e.fromTerminalID
			order = append(order, n)
			queue = append(queue, n)
		}
	}

	childWinding, err := resolveChildWindings(ctx, db, childTerminal)
	if err != nil {
		return nil, nil, err
	}

	return &tree{order: order, parent: parent, children: children, childWinding: childWinding}, barrierHits, nil
}

// resolveChildWindings maps each child to the winding role of the terminal it
// left its parent through, one batch query over the distinct terminal ids seen.
// A child whose edge had no sending-side terminal (the common case -- a
// conductor feeding a junction) maps to "" and the sweep treats it as an
// ordinary inherited-voltage child.
func resolveChildWindings(ctx context.Context, db bun.IDB, childTerminal map[node]string) (map[node]string, error) {
	idSet := map[string]struct{}{}
	for _, tid := range childTerminal {
		if tid != "" {
			idSet[tid] = struct{}{}
		}
	}

	roleByID := make(map[string]string, len(idSet))
	if len(idSet) > 0 {
		ids := make([]string, 0, len(idSet))
		for id := range idSet {
			ids = append(ids, id)
		}
		var rows []terminalRoleRow
		if err := db.NewSelect().Model(&rows).Where("id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
			return nil, fmt.Errorf("powerflow: resolving terminal roles: %w", err)
		}
		for _, r := range rows {
			roleByID[r.ID] = r.Role
		}
	}

	out := make(map[node]string, len(childTerminal))
	for child, tid := range childTerminal {
		out[child] = roleByID[tid] // "" for tid == "" or an unresolved id
	}
	return out, nil
}

// nodeClassCodes batch-resolves every distinct feature_class_id across
// a set of nodes into its code -- same pattern api/internal/trace's own
// nodeClassCodes already established.
func nodeClassCodes(ctx context.Context, db bun.IDB, nodes []node) (map[string]string, error) {
	idSet := map[string]struct{}{}
	for _, n := range nodes {
		idSet[n.classID] = struct{}{}
	}
	if len(idSet) == 0 {
		return map[string]string{}, nil
	}
	ids := make([]string, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}

	var fcs []metadata.FeatureClass
	if err := db.NewSelect().Model(&fcs).Where("id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("powerflow: loading feature classes: %w", err)
	}
	out := make(map[string]string, len(fcs))
	for _, fc := range fcs {
		out[fc.ID] = fc.Code
	}
	return out, nil
}

func toFeatureRefs(nodes []node, classCodes map[string]string) []featureRef {
	out := make([]featureRef, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, featureRef{FeatureClass: classCodes[n.classID], FeatureID: n.featureID})
	}
	return out
}
