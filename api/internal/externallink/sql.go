package externallink

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/externalsystem"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// ExternalLink mirrors runtime.feature_external_links (migration 0057).
//
// alias:feature_external_links is pinned for the same reason
// api/internal/relationship pins its own -- without it bun derives the
// insert's SQL alias from the Go struct name ("external_link") instead of
// the table name, which broke the identical upsert pattern elsewhere in
// this codebase (see docs/phase-2-connectivity.md).
type ExternalLink struct {
	bun.BaseModel `bun:"table:runtime.feature_external_links,alias:feature_external_links"`

	ID             string    `bun:"id,pk,nullzero"`
	FeatureClassID string    `bun:"feature_class_id"`
	FeatureID      string    `bun:"feature_id"`
	Kind           string    `bun:"kind"`
	Label          string    `bun:"label"`
	URL            *string   `bun:"url"`
	SystemID       *string   `bun:"system_id"`
	ResourcePath   *string   `bun:"resource_path"`
	CreatedAt      time.Time `bun:"created_at,nullzero"`
	CreatedBy      *string   `bun:"created_by"`
}

// loadFeatureClass translates a missing feature class into this
// package's own notFoundError, so writeError never has to know about
// metadata's error types -- mirrors api/internal/relationship.
func loadFeatureClass(ctx context.Context, db bun.IDB, code string) (*metadata.Loaded, error) {
	loaded, err := metadata.Load(ctx, db, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("feature class %q does not exist", code)
		}
		return nil, err
	}
	return loaded, nil
}

// resolveFeature validates that a feature class exists and compiles, and
// that the specific feature row exists and is not soft-deleted --
// linking a deleted feature makes no more sense than relating one does.
func resolveFeature(ctx context.Context, db bun.IDB, featureClass, featureID string) (*metadata.Loaded, error) {
	loaded, err := loadFeatureClass(ctx, db, featureClass)
	if err != nil {
		return nil, err
	}

	row, err := crud.GetFeature(ctx, db, loaded, featureID)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return nil, notFound("feature %s (%s) not found", featureID, featureClass)
		}
		return nil, err
	}
	if row["deleted_at"] != nil {
		return nil, invalid("feature %s (%s) is deleted -- restore it before linking", featureID, featureClass)
	}
	return loaded, nil
}

// validateHyperlinkURL enforces the one security-relevant rule Slice 1
// has: a hyperlink's url must be an absolute http/https URL. Rejecting
// everything else keeps javascript:/data: (and other scheme-based XSS
// vectors) out of an anchor the panel later renders with a real href.
func validateHyperlinkURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", invalid("url is required")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", invalid("url is not a valid URL: %v", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", invalid("url must start with http:// or https://")
	}
	if u.Host == "" {
		return "", invalid("url must include a host")
	}
	return trimmed, nil
}

// validateResourcePath keeps an api link's resource_path relative -- a
// path (with optional query) that resolves against the system's base_url
// and cannot repoint the request at a different origin. Rejecting any
// scheme or host here is the core SSRF guard on the less-trusted input:
// base_url is admin-set (metadata:write), but resource_path is set by
// whoever creates the link (features:write). An empty path is allowed --
// it just fetches the base_url itself.
func validateResourcePath(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	// A leading "//" parses as a scheme-relative URL with a host, which
	// would override the base_url's host -- reject before url.Parse can be
	// fooled by it.
	if strings.HasPrefix(trimmed, "//") {
		return "", invalid("resource_path must be a relative path, not %q", trimmed)
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", invalid("resource_path is not valid: %v", err)
	}
	if u.Scheme != "" || u.Host != "" {
		return "", invalid("resource_path must be a relative path (no scheme or host)")
	}
	return trimmed, nil
}

// loadSystemByID fetches a catalogued external system by id, translating
// a miss into this package's notFoundError. Shared by createLink's
// validation and the fetch proxy.
func loadSystemByID(ctx context.Context, db bun.IDB, id string) (*externalsystem.ExternalSystem, error) {
	var sys externalsystem.ExternalSystem
	if err := db.NewSelect().Model(&sys).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("external system %s not found", id)
		}
		return nil, fmt.Errorf("externallink: load system: %w", err)
	}
	return &sys, nil
}

// createLink records one link after validating the target feature exists.
// A hyperlink needs an http/https url; an api link needs a catalogued
// system and a relative resource path.
func createLink(ctx context.Context, db bun.IDB, req createRequest) (*linkResponse, error) {
	loaded, err := resolveFeature(ctx, db, req.FeatureClass, req.FeatureID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Label) == "" {
		return nil, invalid("label is required")
	}

	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		kind = "hyperlink"
	}

	link := &ExternalLink{
		FeatureClassID: loaded.FeatureClass.ID,
		FeatureID:      req.FeatureID,
		Kind:           kind,
		Label:          strings.TrimSpace(req.Label),
	}
	var sys *externalsystem.ExternalSystem

	switch kind {
	case "hyperlink":
		cleanURL, err := validateHyperlinkURL(req.URL)
		if err != nil {
			return nil, err
		}
		link.URL = &cleanURL
	case "api":
		if strings.TrimSpace(req.SystemID) == "" {
			return nil, invalid("system_id is required for an api link")
		}
		sys, err = loadSystemByID(ctx, db, strings.TrimSpace(req.SystemID))
		if err != nil {
			return nil, err
		}
		path, err := validateResourcePath(req.ResourcePath)
		if err != nil {
			return nil, err
		}
		link.SystemID = &sys.ID
		link.ResourcePath = &path
	default:
		return nil, invalid("kind must be 'hyperlink' or 'api'")
	}

	if actor := auth.ActorID(ctx); actor != "" {
		link.CreatedBy = &actor
	}

	if _, err := db.NewInsert().Model(link).Returning("*").Exec(ctx); err != nil {
		return nil, fmt.Errorf("externallink: create: %w", err)
	}

	resp := formatLink(link)
	if sys != nil {
		resp.SystemCode = sys.Code
		resp.SystemName = sys.Name
	}
	return resp, nil
}

