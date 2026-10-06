package basemap

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/uptrace/bun"
)

// InvalidError is a client-fixable problem (blank label, malformed URL
// template, a duplicate code, an out-of-range zoom, deleting a built-in) --
// distinct from a plain error so the HTTP layer maps it to 400 not 500, the
// same split api/internal/circuit draws. Callers test with IsInvalid.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalidf(format string, args ...any) error {
	return &InvalidError{msg: fmt.Sprintf(format, args...)}
}

// IsInvalid reports whether err is (or wraps) an InvalidError.
func IsInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

// ListBasemaps returns basemaps ordered by sort_order then label. When
// enabledOnly is set (the map's request) disabled rows are dropped; the admin
// page passes false to see every row. The set is tiny (a handful of tile
// sources), so it's fetched whole.
func ListBasemaps(ctx context.Context, db bun.IDB, enabledOnly bool) ([]Basemap, error) {
	var basemaps []Basemap
	q := db.NewSelect().Model(&basemaps).Order("sort_order", "label")
	if enabledOnly {
		q = q.Where("is_enabled = true")
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("basemap: listing basemaps: %w", err)
	}
	// bun leaves a JSON `[]` as a nil slice; normalise so the API always emits
	// an array, never null, for attribution.
	for i := range basemaps {
		if basemaps[i].Attribution == nil {
			basemaps[i].Attribution = []AttributionLink{}
		}
	}
	return basemaps, nil
}

// GetBasemap fetches one basemap by id.
func GetBasemap(ctx context.Context, db bun.IDB, id string) (*Basemap, error) {
	b := new(Basemap)
	if err := db.NewSelect().Model(b).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("basemap %q does not exist", id)
		}
		return nil, fmt.Errorf("basemap: loading basemap %s: %w", id, err)
	}
	if b.Attribution == nil {
		b.Attribution = []AttributionLink{}
	}
	return b, nil
}

// CreateParams is the input to CreateBasemap. A created basemap is always a
// custom one (is_builtin false, set by the DB default) -- only migration 0077's
// seed marks built-ins.
type CreateParams struct {
	Code        string
	Label       string
	URLTemplate string
	Attribution []AttributionLink
	MaxZoom     *int
	MinZoom     int
	TMS         bool
	SortOrder   int
	IsEnabled   bool
	UpdatedBy   string
}

// CreateBasemap validates and inserts a custom basemap. Code uniqueness is
// pre-checked so the DB's UNIQUE(code) violation surfaces as a clear 400.
func CreateBasemap(ctx context.Context, db bun.IDB, p CreateParams) (*Basemap, error) {
	code := strings.TrimSpace(p.Code)
	label := strings.TrimSpace(p.Label)
	tmpl := strings.TrimSpace(p.URLTemplate)
	if err := validateCode(code); err != nil {
		return nil, err
	}
	if label == "" {
		return nil, invalidf("basemap label is required")
	}
	if err := validateTemplate(tmpl); err != nil {
		return nil, err
	}
	if err := validateZoom(p.MinZoom, p.MaxZoom); err != nil {
		return nil, err
	}
	attribution := normaliseAttribution(p.Attribution)
	if err := ensureCodeFree(ctx, db, code, ""); err != nil {
		return nil, err
	}

	row := &Basemap{
		Code:        code,
		Label:       label,
		URLTemplate: tmpl,
		Attribution: attribution,
		MaxZoom:     p.MaxZoom,
		MinZoom:     p.MinZoom,
		TMS:         p.TMS,
		SortOrder:   p.SortOrder,
		IsEnabled:   p.IsEnabled,
		UpdatedBy:   nullableID(p.UpdatedBy),
	}
	_, err := db.NewInsert().Model(row).
		Column("code", "label", "url_template", "attribution", "max_zoom",
			"min_zoom", "tms", "sort_order", "is_enabled", "updated_by").
		Returning("id").
		Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("basemap: inserting basemap: %w", err)
	}
	return GetBasemap(ctx, db, row.ID)
}

