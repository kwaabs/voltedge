package trace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
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
	// junctionByID is the set of feature_class ids whose network_role is
	// 'junction' (a busbar, a switchgear panel bus -- a common node with no
	// inherent direction), resolved once per trace run by junctions() and cached
	// here. nil until first loaded, so a trace that never calls junctions() pays
	// nothing; a feeder/section trace that calls bfs in a loop pays exactly one
	// query, shared across every iteration through this same loader.
	junctionByID map[string]bool
	// transformerRoleByID is the set of feature_class ids whose functional_role
	// (migration 0091) is 'transformer' -- the boundary a downstream trace stops
	// at unless told to continue into the LV/secondary side. Same lazy, memoized
	// shape as junctionByID: nil until transformerRoles() is first called, so only
	// a trace that actually asks to stop at transformers pays the one query.
	transformerRoleByID map[string]bool
	// tierByID maps a feature_class id to its trace_tier (migration 0126) -- its
	// rank in the vertical source-to-load hierarchy (0 = grid infeed, higher =
	// closer to the load). ONLY tiered classes appear here; an untiered (NULL)
	// class is absent, so a step to or from it is never tier-guarded (presence
	// must be tested comma-ok, since tier 0 is a real, valid rank). Same lazy,
	// memoized shape as junctionByID: loaded once, only for a directional
	// (DOWNSTREAM/UPSTREAM) walk that actually consults the cross-layer guard.
	tierByID map[string]int
	// boundaryRoleByID maps a feature_class id to its boundary_role (migration
	// 0129) -- the network-SECTIONING function (source/protective/switching/
	// transformer), refining functional_role for "what kind of device heads a
	// section". Only the supply-path terminus walk consults it; nil until
	// boundaryRoles() is first called, same lazy/memoized shape as the others.
	boundaryRoleByID map[string]string
}

func newFeatureClassLoader() *featureClassLoader {
	return &featureClassLoader{codeByID: map[string]string{}, loadedByID: map[string]*metadata.Loaded{}}
}

// junctions returns the set of feature_class ids carrying network_role
// 'junction'. A junction (busbar, switchgear bus) is an electrical common node:
// every edge on it meets at one point with no inherent from/to direction, which
// is why a *directional* downstream walk must pass THROUGH it undirected rather
// than dead-ending on the arbitrary orientation its incident connections were
// authored with (see bfs). Loaded once and memoized on the shared loader.
func (l *featureClassLoader) junctions(ctx context.Context, db bun.IDB) (map[string]bool, error) {
	if l.junctionByID != nil {
		return l.junctionByID, nil
	}
	var fcs []metadata.FeatureClass
	if err := db.NewSelect().Model(&fcs).Column("id").Where("network_role = ?", "junction").Scan(ctx); err != nil {
		return nil, fmt.Errorf("trace: loading junction feature classes: %w", err)
	}
	out := make(map[string]bool, len(fcs))
	for _, fc := range fcs {
		out[fc.ID] = true
	}
	l.junctionByID = out
	return out, nil
}

// transformerRoles returns the set of feature_class ids carrying
// functional_role 'transformer' (DIST_XFMR, POWER_XFMR, AUX_TRANSFORMER, ...).
// A transformer is the boundary between two voltage layers: a downstream walk
// that reaches one has arrived at the far end of a feeder and, unless told to
// cross, should stop rather than leak into the transformer's stepped-down LV
// network (which is a distinct feeder with its own downstream shape). Loaded
// once and memoized on the shared loader, exactly like junctions().
func (l *featureClassLoader) transformerRoles(ctx context.Context, db bun.IDB) (map[string]bool, error) {
	if l.transformerRoleByID != nil {
		return l.transformerRoleByID, nil
	}
	var fcs []metadata.FeatureClass
	if err := db.NewSelect().Model(&fcs).Column("id").Where("functional_role = ?", "transformer").Scan(ctx); err != nil {
		return nil, fmt.Errorf("trace: loading transformer feature classes: %w", err)
	}
	out := make(map[string]bool, len(fcs))
	for _, fc := range fcs {
		out[fc.ID] = true
	}
	l.transformerRoleByID = out
	return out, nil
}

