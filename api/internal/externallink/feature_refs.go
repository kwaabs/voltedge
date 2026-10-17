package externallink

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// This file is the Slice-4 management surface for a feature's external_ref jsonb
// (migration 0145): read the whole map and replace it. external_ref is the SOURCE
// OF TRUTH -- it's what the reverse-index trigger derives from and what the
// outbound live-fetch (feature_fetch.go) reads -- so an operator tagging a feature
// with its id in SCADA/AMI/CIS writes it here. It's a SYSTEM column (added by the
// compiler like deleted_at/territory_id), not a feature attribute, so it's managed
// by this dedicated read/replace pair rather than threaded through CreateFeature /
// UpdateFeature (staging, territory re-resolution, completeness -- none of which
// apply to a side index of foreign ids). The trigger keeps the index in sync on
// the write.

// externalRefEntry is one system's reference as the panel renders it: the system
// code it's keyed under, the correlation id (the scalar, or the object's "id"),
// and the raw value (scalar string or object) so an object ref round-trips edits
// without loss. HasID is false for an object ref with no "id" field (outbound-only,
// e.g. a template that needs no correlation) -- it has no reverse-index row.
type externalRefEntry struct {
	System string          `json:"system"`
	ID     string          `json:"id"`
	HasID  bool            `json:"has_id"`
	Value  json.RawMessage `json:"value"`
}

// featureRefsResponse is GET /feature-links/external-refs: a feature's refs,
// one per system, system-code order (stable for the UI).
type featureRefsResponse struct {
	Refs []externalRefEntry `json:"refs"`
}

// setFeatureRefsRequest is PUT /feature-links/external-refs: the feature plus the
// FULL replacement map (keyed by system_code). A whole-map replace, not a merge --
// the UI holds the current set and sends the next one, same as the attributes edit.
type setFeatureRefsRequest struct {
	FeatureClass string                     `json:"feature_class"`
	FeatureID    string                     `json:"feature_id"`
	ExternalRef  map[string]json.RawMessage `json:"external_ref"`
}

// readFeatureExternalRef reads the external_ref jsonb straight off a feature's
// physical table (the table name is compiled metadata, trusted; the id is bound).
// A null/absent column is an empty map, not an error. deleted_at IS NULL: a
// soft-deleted feature has no refs to manage (and the trigger wouldn't index them).
func readFeatureExternalRef(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, featureID string) (map[string]json.RawMessage, error) {
	if loaded.FeatureClass.PhysicalTable == nil {
		return nil, invalid("feature class %s is not compiled", loaded.FeatureClass.Code)
	}
	var raw []byte
	err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT external_ref FROM %s WHERE id = ? AND deleted_at IS NULL", *loaded.FeatureClass.PhysicalTable),
		featureID,
	).Scan(&raw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("feature %s not found", featureID)
		}
		return nil, fmt.Errorf("externallink: external-refs: read: %w", err)
	}
	if len(raw) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var refs map[string]json.RawMessage
	if err := json.Unmarshal(raw, &refs); err != nil {
		return nil, invalid("the feature's external_ref is not a JSON object")
	}
	return refs, nil
}

// listFeatureExternalRefs reads a feature's external_ref and shapes it for the
// panel: one entry per system in code order, each carrying the correlation id
// (scalar, or the object's "id") and the raw value for round-tripping.
func listFeatureExternalRefs(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, featureID string) (*featureRefsResponse, error) {
	refs, err := readFeatureExternalRef(ctx, db, loaded, featureID)
	if err != nil {
		return nil, err
	}
	codes := make([]string, 0, len(refs))
	for code := range refs {
		codes = append(codes, code)
	}
	sort.Strings(codes)

	out := make([]externalRefEntry, 0, len(codes))
	for _, code := range codes {
		val := refs[code]
		id, hasID := correlationID(val)
		out = append(out, externalRefEntry{System: code, ID: id, HasID: hasID, Value: val})
	}
	return &featureRefsResponse{Refs: out}, nil
}

// correlationID pulls the id a ref value correlates on: the scalar itself for a
// string ref, the "id" field for an object ref, and ("", false) for anything else
// (an object with no "id" is outbound-only and carries no reverse-index row).
func correlationID(val json.RawMessage) (string, bool) {
	var s string
	if err := json.Unmarshal(val, &s); err == nil {
		s = strings.TrimSpace(s)
		return s, s != ""
	}
	var obj map[string]any
	if err := json.Unmarshal(val, &obj); err == nil {
		if id, ok := obj["id"].(string); ok {
			id = strings.TrimSpace(id)
			return id, id != ""
		}
	}
	return "", false
}

// setFeatureExternalRef validates and writes the FULL external_ref map onto a
// feature, replacing whatever was there. Each value must be a non-empty scalar
// string (the id) or an object (whose optional "id" is the correlation id). An
// empty map clears the column to NULL. The reverse-index trigger re-derives
// runtime.external_feature_refs from the new value on the write. The caller MUST
// have enforced write scope already (the handler does, via
// crud.EnforceFeatureWriteScope).
func setFeatureExternalRef(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, featureID string, refs map[string]json.RawMessage) (*featureRefsResponse, error) {
	if loaded.FeatureClass.PhysicalTable == nil {
		return nil, invalid("feature class %s is not compiled", loaded.FeatureClass.Code)
	}

	// Validate every entry before touching the row: a bad value fails the whole
	// request (no partial write), and a validated map is what the trigger indexes.
	cleaned := map[string]json.RawMessage{}
	for code, val := range refs {
		code = strings.TrimSpace(code)
		if code == "" {
			return nil, invalid("an external reference has a blank system code")
		}
		switch jsonType(val) {
		case "string":
			var s string
			_ = json.Unmarshal(val, &s)
			if strings.TrimSpace(s) == "" {
				return nil, invalid("the reference for system %q is an empty id", code)
			}
		case "object":
			// An object ref is allowed with or without an "id" (an outbound-only
			// template may need none) -- shape only is checked here.
		default:
			return nil, invalid("the reference for system %q must be a string id or an object", code)
		}
		cleaned[code] = val
	}

	var payload any
	if len(cleaned) > 0 {
		encoded, err := json.Marshal(cleaned)
		if err != nil {
			return nil, fmt.Errorf("externallink: external-refs: encode: %w", err)
		}
		payload = string(encoded)
	} // else nil -> NULL column (cleared)

	res, err := db.ExecContext(ctx,
		fmt.Sprintf("UPDATE %s SET external_ref = ?::jsonb WHERE id = ? AND deleted_at IS NULL", *loaded.FeatureClass.PhysicalTable),
		payload, featureID,
	)
	if err != nil {
		return nil, fmt.Errorf("externallink: external-refs: write: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, notFound("feature %s not found", featureID)
	}
	return listFeatureExternalRefs(ctx, db, loaded, featureID)
}

// jsonType reports a raw JSON value's type ("string", "object", "number",
// "array", "bool", "null", or "" for empty/invalid) without fully decoding it --
// enough to classify an external_ref value.
func jsonType(raw json.RawMessage) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	switch trimmed[0] {
	case '"':
		return "string"
	case '{':
		return "object"
	case '[':
		return "array"
	case 't', 'f':
		return "bool"
	case 'n':
		return "null"
	default:
		return "number"
	}
}
