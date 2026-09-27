package export

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// This file hand-rolls the ESRI Shapefile format (the .shp/.shx/.dbf/.prj
// quartet) against the stdlib -- there is no shapefile dependency. The format
// is old but fully specified (ESRI's "ESRI Shapefile Technical Description",
// July 1998) and small enough to emit directly. Two hard constraints shape the
// design: a single .shp holds exactly one geometry type (so each feature class
// becomes its own shapefile, all bundled in one zip), and DBF field names are
// capped at 10 characters (so attribute names are truncated and de-duplicated).
// The .prj carries the CRS: by default EPSG:4326 (the stored SRID), but when the
// export reprojection engine reprojects to a target SRID the caller passes that
// SRID's WKT through instead, so a projected export is correctly labelled. See
// docs/phase-14-export.md.

// Shape type codes (ESRI spec, 2D variants only -- we never emit Z/M).
const (
	shpNull     int32 = 0
	shpPoint    int32 = 1
	shpPolyLine int32 = 3
	shpPolygon  int32 = 5
)

// wgs84WKT is the ESRI-flavoured WKT for EPSG:4326, written into every layer's
// .prj when no reprojection target is set (the stored SRID), so GIS tools pick
// up the CRS.
const wgs84WKT = `GEOGCS["GCS_WGS_1984",DATUM["D_WGS_1984",SPHEROID["WGS_1984",6378137,298.257223563]],PRIMEM["Greenwich",0],UNIT["Degree",0.017453292519943295]]`

// writeShapefiles streams a zip containing one shapefile (four files) per
// layer. prjWKT is the CRS WKT stamped into every layer's .prj -- wgs84WKT for a
// non-reprojected export, or the target SRID's WKT when reprojecting. A layer
// whose geometry type can't be mapped to a shape type is an error rather than a
// silently-dropped layer.
func writeShapefiles(w io.Writer, _ string, layers []Layer, prjWKT string) error {
	zw := zip.NewWriter(w)
	for _, layer := range layers {
		if err := writeLayerShapefile(zw, layer, prjWKT); err != nil {
			return err
		}
	}
	return zw.Close()
}

func writeLayerShapefile(zw *zip.Writer, layer Layer, prjWKT string) error {
	shapeType, ok := shapeTypeFor(layer.GeometryType)
	if !ok {
		return fmt.Errorf("export: shapefile: feature class %q has geometry type %q, which has no shapefile shape type", layer.Code, layer.GeometryType)
	}

	shp, shx, err := buildSHP(layer, shapeType)
	if err != nil {
		return err
	}
	dbf, err := buildDBF(layer)
	if err != nil {
		return err
	}

	base := safeName(layer.Code)
	// Written in a fixed order so a given export is byte-identical run to run.
	entries := []struct {
		name string
		data []byte
	}{
		{base + ".shp", shp},
		{base + ".shx", shx},
		{base + ".dbf", dbf},
		{base + ".prj", []byte(prjWKT)},
	}
	for _, e := range entries {
		f, err := zw.Create(e.name)
		if err != nil {
			return err
		}
		if _, err := f.Write(e.data); err != nil {
			return err
		}
	}
	return nil
}

func shapeTypeFor(geometryType string) (int32, bool) {
	switch geometryType {
	case "point":
		return shpPoint, true
	case "line":
		return shpPolyLine, true
	case "polygon":
		return shpPolygon, true
	}
	return 0, false
}

// bbox accumulates the min/max extent of the geometry written so far, for the
// .shp/.shx file headers (and each PolyLine/Polygon record's own box).
type bbox struct {
	minX, minY, maxX, maxY float64
	set                    bool
}

func (b *bbox) add(x, y float64) {
	if !b.set {
		b.minX, b.minY, b.maxX, b.maxY, b.set = x, y, x, y, true
		return
	}
	if x < b.minX {
		b.minX = x
	}
	if y < b.minY {
		b.minY = y
	}
	if x > b.maxX {
		b.maxX = x
	}
	if y > b.maxY {
		b.maxY = y
	}
}

func (b *bbox) merge(o bbox) {
	if o.set {
		b.add(o.minX, o.minY)
		b.add(o.maxX, o.maxY)
	}
}

