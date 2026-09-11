package crud

import (
	"context"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// publishFeature transitions a feature from draft to published. It's
// idempotent -- publishing an already-published row just returns its
// current state without re-running the check below -- but that's only
// safe because updateHandler enforces the same completeness rule on
// every write to an already-published row (see validateUpdateCompleteness).
// Without that, an already-published row could go incomplete via a later
// PATCH and this idempotent short-circuit would let it through silently
// forever, since it never reaches the check again. Found exactly that
// gap by testing the real sequence: publish, then null a required field,
// then publish again -- it returned the stale "published" state instead
// of rejecting.
//
// The check itself matters because "required" and "nullable" are
// different questions. A field can be required for one subtype but stay
// a nullable column so a different subtype (which doesn't require it)
// can still omit it -- so a required field going null via a later PATCH
// isn't necessarily caught by a database NOT NULL constraint.
func publishFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string) (map[string]any, error) {
	current, err := getFeature(ctx, db, loaded, id)
	if err != nil {
		return nil, err
	}
	if current["deleted_at"] != nil {
		return nil, invalid("feature %s is deleted -- restore it before publishing", id)
	}
	if status, _ := current["status"].(string); status == "published" {
		return current, nil
	}

	attrs, _ := current["attributes"].(map[string]any)
	if err := validateCompleteness(loaded, current["subtype"], attrs, "required before publishing"); err != nil {
		return nil, err
	}

	return setStatus(ctx, db, loaded, id, "published")
}

// unpublishFeature reverts a feature to draft. No completeness check --
// draft never had to satisfy one, so reverting to it can't violate it.
func unpublishFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string) (map[string]any, error) {
	current, err := getFeature(ctx, db, loaded, id)
	if err != nil {
		return nil, err
	}
	if current["deleted_at"] != nil {
		return nil, invalid("feature %s is deleted -- restore it before unpublishing", id)
	}
	if status, _ := current["status"].(string); status == "draft" {
		return current, nil
	}

	return setStatus(ctx, db, loaded, id, "draft")
}

// validateCompleteness checks a set of attribute values against
// required-by-subtype rules. Takes the subtype and attributes directly
// (rather than a whole formatRow-shaped row) so it can be reused both
// against a row's stored state (publishFeature) and against a proposed,
// not-yet-written merge of existing + incoming values
// (validateUpdateCompleteness). A feature class with no subtypes has no
// subtype_field_rules rows by construction, so there's nothing to
// enforce -- same behavior validateRequired already has on create.
func validateCompleteness(loaded *metadata.Loaded, rawSubtype any, attrs map[string]any, reason string) error {
	if rawSubtype == nil {
		return nil
	}
	code, ok := toInt(rawSubtype)
	if !ok {
		return nil
	}
	subtype := loaded.SubtypeByCode(code)
	if subtype == nil {
		return nil
	}

	for _, name := range loaded.RequiredFieldNames(subtype.ID) {
		if v, present := attrs[name]; !present || v == nil {
			return invalid("field %q is %s", name, reason)
		}
	}
	return nil
}

// validateUpdateCompleteness enforces the same required-field rule on
// every write to an already-published row, not just at the
// draft-to-published transition -- otherwise a required field can go
// null via a plain PATCH (whenever it's still editable_after_publish)
// and stay that way forever, since publishFeature's idempotent
// already-published short-circuit never re-checks it. current is the
// row's existing state (as returned by getFeature); incoming is the
// coerced values about to be written. incoming wins on overlap, since
// that's what the write will actually leave in place.
func validateUpdateCompleteness(loaded *metadata.Loaded, current map[string]any, incoming map[string]any) error {
	existingAttrs, _ := current["attributes"].(map[string]any)
	merged := make(map[string]any, len(existingAttrs)+len(incoming))
	for k, v := range existingAttrs {
		merged[k] = v
	}
	for k, v := range incoming {
		merged[k] = v
	}
	return validateCompleteness(loaded, current["subtype"], merged, "required while the feature is published")
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int64:
		return int(n), true
	case int32:
		return int(n), true
	case int:
		return n, true
	case float64:
		return int(n), true
	default:
		return 0, false
	}
}

func setStatus(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id, status string) (map[string]any, error) {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return nil, err
	}

	// A status transition (publish/unpublish) is an edit too, so it stamps
	// the actor into updated_by alongside updated_at (Slice E).
	query := fmt.Sprintf(
		"UPDATE %s SET status = ?, updated_at = now(), updated_by = ? WHERE id = ? RETURNING %s",
		table, strings.Join(selectColumns(loaded), ", "),
	)
	rows, err := db.QueryContext(ctx, query, status, actorArg(ctx), id)
	if err != nil {
		return nil, fmt.Errorf("crud: set status: %w", err)
	}
	results, err := scanRows(rows)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, notFound("feature %s not found", id)
	}
	out, err := formatRow(loaded, results[0])
	if err != nil {
		return nil, err
	}
	// Audit trail (Phase 41): record the transition by its direction, so the
	// timeline distinguishes a publish from an unpublish without reading status
	// out of the snapshot. "published" is the only publish target; every other
	// value (today just "draft") is an unpublish.
	action := historyUnpublish
	if status == "published" {
		action = historyPublish
	}
	if err := recordFeatureHistory(ctx, db, loaded, action, out); err != nil {
		return nil, err
	}
	return out, nil
}
