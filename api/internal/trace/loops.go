package trace

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/crud"
)

// Loop (mesh) detection, the primitive docs/phase-4-trace-engine.md's "what's
// still open" list named alongside isolation. A healthy distribution network is
// radial -- a tree -- so any cycle in runtime.feature_connectivity is worth
// flagging: an accidental double-feed, a ring left closed, or a deliberate mesh a
// reader wants confirmed. This answers it per feature, in a chosen direction.
//
// Semantics. The search is scoped by DIRECTION but detected UNDIRECTED, and that
// split is deliberate: a ring splits at its feed apex under a strict directional
// walk (the two arms leave the apex "downstream" and meet at the far side, where
// the convergence edge is downstream from both sides), so a purely directional
// back-edge test misses it. Instead we first gather the region reachable in the
// requested direction -- the upstream supply cone, the downstream load cone, or
// (for "both") the whole connected component -- then look for a cycle UNDIRECTED
// within that region. So:
//   - downstream: a load-side ring whose feed point is the start or above it
//     (both arms hang below the start) -- "could a load below me be fed two ways".
//   - upstream: a supply-side ring whose two arms reconnect above the start --
//     "am I (or something above me) fed from more than one direction".
//   - both: any loop anywhere in the start's connected network, including one the
//     start merely sits on but is fed through from elsewhere.
//
// A loop reported is the ordered ring of features closing the cycle; the client
// highlights them on the map exactly like a trace result. The same deleted/
// lifecycle edge gating every other walk honours applies (neighborsOf), and the
// territory scope gate discloses only loops wholly inside the caller's scope.

const maxReportedLoops = 25

// loopCheckRequest is the POST /trace/loops body. Direction is "upstream",
// "downstream" or "both" (the default) -- see this file's semantics note. AsBuilt
// (default false) ignores switch state: by default an OPEN device breaks the walk
// (a normally-open ring is radial IN OPERATION, so it is NOT a live loop), which is
// the question an operator asks; AsBuilt=true reports every physical ring regardless
// of switch position (the planning / as-constructed view).
type loopCheckRequest struct {
	Start     featureRef `json:"start"`
	Direction string     `json:"direction,omitempty"`
	AsBuilt   bool       `json:"as_built,omitempty"`
}

// networkLoop is one detected cycle: the ordered ring of features that close it.
type networkLoop struct {
	Features []featureRef `json:"features"`
	Length   int          `json:"length"`
}

// loopCheckResponse is the whole result of one loop check. HasLoop is the quick
// yes/no; Loops carries up to maxReportedLoops rings for the reader to inspect
// and highlight. Direction echoes the resolved search direction. Truncated is
// true when the region walk or the cycle search hit the maxTraceHops valve, so
// the result may be incomplete (there could be loops past the cut).
type loopCheckResponse struct {
	Start     featureRef    `json:"start"`
	Direction string        `json:"direction"`
	HasLoop   bool          `json:"has_loop"`
	Loops     []networkLoop `json:"loops"`
	Truncated bool          `json:"truncated"`
}