// tiers returns the map of feature_class id -> trace_tier (migration 0126) for
// every class that carries one; untiered (NULL trace_tier) classes are simply
// absent. The tier is a class's rank in the vertical source-to-load hierarchy,
// and it lets a directional walk enforce direction by what a feature IS, not
// only by the from/to orientation its connections were authored with: a
// DOWNSTREAM step may go to an equal-or-higher tier but never to a strictly-
// lower one (walking back toward the source); UPSTREAM mirrors it. A step
// touching an untiered class (absent from this map) is always allowed -- the
// guard is transparent until tiers are seeded (see seed/trace_tiers.sql).
// Loaded once and memoized on the shared loader, exactly like junctions().
func (l *featureClassLoader) tiers(ctx context.Context, db bun.IDB) (map[string]int, error) {
	if l.tierByID != nil {
		return l.tierByID, nil
	}
	var fcs []metadata.FeatureClass
	if err := db.NewSelect().Model(&fcs).Column("id", "trace_tier").Where("trace_tier IS NOT NULL").Scan(ctx); err != nil {
		return nil, fmt.Errorf("trace: loading feature class tiers: %w", err)
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

// boundaryRoles returns feature_class id -> boundary_role (migration 0129) for
// every class carrying one; a NULL-boundary_role class is simply absent. The
// supply-path trace uses it to find the head of a feature's section -- a
// 'protective' breaker/recloser is the MV feeder/section head when no circuit
// designates its source -- and to tag the 'protective'/'switching' section
// boundaries the path passes below the terminus. Loaded once and memoized on
// the shared loader, exactly like transformerRoles().
func (l *featureClassLoader) boundaryRoles(ctx context.Context, db bun.IDB) (map[string]string, error) {
	if l.boundaryRoleByID != nil {
		return l.boundaryRoleByID, nil
	}
	var fcs []metadata.FeatureClass
	if err := db.NewSelect().Model(&fcs).Column("id", "boundary_role").Where("boundary_role IS NOT NULL").Scan(ctx); err != nil {
		return nil, fmt.Errorf("trace: loading boundary roles: %w", err)
	}
	out := make(map[string]string, len(fcs))
	for _, fc := range fcs {
		if fc.BoundaryRole != nil {
			out[fc.ID] = *fc.BoundaryRole
		}
	}
	l.boundaryRoleByID = out
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
	// device_state (a coded DEVICE_STATE value, "OPEN"/"CLOSED") is present only
	// for switching devices; a class without the field, or one carrying it unset,
	// falls through to "" and the field omits. See tracedFeature.DeviceState.
	tf.DeviceState, _ = attrs["device_state"].(string)
	// ownership (a coded OWNERSHIP value, "private"; utility/unset reads as "")
	// -- so a trace result can flag the private stretch of an owner's network
	// without stopping at it. See tracedFeature.Ownership.
	tf.Ownership, _ = attrs["ownership"].(string)
	for _, field := range voltageClassFields {
		raw, _ := attrs[field].(string)
		if v, ok := parseVoltageClassCode(raw); ok {
			tf.NominalVoltageV = &v
			break
		}
	}
	// crud.formatRow only emits a "geometry" key (present + non-nil) when the
	// row has an actual non-null geom, so its presence is exactly "on the map".
	if g, ok := row["geometry"]; ok && g != nil {
		tf.OnMap = true
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
	// Stamp each reached feature with the single-line diagram(s) it's placed on
	// (one batched read for the whole result, not per node), so the client can
	// tell on-map results from diagram-only ones and name the diagrams.
	byKey, err := diagramMembershipsFor(ctx, db, nodes)
	if err != nil {
		return nil, err
	}
	if len(byKey) > 0 {
		for i, n := range nodes {
			if dm := byKey[n.classID+":"+n.featureID]; len(dm) > 0 {
				out[i].Diagrams = dm
			}
		}
	}
	return out, nil
}

// stampParentsFromStart gives a synthetic trace (feeder/section/supply-path) the
// parent pointers runTrace stamps natively (graph.go ~1054), so its result nests
// in the client's Tree view instead of rendering flat. Those traces union or
// exhaust ordinary walks and return a bare feature set with no BFS parent map of
// their own; here we rebuild one by running a plain undirected (CONNECTED) BFS
// rooted at start, CONFINED to the returned set -- so the spanning tree it lays
// down only ever references features already in the response, and every stamped
// parent resolves client-side. features is built 1:1 from nodes (same index, as
// toTracedFeatures guarantees), so we stamp by that index. The start node is the
// root and keeps a nil parent; a node not reachable from start within the set (a
// disconnected island -- e.g. a feeder unioned from several heads) also keeps a
// nil parent, and the client reparents such orphans under the root. Rooting at
// start (not a topological head) is deliberate: it matches the Tree view's
// rootKey = result.start, so start is the one true root across every trace kind.
func stampParentsFromStart(ctx context.Context, db bun.IDB, features []tracedFeature, nodes []node, start node, classCodes map[string]string) error {
	if len(nodes) == 0 {
		return nil
	}
	// nodes/features are 1:1; index by node so a discovered neighbour stamps the
	// right feature. inSet confines the walk to the returned features.
	indexOf := make(map[node]int, len(nodes))
	inSet := make(map[node]bool, len(nodes))
	for i, n := range nodes {
		indexOf[n] = i
		inSet[n] = true
	}
	if !inSet[start] {
		// Start isn't in the result (shouldn't happen for these traces); leave the
		// features parentless so the client falls back to a flat tree under root.
		return nil
	}

	visited := map[node]bool{start: true}
	queue := []node{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		neighbors, err := neighborsOf(ctx, db, current, "CONNECTED")
		if err != nil {
			return err
		}
		for _, n := range neighbors {
			if visited[n] || !inSet[n] {
				continue
			}
			visited[n] = true
			pc := classCodes[current.classID]
			pid := current.featureID
			features[indexOf[n]].ParentFeatureClass = &pc
			features[indexOf[n]].ParentFeatureID = &pid
			queue = append(queue, n)
		}
	}
	return nil
}

// diagramMembershipsFor resolves, in one query, every single-line diagram each
// of nodes is placed on -- runtime.diagram_placements joined to
// runtime.diagram_instances for the diagram's name -- keyed by
// "classID:featureID". Placements are keyed by (feature_class_id, feature_id),
// exactly the pair a trace node carries. Returns nil for an empty node set.
func diagramMembershipsFor(ctx context.Context, db bun.IDB, nodes []node) (map[string][]diagramMembership, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	// Distinct feature ids to filter on; the exact (class,feature) pairs we
	// actually want (feature_id is a uuid, so cross-class collisions are
	// vanishingly unlikely, but match the pair precisely regardless).
	want := make(map[string]bool, len(nodes))
	seen := make(map[string]bool, len(nodes))
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		want[n.classID+":"+n.featureID] = true
		if !seen[n.featureID] {
			seen[n.featureID] = true
			ids = append(ids, n.featureID)
		}
	}
	var rows []struct {
		FeatureClassID    string  `bun:"feature_class_id"`
		FeatureID         string  `bun:"feature_id"`
		DiagramInstanceID string  `bun:"diagram_instance_id"`
		Name              *string `bun:"name"`
	}
	err := db.NewSelect().
		TableExpr("runtime.diagram_placements AS p").
		ColumnExpr("p.feature_class_id, p.feature_id, p.diagram_instance_id, i.name").
		Join("JOIN runtime.diagram_instances AS i ON i.id = p.diagram_instance_id").
		Where("p.feature_id IN (?)", bun.In(ids)).
		OrderExpr("i.name NULLS LAST, i.id").
		Scan(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("load diagram memberships: %w", err)
	}
	out := make(map[string][]diagramMembership)
	for _, r := range rows {
		key := r.FeatureClassID + ":" + r.FeatureID
		if !want[key] {
			continue
		}
		name := ""
		if r.Name != nil {
			name = *r.Name
		}
		out[key] = append(out[key], diagramMembership{InstanceID: r.DiagramInstanceID, Name: name})
	}
	return out, nil
}

// neighborsOf returns every node directly reachable from n through
// runtime.feature_connectivity, filtered by direction: DOWNSTREAM only
// follows an edge where n is the "from" side (walking toward "to"),
// UPSTREAM only follows the reverse, anything else (CONNECTED,
// SHORTEST_PATH) treats every edge touching n as undirected.
// neighborsOf returns n's graph neighbors in the given direction. DOWNSTREAM
// follows an edge only where n is the "from" side (walking toward "to"), UPSTREAM
// only the reverse; anything else (CONNECTED, SHORTEST_PATH) treats every edge
// touching n as undirected. The authored from/to orientation is the direction
// signal -- the network rules wire a device line-IN / load-OUT, so a strict
// directional walk respects a radial feeder's direction on its own (see bfs's
// junction pass-through for how a common-node bus, whose edges are one-way-sink,
// is handled undirected instead).
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
	// A soft-deleted feature must not conduct: drop any edge whose endpoint is a
	// deleted feature so a trace never walks THROUGH or INTO one (the start node is
	// already deleted-checked in resolveNode; this closes the mid-traversal gap, so
	// a deleted feature is gone from analysis too, matching its removal from peers'
	// Terminals/Relationships lists and the map). "Live" == a feature_registry row
	// with deleted_at IS NULL, the cross-class liveness the connectivity audit/
	// cleanup use; correlated EXISTS keeps it an index probe in this per-node BFS.
	//
	// The same probe also enforces the asset-lifecycle gate: an edge only conducts
	// if BOTH endpoints are in an OPERATIONAL lifecycle state (IN_SERVICE/DERATED).
	// A PLANNED, under-construction, retired or removed asset isn't part of the
	// live network, so the trace walks neither THROUGH nor INTO it -- the same
	// mid-traversal gap the deleted-feature filter closes. lifecycle_status is
	// mirrored into the registry (migration 0156) so this stays one index probe.
	q = q.Where("EXISTS (SELECT 1 FROM runtime.feature_registry r WHERE r.feature_id = feature_connectivity.from_feature_id AND r.deleted_at IS NULL AND "+crud.LifecycleOperationalClause("r")+")").
		Where("EXISTS (SELECT 1 FROM runtime.feature_registry r WHERE r.feature_id = feature_connectivity.to_feature_id AND r.deleted_at IS NULL AND "+crud.LifecycleOperationalClause("r")+")")
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

// maxTraceHops bounds how many nodes a single trace will ever visit -- a
// defensive valve against a pathological data bug (e.g. a malformed pair of
// connectivity rows forming an unexpected cycle through nodes a barrier should
// have stopped at), NOT a design limit. A trace that still trips it returns a
// flagged-incomplete result (traceResponse.truncated) rather than silently
// partial, so a large feeder is warned about, never quietly wrong. The default
// accommodates a large distribution feeder; raise it with TRACE_MAX_HOPS for a
// bigger network (and see the set-based walk follow-up for whole-network traces).
var maxTraceHops = traceMaxHopsFromEnv()

// traceMaxHopsFromEnv reads TRACE_MAX_HOPS (a positive integer) or returns the
// default. Read once at package init -- a trace never re-reads it.
func traceMaxHopsFromEnv() int {
	if v := os.Getenv("TRACE_MAX_HOPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 50000
}

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
// targetClassIDs is a different, later-added concept from target above:
// target names one specific feature (shortest_path's destination),
// targetClassIDs names a SET of feature *classes* to stop at the first
// node of ANY of -- e.g. "walk upstream until you reach a DIST_XFMR,"
// or, spanning a whole functional_role, "until you reach the nearest
// controller" (any of MV_SWITCH/MV_BREAKER/MV_RECLOSER/...). A single
// named class is just a one-element set; nil/empty means no class
// target. The start node itself never counts as a class-target hit even
// if its own class matches, so a trace started AT a transformer
// correctly finds the *next* one, not itself. crossTransformers controls what happens
// once a class-target node is reached: false (the trace_configurations
// default) stops expansion there, same as a barrier; true records the
// hit but keeps walking past it -- the real, enforced meaning that
// column only ever had as a stub before this.
//
// Transformer boundary (DOWNSTREAM only, opt-in via stopAtTransformers):
// independent of the config's target_class_id, a downstream walk can be told to
// stop at every functional_role='transformer' node it reaches -- the boundary
// between a feeder and the transformer's stepped-down LV network. When
// stopAtTransformers is true the walk records each such transformer in
// transformerHits and does not expand past it (the transformer itself is still
// visited/energized -- only its onward LV expansion is cut). This is what backs
// the map's "continue past transformers" checkbox: unticked -> stopAtTransformers
// true (default, stop at the transformer); ticked -> false (flow into the LV
// side). Only the user-facing graph trace (runTrace) ever sets it; energization,
// feeder, service, section and isolate all pass false, so a transformer keeps
// passing power downstream for them exactly as before -- stopping there would
// wrongly strand a transformer's whole LV network as de-energized.
//
// scopeCircuit, when non-nil (a scope_to_circuit config, migration 0069),
// confines the walk to the start's stored circuit: a neighbour is only
// enqueued/recorded when its own live circuit_id equals *scopeCircuit. An
// off-circuit neighbour (different, unset, or no circuit_id field at all) is
// the circuit boundary -- marked visited so a second edge to it isn't
// re-checked, but never traversed or returned. The start is always kept
// (resolved by the caller to carry *scopeCircuit in the first place).
//
// Junction pass-through (DOWNSTREAM and UPSTREAM): a busbar / switchgear bus is
// an electrical common node -- every way meets at one point with no inherent
// from/to direction, yet an assembly fan-out authors all its connections in one
// fixed orientation (e.g. the RMU wires every way switch->bus, so the bus is
// only ever a "to"). A strict directional walk following from->to (or its
// reverse) therefore mis-handles such a one-way-sink bus, and the two directions
// mis-handle it in mirror-image ways:
//
//   - A DOWNSTREAM walk *reaches* the bus fine (every way's switch is a "from")
//     but then dead-ends there -- the bus is never a "from", so following
//     from->to can't leave it to reach the sibling ways.
//   - An UPSTREAM walk (following to->from) has the opposite problem: it can
//     leave a bus easily (to->from off the bus lands on every "from" switch) but
//     can never *arrive* -- no edge has the bus as a "from", so no to->from step
//     lands on it. It dead-ends at the last switch *below* the bus instead.
//
// So junction handling has two halves, each covering one direction's gap:
//
//  1. LEAVE (helps DOWNSTREAM): when a walk reaches a network_role='junction'
//     node it expands that node UNDIRECTED (every incident edge, either
//     direction), and every node discovered from there is "unlocked" -- it too
//     expands undirected, so the walk continues out through the sibling ways'
//     switches and cables that were authored bus-inward.
//  2. ARRIVE (helps UPSTREAM): a node that isn't itself expanding undirected
//     additionally pulls in any incident neighbour that *is* a junction,
//     regardless of edge orientation -- so an upstream walk crosses the way->bus
//     edge against its grain to reach the bus, after which half (1) unlocks the
//     source-side spine and the sibling ways. Applied only to UPSTREAM: a
//     downstream walk reaches its bus naturally, and pulling in a junction
//     against the edge there would be the "from" bus one level up -- climbing
//     back toward the source, exactly what downstream must not do.
//
// Everything before the first bus stays strictly directional (the start is
// locked), and the cross-layer tier guard below -- judged on the ORIGINAL
// direction -- still applies through an undirected expansion, so neither
// direction leaks back the way it came past a bus. Barriers (an OPEN switch)
// still stop each branch, so the fan-out past a bus stays bounded exactly as it
// should. CONNECTED/SHORTEST_PATH are undirected to begin with and untouched.
func bfs(ctx context.Context, db bun.IDB, start node, direction string, barriers map[string][]barrierCheck, loader *featureClassLoader, target *node, targetClassIDs map[string]bool, crossTransformers bool, stopAtTransformers bool, scopeCircuit *string) (order []node, barrierHits []node, targetHits []node, transformerHits []node, parent map[node]node, found bool, truncated bool, err error) {
	junctionIDs, jerr := loader.junctions(ctx, db)
	if jerr != nil {
		err = jerr
		return
	}
	// passThroughJunctions gates the whole behaviour to the two directional
	// walks; CONNECTED/SHORTEST_PATH never consult junctionIDs or unlocked at all
	// (already undirected). Both DOWNSTREAM and UPSTREAM cross a one-way-sink bus,
	// each closing its own half of the gap (see the doc comment above).
	passThroughJunctions := direction == "DOWNSTREAM" || direction == "UPSTREAM"

	// startIsInlineDevice is true when the trace STARTS on an inline protective or
	// switching device (boundary_role 'protective'/'switching' -- fuse, switch,
	// breaker, recloser, isolator, cut-out). Such a device is network_role=
	// 'junction' like almost every point feature (it just means "a graph node"),
	// but unlike a common-node busbar it has a real line side and load side. Its
	// connections are authored line-IN / load-OUT by the network rules, so a STRICT
	// directional walk from it already points the right way -- fanning it out
	// undirected (the junction pass-through below) is what made a downstream trace
	// from a feeder-pillar fuse climb back up onto the bus feeding it. So the start
	// stays strictly directional when it is one of these devices; this rests on the
	// authored from/to alone and needs no terminal data. Only the start is treated
	// this way: a device reached MID-walk still expands via the junction rules an
	// RMU and other meshed assemblies depend on (and for a radial pillar a mid-walk
	// fuse can only reach its already-visited bus, so it changes nothing there).
	var startIsInlineDevice bool
	if passThroughJunctions {
		var boundaryRole map[string]string
		boundaryRole, err = loader.boundaryRoles(ctx, db)
		if err != nil {
			return
		}
		switch boundaryRole[start.classID] {
		case "protective", "switching":
			startIsInlineDevice = true
		}
	}

	// transformerIDs is only consulted on an opt-in downstream stop, so only that
	// path pays the (memoized) query -- every other trace leaves it nil, and a nil
	// map reads false, so the boundary check below is a no-op for them.
	var transformerIDs map[string]bool
	if stopAtTransformers && direction == "DOWNSTREAM" {
		transformerIDs, err = loader.transformerRoles(ctx, db)
		if err != nil {
			return
		}
	}

	// tierByID (migration 0126) drives the cross-layer guard below: a directional
	// walk may step within or deeper into the hierarchy but never back toward the
	// end it came from. Loaded only for the two directional walks -- CONNECTED /
	// SHORTEST_PATH are undirected by definition and have no "toward the source"
	// to protect, so they never pay the query. An empty map (nothing tiered yet)
	// makes the guard a clean no-op.
	var tierByID map[string]int
	if direction == "DOWNSTREAM" || direction == "UPSTREAM" {
		tierByID, err = loader.tiers(ctx, db)
		if err != nil {
			return
		}
	}

	visited := map[node]bool{start: true}
	parent = map[node]node{}
	queue := []node{start}
	order = []node{start}
	barrierSet := map[node]bool{}
	targetSet := map[node]bool{}
	transformerSet := map[node]bool{}
	// unlocked[n] is true once the walk has crossed a junction to reach n, after
	// which n expands undirected too (see the junction pass-through note above).
	// The start is locked (directional) so a walk can't immediately double back
	// the way it came before it has even reached a bus.
	unlocked := map[node]bool{}
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

		isClassTarget := len(targetClassIDs) > 0 && current != start && targetClassIDs[current.classID]
		if isClassTarget {
			targetSet[current] = true
			if !crossTransformers {
				continue
			}
		}

		// Transformer boundary: a downstream walk that reached a transformer (and
		// was asked to stop at them) records it and expands no further into the LV
		// side. The transformer stays in `order` -- it's reached/energized -- only
		// its onward expansion is cut, the same record-then-stop shape a barrier or
		// class-target uses. transformerIDs is nil for every non-opted-in trace, so
		// this is a no-op lookup for them.
		if transformerIDs[current.classID] && current != start {
			transformerSet[current] = true
			continue
		}

		// Expand this node undirected when it is (or was reached through) a
		// junction on a pass-through walk; everything it discovers is then
		// unlocked. This is the LEAVE half -- it lets a walk that has reached a bus
		// fan out through the sibling ways authored bus-inward (see the doc above).
		// EXCEPTION: when the START itself is an inline protective/switching device
		// (startIsInlineDevice), it stays strictly directional despite being a
		// network_role='junction' node -- it is a section head with a real line/load
		// orientation, not a common-node bus, so a downstream trace descends to its
		// load side via the authored from/to and never fans back up onto the bus
		// feeding it. Only the start is special-cased; a device reached mid-walk
		// still expands via the junction rules meshed assemblies (an RMU) rely on.
		startInlineStrict := current == start && startIsInlineDevice
		expandUndirected := passThroughJunctions && !startInlineStrict &&
			(unlocked[current] || junctionIDs[current.classID])
		effectiveDirection := direction
		if expandUndirected {
			effectiveDirection = "CONNECTED"
		}
		// unlockNeighbors: a node discovered here expands undirected itself when this
		// node did (the junction LEAVE half) OR when this node is the strict inline-
		// device start. The start fuse walks its ONE authored load-side edge (strict,
		// so it never climbs to the bus), but the conductor chain below it is spliced
		// conductor->conductor in whatever order it was drawn -- an arbitrary from/to
		// that only an undirected walk can follow. Unlocking the start's load-side
		// neighbour hands the rest of that chain to the normal pass-through, so the
		// trace descends the whole feeder instead of dead-ending at the first splice
		// authored against the direction of flow.
		unlockNeighbors := expandUndirected || startInlineStrict

		var neighbors []node
		neighbors, err = neighborsOf(ctx, db, current, effectiveDirection)
		if err != nil {
			return
		}

		// ARRIVE half (UPSTREAM only): a one-way-sink bus is authored with every
		// edge pointing INTO it (way->bus), so an upstream to->from walk never
		// lands on the bus and dead-ends at the last switch below it. When this
		// node isn't already expanding undirected, additionally pull in any
		// incident neighbour that is itself a junction, regardless of edge
		// orientation -- then the bus, once enqueued, takes the LEAVE path above
		// and unlocks the source-side spine. Restricted to UPSTREAM on purpose: a
		// downstream walk reaches its bus naturally, and reaching a junction
		// against the edge there would be the "from" bus one level up (climbing
		// toward the source) -- the one thing a downstream trace must never do. The
		// tier guard still vets every step, so this can't leak past a bus toward
		// the load either.
		if passThroughJunctions && direction == "UPSTREAM" && !expandUndirected {
			var incident []node
			incident, err = neighborsOf(ctx, db, current, "CONNECTED")
			if err != nil {
				return
			}
			for _, n := range incident {
				if junctionIDs[n.classID] {
					neighbors = append(neighbors, n)
				}
			}
		}

		for _, n := range neighbors {
			if visited[n] {
				continue
			}
			// Cross-layer guard (migration 0126): both this node and the neighbour
			// carry a trace_tier, so the step's direction in the source-to-load
			// hierarchy is known independently of the connection's from/to. A
			// DOWNSTREAM walk refuses a step to a strictly-lower tier (that would be
			// climbing back toward the source); UPSTREAM refuses a strictly-higher
			// one. Judged on the ORIGINAL `direction`, so an undirected expansion
			// past a junction bus is still tier-guarded and can't leak back up the
			// feeder that supplies the bus. An untiered class on either end is
			// absent from the map (comma-ok, since tier 0 is valid) and always
			// passes -- the guard is transparent until tiers are seeded. Blocked
			// neighbours are deliberately NOT marked visited, so a legitimate
			// same-or-deeper-tier edge can still reach them by another path.
			if ct, cok := tierByID[current.classID]; cok {
				if nt, nok := tierByID[n.classID]; nok {
					if direction == "DOWNSTREAM" && nt < ct {
						continue
					}
					if direction == "UPSTREAM" && nt > ct {
						continue
					}
				}
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
			if unlockNeighbors {
				unlocked[n] = true
			}
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

	// The cap was hit with work still queued -- the result is incomplete, so
	// callers can warn instead of presenting a silently-partial trace. A `found`
	// break (len(order) below the cap) or a drained queue both leave this false.
	truncated = len(queue) > 0 && len(order) >= maxTraceHops

	barrierHits = make([]node, 0, len(barrierSet))
	for n := range barrierSet {
		barrierHits = append(barrierHits, n)
	}
	targetHits = make([]node, 0, len(targetSet))
	for n := range targetSet {
		targetHits = append(targetHits, n)
	}
	transformerHits = make([]node, 0, len(transformerSet))
	for n := range transformerSet {
		transformerHits = append(transformerHits, n)
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
		out[i] = traceConfigurationSummary{Code: c.Code, Name: c.Name, Direction: codeByID[c.DirectionValueID], ScopeToCircuit: c.ScopeToCircuit}
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

	// Phase 58 spatial gate: a scoped user may not trace from a feature outside
	// their territories, and no out-of-scope feature is ever surfaced in the
	// result. A no-op unless enforcement is on and the user is scoped. The start
	// (and any shortest-path target) must be in scope up front -- an
	// out-of-scope start reads as a 404, the same answer GET gives.
	scope, err := resolveTraceScope(ctx, db)
	if err != nil {
		return nil, err
	}
	if scope.apply {
		ok, err := scope.permits(ctx, db, start, loader)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, notFound("feature %s (%s) not found", req.Start.FeatureID, req.Start.FeatureClass)
		}
		if target != nil {
			ok, err := scope.permits(ctx, db, *target, loader)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, notFound("feature %s (%s) not found", req.Target.FeatureID, req.Target.FeatureClass)
			}
		}
	}

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

	// Effective cross-transformers: the config's stored default (cross_transformers),
	// overridden per-run by the request when the caller sends one -- the map's
	// "continue past transformers" checkbox. It governs both the config's
	// target_class_id crossing (existing) and the functional_role='transformer'
	// boundary below. stopAtTransformers turns that boundary ON, and only for a
	// downstream walk -- upstream/connected/shortest never stop at a transformer,
	// and neither do the other bfs callers (energization et al. pass false).
	crossTransformers := tc.CrossTransformers
	if req.CrossTransformers != nil {
		crossTransformers = *req.CrossTransformers
	}
	stopAtTransformers := direction == "DOWNSTREAM" && !crossTransformers

	visited, barrierHits, targetHits, transformerHits, parent, found, truncated, err := bfs(ctx, db, start, direction, barriers, loader, target, singleClassSet(tc.TargetClassID), crossTransformers, stopAtTransformers, scopeCircuit)
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

	// Phase 58: drop any out-of-scope feature the walk passed through so the
	// response discloses only features within the user's territories (a no-op
	// when the gate doesn't apply). Barriers and targets are filtered too.
	if scope.apply {
		if featureNodes, err = scope.filter(ctx, db, featureNodes, loader); err != nil {
			return nil, err
		}
		if barrierHits, err = scope.filter(ctx, db, barrierHits, loader); err != nil {
			return nil, err
		}
		if targetHits, err = scope.filter(ctx, db, targetHits, loader); err != nil {
			return nil, err
		}
		if transformerHits, err = scope.filter(ctx, db, transformerHits, loader); err != nil {
			return nil, err
		}
	}

	allNodes := make([]node, 0, len(featureNodes)+len(barrierHits)+len(targetHits)+len(transformerHits))
	allNodes = append(allNodes, featureNodes...)
	allNodes = append(allNodes, barrierHits...)
	allNodes = append(allNodes, targetHits...)
	allNodes = append(allNodes, transformerHits...)
	classCodes, err := nodeClassCodes(ctx, db, allNodes)
	if err != nil {
		return nil, err
	}

	features, err := toTracedFeatures(ctx, db, featureNodes, classCodes, loader)
	if err != nil {
		return nil, err
	}

	// Stamp each reached feature's BFS parent (the feature one hop closer to the
	// start) so a client can orient trace direction per line without another
	// query -- the map's directional marching-ants animation reads it. features
	// is built 1:1 from featureNodes, in order, so index i lines up. Only a
	// parent that is itself in the response is referenced (a scope-filtered
	// parent is dropped, since a client couldn't resolve its geometry); the start
	// node has no parent entry at all.
	inResponse := make(map[node]bool, len(featureNodes))
	for _, n := range featureNodes {
		inResponse[n] = true
	}
	for i, n := range featureNodes {
		p, ok := parent[n]
		if !ok || !inResponse[p] {
			continue
		}
		pc := classCodes[p.classID]
		pid := p.featureID
		features[i].ParentFeatureClass = &pc
		features[i].ParentFeatureID = &pid
	}

	// Phase 62: roll the reached SERVICE_POINTs up by load + restoration tier,
	// so every trace carries "what consumption sits below this" alongside the
	// topology. Reuses the same nodes/classCodes/loader the walk already built,
	// so it costs one live read per reached SP and nothing per non-load node.
	service, err := summarizeService(ctx, db, featureNodes, classCodes, loader, true)
	if err != nil {
		return nil, err
	}

	resp := &traceResponse{
		TraceConfiguration: tc.Code,
		Direction:          direction,
		Start:              req.Start,
		Features:           features,
		Barriers:           toFeatureRefs(barrierHits, classCodes),
		Service:            service,
		CrossTransformers:  crossTransformers,
		Truncated:          truncated,
	}
	if len(transformerHits) > 0 {
		resp.TransformerBoundaries = toFeatureRefs(transformerHits, classCodes)
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
