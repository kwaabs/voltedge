package export

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// This file hand-rolls a minimal AutoCAD DXF (the ASCII drawing-interchange
// format) against the stdlib -- no CAD dependency. DXF is a flat list of
// (group-code, value) line pairs grouped into sections; we emit the four that
// matter: HEADER (just the version), TABLES (one APPID for our XDATA plus one
// LAYER per feature class), ENTITIES (the geometry), and EOF. Every entity is
// placed on its feature class's layer, and its attributes ride as per-entity
// XDATA under the VOLTEDGE application -- DXF has no attribute table, so XDATA
// (CAD-native extended entity data) is the round-trippable home for them, the
// decision locked in docs/phase-14-export.md.
//
// Two DXF facts drive the geometry mapping: there is no polygon-with-hole
// entity (HATCH aside), and no native multipart entity. So a polygon's rings,
// a MultiPolygon's polygons, and a MultiLineString's lines are each emitted as
// their own entity, all carrying the same feature's XDATA. Coordinates are
// EPSG:4326 degrees (see the CRS trade-off in the phase doc).

const (
	dxfAppID   = "VOLTEDGE"
	dxfVersion = "AC1015" // AutoCAD 2000 -- the first version with LWPOLYLINE
	// XDATA string values (group 1000) are capped at 255 bytes by the format.
	dxfXDataMax = 255
)

// writeDXF renders every layer into one DXF document, streaming it straight to
// w through a buffered writer (Slice 1, docs/phase-17-large-export.md) rather
// than accumulating the whole drawing in a strings.Builder first -- the buffer
// was an OOM at export scale. The trade-off streaming makes: a geometry error
// (writeDXFEntities) may now surface after some bytes are already on the wire,
// so it can no longer become a clean pre-stream failure -- acceptable, since DB
// geometry is well-formed and the handler treats any post-header error as a
// truncated download.
func writeDXF(w io.Writer, docName string, layers []Layer) error {
	_, err := writeDXFStream(w, docName, asStreams(layers))
	return err
}

// writeDXFStream is the streaming DXF encoder (Phase 17 Slice 3d): it pulls each
// layer's features one at a time through StreamLayer.Each rather than ranging a
// materialized slice, so a whole-dataset DXF never holds its features in memory
// and no maxExportFeatures cap applies. Returns the total feature count
// streamed. writeDXF adapts materialized layers onto it, so DXF has one encoder.
func writeDXFStream(w io.Writer, _ string, layers []StreamLayer) (int, error) {
	b := bufio.NewWriter(w)
	writeDXFHeader(b)
	writeDXFTables(b, layers)
	count, err := writeDXFEntities(b, layers)
	if err != nil {
		return count, err
	}
	dxfPair(b, 0, "EOF")
	return count, b.Flush()
}

// dxfPair writes one group-code/value pair -- the atom of the DXF format.
func dxfPair(b *bufio.Writer, code int, value string) {
	fmt.Fprintf(b, "%d\n%s\n", code, value)
}

func writeDXFHeader(b *bufio.Writer) {
	dxfPair(b, 0, "SECTION")
	dxfPair(b, 2, "HEADER")
	dxfPair(b, 9, "$ACADVER")
	dxfPair(b, 1, dxfVersion)
	dxfPair(b, 0, "ENDSEC")
}

// writeDXFTables emits the APPID table (registering VOLTEDGE so XDATA is valid)
// and the LAYER table (one layer per feature class, so a CAD user can toggle
// classes independently). Layer names reuse the shapefile safeName so they're
// free of DXF's forbidden characters.
func writeDXFTables(b *bufio.Writer, layers []StreamLayer) {
	dxfPair(b, 0, "SECTION")
	dxfPair(b, 2, "TABLES")

	// APPID table -- one registered app for our XDATA.
	dxfPair(b, 0, "TABLE")
	dxfPair(b, 2, "APPID")
	dxfPair(b, 70, "1")
	dxfPair(b, 0, "APPID")
	dxfPair(b, 2, dxfAppID)
	dxfPair(b, 70, "0")
	dxfPair(b, 0, "ENDTAB")

	// LAYER table -- one entry per class.
	dxfPair(b, 0, "TABLE")
	dxfPair(b, 2, "LAYER")
	dxfPair(b, 70, strconv.Itoa(len(layers)))
	for _, layer := range layers {
		dxfPair(b, 0, "LAYER")
		dxfPair(b, 2, safeName(layer.Code))
		dxfPair(b, 70, "0")          // flags: none
		dxfPair(b, 62, "7")          // color 7 (white/black)
		dxfPair(b, 6, "CONTINUOUS")  // line type
	}
	dxfPair(b, 0, "ENDTAB")

	dxfPair(b, 0, "ENDSEC")
}

func writeDXFEntities(b *bufio.Writer, layers []StreamLayer) (int, error) {
	dxfPair(b, 0, "SECTION")
	dxfPair(b, 2, "ENTITIES")
	count := 0
	for _, layer := range layers {
		layerName := safeName(layer.Code)
		keys := layer.FieldKeys()
		n, err := layer.Each(func(feat Feature) error {
			if len(feat.Geometry) == 0 {
				return nil // geometry-less feature: nothing to draw
			}
			if err := dxfFeature(b, layerName, keys, feat); err != nil {
				return fmt.Errorf("export: dxf: feature %s: %w", feat.ID, err)
			}
			return nil
		})
		count += n
		if err != nil {
			return count, err
		}
	}
	dxfPair(b, 0, "ENDSEC")
	return count, nil
}

