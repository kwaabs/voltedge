package trace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

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
// actually be compared against. A class can carry MORE than one check --
// resolveBarriers keys a slice per class, and checkBarrier stops the
// traversal if ANY of them matches (OR-combined). That's what lets a
// single class halt on two independent conditions: DIST_SWITCH stops a
// downstream trace when device_state = OPEN *or* operational_state =
// ISOLATED/RETIRED (Phase 38 Slice 3) -- two orthogonal reasons a device
// isn't passing power, either of which should end the walk.
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
func resolveBarriers(ctx context.Context, db bun.IDB, traceConfigurationID string, stopAtOpenDevices bool) (map[string][]barrierCheck, error) {
	if !stopAtOpenDevices {
		return map[string][]barrierCheck{}, nil
	}

	rules, err := metadata.LoadTraceBarrierRules(ctx, db, traceConfigurationID)
	if err != nil {
		return nil, err
	}
	if len(rules) == 0 {
		return map[string][]barrierCheck{}, nil
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

	out := make(map[string][]barrierCheck, len(rules))
	for _, r := range rules {
		out[r.FeatureClassID] = append(out[r.FeatureClassID], barrierCheck{
			fieldName:   fieldNames[r.FieldID],
			haltsOnCode: valueCodes[r.HaltsOnValueID],
		})
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
func checkBarrier(ctx context.Context, db bun.IDB, n node, barriers map[string][]barrierCheck, loader *featureClassLoader) (bool, error) {
	checks, ok := barriers[n.classID]
	if !ok || len(checks) == 0 {
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
	// (see crud/sql.go's formatRow) -- device_state/operational_state live
	// there, not at the row map's top level alongside id/status/geometry.
	// One live row read serves every check on this class; the node is a
	// barrier if ANY of them matches (OR-combined, see barrierCheck's doc).
	attrs, _ := row["attributes"].(map[string]any)
	for _, check := range checks {
		value, _ := attrs[check.fieldName].(string)
		if value == check.haltsOnCode {
			return true, nil
		}
	}
	return false, nil
}

// nodeCircuitID returns a node's stored circuit membership -- the live
// circuit_id attribute value (Phase 37 Slice 2), or "" when the feature's
// class carries no circuit_id field, the value is unset, or the row has since
// been purged. Only a circuit-scoped trace (scope_to_circuit) calls it, to keep
// the walk on features sharing the start's circuit_id -- reading the *stored*
// Phase 37 membership, not re-deriving it. Reuses the same live-row read and
// class-loader cache checkBarrier uses, so a circuit trace pays one GetFeature
// per visited node, exactly like a barrier-respecting one.
func nodeCircuitID(ctx context.Context, db bun.IDB, n node, loader *featureClassLoader) (string, error) {
	loaded, err := loader.loadedFor(ctx, db, n.classID)
	if err != nil {
		return "", err
	}
	row, err := crud.GetFeature(ctx, db, loaded, n.featureID)
	if err != nil {
		// A dangling connectivity row pointing at a since-purged feature is a
		// dead end, not a trace failure -- same treatment as checkBarrier.
		if crud.IsNotFoundError(err) {
			return "", nil
		}
		return "", err
	}
	// circuit_id is a custom column, nested under "attributes" by formatRow;
	// scanRows normalizes the uuid to a string, so a set value compares cleanly
	// and an unset one (SQL NULL -> nil) falls through to "".
	attrs, _ := row["attributes"].(map[string]any)
	id, _ := attrs["circuit_id"].(string)
	return id, nil
}

// voltageClassFields are the attribute names a feature may carry its own
// nominal operating voltage under, in resolution priority: a conductor's
// voltage_class, then a transformer's primary_voltage (its source/high side
// -- the feeder voltage it sits on; a transformer's stepped-down secondary
// shows up as the nominal of the LV element below it, so one "nominal" per
// node stays unambiguous). Both hold a coded VOLTAGE_CLASS value
// ("11KV"/"400V") parsed by parseVoltageClassCode. A class carrying none of
// these transforms no voltage and reports no nominal.
var voltageClassFields = []string{"voltage_class", "primary_voltage"}

// parseVoltageClassCode turns a coded VOLTAGE_CLASS value ("11KV", "33KV",
// "400V") into volts -- a deliberate parse, not a fixed lookup, so any NkV/NV
// code resolves as the domain grows. This is the trace package's own copy of
// the same helper api/internal/powerflow already carries: kept local for the
// same each-package-keeps-its-own reason featureRef is duplicated, rather than
// importing powerflow into the trace engine the package doc deliberately keeps
// it decoupled from. ok = false for an empty or non-numeric code.
func parseVoltageClassCode(code string) (float64, bool) {
	s := strings.ToUpper(strings.TrimSpace(code))
	if s == "" {
		return 0, false
	}
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "KV"):
		mult = 1000
		s = strings.TrimSuffix(s, "KV")
	case strings.HasSuffix(s, "V"):
		s = strings.TrimSuffix(s, "V")
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n * mult, true
}

// enrichNode reads one node's live attributes once and pulls the two
// trace-display extras out of them: stored circuit membership (Phase 37
// circuit_id + circuit_ref) and nominal operating voltage. Reuses the same
// GetFeature + class-loader cache checkBarrier/nodeCircuitID use, so an
// enriched trace pays one live-row read per returned node -- the same cost a
// barrier-respecting or circuit-scoped walk already does. A since-purged
// feature (a dangling connectivity row) enriches to just its ref, the same
// dead-end treatment those two give.
func enrichNode(ctx context.Context, db bun.IDB, n node, code string, loader *featureClassLoader) (tracedFeature, error) {
	tf := tracedFeature{FeatureClass: code, FeatureID: n.featureID}

	loaded, err := loader.loadedFor(ctx, db, n.classID)
	if err != nil {
		return tf, err
	}
	row, err := crud.GetFeature(ctx, db, loaded, n.featureID)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return tf, nil
		}
		return tf, err
	}

	// Custom columns nest under "attributes" (crud/sql.go's formatRow);
	// scanRows normalizes the circuit_id uuid to a string, so a set value
	// reads cleanly and an unset one (SQL NULL -> nil) falls through to "".
	attrs, _ := row["attributes"].(map[string]any)
	tf.CircuitID, _ = attrs["circuit_id"].(string)
	tf.CircuitRef, _ = attrs["circuit_ref"].(string)
	for _, field := range voltageClassFields {
		raw, _ := attrs[field].(string)
		if v, ok := parseVoltageClassCode(raw); ok {
			tf.NominalVoltageV = &v
			break
		}
	}
	return tf, nil
}

// toTracedFeatures is toFeatureRefs's enriching counterpart: the Features
// list every trace-family response returns, each node carrying its circuit
// membership and nominal voltage (see tracedFeature). One GetFeature per
// node, cached through the shared loader.
func toTracedFeatures(ctx context.Context, db bun.IDB, nodes []node, classCodes map[string]string, loader *featureClassLoader) ([]tracedFeature, error) {
	out := make([]tracedFeature, 0, len(nodes))
	for _, n := range nodes {
		tf, err := enrichNode(ctx, db, n, classCodes[n.classID], loader)
		if err != nil {
			return nil, err
		}
		out = append(out, tf)
	}
	return out, nil
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
//
// targetClassID is a different, later-added concept from target above:
// target names one specific feature (shortest_path's destination),
// targetClassID names a whole feature *class* to stop at the first
// node of -- e.g. "walk upstream until you reach a DIST_XFMR." The
// start node itself never counts as a class-target hit even if its own
// class matches, so a trace started AT a transformer correctly finds
// the *next* one, not itself. crossTransformers controls what happens
// once a class-target node is reached: false (the trace_configurations
// default) stops expansion there, same as a barrier; true records the
// hit but keeps walking past it -- the real, enforced meaning that
// column only ever had as a stub before this.
//
// scopeCircuit, when non-nil (a scope_to_circuit config, migration 0069),
// confines the walk to the start's stored circuit: a neighbour is only
// enqueued/recorded when its own live circuit_id equals *scopeCircuit. An
// off-circuit neighbour (different, unset, or no circuit_id field at all) is
// the circuit boundary -- marked visited so a second edge to it isn't
// re-checked, but never traversed or returned. The start is always kept
// (resolved by the caller to carry *scopeCircuit in the first place).
func bfs(ctx context.Context, db bun.IDB, start node, direction string, barriers map[string][]barrierCheck, loader *featureClassLoader, target *node, targetClassID *string, crossTransformers bool, scopeCircuit *string) (order []node, barrierHits []node, targetHits []node, parent map[node]node, found bool, err error) {
	visited := map[node]bool{start: true}
	parent = map[node]node{}
	queue := []node{start}
	order = []node{start}
	barrierSet := map[node]bool{}
	targetSet := map[node]bool{}
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

		isClassTarget := targetClassID != nil && current != start && current.classID == *targetClassID
		if isClassTarget {
			targetSet[current] = true
			if !crossTransformers {
				continue
			}
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
			// Circuit-scoped: an off-circuit neighbour is the boundary. Mark it
			// visited (so a second edge to it isn't re-read) but never enqueue
			// or record it -- the walk stays on the start's circuit_id.
			if scopeCircuit != nil {
				cid, cerr := nodeCircuitID(ctx, db, n, loader)
				if cerr != nil {
					err = cerr
					return
				}
				if cid != *scopeCircuit {
					visited[n] = true
					continue
				}
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
	targetHits = make([]node, 0, len(targetSet))
	for n := range targetSet {
		targetHits = append(targetHits, n)
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

// listConfigurations returns every trace_configurations row, each
// resolved to its raw direction code -- backs GET /trace/configurations.
func listConfigurations(ctx context.Context, db bun.IDB) ([]traceConfigurationSummary, error) {
	configs, err := metadata.ListTraceConfigurations(ctx, db)
	if err != nil {
		return nil, err
	}
	if len(configs) == 0 {
		return []traceConfigurationSummary{}, nil
	}

	valueIDs := make([]string, len(configs))
	for i, c := range configs {
		valueIDs[i] = c.DirectionValueID
	}
	var values []metadata.DomainValue
	if err := db.NewSelect().Model(&values).Where("id IN (?)", bun.In(valueIDs)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("trace: loading directions: %w", err)
	}
	codeByID := make(map[string]string, len(values))
	for _, v := range values {
		codeByID[v.ID] = v.Code
	}

	out := make([]traceConfigurationSummary, len(configs))
	for i, c := range configs {
		out[i] = traceConfigurationSummary{Code: c.Code, Name: c.Name, Direction: codeByID[c.DirectionValueID]}
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

	loader := newFeatureClassLoader()

	// A circuit-scoped config (scope_to_circuit) confines the walk to the start
	// feature's stored circuit. Resolve that membership once here so bfs can gate
	// every neighbour against it; a start with no circuit is a clear, actionable
	// 400 (assign the circuit first) rather than a walk with nothing to scope to.
	var scopeCircuit *string
	if tc.ScopeToCircuit {
		cid, err := nodeCircuitID(ctx, db, start, loader)
		if err != nil {
			return nil, err
		}
		if cid == "" {
			return nil, invalid("start feature %s (%s) is not assigned to a circuit -- assign it to one before running a circuit trace", req.Start.FeatureID, req.Start.FeatureClass)
		}
		scopeCircuit = &cid
	}

	visited, barrierHits, targetHits, parent, found, err := bfs(ctx, db, start, direction, barriers, loader, target, tc.TargetClassID, tc.CrossTransformers, scopeCircuit)
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

	allNodes := make([]node, 0, len(featureNodes)+len(barrierHits)+len(targetHits))
	allNodes = append(allNodes, featureNodes...)
	allNodes = append(allNodes, barrierHits...)
	allNodes = append(allNodes, targetHits...)
	classCodes, err := nodeClassCodes(ctx, db, allNodes)
	if err != nil {
		return nil, err
	}

	features, err := toTracedFeatures(ctx, db, featureNodes, classCodes, loader)
	if err != nil {
		return nil, err
	}

	resp := &traceResponse{
		TraceConfiguration: tc.Code,
		Direction:          direction,
		Start:              req.Start,
		Features:           features,
		Barriers:           toFeatureRefs(barrierHits, classCodes),
	}
	if direction == "SHORTEST_PATH" {
		resp.PathFound = &found
	}
	if tc.TargetClassID != nil {
		resp.Targets = toFeatureRefs(targetHits, classCodes)
		targetsFound := len(targetHits) > 0
		resp.TargetsFound = &targetsFound
	}
	return resp, nil
}