// detectLoops resolves the start, scopes a region by direction, and searches that
// region for undirected cycles (see this file's doc for why direction scopes but
// does not direct the detection).
func detectLoops(ctx context.Context, db bun.IDB, req loopCheckRequest) (*loopCheckResponse, error) {
	if req.Start.FeatureClass == "" || req.Start.FeatureID == "" {
		return nil, invalid("start.feature_class and start.feature_id are required")
	}

	var dir, label string
	switch strings.ToLower(strings.TrimSpace(req.Direction)) {
	case "", "both", "connected":
		dir, label = "CONNECTED", "both"
	case "upstream":
		dir, label = "UPSTREAM", "upstream"
	case "downstream":
		dir, label = "DOWNSTREAM", "downstream"
	default:
		return nil, invalid("direction must be one of upstream, downstream, both (got %q)", req.Direction)
	}

	start, err := resolveNode(ctx, db, req.Start)
	if err != nil {
		return nil, err
	}

	loader := newFeatureClassLoader()
	// Phase 58 spatial gate: refuse an out-of-scope start, and below disclose only
	// loops wholly in scope. A no-op unless enforcement is on and the caller is scoped.
	scope, err := enforceScopeStart(ctx, db, loader, start, req.Start)
	if err != nil {
		return nil, err
	}

	// Respect switch state by default: an OPEN device is removed from the walk, so
	// a normally-open ring (radial in operation) is not reported as a live loop.
	// AsBuilt bypasses this to show every physical ring. openCache memoizes the
	// per-node device_state probe across both walks.
	respectState := !req.AsBuilt
	openCache := map[node]int8{}

	region, regionTrunc, err := reachableRegion(ctx, db, start, dir, loader, respectState, openCache)
	if err != nil {
		return nil, err
	}

	cycles, loopTrunc, err := loopsInRegion(ctx, db, start, region, loader, respectState, openCache)
	if err != nil {
		return nil, err
	}

	collected := make([]networkLoop, 0, len(cycles))
	for _, cyc := range cycles {
		if scope.apply {
			filtered, ferr := scope.filter(ctx, db, cyc, loader)
			if ferr != nil {
				return nil, ferr
			}
			// Only disclose a loop the caller may see in full -- a ring that dips
			// outside their territories isn't surfaced as a partial fragment.
			if len(filtered) != len(cyc) {
				continue
			}
		}
		classCodes, cerr := nodeClassCodes(ctx, db, cyc)
		if cerr != nil {
			return nil, cerr
		}
		refs := toFeatureRefs(cyc, classCodes)
		collected = append(collected, networkLoop{Features: refs, Length: len(refs)})
		if len(collected) >= maxReportedLoops {
			break
		}
	}

	return &loopCheckResponse{
		Start:     req.Start,
		Direction: label,
		HasLoop:   len(collected) > 0,
		Loops:     collected,
		Truncated: regionTrunc || loopTrunc,
	}, nil
}

// reachableRegion gathers every node reachable from start in the given direction
// ("UPSTREAM"/"DOWNSTREAM" = the directional cone; anything else = CONNECTED, the
// whole component). The set bounds where loopsInRegion searches. Bounded by
// maxTraceHops as a valve against a pathological graph.
func reachableRegion(ctx context.Context, db bun.IDB, start node, direction string, loader *featureClassLoader, respectState bool, openCache map[node]int8) (map[node]bool, bool, error) {
	region := map[node]bool{start: true}
	queue := []node{start}
	maxHops := traceMaxHopsFromEnv()
	truncated := false
	for len(queue) > 0 {
		if len(region) > maxHops {
			truncated = true
			break
		}
		cur := queue[0]
		queue = queue[1:]
		nbrs, err := conductingNeighbors(ctx, db, cur, direction, loader, respectState, openCache)
		if err != nil {
			return nil, false, err
		}
		for _, nb := range nbrs {
			if !region[nb] {
				region[nb] = true
				queue = append(queue, nb)
			}
		}
	}
	return region, truncated, nil
}

// open-device probe cache states (openCache values).
const (
	openUnknown int8 = 0
	openYes     int8 = 1
	openNo      int8 = 2
)

// conductingNeighbors is neighborsOf with the switch-state gate for loop walks:
// when respectState is true an OPEN switching device passes nothing through (it is
// removed from the graph), so a cur that is itself open yields no neighbours, and
// any neighbour that is an open device is dropped. The effect is that a ring broken
// by an open point is acyclic -- the operator's "is this a LIVE loop" question --
// while AsBuilt (respectState=false) walks the raw connectivity as before.
func conductingNeighbors(ctx context.Context, db bun.IDB, n node, direction string, loader *featureClassLoader, respectState bool, openCache map[node]int8) ([]node, error) {
	if respectState {
		open, err := nodeIsOpenDevice(ctx, db, n, loader, openCache)
		if err != nil {
			return nil, err
		}
		if open {
			return nil, nil // an open device conducts to nothing
		}
	}
	nbrs, err := neighborsOf(ctx, db, n, direction)
	if err != nil {
		return nil, err
	}
	if !respectState {
		return nbrs, nil
	}
	out := make([]node, 0, len(nbrs))
	for _, nb := range nbrs {
		open, err := nodeIsOpenDevice(ctx, db, nb, loader, openCache)
		if err != nil {
			return nil, err
		}
		if !open {
			out = append(out, nb)
		}
	}
	return out, nil
}

