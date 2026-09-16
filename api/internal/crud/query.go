package crud

import (
	"context"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// maxQueryConditions caps how many clauses a single query can carry across
// the whole group tree -- a generous ceiling that a real builder never
// approaches, there only so a malformed or hostile request can't hand
// Postgres a WHERE clause with thousands of ANDed terms.
const maxQueryConditions = 50

// maxGroupDepth bounds how deeply query groups can nest (Slice 4). The root
// is depth 0, so this allows the root plus five levels of nested groups --
// far more than any real `(A AND B) OR (C AND D)` needs, there only to keep
// a hand-crafted body from recursing without limit.
const maxGroupDepth = 5

// comparisonSQL maps the ordered-comparison operator names the API accepts
// to their SQL. contains/starts_with (ILIKE) and in/between are handled
// separately in buildCondition since they don't fit this "col OP ?" shape.
var comparisonSQL = map[string]string{"lt": "<", "lte": "<=", "gt": ">", "gte": ">="}

// isOrderedType reports whether a column's data_type supports the ordered
// comparison operators (lt/lte/gt/gte/between). Deliberately excludes text
// and boolean: a "greater than" on a name or a flag is almost always a
// mistake worth a clean 400, not a silently-accepted lexicographic compare.
func isOrderedType(dataType string) bool {
	switch dataType {
	case "numeric", "integer", "date", "timestamp":
		return true
	}
	return false
}

// columnDataType resolves a queryable column name to its data_type,
// covering both the system columns every feature class has and this
// class's own admin-authored fields. Returns ok=false for anything else
// -- an unknown field, or a column the query builder isn't allowed to
// filter on (geom, deleted_at: the latter is always forced NULL by
// queryFeatures, so exposing it as a filter target would only invite
// contradictions). This is the whitelist that keeps a user-supplied field
// name from ever reaching SQL unchecked.
func columnDataType(loaded *metadata.Loaded, field string) (string, bool) {
	switch field {
	case "name", "status":
		return "text", true
	case "id":
		return "uuid", true
	case "created_at", "updated_at":
		return "timestamp", true
	case "subtype":
		if len(loaded.Subtypes) > 0 {
			return "integer", true
		}
		return "", false
	}
	if f := loaded.FieldByName(field); f != nil {
		return f.DataType, true
	}
	return "", false
}

// buildCondition turns one validated queryCondition into a SQL fragment
// plus its bind args. Every value is a bind param (never interpolated);
// the only things spliced into the SQL text are the double-quoted column
// name (from the columnDataType whitelist) and a fixed operator token, so
// nothing a client sends can alter the query's structure.
func buildCondition(loaded *metadata.Loaded, c queryCondition) (string, []any, error) {
	dataType, ok := columnDataType(loaded, c.Field)
	if !ok {
		return "", nil, invalid("unknown or non-filterable field %q on feature class %q", c.Field, loaded.FeatureClass.Code)
	}
	col := quoteIdent(c.Field)
	op := strings.ToLower(strings.TrimSpace(c.Op))

	switch op {
	case "is_null":
		return col + " IS NULL", nil, nil
	case "not_null":
		return col + " IS NOT NULL", nil, nil

	case "eq":
		if c.Value == nil {
			return "", nil, invalid("operator %q on field %q needs a value", op, c.Field)
		}
		return col + " = ?", []any{c.Value}, nil
	case "ne":
		if c.Value == nil {
			return "", nil, invalid("operator %q on field %q needs a value", op, c.Field)
		}
		return col + " <> ?", []any{c.Value}, nil

	case "lt", "lte", "gt", "gte":
		if !isOrderedType(dataType) {
			return "", nil, invalid("operator %q is not valid for field %q (a %s field)", op, c.Field, dataType)
		}
		if c.Value == nil {
			return "", nil, invalid("operator %q on field %q needs a value", op, c.Field)
		}
		return col + " " + comparisonSQL[op] + " ?", []any{c.Value}, nil

	case "contains", "starts_with":
		if dataType != "text" {
			return "", nil, invalid("operator %q is only valid for text fields, but %q is a %s field", op, c.Field, dataType)
		}
		s, ok := c.Value.(string)
		if !ok {
			return "", nil, invalid("operator %q on field %q needs a string value", op, c.Field)
		}
		// ILIKE for case-insensitive matching, same as the list endpoint's
		// name search. "%"/"_" in the typed value stay unescaped -- treating
		// them as wildcards inside a human search string is harmless (at
		// worst a slightly broader match), matching listFeatures' own note.
		pattern := "%" + s + "%"
		if op == "starts_with" {
			pattern = s + "%"
		}
		return col + " ILIKE ?", []any{pattern}, nil

	case "in":
		if len(c.Values) == 0 {
			return "", nil, invalid("operator \"in\" on field %q needs a non-empty values array", c.Field)
		}
		placeholders := make([]string, len(c.Values))
		for i := range c.Values {
			placeholders[i] = "?"
		}
		return col + " IN (" + strings.Join(placeholders, ", ") + ")", append([]any{}, c.Values...), nil

	case "between":
		if !isOrderedType(dataType) {
			return "", nil, invalid("operator \"between\" is not valid for field %q (a %s field)", c.Field, dataType)
		}
		if len(c.Values) != 2 {
			return "", nil, invalid("operator \"between\" on field %q needs exactly two values", c.Field)
		}
		return col + " BETWEEN ? AND ?", []any{c.Values[0], c.Values[1]}, nil

	default:
		return "", nil, invalid("unknown operator %q", c.Op)
	}
}

// countConditions totals the leaf conditions across a group and all of its
// descendants -- the whole-tree count the maxQueryConditions cap is checked
// against (the cap is a ceiling on real filter clauses, which nesting must
// not be able to route around).
func countConditions(g queryGroup) int {
	n := len(g.Conditions)
	for _, sub := range g.Groups {
		n += countConditions(sub)
	}
	return n
}

// buildGroup folds one group into a single parenthesized SQL fragment: its
// own leaf conditions and each nested sub-group's fragment, all joined by AND
// (match "all", the default) or OR (match "any"). Returns an empty fragment
// for an empty group -- a filter-less query is legal and just pages the whole
// class, exactly as the list endpoint does. Every group is parenthesized so a
// nested OR can't change how the level above it binds, and so the root, when
// ANDed with the always-present `deleted_at IS NULL` guard, can't leak deleted
// rows. depth is the recursion guard (root = 0); the whole-tree condition-count
// cap is enforced once at the root so nesting can't smuggle past it.
func buildGroup(loaded *metadata.Loaded, g queryGroup, depth int) (string, []any, error) {
	if depth == 0 {
		if n := countConditions(g); n > maxQueryConditions {
			return "", nil, invalid("too many conditions (%d); the maximum is %d", n, maxQueryConditions)
		}
	}
	if depth > maxGroupDepth {
		return "", nil, invalid("query groups are nested too deeply; the maximum is %d levels", maxGroupDepth)
	}

	joiner := " AND "
	if strings.EqualFold(strings.TrimSpace(g.Match), "any") {
		joiner = " OR "
	}

	frags := make([]string, 0, len(g.Conditions)+len(g.Groups))
	args := make([]any, 0, len(g.Conditions))
	for _, c := range g.Conditions {
		frag, cArgs, err := buildCondition(loaded, c)
		if err != nil {
			return "", nil, err
		}
		frags = append(frags, frag)
		args = append(args, cArgs...)
	}
	for _, sub := range g.Groups {
		frag, sArgs, err := buildGroup(loaded, sub, depth+1)
		if err != nil {
			return "", nil, err
		}
		// An empty sub-group contributes nothing rather than an empty "()".
		if frag == "" {
			continue
		}
		frags = append(frags, frag)
		args = append(args, sArgs...)
	}

	if len(frags) == 0 {
		return "", nil, nil
	}
	return "(" + strings.Join(frags, joiner) + ")", args, nil
}

// queryFeatures runs a structured query and returns the matching rows in
// the same envelope as listFeatures -- it's the typed-operator/AND-OR
// counterpart to that endpoint's flat name/status filter, sharing all of
// its read machinery (selectColumns/scanRows/formatRow) and the same
// always-on `deleted_at IS NULL` exclusion and stable `id` sort tiebreak.
func queryFeatures(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, req queryRequest) ([]map[string]any, error) {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return nil, err
	}

	where := []string{"deleted_at IS NULL"}
	args := make([]any, 0, len(req.Conditions)+2)

	// The request is itself the root group -- its Match combines its own
	// Conditions with any nested Groups (Slice 4). A flat query leaves Groups
	// empty and this produces exactly the Slice 1 SQL.
	root := queryGroup{Match: req.Match, Conditions: req.Conditions, Groups: req.Groups}
	condSQL, condArgs, err := buildGroup(loaded, root, 0)
	if err != nil {
		return nil, err
	}
	if condSQL != "" {
		where = append(where, condSQL)
		args = append(args, condArgs...)
	}

	// Phase 58 spatial read enforcement: same territory constraint as
	// listFeatures, ANDed on top of the user's own conditions (a no-op unless
	// enforcement is on and the user is scoped; fail-closed for a
	// scoped-to-nothing user). See readScope.
	scopeIDs, scopeApply, err := readScope(ctx, db, loaded)
	if err != nil {
		return nil, err
	}
	if clause, scopeArgs := scopeWhere(scopeIDs, scopeApply); clause != "" {
		where = append(where, clause)
		args = append(args, scopeArgs...)
	}

	// Sort/paging: identical handling to listFeatures -- an unrecognized
	// sort column silently falls back to created_at, id is always appended
	// as a tiebreaker, limit clamps to [1,500] defaulting to defaultListLimit.
	sortCol := "created_at"
	if req.Sort != "" && validSortColumns(loaded)[req.Sort] {
		sortCol = req.Sort
	}
	direction := "ASC"
	if strings.EqualFold(req.Order, "desc") {
		direction = "DESC"
	}
	orderBy := quoteIdent(sortCol) + " " + direction
	if sortCol != "id" {
		orderBy += ", id"
	}

	limit := defaultListLimit
	if req.Limit > 0 && req.Limit <= 500 {
		limit = req.Limit
	}
	offset := 0
	if req.Offset > 0 {
		offset = req.Offset
	}

	query := fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s ORDER BY %s LIMIT ? OFFSET ?",
		strings.Join(selectColumns(loaded), ", "), table, strings.Join(where, " AND "), orderBy,
	)
	args = append(args, limit, offset)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("crud: query: %w", err)
	}
	results, err := scanRows(rows)
	if err != nil {
		return nil, err
	}

	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		formatted, err := formatRow(loaded, r)
		if err != nil {
			return nil, err
		}
		out = append(out, formatted)
	}
	return out, nil
}
