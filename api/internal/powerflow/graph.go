package powerflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"

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
	// ToTerminalID is the terminal on the "to" side of the edge -- normally
	// unused (a downstream from->to step reads FromTerminalID), but the junction
	// pass-through's undirected step crosses an edge in reverse (arriving at the
	// "from" node from the "to" side), and then the "to" side is the sending
	// terminal whose winding role a transformer child must resolve from.
	ToTerminalID *string `bun:"to_terminal_id"`
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

	// Phase 58 START gate: a scoped user may not start (or target) an analysis on
	// a feature outside their territories. resolveNode is the single choke point
	// for every USER-SUPPLIED feature reference (start/source/fault/tie/node) --
	// the walk's own discovered nodes go through downstreamNeighbors, not here --
	// so gating it here covers all of them at once, reusing the row just read (no
	// extra query). Results are deliberately NOT filtered; see scope.go.
	if err := enforceScope(ctx, db, loaded, row, ref); err != nil {
		return node{}, err
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
	// junctionByID is the set of feature_class ids whose network_role is
	// 'junction' (a busbar, a switchgear panel bus) -- an electrical common node
	// with no inherent direction. buildTree passes THROUGH one undirected so a
	// downstream sweep reaching a one-way-sink bus doesn't dead-end on it (see
	// buildTree's junction pass-through note). nil until junctions() first loads
	// it, then memoized on the loader the whole analysis shares -- this package's
	// own copy of api/internal/trace's junctionByID, same lazy shape.
	junctionByID map[string]bool
	// tierByID maps a feature_class id to its trace_tier (migration 0126) -- its
	// rank in the source-to-load hierarchy (0 = grid infeed, higher = closer to
	// the load). ONLY tiered classes appear here; an untiered (NULL) class is
	// absent, so a step to or from it is never guarded (presence tested comma-ok,
	// since tier 0 is a real rank). It stops the junction pass-through's undirected
	// expansion from leaking back toward the source. This package's own copy of
	// api/internal/trace's tierByID, loaded once by tiers().
	tierByID map[string]int
	// specByDomainCode maps "<domain_id>\x00<code>" to the material spec that
	// domain value links (metadata.domain_values.material_spec_id), loaded ONCE for
	// the whole global catalogue the first time any conductor needs it -- so an
	// analysis pays two queries for every material spec instead of two PER conductor
	// (resolveConductorImpedance previously resolved the spec per edge, the dominant
	// term in the per-node N+1 the concurrency harness measured). nil until loaded.
	specByDomainCode map[string]*metadata.MaterialSpec
}

func newFeatureClassLoader() *featureClassLoader {
	return &featureClassLoader{codeByID: map[string]string{}, loadedByID: map[string]*metadata.Loaded{}}
}

// junctions returns the set of feature_class ids carrying network_role
// 'junction' (busbar, switchgear bus). A junction is an electrical common node:
// every edge on it meets at one point with no inherent from/to direction, which
// is why buildTree must pass THROUGH it undirected rather than dead-ending on
// the arbitrary orientation an assembly fan-out authored its ways with (an RMU
// wires every way switch->bus, so the bus is only ever a "to"). This package's
// own copy of api/internal/trace's junctions(): loaded once, memoized on the
// shared loader, so a run pays exactly one query however many analyses reuse it.
func (l *featureClassLoader) junctions(ctx context.Context, db bun.IDB) (map[string]bool, error) {
	if l.junctionByID != nil {
		return l.junctionByID, nil
	}
	var fcs []metadata.FeatureClass
	if err := db.NewSelect().Model(&fcs).Column("id").Where("network_role = ?", "junction").Scan(ctx); err != nil {
		return nil, fmt.Errorf("powerflow: loading junction feature classes: %w", err)
	}
	out := make(map[string]bool, len(fcs))
	for _, fc := range fcs {
		out[fc.ID] = true
	}
	l.junctionByID = out
	return out, nil
}

