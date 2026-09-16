package export

// CIM topology synthesis -- Phase 16 Slice 2, docs/phase-16-cim-export.md.
//
// The behavioral heart of the CIM export: turning VoltEdge's pairwise
// connectivity records into the ConnectivityNode/Terminal graph CIM models
// electrical networks with. VoltEdge stores connectivity as undirected edges in
// runtime.feature_connectivity -- each row says "endpoint A of feature X touches
// endpoint B of feature Y". CIM instead models a shared electrical point (a
// ConnectivityNode) that every touching equipment endpoint (a Terminal) points
// at. So this file:
//
//  1. Builds a graph whose vertices are equipment endpoints and whose edges are
//     the connectivity rows, then finds its connected components (union-find).
//     Each component is one ConnectivityNode; every endpoint in it is a Terminal
//     referencing that node.
//  2. Disambiguates conductor ends. An edge feature (a conductor) has no named
//     terminals, so its connectivity rows carry a NULL terminal -- both ends of
//     a line look identical by id. They're told apart by geometry: each
//     connection is matched to the nearer end of the conductor's LineString.
//  3. Synthesizes the full expected terminal set per equipment (a conductor gets
//     2, a junction gets one per non-ground metadata terminal), so an
//     unconnected end still yields a valid Terminal on its own isolated node
//     rather than a malformed one-terminal ACLineSegment.
//  4. Emits PowerTransformerEnds: a PowerTransformer's terminals belong to its
//     windings, so each transformer terminal also produces a TransformerEnd.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// connRow is a read-only view of the runtime.feature_connectivity columns the
// encoder needs: which feature+terminal each end of an edge touches. Terminal
// ids are nullable -- NULL on a conductor end (an edge feature has no named
// terminals) and on a junction whose class defines none.
type connRow struct {
	bun.BaseModel `bun:"table:runtime.feature_connectivity,alias:fc"`

	FromFeatureClassID string  `bun:"from_feature_class_id"`
	FromFeatureID      string  `bun:"from_feature_id"`
	FromTerminalID     *string `bun:"from_terminal_id"`
	ToFeatureClassID   string  `bun:"to_feature_class_id"`
	ToFeatureID        string  `bun:"to_feature_id"`
	ToTerminalID       *string `bun:"to_terminal_id"`
}

// loadConnectivity pulls the connectivity edges that lie wholly inside the
// exported feature set. It filters in SQL by the exported class ids (few), then
// in Go to rows whose both endpoints survived the export's bbox/territory scope
// -- so the resulting graph is closed: no Terminal ever points at a feature that
// isn't in the file.
func loadConnectivity(ctx context.Context, db bun.IDB, layers []Layer) ([]connRow, error) {
	classIDs := make([]string, 0, len(layers))
	inSet := map[string]bool{}
	for _, l := range layers {
		if l.ClassID != "" {
			classIDs = append(classIDs, l.ClassID)
		}
		for _, f := range l.Features {
			inSet[f.ID] = true
		}
	}
	if len(classIDs) == 0 {
		return nil, nil
	}

	var rows []connRow
	err := db.NewSelect().
		Model(&rows).
		Where("from_feature_class_id IN (?) AND to_feature_class_id IN (?)", bun.In(classIDs), bun.In(classIDs)).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("export: loading connectivity: %w", err)
	}

	filtered := rows[:0]
	for _, r := range rows {
		if inSet[r.FromFeatureID] && inSet[r.ToFeatureID] {
			filtered = append(filtered, r)
		}
	}
	return filtered, nil
}

// featTopo is a conducting feature indexed for topology: its cim mapping, its
// vertices (for representative point + conductor ends), and enough of its layer
// to resolve its expected terminal set. Only conducting_edge/conducting_junction
// features get one -- containers/assets/protection have no terminals.
type featTopo struct {
	id          string
	layer       *Layer
	feat        *Feature // the underlying feature (attributes, for transformer ratings)
	cim         metadata.CimClassMapping
	subtypeCode *int
	isEdge      bool
	pts         []coord
}