// buildSHP produces the .shp and .shx bytes for one layer. Both files share
// the same 100-byte header (differing only in file length); the .shp then
// carries each feature's record, the .shx a fixed 8-byte (offset, length)
// index entry per record. A feature with no/empty geometry becomes a Null
// shape so the record count stays in lockstep with the DBF.
func buildSHP(layer Layer, shapeType int32) (shp, shx []byte, err error) {
	var records bytes.Buffer // .shp record headers + contents
	var index bytes.Buffer   // .shx entries
	var file bbox

	offsetWords := int32(50) // header is 100 bytes = 50 16-bit words
	for i, feat := range layer.Features {
		content, recBox, err := shapeRecord(shapeType, feat.Geometry)
		if err != nil {
			return nil, nil, fmt.Errorf("export: shapefile: feature %s: %w", feat.ID, err)
		}
		file.merge(recBox)

		contentWords := int32(len(content) / 2)
		// .shp record header: record number (1-based), content length in words.
		binary.Write(&records, binary.BigEndian, int32(i+1))
		binary.Write(&records, binary.BigEndian, contentWords)
		records.Write(content)

		// .shx entry: offset of this record (in words), its content length.
		binary.Write(&index, binary.BigEndian, offsetWords)
		binary.Write(&index, binary.BigEndian, contentWords)
		offsetWords += 4 + contentWords // 4 words = the 8-byte record header
	}

	shp = append(shpHeader(shapeType, 50+int32(records.Len()/2), file), records.Bytes()...)
	shx = append(shpHeader(shapeType, 50+int32(index.Len()/2), file), index.Bytes()...)
	return shp, shx, nil
}

// shpHeader builds the shared 100-byte .shp/.shx header. fileLengthWords is the
// total file length (header + body) in 16-bit words.
func shpHeader(shapeType, fileLengthWords int32, box bbox) []byte {
	var h bytes.Buffer
	binary.Write(&h, binary.BigEndian, int32(9994)) // file code
	h.Write(make([]byte, 20))                        // 5 unused int32
	binary.Write(&h, binary.BigEndian, fileLengthWords)
	binary.Write(&h, binary.LittleEndian, int32(1000)) // version
	binary.Write(&h, binary.LittleEndian, shapeType)
	binary.Write(&h, binary.LittleEndian, box.minX)
	binary.Write(&h, binary.LittleEndian, box.minY)
	binary.Write(&h, binary.LittleEndian, box.maxX)
	binary.Write(&h, binary.LittleEndian, box.maxY)
	h.Write(make([]byte, 32)) // Z and M ranges, unused (all zero)
	return h.Bytes()
}

// shapeRecord encodes one feature's geometry into a .shp record content
// (little-endian, starting with the shape type) and returns its bounding box.
// The layer's shape type is authoritative; a geometry that doesn't fit it
// (or is absent) is written as a Null shape rather than corrupting the file.
func shapeRecord(shapeType int32, raw json.RawMessage) ([]byte, bbox, error) {
	var box bbox
	if len(raw) == 0 {
		return nullShape(), box, nil
	}

	var g struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, box, err
	}

	switch shapeType {
	case shpPoint:
		pt, ok, err := firstPoint(g.Type, g.Coordinates)
		if err != nil {
			return nil, box, err
		}
		if !ok {
			return nullShape(), box, nil
		}
		box.add(pt[0], pt[1])
		var b bytes.Buffer
		binary.Write(&b, binary.LittleEndian, shpPoint)
		binary.Write(&b, binary.LittleEndian, pt[0])
		binary.Write(&b, binary.LittleEndian, pt[1])
		return b.Bytes(), box, nil

	case shpPolyLine:
		parts, err := lineParts(g.Type, g.Coordinates)
		if err != nil {
			return nil, box, err
		}
		if len(parts) == 0 {
			return nullShape(), box, nil
		}
		return partsShape(shpPolyLine, parts, &box), box, nil

	case shpPolygon:
		rings, err := polygonRings(g.Type, g.Coordinates)
		if err != nil {
			return nil, box, err
		}
		if len(rings) == 0 {
			return nullShape(), box, nil
		}
		return partsShape(shpPolygon, rings, &box), box, nil
	}
	return nullShape(), box, nil
}

func nullShape() []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, shpNull)
	return b.Bytes()
}

