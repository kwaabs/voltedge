package trace

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// The "is this a critical facility" signal is no longer a hardcoded pair here:
// it is DERIVED from the graded load_priority tier (rank <= P2) across the load
// leaves service.go defines (isLoadLeaf / meterCountsAsLoad -- SERVICE_POINTs
// and PRIMARY METERs), with is_critical still honoured as a fallback inside
// resolveServicePriority. See the critical-facility check in isolate below.

// isolationRequest is the POST /trace/isolate body. TraceConfiguration
// must name a DOWNSTREAM-direction row -- its barrier settings (stop at
// other open devices, etc.) govern the de-energized leg below.
// BoundaryClass is optional and defaults to Device's own feature class
// -- "the nearest other switch" is the natural boundary to look for
// when isolating a switch, without the caller having to name it.
type isolationRequest struct {
	TraceConfiguration string     `json:"trace_configuration"`
	Device             featureRef `json:"device"`
	BoundaryClass      string     `json:"boundary_class,omitempty"`
}

// isolationResponse is the whole result of one isolation run.
// DeEnergized is everything that would lose power if Device were
// opened right now -- computed without mutating Device's actual
// current state (see isolate's own comment on how). UpstreamBoundary
// is the nearest node upstream of Device matching BoundaryClass, i.e.
// the real-world "where's the isolation boundary" answer -- nil if
// none exists upstream (Device is fed all the way back to an
// unmodeled source with nothing of that class in between).
// CriticalFacilities is the subset of DeEnergized flagged
// is_critical on a SERVICE_POINT. PossibleBackfeedPaths is every
// physically-connected neighbor of anything in DeEnergized that isn't
// already accounted for by this result (the device, DeEnergized
// itself, or the boundary) -- a live connection the directed
// downstream walk wouldn't have needed to cross, e.g. a normally-open
// tie point that's actually closed right now, or any meshed
// connection. Reported as a possibility for a human to verify, not a
// claimed guarantee either way -- see isolate's own comment.
type isolationResponse struct {
	Device                featureRef   `json:"device"`
	DeEnergized           []featureRef `json:"de_energized"`
	UpstreamBoundary      *featureRef  `json:"upstream_boundary,omitempty"`
	CriticalFacilities    []featureRef `json:"critical_facilities,omitempty"`
	PossibleBackfeedPaths []featureRef `json:"possible_backfeed_paths,omitempty"`
	// Service is the Phase 62 consumption rollup of the de-energized set -- how
	// much load, how many customers, of what restoration priority, this
	// isolation drops. The graded successor to CriticalFacilities (which stays
	// as the explicit list of the individual critical SPs); Service.Critical is
	// that same set's totals plus the P1 life-support split within it. See
	// ServiceRollup.
	Service *ServiceRollup `json:"service,omitempty"`
	// Truncated is true when a downstream de-energization walk (or the upstream
	// boundary walk) hit the maxTraceHops cap -- the de-energized set is
	// incomplete and the client should warn.
	Truncated bool `json:"truncated"`
}