// rep is the feature's representative point (its first vertex), the anchor a
// touching conductor end is measured against. ok is false for an empty geometry.
func (ft *featTopo) rep() (coord, bool) {
	if len(ft.pts) > 0 {
		return ft.pts[0], true
	}
	return coord{}, false
}

// ends returns a conductor's two LineString endpoints (first and last vertex).
// A single-vertex or empty geometry yields a degenerate pair so callers can
// still index [0]/[1] safely.
func (ft *featTopo) ends() [2]coord {
	n := len(ft.pts)
	switch {
	case n == 0:
		return [2]coord{}
	case n == 1:
		return [2]coord{ft.pts[0], ft.pts[0]}
	default:
		return [2]coord{ft.pts[0], ft.pts[n-1]}
	}
}

// endpoint is one vertex of the topology graph: an equipment endpoint that
// becomes a CIM Terminal. seq is the Terminal.sequenceNumber (and, for a
// transformer, the winding endNumber): 1/2 for a conductor's two ends, the
// terminal's ordinal for a junction.
type endpoint struct {
	feature string
	seq     int
}

// topoStats is the observability signal for the union-find pass -- the sizes
// operators watch to know how close a CIM export gets to the topology memory
// ceiling (Phase 17 Slice 5, docs/phase-17-large-export.md). Slice 5 (a
// persisted, incrementally-maintained topology table) was designed but NOT
// built: no export has yet hit the bottleneck. This struct is the trigger
// signal instead -- runExport logs it per CIM job, so the day a real export's
// ParentBytes approaches the ~240MB projection (the estimate at 10M features),
// there is a concrete number in the logs telling us Slice 5's time has come.
type topoStats struct {
	Conns     int // connectivity edges consumed
	Endpoints int // graph vertices (Terminals) == union-find set size
	Nodes     int // connected components == ConnectivityNodes emitted
	// ParentBytes is a rough footprint of the union-find `parent` map, the
	// dominant in-memory structure. A map[string]string entry costs ~48 bytes of
	// header/bucket overhead plus the key bytes twice (key + a same-shaped root
	// value), and every endpoint key is stored, so len(parent) == Endpoints.
	// It is an order-of-magnitude gauge, not an exact allocation.
	ParentBytes int64
	// Unmapped is the Phase 55 Slice 2 warn-on-drop signal, piggybacked on the
	// same stats return runExport already logs: feature_class_code -> count of
	// features silently dropped from this export because the class has no
	// cim_class_mappings row (ClassFor returned ok=false). A class intentionally
	// excluded via cim_kind='skip' is NOT counted here -- it has a mapping row and
	// its omission is deliberate. Empty when every class with features was mapped.
	Unmapped map[string]int
}

