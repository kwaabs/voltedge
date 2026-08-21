package export

// CIM/XML (RDF) encoder -- Phase 16, docs/phase-16-cim-export.md. Unlike the
// KMZ/SHP/DXF encoders (geometry containers), this emits a graph of typed CIM
// electrical objects (ACLineSegment, PowerTransformer, Breaker, ...), each an
// RDF resource with a UUID mRID. The class/attribute/profile mapping is
// declarative metadata (metadata.cim_*); this file is the behavioral half that
// the mapping can't express: mRID/UUIDv5 derivation, geometry -> GL
// Location/PositionPoint, substation containment, and RDF/XML serialization.
//
// Slice 1 built the equipment + geometry + containment skeleton. Slice 2
// (topology.go) wires it together: ConnectivityNodes synthesized from
// runtime.feature_connectivity, one CIM Terminal per equipment endpoint, and
// PowerTransformerEnds for transformer windings.

import (
	"bufio"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/kwaabs/voltedge/api/internal/metadata"
)

const (
	rdfNS = "http://www.w3.org/1999/02/22-rdf-syntax-ns#"
	mdNS  = "http://iec.ch/TC57/61970-552/ModelDescription/1#"
)

// cimUUIDNamespace is the fixed namespace UUID that seeds every UUIDv5 this
// encoder synthesizes (BaseVoltage, Location, PositionPoint, the region/CS
// scaffold, the default container). A stable namespace means a given input
// always yields the same synthesized mRIDs, so re-exports diff cleanly. It is
// an arbitrary constant -- not derived from anything, just fixed forever.
var cimUUIDNamespace = [16]byte{
	0x9f, 0x1a, 0x4c, 0x7e, 0x3b, 0x62, 0x54, 0xd8,
	0xa1, 0x0c, 0x77, 0x2e, 0x6b, 0x90, 0x11, 0x3d,
}

// coord is one lon/lat point (EPSG:4326, GeoJSON axis order). x = longitude ->
// CIM PositionPoint.xPosition; y = latitude -> yPosition.
type coord struct{ x, y float64 }

// cimSubstation is a SUBSTATION_YARD feature promoted to a CIM Substation: its
// mRID (the yard's own feature id) and its outer ring, against which equipment
// points are tested for point-in-polygon containment.
type cimSubstation struct {
	mRID string
	ring []coord
}

// writeCIM streams a single combined CIM RDF/XML document of every layer to w,
// using the resolved declarative mapping. generated stamps the md:FullModel
// header (passed in rather than read from the clock so the output is testable).
func writeCIM(w io.Writer, docName string, layers []Layer, mapping *metadata.CIMMapping, conns []connRow, specs map[string]*metadata.MaterialSpec, generated time.Time) error {
	cw := &cimWriter{
		mapping:   mapping,
		specs:     specs,
		generated: generated,
		geoID:     uuidV5("geographical-region"),
		subID:     uuidV5("sub-geographical-region"),
		csID:      uuidV5("coordinate-system"),
		lineID:    uuidV5("default-line-container"),
	}
	return cw.write(w, docName, layers, conns)
}

// cimBuf adapts the buffered destination writer to the strings.Builder-style
// API every emit helper already uses (Write for fmt.Fprintf, WriteString for
// the direct literal appends). Slice 1 (docs/phase-17-large-export.md) retired
// the whole-document strings.Builder -- which held the entire serialized RDF/XML
// in memory before a single byte was flushed, an OOM at 10M features -- for a
// bufio.Writer that streams straight to the response. Wrapping it this way lets
// the encoder stream at constant memory without touching every `&cw.b` call
// site across cim.go/topology.go/electrical.go/phasing.go. Write errors are
// sticky on the underlying bufio.Writer, so the helpers keep ignoring them and
// the single Flush at the end of write() surfaces the first failure.
type cimBuf struct{ w io.Writer }

func (c *cimBuf) Write(p []byte) (int, error)       { return c.w.Write(p) }
func (c *cimBuf) WriteString(s string) (int, error) { return io.WriteString(c.w, s) }