// nodeIsOpenDevice reports whether a node is a switching device currently OPEN --
// the same live device_state = "OPEN" signal checkBarrier tests. A class with no
// device_state field is never a switch (cached cheaply); a since-purged/unreadable
// row is treated as not-open (a dead end, not a barrier). Memoized in openCache so
// each node is probed at most once per loop check.
func nodeIsOpenDevice(ctx context.Context, db bun.IDB, n node, loader *featureClassLoader, openCache map[node]int8) (bool, error) {
	switch openCache[n] {
	case openYes:
		return true, nil
	case openNo:
		return false, nil
	}
	loaded, err := loader.loadedFor(ctx, db, n.classID)
	if err != nil {
		return false, err
	}
	if loaded.FieldByName("device_state") == nil {
		openCache[n] = openNo
		return false, nil
	}
	row, err := crud.GetFeature(ctx, db, loaded, n.featureID)
	if err != nil {
		openCache[n] = openNo // unreadable/purged -> not a live barrier
		return false, nil
	}
	open := false
	if attrs, ok := row["attributes"].(map[string]any); ok {
		if s, _ := attrs["device_state"].(string); s == "OPEN" {
			open = true
		}
	}
	if open {
		openCache[n] = openYes
	} else {
		openCache[n] = openNo
	}
	return open, nil
}

// nodePair is an undirected edge key: the two endpoints in a canonical order so
// an edge seen from either side maps to the same key.
type nodePair struct{ a, b node }

func orderNodePair(x, y node) nodePair {
	if x.classID < y.classID || (x.classID == y.classID && x.featureID < y.featureID) {
		return nodePair{x, y}
	}
	return nodePair{y, x}
}

// loopsInRegion runs an undirected BFS from start over CONNECTED edges, confined
// to region, and returns a cycle for every back-edge found (deduped, capped at
// maxReportedLoops). A back-edge is a CONNECTED edge to an already-tree node that
// is not the current node's tree-parent -- it closes a fundamental cycle, which
// reconstructCycle expands into the ring of nodes.
func loopsInRegion(ctx context.Context, db bun.IDB, start node, region map[node]bool, loader *featureClassLoader, respectState bool, openCache map[node]int8) ([][]node, bool, error) {
	parent := map[node]node{start: start}
	visited := map[node]bool{start: true}
	queue := []node{start}
	maxHops := traceMaxHopsFromEnv()
	truncated := false

	seenEdge := map[nodePair]bool{}
	var backEdges []nodePair

	for len(queue) > 0 {
		if len(visited) > maxHops {
			truncated = true
			break
		}
		cur := queue[0]
		queue = queue[1:]
		nbrs, err := conductingNeighbors(ctx, db, cur, "CONNECTED", loader, respectState, openCache)
		if err != nil {
			return nil, false, err
		}
		for _, nb := range nbrs {
			if !region[nb] {
				continue
			}
			// Each undirected edge is reachable from both ends; fold them to one key
			// so a tree edge is never also counted as a back-edge, and a back-edge is
			// recorded exactly once.
			key := orderNodePair(cur, nb)
			if seenEdge[key] {
				continue
			}
			seenEdge[key] = true
			if !visited[nb] {
				visited[nb] = true
				parent[nb] = cur
				queue = append(queue, nb)
			} else if nb != parent[cur] {
				backEdges = append(backEdges, nodePair{cur, nb})
			}
		}
	}

	loops := make([][]node, 0, len(backEdges))
	for _, be := range backEdges {
		cyc := reconstructCycle(parent, be.a, be.b)
		if len(cyc) >= 3 { // a real topological ring (a 2-node parallel pair isn't one here)
			loops = append(loops, cyc)
		}
		if len(loops) >= maxReportedLoops {
			break
		}
	}
	return loops, truncated, nil
}

