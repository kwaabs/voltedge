package trace

import (
	"context"
	"sort"

	"github.com/uptrace/bun"
)

// Supply-path trace: from a consumption point (a service point or meter, though
// any feature is accepted) walk UPSTREAM toward the source and report the
// electrical supply chain feeding it -- the LV switching devices in the way,
// the distribution transformer that serves it, and, continuing past the
// transformer, the MV switching/protective devices, the power transformer, and
// the network source. Answers the field question "what feeds this meter, and
// through what device?".
//
// Like the OMS class walks (class_walk.go) this is BARRIER-FREE and topological:
// it ignores live switch state and operational_state, so it always finds the
// serving transformer and source even when a switch on the path happens to be
// open right now. That is the right default for "what is this permanently wired
// to"; a live-energization view is a separate question the power-flow engine
// answers. Composes reachableWithDepth rather than a new BFS mode, same
// reasoning feederTrace/isolate compose the ordinary walks.
//
// What each reached feature IS -- transformer, switching device, source -- is
// read from its class's declared functional_role (migration 0091), not a
// hardcoded class-code list, so a deployment that adds a new switch or
// transformer class gets it in the supply path for free once the class is given
// a role. See memory functional-role-taxonomy.

const (
	// roleTransformer / roleController / roleSource are the functional_role
	// values (migration 0091) the supply-path walk keys off. A class carrying
	// one of these is called out as a milestone; the nearest roleTransformer is
	// the serving transformer, and roleSource (when any class declares it)
	// pins the source.
	roleTransformer = "transformer"
	roleController  = "controller"
	roleSource      = "source"
)

// supplyPathMilestoneRoles are the functional roles a supply-path trace calls
// out as milestones (everything else on the path -- conductors [passthrough],
// busbars [node], sensors -- is still returned in Features but not flagged).
// A map so membership is a direct lookup; overridable by a deployment that
// models a different taxonomy.
var supplyPathMilestoneRoles = map[string]bool{
	roleTransformer: true,
	roleController:  true,
	roleSource:      true,
}

// supplyPathRequest is the POST /trace/supply-path body. Start is the
// consumption point (or any feature) whose upstream supply chain to report.
type supplyPathRequest struct {
	Start featureRef `json:"start"`
}

// supplyMilestone is one significant feature on the supply path, tagged with the
// functional role that made it significant (transformer/controller/source) so a
// client can label and group the chain -- "serving transformer", "switching
// devices you'd operate", "source" -- rather than re-deriving it from class
// codes. Embeds featureRef, so its JSON is the same feature_class/feature_id a
// plain ref carries, plus role.
type supplyMilestone struct {
	featureRef
	Role string `json:"role"`
}

// supplyPathResponse is the whole supply chain feeding Start. Features is the
// full upstream path nearest-first (Start itself included, at index 0), so a
// client reads it top-to-bottom as consumption-point -> ... -> source and can
// highlight the whole route. Milestones is the switching/transformer/source
// subset, also nearest-first, each tagged with its role, for distinct
// highlighting. ServingTransformer is the nearest upstream transformer-role
// instance (nil, with ReachedTransformer=false, when none is reachable -- e.g.
// Start already sits on the MV network above every transformer, or connectivity
// upstream is incomplete). Source is the network source feeding Start; nil when
// Start is itself the head.
type supplyPathResponse struct {
	Start              featureRef        `json:"start"`
	Features           []tracedFeature   `json:"features"`
	Milestones         []supplyMilestone `json:"milestones"`
	ServingTransformer *featureRef       `json:"serving_transformer,omitempty"`
	Source             *featureRef       `json:"source,omitempty"`
	ReachedTransformer bool              `json:"reached_transformer"`
	// Service is the Phase 62 consumption rollup over the path's SERVICE_POINTs.
	// For an upstream supply-path walk that is typically just the start
	// consumption point itself -- so this reads as "what this meter draws, and
	// at what restoration priority" alongside "what feeds it." See ServiceRollup.
	Service *ServiceRollup `json:"service,omitempty"`
}

