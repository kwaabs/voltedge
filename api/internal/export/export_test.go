package export

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// pointLayer is a one-feature Point layer used across the KML tests.
func pointLayer() Layer {
	return Layer{
		Code: "pole", Name: "Pole", GeometryType: "point",
		Features: []Feature{{
			ID:         "abc",
			Name:       "P1",
			Status:     "published",
			Geometry:   json.RawMessage(`{"type":"Point","coordinates":[-0.1,51.5]}`),
			Attributes: map[string]any{"voltage_class": "11kv", "pole_count": 3},
		}},
	}
}

func renderKML(t *testing.T, layers []Layer) string {
	t.Helper()
	var b bytes.Buffer
	if err := writeKML(&b, "VoltEdge export", layers); err != nil {
		t.Fatalf("writeKML: %v", err)
	}
	return b.String()
}

func TestWriteKMLPoint(t *testing.T) {
	out := renderKML(t, []Layer{pointLayer()})

	for _, want := range []string{
		"<Folder>",
		"<name>Pole</name>",
		"<Placemark>",
		"<name>P1</name>",
		`<Data name="status"><value>published</value></Data>`,
		`<Data name="voltage_class"><value>11kv</value></Data>`,
		`<Data name="pole_count"><value>3</value></Data>`,
		"<Point><coordinates>-0.1,51.5</coordinates></Point>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("KML missing %q\n---\n%s", want, out)
		}
	}
}

func TestWriteKMLGeometries(t *testing.T) {
	cases := []struct {
		name string
		geom string
		want string
	}{
		{
			"line",
			`{"type":"LineString","coordinates":[[0,0],[1,1]]}`,
			"<LineString><coordinates>0,0 1,1</coordinates></LineString>",
		},
		{
			"polygon with hole",
			`{"type":"Polygon","coordinates":[[[0,0],[4,0],[4,4],[0,0]],[[1,1],[2,1],[2,2],[1,1]]]}`,
			"<Polygon><outerBoundaryIs><LinearRing><coordinates>0,0 4,0 4,4 0,0</coordinates></LinearRing></outerBoundaryIs><innerBoundaryIs><LinearRing><coordinates>1,1 2,1 2,2 1,1</coordinates></LinearRing></innerBoundaryIs></Polygon>",
		},
		{
			"multipoint",
			`{"type":"MultiPoint","coordinates":[[0,0],[1,1]]}`,
			"<MultiGeometry><Point><coordinates>0,0</coordinates></Point><Point><coordinates>1,1</coordinates></Point></MultiGeometry>",
		},
		{
			"multipolygon",
			`{"type":"MultiPolygon","coordinates":[[[[0,0],[1,0],[1,1],[0,0]]]]}`,
			"<MultiGeometry><Polygon><outerBoundaryIs><LinearRing><coordinates>0,0 1,0 1,1 0,0</coordinates></LinearRing></outerBoundaryIs></Polygon></MultiGeometry>",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			layer := Layer{Code: "x", Name: "X", Features: []Feature{{Geometry: json.RawMessage(c.geom)}}}
			out := renderKML(t, []Layer{layer})
			if !strings.Contains(out, c.want) {
				t.Errorf("KML missing %q\n---\n%s", c.want, out)
			}
		})
	}
}

func TestWriteKMLSkipsFeatureWithoutGeometry(t *testing.T) {
	layer := Layer{Code: "x", Name: "X", Features: []Feature{{ID: "no-geom", Name: "ghost"}}}
	out := renderKML(t, []Layer{layer})
	if strings.Contains(out, "<Placemark>") {
		t.Errorf("a geometry-less feature should produce no Placemark\n%s", out)
	}
}

func TestWriteKMLRejectsUnknownGeometry(t *testing.T) {
	layer := Layer{Code: "x", Name: "X", Features: []Feature{{Geometry: json.RawMessage(`{"type":"Nope","coordinates":[]}`)}}}
	var b bytes.Buffer
	if err := writeKML(&b, "d", []Layer{layer}); err == nil {
		t.Fatal("want an error for an unsupported geometry type")
	}
}