// tiers returns the map of feature_class id -> trace_tier (migration 0126) for
// every class that carries one; untiered (NULL) classes are absent. The tier
// lets buildTree enforce direction by what a feature IS, not only by the from/to
// orientation its connections were authored with: a downstream step may go to an
// equal-or-higher tier but never a strictly-lower one (which would be walking
// back toward the source) -- the guard that keeps the junction pass-through's
// undirected expansion from leaking up the feeder that supplies the bus. A step
// touching an untiered class is always allowed, so the guard is transparent
// until tiers are seeded (seed/trace_tiers.sql). This package's own copy of
// api/internal/trace's tiers(): loaded once, memoized on the shared loader.
func (l *featureClassLoader) tiers(ctx context.Context, db bun.IDB) (map[string]int, error) {
	if l.tierByID != nil {
		return l.tierByID, nil
	}
	var fcs []metadata.FeatureClass
	if err := db.NewSelect().Model(&fcs).Column("id", "trace_tier").Where("trace_tier IS NOT NULL").Scan(ctx); err != nil {
		return nil, fmt.Errorf("powerflow: loading feature class tiers: %w", err)
	}
	out := make(map[string]int, len(fcs))
	for _, fc := range fcs {
		if fc.TraceTier != nil {
			out[fc.ID] = *fc.TraceTier
		}
	}
	l.tierByID = out
	return out, nil
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

// materialSpec resolves the material spec a (domain_id, code) links, from a
// run-wide cache loaded once. It returns the SAME result as resolveMaterialSpecByCode
// -- the spec the domain value's material_spec_id points to, or nil when the code is
// unknown or carries no spec -- but reading the whole metadata.domain_values ->
// material_specs catalogue in bulk the first time, so N conductors cost two queries
// for the global set rather than two queries each. The catalogue is small and
// immutable for the life of an analysis, so caching it is behaviour-preserving.
func (l *featureClassLoader) materialSpec(ctx context.Context, db bun.IDB, domainID, code string) (*metadata.MaterialSpec, error) {
	if l.specByDomainCode == nil {
		if err := l.loadMaterialSpecs(ctx, db); err != nil {
			return nil, err
		}
	}
	return l.specByDomainCode[domainID+"\x00"+code], nil
}

// loadMaterialSpecs populates specByDomainCode: every domain value that links a
// material spec, joined to the spec in memory. Two queries total (the linked domain
// values, the spec catalogue), run once.
func (l *featureClassLoader) loadMaterialSpecs(ctx context.Context, db bun.IDB) error {
	var dvs []metadata.DomainValue
	if err := db.NewSelect().Model(&dvs).Where("material_spec_id IS NOT NULL").Scan(ctx); err != nil {
		return fmt.Errorf("powerflow: loading material-spec domain values: %w", err)
	}
	specs, err := metadata.ListMaterialSpecs(ctx, db)
	if err != nil {
		return fmt.Errorf("powerflow: loading material specs: %w", err)
	}
	byID := make(map[string]*metadata.MaterialSpec, len(specs))
	for i := range specs {
		byID[specs[i].ID] = &specs[i]
	}
	m := make(map[string]*metadata.MaterialSpec, len(dvs))
	for _, dv := range dvs {
		if dv.MaterialSpecID == nil {
			continue
		}
		if spec, ok := byID[*dv.MaterialSpecID]; ok {
			m[dv.DomainID+"\x00"+dv.Code] = spec
		}
	}
	l.specByDomainCode = m
	return nil
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
		// Skip edges to a soft-deleted feature so powerflow never traverses one (see
		// trace.neighborsOf for the rationale and the shared feature_registry
		// liveness definition).
		Where("EXISTS (SELECT 1 FROM runtime.feature_registry r WHERE r.feature_id = feature_connectivity.from_feature_id AND r.deleted_at IS NULL)").
		Where("EXISTS (SELECT 1 FROM runtime.feature_registry r WHERE r.feature_id = feature_connectivity.to_feature_id AND r.deleted_at IS NULL)").
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

// incidentNeighbors returns every node incident to n on ANY edge, in either
// stored orientation -- the junction pass-through's expansion (buildTree) uses
// it in place of downstreamNeighbors once a walk has reached a busbar, so the
// sweep can leave a one-way-sink bus that the fan-out authored every way INTO.
// Each neighbor comes back with the terminal on n's (the sending) side of that
// edge: FromTerminalID for a forward edge (n is the "from"), ToTerminalID for a
// reverse one (n is the "to") -- so a transformer reached this way still resolves
// its child windings correctly. Distinct from nodal.go's undirectedNeighbors,
// which feeds the Y-bus solver and carries no terminal.
func incidentNeighbors(ctx context.Context, db bun.IDB, n node) ([]downstreamEdge, error) {
	var rows []edgeRow
	if err := db.NewSelect().Model(&rows).
		Where("(from_feature_id = ? AND from_feature_class_id = ?) OR (to_feature_id = ? AND to_feature_class_id = ?)",
			n.featureID, n.classID, n.featureID, n.classID).
		// Skip edges to a soft-deleted feature so powerflow never traverses one (see
		// trace.neighborsOf for the rationale and the shared feature_registry
		// liveness definition).
		Where("EXISTS (SELECT 1 FROM runtime.feature_registry r WHERE r.feature_id = feature_connectivity.from_feature_id AND r.deleted_at IS NULL)").
		Where("EXISTS (SELECT 1 FROM runtime.feature_registry r WHERE r.feature_id = feature_connectivity.to_feature_id AND r.deleted_at IS NULL)").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("powerflow: loading incident neighbors: %w", err)
	}
	out := make([]downstreamEdge, 0, len(rows))
	for _, r := range rows {
		from := node{classID: r.FromFeatureClassID, featureID: r.FromFeatureID}
		if from == n {
			// Forward edge: the far side is "to", n is the sending "from".
			terminalID := ""
			if r.FromTerminalID != nil {
				terminalID = *r.FromTerminalID
			}
			out = append(out, downstreamEdge{
				to:             node{classID: r.ToFeatureClassID, featureID: r.ToFeatureID},
				fromTerminalID: terminalID,
			})
			continue
		}
		// Reverse edge: n is the "to"; the far side is the "from", and n's own
		// sending terminal is this edge's to_terminal_id.
		terminalID := ""
		if r.ToTerminalID != nil {
			terminalID = *r.ToTerminalID
		}
		out = append(out, downstreamEdge{
			to:             from,
			fromTerminalID: terminalID,
		})
	}
	return out, nil
}

// maxSweepNodes bounds how many nodes one load-flow tree will ever hold -- a
// defensive valve against a pathological data bug (a malformed connectivity
// cycle a barrier should have stopped), NOT a real design limit. It mirrors
// api/internal/trace's maxTraceHops, with one deliberate difference in how a
// trip is HANDLED: a trace of an over-large feeder returns a still-useful
// partial topology and just flags it `truncated`, but a load-flow / fault /
// coordination result over a truncated network is numerically WRONG, not merely
// partial -- the backward sweep would accumulate only the demand of the nodes it
// kept, understating load and overstating voltage -- so buildTree FAILS LOUDLY
// when it trips this (see buildTree), exactly as it already does for a non-radial
// network, rather than hand back a plausible-but-wrong study. The old value
// (2000) was both silent AND too low for a real distribution feeder; the default
// is raised to match trace and is overridable with POWERFLOW_MAX_NODES. Read once
// at package init.
var maxSweepNodes = maxSweepNodesFromEnv()

// maxSweepNodesFromEnv reads POWERFLOW_MAX_NODES (a positive integer) or returns
// the default. Read once at init -- an analysis never re-reads it.
func maxSweepNodesFromEnv() int {
	if v := os.Getenv("POWERFLOW_MAX_NODES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 50000
}

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
//
// Junction pass-through: a busbar / switchgear bus is an electrical common node
// -- every way meets at one point with no inherent from/to direction, yet an
// assembly fan-out authors all its connections in one fixed orientation (an RMU
// wires every way switch->bus, so the bus is only ever a "to"). A strict from->to
// downstream sweep therefore REACHES such a one-way-sink bus fine but then dead-
// ends on it, silently truncating every sibling way and its own downstream out of
// the study -- exactly the gap api/internal/trace's bfs closed for traces. This
// mirrors that fix's DOWNSTREAM half: when the sweep reaches a network_role=
// 'junction' node it expands that node UNDIRECTED (every incident edge, either
// orientation) and every node discovered from there is "unlocked" -- it too
// expands undirected, so the walk continues out through the sibling ways'
// switches and cables the fan-out authored bus-inward. Two guards keep this from
// walking back toward the source: (a) an undirected expansion never follows the
// edge back to the node it was reached from (that reversed edge isn't a real
// second path, so it must not trip mesh detection), and (b) the cross-layer tier
// guard (migration 0126) refuses any step to a strictly-lower trace_tier. A third
// safeguard collapses the same neighbour appearing twice in ONE node's expansion
// (a single adjacency stored on two connectivity rows -- a way switch wired to its
// bus through two terminals, or an edge kept in both orientations): that is one
// electrical connection seen twice, not a loop, so the duplicate must not trip the
// mesh check. A genuine reconvergence past a bus still reaches an already-visited
// node from a DIFFERENT node and is caught as a mesh exactly as before. Barriers
// (an OPEN switch) still stop each branch, so the fan-out stays bounded.
func buildTree(ctx context.Context, db bun.IDB, start node, barriers map[string]barrierCheck, loader *featureClassLoader, closed map[node]bool, breaks *[]meshBreak) (*tree, []node, error) {
	junctionIDs, err := loader.junctions(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	// tierByID drives the cross-layer guard below; an empty map (nothing tiered
	// yet) makes it a clean no-op, so an unseeded deployment behaves exactly as
	// before.
	tierByID, err := loader.tiers(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	visited := map[node]bool{start: true}
	parent := map[node]node{}
	children := map[node][]node{}
	childTerminal := map[node]string{}
	queue := []node{start}
	order := []node{start}
	var barrierHits []node
	// unlocked[n] is true once the sweep has crossed a junction to reach n, after
	// which n expands undirected too (see the junction pass-through note above).
	// The start is locked (directional) so a sweep can't double back before it has
	// even reached a bus.
	unlocked := map[node]bool{}

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

		// Expand this node undirected when it is (or was reached through) a
		// junction; everything it discovers is then unlocked. Otherwise it stays a
		// strict from->to downstream step, exactly as before junction pass-through.
		expandUndirected := unlocked[current] || junctionIDs[current.classID]
		var edges []downstreamEdge
		if expandUndirected {
			edges, err = incidentNeighbors(ctx, db, current)
		} else {
			edges, err = downstreamNeighbors(ctx, db, current)
		}
		if err != nil {
			return nil, nil, err
		}
		// A single physical adjacency can be stored on more than one connectivity
		// row -- a busbar and a way switch wired through two terminals, or the same
		// edge kept in both orientations -- so incidentNeighbors (unlike the strict
		// downstreamNeighbors) can list the same neighbour twice from one node. That
		// is NOT a loop: it is one electrical connection seen twice. Collapse repeats
		// within THIS node's expansion so the second copy can't trip the mesh check
		// below. A genuine reconvergence reaches an already-visited node from a
		// DIFFERENT node (a different `current`), so it still surfaces as a mesh --
		// exactly the artifact-vs-loop split nodal.go's canonicalEdge de-dup makes for
		// the Y-bus builder.
		seenNeighbor := make(map[node]bool, len(edges))
		for _, e := range edges {
			n := e.to
			if seenNeighbor[n] {
				continue
			}
			seenNeighbor[n] = true
			// An undirected expansion lists the edge back to the node current was
			// reached from; that reversed edge is not a real second path, so skip it
			// rather than let it trip mesh detection below.
			if expandUndirected && n == parent[current] {
				continue
			}
			// Cross-layer guard (migration 0126): refuse a downstream step to a
			// strictly-lower tier -- that would be climbing back toward the source,
			// which the undirected expansion past a junction must never do. Judged on
			// the sweep's always-downstream direction. An untiered class on either end
			// is absent from the map (comma-ok, since tier 0 is valid) and always
			// passes, so the guard is transparent until tiers are seeded. A blocked
			// neighbour is NOT marked visited, so a legitimate deeper-tier edge can
			// still reach it by another path.
			if ct, cok := tierByID[current.classID]; cok {
				if nt, nok := tierByID[n.classID]; nok && nt < ct {
					continue
				}
			}
			if visited[n] {
				if breaks != nil {
					// Weakly-meshed run: record the loop-closing edge and keep the
					// spanning tree radial by not re-parenting n. existing keeps its
					// first-discovered parent; mesh.go compensates the loop.
					*breaks = append(*breaks, meshBreak{closer: current, existing: n})
					continue
				}
				return nil, nil, invalid(
					"the network downstream of the start feature is not radial (loop/mesh detected at feature class id %s, feature %s reached by more than one path) -- this analysis walks a single impedance path to each point, so it needs a radial network; run LOOP_CHECK_TRACE first to locate and eliminate the mesh (open the normally-open ring/tie point, or fix the duplicate connectivity)",
					n.classID, n.featureID,
				)
			}
			visited[n] = true
			parent[n] = current
			children[current] = append(children[current], n)
			childTerminal[n] = e.fromTerminalID
			if expandUndirected {
				unlocked[n] = true
			}
			order = append(order, n)
			queue = append(queue, n)
		}
	}

	// Cap tripped with work still queued: the tree is truncated. A sweep over a
	// truncated network is not merely partial (as a trace would be) but
	// numerically WRONG -- the backward sweep would accumulate only the demand of
	// the nodes that fit, silently understating downstream load and overstating
	// voltage. So fail loudly here, the same precondition-failure contract
	// buildTree already applies to a non-radial network above, rather than return
	// an optimistic, complete-looking study. Raise POWERFLOW_MAX_NODES (or start
	// the study lower in the feeder) for a network genuinely larger than the cap.
	if len(queue) > 0 && len(order) >= maxSweepNodes {
		return nil, nil, invalid(
			"the network downstream of the start feature exceeds the %d-node analysis cap -- the study would be computed over a truncated, incomplete network and silently understate downstream load, so it is refused; narrow the study (start lower in the feeder) or raise POWERFLOW_MAX_NODES",
			maxSweepNodes,
		)
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