func supplyPathTrace(ctx context.Context, db bun.IDB, req supplyPathRequest) (*supplyPathResponse, error) {
	if req.Start.FeatureClass == "" || req.Start.FeatureID == "" {
		return nil, invalid("start.feature_class and start.feature_id are required")
	}

	start, err := resolveNode(ctx, db, req.Start)
	if err != nil {
		return nil, err
	}

	// Barrier-free upstream walk, with each reached node's hop distance so the
	// path (and its milestones) come back nearest-first.
	reached, err := reachableWithDepth(ctx, db, start, "UPSTREAM")
	if err != nil {
		return nil, err
	}
	sort.Slice(reached, func(i, j int) bool {
		if reached[i].depth != reached[j].depth {
			return reached[i].depth < reached[j].depth
		}
		return reached[i].n.featureID < reached[j].n.featureID
	})

	orderedNodes := make([]node, len(reached))
	for i, nd := range reached {
		orderedNodes[i] = nd.n
	}
	classCodes, err := nodeClassCodes(ctx, db, orderedNodes)
	if err != nil {
		return nil, err
	}
	roles, err := nodeClassRoles(ctx, db, orderedNodes)
	if err != nil {
		return nil, err
	}

	loader := newFeatureClassLoader()
	features, err := toTracedFeatures(ctx, db, orderedNodes, classCodes, loader)
	if err != nil {
		return nil, err
	}

	service, err := summarizeService(ctx, db, orderedNodes, classCodes, loader)
	if err != nil {
		return nil, err
	}

	resp := &supplyPathResponse{
		Start:    req.Start,
		Features: features,
		Service:  service,
	}

	// One nearest-first pass over the reached nodes: collect the milestone
	// subset (by role), latch the nearest serving transformer, and remember the
	// deepest declared source node (if any class is a source). The start node is
	// never itself a milestone or the serving transformer -- "what feeds it" is
	// strictly upstream of it.
	milestones := make([]supplyMilestone, 0, 8)
	var sourceByRole *nodeDepth
	for i := range reached {
		nd := reached[i]
		if nd.n == start {
			continue
		}
		role := roles[nd.n.classID]
		ref := featureRef{FeatureClass: classCodes[nd.n.classID], FeatureID: nd.n.featureID}
		if supplyPathMilestoneRoles[role] {
			milestones = append(milestones, supplyMilestone{featureRef: ref, Role: role})
		}
		if resp.ServingTransformer == nil && role == roleTransformer {
			t := ref
			resp.ServingTransformer = &t
			resp.ReachedTransformer = true
		}
		if role == roleSource && (sourceByRole == nil || nd.depth > sourceByRole.depth) {
			sourceByRole = &reached[i]
		}
	}

	// Source: prefer a node whose class is declared functional_role='source'
	// (the deepest, for a network fed through several); fall back to the
	// topmost topological head when no class carries the source role -- which is
	// today's case, since the network source is modeled as a slack DIST_XFMR
	// instance, not a dedicated source class (see supply.go's package doc).
	source, err := supplyPathSourceRef(ctx, db, reached, start, classCodes, sourceByRole)
	if err != nil {
		return nil, err
	}
	if source != nil {
		resp.Source = source
		if !containsMilestone(milestones, *source) {
			// Tag with the source node's own class role when it has one (a slack
			// DIST_XFMR reads as 'transformer'); else label it 'source'.
			role := roleForCode(orderedNodes, roles, classCodes, source.FeatureClass)
			if role == "" {
				role = roleSource
			}
			milestones = append(milestones, supplyMilestone{featureRef: *source, Role: role})
		}
	}
	resp.Milestones = milestones

	return resp, nil
}

// supplyPathSourceRef returns the network source feeding start. When a reached
// node's class declares functional_role='source' (sourceByRole, the deepest
// such), that is authoritative. Otherwise it falls back to the deepest reached
// node with no further upstream neighbour of its own (a feeder head) -- the
// topological answer, correct for a radial network whose source is a slack
// transformer instance rather than a dedicated source class. Nil when the only
// head is start itself (running the trace straight from a source).
func supplyPathSourceRef(ctx context.Context, db bun.IDB, reached []nodeDepth, start node, classCodes map[string]string, sourceByRole *nodeDepth) (*featureRef, error) {
	if sourceByRole != nil {
		ref := featureRef{FeatureClass: classCodes[sourceByRole.n.classID], FeatureID: sourceByRole.n.featureID}
		return &ref, nil
	}

	var best *nodeDepth
	for i := range reached {
		nd := reached[i]
		if nd.n == start {
			continue
		}
		neighbors, err := neighborsOf(ctx, db, nd.n, "UPSTREAM")
		if err != nil {
			return nil, err
		}
		if len(neighbors) == 0 && (best == nil || nd.depth > best.depth) {
			best = &reached[i]
		}
	}
	if best == nil {
		return nil, nil
	}
	ref := featureRef{FeatureClass: classCodes[best.n.classID], FeatureID: best.n.featureID}
	return &ref, nil
}

// roleForCode returns the functional_role of the first reached node whose class
// code matches -- used to tag the source milestone with its own class role.
func roleForCode(nodes []node, roles map[string]string, classCodes map[string]string, code string) string {
	for _, n := range nodes {
		if classCodes[n.classID] == code {
			return roles[n.classID]
		}
	}
	return ""
}

func containsMilestone(ms []supplyMilestone, r featureRef) bool {
	for _, m := range ms {
		if m.featureRef == r {
			return true
		}
	}
	return false
}
