package webhookout

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Per-subscription payload mapping (design doc §6) -- the INVERSE of the inbound
// webhook mapping (api/internal/webhook/mapping.go): where that reshapes a
// sender's payload INTO native changes, this reshapes our outbound envelope INTO
// whatever shape a receiver expects. The field grammar is identical (const / path
// / map / default / nested fields); the only difference is the driver -- one
// envelope in, one object out (no items_path fan). It is reimplemented here rather
// than imported because webhookout must not import the webhook package (that would
// cycle: wam/faults import webhookout).

type mappingSpec struct {
	Fields map[string]fieldSpec `json:"fields"`
}

// fieldSpec describes how one output field is produced from the envelope. Exactly
// one value source is used, tried in order: Fields (a nested object), then Const
// (a literal), then Path (a dot-path lookup into the envelope). Map, when set,
// translates the resolved value by its string key (an unmapped value is a miss).
// Default supplies a value when nothing else resolved. A field that resolves to
// nothing is OMITTED from the output (not emitted as null).
type fieldSpec struct {
	Const   *json.RawMessage           `json:"const"`
	Path    string                     `json:"path"`
	Map     map[string]json.RawMessage `json:"map"`
	Default *json.RawMessage           `json:"default"`
	Fields  map[string]fieldSpec       `json:"fields"`
}

// applyEnvelopeMapping transforms the envelope into one receiver-shaped object per
// the spec. An empty/invalid spec is an error, so the caller can fall back to the
// unmapped envelope.
func applyEnvelopeMapping(specRaw, envelope json.RawMessage) (json.RawMessage, error) {
	var spec mappingSpec
	if err := json.Unmarshal(specRaw, &spec); err != nil {
		return nil, fmt.Errorf("webhookout: parsing mapping spec: %w", err)
	}
	if len(spec.Fields) == 0 {
		return nil, fmt.Errorf("webhookout: mapping spec has no fields")
	}
	var env any
	if err := json.Unmarshal(envelope, &env); err != nil {
		return nil, fmt.Errorf("webhookout: parsing envelope for mapping: %w", err)
	}
	body, err := json.Marshal(resolveFields(spec.Fields, env))
	if err != nil {
		return nil, fmt.Errorf("webhookout: marshaling mapped body: %w", err)
	}
	return body, nil
}

func resolveFields(fields map[string]fieldSpec, item any) map[string]any {
	out := make(map[string]any, len(fields))
	for name, spec := range fields {
		if v, ok := resolveField(spec, item); ok {
			out[name] = v
		}
	}
	return out
}

func resolveField(spec fieldSpec, item any) (any, bool) {
	// Nested object: recurse; an all-empty nested result is omitted.
	if len(spec.Fields) > 0 {
		nested := resolveFields(spec.Fields, item)
		if len(nested) == 0 {
			return nil, false
		}
		return nested, true
	}

	var val any
	var found bool
	switch {
	case spec.Const != nil:
		val, found = rawToAny(*spec.Const), true
	case spec.Path != "":
		val, found = getPath(item, spec.Path)
	}

	// Map is exhaustive: a value it doesn't list is treated as not-found so the
	// Default (if any) applies.
	if found && spec.Map != nil {
		if mapped, ok := spec.Map[toKey(val)]; ok {
			val, found = rawToAny(mapped), true
		} else {
			found = false
		}
	}
	if !found && spec.Default != nil {
		val, found = rawToAny(*spec.Default), true
	}
	return val, found
}

// getPath walks a dot-path into a decoded JSON value: maps by key, arrays by
// integer index. An empty path returns the value itself.
func getPath(v any, path string) (any, bool) {
	if path == "" {
		return v, true
	}
	cur := v
	for _, seg := range strings.Split(path, ".") {
		switch c := cur.(type) {
		case map[string]any:
			nv, ok := c[seg]
			if !ok {
				return nil, false
			}
			cur = nv
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(c) {
				return nil, false
			}
			cur = c[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}

// toKey renders a resolved value as the string key a Map is keyed by. An integral
// float64 (JSON numbers decode as float64) renders without a ".0" so a source int
// lines up with a map key like "2".
func toKey(v any) string {
	switch n := v.(type) {
	case float64:
		if n == math.Trunc(n) {
			return strconv.FormatInt(int64(n), 10)
		}
		return strconv.FormatFloat(n, 'f', -1, 64)
	case string:
		return n
	case bool:
		return strconv.FormatBool(n)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func rawToAny(raw json.RawMessage) any {
	var v any
	_ = json.Unmarshal(raw, &v)
	return v
}
