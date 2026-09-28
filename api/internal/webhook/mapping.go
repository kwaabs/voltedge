package webhook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// mappingSpec is an endpoint's optional field-mapping, stored in ep.Mapping
// (jsonb). It transforms a sender's OWN JSON into VoltEdge-native change objects
// BEFORE the target adapter ever sees them, so an integration that can't emit
// native payloads is still first-class -- parseChanges runs it and hands the
// resulting native objects to the same pipeline a native push uses.
//
// The transform is target-agnostic: it builds arbitrary JSON from `fields`, and
// the endpoint's target adapter decodes that into its typed shape (feature/reading
// -> nativeChange, work -> workChange, fault -> faultChange). Author the field
// names to match the target's native change.
//
//	{
//	  "items_path": "data.records",          // dot-path to the array of source items ("" = whole body)
//	  "fields": {
//	    "op":            {"const": "upsert"},
//	    "external_id":   {"path": "id"},
//	    "feature_class": {"const": "MV_CONDUCTOR"},
//	    "subtype":       {"path": "type", "map": {"OH": 1, "UG": 2}},
//	    "geometry":      {"path": "geom"},
//	    "attributes":    {"fields": {"voltage_class": {"path": "voltage"}}}
//	  }
//	}
type mappingSpec struct {
	ItemsPath string               `json:"items_path"`
	Fields    map[string]fieldSpec `json:"fields"`
}

// fieldSpec resolves one output field from a source item. Exactly one value
// source applies, tried in order: Const (a literal), Path (a dot-path lookup into
// the item), or Fields (build a nested object from more specs -- how `attributes`
// is expressed). An optional Map translates the resolved value through a lookup
// table keyed by the value's string form; Default supplies a value when the result
// is otherwise absent (a missing path, or a value Map didn't cover). A field that
// resolves to nothing is omitted from the output object, so the adapter's own
// required-field checks (external_id, feature_class, ...) still fire rather than
// being masked by an explicit null.
type fieldSpec struct {
	Const   *json.RawMessage           `json:"const"`
	Path    string                     `json:"path"`
	Map     map[string]json.RawMessage `json:"map"`
	Default *json.RawMessage           `json:"default"`
	Fields  map[string]fieldSpec       `json:"fields"`
}

// parseMappingSpec decodes a mapping spec strictly (unknown keys are rejected so a
// typo like "feild" is caught at configure time, not silently ignored at runtime).
func parseMappingSpec(raw json.RawMessage) (*mappingSpec, error) {
	var spec mappingSpec
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		return nil, invalid("mapping spec is not valid: %v", err)
	}
	return &spec, nil
}

// validateMappingSpec parses a spec and checks it is minimally usable, so a
// malformed spec is a clean 400 when an endpoint is configured rather than a
// per-delivery dead-letter later. It does not exercise the spec against a payload.
func validateMappingSpec(raw json.RawMessage) error {
	spec, err := parseMappingSpec(raw)
	if err != nil {
		return err
	}
	if len(spec.Fields) == 0 {
		return invalid(`mapping spec needs at least one entry under "fields"`)
	}
	return nil
}

// applyMapping transforms a sender payload into native change objects via the
// endpoint's mapping spec -- called by parseChanges when ep.Mapping is set. Each
// source item becomes one native change object; the target adapter decodes them.
func applyMapping(raw json.RawMessage, payload json.RawMessage) ([]json.RawMessage, error) {
	spec, err := parseMappingSpec(raw)
	if err != nil {
		return nil, err
	}
	if len(spec.Fields) == 0 {
		return nil, invalid(`mapping spec has no "fields" to map`)
	}

	var body any
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, invalid("mapped payload is not valid JSON: %v", err)
	}

	items, err := extractItems(body, spec.ItemsPath)
	if err != nil {
		return nil, err
	}

	out := make([]json.RawMessage, 0, len(items))
	for i, item := range items {
		obj := resolveFields(spec.Fields, item)
		b, err := json.Marshal(obj)
		if err != nil {
			return nil, fmt.Errorf("webhook: marshal mapped change %d: %w", i, err)
		}
		out = append(out, b)
	}
	return out, nil
}

// extractItems finds the list of source items to map. With items_path it navigates
// to that value and uses an array as-is (a single object there is wrapped); with no
// items_path the whole body is the source (an array is used as-is, a single object
// wrapped as a one-item batch).
func extractItems(body any, itemsPath string) ([]any, error) {
	target := body
	if p := strings.TrimSpace(itemsPath); p != "" {
		v, ok := getPath(body, p)
		if !ok {
			return nil, invalid("mapping items_path %q was not found in the payload", p)
		}
		target = v
	}
	switch t := target.(type) {
	case []any:
		return t, nil
	case map[string]any:
		return []any{t}, nil
	default:
		return nil, invalid("mapping source must be a JSON object or an array of objects")
	}
}

// resolveFields builds an output object from a spec map against one source item.
// A field that resolves to nothing is omitted rather than emitted as null.
func resolveFields(fields map[string]fieldSpec, item any) map[string]any {
	out := make(map[string]any, len(fields))
	for name, fs := range fields {
		if v, ok := resolveField(fs, item); ok {
			out[name] = v
		}
	}
	return out
}

// resolveField resolves one field spec against a source item, returning
// (value, true) or (nil, false) when it resolves to nothing.
func resolveField(fs fieldSpec, item any) (any, bool) {
	// Nested object (e.g. attributes): recurse. An all-empty result is omitted.
	if fs.Fields != nil {
		obj := resolveFields(fs.Fields, item)
		if len(obj) == 0 {
			return nil, false
		}
		return obj, true
	}

	var (
		val   any
		found bool
	)
	switch {
	case fs.Const != nil:
		val, found = rawToAny(*fs.Const), true
	case strings.TrimSpace(fs.Path) != "":
		val, found = getPath(item, strings.TrimSpace(fs.Path))
	}

	// Value translation, keyed by the resolved value's canonical string form. An
	// unmapped value falls through to Default (Map is treated as exhaustive).
	if found && fs.Map != nil {
		if mapped, ok := fs.Map[toKey(val)]; ok {
			val = rawToAny(mapped)
		} else {
			found = false
		}
	}

	if !found {
		if fs.Default != nil {
			return rawToAny(*fs.Default), true
		}
		return nil, false
	}
	return val, true
}

// getPath walks a dot-separated path into decoded JSON -- maps by key, arrays by
// integer index -- returning (value, true) or (nil, false) if any segment misses.
func getPath(v any, path string) (any, bool) {
	cur := v
	for _, seg := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			nv, ok := node[seg]
			if !ok {
				return nil, false
			}
			cur = nv
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

// toKey renders a resolved value as the string key used to look it up in a Map
// table. An integral number renders without a trailing ".0" so a source integer
// lines up with a table key like "2".
func toKey(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'g', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprint(t)
	}
}

// rawToAny decodes a spec literal (const / default / map value) into a Go value; a
// malformed literal decodes to nil, which simply omits the field.
func rawToAny(raw json.RawMessage) any {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	return v
}
