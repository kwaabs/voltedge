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

	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// Feature is one row normalized for the format encoders: its id, optional
// name, GeoJSON geometry (EPSG:4326, exactly as the read path emits it), and
// its attributes. Status is kept out of Attributes and carried separately so
// every encoder can decide whether to surface it (all currently do, as a
// leading column/field/label) without it colliding with a class's own field
// named "status".
type Feature struct {
	ID     string
	Name   string
	Status string
	// SubtypeCode is the feature's integer subtype code (metadata.subtypes.code),
	// or nil for a class with no subtypes / a row with none set. The geometry
	// formats ignore it; the CIM encoder needs it to pick a subtype-scoped class
	// mapping (e.g. a DIST_SWITCH subtype that is a LoadBreakSwitch, not the
	// default Breaker). See docs/phase-16-cim-export.md.
	SubtypeCode *int
	Geometry    json.RawMessage
	Attributes  map[string]any
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
	// ClassID, NetworkRole, Terminals, and Subtypes are the topology inputs the
	// CIM encoder needs and the geometry formats ignore (see
	// docs/phase-16-cim-export.md). ClassID is the metadata.feature_classes.id,
	// used to pull this class's runtime.feature_connectivity rows. NetworkRole
	// ("edge"/"junction"/...) tells the encoder whether a NULL-terminal endpoint
	// is a conductor end (disambiguated by geometry) or a terminal-less junction.
	// Terminals/Subtypes let it resolve a feature's expected terminal set.
	ClassID     string
	NetworkRole string
	Terminals   []metadata.Terminal
	Subtypes    []metadata.Subtype
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

// StreamLayer is the streaming counterpart of Layer (Phase 17 Slice 3d): one
// feature class whose features are NOT materialized but pulled one at a time
// through Each, so the streaming encoders (KMZ/DXF) never hold the whole class
// in memory and no maxExportFeatures cap applies. Its metadata mirrors Layer's,
// minus the CIM topology inputs (CIM cannot stream -- it needs the whole graph)
// and the materialized Features slice.
//
// Each drives one pass over the class's features: it calls emit once per
// feature (in the same id order the materialized read uses) and returns how
// many features it streamed. An emit error stops the pass and is returned; a
// read error surfaces the same way. Because Each is a live DB cursor, it must
// run inside the caller's snapshot transaction.
type StreamLayer struct {
	Code         string
	Name         string
	GeometryType string
	Fields       []AttributeField
	Each         func(emit func(Feature) error) (int, error)
}

// FieldKeys returns the layer's attribute column set as the sorted field names
// -- the streaming analog of Layer.AttributeKeys. Where AttributeKeys unions
// the keys actually present across materialized features, FieldKeys reads the
// column set from the class metadata (Fields) instead, which the streaming path
// can't derive from a union it never holds. The two agree in production: the
// read path's formatRow populates every metadata field on every row (nulls
// included), so the data-union equals the sorted metadata field names.
func (l StreamLayer) FieldKeys() []string {
	keys := make([]string, len(l.Fields))
	for i, f := range l.Fields {
		keys[i] = f.Name
	}
	sort.Strings(keys)
	return keys
}

// asStream adapts an already-materialized Layer to a StreamLayer so the
// streaming encoders can serve the materialized callers (the format tests and
// any future in-memory path) through one code path. Its Fields are seeded from
// AttributeKeys so FieldKeys reproduces exactly the data-union column set the
// materialized encoders used -- keeping output byte-identical even for a layer
// whose metadata Fields are unset (as the encoder tests construct). Each simply
// ranges the materialized Features.
func asStream(l Layer) StreamLayer {
	keys := l.AttributeKeys()
	fields := make([]AttributeField, len(keys))
	for i, k := range keys {
		fields[i] = AttributeField{Name: k}
	}
	features := l.Features
	return StreamLayer{
		Code:         l.Code,
		Name:         l.Name,
		GeometryType: l.GeometryType,
		Fields:       fields,
		Each: func(emit func(Feature) error) (int, error) {
			for _, f := range features {
				if err := emit(f); err != nil {
					return 0, err
				}
			}
			return len(features), nil
		},
	}
}

// asStreams adapts a slice of materialized layers, preserving order.
func asStreams(layers []Layer) []StreamLayer {
	out := make([]StreamLayer, len(layers))
	for i, l := range layers {
		out[i] = asStream(l)
	}
	return out
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
	// subtype is the integer subtype code; the generic row scan can surface it
	// as any of the numeric Go types depending on the driver, so coerce broadly.
	if s, ok := row["subtype"]; ok && s != nil {
		if code, ok := asInt(s); ok {
			f.SubtypeCode = &code
		}
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

// asInt coerces a value from the generic row scan to an int. The subtype code
// is an integer column, but the driver may hand it back as int64/int32/int or
// (via a JSON round-trip) float64, so every numeric shape is accepted; a
// non-numeric or non-integral value yields ok == false.
func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
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