func TestWriteKMZIsAZipWithDocKML(t *testing.T) {
	var b bytes.Buffer
	if err := writeKMZ(&b, "VoltEdge export", []Layer{pointLayer()}); err != nil {
		t.Fatalf("writeKMZ: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil {
		t.Fatalf("output is not a valid zip: %v", err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "doc.kml" {
		t.Fatalf("KMZ should contain exactly doc.kml, got %v", zr.File)
	}
	rc, _ := zr.File[0].Open()
	body, _ := io.ReadAll(rc)
	rc.Close()
	if !strings.Contains(string(body), "<Point><coordinates>-0.1,51.5</coordinates></Point>") {
		t.Errorf("doc.kml missing the point geometry:\n%s", body)
	}
}

func TestXMLEscape(t *testing.T) {
	got := xmlEscape(`a & b <c> "d" 'e'`)
	want := "a &amp; b &lt;c&gt; &quot;d&quot; &apos;e&apos;"
	if got != want {
		t.Errorf("xmlEscape = %q, want %q", got, want)
	}
}

func TestLayerAttributeKeys(t *testing.T) {
	l := Layer{Features: []Feature{
		{Attributes: map[string]any{"b": 1, "a": 2}},
		{Attributes: map[string]any{"c": 3, "a": 4}},
	}}
	if got := l.AttributeKeys(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("AttributeKeys = %v, want [a b c]", got)
	}
}

func TestFeatureFrom(t *testing.T) {
	row := map[string]any{
		"id":         "id-1",
		"name":       "N",
		"status":     "draft",
		"geometry":   map[string]any{"type": "Point", "coordinates": []any{1.0, 2.0}},
		"attributes": map[string]any{"k": "v"},
	}
	f, err := featureFrom(row)
	if err != nil {
		t.Fatalf("featureFrom: %v", err)
	}
	if f.ID != "id-1" || f.Name != "N" || f.Status != "draft" {
		t.Errorf("scalar fields wrong: %+v", f)
	}
	if len(f.Geometry) == 0 || !strings.Contains(string(f.Geometry), `"Point"`) {
		t.Errorf("geometry not re-encoded: %s", f.Geometry)
	}
	if f.Attributes["k"] != "v" {
		t.Errorf("attributes wrong: %v", f.Attributes)
	}
}

// --- Shapefile (Slice 2) ---------------------------------------------------

func TestShapeTypeFor(t *testing.T) {
	cases := map[string]struct {
		want int32
		ok   bool
	}{
		"point":   {shpPoint, true},
		"line":    {shpPolyLine, true},
		"polygon": {shpPolygon, true},
		"none":    {0, false},
		"weird":   {0, false},
	}
	for geom, want := range cases {
		got, ok := shapeTypeFor(geom)
		if got != want.want || ok != want.ok {
			t.Errorf("shapeTypeFor(%q) = (%d, %v), want (%d, %v)", geom, got, ok, want.want, want.ok)
		}
	}
}

// readShapefileZip unpacks writeShapefiles output into a name->bytes map.
func readShapefileZip(t *testing.T, layers []Layer) map[string][]byte {
	t.Helper()
	var b bytes.Buffer
	if err := writeShapefiles(&b, "VoltEdge export", layers); err != nil {
		t.Fatalf("writeShapefiles: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil {
		t.Fatalf("output is not a valid zip: %v", err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = data
	}
	return files
}

func TestWriteShapefilesBundlesFourFiles(t *testing.T) {
	files := readShapefileZip(t, []Layer{pointLayer()})
	for _, ext := range []string{".shp", ".shx", ".dbf", ".prj"} {
		if _, ok := files["pole"+ext]; !ok {
			names := make([]string, 0, len(files))
			for n := range files {
				names = append(names, n)
			}
			sort.Strings(names)
			t.Errorf("missing pole%s in bundle; got %v", ext, names)
		}
	}
	if !strings.Contains(string(files["pole.prj"]), "GCS_WGS_1984") {
		t.Errorf(".prj should be WGS84 WKT, got %q", files["pole.prj"])
	}
}

func TestShapefileHeaderAndPointRecord(t *testing.T) {
	files := readShapefileZip(t, []Layer{pointLayer()})
	shp := files["pole.shp"]
	if len(shp) < 100 {
		t.Fatalf("shp too short: %d bytes", len(shp))
	}
	if code := int32(binary.BigEndian.Uint32(shp[0:4])); code != 9994 {
		t.Errorf("file code = %d, want 9994", code)
	}
	if st := int32(binary.LittleEndian.Uint32(shp[32:36])); st != shpPoint {
		t.Errorf("header shape type = %d, want %d", st, shpPoint)
	}
	// File length in the header (words) must match the actual byte length.
	if words := int32(binary.BigEndian.Uint32(shp[24:28])); int(words)*2 != len(shp) {
		t.Errorf("header file length = %d words (%d bytes), actual %d bytes", words, words*2, len(shp))
	}
	// First record: number 1, then content shape type Point at offset 108.
	if recNum := int32(binary.BigEndian.Uint32(shp[100:104])); recNum != 1 {
		t.Errorf("first record number = %d, want 1", recNum)
	}
	if st := int32(binary.LittleEndian.Uint32(shp[108:112])); st != shpPoint {
		t.Errorf("record shape type = %d, want %d", st, shpPoint)
	}
	x := math.Float64frombits(binary.LittleEndian.Uint64(shp[112:120]))
	y := math.Float64frombits(binary.LittleEndian.Uint64(shp[120:128]))
	if x != -0.1 || y != 51.5 {
		t.Errorf("point coords = (%v, %v), want (-0.1, 51.5)", x, y)
	}
}

func TestShapefilePolygonOrientation(t *testing.T) {
	// GeoJSON outer ring given counter-clockwise; the shapefile requires the
	// outer ring clockwise, so orientPolygon must reverse it.
	rings := [][][]float64{{{0, 0}, {4, 0}, {4, 4}, {0, 4}, {0, 0}}}
	out := orientPolygon(rings)
	if len(out) != 1 {
		t.Fatalf("want 1 ring, got %d", len(out))
	}
	if a := signedArea(out[0]); a >= 0 {
		t.Errorf("outer ring signed area = %v, want negative (clockwise)", a)
	}
	// A hole (second ring) must come out counter-clockwise (positive area).
	withHole := [][][]float64{
		{{0, 0}, {4, 0}, {4, 4}, {0, 4}, {0, 0}},
		{{1, 1}, {1, 2}, {2, 2}, {2, 1}, {1, 1}},
	}
	oh := orientPolygon(withHole)
	if signedArea(oh[0]) >= 0 {
		t.Errorf("outer ring not clockwise: %v", signedArea(oh[0]))
	}
	if signedArea(oh[1]) <= 0 {
		t.Errorf("hole ring not counter-clockwise: %v", signedArea(oh[1]))
	}
}

func TestCloseRing(t *testing.T) {
	open := [][2]float64{{0, 0}, {1, 0}, {1, 1}}
	closed := closeRing(open)
	if closed[0] != closed[len(closed)-1] {
		t.Errorf("ring not closed: %v", closed)
	}
	// Already-closed ring is left as-is.
	already := [][2]float64{{0, 0}, {1, 0}, {0, 0}}
	if got := closeRing(already); len(got) != 3 {
		t.Errorf("already-closed ring changed length: %v", got)
	}
}

func TestDBFFieldNameTruncateAndUnique(t *testing.T) {
	used := map[string]bool{}
	if n := dbfFieldName("short", used); n != "short" {
		t.Errorf("short name = %q, want short", n)
	}
	if n := dbfFieldName("this_is_a_very_long_field", used); n != "this_is_a_" {
		t.Errorf("long name = %q, want this_is_a_ (10 chars)", n)
	}
	// A second field truncating to the same 10 chars must be de-duplicated.
	if n := dbfFieldName("this_is_a_second_long_one", used); n == "this_is_a_" {
		t.Errorf("collision not de-duplicated: %q", n)
	}
}

func TestDBFRecordShape(t *testing.T) {
	layer := Layer{
		Code: "pole", Name: "Pole", GeometryType: "point",
		Fields: []AttributeField{
			{Name: "pole_count", DataType: "integer"},
			{Name: "energized", DataType: "boolean"},
		},
		Features: []Feature{
			{Name: "P1", Status: "published", Attributes: map[string]any{"pole_count": int64(3), "energized": true}},
			{Name: "P2", Status: "draft", Attributes: map[string]any{"pole_count": int64(12), "energized": false}},
		},
	}
	dbf, err := buildDBF(layer)
	if err != nil {
		t.Fatalf("buildDBF: %v", err)
	}
	if dbf[0] != 0x03 {
		t.Errorf("dbf version byte = %#x, want 0x03", dbf[0])
	}
	recCount := int32(binary.LittleEndian.Uint32(dbf[4:8]))
	if recCount != 2 {
		t.Errorf("record count = %d, want 2", recCount)
	}
	headerLen := int(binary.LittleEndian.Uint16(dbf[8:10]))
	recordLen := int(binary.LittleEndian.Uint16(dbf[10:12]))
	// name(C) + status(C) + pole_count(N) + energized(L) = 4 fields.
	if want := 32 + 32*4 + 1; headerLen != want {
		t.Errorf("header length = %d, want %d", headerLen, want)
	}
	// Trailer: header + recCount*recordLen + 0x1A EOF.
	if want := headerLen + int(recCount)*recordLen + 1; len(dbf) != want {
		t.Errorf("dbf length = %d, want %d", len(dbf), want)
	}
	if dbf[len(dbf)-1] != 0x1A {
		t.Errorf("missing 0x1A EOF marker")
	}
}

func TestToFloat(t *testing.T) {
	cases := []struct {
		in   any
		want float64
		ok   bool
	}{
		{int64(3), 3, true},
		{3.5, 3.5, true},
		{"4.25", 4.25, true},
		{[]byte("5"), 5, true},
		{nil, 0, false},
		{"not a number", 0, false},
	}
	for _, c := range cases {
		got, ok := toFloat(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("toFloat(%v) = (%v, %v), want (%v, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestPadNOverflow(t *testing.T) {
	if got := padN("123456", 4); got != "****" {
		t.Errorf("padN overflow = %q, want ****", got)
	}
	if got := padN("12", 4); got != "  12" {
		t.Errorf("padN = %q, want '  12'", got)
	}
	if got := padN("", 3); got != "   " {
		t.Errorf("padN empty = %q, want 3 spaces", got)
	}
}

// --- DXF (Slice 3) ---------------------------------------------------------

func renderDXF(t *testing.T, layers []Layer) string {
	t.Helper()
	var b bytes.Buffer
	if err := writeDXF(&b, "VoltEdge export", layers); err != nil {
		t.Fatalf("writeDXF: %v", err)
	}
	return b.String()
}

func TestWriteDXFPointStructure(t *testing.T) {
	out := renderDXF(t, []Layer{pointLayer()})
	for _, want := range []string{
		"0\nSECTION\n2\nHEADER\n",
		"9\n$ACADVER\n1\nAC1015\n",
		"2\nTABLES\n",
		"2\nAPPID\n",         // APPID table
		"2\nVOLTEDGE\n",      // registered app
		"0\nLAYER\n2\npole\n", // a layer named for the class
		"2\nENTITIES\n",
		"0\nPOINT\n8\npole\n", // point on the pole layer
		"10\n-0.1\n",
		"20\n51.5\n",
		"1001\nVOLTEDGE\n",       // XDATA app
		"1000\nname=P1\n",        // XDATA fields
		"1000\nstatus=published\n",
		"1000\nvoltage_class=11kv\n",
		"1000\npole_count=3\n",
		"0\nEOF\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("DXF missing %q\n---\n%s", want, out)
		}
	}
}

func TestWriteDXFLineIsOpenPolyline(t *testing.T) {
	layer := Layer{Code: "l", Name: "L", GeometryType: "line", Features: []Feature{{
		Geometry: json.RawMessage(`{"type":"LineString","coordinates":[[0,0],[1,1],[2,3]]}`),
	}}}
	out := renderDXF(t, []Layer{layer})
	if !strings.Contains(out, "0\nLWPOLYLINE\n8\nl\n90\n3\n70\n0\n") {
		t.Errorf("line should be an open 3-vertex LWPOLYLINE\n%s", out)
	}
}

func TestWriteDXFPolygonClosesAndDropsDuplicate(t *testing.T) {
	// GeoJSON ring repeats the first point as the last; DXF should drop it and
	// set the closed flag, leaving 3 vertices.
	layer := Layer{Code: "p", Name: "P", GeometryType: "polygon", Features: []Feature{{
		Geometry: json.RawMessage(`{"type":"Polygon","coordinates":[[[0,0],[4,0],[4,4],[0,0]]]}`),
	}}}
	out := renderDXF(t, []Layer{layer})
	if !strings.Contains(out, "0\nLWPOLYLINE\n8\np\n90\n3\n70\n1\n") {
		t.Errorf("polygon ring should be a closed 3-vertex LWPOLYLINE\n%s", out)
	}
}

func TestWriteDXFRejectsUnknownGeometry(t *testing.T) {
	layer := Layer{Code: "x", Name: "X", Features: []Feature{{
		Geometry: json.RawMessage(`{"type":"Nope","coordinates":[]}`),
	}}}
	var b bytes.Buffer
	if err := writeDXF(&b, "d", []Layer{layer}); err == nil {
		t.Fatal("want an error for an unsupported geometry type")
	}
}

func TestWriteDXFSkipsGeometryless(t *testing.T) {
	layer := Layer{Code: "x", Name: "X", Features: []Feature{{ID: "ghost", Name: "g"}}}
	out := renderDXF(t, []Layer{layer})
	if strings.Contains(out, "0\nPOINT\n") || strings.Contains(out, "LWPOLYLINE") {
		t.Errorf("geometry-less feature should emit no entity\n%s", out)
	}
}

func TestDXFXFieldCleanAndClip(t *testing.T) {
	// Newlines are flattened to spaces so they can't corrupt the pair stream.
	if got := dxfXField("k", "a\nb\rc"); got != "k=a b c" {
		t.Errorf("dxfXField clean = %q, want 'k=a b c'", got)
	}
	// Values are clipped to the 255-byte XDATA limit.
	long := dxfXField("k", strings.Repeat("x", 400))
	if len(long) != dxfXDataMax {
		t.Errorf("dxfXField length = %d, want %d", len(long), dxfXDataMax)
	}
}

func TestParseBBox(t *testing.T) {
	if b, err := parseBBox(nil); err != nil || b != nil {
		t.Errorf("nil bbox should be (nil, nil), got (%v, %v)", b, err)
	}
	if _, err := parseBBox([]float64{1, 2, 3}); err == nil {
		t.Error("a 3-element bbox should error")
	}
	// Swapped min/max is normalized rather than rejected.
	b, err := parseBBox([]float64{4, 40, 1, 10})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.MinX != 1 || b.MinY != 10 || b.MaxX != 4 || b.MaxY != 40 {
		t.Errorf("bbox not normalized: %+v", b)
	}
}