// reconstructCycle turns a back-edge (u,v) plus the BFS parent map into the ring
// of nodes closing the cycle: v up to the lowest common ancestor, then back down
// to u. start is its own parent, so the climb always terminates (at worst at the
// root, which is then the common ancestor).
func reconstructCycle(parent map[node]node, u, v node) []node {
	ancU := map[node]bool{}
	for cur := u; ; {
		ancU[cur] = true
		p := parent[cur]
		if p == cur {
			break
		}
		cur = p
	}

	var lca node
	for cur := v; ; {
		if ancU[cur] {
			lca = cur
			break
		}
		p := parent[cur]
		if p == cur {
			lca = cur
			break
		}
		cur = p
	}

	// v .. lca (inclusive).
	cycle := []node{}
	for cur := v; ; {
		cycle = append(cycle, cur)
		if cur == lca {
			break
		}
		cur = parent[cur]
	}
	// u .. up-to-but-not-including lca, appended in reverse so the ring reads
	// contiguously v -> ... -> lca -> ... -> u.
	uSide := []node{}
	for cur := u; cur != lca; cur = parent[cur] {
		uSide = append(uSide, cur)
	}
	for i := len(uSide) - 1; i >= 0; i-- {
		cycle = append(cycle, uSide[i])
	}
	return cycle
}

// maxAuditLoops caps how many rings a whole-network audit scan reports, so a badly
// meshed deployment can't produce an unbounded finding list. When hit, Truncated is
// returned so ReconcileIssues does not auto-resolve loops that fell off the list.
const maxAuditLoops = 200

// DetectLoopsInScope enumerates every CLOSED loop reachable from a scope, switch
// state always respected (an open ring is not a loop). An empty scope scans the
// whole network (seeded from every connectivity endpoint); a non-empty scope seeds
// from those features, so the incremental audit finds loops touching the features
// that just changed. Each network component is walked once (componentDone), and
// rings are deduped by their member-id set. Each ring carries its lowest member
// voltage for the radial-vs-mesh policy. Bounded by maxTraceHops (per walk) and
// maxAuditLoops (total) -> Truncated.
func DetectLoopsInScope(ctx context.Context, db bun.IDB, scope []FeatureRef) ([]LoopRing, bool, error) {
	loader := newFeatureClassLoader()
	openCache := map[node]int8{}
	truncated := false

	seeds, err := loopSeeds(ctx, db, scope)
	if err != nil {
		return nil, false, err
	}

	componentDone := map[node]bool{}
	seenRing := map[string]bool{}
	var rings []LoopRing

	for _, seed := range seeds {
		if componentDone[seed] {
			continue
		}
		region, rtrunc, err := reachableRegion(ctx, db, seed, "CONNECTED", loader, true, openCache)
		if err != nil {
			return nil, false, err
		}
		truncated = truncated || rtrunc
		for n := range region {
			componentDone[n] = true
		}
		cycles, ltrunc, err := loopsInRegion(ctx, db, seed, region, loader, true, openCache)
		if err != nil {
			return nil, false, err
		}
		truncated = truncated || ltrunc
		for _, cyc := range cycles {
			key := ringKey(cyc)
			if seenRing[key] {
				continue
			}
			seenRing[key] = true
			classCodes, err := nodeClassCodes(ctx, db, cyc)
			if err != nil {
				return nil, false, err
			}
			ring := LoopRing{Features: toExportedRefs(toFeatureRefs(cyc, classCodes))}
			ring.MinVoltageV, err = ringMinVoltage(ctx, db, cyc, loader)
			if err != nil {
				return nil, false, err
			}
			rings = append(rings, ring)
			if len(rings) >= maxAuditLoops {
				return rings, true, nil
			}
		}
	}
	return rings, truncated, nil
}

