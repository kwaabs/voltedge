package crud

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// loadedFixture builds a minimal metadata.Loaded covering every column
// data_type the query builder branches on -- enough for buildCondition/
// buildConditions, which only read FeatureClass.Code, Subtypes, and
// FieldByName. hasSubtypes toggles whether the synthetic "subtype" system
// column is queryable.
func loadedFixture(hasSubtypes bool) *metadata.Loaded {
	l := &metadata.Loaded{
		FeatureClass: &metadata.FeatureClass{Code: "pole"},
		Fields: []metadata.Field{
			{Name: "voltage_class", DataType: "text"},
			{Name: "install_date", DataType: "date"},
			{Name: "span_length", DataType: "numeric"},
			{Name: "pole_count", DataType: "integer"},
			{Name: "is_energized", DataType: "boolean"},
		},
	}
	if hasSubtypes {
		l.Subtypes = []metadata.Subtype{{Code: 1, Name: "wood"}}
	}
	return l
}

func TestBuildCondition(t *testing.T) {
	loaded := loadedFixture(true)

	cases := []struct {
		name    string
		cond    queryCondition
		wantSQL string
		wantArg []any
	}{
		{
			name:    "eq on system name column",
			cond:    queryCondition{Field: "name", Op: "eq", Value: "substation-a"},
			wantSQL: `"name" = ?`,
			wantArg: []any{"substation-a"},
		},
		{
			name:    "ne on text field",
			cond:    queryCondition{Field: "voltage_class", Op: "ne", Value: "11kv"},
			wantSQL: `"voltage_class" <> ?`,
			wantArg: []any{"11kv"},
		},
		{
			name:    "contains wraps the value in percent signs",
			cond:    queryCondition{Field: "voltage_class", Op: "contains", Value: "11"},
			wantSQL: `"voltage_class" ILIKE ?`,
			wantArg: []any{"%11%"},
		},
		{
			name:    "starts_with anchors on the left only",
			cond:    queryCondition{Field: "voltage_class", Op: "starts_with", Value: "11"},
			wantSQL: `"voltage_class" ILIKE ?`,
			wantArg: []any{"11%"},
		},
		{
			name:    "gte on a date field",
			cond:    queryCondition{Field: "install_date", Op: "gte", Value: "2020-01-01"},
			wantSQL: `"install_date" >= ?`,
			wantArg: []any{"2020-01-01"},
		},
		{
			name:    "between on a numeric field",
			cond:    queryCondition{Field: "span_length", Op: "between", Values: []any{10.0, 20.0}},
			wantSQL: `"span_length" BETWEEN ? AND ?`,
			wantArg: []any{10.0, 20.0},
		},
		{
			name:    "in on a text field expands one placeholder per value",
			cond:    queryCondition{Field: "voltage_class", Op: "in", Values: []any{"11kv", "33kv"}},
			wantSQL: `"voltage_class" IN (?, ?)`,
			wantArg: []any{"11kv", "33kv"},
		},
		{
			name:    "is_null takes no argument",
			cond:    queryCondition{Field: "install_date", Op: "is_null"},
			wantSQL: `"install_date" IS NULL`,
			wantArg: nil,
		},
		{
			name:    "not_null takes no argument",
			cond:    queryCondition{Field: "install_date", Op: "not_null"},
			wantSQL: `"install_date" IS NOT NULL`,
			wantArg: nil,
		},
		{
			name:    "eq on the subtype system column",
			cond:    queryCondition{Field: "subtype", Op: "eq", Value: 1.0},
			wantSQL: `"subtype" = ?`,
			wantArg: []any{1.0},
		},
		{
			name:    "eq on a boolean field",
			cond:    queryCondition{Field: "is_energized", Op: "eq", Value: true},
			wantSQL: `"is_energized" = ?`,
			wantArg: []any{true},
		},
		{
			name:    "operator is case- and space-insensitive",
			cond:    queryCondition{Field: "span_length", Op: " GTE ", Value: 5.0},
			wantSQL: `"span_length" >= ?`,
			wantArg: []any{5.0},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotSQL, gotArg, err := buildCondition(loaded, c.cond)
			if err != nil {
				t.Fatalf("buildCondition(%+v) unexpected error: %v", c.cond, err)
			}
			if gotSQL != c.wantSQL {
				t.Errorf("SQL = %q, want %q", gotSQL, c.wantSQL)
			}
			if !reflect.DeepEqual(gotArg, c.wantArg) {
				t.Errorf("args = %#v, want %#v", gotArg, c.wantArg)
			}
		})
	}
}