// partsShape encodes a PolyLine (type 3) or Polygon (type 5) record: both have
// the identical layout of a box, a parts index, and a flat point array. Adds
// every point to box as it goes.
func partsShape(shapeType int32, parts [][][2]float64, box *bbox) []byte {
	var recBox bbox
	numPoints := 0
	for _, part := range parts {
		numPoints += len(part)
		for _, p := range part {
			recBox.add(p[0], p[1])
		}
	}
	box.merge(recBox)

	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, shapeType)
	binary.Write(&b, binary.LittleEndian, recBox.minX)
	binary.Write(&b, binary.LittleEndian, recBox.minY)
	binary.Write(&b, binary.LittleEndian, recBox.maxX)
	binary.Write(&b, binary.LittleEndian, recBox.maxY)
	binary.Write(&b, binary.LittleEndian, int32(len(parts)))
	binary.Write(&b, binary.LittleEndian, int32(numPoints))

	start := int32(0)
	for _, part := range parts {
		binary.Write(&b, binary.LittleEndian, start)
		start += int32(len(part))
	}
	for _, part := range parts {
		for _, p := range part {
			binary.Write(&b, binary.LittleEndian, p[0])
			binary.Write(&b, binary.LittleEndian, p[1])
		}
	}
	return b.Bytes()
}

// firstPoint pulls a single (x, y) for a Point shapefile. Tolerates a
// MultiPoint by taking its first coordinate. ok is false for an empty
// coordinate array.
func firstPoint(geomType string, coords json.RawMessage) ([2]float64, bool, error) {
	switch geomType {
	case "Point":
		var c []float64
		if err := json.Unmarshal(coords, &c); err != nil {
			return [2]float64{}, false, err
		}
		if len(c) < 2 {
			return [2]float64{}, false, nil
		}
		return [2]float64{c[0], c[1]}, true, nil
	case "MultiPoint":
		var cs [][]float64
		if err := json.Unmarshal(coords, &cs); err != nil {
			return [2]float64{}, false, err
		}
		if len(cs) == 0 || len(cs[0]) < 2 {
			return [2]float64{}, false, nil
		}
		return [2]float64{cs[0][0], cs[0][1]}, true, nil
	}
	return [2]float64{}, false, nil
}

// lineParts returns one part per LineString (a MultiLineString contributes
// several), each a slice of (x, y) points.
func lineParts(geomType string, coords json.RawMessage) ([][][2]float64, error) {
	switch geomType {
	case "LineString":
		var cs [][]float64
		if err := json.Unmarshal(coords, &cs); err != nil {
			return nil, err
		}
		return [][][2]float64{toPoints(cs)}, nil
	case "MultiLineString":
		var lines [][][]float64
		if err := json.Unmarshal(coords, &lines); err != nil {
			return nil, err
		}
		parts := make([][][2]float64, 0, len(lines))
		for _, cs := range lines {
			parts = append(parts, toPoints(cs))
		}
		return parts, nil
	}
	return nil, nil
}

// polygonRings flattens a Polygon (or MultiPolygon) into shapefile rings, with
// each ring's winding fixed to the shapefile convention: the outer ring of
// each polygon clockwise, its holes counter-clockwise (GeoJSON uses the
// opposite, and PostGIS makes no winding guarantee, so this is enforced, not
// assumed). Rings are closed if the source left them open.
func polygonRings(geomType string, coords json.RawMessage) ([][][2]float64, error) {
	switch geomType {
	case "Polygon":
		var rings [][][]float64
		if err := json.Unmarshal(coords, &rings); err != nil {
			return nil, err
		}
		return orientPolygon(rings), nil
	case "MultiPolygon":
		var polys [][][][]float64
		if err := json.Unmarshal(coords, &polys); err != nil {
			return nil, err
		}
		var out [][][2]float64
		for _, rings := range polys {
			out = append(out, orientPolygon(rings)...)
		}
		return out, nil
	}
	return nil, nil
}

// orientPolygon converts one GeoJSON polygon's rings (outer first, then holes)
// to closed, correctly-wound shapefile rings.
func orientPolygon(rings [][][]float64) [][][2]float64 {
	out := make([][][2]float64, 0, len(rings))
	for i, ring := range rings {
		pts := closeRing(toPoints(ring))
		clockwise := signedArea(pts) < 0
		if i == 0 {
			// Outer ring must be clockwise.
			if !clockwise {
				reverse(pts)
			}
		} else {
			// Holes must be counter-clockwise.
			if clockwise {
				reverse(pts)
			}
		}
		out = append(out, pts)
	}
	return out
}

