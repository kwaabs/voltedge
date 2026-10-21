package assetlife

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

const (
	// daysPerYear converts an age in days to fractional years (accounts for leap
	// years on average) -- the report is a planning signal, not an actuarial one.
	daysPerYear = 365.25
	// reportDefaultDueWithinYears: an in-service asset within this many years of
	// its expected end of life is flagged DUE_SOON (not yet overdue).
	reportDefaultDueWithinYears = 2.0
	// reportDefaultMaxItems caps the returned per-asset rows; the summary counts
	// are never capped (every scanned asset is counted).
	reportDefaultMaxItems = 1000
)

// AgingInput bounds a report run. All optional.
type AgingInput struct {
	FeatureClass   string  // limit to one class; empty = every catalogued class
	DueWithinYears float64 // the DUE_SOON window; <=0 uses the default
	MaxItems       int     // cap on returned items; <=0 uses the default
}

// AgingItem is one asset's standing against its expected life.
type AgingItem struct {
	FeatureClass    string   `json:"feature_class"`
	FeatureID       string   `json:"feature_id"`
	Name            *string  `json:"name,omitempty"`
	Subtype         *int     `json:"subtype,omitempty"`
	LifecycleStatus string   `json:"lifecycle_status"`
	InServiceSince  string   `json:"in_service_since"` // YYYY-MM-DD
	AgeYears        float64  `json:"age_years"`
	ExpectedYears   int      `json:"expected_years"`
	RemainingYears  float64  `json:"remaining_years"`
	Bucket          string   `json:"bucket"` // overdue / due_soon / ok
}

// ClassSummary rolls the buckets up per class.
type ClassSummary struct {
	FeatureClass string `json:"feature_class"`
	Total        int    `json:"total"`
	Overdue      int    `json:"overdue"`
	DueSoon      int    `json:"due_soon"`
	Ok          int     `json:"ok"`
}

// AgingReport is the whole result: overall + per-class counts and the worst-first
// per-asset list (capped).
type AgingReport struct {
	GeneratedAt    time.Time      `json:"generated_at"`
	DueWithinYears float64        `json:"due_within_years"`
	Total          int            `json:"total"`
	Overdue        int            `json:"overdue"`
	DueSoon        int            `json:"due_soon"`
	Ok             int            `json:"ok"`
	ByClass        []ClassSummary `json:"by_class"`
	Items          []AgingItem    `json:"items"`
	Truncated      bool           `json:"truncated"`
}

const (
	bucketOverdue = "overdue"
	bucketDueSoon = "due_soon"
	bucketOk      = "ok"
)

// classExpectancy holds a class's resolved expected-life lookup: a default (the
// subtype-NULL row) and any per-subtype overrides.
type classExpectancy struct {
	defaultYears *int
	bySubtype    map[int]int
}

// resolve returns the expected years for a feature of the given subtype, honoring
// subtype override then class default. ok is false when neither applies (that
// feature simply isn't covered and is skipped).
func (c classExpectancy) resolve(subtype *int) (int, bool) {
	if subtype != nil {
		if y, ok := c.bySubtype[*subtype]; ok {
			return y, true
		}
	}
	if c.defaultYears != nil {
		return *c.defaultYears, true
	}
	return 0, false
}

// Aging builds the asset-lifecycle aging report. For every class that has a
// catalog entry it scans the live, operational (IN_SERVICE/DERATED) features,
// derives each one's in-service age from commissioned_at (falling back to
// created_at), and buckets it against the expected life. PLANNED / retired /
// removed assets are excluded -- the report is about what is actually in service
// and aging. Writes nothing.
func Aging(ctx context.Context, db bun.IDB, in AgingInput) (*AgingReport, error) {
	dueWithin := in.DueWithinYears
	if dueWithin <= 0 {
		dueWithin = reportDefaultDueWithinYears
	}
	maxItems := in.MaxItems
	if maxItems <= 0 {
		maxItems = reportDefaultMaxItems
	}

	cat, err := ListExpectancies(ctx, db, in.FeatureClass)
	if err != nil {
		return nil, err
	}
	byClass := map[string]*classExpectancy{}
	var order []string
	for i := range cat {
		row := cat[i]
		ce, ok := byClass[row.FeatureClassCode]
		if !ok {
			ce = &classExpectancy{bySubtype: map[int]int{}}
			byClass[row.FeatureClassCode] = ce
			order = append(order, row.FeatureClassCode)
		}
		if row.SubtypeCode == nil {
			y := row.ExpectedYears
			ce.defaultYears = &y
		} else {
			ce.bySubtype[*row.SubtypeCode] = row.ExpectedYears
		}
	}

	now := time.Now()
	report := &AgingReport{GeneratedAt: now, DueWithinYears: dueWithin}
	var all []AgingItem

	for _, code := range order {
		ce := byClass[code]
		loaded, err := metadata.Load(ctx, db, code)
		if err != nil || loaded.FeatureClass.PhysicalTable == nil {
			continue // unknown / not compiled -- nothing to scan
		}
		hasSubtypes := len(loaded.Subtypes) > 0

		items, err := scanClassAging(ctx, db, *loaded.FeatureClass.PhysicalTable, code, hasSubtypes, *ce, now, dueWithin)
		if err != nil {
			return nil, err
		}
		cs := ClassSummary{FeatureClass: code}
		for _, it := range items {
			cs.Total++
			switch it.Bucket {
			case bucketOverdue:
				cs.Overdue++
			case bucketDueSoon:
				cs.DueSoon++
			default:
				cs.Ok++
			}
		}
		report.ByClass = append(report.ByClass, cs)
		report.Total += cs.Total
		report.Overdue += cs.Overdue
		report.DueSoon += cs.DueSoon
		report.Ok += cs.Ok
		all = append(all, items...)
	}

	// Worst-first: least remaining life at the top (the replacement work-list).
	sort.SliceStable(all, func(i, j int) bool { return all[i].RemainingYears < all[j].RemainingYears })
	if len(all) > maxItems {
		all = all[:maxItems]
		report.Truncated = true
	}
	report.Items = all
	if report.Items == nil {
		report.Items = []AgingItem{}
	}
	if report.ByClass == nil {
		report.ByClass = []ClassSummary{}
	}
	return report, nil
}