// writeTopology builds the connectivity graph over the exported equipment, finds
// its connected components, and emits the ConnectivityNodes, Terminals, and
// PowerTransformerEnds. It appends to cw.b like the other passes, and returns
// the topoStats trigger signal (Slice 5) for the caller to log.
func (cw *cimWriter) writeTopology(layers []Layer, conns []connRow) topoStats {
	feats := cw.indexFeatures(layers)

	// Union-find over endpoint keys. A key is "T|<feature>|<terminalID>" for a
	// named/implicit terminal or "E|<feature>|<endIndex>" for a conductor end.
	parent := map[string]string{}
	endpoints := map[string]endpoint{}
	var find func(string) string
	find = func(x string) string {
		p, ok := parent[x]
		if !ok || p == x {
			parent[x] = x
			return x
		}
		r := find(p)
		parent[x] = r
		return r
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[ra] = rb
		}
	}
	ensure := func(key string, ep endpoint) {
		if _, ok := endpoints[key]; !ok {
			endpoints[key] = ep
			parent[key] = key
		}
	}

	// ground terminal ids are excluded from CIM (a grounded winding is modeled
	// differently); a connection landing on one is dropped rather than wired.
	ground := groundTerminalIDs(layers)

	// Pass over the connectivity rows: resolve each end to a key and union them.
	for _, c := range conns {
		ftA, okA := feats[c.FromFeatureID]
		ftB, okB := feats[c.ToFeatureID]
		if !okA || !okB {
			continue // a non-conducting endpoint (container/asset) -- no terminal
		}

		// Conductor-to-conductor: both ends carry NULL terminals, so neither
		// side's representative point anchors the other well. Pick the nearest
		// endpoint pair between the two LineStrings and assign each accordingly.
		if c.FromTerminalID == nil && ftA.isEdge && c.ToTerminalID == nil && ftB.isEdge {
			i, j := nearestEndPair(ftA.ends(), ftB.ends())
			keyA, keyB := edgeKey(ftA.id, i), edgeKey(ftB.id, j)
			ensure(keyA, endpoint{feature: ftA.id, seq: i + 1})
			ensure(keyB, endpoint{feature: ftB.id, seq: j + 1})
			union(keyA, keyB)
			continue
		}

		keyA, okA := cw.endpointKey(ftA, c.FromTerminalID, ftB, ground, ensure)
		keyB, okB := cw.endpointKey(ftB, c.ToTerminalID, ftA, ground, ensure)
		if okA && okB {
			union(keyA, keyB)
		}
	}

	// Synthesize the full expected terminal set so every equipment is complete,
	// even where connectivity is sparse. Endpoints already created above are left
	// as-is (ensure is a no-op); missing ones become singleton nodes.
	for _, ft := range feats {
		if ft.isEdge {
			for idx := 0; idx < 2; idx++ {
				ensure(edgeKey(ft.id, idx), endpoint{feature: ft.id, seq: idx + 1})
			}
			continue
		}
		terms := resolveTerminals(ft.layer.Terminals, ft.layer.Subtypes, ft.subtypeCode)
		if len(terms) == 0 {
			ensure(termKey(ft.id, ""), endpoint{feature: ft.id, seq: 1})
			continue
		}
		for i, t := range terms {
			ensure(termKey(ft.id, t.ID), endpoint{feature: ft.id, seq: i + 1})
		}
	}

	// Group endpoints into connected components. Sorted keys throughout keep the
	// output (node mRIDs, element order) deterministic across exports.
	comps := map[string][]string{}
	for key := range endpoints {
		root := find(key)
		comps[root] = append(comps[root], key)
	}
	roots := make([]string, 0, len(comps))
	for root := range comps {
		roots = append(roots, root)
	}
	sort.Strings(roots)

	for _, root := range roots {
		members := comps[root]
		sort.Strings(members)
		cw.writeConnectivityNode(members, endpoints, feats)
	}

	// Estimate the parent map's footprint from its entry count (== len(endpoints),
	// since every endpoint key is a parent key). ~48 bytes/entry of Go map
	// overhead plus the key string held as both key and root value.
	var keyBytes int64
	for k := range parent {
		keyBytes += int64(len(k)) * 2
	}
	return topoStats{
		Conns:       len(conns),
		Endpoints:   len(endpoints),
		Nodes:       len(comps),
		ParentBytes: int64(len(parent))*48 + keyBytes,
	}
}

// endpointKey resolves one side of a connection to its graph key, creating the
// endpoint if new. termID is that side's stored terminal id (nil for a conductor
// end or a terminal-less junction); other is the feature at the far end, whose
// representative point anchors a conductor-end geometry match. It returns false
// (and creates nothing) for a connection landing on a ground terminal.
func (cw *cimWriter) endpointKey(ft *featTopo, termID *string, other *featTopo, ground map[string]bool, ensure func(string, endpoint)) (string, bool) {
	if termID != nil {
		if ground[*termID] {
			return "", false
		}
		key := termKey(ft.id, *termID)
		ensure(key, endpoint{feature: ft.id, seq: cw.terminalSeq(ft, *termID)})
		return key, true
	}
	if ft.isEdge {
		idx := 0
		ends := ft.ends()
		if anchor, ok := other.rep(); ok {
			if dist2(anchor, ends[1]) < dist2(anchor, ends[0]) {
				idx = 1
			}
		}
		key := edgeKey(ft.id, idx)
		ensure(key, endpoint{feature: ft.id, seq: idx + 1})
		return key, true
	}
	// A junction whose class defines no terminals: one implicit terminal.
	key := termKey(ft.id, "")
	ensure(key, endpoint{feature: ft.id, seq: 1})
	return key, true
}