type cimWriter struct {
	b         cimBuf
	mapping   *metadata.CIMMapping
	specs     map[string]*metadata.MaterialSpec // conductor spec code -> material spec (Slice 3 impedance)
	generated time.Time

	geoID  string // GeographicalRegion mRID
	subID  string // SubGeographicalRegion mRID
	csID   string // CoordinateSystem mRID
	lineID string // default Line container mRID (equipment with no substation)

	substations []cimSubstation
	subByID     map[string]bool // yard feature ids, to skip in the equipment pass
}

func (cw *cimWriter) write(w io.Writer, docName string, layers []Layer, conns []connRow) error {
	// Stream straight to the destination through a buffered writer instead of
	// accumulating the whole document in memory (Slice 1). Every emit helper
	// writes through cw.b; the lone Flush below surfaces the first write error.
	bw := bufio.NewWriter(w)
	cw.b = cimBuf{w: bw}
	profile := cw.mapping.Profile

	cw.b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	fmt.Fprintf(&cw.b, "<rdf:RDF xmlns:rdf=%q xmlns:cim=%q xmlns:md=%q>\n",
		rdfNS, profile.CimNamespace, mdNS)

	cw.writeHeader(docName)
	cw.writeScaffold(profile)

	// Pass 1: promote SUBSTATION_YARD features to Substations (containment
	// parents) and record their rings for the point-in-polygon test below.
	cw.collectSubstations(layers)
	for _, layer := range layers {
		for _, f := range layer.Features {
			cm, ok := cw.mapping.ClassFor(layer.Code, f.SubtypeCode)
			if !ok || cm.CimClass != "Substation" {
				continue
			}
			cw.writeContainerObject(cm.CimClass, f, "Substation.Region", cw.subID)
		}
	}

	// Pass 2: distinct BaseVoltages across every feature, emitted once and
	// referenced by mRID from the equipment.
	cw.writeBaseVoltages(layers)

	// Pass 3: the equipment/asset/protection objects themselves.
	for _, layer := range layers {
		attrs := cw.mapping.AttributesFor(layer.Code)
		for _, f := range layer.Features {
			cm, ok := cw.mapping.ClassFor(layer.Code, f.SubtypeCode)
			if !ok || cm.CimKind == "skip" {
				continue
			}
			if cw.subByID[f.ID] {
				continue // already emitted as a Substation in pass 1
			}
			cw.writeFeature(cm, f, attrs)
		}
	}

	// Pass 4 (Slice 2): synthesize the topology -- ConnectivityNodes from the
	// connectivity graph, one Terminal per equipment endpoint, PowerTransformerEnds.
	cw.writeTopology(layers, conns)

	cw.b.WriteString("</rdf:RDF>\n")
	return bw.Flush()
}

// writeHeader emits the md:FullModel model-description header the CIM/XML
// document is identified and profiled by.
func (cw *cimWriter) writeHeader(docName string) {
	profile := cw.mapping.Profile
	modelID := uuidV5("model:" + docName + ":" + cw.generated.Format(time.RFC3339))
	fmt.Fprintf(&cw.b, "  <md:FullModel rdf:about=\"urn:uuid:%s\">\n", modelID)
	fmt.Fprintf(&cw.b, "    <md:Model.created>%s</md:Model.created>\n", cw.generated.UTC().Format(time.RFC3339))
	if docName != "" {
		fmt.Fprintf(&cw.b, "    <md:Model.description>%s</md:Model.description>\n", xmlEscape(docName))
	}
	fmt.Fprintf(&cw.b, "    <md:Model.modelingAuthoritySet>%s</md:Model.modelingAuthoritySet>\n", xmlEscape(profile.ModelAuthority))
	fmt.Fprintf(&cw.b, "    <md:Model.profile>%s</md:Model.profile>\n", xmlEscape(profile.ProfileURI))
	cw.b.WriteString("  </md:FullModel>\n")
}

