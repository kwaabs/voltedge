// Package export turns a set of feature classes -- optionally clipped to the
// map's current viewport -- into a downloadable geographic file: KMZ, ESRI
// Shapefile, or AutoCAD DXF, each carrying geometry and attributes. It reuses
// api/internal/crud's read path (every row already arrives as GeoJSON + an
// attribute map) and hand-rolls each format's encoder against Go's stdlib
// (archive/zip, encoding/json, encoding/xml), so no GIS/format dependency is
// added. See docs/phase-14-export.md.
package export

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Feature is one row normalized for the format encoders: its id, optional
// name, GeoJSON geometry (EPSG:4326, exactly as the read path emits it), and
// its attributes. Status is kept out of Attributes and carried separately so
// every encoder can decide whether to surface it (all currently do, as a
// leading column/field/label) without it colliding with a class's own field
// named "status".
type Feature struct {
	ID         string
	Name       string
	Status     string
	Geometry   json.RawMessage
	Attributes map[string]any
}

// AttributeField is one of a layer's attribute columns, in the feature
// class's own field order, with the metadata data_type the shapefile DBF
// encoder needs to pick a typed column (an `integer` -> a numeric DBF column,
// etc.). The name matches the key used in each Feature.Attributes map.
type AttributeField struct {
	Name     string
	DataType string
}

// Layer is one feature class's worth of features plus the metadata every
// encoder needs to name and shape its output: the class code (a stable,
// filesystem-safe slug used for shapefile/DXF layer names), its display name,
// its geometry type ("point"/"line"/"polygon" family) so the shapefile
// encoder can pick a shape type and the others can validate, and its ordered
// attribute fields (for the DBF/DXF typed attribute columns).
type Layer struct {
	Code         string
	Name         string
	GeometryType string
	Fields       []AttributeField
	Features     []Feature
}

// AttributeKeys returns the sorted union of every attribute key across the
// layer's features -- the stable column set the tabular encoders (shapefile
// DBF, and the KML/DXF attribute lists) build their headers from, so a class
// with optional fields still produces an aligned, deterministic output.
func (l Layer) AttributeKeys() []string {
	seen := map[string]bool{}
	for _, f := range l.Features {
		for k := range f.Attributes {
			seen[k] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// featureFrom converts one crud read-path row (formatRow's envelope) into a
// Feature. The geometry, parsed to a generic value by formatRow, is
// re-marshaled back to raw GeoJSON for the encoders to walk; attributes are
// taken as-is. A row with no geometry (should never happen for a geometric
// class, but defended against) yields a nil Geometry the encoders skip.
func featureFrom(row map[string]any) (Feature, error) {
	f := Feature{Attributes: map[string]any{}}
	if id, ok := row["id"].(string); ok {
		f.ID = id
	}
	if name, ok := row["name"].(string); ok {
		f.Name = name
	}
	if status, ok := row["status"].(string); ok {
		f.Status = status
	}
	if g, ok := row["geometry"]; ok && g != nil {
		raw, err := json.Marshal(g)
		if err != nil {
			return f, fmt.Errorf("export: re-encoding geometry: %w", err)
		}
		f.Geometry = raw
	}
	if attrs, ok := row["attributes"].(map[string]any); ok {
		f.Attributes = attrs
	}
	return f, nil
}

// attrString renders one attribute value as a display string for the
// text-oriented encoders (KML ExtendedData, DXF XDATA). A nil value becomes
// the empty string rather than the literal "<nil>".
func attrString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