// writeConnectivityNode emits one ConnectivityNode for a component plus a
// Terminal (and, for a transformer, a PowerTransformerEnd) per member endpoint.
// The node mRID is derived from the sorted member keys so it's stable across
// exports; it's placed in the substation containing any member, else the default
// Line container.
func (cw *cimWriter) writeConnectivityNode(members []string, endpoints map[string]endpoint, feats map[string]*featTopo) {
	nodeID := uuidV5("connectivitynode:" + strings.Join(members, ";"))
	container := cw.lineID
	for _, k := range members {
		ft, ok := feats[endpoints[k].feature]
		if !ok {
			continue
		}
		if p, ok := ft.rep(); ok {
			if sub, ok := cw.substationFor(p); ok {
				container = sub
				break
			}
		}
	}

	cw.openObj("ConnectivityNode", nodeID)
	cw.mRID(nodeID)
	cw.ref("ConnectivityNode.ConnectivityNodeContainer", container)
	cw.closeObj("ConnectivityNode")

	for _, k := range members {
		ep := endpoints[k]
		termID := uuidV5("terminal:" + k)
		cw.writeTerminal(termID, ep.feature, nodeID, ep.seq)
		if ft, ok := feats[ep.feature]; ok && ft.cim.CimClass == "PowerTransformer" {
			cw.writeTransformerEnd(ft, k, ep, termID)
		}
	}
}

// writeTerminal emits a cim:Terminal binding one equipment endpoint to its
// ConnectivityNode.
func (cw *cimWriter) writeTerminal(termID, feature, nodeID string, seq int) {
	cw.openObj("Terminal", termID)
	cw.mRID(termID)
	cw.ref("Terminal.ConductingEquipment", feature)
	cw.ref("Terminal.ConnectivityNode", nodeID)
	cw.lit("Terminal.sequenceNumber", strconv.Itoa(seq))
	cw.closeObj("Terminal")
}

// writeTransformerEnd emits the PowerTransformerEnd (winding) a transformer's
// terminal belongs to: wired to its transformer and terminal with its endNumber,
// plus (Slice 3) its ratings. End-targeted attribute mappings (e.g. rated_kva ->
// PowerTransformerEnd.ratedS) apply to every end; the primary winding (endNumber
// 1) additionally carries the rated voltage + BaseVoltage from primary_voltage
// (there is no seeded secondary voltage, so the LV end gets none).
func (cw *cimWriter) writeTransformerEnd(ft *featTopo, memberKey string, ep endpoint, termID string) {
	endID := uuidV5("ptend:" + memberKey)
	attrs := cw.mapping.AttributesFor(ft.layer.Code)

	cw.openObj("PowerTransformerEnd", endID)
	cw.mRID(endID)
	cw.ref("PowerTransformerEnd.PowerTransformer", ep.feature)
	cw.lit("TransformerEnd.endNumber", strconv.Itoa(ep.seq))
	cw.ref("TransformerEnd.Terminal", termID)
	for _, a := range attrs {
		if !strings.HasPrefix(a.CimAttribute, "PowerTransformerEnd.") &&
			!strings.HasPrefix(a.CimAttribute, "TransformerEnd.") {
			continue
		}
		raw, ok := ft.feat.Attributes[a.FieldName]
		if !ok || raw == nil {
			continue
		}
		if val, ok := applyLiteral(a, raw); ok {
			cw.lit(a.CimAttribute, val)
		}
	}
	if ep.seq == 1 {
		if v, ok := nominalVoltage(*ft.feat, attrs); ok {
			cw.lit("PowerTransformerEnd.ratedU", strconv.Itoa(v))
			cw.ref("TransformerEnd.BaseVoltage", uuidV5(fmt.Sprintf("basevoltage:%d", v)))
		}
	}
	cw.closeObj("PowerTransformerEnd")
}