func TestBuildConditionRejects(t *testing.T) {
	loaded := loadedFixture(true)
	noSubtypes := loadedFixture(false)

	cases := []struct {
		name   string
		loaded *metadata.Loaded
		cond   queryCondition
	}{
		{"unknown field", loaded, queryCondition{Field: "nope", Op: "eq", Value: "x"}},
		{"geom is not filterable", loaded, queryCondition{Field: "geom", Op: "eq", Value: "x"}},
		{"deleted_at is not filterable", loaded, queryCondition{Field: "deleted_at", Op: "not_null"}},
		{"subtype absent when class has none", noSubtypes, queryCondition{Field: "subtype", Op: "eq", Value: 1.0}},
		{"contains on a numeric field", loaded, queryCondition{Field: "span_length", Op: "contains", Value: "5"}},
		{"contains with a non-string value", loaded, queryCondition{Field: "voltage_class", Op: "contains", Value: 5.0}},
		{"gt on a text field", loaded, queryCondition{Field: "voltage_class", Op: "gt", Value: "a"}},
		{"gt on a boolean field", loaded, queryCondition{Field: "is_energized", Op: "gt", Value: true}},
		{"between on a text field", loaded, queryCondition{Field: "voltage_class", Op: "between", Values: []any{"a", "z"}}},
		{"between with one value", loaded, queryCondition{Field: "span_length", Op: "between", Values: []any{10.0}}},
		{"in with an empty list", loaded, queryCondition{Field: "voltage_class", Op: "in", Values: []any{}}},
		{"eq with a nil value", loaded, queryCondition{Field: "voltage_class", Op: "eq"}},
		{"unknown operator", loaded, queryCondition{Field: "voltage_class", Op: "wat", Value: "x"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, err := buildCondition(c.loaded, c.cond)
			if err == nil {
				t.Fatalf("buildCondition(%+v) = nil error, want a validation error", c.cond)
			}
			if !isValidationError(err) {
				t.Fatalf("error %v is not a validationError (would not map to 400)", err)
			}
		})
	}
}

