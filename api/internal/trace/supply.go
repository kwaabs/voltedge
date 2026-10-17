package trace

import (
	"context"
	"database/sql"
	"errors"

	"github.com/uptrace/bun"
)

// Supply-path trace: from any feature, walk UPSTREAM toward the source and
// report the supply chain feeding it, TERMINATING at the head of the feature's
// own network section -- not the distant grid slack bus. The terminus is
// level-dependent, which is the whole point of the trace:
//
//   - An LV feature (trace_tier >= 500) terminates at its DISTRIBUTION
//     TRANSFORMER: the LV network's source is the transformer secondary (see
//     memory transformer-boundary-source). The MV side above it is a different
//     network and a separate question you answer by re-running from the
//     transformer.
//   - An MV feature (and a DIST_XFMR, whose source side is MV) terminates at the
//     FEEDER HEAD: the breaker a circuit designates as its source
//     (gis.circuits.source_feature_*, migration 0062). When no circuit source is
//     designated -- the common case until circuits are assigned -- it falls back
//     to the nearest upstream 'protective' device (boundary_role, migration 0129):
//     the head of the feature's protection section. SourceKind reports which it is.
//
// It is BARRIER-FREE: it ignores live switch state (an OPEN switch on the path is
// flagged by its own device_state, not treated as the end), so it finds the
// permanent wired source even when something on the path is open right now. That
// is "what is this wired to", not "what is energised this instant" -- the latter
// is the Upstream trace / power-flow engine.
//
// Mechanism: one barrier-free bfs UPSTREAM with targetClassIDs = the transformer
// classes and crossTransformers=false, so each branch stops at its first
// transformer -- directly the LV terminus (DIST_XFMR) and the outer bound for an
// MV walk (POWER_XFMR). A section prune over the returned parent tree then tightens
// the MV outer bound to the feeder/section head and drops everything above it. All
// keyed off declared roles (functional_role 0091, boundary_role 0129, trace_tier
// 0126), never class-code lists, so a new class joins the taxonomy once given roles.

const (
	// roleTransformer / roleSource / roleSection are the milestone roles a
	// supply-path trace tags onto the chain. roleSource is the terminus (the
	// section head feeding the start); roleTransformer is a transformer that is
	// not itself the terminus; roleSection is an intermediate protective/switching
	// boundary below the terminus (a section the operator would open to isolate).
	roleTransformer = "transformer"
	roleSource      = "source"
	roleSection     = "section"
	// roleController is the functional_role (migration 0091) of a device whose
	// open/closed state gates flow (switches, breakers). Not used by the supply
	// path itself, but defined here with the other role constants and consumed by
	// the OMS class walks (class_walk.go).
	roleController = "controller"
)

// supplyPathRequest is the POST /trace/supply-path body. Start is the
// consumption point (or any feature) whose upstream supply chain to report.
// RespectSwitchState, when true, makes the walk HALT at open switching devices
// (the energized view) instead of the default barrier-free topological walk: on a
// ring main the normally-open point then bounds the trace to the single live supply
// direction, rather than threading both ways. "What feeds me right now" vs "what am
// I wired to."
// ToPrimarySource, when true, CHAINS across transformers: after reaching the
// start's own section head, if that head is a distribution transformer the trace
// re-runs from it and keeps going up a voltage level at a time, until it reaches a
// non-transformer primary source (the substation feeder breaker) or a grid source.
// Answers "which substation breaker feeds this customer", not just "which
// transformer". Default (false) stops at the local section head.
type supplyPathRequest struct {
	Start              featureRef `json:"start"`
	RespectSwitchState bool       `json:"respect_switch_state,omitempty"`
	ToPrimarySource    bool       `json:"to_primary_source,omitempty"`
}

// supplyMilestone is one significant feature on the supply path, tagged with the
// role that made it significant (transformer/section/source) so a client can
// label and group the chain -- "serving transformer", "section boundaries you'd
// operate", "source" -- rather than re-deriving it from class codes. Embeds
// featureRef, so its JSON is the same feature_class/feature_id a plain ref carries,
// plus role.
type supplyMilestone struct {
	featureRef
	Role string `json:"role"`
}

