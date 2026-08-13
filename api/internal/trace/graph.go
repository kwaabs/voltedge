package trace

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
// the internal graph vertex identity used throughout this file. Class
// id, not class code, because runtime.feature_connectivity itself
// stores ids -- resolving codes only ever happens at the request/
// response boundary (resolveNode, nodeClassCodes), never mid-traversal.
type node struct {
	classID   string
	featureID string
}

// edgeRow is a read-only projection of runtime.feature_connectivity
// (migrations/0016) -- only the columns a traversal actually needs.
// Deliberately not api/internal/connectivity's own Connectivity model:
// same reasoning that package gives for keeping its own copy of
// resolveEndpoint's shape instead of importing crud's -- this package
// reads the table, connectivity owns writing it, and neither needs the
// other's full column set (terminal ids, established_at/by mean
// nothing to a traversal).
type edgeRow struct {
	bun.BaseModel `bun:"table:runtime.feature_connectivity,alias:feature_connectivity"`

	FromFeatureClassID string `bun:"from_feature_class_id"`
	FromFeatureID      string `bun:"from_feature_id"`
	ToFeatureClassID   string `bun:"to_feature_class_id"`
	ToFeatureID        string `bun:"to_feature_id"`
}

// barrierCheck is one feature class's live-stop condition, resolved
// once per trace run (see resolveBarriers) from a trace_barrier_rules
// row's field/value ids into names a live feature row's attributes can
// actually be compared against. Only one check per feature class is
// kept -- if a class ever has more than one trace_barrier_rules row for
// the same trace_configurations profile, the last one loaded wins; not
// a real scenario this pass's seed data or design anticipates, just
// worth knowing if metadata authoring ever adds a second one.
type barrierCheck struct {
	fieldName   string
	haltsOnCode string
}

// resolveNode validates a featureRef the same way
// api/internal/connectivity's resolveEndpoint does for one side of a
// connection (feature class exists, feature row exists, not soft
// deleted) -- simpler, since a trace has no terminal/subtype to resolve,
// just the node itself.
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

// resolveBarriers loads and resolves every trace_barrier_rules row for
// one trace_configurations id into a lookup by feature_class_id.
// Returns an empty map without even querying trace_barrier_rules when
// stopAtOpenDevices is false -- a config that doesn't respect barriers
// shouldn't have authored barrier rows silently take effect anyway.
func resolveBarriers(ctx context.Context, db bun.IDB, traceConfigurationID string, stopAtOpenDevices bool) (map[string]barrierCheck, error) {
	if !stopAtOpenDevices {
		return map[string]barrierCheck{}, nil
	}

	rules, err := metadata.LoadTraceBarrierRules(ctx, db, traceConfigurationID)
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
		return nil, fmt.Errorf("trace: loading barrier fields: %w", err)
	}
	fieldNames := make(map[string]string, len(fields))
	for _, f := range fields {
		fieldNames[f.ID] = f.Name
	}

	var values []metadata.DomainValue
	if err := db.NewSelect().Model(&values).Where("id IN (?)", bun.In(valueIDs)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("trace: loading barrier halt values: %w", err)
	}
	valueCodes := make(map[string]string, len(values))
	for _, v := range values {
		valueCodes[v.ID] = v.Code
	}

	out := make(map[string]barrierCheck, len(rules))
	for _, r := range rules {
		out[r.FeatureClassID] = barrierCheck{
			fieldName:   fieldNames[r.FieldID],
			haltsOnCode: valueCodes[r.HaltsOnValueID],
		}
	}
	return out, nil
}

// featureClassLoader lazily resolves and caches a metadata.Loaded per
// feature class id, encountered during a single trace run -- checkBarrier
// needs a full Loaded to call crud.GetFeature, but only for the (small)
// set of classes that actually have a barrier condition, not every class
// a trace happens to pass through.
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
			return nil, fmt.Errorf("trace: resolving feature class %s: %w", classID, err)
		}
		code = fc.Code
		l.codeByID[classID] = code
	}

	loaded, err := metadata.Load(ctx, db, code)
	if err != nil {
		return nil, fmt.Errorf("trace: loading %s: %w", code, err)
	}
	l.loadedByID[classID] = loaded
	return loaded, nil
}

