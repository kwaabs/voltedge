package trace

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// defaultBoundaryClass is used when a request doesn't name one -- the
// generic MV switch this app's seed data models as a sectionalizing/
// protective device (the successor to the retired DIST_SWITCH).
// Overridable via the request for any network that adds another (e.g.
// a fuse class).
const defaultBoundaryClass = "MV_SWITCH"

// sectionRequest is the POST /trace/section body. BoundaryClass is
// optional, defaulting to defaultBoundaryClass.
type sectionRequest struct {
	TraceConfiguration string     `json:"trace_configuration"`
	Start              featureRef `json:"start"`
	BoundaryClass      string     `json:"boundary_class,omitempty"`
}

// sectionResponse is the whole result: everything between the nearest
// bounding device upstream and the nearest bounding device downstream
// of Start (Start's own section, in utility terms -- what a planned
// outage notification or a "which customers are between these two
// switches" query needs). Either boundary can be absent -- the section
// simply runs to the edge of the modeled network in that direction
// (e.g. no upstream switch exists before an unmodeled source).
type sectionResponse struct {
	Start              featureRef      `json:"start"`
	Features           []tracedFeature `json:"features"`
	UpstreamBoundary   *featureRef     `json:"upstream_boundary,omitempty"`
	DownstreamBoundary *featureRef     `json:"downstream_boundary,omitempty"`
}

// sectionTrace composes two bounded walks -- an UPSTREAM and a
// DOWNSTREAM target_class search from the same start, both stopping at
// (not crossing) the first node of BoundaryClass -- rather than a new
// BFS mode. Their unioned reachable sets are the section; each
// direction's own target hit (if any) is that boundary. Unlike
// isolate's neighbor-first downstream leg (which deliberately starts
// past the device to simulate it open), this walks from Start itself
// in both directions -- a section trace isn't about what a device
// controls, it's about what lies between two devices.
func sectionTrace(ctx context.Context, db bun.IDB, req sectionRequest) (*sectionResponse, error) {
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

	start, err := resolveNode(ctx, db, req.Start)
	if err != nil {
		return nil, err
	}

	// Section boundaries: the nearest bounding switching device on each side.
	// A named boundary_class is honored as exactly that one class; otherwise
	// the section runs to the nearest CONTROLLER (functional_role='controller',
	// migration 0091) -- the whole switching family, not just MV_SWITCH, so a
	// recloser or breaker bounds a section as it should rather than being
	// crossed. Falls back to defaultBoundaryClass when no controller role is
	// stamped yet.
	boundaryClassIDs, err := boundaryClassSet(ctx, db, req.BoundaryClass, defaultBoundaryClass)
	if err != nil {
		return nil, err
	}

	barriers, err := resolveBarriers(ctx, db, tc.ID, tc.StopAtOpenDevices)
	if err != nil {
		return nil, err
	}
	loader := newFeatureClassLoader()

	upstreamVisited, _, upstreamHits, _, _, err := bfs(ctx, db, start, "UPSTREAM", barriers, loader, nil, boundaryClassIDs, false, nil)
	if err != nil {
		return nil, err
	}
	downstreamVisited, _, downstreamHits, _, _, err := bfs(ctx, db, start, "DOWNSTREAM", barriers, loader, nil, boundaryClassIDs, false, nil)
	if err != nil {
		return nil, err
	}

	featureSet := map[node]bool{}
	for _, n := range upstreamVisited {
		featureSet[n] = true
	}
	for _, n := range downstreamVisited {
		featureSet[n] = true
	}
	featureNodes := make([]node, 0, len(featureSet))
	for n := range featureSet {
		featureNodes = append(featureNodes, n)
	}

	allNodes := make([]node, 0, len(featureNodes)+len(upstreamHits)+len(downstreamHits))
	allNodes = append(allNodes, featureNodes...)
	allNodes = append(allNodes, upstreamHits...)
	allNodes = append(allNodes, downstreamHits...)
	classCodes, err := nodeClassCodes(ctx, db, allNodes)
	if err != nil {
		return nil, err
	}

	features, err := toTracedFeatures(ctx, db, featureNodes, classCodes, loader)
	if err != nil {
		return nil, err
	}

	resp := &sectionResponse{
		Start:    req.Start,
		Features: features,
	}
	if len(upstreamHits) > 0 {
		refs := toFeatureRefs(upstreamHits[:1], classCodes)
		resp.UpstreamBoundary = &refs[0]
	}
	if len(downstreamHits) > 0 {
		refs := toFeatureRefs(downstreamHits[:1], classCodes)
		resp.DownstreamBoundary = &refs[0]
	}
	return resp, nil
}