func TestBuildGroup(t *testing.T) {
	loaded := loadedFixture(true)

	conds := []queryCondition{
		{Field: "voltage_class", Op: "eq", Value: "11kv"},
		{Field: "span_length", Op: "gte", Value: 5.0},
	}

	t.Run("match all joins with AND and parenthesizes", func(t *testing.T) {
		sql, args, err := buildGroup(loaded, queryGroup{Match: "all", Conditions: conds}, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := `("voltage_class" = ? AND "span_length" >= ?)`
		if sql != want {
			t.Errorf("SQL = %q, want %q", sql, want)
		}
		if !reflect.DeepEqual(args, []any{"11kv", 5.0}) {
			t.Errorf("args = %#v", args)
		}
	})

	t.Run("match any joins with OR (case-insensitive)", func(t *testing.T) {
		sql, _, err := buildGroup(loaded, queryGroup{Match: "ANY", Conditions: conds}, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(sql, " OR ") {
			t.Errorf("SQL = %q, want an OR join", sql)
		}
	})

	t.Run("empty group yields no fragment", func(t *testing.T) {
		sql, args, err := buildGroup(loaded, queryGroup{Match: "all"}, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sql != "" || args != nil {
			t.Errorf("want empty fragment, got SQL=%q args=%#v", sql, args)
		}
	})

	t.Run("nested group composes as (A AND B) OR (C)", func(t *testing.T) {
		root := queryGroup{
			Match:      "any",
			Conditions: []queryCondition{{Field: "voltage_class", Op: "eq", Value: "33kv"}},
			Groups: []queryGroup{{
				Match: "all",
				Conditions: []queryCondition{
					{Field: "span_length", Op: "gte", Value: 5.0},
					{Field: "is_energized", Op: "eq", Value: true},
				},
			}},
		}
		sql, args, err := buildGroup(loaded, root, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := `("voltage_class" = ? OR ("span_length" >= ? AND "is_energized" = ?))`
		if sql != want {
			t.Errorf("SQL = %q, want %q", sql, want)
		}
		if !reflect.DeepEqual(args, []any{"33kv", 5.0, true}) {
			t.Errorf("args = %#v", args)
		}
	})

	t.Run("an empty nested group contributes nothing", func(t *testing.T) {
		root := queryGroup{
			Match:      "all",
			Conditions: conds,
			Groups:     []queryGroup{{Match: "all"}},
		}
		sql, _, err := buildGroup(loaded, root, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := `("voltage_class" = ? AND "span_length" >= ?)`
		if sql != want {
			t.Errorf("SQL = %q, want %q (empty sub-group should drop out)", sql, want)
		}
	})

	t.Run("too many conditions across the tree is rejected", func(t *testing.T) {
		// Split the over-limit count across a nested group to prove the cap
		// counts the whole tree, not just the root's own conditions.
		half := make([]queryCondition, maxQueryConditions)
		for i := range half {
			half[i] = queryCondition{Field: "voltage_class", Op: "eq", Value: "x"}
		}
		root := queryGroup{
			Match:      "all",
			Conditions: half,
			Groups:     []queryGroup{{Match: "all", Conditions: []queryCondition{{Field: "voltage_class", Op: "eq", Value: "x"}}}},
		}
		if _, _, err := buildGroup(loaded, root, 0); err == nil || !isValidationError(err) {
			t.Fatalf("want a validation error for over-limit conditions, got %v", err)
		}
	})

	t.Run("nesting deeper than the limit is rejected", func(t *testing.T) {
		// Build a chain one level past maxGroupDepth.
		leaf := queryGroup{Match: "all", Conditions: []queryCondition{{Field: "voltage_class", Op: "eq", Value: "x"}}}
		g := leaf
		for i := 0; i < maxGroupDepth+1; i++ {
			g = queryGroup{Match: "all", Groups: []queryGroup{g}}
		}
		if _, _, err := buildGroup(loaded, g, 0); err == nil || !isValidationError(err) {
			t.Fatalf("want a validation error for over-deep nesting, got %v", err)
		}
	})

	t.Run("a bad condition inside a nested group surfaces its error", func(t *testing.T) {
		root := queryGroup{
			Match:  "all",
			Groups: []queryGroup{{Match: "all", Conditions: []queryCondition{{Field: "nope", Op: "eq", Value: "x"}}}},
		}
		if _, _, err := buildGroup(loaded, root, 0); err == nil {
			t.Fatal("want an error for an unknown field nested in a sub-group")
		}
	})
}

func TestCountConditions(t *testing.T) {
	root := queryGroup{
		Conditions: []queryCondition{{Field: "a", Op: "eq", Value: 1}},
		Groups: []queryGroup{
			{Conditions: []queryCondition{{Field: "b", Op: "eq", Value: 1}, {Field: "c", Op: "eq", Value: 1}}},
			{Groups: []queryGroup{{Conditions: []queryCondition{{Field: "d", Op: "eq", Value: 1}}}}},
		},
	}
	if got := countConditions(root); got != 4 {
		t.Fatalf("countConditions = %d, want 4", got)
	}
}