// writeScaffold emits the containment/coordinate scaffold every export shares:
// a GeographicalRegion + SubGeographicalRegion (the root of substation
// containment), the CoordinateSystem the GL PositionPoints reference, and the
// default Line container that holds equipment falling outside every substation.
func (cw *cimWriter) writeScaffold(profile *metadata.CimProfile) {
	cw.openObj("GeographicalRegion", cw.geoID)
	cw.idName("VoltEdge")
	cw.mRID(cw.geoID)
	cw.closeObj("GeographicalRegion")

	cw.openObj("SubGeographicalRegion", cw.subID)
	cw.idName("VoltEdge")
	cw.mRID(cw.subID)
	cw.ref("SubGeographicalRegion.Region", cw.geoID)
	cw.closeObj("SubGeographicalRegion")

	cw.openObj("CoordinateSystem", cw.csID)
	cw.idName(profile.CoordinateCRS)
	cw.mRID(cw.csID)
	cw.lit("CoordinateSystem.crsUrn", profile.CoordinateCRS)
	cw.closeObj("CoordinateSystem")

	cw.openObj("Line", cw.lineID)
	cw.idName("Unassigned equipment")
	cw.mRID(cw.lineID)
	cw.ref("Line.Region", cw.subID)
	cw.closeObj("Line")
}

// collectSubstations records each SUBSTATION_YARD feature's outer ring for the
// point-in-polygon containment test. A yard with no polygon geometry still
// becomes a Substation (emitted in pass 1) but contains nothing.
func (cw *cimWriter) collectSubstations(layers []Layer) {
	cw.subByID = map[string]bool{}
	for _, layer := range layers {
		for _, f := range layer.Features {
			cm, ok := cw.mapping.ClassFor(layer.Code, f.SubtypeCode)
			if !ok || cm.CimClass != "Substation" {
				continue
			}
			cw.subByID[f.ID] = true
			ring := outerRingOf(f.Geometry)
			cw.substations = append(cw.substations, cimSubstation{mRID: f.ID, ring: ring})
		}
	}
}

// writeBaseVoltages emits one cim:BaseVoltage per distinct nominal voltage
// resolved across every feature. Equipment references these by the same
// deterministic mRID (uuidV5("basevoltage:<v>")) writeFeature recomputes.
func (cw *cimWriter) writeBaseVoltages(layers []Layer) {
	seen := map[int]bool{}
	var volts []int
	for _, layer := range layers {
		attrs := cw.mapping.AttributesFor(layer.Code)
		for _, f := range layer.Features {
			if v, ok := nominalVoltage(f, attrs); ok && !seen[v] {
				seen[v] = true
				volts = append(volts, v)
			}
		}
	}
	sort.Ints(volts)
	for _, v := range volts {
		id := uuidV5(fmt.Sprintf("basevoltage:%d", v))
		cw.openObj("BaseVoltage", id)
		cw.idName(fmt.Sprintf("%d V", v))
		cw.mRID(id)
		cw.lit("BaseVoltage.nominalVoltage", strconv.Itoa(v))
		cw.closeObj("BaseVoltage")
	}
}

// writeContainerObject emits a container feature (Substation, or a generic
// EquipmentContainer) with its region link and, if it has geometry, its GL
// Location. mRID is the feature's own id.
func (cw *cimWriter) writeContainerObject(cimClass string, f Feature, regionPred, regionID string) {
	locID := ""
	if pts := pointsOf(f.Geometry); len(pts) > 0 {
		locID = uuidV5("location:" + f.ID)
	}
	cw.openObj(cimClass, f.ID)
	cw.idName(f.Name)
	cw.mRID(f.ID)
	if regionPred != "" {
		cw.ref(regionPred, regionID)
	}
	if locID != "" {
		cw.ref("PowerSystemResource.Location", locID)
	}
	cw.closeObj(cimClass)
	if locID != "" {
		cw.writeLocation(locID, pointsOf(f.Geometry))
	}
}