// isolate composes two of the engine's existing primitives rather than
// walking the graph itself -- exactly what docs/phase-4-trace-engine.md's
// own "isolation... composes two of the four primitive traces" framing
// described before either half existed:
//
//  1. De-energized set: a downstream trace from each of Device's own
//     downstream neighbors (not from Device itself). Starting past
//     Device, rather than mutating its live device_state to simulate
//     "open," is what lets this reflect "if I opened this right now"
//     without ever writing fake data -- checkBarrier always reads a
//     feature's real, current attribute value, so faking the state
//     would mean either a real write (with all its side effects) or a
//     parallel, less-trustworthy code path that ignores live state.
//  2. Upstream boundary: an UPSTREAM walk from Device itself using the
//     same target_class_id stop mechanism runTrace uses, targeting
//     BoundaryClass (or Device's own class by default) -- the nearest
//     protective device found is the isolation boundary.
//
// Two more checks run against that result before returning, both
// safety-oriented and both best-effort rather than exhaustive: which
// of the de-energized set is a critical facility (a plain attribute
// cross-reference), and which of it has any live connection this
// isolation didn't already account for (a possible backfeed path --
// see isolationResponse's own comment on what that does and doesn't
// claim).
func isolate(ctx context.Context, db bun.IDB, req isolationRequest) (*isolationResponse, error) {
	if req.TraceConfiguration == "" {
		return nil, invalid("trace_configuration is required")
	}
	if req.Device.FeatureClass == "" || req.Device.FeatureID == "" {
		return nil, invalid("device.feature_class and device.feature_id are required")
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
	if direction != "DOWNSTREAM" {
		return nil, invalid("isolation requires a DOWNSTREAM trace_configuration, %q is %s", req.TraceConfiguration, direction)
	}

	device, err := resolveNode(ctx, db, req.Device)
	if err != nil {
		return nil, err
	}

	barriers, err := resolveBarriers(ctx, db, tc.ID, tc.StopAtOpenDevices)
	if err != nil {
		return nil, err
	}
	loader := newFeatureClassLoader()

	// Phase 58 spatial gate: refuse an out-of-scope device and disclose only
	// in-scope features in every result set below, exactly as runTrace does (see
	// scope.go). A no-op unless enforcement is on and the caller is scoped.
	scope, err := enforceScopeStart(ctx, db, loader, device, req.Device)
	if err != nil {
		return nil, err
	}

	downstreamNeighbors, err := neighborsOf(ctx, db, device, "DOWNSTREAM")
	if err != nil {
		return nil, err
	}
	deEnergized := map[node]bool{}
	truncated := false
	for _, n := range downstreamNeighbors {
		visited, _, _, _, _, _, dTruncated, err := bfs(ctx, db, n, "DOWNSTREAM", barriers, loader, nil, nil, false, false, nil)
		if err != nil {
			return nil, err
		}
		truncated = truncated || dTruncated
		for _, v := range visited {
			deEnergized[v] = true
		}
	}
	deEnergizedNodes := make([]node, 0, len(deEnergized))
	for n := range deEnergized {
		deEnergizedNodes = append(deEnergizedNodes, n)
	}

	// Upstream isolation boundary: the nearest switching device above Device.
	// A request that names a boundary_class is honored as exactly that one
	// class. Otherwise the boundary is the nearest CONTROLLER -- any class
	// carrying functional_role='controller' (migration 0091), which spans
	// every switch/breaker/recloser/isolator/fuse rather than only Device's
	// own class, so isolating an MV_BREAKER correctly stops at an upstream
	// MV_SWITCH (or vice versa), whatever the nearest switching device is.
	// Falls back to Device's own class when no controller role is stamped yet,
	// preserving the pre-role behavior on an unclassified deployment.
	// Isolation reports its boundaries as the switches to OPEN, and a transformer
	// isn't openable -- so it bounds only on protective/switching devices.
	boundaryClassIDs, err := boundaryClassSet(ctx, db, req.BoundaryClass, req.Device.FeatureClass, isolationBoundaryRoles)
	if err != nil {
		return nil, err
	}

	_, _, boundaryHits, _, _, _, upTruncated, err := bfs(ctx, db, device, "UPSTREAM", map[string][]barrierCheck{}, loader, nil, boundaryClassIDs, false, false, nil)
	if err != nil {
		return nil, err
	}
	truncated = truncated || upTruncated

	// Phase 58: drop out-of-scope features from the de-energized set and the
	// upstream boundary before anything derived from them (backfeed, critical,
	// service) is computed, so every disclosed set is in-scope. The backfeed
	// neighbours below are filtered after they are gathered.
	if scope.apply {
		if deEnergizedNodes, err = scope.filter(ctx, db, deEnergizedNodes, loader); err != nil {
			return nil, err
		}
		if boundaryHits, err = scope.filter(ctx, db, boundaryHits, loader); err != nil {
			return nil, err
		}
	}

	// Backfeed check: every physically-connected neighbor (either
	// direction -- a real second source doesn't announce which way its
	// edge happens to be recorded) of anything de-energized, that isn't
	// already accounted for by this result.
	accounted := map[node]bool{device: true}
	for _, n := range deEnergizedNodes {
		accounted[n] = true
	}
	for _, n := range boundaryHits {
		accounted[n] = true
	}
	backfeedSet := map[node]bool{}
	for _, n := range deEnergizedNodes {
		neighbors, nerr := neighborsOf(ctx, db, n, "CONNECTED")
		if nerr != nil {
			return nil, nerr
		}
		for _, nb := range neighbors {
			if !accounted[nb] {
				backfeedSet[nb] = true
			}
		}
	}
	backfeedNodes := make([]node, 0, len(backfeedSet))
	for n := range backfeedSet {
		backfeedNodes = append(backfeedNodes, n)
	}
	// Phase 58: a backfeed path outside the user's territories is not disclosed.
	if scope.apply {
		if backfeedNodes, err = scope.filter(ctx, db, backfeedNodes, loader); err != nil {
			return nil, err
		}
	}

	allNodes := make([]node, 0, len(deEnergizedNodes)+len(boundaryHits)+len(backfeedNodes))
	allNodes = append(allNodes, deEnergizedNodes...)
	allNodes = append(allNodes, boundaryHits...)
	allNodes = append(allNodes, backfeedNodes...)
	classCodes, err := nodeClassCodes(ctx, db, allNodes)
	if err != nil {
		return nil, err
	}

	// Critical-facility check: which of the de-energized set is a critical load
	// leaf. A load leaf is a SERVICE_POINT or a PRIMARY METER (Phase 63 --
	// isLoadLeaf / meterCountsAsLoad, the same set the consumption rollup below
	// counts), so a critical PRIMARY meter lands in this list too and stays in
	// step with service.Critical. Critical is DERIVED from the graded
	// load_priority tier (Phase 62) -- rank <= P2 (life-support or essential) --
	// superseding the old is_critical boolean, which resolveServicePriority
	// still honours as a fallback for any row not yet re-graded. A
	// missing/unreadable attribute (e.g. a dangling reference to a since-purged
	// feature) is treated as "not critical" rather than failing the whole
	// isolation over one row -- same reasoning checkBarrier already uses.
	var criticalNodes []node
	for _, n := range deEnergizedNodes {
		code := classCodes[n.classID]
		if !isLoadLeaf(code) {
			continue
		}
		classLoaded, lerr := loader.loadedFor(ctx, db, n.classID)
		if lerr != nil {
			return nil, lerr
		}
		row, gerr := crud.GetFeature(ctx, db, classLoaded, n.featureID)
		if gerr != nil {
			continue
		}
		attrs, _ := row["attributes"].(map[string]any)
		if !meterCountsAsLoad(code, attrs) {
			continue
		}
		if priorityRank[resolveServicePriority(attrs)] <= criticalMaxRank {
			criticalNodes = append(criticalNodes, n)
		}
	}

	service, err := summarizeService(ctx, db, deEnergizedNodes, classCodes, loader)
	if err != nil {
		return nil, err
	}

	resp := &isolationResponse{
		Device:      req.Device,
		DeEnergized: toFeatureRefs(deEnergizedNodes, classCodes),
		Service:     service,
		Truncated:   truncated,
	}
	if len(criticalNodes) > 0 {
		resp.CriticalFacilities = toFeatureRefs(criticalNodes, classCodes)
	}
	if len(backfeedNodes) > 0 {
		resp.PossibleBackfeedPaths = toFeatureRefs(backfeedNodes, classCodes)
	}
	if len(boundaryHits) > 0 {
		// A radial network has at most one boundary node per branch --
		// cross_transformers is forced false above, so BFS halts
		// expansion the moment it finds one, leaving no way to reach a
		// second on the same path. Multiple parallel upstream paths
		// (not a scenario this app's seed data or design models) could
		// in principle populate more than one; picking the first is a
		// deliberate simplification, not a correctness claim about
		// which is truly nearest in that case.
		refs := toFeatureRefs(boundaryHits[:1], classCodes)
		resp.UpstreamBoundary = &refs[0]
	}
	return resp, nil
}