// supplyPathResponse is the supply chain feeding Start, up to the head of its own
// section. Features is the path nearest-first (Start at index 0), so a client reads
// it consumption-point -> ... -> source. Milestones is the transformer/section/source
// subset, nearest-first, each role-tagged. ServingTransformer is the nearest upstream
// transformer-role instance (set for an LV start, whose terminus IS its transformer;
// nil for an MV start, whose terminus is a breaker). Source is the reported section
// head; SourceKind says what kind it is.
type supplyPathResponse struct {
	Start              featureRef        `json:"start"`
	Features           []tracedFeature   `json:"features"`
	Milestones         []supplyMilestone `json:"milestones"`
	ServingTransformer *featureRef       `json:"serving_transformer,omitempty"`
	Source             *featureRef       `json:"source,omitempty"`
	ReachedTransformer bool              `json:"reached_transformer"`
	// SourceKind names what the reported Source IS, so a client can label it
	// honestly: "serving_transformer" (LV -> its DIST_XFMR), "feeder_head" (MV ->
	// the breaker a circuit designates as its source), "section_head" (MV, no
	// circuit source designated -> nearest protective device; assign the circuit a
	// source to resolve the true feeder head), or "none" (no source reachable --
	// incomplete upstream connectivity).
	SourceKind string `json:"source_kind,omitempty"`
	// RespectSwitchState echoes the request flag: true = the energized view (the walk
	// halted at open switches, so only the live supply direction is reported), false
	// = the full barrier-free wired view.
	RespectSwitchState bool `json:"respect_switch_state,omitempty"`
	// ToPrimarySource echoes the request flag: true = the chain crossed transformers
	// to the primary (substation) source, false = stopped at the local section head.
	ToPrimarySource bool `json:"to_primary_source,omitempty"`
	// Service is the Phase 62 consumption rollup over the path's SERVICE_POINTs.
	// For an upstream supply-path walk that is typically just the start consumption
	// point itself -- "what this meter draws, and at what restoration priority"
	// alongside "what feeds it." See ServiceRollup.
	Service *ServiceRollup `json:"service,omitempty"`
	// Truncated is true when the upstream walk hit the maxTraceHops cap -- the
	// supply path is incomplete and the client should warn.
	Truncated bool `json:"truncated"`
}

