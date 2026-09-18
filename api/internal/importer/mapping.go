package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// This file is the heart the dry-run (S3) and the load worker (S4) share: taking
// one NDJSON source row + a resolved import profile and producing the
// create-shaped attributes + geometry the crud engine validates and writes.
//
// The one non-obvious step is TYPE COERCION. crud.validateValue expects a value
// already in the target field's Go shape -- a float64 for a numeric/integer
// field, a bool for a boolean field -- but a CSV cell always arrives as a
// string ("123", "true"). So every mapped value is coerced to its field's
// data_type before it reaches validation: parse the string, or pass a value
// that is already the right type straight through (GeoJSON numbers decode as
// float64 already). A blank cell coerces to "unset" (nil), i.e. a blank column =
// no value, not an empty string -- the conventional ETL reading.

// ResolvedProfile is an import profile compiled against its target class's live
// metadata: the loaded bundle crud validates against, the profile's subtype as a
// code (crud takes the code, the row stores the id), and one compiledMapping per
// field mapping (its target field + a pre-parsed value_map for domain_map). It
// is resolved once per run/dry-run and then applied to every row.
type ResolvedProfile struct {
	Profile     *Profile
	Loaded      *metadata.Loaded
	SubtypeCode *int

	compiled []compiledMapping
}

// compiledMapping pairs one field mapping with its resolved target field and, for
// a domain_map transform, the value_map parsed once (not per row).
type compiledMapping struct {
	m        *FieldMapping
	field    *metadata.Field
	valueMap map[string]any
}

// ResolveProfile loads the profile by code, its field mappings, and the target
// class's metadata bundle, and compiles them together. A mapping whose target
// field is no longer active on the class is dropped (deprecated fields are
// invisible to the crud engine, so there is nothing to write) rather than failing
// the whole run. An unknown profile code is a notFoundError (404).
func ResolveProfile(ctx context.Context, db bun.IDB, code string) (*ResolvedProfile, error) {
	profile := new(Profile)
	if err := db.NewSelect().Model(profile).Where("code = ?", code).Scan(ctx); err != nil {
		return nil, notFoundf("import profile %q not found", code)
	}

	// The profile stores the class by id; metadata.Load keys on code.
	var classCode string
	if err := db.NewSelect().
		Model((*metadata.FeatureClass)(nil)).
		ColumnExpr("code").
		Where("id = ?", profile.FeatureClassID).
		Scan(ctx, &classCode); err != nil {
		return nil, fmt.Errorf("resolving feature class for profile %q: %w", code, err)
	}

	loaded, err := metadata.Load(ctx, db, classCode)
	if err != nil {
		return nil, fmt.Errorf("loading metadata for profile %q: %w", code, err)
	}

	var mappings []FieldMapping
	if err := db.NewSelect().Model(&mappings).Where("profile_id = ?", profile.ID).Scan(ctx); err != nil {
		return nil, fmt.Errorf("loading field mappings for profile %q: %w", code, err)
	}

	fieldsByID := make(map[string]*metadata.Field, len(loaded.Fields))
	for i := range loaded.Fields {
		fieldsByID[loaded.Fields[i].ID] = &loaded.Fields[i]
	}

	rp := &ResolvedProfile{Profile: profile, Loaded: loaded}

	// The subtype the profile pins (if any), as its integer code -- what crud
	// validation takes.
	if profile.SubtypeID != nil {
		for i := range loaded.Subtypes {
			if loaded.Subtypes[i].ID == *profile.SubtypeID {
				c := loaded.Subtypes[i].Code
				rp.SubtypeCode = &c
				break
			}
		}
	}

	for i := range mappings {
		m := &mappings[i]
		field := fieldsByID[m.FieldID]
		if field == nil {
			continue // target field deprecated/removed -- nothing to write
		}
		cm := compiledMapping{m: m, field: field}
		if m.Transform == "domain_map" && len(m.ValueMap) > 0 {
			if err := json.Unmarshal(m.ValueMap, &cm.valueMap); err != nil {
				return nil, invalidf("field %q has a malformed value_map: %v", field.Name, err)
			}
		}
		rp.compiled = append(rp.compiled, cm)
	}

	return rp, nil
}