// DetectLoopBreakOptions finds the live closed loops reachable from start (switch
// state respected) and, for each, the switchable devices ON the ring that could be
// opened to break it -- a feature whose class models device_state. In a ring every
// member stays fed when one point is opened, so any CLOSED device is a valid break;
// the first CLOSED one is flagged recommended. Backs POST /trace/loops/break-options.
func DetectLoopBreakOptions(ctx context.Context, db bun.IDB, start FeatureRef) ([]LoopBreakRing, bool, error) {
	startNode, err := resolveNode(ctx, db, featureRef{FeatureClass: start.FeatureClass, FeatureID: start.FeatureID})
	if err != nil {
		return nil, false, err
	}
	loader := newFeatureClassLoader()
	openCache := map[node]int8{}
	region, rt, err := reachableRegion(ctx, db, startNode, "CONNECTED", loader, true, openCache)
	if err != nil {
		return nil, false, err
	}
	cycles, lt, err := loopsInRegion(ctx, db, startNode, region, loader, true, openCache)
	if err != nil {
		return nil, false, err
	}

	out := make([]LoopBreakRing, 0, len(cycles))
	for _, cyc := range cycles {
		classCodes, err := nodeClassCodes(ctx, db, cyc)
		if err != nil {
			return nil, false, err
		}
		ring := LoopBreakRing{Features: toExportedRefs(toFeatureRefs(cyc, classCodes))}
		for _, n := range cyc {
			loaded, err := loader.loadedFor(ctx, db, n.classID)
			if err != nil {
				return nil, false, err
			}
			if loaded.FieldByName("device_state") == nil {
				continue // not an operable device
			}
			state := ""
			if row, err := crud.GetFeature(ctx, db, loaded, n.featureID); err == nil {
				if attrs, ok := row["attributes"].(map[string]any); ok {
					state, _ = attrs["device_state"].(string)
				}
			}
			ring.Candidates = append(ring.Candidates, LoopBreakCandidate{
				FeatureClass: classCodes[n.classID],
				FeatureID:    n.featureID,
				DeviceState:  state,
			})
		}
		for i := range ring.Candidates {
			if ring.Candidates[i].DeviceState == "CLOSED" {
				ring.Candidates[i].Recommended = true
				break
			}
		}
		out = append(out, ring)
	}
	return out, rt || lt, nil
}

// loopSeeds resolves the starting nodes for a scan: the given features (scoped
// audit), or every distinct live connectivity endpoint (whole-network). Only
// connected features can lie on a loop, so the whole-network seed list is exactly
// the connectivity endpoints -- nothing else need be walked.
func loopSeeds(ctx context.Context, db bun.IDB, scope []FeatureRef) ([]node, error) {
	if len(scope) > 0 {
		out := make([]node, 0, len(scope))
		for _, ref := range scope {
			n, err := resolveNode(ctx, db, featureRef{FeatureClass: ref.FeatureClass, FeatureID: ref.FeatureID})
			if err != nil {
				// A since-purged / unknown scoped feature simply contributes no seed.
				if isNotFoundError(err) {
					continue
				}
				return nil, err
			}
			out = append(out, n)
		}
		return out, nil
	}

	const q = `
SELECT fc, fid FROM (
    SELECT from_feature_class_id AS fc, from_feature_id AS fid FROM runtime.feature_connectivity
    UNION
    SELECT to_feature_class_id AS fc, to_feature_id AS fid FROM runtime.feature_connectivity
) t`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("trace: loop seeds: %w", err)
	}
	defer rows.Close()
	var out []node
	for rows.Next() {
		var n node
		if err := rows.Scan(&n.classID, &n.featureID); err != nil {
			return nil, fmt.Errorf("trace: scanning loop seed: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ringKey is a stable identity for a ring independent of starting point/direction:
// its member feature ids sorted and joined. The topology audit fingerprints on this
// so the same ring maps to one issue row and auto-resolves when broken.
func ringKey(cyc []node) string {
	ids := make([]string, len(cyc))
	for i, n := range cyc {
		ids[i] = n.featureID
	}
	sort.Strings(ids)
	return strings.Join(ids, "|")
}

// ringMinVoltage returns the lowest nominal voltage among a ring's members (nil if
// none carry one), read from each member's voltage_class/primary_voltage exactly as
// enrichNode does -- so the policy can tell an LV ring from an MV one.
func ringMinVoltage(ctx context.Context, db bun.IDB, cyc []node, loader *featureClassLoader) (*float64, error) {
	var min *float64
	for _, n := range cyc {
		loaded, err := loader.loadedFor(ctx, db, n.classID)
		if err != nil {
			return nil, err
		}
		hasField := false
		for _, f := range voltageClassFields {
			if loaded.FieldByName(f) != nil {
				hasField = true
				break
			}
		}
		if !hasField {
			continue
		}
		row, err := crud.GetFeature(ctx, db, loaded, n.featureID)
		if err != nil {
			continue
		}
		attrs, _ := row["attributes"].(map[string]any)
		for _, f := range voltageClassFields {
			s, _ := attrs[f].(string)
			if v, ok := parseVoltageClassCode(s); ok {
				if min == nil || v < *min {
					vv := v
					min = &vv
				}
				break
			}
		}
	}
	return min, nil
}