// checkBarrier reports whether a node's *current, live* attribute value
// matches its feature class's barrier condition (if it has one) --
// "device state is checked live at each hop, never baked into the
// graph" (docs/metadata-core.md). A feature class with no barrier
// condition at all short-circuits before ever touching the database.
func checkBarrier(ctx context.Context, db bun.IDB, n node, barriers map[string]barrierCheck, loader *featureClassLoader) (bool, error) {
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
		// A dangling connectivity row pointing at a since-purged feature
		// shouldn't fail the whole trace -- treat it as a dead end
		// instead, same as a node with simply no further edges.
		if crud.IsNotFoundError(err) {
			return false, nil
		}
		return false, err
	}

	// crud.GetFeature nests every custom column under "attributes"
	// (see crud/sql.go's formatRow) -- device_state lives there, not at
	// the row map's top level alongside id/status/geometry.
	attrs, _ := row["attributes"].(map[string]any)
	value, _ := attrs[check.fieldName].(string)
	return value == check.haltsOnCode, nil
}

// neighborsOf returns every node directly reachable from n through
// runtime.feature_connectivity, filtered by direction: DOWNSTREAM only
// follows an edge where n is the "from" side (walking toward "to"),
// UPSTREAM only follows the reverse, anything else (CONNECTED,
// SHORTEST_PATH) treats every edge touching n as undirected.
func neighborsOf(ctx context.Context, db bun.IDB, n node, direction string) ([]node, error) {
	var rows []edgeRow
	q := db.NewSelect().Model(&rows)
	switch direction {
	case "DOWNSTREAM":
		q = q.Where("from_feature_id = ? AND from_feature_class_id = ?", n.featureID, n.classID)
	case "UPSTREAM":
		q = q.Where("to_feature_id = ? AND to_feature_class_id = ?", n.featureID, n.classID)
	default:
		q = q.Where("(from_feature_id = ? AND from_feature_class_id = ?) OR (to_feature_id = ? AND to_feature_class_id = ?)",
			n.featureID, n.classID, n.featureID, n.classID)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("trace: loading neighbors: %w", err)
	}

	out := make([]node, 0, len(rows))
	for _, r := range rows {
		from := node{classID: r.FromFeatureClassID, featureID: r.FromFeatureID}
		to := node{classID: r.ToFeatureClassID, featureID: r.ToFeatureID}
		switch direction {
		case "DOWNSTREAM":
			out = append(out, to)
		case "UPSTREAM":
			out = append(out, from)
		default:
			if from == n {
				out = append(out, to)
			} else {
				out = append(out, from)
			}
		}
	}
	return out, nil
}

// maxTraceHops bounds how many nodes a single trace will ever visit --
// purely a defensive valve against a pathological data bug (e.g. a
// malformed pair of connectivity rows forming an unexpected cycle
// through nodes a barrier should have stopped at), not a real design
// limit. Real utility networks this app is modeled on are nowhere near
// this size yet.
const maxTraceHops = 2000

// bfs walks the graph breadth-first from start, honoring direction and
// stopping expansion (but not visiting) at any node checkBarrier flags.
// Always tracks parent pointers, even for a direction that never uses
// them (CONNECTED/DOWNSTREAM/UPSTREAM's callers just ignore the
// returned map) -- cheap enough not to bother special-casing, and
// SHORTEST_PATH is the one direction that needs them for
// reconstructPath. When target is non-nil, returns as soon as it's
// discovered rather than exhausting the whole reachable set -- correct
// for shortest-path because BFS discovers nodes in non-decreasing
// distance order, so the first discovery is already a shortest path.
func bfs(ctx context.Context, db bun.IDB, start node, direction string, barriers map[string]barrierCheck, loader *featureClassLoader, target *node) (order []node, barrierHits []node, parent map[node]node, found bool, err error) {
	visited := map[node]bool{start: true}
	parent = map[node]node{}
	queue := []node{start}
	order = []node{start}
	barrierSet := map[node]bool{}
	found = target != nil && start == *target

	for len(queue) > 0 && len(order) < maxTraceHops {
		current := queue[0]
		queue = queue[1:]

		isBarrier, berr := checkBarrier(ctx, db, current, barriers, loader)
		if berr != nil {
			err = berr
			return
		}
		if isBarrier {
			barrierSet[current] = true
			continue
		}

		var neighbors []node
		neighbors, err = neighborsOf(ctx, db, current, direction)
		if err != nil {
			return
		}
		for _, n := range neighbors {
			if visited[n] {
				continue
			}
			visited[n] = true
			parent[n] = current
			order = append(order, n)
			queue = append(queue, n)
			if target != nil && n == *target {
				found = true
			}
		}
		if found {
			break
		}
	}

	barrierHits = make([]node, 0, len(barrierSet))
	for n := range barrierSet {
		barrierHits = append(barrierHits, n)
	}
	err = nil
	return
}