// writeFeature emits one non-container feature per its cim_kind: equipment
// (conducting_edge/junction) gets an EquipmentContainer, a BaseVoltage (when
// resolved), a Location, and any literal attributes; an asset gets name + mRID
// + Location; protection gets name + mRID only (it has no map geometry).
func (cw *cimWriter) writeFeature(cm metadata.CimClassMapping, f Feature, attrs []metadata.CimAttributeMapping) {
	switch cm.CimKind {
	case "container":
		// A non-Substation container (e.g. METER_CHAMBER -> EquipmentContainer):
		// emitted, but not used as a containment parent in Slice 1 (containment
		// is SUBSTATION_YARD-only), so no region link.
		cw.writeContainerObject(cm.CimClass, f, "", "")
		return
	case "asset":
		locID := ""
		if pts := pointsOf(f.Geometry); len(pts) > 0 {
			locID = uuidV5("location:" + f.ID)
		}
		cw.openObj(cm.CimClass, f.ID)
		cw.idName(f.Name)
		cw.mRID(f.ID)
		if locID != "" {
			cw.ref("PowerSystemResource.Location", locID)
		}
		cw.closeObj(cm.CimClass)
		if locID != "" {
			cw.writeLocation(locID, pointsOf(f.Geometry))
		}
		return
	case "protection":
		cw.openObj(cm.CimClass, f.ID)
		cw.idName(f.Name)
		cw.mRID(f.ID)
		cw.closeObj(cm.CimClass)
		return
	}

	// conducting_edge / conducting_junction -> Equipment.
	pts := pointsOf(f.Geometry)
	locID := ""
	if len(pts) > 0 {
		locID = uuidV5("location:" + f.ID)
	}
	containerID := cw.lineID
	if len(pts) > 0 {
		if sub, ok := cw.substationFor(pts[0]); ok {
			containerID = sub
		}
	}

	cw.openObj(cm.CimClass, f.ID)
	cw.idName(f.Name)
	cw.mRID(f.ID)
	cw.ref("Equipment.EquipmentContainer", containerID)
	// A PowerTransformer carries voltage on its PowerTransformerEnds (built in
	// the topology pass), not on the transformer itself -- so it gets no
	// ConductingEquipment.BaseVoltage (Slice 3 corrects the Slice-1 placeholder).
	if cm.CimClass != "PowerTransformer" {
		if v, ok := nominalVoltage(f, attrs); ok {
			cw.ref("ConductingEquipment.BaseVoltage", uuidV5(fmt.Sprintf("basevoltage:%d", v)))
		}
	}
	if locID != "" {
		cw.ref("PowerSystemResource.Location", locID)
	}
	for _, kv := range literalAttributes(f, attrs) {
		cw.lit(kv[0], kv[1])
	}
	// Slice 3: a conductor's length (from geometry) and series impedance (from
	// its material spec) -- emitted as literals inside the ACLineSegment.
	cw.writeConductorDetail(cm, f, attrs, pts)
	cw.closeObj(cm.CimClass)
	if locID != "" {
		cw.writeLocation(locID, pts)
	}
	// Slice 4: per-phase objects (ACLineSegmentPhase / EnergyConsumerPhase) are
	// siblings of the equipment, so they're emitted after it closes.
	cw.writePhases(cm, f, attrs)
}

// substationFor returns the mRID of the first substation whose ring contains p.
func (cw *cimWriter) substationFor(p coord) (string, bool) {
	for _, s := range cw.substations {
		if len(s.ring) >= 3 && pointInRing(p, s.ring) {
			return s.mRID, true
		}
	}
	return "", false
}

// writeLocation emits a GL Location plus one PositionPoint per ordered vertex.
func (cw *cimWriter) writeLocation(locID string, pts []coord) {
	cw.openObj("Location", locID)
	cw.mRID(locID)
	cw.ref("Location.CoordinateSystem", cw.csID)
	cw.closeObj("Location")
	for i, p := range pts {
		ppID := uuidV5(fmt.Sprintf("positionpoint:%s:%d", locID, i+1))
		cw.openObj("PositionPoint", ppID)
		cw.ref("PositionPoint.Location", locID)
		cw.lit("PositionPoint.sequenceNumber", strconv.Itoa(i+1))
		cw.lit("PositionPoint.xPosition", formatFloat(p.x))
		cw.lit("PositionPoint.yPosition", formatFloat(p.y))
		cw.closeObj("PositionPoint")
	}
}

// --- small RDF/XML emit helpers (an object's rdf:ID and every reference use
// the "_" + mRID form CDPSM conventionally writes) ---

func (cw *cimWriter) openObj(class, id string) {
	fmt.Fprintf(&cw.b, "  <cim:%s rdf:ID=\"_%s\">\n", class, id)
}

func (cw *cimWriter) closeObj(class string) {
	fmt.Fprintf(&cw.b, "  </cim:%s>\n", class)
}