func supplyPathTrace(ctx context.Context, db bun.IDB, req supplyPathRequest) (*supplyPathResponse, error) {
	if req.Start.FeatureClass == "" || req.Start.FeatureID == "" {
		return nil, invalid("start.feature_class and start.feature_id are required")
	}

	start, err := resolveNode(ctx, db, req.Start)
	if err != nil {
		return nil, err
	}

	loader := newFeatureClassLoader()

	// Phase 58 spatial gate: refuse an out-of-scope start and disclose only
	// in-scope features, exactly as runTrace does (see scope.go). A no-op unless
	// enforcement is on and the caller is scoped.
	scope, err := enforceScopeStart(ctx, db, loader, start, req.Start)
	if err != nil {
		return nil, err
	}

	transformerIDs, err := loader.transformerRoles(ctx, db)
	if err != nil {
		return nil, err
	}
	tiers, err := loader.tiers(ctx, db)
	if err != nil {
		return nil, err
	}
	boundaryByID, err := loader.boundaryRoles(ctx, db)
	if err != nil {
		return nil, err
	}

	// Barriers: the default is a barrier-free walk (nil) -- an OPEN switch never hides
	// the wired supply (it is flagged by its own device_state). When the caller asks
	// for the energized view (RespectSwitchState), halt at open switching devices
	// exactly as the downstream energization walk does, so a ring's normally-open
	// point bounds the trace to the single live direction.
	var barriers map[string][]barrierCheck
	if req.RespectSwitchState {
		barriers, err = resolveOpenDeviceBarriers(ctx, db)
		if err != nil {
			return nil, err
		}
	}

	// Chain of single-path levels. Default is one level (stop at the start's own
	// section head). ToPrimarySource keeps going: when a level ends at a transformer,
	// re-run from it a voltage level up, until a non-transformer primary source (the
	// substation feeder breaker) or a grid source -- bounded by maxSupplyLevels so a
	// cycle can't loop forever. Each level returns the single nearest-first path to
	// its one chosen source; levels join at the shared transformer node.
	maxSupplyLevels := 1
	if req.ToPrimarySource {
		maxSupplyLevels = 6
	}

	chain := []node{start}
	inChain := map[node]bool{start: true}
	levelStart := start
	var finalSource *node
	sourceKind := "none"
	truncated := false

	for level := 0; level < maxSupplyLevels; level++ {
		pathNodes, srcNode, kind, trunc, werr := supplyWalkSinglePath(ctx, db, loader, levelStart, transformerIDs, boundaryByID, tiers, barriers)
		if werr != nil {
			return nil, werr
		}
		truncated = truncated || trunc
		for _, n := range pathNodes {
			if inChain[n] {
				continue
			}
			chain = append(chain, n)
			inChain[n] = true
		}
		if srcNode == nil {
			break
		}
		finalSource = srcNode
		sourceKind = kind
		// Continue toward the primary source only when asked AND this level ended at a
		// transformer we can cross; a breaker/grid source is the primary -- stop here.
		if !req.ToPrimarySource || !transformerIDs[srcNode.classID] {
			break
		}
		levelStart = *srcNode
	}

	// The chain is already nearest-first (each level nearest-first, appended in order),
	// so depth = position in the chain.
	reached := make([]nodeDepth, 0, len(chain))
	for i, n := range chain {
		reached = append(reached, nodeDepth{n: n, depth: i})
	}

	// Phase 58: filter the reached set to in-scope nodes BEFORE anything is derived
	// from it, so no feature outside the user's territories is ever surfaced -- and
	// an out-of-scope hop can't become the reported source.
	if scope.apply {
		filtered := reached[:0]
		for _, nd := range reached {
			ok, perr := scope.permits(ctx, db, nd.n, loader)
			if perr != nil {
				return nil, perr
			}
			if ok {
				filtered = append(filtered, nd)
			}
		}
		reached = filtered
	}

	orderedNodes := make([]node, len(reached))
	for i, nd := range reached {
		orderedNodes[i] = nd.n
	}
	classCodes, err := nodeClassCodes(ctx, db, orderedNodes)
	if err != nil {
		return nil, err
	}

	features, err := toTracedFeatures(ctx, db, orderedNodes, classCodes, loader)
	if err != nil {
		return nil, err
	}
	// Parent pointers (rooted at Start) so the supply path nests in the Tree view;
	// the upstream walk returns a bare feature set with no native parent map.
	if err := stampParentsFromStart(ctx, db, features, orderedNodes, start, classCodes); err != nil {
		return nil, err
	}

	service, err := summarizeService(ctx, db, orderedNodes, classCodes, loader, true)
	if err != nil {
		return nil, err
	}

	resp := &supplyPathResponse{
		Start:              req.Start,
		Features:           features,
		Service:            service,
		Truncated:          truncated,
		SourceKind:         sourceKind,
		RespectSwitchState: req.RespectSwitchState,
		ToPrimarySource:    req.ToPrimarySource,
	}

	// One nearest-first pass over the pruned set: tag milestones (terminus->source,
	// other transformers->transformer, protective/switching below the terminus->
	// section), and latch the nearest Source and ServingTransformer. The start is
	// never itself a milestone -- "what feeds it" is strictly upstream of it.
	milestones := make([]supplyMilestone, 0, 8)
	for i := range reached {
		nd := reached[i]
		if nd.n == start {
			continue
		}
		ref := featureRef{FeatureClass: classCodes[nd.n.classID], FeatureID: nd.n.featureID}
		switch {
		case finalSource != nil && nd.n == *finalSource:
			milestones = append(milestones, supplyMilestone{featureRef: ref, Role: roleSource})
			if resp.Source == nil {
				s := ref
				resp.Source = &s
			}
			if transformerIDs[nd.n.classID] && resp.ServingTransformer == nil {
				t := ref
				resp.ServingTransformer = &t
				resp.ReachedTransformer = true
			}
		case transformerIDs[nd.n.classID]:
			milestones = append(milestones, supplyMilestone{featureRef: ref, Role: roleTransformer})
			if resp.ServingTransformer == nil {
				t := ref
				resp.ServingTransformer = &t
				resp.ReachedTransformer = true
			}
		case boundaryByID[nd.n.classID] == "protective" || boundaryByID[nd.n.classID] == "switching":
			milestones = append(milestones, supplyMilestone{featureRef: ref, Role: roleSection})
		}
	}

	// No terminus reached (incomplete upstream connectivity): fall back to the
	// topological head so the trace still names a source rather than nothing.
	if resp.Source == nil {
		source, serr := supplyPathSourceRef(ctx, db, reached, start, classCodes)
		if serr != nil {
			return nil, serr
		}
		if source != nil {
			resp.Source = source
			if !containsMilestone(milestones, *source) {
				milestones = append(milestones, supplyMilestone{featureRef: *source, Role: roleSource})
			}
		}
	}

	resp.Milestones = milestones
	return resp, nil
}