// reconstructPath walks bfs's parent map backward from target to start
// and reverses it into a start-to-target path. Only ever called when
// bfs reported found = true, so every step is guaranteed present.
func reconstructPath(parent map[node]node, start, target node) []node {
	path := []node{target}
	current := target
	for current != start {
		p, ok := parent[current]
		if !ok {
			return nil
		}
		path = append(path, p)
		current = p
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// nodeClassCodes batch-resolves every distinct feature_class_id across
// a set of nodes into its code, for building the response's featureRefs
// -- one query regardless of how many nodes share a class, same pattern
// api/internal/connectivity's loadFeatureClassCodes already established.
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
		return nil, fmt.Errorf("trace: loading feature classes: %w", err)
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

// runTrace is the whole engine's single entry point: resolve the named
// trace_configurations profile and its direction, validate the start
// (and, for a shortest_path trace, target) feature, resolve barrier
// conditions, walk the graph, and format the result.
func runTrace(ctx context.Context, db bun.IDB, req traceRequest) (*traceResponse, error) {
	if req.TraceConfiguration == "" {
		return nil, invalid("trace_configuration is required")
	}
	if req.Start.FeatureClass == "" || req.Start.FeatureID == "" {
		return nil, invalid("start.feature_class and start.feature_id are required")
	}

	tc, err := metadata.LoadTraceConfiguration(ctx, db, req.TraceConfiguration)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("trace configuration %q does not exist", req.TraceConfiguration)
		}
		return nil, err
	}
	direction, err := metadata.DomainValueCode(ctx, db, tc.DirectionValueID)
	if err != nil {
		return nil, err
	}

	start, err := resolveNode(ctx, db, req.Start)
	if err != nil {
		return nil, err
	}

	var target *node
	if direction == "SHORTEST_PATH" {
		if req.Target == nil {
			return nil, invalid("target is required for a shortest_path trace")
		}
		t, err := resolveNode(ctx, db, *req.Target)
		if err != nil {
			return nil, err
		}
		target = &t
	}

	barriers, err := resolveBarriers(ctx, db, tc.ID, tc.StopAtOpenDevices)
	if err != nil {
		return nil, err
	}

	visited, barrierHits, parent, found, err := bfs(ctx, db, start, direction, barriers, newFeatureClassLoader(), target)
	if err != nil {
		return nil, err
	}

	var featureNodes []node
	if direction == "SHORTEST_PATH" {
		if found {
			featureNodes = reconstructPath(parent, start, *target)
		}
	} else {
		featureNodes = visited
	}

	allNodes := make([]node, 0, len(featureNodes)+len(barrierHits))
	allNodes = append(allNodes, featureNodes...)
	allNodes = append(allNodes, barrierHits...)
	classCodes, err := nodeClassCodes(ctx, db, allNodes)
	if err != nil {
		return nil, err
	}

	resp := &traceResponse{
		TraceConfiguration: tc.Code,
		Direction:          direction,
		Start:              req.Start,
		Features:           toFeatureRefs(featureNodes, classCodes),
		Barriers:           toFeatureRefs(barrierHits, classCodes),
	}
	if direction == "SHORTEST_PATH" {
		resp.PathFound = &found
	}
	return resp, nil
}