func (cw *cimWriter) idName(name string) {
	if name == "" {
		return
	}
	fmt.Fprintf(&cw.b, "    <cim:IdentifiedObject.name>%s</cim:IdentifiedObject.name>\n", xmlEscape(name))
}

func (cw *cimWriter) mRID(id string) {
	fmt.Fprintf(&cw.b, "    <cim:IdentifiedObject.mRID>%s</cim:IdentifiedObject.mRID>\n", id)
}

func (cw *cimWriter) ref(predicate, targetID string) {
	fmt.Fprintf(&cw.b, "    <cim:%s rdf:resource=\"#_%s\"/>\n", predicate, targetID)
}

// enumRef references a CIM enumeration literal (e.g. SinglePhaseKind.A) rather
// than another object: the resource is an absolute URI into the CIM namespace,
// not a local "#_mRID". Used for phase codes (Slice 4).
func (cw *cimWriter) enumRef(predicate, enumValue string) {
	fmt.Fprintf(&cw.b, "    <cim:%s rdf:resource=\"%s%s\"/>\n",
		predicate, cw.mapping.Profile.CimNamespace, enumValue)
}

func (cw *cimWriter) lit(predicate, value string) {
	fmt.Fprintf(&cw.b, "    <cim:%s>%s</cim:%s>\n", predicate, xmlEscape(value), predicate)
}