// UpdateParams is the input to UpdateBasemap. Every field is optional -- only
// what's set changes. Code is intentionally absent: it is the stable client id
// (persisted per browser, looked up by the map), so it is immutable -- an admin
// who needs a new code deletes and recreates. ClearMaxZoom removes the zoom cap
// (distinct from "leave it alone").
type UpdateParams struct {
	Label        *string
	URLTemplate  *string
	Attribution  *[]AttributionLink
	MaxZoom      *int
	ClearMaxZoom bool
	MinZoom      *int
	TMS          *bool
	SortOrder    *int
	IsEnabled    *bool
	UpdatedBy    string
}

// UpdateBasemap edits a basemap in place. A built-in may be edited (relabelled,
// re-pointed, reordered, disabled) -- only DELETE is blocked for built-ins.
func UpdateBasemap(ctx context.Context, db bun.IDB, id string, p UpdateParams) (*Basemap, error) {
	existing, err := GetBasemap(ctx, db, id)
	if err != nil {
		return nil, err
	}

	q := db.NewUpdate().Model((*Basemap)(nil)).Where("id = ?", id)
	changed := false

	if p.Label != nil {
		label := strings.TrimSpace(*p.Label)
		if label == "" {
			return nil, invalidf("basemap label cannot be empty")
		}
		q = q.Set("label = ?", label)
		changed = true
	}
	if p.URLTemplate != nil {
		tmpl := strings.TrimSpace(*p.URLTemplate)
		if err := validateTemplate(tmpl); err != nil {
			return nil, err
		}
		q = q.Set("url_template = ?", tmpl)
		changed = true
	}
	if p.Attribution != nil {
		encoded, err := encodeAttribution(*p.Attribution)
		if err != nil {
			return nil, err
		}
		q = q.Set("attribution = ?::jsonb", encoded)
		changed = true
	}

	// Validate the resulting min/max pair against whichever side is changing,
	// so an edit can't leave min_zoom above max_zoom.
	newMin := existing.MinZoom
	if p.MinZoom != nil {
		newMin = *p.MinZoom
	}
	newMax := existing.MaxZoom
	switch {
	case p.ClearMaxZoom:
		newMax = nil
	case p.MaxZoom != nil:
		newMax = p.MaxZoom
	}
	if p.MinZoom != nil || p.MaxZoom != nil || p.ClearMaxZoom {
		if err := validateZoom(newMin, newMax); err != nil {
			return nil, err
		}
	}
	if p.MinZoom != nil {
		q = q.Set("min_zoom = ?", *p.MinZoom)
		changed = true
	}
	switch {
	case p.ClearMaxZoom:
		q = q.Set("max_zoom = NULL")
		changed = true
	case p.MaxZoom != nil:
		q = q.Set("max_zoom = ?", *p.MaxZoom)
		changed = true
	}

	if p.TMS != nil {
		q = q.Set("tms = ?", *p.TMS)
		changed = true
	}
	if p.SortOrder != nil {
		q = q.Set("sort_order = ?", *p.SortOrder)
		changed = true
	}
	if p.IsEnabled != nil {
		q = q.Set("is_enabled = ?", *p.IsEnabled)
		changed = true
	}

	if !changed {
		return nil, invalidf("update body has nothing to change")
	}
	q = q.Set("updated_at = now()").Set("updated_by = ?", nullableID(p.UpdatedBy))
	if _, err := q.Exec(ctx); err != nil {
		return nil, fmt.Errorf("basemap: updating basemap %s: %w", id, err)
	}
	return GetBasemap(ctx, db, id)
}

// DeleteBasemap removes a custom basemap. Built-ins (the migration-seeded set)
// are protected -- an admin disables them instead, so the shipped list can't be
// lost by accident.
func DeleteBasemap(ctx context.Context, db bun.IDB, id string) error {
	existing, err := GetBasemap(ctx, db, id)
	if err != nil {
		return err
	}
	if existing.IsBuiltin {
		return invalidf("basemap %q is built in and cannot be deleted; disable it instead", existing.Label)
	}
	if _, err := db.NewDelete().Model((*Basemap)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("basemap: deleting basemap %s: %w", id, err)
	}
	return nil
}

