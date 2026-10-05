package trace

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// feederRequest is the POST /trace/feeder body. TraceConfiguration
// must name a DOWNSTREAM-direction row -- its barrier settings govern
// the downstream leg below, same reasoning isolationRequest's own
// TraceConfiguration field uses.
type feederRequest struct {
	TraceConfiguration string     `json:"trace_configuration"`
	Start              featureRef `json:"start"`
}

// feederResponse is the whole result of one feeder-trace run. Heads is
// almost always exactly one node in a real radial distribution network
// (a substation source), but reported as a list rather than assuming
// that -- a meshed or multi-source upstream (not a scenario this app's
// seed data models, but not one the engine should silently mishandle
// either) would surface more than one. Features is every node the
// whole feeder reaches downstream of every head, deduplicated.
type feederResponse struct {
	Start    featureRef      `json:"start"`
	Heads    []featureRef    `json:"heads"`
	Features []tracedFeature `json:"features"`
	// Service is the Phase 62 consumption rollup of the whole feeder's
	// SERVICE_POINTs -- the load + customers, by restoration tier, the feeder
	// carries. See ServiceRollup.
	Service *ServiceRollup `json:"service,omitempty"`
	// Truncated is true when any of the per-head downstream walks hit the
	// maxTraceHops cap -- the feeder is incomplete and the client should warn.
	Truncated bool `json:"truncated"`
}

// feederTrace answers "which whole feeder does this feature belong
// to," regardless of which point along it you start from -- composing
// two ordinary walks rather than a new BFS mode of its own, same
// reasoning isolate does:
//
//  1. Find the feeder's head(s): an UPSTREAM walk from Start with no
//     barriers and no target_class, to full exhaustion. A feeder's
//     identity is a topological property, not a live-switching-state
//     one -- deliberately ignoring barriers here matches
//     CONNECTED_TRACE's own stop_at_open_devices=false reasoning ("an
//     audit of what's wired to what shouldn't silently stop at a
//     switch just because it happens to be open right now"). The
//     head(s) are whichever reached nodes have no further upstream
//     neighbors of their own -- the network's root(s) on that branch.
//  2. Union a DOWNSTREAM trace (respecting TraceConfiguration's real
//     barrier settings this time -- the *footprint* returned should
//     reflect live switching state) from each head.
func feederTrace(ctx context.Context, db bun.IDB, req feederRequest) (*feederResponse, error) {
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
	if direction != "DOWNSTREAM" {
		return nil, invalid("feeder trace requires a DOWNSTREAM trace_configuration, %q is %s", req.TraceConfiguration, direction)
	}

	start, err := resolveNode(ctx, db, req.Start)
	if err != nil {
		return nil, err
	}

	loader := newFeatureClassLoader()

	upstreamVisited, _, _, _, _, _, truncated, err := bfs(ctx, db, start, "UPSTREAM", map[string][]barrierCheck{}, loader, nil, nil, false, false, nil)
	if err != nil {
		return nil, err
	}

	var heads []node
	for _, n := range upstreamVisited {
		neighbors, nerr := neighborsOf(ctx, db, n, "UPSTREAM")
		if nerr != nil {
			return nil, nerr
		}
		if len(neighbors) == 0 {
			heads = append(heads, n)
		}
	}
	if len(heads) == 0 {
		// Start itself has no further upstream neighbors -- it is its
		// own feeder head (e.g. running this straight from the
		// substation source).
		heads = []node{start}
	}

	barriers, err := resolveBarriers(ctx, db, tc.ID, tc.StopAtOpenDevices)
	if err != nil {
		return nil, err
	}

	featureSet := map[node]bool{}
	for _, head := range heads {
		visited, _, _, _, _, _, headTruncated, derr := bfs(ctx, db, head, "DOWNSTREAM", barriers, loader, nil, nil, false, false, nil)
		if derr != nil {
			return nil, derr
		}
		truncated = truncated || headTruncated
		for _, v := range visited {
			featureSet[v] = true
		}
	}
	featureNodes := make([]node, 0, len(featureSet))
	for n := range featureSet {
		featureNodes = append(featureNodes, n)
	}

	allNodes := make([]node, 0, len(heads)+len(featureNodes))
	allNodes = append(allNodes, heads...)
	allNodes = append(allNodes, featureNodes...)
	classCodes, err := nodeClassCodes(ctx, db, allNodes)
	if err != nil {
		return nil, err
	}

	features, err := toTracedFeatures(ctx, db, featureNodes, classCodes, loader)
	if err != nil {
		return nil, err
	}
	// Give the feeder result parent pointers (rooted at Start) so it nests in the
	// client's Tree view; a feeder is a union of downstream walks with no native
	// parent map. See stampParentsFromStart.
	if err := stampParentsFromStart(ctx, db, features, featureNodes, start, classCodes); err != nil {
		return nil, err
	}

	service, err := summarizeService(ctx, db, featureNodes, classCodes, loader)
	if err != nil {
		return nil, err
	}

	return &feederResponse{
		Start:     req.Start,
		Heads:     toFeatureRefs(heads, classCodes),
		Features:  features,
		Service:   service,
		Truncated: truncated,
	}, nil
}