func toPoints(cs [][]float64) [][2]float64 {
	pts := make([][2]float64, 0, len(cs))
	for _, c := range cs {
		if len(c) < 2 {
			continue
		}
		pts = append(pts, [2]float64{c[0], c[1]})
	}
	return pts
}

func closeRing(pts [][2]float64) [][2]float64 {
	if len(pts) < 2 {
		return pts
	}
	if pts[0] != pts[len(pts)-1] {
		pts = append(pts, pts[0])
	}
	return pts
}

func reverse(pts [][2]float64) {
	for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
		pts[i], pts[j] = pts[j], pts[i]
	}
}

// signedArea is the shoelace signed area; positive means counter-clockwise,
// negative clockwise (in the shapefile's x=east, y=north space).
func signedArea(pts [][2]float64) float64 {
	var sum float64
	for i := 0; i < len(pts)-1; i++ {
		sum += pts[i][0]*pts[i+1][1] - pts[i+1][0]*pts[i][1]
	}
	return sum / 2
}

// safeName reduces a feature class code to a filesystem-safe shapefile base
// name -- codes are already lower_snake_case, so this is belt-and-braces.
func safeName(code string) string {
	var b strings.Builder
	for _, r := range code {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "layer"
	}
	return b.String()
}

// --- DBF (dBASE III attribute table) ---------------------------------------

// dbfColumn is one attribute column of the DBF: its (≤10-char, unique) name,
// dBASE type byte, fixed width and decimal count, and a renderer that formats
// one feature's value to exactly `length` ASCII bytes.
type dbfColumn struct {
	name     string
	typ      byte
	length   int
	decimals int
	render   func(Feature) string
}

// buildDBF produces the .dbf bytes for a layer: a leading name and status
// column (both text), then one typed column per attribute field. Column
// widths for text are sized to the data (capped at the DBF max of 254).
func buildDBF(layer Layer) ([]byte, error) {
	used := map[string]bool{}
	cols := []dbfColumn{
		textColumn("name", layer, func(f Feature) string { return f.Name }, used),
		textColumn("status", layer, func(f Feature) string { return f.Status }, used),
	}
	for _, field := range layer.Fields {
		cols = append(cols, columnForField(field, layer, used))
	}

	recordLength := 1 // leading deletion flag
	for _, c := range cols {
		recordLength += c.length
	}
	headerLength := 32 + 32*len(cols) + 1

	var out bytes.Buffer
	// Header.
	out.WriteByte(0x03)          // dBASE III, no memo
	out.Write([]byte{100, 1, 1}) // last-updated date YY=100 (2000)-MM-DD, fixed for determinism
	binary.Write(&out, binary.LittleEndian, int32(len(layer.Features)))
	binary.Write(&out, binary.LittleEndian, int16(headerLength))
	binary.Write(&out, binary.LittleEndian, int16(recordLength))
	out.Write(make([]byte, 20)) // reserved

	// Field descriptors (32 bytes each).
	for _, c := range cols {
		var fd [32]byte
		copy(fd[0:11], c.name) // zero-padded => null-terminated
		fd[11] = c.typ
		fd[16] = byte(c.length)
		fd[17] = byte(c.decimals)
		out.Write(fd[:])
	}
	out.WriteByte(0x0D) // header terminator

	// Records.
	for _, feat := range layer.Features {
		out.WriteByte(0x20) // 0x20 = not deleted
		for _, c := range cols {
			out.WriteString(c.render(feat))
		}
	}
	out.WriteByte(0x1A) // EOF marker
	return out.Bytes(), nil
}