// scanClassAging reads one class's live, operational features and buckets each
// against its expected life. physicalTable is the compiler-generated,
// schema-qualified table name (trusted metadata, same as attribute_audit uses).
func scanClassAging(ctx context.Context, db bun.IDB, physicalTable, code string, hasSubtypes bool, ce classExpectancy, now time.Time, dueWithin float64) ([]AgingItem, error) {
	cols := "id::text AS id, name, lifecycle_status, commissioned_at, created_at"
	if hasSubtypes {
		cols += ", subtype"
	}
	// Only assets actually in service age toward replacement; PLANNED / retired /
	// removed are out of scope (matches the conduction gate's operational set).
	query := fmt.Sprintf(
		"SELECT %s FROM %s WHERE deleted_at IS NULL AND lifecycle_status IN ('IN_SERVICE', 'DERATED')",
		cols, physicalTable,
	)
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("assetlife: scanning %s: %w", code, err)
	}
	scanned, err := scanRows(rows)
	if err != nil {
		return nil, err
	}

	out := make([]AgingItem, 0, len(scanned))
	for _, row := range scanned {
		var subtype *int
		if hasSubtypes {
			if n, ok := toInt(row["subtype"]); ok {
				subtype = &n
			}
		}
		expected, ok := ce.resolve(subtype)
		if !ok {
			continue // this subtype has no expectancy and no class default
		}

		// In service since commissioned_at, else created_at (the row has always
		// existed since creation, a safe lower bound when no commission date was set).
		inService, ok := toTime(row["commissioned_at"])
		if !ok {
			inService, ok = toTime(row["created_at"])
			if !ok {
				continue // no usable date -- can't age it
			}
		}

		ageYears := now.Sub(inService).Hours() / 24 / daysPerYear
		remaining := float64(expected) - ageYears
		bucket := bucketOk
		switch {
		case remaining <= 0:
			bucket = bucketOverdue
		case remaining <= dueWithin:
			bucket = bucketDueSoon
		}

		item := AgingItem{
			FeatureClass:    code,
			FeatureID:       asString(row["id"]),
			Subtype:         subtype,
			LifecycleStatus: asString(row["lifecycle_status"]),
			InServiceSince:  inService.Format("2006-01-02"),
			AgeYears:        round1(ageYears),
			ExpectedYears:   expected,
			RemainingYears:  round1(remaining),
			Bucket:          bucket,
		}
		if n := asString(row["name"]); n != "" {
			item.Name = &n
		}
		out = append(out, item)
	}
	return out, nil
}

// --- generic scan + value helpers (mirror crud's own, kept local) ----------

func scanRows(rows *sql.Rows) ([]map[string]any, error) {
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("assetlife: reading columns: %w", err)
	}
	var results []map[string]any
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("assetlife: scanning row: %w", err)
		}
		row := make(map[string]any, len(columns))
		for i, col := range columns {
			if b, ok := values[i].([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = values[i]
			}
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

func asString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", s)
	}
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

// toTime normalizes a scanned date/timestamp into a time.Time. pgdriver can hand
// back either a real time.Time or a text form depending on the column and path.
func toTime(v any) (time.Time, bool) {
	switch t := v.(type) {
	case time.Time:
		return t, true
	case string:
		return parseTime(t)
	case []byte:
		return parseTime(string(t))
	default:
		return time.Time{}, false
	}
}

func parseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02 15:04:05.999999-07", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func round1(f float64) float64 {
	return float64(int64(f*10+sign(f)*0.5)) / 10
}

func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
}