// supplyWalkSinglePath runs ONE level of the supply path: a barrier-free (or, with
// barriers, energized) UPSTREAM walk from start that stops at the start's level
// source boundary (LV -> its transformer; MV / a DIST_XFMR -> the circuit source,
// else the nearest protective section head), then returns the SINGLE nearest-first
// path from start to the one chosen source (goal-directed -- every sibling /
// other-direction branch the walk fanned into is dropped). The returned *node is the
// chosen source; it is nil (and the whole reached set is returned as the path) when
// no source boundary was reachable, so the caller's topological-head fallback can
// still name one. Shared by every level of a to-primary-source chain.
func supplyWalkSinglePath(ctx context.Context, db bun.IDB, loader *featureClassLoader, start node, transformerIDs map[string]bool, boundaryByID map[string]string, tiers map[string]int, barriers map[string][]barrierCheck) ([]node, *node, string, bool, error) {
	startTier, hasTier := tiers[start.classID]
	isLV := hasTier && startTier >= 500
	isMV := hasTier && (startTier == 300 || startTier == 400)

	// Stop set: an LV walk stops only at a transformer (its DIST_XFMR), passing
	// through any LV fuse. An MV walk ALSO stops at the nearest 'protective' device
	// (the section/feeder head) so it doesn't fan across the whole MV network out to
	// the POWER_XFMR (the transformers stay in the set as an outer backstop).
	walkTargets := transformerIDs
	if isMV {
		walkTargets = make(map[string]bool, len(transformerIDs)+8)
		for id := range transformerIDs {
			walkTargets[id] = true
		}
		for id, role := range boundaryByID {
			if role == "protective" {
				walkTargets[id] = true
			}
		}
	}

	// crossTransformers=false so each branch stops at its first stop-set class. bfs
	// gives junction pass-through + the tier guard.
	order, _, _, _, parent, _, truncated, err := bfs(ctx, db, start, "UPSTREAM", barriers, loader, nil, walkTargets, false, false, nil)
	if err != nil {
		return nil, nil, "", false, err
	}

	reachedSet := make(map[node]bool, len(order))
	for _, n := range order {
		reachedSet[n] = true
	}

	// Classify the reached stop candidates (excluding start).
	var reachedTransformers, reachedProtective []node
	for _, n := range order {
		if n == start {
			continue
		}
		if transformerIDs[n.classID] {
			reachedTransformers = append(reachedTransformers, n)
		}
		if boundaryByID[n.classID] == "protective" {
			reachedProtective = append(reachedProtective, n)
		}
	}

	// Terminus set: the head of the start's own section.
	terminus := make(map[node]bool)
	sourceKind := "none"
	switch {
	case isLV:
		for _, n := range reachedTransformers {
			terminus[n] = true
		}
		if len(terminus) > 0 {
			sourceKind = "serving_transformer"
		}
	case isMV:
		src, ok, serr := circuitSourceNode(ctx, db, start, loader, reachedSet)
		if serr != nil {
			return nil, nil, "", false, serr
		}
		switch {
		case ok:
			terminus[src] = true
			sourceKind = "feeder_head"
		case len(reachedProtective) > 0:
			for _, n := range reachedProtective {
				terminus[n] = true
			}
			sourceKind = "section_head"
		default:
			for _, n := range reachedTransformers {
				terminus[n] = true
			}
			if len(terminus) > 0 {
				sourceKind = "serving_transformer"
			}
		}
	default:
		for _, n := range reachedTransformers {
			terminus[n] = true
		}
		if len(terminus) > 0 {
			sourceKind = "serving_transformer"
		}
	}

	depthOf := func(n node) int {
		d := 0
		for n != start {
			p, ok := parent[n]
			if !ok {
				break
			}
			n = p
			d++
		}
		return d
	}

	// Goal-directed: pick a SINGLE source -- the circuit source if designated, else
	// the nearest (bfs discovers nearest-first, tie-broken deterministically).
	var chosen *node
	chosenDepth := 0
	for n := range terminus {
		d := depthOf(n)
		if chosen == nil || d < chosenDepth || (d == chosenDepth && n.featureID < chosen.featureID) {
			c := n
			chosen = &c
			chosenDepth = d
		}
	}

	if chosen == nil {
		// No source reached: hand back the whole reached set so the caller's
		// topological-head fallback can still name something.
		return order, nil, "none", truncated, nil
	}

	// The single path = the chosen source and its ancestors back to start, reversed to
	// nearest-first (start ... chosen).
	rev := make([]node, 0, chosenDepth+1)
	for n := *chosen; ; {
		rev = append(rev, n)
		if n == start {
			break
		}
		p, ok := parent[n]
		if !ok {
			break
		}
		n = p
	}
	pathNodes := make([]node, len(rev))
	for i, n := range rev {
		pathNodes[len(rev)-1-i] = n
	}
	return pathNodes, chosen, sourceKind, truncated, nil
}

