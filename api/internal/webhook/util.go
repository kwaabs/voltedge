package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/auth"
)

// defaulted returns a trimmed value, or the fallback when it is empty.
func defaulted(v, fallback string) string {
	if t := strings.TrimSpace(v); t != "" {
		return t
	}
	return fallback
}

func boolOr(p *bool, fallback bool) bool {
	if p != nil {
		return *p
	}
	return fallback
}

// nilIfEmpty returns a pointer to a trimmed non-empty string, or nil -- so a
// sparse patch (wam/faults update params) only touches a column the sender
// actually set, and a resent-in-full object never blanks a required field.
func nilIfEmpty(s string) *string {
	if t := strings.TrimSpace(s); t != "" {
		return &t
	}
	return nil
}

// requirePerm asserts the endpoint's service-user context holds a permission
// before a non-feature adapter calls an engine function that doesn't self-check
// RBAC (wam/faults enforce permissions only at their REST middleware, which
// ingestion bypasses). A miss is an authError, so the delivery dead-letters as a
// permanent failure the worker never retries -- the same terminal outcome a REST
// 403 would be. The feature adapter needs no such guard: crud enforces territory
// write-scope itself on every write.
func requirePerm(ctx context.Context, perm string) error {
	u, ok := auth.UserFrom(ctx)
	if !ok || u == nil || !u.Has(perm) {
		return unauthorized("the endpoint's service user lacks %s", perm)
	}
	return nil
}

// nilIfEmptyJSON normalizes an absent/empty/`null` raw JSON to nil so the column
// stores SQL NULL rather than the literal string "null".
func nilIfEmptyJSON(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	return raw
}

// orEmptyObject guarantees a non-null JSON object for columns/fields that must
// never be null on the wire (config).
func orEmptyObject(raw json.RawMessage) json.RawMessage {
	if v := nilIfEmptyJSON(raw); v != nil {
		return v
	}
	return json.RawMessage(`{}`)
}

// actorID is the acting user id for created_by stamping, or "" (anonymous).
func actorID(ctx context.Context) string { return auth.ActorID(ctx) }