// columnForField maps one attribute field to a typed DBF column. Integers and
// numerics become numeric ('N') columns, booleans logical ('L'); everything
// else (text, uuid, dates, timestamps, jsonb) is stored as text so no value is
// ever lost to a too-narrow typed column.
func columnForField(field AttributeField, layer Layer, used map[string]bool) dbfColumn {
	name := dbfFieldName(field.Name, used)
	switch field.DataType {
	case "integer":
		return dbfColumn{name: name, typ: 'N', length: 18, decimals: 0, render: numericRenderer(field.Name, 18, 0)}
	case "numeric":
		return dbfColumn{name: name, typ: 'N', length: 19, decimals: 6, render: numericRenderer(field.Name, 19, 6)}
	case "boolean":
		return dbfColumn{name: name, typ: 'L', length: 1, decimals: 0, render: logicalRenderer(field.Name)}
	default:
		width := textWidth(layer, func(f Feature) string { return attrString(f.Attributes[field.Name]) })
		return dbfColumn{name: name, typ: 'C', length: width, decimals: 0, render: func(f Feature) string {
			return padC(attrString(f.Attributes[field.Name]), width)
		}}
	}
}

// textColumn builds a 'C' column whose width is sized to the widest value the
// getter produces across the layer (min 1, max 254).
func textColumn(rawName string, layer Layer, get func(Feature) string, used map[string]bool) dbfColumn {
	name := dbfFieldName(rawName, used)
	width := textWidth(layer, get)
	return dbfColumn{name: name, typ: 'C', length: width, decimals: 0, render: func(f Feature) string {
		return padC(get(f), width)
	}}
}

func textWidth(layer Layer, get func(Feature) string) int {
	width := 1
	for _, f := range layer.Features {
		if n := len(get(f)); n > width {
			width = n
		}
	}
	if width > 254 {
		width = 254
	}
	return width
}

func numericRenderer(field string, width, decimals int) func(Feature) string {
	return func(f Feature) string {
		v, ok := toFloat(f.Attributes[field])
		if !ok {
			return strings.Repeat(" ", width) // null -> blank
		}
		return padN(strconv.FormatFloat(v, 'f', decimals, 64), width)
	}
}

func logicalRenderer(field string) func(Feature) string {
	return func(f Feature) string {
		switch v := f.Attributes[field].(type) {
		case bool:
			if v {
				return "T"
			}
			return "F"
		case string:
			return logicalFromString(v)
		case []byte:
			return logicalFromString(string(v))
		default:
			return "?"
		}
	}
}

func logicalFromString(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "t", "true", "1", "y", "yes":
		return "T"
	case "f", "false", "0", "n", "no":
		return "F"
	}
	return "?"
}

// dbfFieldName truncates an attribute name to the DBF's 10-character limit and
// de-duplicates collisions (two fields truncating to the same 10 chars) by
// appending an incrementing digit, so no column silently overwrites another.
func dbfFieldName(name string, used map[string]bool) string {
	trimmed := name
	if len(trimmed) > 10 {
		trimmed = trimmed[:10]
	}
	candidate := trimmed
	for i := 1; used[strings.ToLower(candidate)]; i++ {
		suffix := strconv.Itoa(i)
		base := trimmed
		if len(base)+len(suffix) > 10 {
			base = base[:10-len(suffix)]
		}
		candidate = base + suffix
	}
	used[strings.ToLower(candidate)] = true
	return candidate
}

// padC left-justifies a text value in a fixed width, truncating (without
// splitting a multi-byte rune) or space-padding on the right.
func padC(s string, width int) string {
	if len(s) > width {
		s = truncateBytes(s, width)
	}
	return s + strings.Repeat(" ", width-len(s))
}

// padN right-justifies a numeric string in a fixed width. An over-wide number
// can't be represented, so it's filled with '*' (the dBASE overflow marker)
// rather than silently truncated to a wrong value.
func padN(s string, width int) string {
	if s == "" {
		return strings.Repeat(" ", width)
	}
	if len(s) > width {
		return strings.Repeat("*", width)
	}
	return strings.Repeat(" ", width-len(s)) + s
}

// truncateBytes trims s to at most n bytes without splitting the final
// multi-byte UTF-8 rune.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 { // mid-rune continuation byte
		n--
	}
	return s[:n]
}

// toFloat coerces an attribute value to a float64 for a numeric DBF column,
// tolerating the range of concrete types the generic pgdriver scan can hand
// back (numbers, or the textual form of one).
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case nil:
		return 0, false
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	case []byte:
		f, err := strconv.ParseFloat(strings.TrimSpace(string(n)), 64)
		return f, err == nil
	}
	return 0, false
}