// BuildAttributes applies every compiled mapping to one source row, producing the
// create-shaped attribute map keyed by target field name. A mapping whose source
// cell is absent/blank leaves its field unset (so required-field enforcement in
// validation catches a genuinely missing value); a value that a transform can't
// coerce to its field's type is a row error the caller records.
func (rp *ResolvedProfile) BuildAttributes(f *Feature) (map[string]any, error) {
	attrs := make(map[string]any, len(rp.compiled))
	for i := range rp.compiled {
		cm := &rp.compiled[i]
		raw, err := cm.rawValue(f)
		if err != nil {
			return nil, invalidf("field %q: %v", cm.field.Name, err)
		}
		coerced, err := coerceValue(cm.field.DataType, raw)
		if err != nil {
			return nil, invalidf("field %q: %v", cm.field.Name, err)
		}
		if coerced == nil {
			continue // blank/absent source -> leave the field unset
		}
		attrs[cm.field.Name] = coerced
	}
	return attrs, nil
}

// rawValue produces the pre-coercion value a single mapping contributes, per its
// transform. It returns nil for an absent source cell (so the field is left
// unset), except constant, which always contributes its literal.
func (cm *compiledMapping) rawValue(f *Feature) (any, error) {
	m := cm.m
	switch m.Transform {
	case "constant":
		if m.ConstantValue == nil {
			return nil, nil
		}
		return *m.ConstantValue, nil

	case "direct":
		return sourceCell(f, m.SourceColumn), nil

	case "scale":
		cell := sourceCell(f, m.SourceColumn)
		if cell == nil {
			return nil, nil
		}
		n, ok := toFloat(cell)
		if !ok {
			return nil, fmt.Errorf("value %v is not numeric (scale transform)", cell)
		}
		factor := 1.0
		if m.ScaleFactor != nil {
			factor = *m.ScaleFactor
		}
		return n * factor, nil

	case "domain_map":
		cell := sourceCell(f, m.SourceColumn)
		if cell == nil {
			return nil, nil
		}
		key := fmt.Sprint(cell)
		mapped, ok := cm.valueMap[key]
		if !ok {
			return nil, fmt.Errorf("source value %q has no value_map entry", key)
		}
		return mapped, nil
	}
	return nil, fmt.Errorf("unknown transform %q", m.Transform)
}

// sourceCell reads a source column from the row's properties, returning nil when
// the mapping names no column or the row has no such column.
func sourceCell(f *Feature, column *string) any {
	if column == nil || *column == "" || f.Properties == nil {
		return nil
	}
	return f.Properties[*column]
}

// coerceValue converts a mapped value into the shape crud.validateValue expects
// for the target field's data_type. A string source (every CSV cell) is parsed;
// a value already of the right type (a GeoJSON number decoded as float64) passes
// through. A blank string is treated as "unset" (nil) for every type. It does NOT
// enforce domain membership, ranges, or whole-number-ness -- crud validation owns
// those, so a "123.5" onto an integer field coerces to 123.5 and validation
// reports the clean "must be a whole number".
func coerceValue(dataType string, raw any) (any, error) {
	if raw == nil {
		return nil, nil
	}
	switch dataType {
	case "integer", "numeric":
		switch v := raw.(type) {
		case float64:
			return v, nil
		case int64:
			return float64(v), nil
		case string:
			s := strings.TrimSpace(v)
			if s == "" {
				return nil, nil
			}
			n, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, fmt.Errorf("%q is not a number", v)
			}
			return n, nil
		}
		return nil, fmt.Errorf("cannot use %T as a number", raw)

	case "boolean":
		switch v := raw.(type) {
		case bool:
			return v, nil
		case string:
			s := strings.TrimSpace(strings.ToLower(v))
			if s == "" {
				return nil, nil
			}
			switch s {
			case "true", "t", "1", "yes", "y":
				return true, nil
			case "false", "f", "0", "no", "n":
				return false, nil
			}
			return nil, fmt.Errorf("%q is not a boolean", v)
		}
		return nil, fmt.Errorf("cannot use %T as a boolean", raw)

	default: // text, date, timestamp, uuid, jsonb
		switch v := raw.(type) {
		case string:
			if strings.TrimSpace(v) == "" {
				return nil, nil
			}
			return v, nil
		case float64:
			return formatFloat(v), nil
		case bool:
			return strconv.FormatBool(v), nil
		default:
			return v, nil
		}
	}
}