// indexFeatures builds the id -> featTopo lookup over every conducting feature
// in the export (edges and junctions). Non-conducting kinds are skipped: they
// have no terminals to synthesize.
func (cw *cimWriter) indexFeatures(layers []Layer) map[string]*featTopo {
	feats := map[string]*featTopo{}
	for li := range layers {
		l := &layers[li]
		for fi := range l.Features {
			f := &l.Features[fi]
			cm, ok := cw.mapping.ClassFor(l.Code, f.SubtypeCode)
			if !ok {
				continue
			}
			if cm.CimKind != "conducting_edge" && cm.CimKind != "conducting_junction" {
				continue
			}
			feats[f.ID] = &featTopo{
				id:          f.ID,
				layer:       l,
				feat:        f,
				cim:         cm,
				subtypeCode: f.SubtypeCode,
				isEdge:      l.NetworkRole == "edge",
				pts:         pointsOf(f.Geometry),
			}
		}
	}
	return feats
}

// terminalSeq is the Terminal.sequenceNumber for a named terminal: its 1-based
// position in the feature's resolved (non-ground, code-ordered) terminal set.
// A terminal not in that set (e.g. a stale connection) falls back to 1.
func (cw *cimWriter) terminalSeq(ft *featTopo, termID string) int {
	terms := resolveTerminals(ft.layer.Terminals, ft.layer.Subtypes, ft.subtypeCode)
	for i, t := range terms {
		if t.ID == termID {
			return i + 1
		}
	}
	return 1
}

// resolveTerminals is a feature's expected CIM terminal set: its class terminals
// applicable to its subtype (subtype-scoped rows override the class-wide ones of
// the same code), excluding ground-role terminals, ordered by code (T1, T2, ...)
// so the sequence numbers are stable.
func resolveTerminals(terms []metadata.Terminal, subs []metadata.Subtype, subtypeCode *int) []metadata.Terminal {
	var subID *string
	if subtypeCode != nil {
		for i := range subs {
			if subs[i].Code == *subtypeCode {
				subID = &subs[i].ID
				break
			}
		}
	}
	byCode := map[string]metadata.Terminal{}
	for _, t := range terms {
		if t.Role == "ground" {
			continue
		}
		switch {
		case t.SubtypeID == nil:
			if _, ok := byCode[t.Code]; !ok {
				byCode[t.Code] = t
			}
		case subID != nil && *t.SubtypeID == *subID:
			byCode[t.Code] = t // subtype-specific overrides the class-wide row
		}
	}
	out := make([]metadata.Terminal, 0, len(byCode))
	for _, t := range byCode {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// groundTerminalIDs collects the ids of every ground-role terminal across the
// exported classes, so connections landing on one can be dropped.
func groundTerminalIDs(layers []Layer) map[string]bool {
	ground := map[string]bool{}
	for _, l := range layers {
		for _, t := range l.Terminals {
			if t.Role == "ground" {
				ground[t.ID] = true
			}
		}
	}
	return ground
}

// formatUnmapped renders topoStats.Unmapped as a stable, code-sorted
// "CODE=count" list for the runExport warning log (Phase 55 Slice 2). Empty
// string when nothing was dropped, so the caller can skip the line entirely.
func formatUnmapped(m map[string]int) string {
	if len(m) == 0 {
		return ""
	}
	codes := make([]string, 0, len(m))
	for code := range m {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	parts := make([]string, len(codes))
	for i, code := range codes {
		parts[i] = fmt.Sprintf("%s=%d", code, m[code])
	}
	return strings.Join(parts, " ")
}

func termKey(feature, terminalID string) string { return "T|" + feature + "|" + terminalID }
func edgeKey(feature string, idx int) string { return "E|" + feature + "|" + strconv.Itoa(idx) }

// dist2 is squared Euclidean distance in degrees -- enough to pick the nearer of
// a conductor's two ends (a comparison, never a real length).
func dist2(a, b coord) float64 {
	dx, dy := a.x-b.x, a.y-b.y
	return dx*dx + dy*dy
}

// nearestEndPair returns the (i, j) in {0,1}x{0,1} minimizing the distance
// between endpoint a[i] and b[j] -- the two conductor ends that meet, for a
// direct conductor-to-conductor connection.
func nearestEndPair(a, b [2]coord) (int, int) {
	bi, bj, best := 0, 0, dist2(a[0], b[0])
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			if d := dist2(a[i], b[j]); d < best {
				bi, bj, best = i, j, d
			}
		}
	}
	return bi, bj
}
