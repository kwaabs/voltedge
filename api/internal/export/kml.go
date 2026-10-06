package export

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// writeKMZ streams a KMZ (a zip containing a single doc.kml) of every
// materialized layer to w. It adapts its layers onto the streaming encoder
// (writeKMZStream) so KMZ has a single implementation; this materialized entry
// serves the format tests (and any in-memory caller). The runner uses
// writeKMZStream directly for the uncapped cursor path.
func writeKMZ(w io.Writer, docName string, layers []Layer) error {
	_, err := writeKMZStream(w, docName, asStreams(layers))
	return err
}

// writeKML writes the KML document body from materialized layers; a thin
// adapter over writeKMLStream (see writeKMZ).
func writeKML(w io.Writer, docName string, layers []Layer) error {
	_, err := writeKMLStream(w, docName, asStreams(layers))
	return err
}

// writeKMZStream streams a KMZ (a zip containing a single doc.kml) of every
// layer to w, pulling each layer's features one at a time through StreamLayer.
// Each so no class is ever fully held in memory (Phase 17 Slice 3d). Returns
// the total feature count streamed. KML is inherently EPSG:4326 (lon/lat),
// exactly what the read path emits, so no coordinate transform is needed.
func writeKMZStream(w io.Writer, docName string, layers []StreamLayer) (int, error) {
	zw := zip.NewWriter(w)
	f, err := zw.Create("doc.kml")
	if err != nil {
		return 0, err
	}
	n, err := writeKMLStream(f, docName, layers)
	if err != nil {
		return n, err
	}
	return n, zw.Close()
}