// circuitSourceRow reads just the designated source feature off a gis.circuits
// row (migration 0062). Local to the supply-path trace -- the circuit package
// imports trace (assign-by-trace), so trace can't import it back; this reads the
// two columns directly instead.
type circuitSourceRow struct {
	bun.BaseModel `bun:"table:gis.circuits"`

	SourceClass *string `bun:"source_feature_class"`
	SourceID    *string `bun:"source_feature_id"`
}

// circuitSourceNode resolves the feeder head a circuit designates as its source
// (gis.circuits.source_feature_*, migration 0062) for the start's stored circuit_id,
// if that feature was actually reached by the upstream walk. ok=false (no error)
// when the start carries no circuit, the circuit has no source designated, the
// source feature can't be resolved (purged / unknown class), or it wasn't on the
// upstream path -- each a clean "fall back to the nearest protective section head",
// not a trace failure.
func circuitSourceNode(ctx context.Context, db bun.IDB, start node, loader *featureClassLoader, reached map[node]bool) (node, bool, error) {
	cid, err := nodeCircuitID(ctx, db, start, loader)
	if err != nil {
		return node{}, false, err
	}
	if cid == "" {
		return node{}, false, nil
	}
	var row circuitSourceRow
	err = db.NewSelect().
		Model(&row).
		Column("source_feature_class", "source_feature_id").
		Where("id = ?", cid).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return node{}, false, nil
		}
		return node{}, false, err
	}
	if row.SourceClass == nil || row.SourceID == nil {
		return node{}, false, nil
	}
	srcNode, err := resolveNode(ctx, db, featureRef{FeatureClass: *row.SourceClass, FeatureID: *row.SourceID})
	if err != nil {
		// A source feature of an unknown/purged class is not a trace failure --
		// fall back to the section head.
		return node{}, false, nil
	}
	if !reached[srcNode] {
		return node{}, false, nil
	}
	return srcNode, true, nil
}

// supplyPathSourceRef returns the topological head of the reached set -- the
// deepest reached node with no further upstream neighbour of its own (a feeder
// head). Used only as the last-resort Source when no section terminus was reached
// (incomplete upstream connectivity). Nil when the only head is start itself.
func supplyPathSourceRef(ctx context.Context, db bun.IDB, reached []nodeDepth, start node, classCodes map[string]string) (*featureRef, error) {
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

func containsMilestone(ms []supplyMilestone, r featureRef) bool {
	for _, m := range ms {
		if m.featureRef == r {
			return true
		}
	}
	return false
}