// listLinks returns every link attached to one feature, newest last.
func listLinks(ctx context.Context, db bun.IDB, featureClass, featureID string) ([]linkResponse, error) {
	loaded, err := loadFeatureClass(ctx, db, featureClass)
	if err != nil {
		return nil, err
	}

	var rows []ExternalLink
	if err := db.NewSelect().Model(&rows).
		Where("feature_class_id = ? AND feature_id = ?", loaded.FeatureClass.ID, featureID).
		Order("created_at").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("externallink: list: %w", err)
	}

	// Enrich api links with their system's code/name in one lookup, so the
	// panel can label them without a call per row. A link whose system was
	// deleted (system_id nulled) simply resolves to no name here and the
	// panel shows it as orphaned.
	names, err := systemNames(ctx, db, rows)
	if err != nil {
		return nil, err
	}

	out := make([]linkResponse, 0, len(rows))
	for i := range rows {
		resp := formatLink(&rows[i])
		if rows[i].SystemID != nil {
			if n, ok := names[*rows[i].SystemID]; ok {
				resp.SystemCode = n.code
				resp.SystemName = n.name
			}
		}
		out = append(out, *resp)
	}
	return out, nil
}

type systemName struct{ code, name string }

// systemNames resolves the distinct system ids referenced by a batch of
// links to their code/name in a single query.
func systemNames(ctx context.Context, db bun.IDB, links []ExternalLink) (map[string]systemName, error) {
	ids := make([]string, 0)
	seen := map[string]bool{}
	for i := range links {
		if links[i].SystemID != nil && !seen[*links[i].SystemID] {
			seen[*links[i].SystemID] = true
			ids = append(ids, *links[i].SystemID)
		}
	}
	out := map[string]systemName{}
	if len(ids) == 0 {
		return out, nil
	}
	var systems []externalsystem.ExternalSystem
	if err := db.NewSelect().Model(&systems).
		Column("id", "code", "name").
		Where("id IN (?)", bun.In(ids)).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("externallink: system names: %w", err)
	}
	for i := range systems {
		out[systems[i].ID] = systemName{code: systems[i].Code, name: systems[i].Name}
	}
	return out, nil
}

// updateLink renames a link and/or repoints its url. Only the fields
// present in the request change; the url, when present, is re-validated.
func updateLink(ctx context.Context, db bun.IDB, id string, req updateRequest) (*linkResponse, error) {
	var link ExternalLink
	if err := db.NewSelect().Model(&link).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("link %s not found", id)
		}
		return nil, fmt.Errorf("externallink: update: load: %w", err)
	}

	if req.Label != nil {
		label := strings.TrimSpace(*req.Label)
		if label == "" {
			return nil, invalid("label cannot be empty")
		}
		link.Label = label
	}
	if req.URL != nil {
		if link.Kind != "hyperlink" {
			return nil, invalid("url can only be set on a hyperlink")
		}
		cleanURL, err := validateHyperlinkURL(*req.URL)
		if err != nil {
			return nil, err
		}
		link.URL = &cleanURL
	}
	if req.ResourcePath != nil {
		if link.Kind != "api" {
			return nil, invalid("resource_path can only be set on an api link")
		}
		path, err := validateResourcePath(*req.ResourcePath)
		if err != nil {
			return nil, err
		}
		link.ResourcePath = &path
	}

	if _, err := db.NewUpdate().Model(&link).
		Column("label", "url", "resource_path").
		WherePK().
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("externallink: update: %w", err)
	}
	return formatLink(&link), nil
}

// deleteLink permanently removes one link. No soft-delete -- a link is a
// binary fact with no draft/published life cycle of its own, same
// reasoning as relationship revoke.
func deleteLink(ctx context.Context, db bun.IDB, id string) error {
	res, err := db.NewDelete().Model((*ExternalLink)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("externallink: delete: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("externallink: delete: %w", err)
	}
	if n == 0 {
		return notFound("link %s not found", id)
	}
	return nil
}

func formatLink(link *ExternalLink) *linkResponse {
	resp := &linkResponse{
		ID:        link.ID,
		Kind:      link.Kind,
		Label:     link.Label,
		CreatedAt: link.CreatedAt,
	}
	if link.URL != nil {
		resp.URL = *link.URL
	}
	if link.SystemID != nil {
		resp.SystemID = *link.SystemID
	}
	if link.ResourcePath != nil {
		resp.ResourcePath = *link.ResourcePath
	}
	return resp
}
