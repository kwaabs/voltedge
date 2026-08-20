package export

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// writeKMZ streams a KMZ (a zip containing a single doc.kml) of every layer to
// w. KML is inherently EPSG:4326 (lon/lat), which is exactly what the read
// path emits, so no coordinate transform is needed. Each feature class becomes
// a KML Folder; each feature a Placemark with its attributes in ExtendedData
// and its geometry translated from GeoJSON.
func writeKMZ(w io.Writer, docName string, layers []Layer) error {
	zw := zip.NewWriter(w)
	f, err := zw.Create("doc.kml")
	if err != nil {
		return err
	}
	if err := writeKML(f, docName, layers); err != nil {
		return err
	}
	return zw.Close()
}

// writeKML writes the KML document body. Built with a strings.Builder and
// flushed once -- an export is bounded (maxExportFeatures per class) and this
// keeps the geometry/escape helpers simple string-in string-out.
func writeKML(w io.Writer, docName string, layers []Layer) error {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<kml xmlns="http://www.opengis.net/kml/2.2">` + "\n")
	b.WriteString("<Document>\n")
	b.WriteString("<name>" + xmlEscape(docName) + "</name>\n")

	for _, layer := range layers {
		b.WriteString("<Folder>\n")
		b.WriteString("<name>" + xmlEscape(layer.Name) + "</name>\n")
		keys := layer.AttributeKeys()
		for _, feat := range layer.Features {
			if err := kmlPlacemark(&b, feat, keys); err != nil {
				return err
			}
		}
		b.WriteString("</Folder>\n")
	}

	b.WriteString("</Document>\n</kml>\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// kmlPlacemark writes one feature: its name, its attributes as ExtendedData
// (status first, then the layer's sorted attribute keys so every placemark
// has the same column set), then its geometry. A feature with no geometry is
// skipped entirely -- a Placemark with no geometry is meaningless on a map.
func kmlPlacemark(b *strings.Builder, feat Feature, keys []string) error {
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

func kmlData(b *strings.Builder, name, value string) {
	b.WriteString(`<Data name="` + xmlEscape(name) + `"><value>` + xmlEscape(value) + "</value></Data>\n")
}

// kmlGeometry translates a GeoJSON geometry object into its KML equivalent.
// Handles the single and Multi* variants of Point/LineString/Polygon -- the
// only geometry types a feature class column can hold.
func kmlGeometry(b *strings.Builder, raw json.RawMessage) error {
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

func kmlPoint(b *strings.Builder, c []float64) {
	b.WriteString("<Point><coordinates>")
	writeCoord(b, c)
	b.WriteString("</coordinates></Point>")
}

func kmlLineString(b *strings.Builder, cs [][]float64) {
	b.WriteString("<LineString><coordinates>")
	writeCoordList(b, cs)
	b.WriteString("</coordinates></LineString>")
}

// kmlPolygon writes a Polygon: the first ring is the outer boundary, any
// remaining rings are holes, matching GeoJSON's ring ordering.
func kmlPolygon(b *strings.Builder, rings [][][]float64) {
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

func writeCoordList(b *strings.Builder, cs [][]float64) {
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
func writeCoord(b *strings.Builder, c []float64) {
	if len(c) < 2 {
		return
	}
	fmt.Fprintf(b, "%g,%g", c[0], c[1])
}

// xmlEscape escapes the five XML metacharacters. The stdlib's xml.EscapeText
// targets an io.Writer and also rewrites tabs/newlines; a small replacer keeps
// attribute names and values readable and is enough here since every value is
// emitted as element/attribute text.
var xmlEscaper = strings.NewReplacer(
	"&", "&amp;",
	"<", "&lt;",
	">", "&gt;",
	`"`, "&quot;",
	"'", "&apos;",
)

func xmlEscape(s string) string {
	return xmlEscaper.Replace(s)
}