// dxfFeature dispatches one feature's GeoJSON geometry to the matching DXF
// entity emitter. Multi* and polygon geometries fan out to one entity per
// part/ring, each carrying the same feature's XDATA.
func dxfFeature(b *bufio.Writer, layer string, keys []string, feat Feature) error {
	var g struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if err := json.Unmarshal(feat.Geometry, &g); err != nil {
		return err
	}
	xdata := func() { writeDXFXData(b, keys, feat) }

	switch g.Type {
	case "Point":
		var c []float64
		if err := json.Unmarshal(g.Coordinates, &c); err != nil {
			return err
		}
		if len(c) >= 2 {
			dxfPoint(b, layer, c[0], c[1], xdata)
		}
	case "MultiPoint":
		var cs [][]float64
		if err := json.Unmarshal(g.Coordinates, &cs); err != nil {
			return err
		}
		for _, c := range cs {
			if len(c) >= 2 {
				dxfPoint(b, layer, c[0], c[1], xdata)
			}
		}
	case "LineString":
		var cs [][]float64
		if err := json.Unmarshal(g.Coordinates, &cs); err != nil {
			return err
		}
		dxfPolyline(b, layer, toPoints(cs), false, xdata)
	case "MultiLineString":
		var lines [][][]float64
		if err := json.Unmarshal(g.Coordinates, &lines); err != nil {
			return err
		}
		for _, cs := range lines {
			dxfPolyline(b, layer, toPoints(cs), false, xdata)
		}
	case "Polygon":
		var rings [][][]float64
		if err := json.Unmarshal(g.Coordinates, &rings); err != nil {
			return err
		}
		for _, ring := range rings {
			dxfPolyline(b, layer, toPoints(ring), true, xdata)
		}
	case "MultiPolygon":
		var polys [][][][]float64
		if err := json.Unmarshal(g.Coordinates, &polys); err != nil {
			return err
		}
		for _, rings := range polys {
			for _, ring := range rings {
				dxfPolyline(b, layer, toPoints(ring), true, xdata)
			}
		}
	default:
		return fmt.Errorf("unsupported geometry type %q", g.Type)
	}
	return nil
}

func dxfPoint(b *bufio.Writer, layer string, x, y float64, xdata func()) {
	dxfPair(b, 0, "POINT")
	dxfPair(b, 8, layer)
	dxfPair(b, 10, dxfFloat(x))
	dxfPair(b, 20, dxfFloat(y))
	dxfPair(b, 30, "0")
	xdata()
}

// dxfPolyline emits an LWPOLYLINE for a line (closed=false) or a polygon ring
// (closed=true). For a closed ring the GeoJSON already repeats the first point
// as the last; that duplicate is dropped and the closed flag set instead, so
// CAD sees a clean closed shape with no zero-length segment.
func dxfPolyline(b *bufio.Writer, layer string, pts [][2]float64, closed bool, xdata func()) {
	if closed && len(pts) > 1 && pts[0] == pts[len(pts)-1] {
		pts = pts[:len(pts)-1]
	}
	if len(pts) == 0 {
		return
	}
	dxfPair(b, 0, "LWPOLYLINE")
	dxfPair(b, 8, layer)
	dxfPair(b, 90, strconv.Itoa(len(pts)))
	if closed {
		dxfPair(b, 70, "1")
	} else {
		dxfPair(b, 70, "0")
	}
	for _, p := range pts {
		dxfPair(b, 10, dxfFloat(p[0]))
		dxfPair(b, 20, dxfFloat(p[1]))
	}
	xdata()
}

// writeDXFXData appends the feature's attributes as XDATA under the VOLTEDGE
// app: name and status first, then the layer's sorted attribute keys, each as
// a "key=value" string (group 1000). Values are sanitized to a single line and
// clipped to the format's 255-byte limit.
func writeDXFXData(b *bufio.Writer, keys []string, feat Feature) {
	dxfPair(b, 1001, dxfAppID)
	dxfPair(b, 1000, dxfXField("name", feat.Name))
	dxfPair(b, 1000, dxfXField("status", feat.Status))
	for _, k := range keys {
		dxfPair(b, 1000, dxfXField(k, attrString(feat.Attributes[k])))
	}
}

func dxfXField(key, value string) string {
	return dxfClip(key + "=" + dxfClean(value))
}

// dxfClean flattens a value to a single line -- DXF is line-oriented, so a
// newline in a value would corrupt the group-code stream.
func dxfClean(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

// dxfClip trims a string to the XDATA 255-byte limit without splitting the
// final multi-byte rune.
func dxfClip(s string) string {
	if len(s) <= dxfXDataMax {
		return s
	}
	return truncateBytes(s, dxfXDataMax)
}

// dxfFloat formats a coordinate as a plain decimal (never scientific notation,
// which some DXF readers reject).
func dxfFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
