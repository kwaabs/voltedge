package crud

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// validationError is a client-fixable problem (bad request body, unknown
// field, wrong subtype, ...) -- handlers return these as 400. notFoundError
// is its own type -- handlers return these as 404. Everything else is
// 500. Kept distinct from a plain error so the HTTP layer never has to
// guess which status code a failure deserves.
type validationError struct{ msg string }

func (e *validationError) Error() string { return e.msg }

func invalid(format string, args ...any) error {
	return &validationError{msg: fmt.Sprintf(format, args...)}
}

func isValidationError(err error) bool {
	_, ok := err.(*validationError)
	return ok
}

type notFoundError struct{ msg string }

func (e *notFoundError) Error() string { return e.msg }

func notFound(format string, args ...any) error {
	return &notFoundError{msg: fmt.Sprintf(format, args...)}
}

func isNotFoundError(err error) bool {
	_, ok := err.(*notFoundError)
	return ok
}

// validateSubtype checks the request's subtype against what the feature
// class actually defines. A feature class with no subtypes must not
// receive one; a feature class with subtypes must receive a valid code.
func validateSubtype(loaded *metadata.Loaded, subtype *int) (*metadata.Subtype, error) {
	if len(loaded.Subtypes) == 0 {
		return nil, nil
	}
	if subtype == nil {
		return nil, invalid("subtype is required for feature class %q", loaded.FeatureClass.Code)
	}
	st := loaded.SubtypeByCode(*subtype)
	if st == nil {
		return nil, invalid("subtype %d is not valid for feature class %q", *subtype, loaded.FeatureClass.Code)
	}
	return st, nil
}

// validateGeometry checks that a geometry was supplied exactly when the
// feature class expects one, and that its GeoJSON "type" matches.
// Coordinate-level validity is left to PostGIS.
func validateGeometry(loaded *metadata.Loaded, geometry []byte) error {
	fc := loaded.FeatureClass
	if fc.GeometryType == "none" {
		if len(geometry) > 0 {
			return invalid("feature class %q has no geometry -- do not send one", fc.Code)
		}
		return nil
	}

	if len(geometry) == 0 {
		return invalid("geometry is required for feature class %q", fc.Code)
	}

	var g geoJSONGeometry
	if err := json.Unmarshal(geometry, &g); err != nil {
		return invalid("geometry is not valid GeoJSON: %v", err)
	}

	expected := map[string]string{"point": "Point", "line": "LineString", "polygon": "Polygon"}[fc.GeometryType]
	if g.Type != expected {
		return invalid("geometry type %q does not match feature class %q (expects %q)", g.Type, fc.Code, expected)
	}
	return nil
}

// writeMode describes which write is being validated -- create and
// update differ on which visibility flag applies and whether
// missing-required is even a question. Named writeMode, not context, to
// avoid colliding with the standard library's context package used
// elsewhere in this file and package.
type writeMode int

const (
	writeCreate writeMode = iota
	writeUpdate
)

// validateAttributes checks every key in attrs against the feature
// class's metadata: it must be a known active field, not computed, and
// visible in this context. currentStatus is only meaningful for update
// (nil on create) -- a published row locks any field with
// editable_after_publish = false.
func validateAttributes(loaded *metadata.Loaded, attrs map[string]any, mode writeMode, currentStatus *string) (map[string]*metadata.Field, error) {
	fields := make(map[string]*metadata.Field, len(attrs))

	for name := range attrs {
		f := loaded.FieldByName(name)
		if f == nil {
			return nil, invalid("unknown field %q on feature class %q", name, loaded.FeatureClass.Code)
		}
		if f.IsComputed {
			return nil, invalid("field %q is computed and cannot be set directly", name)
		}

		visible := f.VisibleOnCreate
		if mode == writeUpdate {
			visible = f.VisibleOnUpdate
		}
		if !visible {
			return nil, invalid("field %q is not editable in this context", name)
		}

		if mode == writeUpdate && currentStatus != nil && *currentStatus == "published" && !f.EditableAfterPublish {
			return nil, invalid("field %q is locked once the feature is published", name)
		}

		fields[name] = f
	}

	return fields, nil
}