// toFloat coerces a numeric-or-numeric-string cell to a float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// formatFloat renders a float for a text field, dropping a trailing ".0" so an
// integer-valued source cell (a code, an id) doesn't gain a spurious decimal.
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// pointGeometry is a GeoJSON Point marshalled from a lon/lat pair.
type pointGeometry struct {
	Type        string     `json:"type"`
	Coordinates [2]float64 `json:"coordinates"`
}

// AssembleGeometry builds the row's GeoJSON geometry per the profile's
// geometry_mode: none -> nil; native -> the row's own inline geometry; lonlat ->
// a Point from two numeric columns; wkt -> the WKT column converted to GeoJSON in
// the database (ST_GeomFromText). A missing coordinate/WKT cell yields no geometry
// (nil), leaving crud validation to report it if the class requires geometry. The
// db handle is only used for the wkt conversion; the other modes are pure.
func (rp *ResolvedProfile) AssembleGeometry(ctx context.Context, db bun.IDB, f *Feature) (json.RawMessage, error) {
	switch rp.Profile.GeometryMode {
	case "none":
		return nil, nil

	case "native":
		if f.HasGeometry() {
			return f.Geometry, nil
		}
		return nil, nil

	case "lonlat":
		lon, okLon := toFloat(sourceCell(f, rp.Profile.GeometryLonColumn))
		lat, okLat := toFloat(sourceCell(f, rp.Profile.GeometryLatColumn))
		if !okLon || !okLat {
			return nil, nil
		}
		b, err := json.Marshal(pointGeometry{Type: "Point", Coordinates: [2]float64{lon, lat}})
		if err != nil {
			return nil, err
		}
		return b, nil

	case "wkt":
		cell := sourceCell(f, rp.Profile.GeometryWktColumn)
		if cell == nil {
			return nil, nil
		}
		wkt, _ := cell.(string)
		if strings.TrimSpace(wkt) == "" {
			return nil, nil
		}
		return wktToGeoJSON(ctx, db, wkt, rp.srid())
	}
	return nil, nil
}

// srid is the target class's SRID, defaulting to 4326 (the browser normalizes
// geometry to WGS84, and lon/lat columns are lon/lat degrees).
func (rp *ResolvedProfile) srid() int {
	if rp.Loaded.FeatureClass.SRID != nil {
		return *rp.Loaded.FeatureClass.SRID
	}
	return 4326
}

// wktToGeoJSON converts a WKT string to a GeoJSON geometry in the database. A
// malformed WKT surfaces as the Postgres error, which the caller records as a
// per-row problem (this runs outside any transaction, so a bad row doesn't poison
// a batch).
func wktToGeoJSON(ctx context.Context, db bun.IDB, wkt string, srid int) (json.RawMessage, error) {
	var gj string
	if err := db.NewSelect().
		ColumnExpr("ST_AsGeoJSON(ST_GeomFromText(?, ?))", wkt, srid).
		Scan(ctx, &gj); err != nil {
		return nil, fmt.Errorf("invalid WKT geometry: %w", err)
	}
	return json.RawMessage(gj), nil
}