// writeKMLStream writes the KML document body, streaming it to w through a
// buffered writer (Slice 1, docs/phase-17-large-export.md) and pulling features
// through each layer's cursor (Slice 3d) so neither the document nor the
// feature set is ever fully buffered. Each feature class becomes a KML Folder;
// each feature a Placemark with its attributes in ExtendedData and its geometry
// translated from GeoJSON. bufio.Writer's write errors are sticky, so the
// geometry/escape helpers keep ignoring them and the single Flush at the end
// reports the first failure. Returns the total feature count streamed.
func writeKMLStream(w io.Writer, docName string, layers []StreamLayer) (int, error) {
	b := bufio.NewWriter(w)
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<kml xmlns="http://www.opengis.net/kml/2.2">` + "\n")
	b.WriteString("<Document>\n")
	b.WriteString("<name>" + xmlEscape(docName) + "</name>\n")

	count := 0
	for _, layer := range layers {
		b.WriteString("<Folder>\n")
		b.WriteString("<name>" + xmlEscape(layer.Name) + "</name>\n")
		keys := layer.FieldKeys()
		n, err := layer.Each(func(feat Feature) error {
			return kmlPlacemark(b, feat, keys)
		})
		count += n
		if err != nil {
			return count, err
		}
		b.WriteString("</Folder>\n")
	}

	b.WriteString("</Document>\n</kml>\n")
	return count, b.Flush()
}

// kmlPlacemark writes one feature: its name, its attributes as ExtendedData
// (status first, then the layer's sorted attribute keys so every placemark
// has the same column set), then its geometry. A feature with no geometry is
// skipped entirely -- a Placemark with no geometry is meaningless on a map.
func kmlPlacemark(b *bufio.Writer, feat Feature, keys []string) error {
	if len(feat.Geometry) == 0 {
		return nil
	}
	b.WriteString("<Placemark>\n")
	if feat.Name != "" {
		b.WriteString("<name>" + xmlEscape(feat.Name) + "</name>\n")
	}

	b.WriteString("<ExtendedData>\n")
	kmlData(b, "status", feat.Status)
	for _, k := range keys {
		kmlData(b, k, attrString(feat.Attributes[k]))
	}
	b.WriteString("</ExtendedData>\n")

	if err := kmlGeometry(b, feat.Geometry); err != nil {
		return fmt.Errorf("export: feature %s: %w", feat.ID, err)
	}
	b.WriteString("\n</Placemark>\n")
	return nil
}

func kmlData(b *bufio.Writer, name, value string) {
	b.WriteString(`<Data name="` + xmlEscape(name) + `"><value>` + xmlEscape(value) + "</value></Data>\n")
}

// kmlGeometry translates a GeoJSON geometry object into its KML equivalent.
// Handles the single and Multi* variants of Point/LineString/Polygon -- the
// only geometry types a feature class column can hold.
func kmlGeometry(b *bufio.Writer, raw json.RawMessage) error {
	var g struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return err
	}

	switch g.Type {
	case "Point":
		var c []float64
		if err := json.Unmarshal(g.Coordinates, &c); err != nil {
			return err
		}
		kmlPoint(b, c)
	case "LineString":
		var cs [][]float64
		if err := json.Unmarshal(g.Coordinates, &cs); err != nil {
			return err
		}
		kmlLineString(b, cs)
	case "Polygon":
		var rings [][][]float64
		if err := json.Unmarshal(g.Coordinates, &rings); err != nil {
			return err
		}
		kmlPolygon(b, rings)
	case "MultiPoint":
		var cs [][]float64
		if err := json.Unmarshal(g.Coordinates, &cs); err != nil {
			return err
		}
		b.WriteString("<MultiGeometry>")
		for _, c := range cs {
			kmlPoint(b, c)
		}
		b.WriteString("</MultiGeometry>")
	case "MultiLineString":
		var lines [][][]float64
		if err := json.Unmarshal(g.Coordinates, &lines); err != nil {
			return err
		}
		b.WriteString("<MultiGeometry>")
		for _, cs := range lines {
			kmlLineString(b, cs)
		}
		b.WriteString("</MultiGeometry>")
	case "MultiPolygon":
		var polys [][][][]float64
		if err := json.Unmarshal(g.Coordinates, &polys); err != nil {
			return err
		}
		b.WriteString("<MultiGeometry>")
		for _, rings := range polys {
			kmlPolygon(b, rings)
		}
		b.WriteString("</MultiGeometry>")
	default:
		return fmt.Errorf("unsupported geometry type %q", g.Type)
	}
	return nil
}

func kmlPoint(b *bufio.Writer, c []float64) {
	b.WriteString("<Point><coordinates>")
	writeCoord(b, c)
	b.WriteString("</coordinates></Point>")
}

func kmlLineString(b *bufio.Writer, cs [][]float64) {
	b.WriteString("<LineString><coordinates>")
	writeCoordList(b, cs)
	b.WriteString("</coordinates></LineString>")
}

// kmlPolygon writes a Polygon: the first ring is the outer boundary, any
// remaining rings are holes, matching GeoJSON's ring ordering.
func kmlPolygon(b *bufio.Writer, rings [][][]float64) {
	b.WriteString("<Polygon>")
	for i, ring := range rings {
		if i == 0 {
			b.WriteString("<outerBoundaryIs><LinearRing><coordinates>")
		} else {
			b.WriteString("<innerBoundaryIs><LinearRing><coordinates>")
		}
		writeCoordList(b, ring)
		if i == 0 {
			b.WriteString("</coordinates></LinearRing></outerBoundaryIs>")
		} else {
			b.WriteString("</coordinates></LinearRing></innerBoundaryIs>")
		}
	}
	b.WriteString("</Polygon>")
}

func writeCoordList(b *bufio.Writer, cs [][]float64) {
	for i, c := range cs {
		if i > 0 {
			b.WriteByte(' ')
		}
		writeCoord(b, c)
	}
}

// writeCoord writes one KML coordinate tuple, "lon,lat" (KML's axis order),
// dropping any z. A coordinate with fewer than two ordinates is skipped rather
// than panicking on a malformed geometry.
func writeCoord(b *bufio.Writer, c []float64) {
	if len(c) < 2 {
		return
	}
	fmt.Fprintf(b, "%g,%g", c[0], c[1])
}

// xmlEscaper escapes the five XML metacharacters, keeping attribute names and
// values readable (the stdlib's xml.EscapeText targets an io.Writer and also
// rewrites tabs/newlines).
var xmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

// xmlEscape makes s safe to emit as XML element/attribute text. It does two
// things: (1) drops characters that are not legal in XML 1.0 AT ALL -- the C0
// control codes other than tab/LF/CR (a stray NUL, vertical tab, form feed, etc.
// that dirty imported free-text can carry) plus the forbidden surrogate/sentinel
// ranges. Those are UNESCAPABLE: emitting them, escaped or not, yields a document
// every parser rejects, so a CIM/KML export of data containing one would be
// corrupt. strings.Map also folds invalid UTF-8 to U+FFFD (a legal char) as a
// side effect, so malformed bytes can't break the output either. Then (2) escapes
// the five metacharacters. Drop-then-escape so the escape sequences are never
// themselves stripped.
func xmlEscape(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		if isInvalidXMLChar(r) {
			return -1 // drop
		}
		return r
	}, s)
	return xmlEscaper.Replace(cleaned)
}

// isInvalidXMLChar reports whether r is NOT a legal XML 1.0 character (XML 1.0
// §2.2 Char): the valid set is {0x09, 0x0A, 0x0D, 0x20-0xD7FF, 0xE000-0xFFFD,
// 0x10000-0x10FFFF}. Everything else -- chiefly the C0 controls below 0x20 that
// aren't tab/LF/CR -- is illegal and must be removed, not escaped.
func isInvalidXMLChar(r rune) bool {
	switch {
	case r == 0x09 || r == 0x0A || r == 0x0D:
		return false
	case r >= 0x20 && r <= 0xD7FF:
		return false
	case r >= 0xE000 && r <= 0xFFFD:
		return false
	case r >= 0x10000 && r <= 0x10FFFF:
		return false
	}
	return true
}