// validateRequired checks that every field subtype_field_rules marks
// required for the given subtype is present in attrs, unless it has a
// default_override to fall back on. Create-only -- update is a partial
// write and doesn't re-check fields the request didn't touch.
func validateRequired(loaded *metadata.Loaded, subtype *metadata.Subtype, attrs map[string]any) error {
	if subtype == nil {
		return nil
	}

	for _, name := range loaded.RequiredFieldNames(subtype.ID) {
		if _, present := attrs[name]; present {
			continue
		}
		f := loaded.FieldByName(name)
		if f == nil {
			continue
		}
		// A default_override on the rule means "required" is satisfied by
		// falling back to that value, not by demanding the client send it.
		hasDefault := false
		for _, r := range loaded.SubtypeFieldRules {
			if r.SubtypeID == subtype.ID && r.FieldID == f.ID && r.DefaultOverride != nil {
				attrs[name] = *r.DefaultOverride
				hasDefault = true
				break
			}
		}
		if !hasDefault {
			return invalid("field %q is required for subtype %q", name, subtype.Name)
		}
	}
	return nil
}

// validateValue does a light type check on a decoded JSON value against
// a field's data_type -- enough to give a clean 400 instead of a raw
// Postgres error, not full semantic validation. Domain membership (for
// coded domains) is checked here too, so a bad value never reaches the
// database's own CHECK constraint.
func validateValue(loaded *metadata.Loaded, f *metadata.Field, value any) (any, error) {
	if value == nil {
		if !f.Nullable {
			return nil, invalid("field %q cannot be null", f.Name)
		}
		return nil, nil
	}

	switch f.DataType {
	case "text":
		s, ok := value.(string)
		if !ok {
			return nil, invalid("field %q must be a string", f.Name)
		}
		if f.DomainID != nil {
			if domain, ok := loaded.Domains[*f.DomainID]; ok && domain.DomainType == "coded" {
				if !contains(loaded.DomainValues[domain.ID], s) {
					return nil, invalid("field %q value %q is not a valid %s value", f.Name, s, domain.Code)
				}
			}
		}
		return s, nil

	case "boolean":
		b, ok := value.(bool)
		if !ok {
			return nil, invalid("field %q must be a boolean", f.Name)
		}
		return b, nil

	case "integer":
		n, ok := value.(float64)
		if !ok || n != float64(int64(n)) {
			return nil, invalid("field %q must be a whole number", f.Name)
		}
		if err := validateDomainRange(loaded, f, n); err != nil {
			return nil, err
		}
		return int64(n), nil

	case "numeric":
		n, ok := value.(float64)
		if !ok {
			return nil, invalid("field %q must be a number", f.Name)
		}
		if err := validateDomainRange(loaded, f, n); err != nil {
			return nil, err
		}
		return n, nil

	case "date":
		s, ok := value.(string)
		if !ok {
			return nil, invalid("field %q must be a date string (YYYY-MM-DD)", f.Name)
		}
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return nil, invalid("field %q is not a valid date (want YYYY-MM-DD): %v", f.Name, err)
		}
		return t, nil

	case "timestamp":
		s, ok := value.(string)
		if !ok {
			return nil, invalid("field %q must be an RFC3339 timestamp string", f.Name)
		}
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, invalid("field %q is not a valid RFC3339 timestamp: %v", f.Name, err)
		}
		return t, nil

	case "uuid":
		s, ok := value.(string)
		if !ok {
			return nil, invalid("field %q must be a UUID string", f.Name)
		}
		return s, nil

	case "jsonb":
		return value, nil

	default:
		return nil, invalid("field %q has an unrecognized data_type %q", f.Name, f.DataType)
	}
}

// validateDomainRange mirrors the coded-domain membership check above,
// for a numeric/integer field linked to a range domain -- an out-of-range
// value gets a clean 400 here instead of reaching the database's own
// BETWEEN CHECK constraint (compiler/ddl.go). A range domain with either
// bound unset compiles with no CHECK at all (see ddl.go), so this stays
// a no-op the same way, rather than rejecting everything.
func validateDomainRange(loaded *metadata.Loaded, f *metadata.Field, n float64) error {
	if f.DomainID == nil {
		return nil
	}
	domain, ok := loaded.Domains[*f.DomainID]
	if !ok || domain.DomainType != "range" || domain.RangeMin == nil || domain.RangeMax == nil {
		return nil
	}
	if n < *domain.RangeMin || n > *domain.RangeMax {
		return invalid("field %q value %v is outside %s's range [%v, %v]", f.Name, n, domain.Code, *domain.RangeMin, *domain.RangeMax)
	}
	return nil
}

func contains(values []string, target string) bool {
	for _, v := range values {
		if v == target {
			return true
		}
	}
	return false
}