// uuidV5 is RFC 4122 name-based UUIDv5 (SHA-1) under cimUUIDNamespace -- the
// deterministic id for every synthesized object. No external uuid dependency:
// SHA-1 the namespace+name, then stamp the version (5) and variant bits.
func uuidV5(name string) string {
	h := sha1.New()
	h.Write(cimUUIDNamespace[:])
	io.WriteString(h, name)
	sum := h.Sum(nil)
	var u [16]byte
	copy(u[:], sum[:16])
	u[6] = (u[6] & 0x0f) | 0x50 // version 5
	u[8] = (u[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

// --- attribute coercion (the transforms Slice 1 can serialize) ---

// nominalVoltage resolves a feature's BaseVoltage.nominalVoltage (in volts)
// from the first attribute mapping targeting it. domain_map/direct/scale are
// the transforms that can yield a voltage in Slice 1.
func nominalVoltage(f Feature, attrs []metadata.CimAttributeMapping) (int, bool) {
	for _, a := range attrs {
		if a.CimAttribute != "BaseVoltage.nominalVoltage" {
			continue
		}
		raw, ok := f.Attributes[a.FieldName]
		if !ok || raw == nil {
			continue
		}
		switch a.Transform {
		case "domain_map":
			if mv, ok := a.ValueMap[fmt.Sprintf("%v", raw)]; ok {
				if n, ok := toInt(mv); ok {
					return n, true
				}
			}
		case "direct":
			if n, ok := toInt(raw); ok {
				return n, true
			}
		case "scale":
			if fv, ok := toFloat(raw); ok && a.ScaleFactor != nil {
				return int(fv * *a.ScaleFactor), true
			}
		}
	}
	return 0, false
}

// literalAttributes returns the (cimAttribute, value) pairs to emit as literal
// child elements on a feature, sorted for deterministic output. It skips
// BaseVoltage.nominalVoltage (handled via the shared BaseVoltage object) and
// the transforms that emit their own objects rather than a literal on the
// equipment (from_geometry, material_spec, phase -- Slices 3/4).
func literalAttributes(f Feature, attrs []metadata.CimAttributeMapping) [][2]string {
	var out [][2]string
	for _, a := range attrs {
		if a.CimAttribute == "BaseVoltage.nominalVoltage" {
			continue
		}
		if a.Transform == "material_spec" || a.Transform == "from_geometry" || a.Transform == "phase" {
			continue
		}
		raw, ok := f.Attributes[a.FieldName]
		if !ok || raw == nil {
			continue
		}
		if val, ok := applyLiteral(a, raw); ok {
			out = append(out, [2]string{a.CimAttribute, val})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// applyLiteral coerces one field value to its CIM literal string per transform.
func applyLiteral(a metadata.CimAttributeMapping, raw any) (string, bool) {
	switch a.Transform {
	case "direct":
		return literalString(raw), true
	case "scale":
		fv, ok := toFloat(raw)
		if !ok || a.ScaleFactor == nil {
			return "", false
		}
		return formatFloat(fv * *a.ScaleFactor), true
	case "domain_map":
		mv, ok := a.ValueMap[fmt.Sprintf("%v", raw)]
		if !ok {
			return "", false
		}
		return literalString(mv), true
	case "boolean":
		mv, ok := a.ValueMap[fmt.Sprintf("%v", raw)]
		if !ok {
			return "", false
		}
		bv, ok := mv.(bool)
		if !ok {
			return "", false
		}
		if bv {
			return "true", true
		}
		return "false", true
	}
	return "", false
}

func literalString(v any) string {
	switch n := v.(type) {
	case bool:
		if n {
			return "true"
		}
		return "false"
	case float64:
		return formatFloat(n)
	case string:
		return n
	default:
		return fmt.Sprintf("%v", v)
	}
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
	}
	return 0, false
}

// toFloat lives in shapefile.go (shared across the encoders) -- it handles the
// same numeric types plus the textual forms the DBF path needs.

// --- geometry helpers (GeoJSON, EPSG:4326) ---

// pointsOf returns every vertex of a geometry in document order, flattened
// across the Multi*/Polygon-ring nesting. Slice 1 uses this both for the GL
// PositionPoint sequence and (its first element) as a feature's representative
// point for substation containment.
func pointsOf(raw json.RawMessage) []coord {
	typ, coordsRaw, ok := geomEnvelope(raw)
	if !ok || typ == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal(coordsRaw, &v); err != nil {
		return nil
	}
	var out []coord
	walkCoords(v, &out)
	return out
}

// walkCoords descends a decoded GeoJSON coordinates value, appending each
// [x, y(, z...)] tuple it finds. A tuple is recognized as an array whose first
// two elements are numbers; anything else is a container to recurse into.
func walkCoords(v any, out *[]coord) {
	arr, ok := v.([]any)
	if !ok {
		return
	}
	if len(arr) >= 2 {
		if x, ok1 := arr[0].(float64); ok1 {
			if y, ok2 := arr[1].(float64); ok2 {
				*out = append(*out, coord{x: x, y: y})
				return
			}
		}
	}
	for _, e := range arr {
		walkCoords(e, out)
	}
}

// outerRingOf returns the outer ring of a Polygon/MultiPolygon (the first ring
// of the first polygon), for the point-in-polygon containment test. Non-areal
// geometries yield nil.
func outerRingOf(raw json.RawMessage) []coord {
	typ, coordsRaw, ok := geomEnvelope(raw)
	if !ok {
		return nil
	}
	switch typ {
	case "Polygon":
		var rings [][][]float64
		if err := json.Unmarshal(coordsRaw, &rings); err != nil || len(rings) == 0 {
			return nil
		}
		return toCoords(rings[0])
	case "MultiPolygon":
		var polys [][][][]float64
		if err := json.Unmarshal(coordsRaw, &polys); err != nil || len(polys) == 0 || len(polys[0]) == 0 {
			return nil
		}
		return toCoords(polys[0][0])
	}
	return nil
}

func toCoords(ring [][]float64) []coord {
	out := make([]coord, 0, len(ring))
	for _, c := range ring {
		if len(c) >= 2 {
			out = append(out, coord{x: c[0], y: c[1]})
		}
	}
	return out
}

func geomEnvelope(raw json.RawMessage) (string, json.RawMessage, bool) {
	if len(raw) == 0 {
		return "", nil, false
	}
	var g struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return "", nil, false
	}
	return g.Type, g.Coordinates, true
}

// pointInRing is the standard even-odd ray-cast: is p inside the polygon ring?
// Points exactly on an edge are not guaranteed either way (acceptable -- a
// substation boundary is drawn around its equipment, not through it).
func pointInRing(p coord, ring []coord) bool {
	inside := false
	n := len(ring)
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		yi, yj := ring[i].y, ring[j].y
		if (yi > p.y) != (yj > p.y) {
			xcross := (ring[j].x-ring[i].x)*(p.y-yi)/(yj-yi) + ring[i].x
			if p.x < xcross {
				inside = !inside
			}
		}
	}
	return inside
}
