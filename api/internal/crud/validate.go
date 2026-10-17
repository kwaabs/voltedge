package crud

import (
	"encoding/json"
	"errors"
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

// IsValidationError lets another package (api/internal/panel's composite create)
// map a crud validation failure -- a bad request body, unknown field, wrong
// subtype -- to a 400 without crud exporting its whole error-type hierarchy, the
// same way IsNotFoundError already exposes the not-found case. Uses errors.As so
// it still classifies correctly when the caller has wrapped the error with %w.
func IsValidationError(err error) bool {
	var ve *validationError
	return errors.As(err, &ve)
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

// conflictError is an optimistic-concurrency failure: the caller edited a
// version of the row that another write has since superseded (Phase 40). Its
// own type so the HTTP layer returns 409 -- distinct from a 400 (the request
// was well-formed, it just lost a race) and a 404 (the row does exist). The
// client's contract on a 409 is reload-and-reapply.
type conflictError struct{ msg string }

func (e *conflictError) Error() string { return e.msg }

func conflict(format string, args ...any) error {
	return &conflictError{msg: fmt.Sprintf(format, args...)}
}

func isConflictError(err error) bool {
	_, ok := err.(*conflictError)
	return ok
}

// forbiddenError is a spatial-access denial (Phase 58): the request was
// well-formed and the row exists, but the acting user's territory scope
// doesn't cover the feature they're trying to write. Its own type so the HTTP
// layer returns 403 -- distinct from a 400 (well-formed) and a 404 (a read of
// an out-of-scope feature, which is deliberately hidden as not-found; a write
// names a territory the user demonstrably knows about, so 403 is honest).
type forbiddenError struct{ msg string }

func (e *forbiddenError) Error() string { return e.msg }

func forbidden(format string, args ...any) error {
	return &forbiddenError{msg: fmt.Sprintf(format, args...)}
}

func isForbiddenError(err error) bool {
	_, ok := err.(*forbiddenError)
	return ok
}

// IsForbiddenError reports whether err is a Phase 58 spatial-access denial, for
// callers OUTSIDE this package (e.g. the external_ref editor in
// api/internal/externallink) that get a forbidden from EnforceFeatureWriteScope
// and must map it to an HTTP 403 rather than a generic 500.
func IsForbiddenError(err error) bool { return isForbiddenError(err) }

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

// resolveSubtypeChange decides whether an update actually changes the feature's
// subtype and, if so, resolves the target subtype. requested is the update
// request's subtype code (nil = the request didn't mention subtype, so leave it
// alone). A class with no subtypes can't receive one; an invalid code is
// rejected the same way create rejects it (validateSubtype). Requesting the
// subtype the feature already has is a no-op -- returns nil so the writer leaves
// the column untouched and the required-field re-check is skipped. existing is
// the getFeature envelope, whose "subtype" holds the current code.
func resolveSubtypeChange(loaded *metadata.Loaded, existing map[string]any, requested *int) (*metadata.Subtype, error) {
	if requested == nil {
		return nil, nil
	}
	if len(loaded.Subtypes) == 0 {
		return nil, invalid("feature class %q has no subtypes, so subtype can't be changed", loaded.FeatureClass.Code)
	}
	st, err := validateSubtype(loaded, requested)
	if err != nil {
		return nil, err
	}
	if cur, ok := toInt(existing["subtype"]); ok && cur == *requested {
		return nil, nil
	}
	return st, nil
}

// validateRequiredForSubtypeChange enforces the new subtype's required fields
// when an update changes subtype -- the check the update path historically
// punted on (types.go). It's merge-aware, unlike create's validateRequired: a
// required field the row already carries counts as satisfied even when this
// request doesn't resend it, and a field this request sets counts too, so the
// caller can change subtype and supply the newly-required values in one PATCH.
// A field neither present nor already set is filled from a default_override rule
// (injected into attrs so it's written and coerced downstream, same as create);
// with no default it's a 400 naming the field, so the caller knows exactly what
// to send. existingAttrs is the row's stored attributes; attrs is the incoming
// (raw, not yet coerced) request map, mutated in place for the default fill.
func validateRequiredForSubtypeChange(loaded *metadata.Loaded, newSubtype *metadata.Subtype, existingAttrs, attrs map[string]any) error {
	if newSubtype == nil {
		return nil
	}
	for _, name := range loaded.RequiredFieldNames(newSubtype.ID) {
		if v, present := attrs[name]; present && v != nil {
			continue
		}
		if v, present := existingAttrs[name]; present && v != nil {
			continue
		}
		f := loaded.FieldByName(name)
		if f == nil {
			continue
		}
		hasDefault := false
		for _, r := range loaded.SubtypeFieldRules {
			if r.SubtypeID == newSubtype.ID && r.FieldID == f.ID && r.DefaultOverride != nil {
				attrs[name] = *r.DefaultOverride
				hasDefault = true
				break
			}
		}
		if !hasDefault {
			return invalid("field %q is required for subtype %q -- set it in this update to change the subtype", name, newSubtype.Name)
		}
	}
	return nil
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

	// geometry_optional (Phase 26) lets a geometric class hold features with
	// no map geometry (map-only / diagram-only / both, per feature), so an
	// empty geometry is accepted here; a supplied one is still type-checked
	// below. Default false keeps geometry mandatory.
	if len(geometry) == 0 {
		if fc.GeometryOptional {
			return nil
		}
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

// validateForCreate runs the whole create-time validation sequence over an
// in-memory metadata bundle: subtype, geometry, required-by-subtype (which may
// inject default_override values into attrs), field known-ness/visibility, and
// per-value type/domain/range checks. It returns the resolved subtype and the
// coerced attribute values ready to write. It touches NO database and writes
// nothing -- so it is the single shared source of truth for both createHandler
// (the write path) and the read-only ValidateFeature dry-run (Phase 18 MCP,
// docs/phase-18-mcp.md), guaranteeing the two can never disagree on what a
// valid create looks like. Fail-fast: it returns the first validationError, the
// same first error the create endpoint surfaces. Note it does NOT cover what
// only the database enforces on the real write (FK, uniqueness, PostGIS
// coordinate validity) or the territory assignment, which is a spatial DB call
// the caller runs separately.
func validateForCreate(loaded *metadata.Loaded, subtypeCode *int, geometry []byte, attrs map[string]any) (*metadata.Subtype, map[string]any, error) {
	subtype, err := validateSubtype(loaded, subtypeCode)
	if err != nil {
		return nil, nil, err
	}
	if err := validateGeometry(loaded, geometry); err != nil {
		return nil, nil, err
	}
	if err := validateRequired(loaded, subtype, attrs); err != nil {
		return nil, nil, err
	}
	fields, err := validateAttributes(loaded, attrs, writeCreate, nil)
	if err != nil {
		return nil, nil, err
	}
	values := make(map[string]any, len(attrs))
	for name, raw := range attrs {
		coerced, err := validateValue(loaded, fields[name], raw)
		if err != nil {
			return nil, nil, err
		}
		values[name] = coerced
	}
	return subtype, values, nil
}

// validateTransitions enforces coded-domain state-machine rules on an update:
// for every field the request changes that is backed by a coded domain carrying
// transition edges (metadata.domain_transitions, migration 0065), the old ->
// new move must be a legal edge. A domain with no edges is unconstrained -- the
// default for every coded field (device_state, phase, ...) -- so this is a no-op
// unless an admin has actually authored a transition graph, which today is only
// OPERATIONAL_STATE (Phase 38 Slice 2). Update-only by construction: it reads
// the old value from the current row, so create (which has no prior value) never
// reaches here. Setting a previously-unset value and clearing one back to NULL
// are both allowed by TransitionAllowed -- first-declaration and un-declaration
// aren't lifecycle transitions.
//
// existing is the getFeature/formatRow envelope, so the prior coded value lives
// under existing["attributes"][name] (a string for a coded text column); values
// is the coerced new attribute set. The 400 names the codes involved and the
// domain, the same vocabulary validateValue's own membership error already uses.
func validateTransitions(loaded *metadata.Loaded, existing map[string]any, values map[string]any) error {
	existingAttrs, _ := existing["attributes"].(map[string]any)
	for name, newRaw := range values {
		f := loaded.FieldByName(name)
		if f == nil || f.DomainID == nil {
			continue
		}
		domain, ok := loaded.Domains[*f.DomainID]
		if !ok || domain.DomainType != "coded" {
			continue
		}
		newVal, _ := newRaw.(string)
		oldVal, _ := existingAttrs[name].(string)
		if loaded.TransitionAllowed(domain.ID, oldVal, newVal) {
			continue
		}
		return invalid("field %q cannot change from %q to %q -- not a permitted %s transition", name, oldVal, newVal, domain.Code)
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
