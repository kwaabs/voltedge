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
// current state -- but not a no-op: before the first real transition it
// re-checks every field subtype_field_rules marks required against the
// row's *current* values, not just what was true at create time.
//
// That check matters because "required" and "nullable" are different
// questions. A field can be required for one subtype but stay a nullable
// column so a different subtype (which doesn't require it) can still
// omit it -- so a required field going null via a later PATCH isn't
// necessarily caught by a database NOT NULL constraint. Publish is the
// deliberate gate where completeness actually gets enforced.
func publishFeature(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string) (map[string]any, error) {
	current, err := getFeature(ctx, db, loaded, id)
	if err != nil {
		return nil, err
	}
	if status, _ := current["status"].(string); status == "published" {
		return current, nil
	}

	if err := validateCompleteness(loaded, current); err != nil {
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
	if status, _ := current["status"].(string); status == "draft" {
		return current, nil
	}

	return setStatus(ctx, db, loaded, id, "draft")
}

// validateCompleteness checks a formatRow-shaped row (as returned by
// getFeature) against required-by-subtype rules. A feature class with no
// subtypes has no subtype_field_rules rows by construction, so there's
// nothing to enforce -- same behavior validateRequired already has on
// create.
func validateCompleteness(loaded *metadata.Loaded, current map[string]any) error {
	rawSubtype, ok := current["subtype"]
	if !ok || rawSubtype == nil {
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

	attrs, _ := current["attributes"].(map[string]any)
	for _, name := range loaded.RequiredFieldNames(subtype.ID) {
		if v, present := attrs[name]; !present || v == nil {
			return invalid("field %q is required before publishing", name)
		}
	}
	return nil
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

	query := fmt.Sprintf(
		"UPDATE %s SET status = ?, updated_at = now() WHERE id = ? RETURNING %s",
		table, strings.Join(selectColumns(loaded), ", "),
	)
	rows, err := db.QueryContext(ctx, query, status, id)
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
	return formatRow(loaded, results[0])
}