// validateCode enforces a non-empty, lowercase-kebab-ish stable id (letters,
// digits, hyphens) -- it lives in localStorage and in URLs conceptually, so it
// stays simple and slug-like.
func validateCode(code string) error {
	if code == "" {
		return invalidf("basemap code is required")
	}
	for _, r := range code {
		isLower := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if !isLower && !isDigit && r != '-' {
			return invalidf("basemap code must be lowercase letters, digits and hyphens, got %q", code)
		}
	}
	// "blank" is the client-side no-basemap built-in and is never a row -- reject
	// it so a stored basemap can't shadow it.
	if code == "blank" {
		return invalidf("basemap code %q is reserved", code)
	}
	return nil
}

// validateTemplate rejects an unusable tile URL: it must be an http(s) URL and
// carry the {x}/{y}/{z} placeholders OpenLayers substitutes. Without {z} (or
// x/y) OL would request one fixed tile forever.
func validateTemplate(tmpl string) error {
	if tmpl == "" {
		return invalidf("basemap url template is required")
	}
	u, err := url.Parse(tmpl)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return invalidf("basemap url template must be an http(s) URL")
	}
	for _, ph := range []string{"{z}", "{x}", "{y}"} {
		if !strings.Contains(tmpl, ph) {
			return invalidf("basemap url template must contain the %s placeholder", ph)
		}
	}
	return nil
}

// validateZoom keeps both zoom bounds inside the tile-scheme range and min at or
// below max. A nil max means "no cap".
func validateZoom(min int, max *int) error {
	if min < 0 || min > 24 {
		return invalidf("basemap min_zoom must be between 0 and 24, got %d", min)
	}
	if max != nil {
		if *max < 0 || *max > 24 {
			return invalidf("basemap max_zoom must be between 0 and 24, got %d", *max)
		}
		if min > *max {
			return invalidf("basemap min_zoom (%d) cannot exceed max_zoom (%d)", min, *max)
		}
	}
	return nil
}

// normaliseAttribution drops blank-label entries and guarantees a non-nil slice
// so the column is a JSON array, never null.
func normaliseAttribution(links []AttributionLink) []AttributionLink {
	out := make([]AttributionLink, 0, len(links))
	for _, l := range links {
		label := strings.TrimSpace(l.Label)
		if label == "" {
			continue
		}
		out = append(out, AttributionLink{Label: label, Href: strings.TrimSpace(l.Href)})
	}
	return out
}

// encodeAttribution normalises then JSON-encodes attribution links for a raw
// jsonb assignment in UPDATE. (The INSERT path binds the struct field via its
// bun `type:jsonb` tag, which marshals on its own; a Set("... = ?") clause does
// not, so it needs the text+cast here.)
func encodeAttribution(links []AttributionLink) (string, error) {
	b, err := json.Marshal(normaliseAttribution(links))
	if err != nil {
		return "", fmt.Errorf("basemap: encoding attribution: %w", err)
	}
	return string(b), nil
}

// ensureCodeFree rejects a code already used by another basemap (matching the
// UNIQUE(code) index). excludeID is skipped so a no-op keeps its own code.
func ensureCodeFree(ctx context.Context, db bun.IDB, code, excludeID string) error {
	q := db.NewSelect().Model((*Basemap)(nil)).Where("code = ?", code)
	if excludeID != "" {
		q = q.Where("id <> ?", excludeID)
	}
	exists, err := q.Exists(ctx)
	if err != nil {
		return fmt.Errorf("basemap: checking code uniqueness: %w", err)
	}
	if exists {
		return invalidf("a basemap with code %q already exists", code)
	}
	return nil
}

// nullableID maps an empty actor id to nil so updated_by stores NULL rather
// than an empty string that fails the uuid column.
func nullableID(id string) *string {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return &id
}
