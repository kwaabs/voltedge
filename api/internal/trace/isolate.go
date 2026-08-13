package trace

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

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
type isolationResponse struct {
	Device           featureRef   `json:"device"`
	DeEnergized      []featureRef `json:"de_energized"`
	UpstreamBoundary *featureRef  `json:"upstream_boundary,omitempty"`
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

	downstreamNeighbors, err := neighborsOf(ctx, db, device, "DOWNSTREAM")
	if err != nil {
		return nil, err
	}
	deEnergized := map[node]bool{}
	for _, n := range downstreamNeighbors {
		visited, _, _, _, _, err := bfs(ctx, db, n, "DOWNSTREAM", barriers, loader, nil, nil, false)
		if err != nil {
			return nil, err
		}
		for _, v := range visited {
			deEnergized[v] = true
		}
	}
	deEnergizedNodes := make([]node, 0, len(deEnergized))
	for n := range deEnergized {
		deEnergizedNodes = append(deEnergizedNodes, n)
	}

	boundaryClassCode := req.BoundaryClass
	if boundaryClassCode == "" {
		boundaryClassCode = req.Device.FeatureClass
	}
	boundaryLoaded, err := metadata.Load(ctx, db, boundaryClassCode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("boundary feature class %q does not exist", boundaryClassCode)
		}
		return nil, err
	}
	boundaryClassID := boundaryLoaded.FeatureClass.ID

	_, _, boundaryHits, _, _, err := bfs(ctx, db, device, "UPSTREAM", map[string]barrierCheck{}, loader, nil, &boundaryClassID, false)
	if err != nil {
		return nil, err
	}

	allNodes := make([]node, 0, len(deEnergizedNodes)+len(boundaryHits))
	allNodes = append(allNodes, deEnergizedNodes...)
	allNodes = append(allNodes, boundaryHits...)
	classCodes, err := nodeClassCodes(ctx, db, allNodes)
	if err != nil {
		return nil, err
	}

	resp := &isolationResponse{
		Device:      req.Device,
		DeEnergized: toFeatureRefs(deEnergizedNodes, classCodes),
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
