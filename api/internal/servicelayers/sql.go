package servicelayers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/secretbox"
	"github.com/uptrace/bun"
)

// store bundles the db with the optional at-rest cipher so every operation has
// both. A nil cipher means the encryption key is unset: layers still work, but
// storing a credential is refused with a clear error rather than silently
// dropped -- same contract as api/internal/externalsystem.
type store struct {
	db     *bun.DB
	cipher *secretbox.Cipher
}

var (
	validServiceTypes = map[string]bool{
		"wms": true, "wmts": true, "xyz": true, "vector_tile": true,
		"esri_map": true, "esri_feature": true, "wfs": true,
	}
	validAuthTypes = map[string]bool{"none": true, "api_key": true, "basic": true, "bearer": true}
	// Service types whose upstream needs a per-layer selector: WMS LAYERS, WMTS
	// layer id, WFS typeName. xyz/vector_tile are a single implicit layer, and an
	// Esri layer id is usually already baked into the URL path.
	layerIDRequired = map[string]bool{"wms": true, "wmts": true, "wfs": true}
	// Service types whose `url` is a {z}/{x}/{y} tile template rather than a
	// service endpoint -- these must carry the placeholders OL substitutes.
	tileTemplateTypes = map[string]bool{"xyz": true, "vector_tile": true}
)

// ListServiceLayers returns layers ordered by sort_order then label. enabledOnly
// (the map's request) drops disabled rows; the admin page passes false.
func ListServiceLayers(ctx context.Context, s *store, enabledOnly bool) ([]serviceLayerResponse, error) {
	var rows []ServiceLayer
	q := s.db.NewSelect().Model(&rows).Order("sort_order", "label")
	if enabledOnly {
		q = q.Where("is_enabled = true")
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("servicelayers: list: %w", err)
	}
	out := make([]serviceLayerResponse, 0, len(rows))
	for i := range rows {
		out = append(out, *format(&rows[i]))
	}
	return out, nil
}

// getRow loads one layer by id, mapping a missing row to a 404.
func getRow(ctx context.Context, s *store, id string) (*ServiceLayer, error) {
	sl := new(ServiceLayer)
	if err := s.db.NewSelect().Model(sl).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("service layer %q not found", id)
		}
		return nil, fmt.Errorf("servicelayers: load %s: %w", id, err)
	}
	return sl, nil
}

func createServiceLayer(ctx context.Context, s *store, req createRequest, actor string) (*serviceLayerResponse, error) {
	code := strings.TrimSpace(req.Code)
	if err := validateCode(code); err != nil {
		return nil, err
	}

	sl := &ServiceLayer{
		Code:           code,
		Label:          strings.TrimSpace(req.Label),
		Description:    ptrIfSet(req.Description),
		ServiceType:    strings.TrimSpace(req.ServiceType),
		URL:            strings.TrimSpace(req.URL),
		LayerIDs:       ptrIfSet(req.LayerIDs),
		StyleURL:       ptrIfSet(req.StyleURL),
		ImageFormat:    ptrIfSet(req.ImageFormat),
		Version:        ptrIfSet(req.Version),
		CRS:            defaultString(req.CRS, "EPSG:3857"),
		Extent:         normaliseExtent(req.Extent),
		MaxZoom:        req.MaxZoom,
		MinZoom:        req.MinZoom,
		DefaultOpacity: 1,
		IsQueryable:    req.IsQueryable,
		Attribution:    normaliseAttribution(req.Attribution),
		Proxied:        true,
		AuthType:       defaultString(req.AuthType, "none"),
		SortOrder:      req.SortOrder,
		IsEnabled:      true,
		UpdatedBy:      nullableID(actor),
	}
	if req.DefaultOpacity != nil {
		sl.DefaultOpacity = *req.DefaultOpacity
	}
	if req.Proxied != nil {
		sl.Proxied = *req.Proxied
	}
	if req.IsEnabled != nil {
		sl.IsEnabled = *req.IsEnabled
	}
	// 'none' carries no auth params even if the caller sent stray ones.
	if sl.AuthType != "none" {
		sl.AuthHeader = ptrIfSet(req.AuthHeader)
		sl.AuthQueryParam = ptrIfSet(req.AuthQueryParam)
		sl.AuthUsername = ptrIfSet(req.AuthUsername)
	}

	if err := validate(sl); err != nil {
		return nil, err
	}

	// Credential: required for a non-'none' layer, sealed at rest.
	cred := strings.TrimSpace(req.Credential)
	switch {
	case sl.AuthType == "none":
		// no secret regardless of what was sent
	case cred == "":
		return nil, invalid("credential is required for auth_type %s", sl.AuthType)
	default:
		sealed, err := s.sealCredential(cred)
		if err != nil {
			return nil, err
		}
		sl.CredentialEnc = sealed
	}

	if err := ensureCodeFree(ctx, s, code, ""); err != nil {
		return nil, err
	}
	if _, err := s.db.NewInsert().Model(sl).Returning("*").Exec(ctx); err != nil {
		return nil, fmt.Errorf("servicelayers: insert: %w", err)
	}
	return format(sl), nil
}

func updateServiceLayer(ctx context.Context, s *store, id string, req updateRequest, actor string) (*serviceLayerResponse, error) {
	sl, err := getRow(ctx, s, id)
	if err != nil {
		return nil, err
	}

	if req.Label != nil {
		sl.Label = strings.TrimSpace(*req.Label)
	}
	if req.Description != nil {
		sl.Description = ptrIfSet(*req.Description)
	}
	if req.ServiceType != nil {
		sl.ServiceType = strings.TrimSpace(*req.ServiceType)
	}
	if req.URL != nil {
		sl.URL = strings.TrimSpace(*req.URL)
	}
	if req.LayerIDs != nil {
		sl.LayerIDs = ptrIfSet(*req.LayerIDs)
	}
	if req.StyleURL != nil {
		sl.StyleURL = ptrIfSet(*req.StyleURL)
	}
	if req.ImageFormat != nil {
		sl.ImageFormat = ptrIfSet(*req.ImageFormat)
	}
	if req.Version != nil {
		sl.Version = ptrIfSet(*req.Version)
	}
	if req.CRS != nil {
		sl.CRS = defaultString(*req.CRS, "EPSG:3857")
	}
	if req.Extent != nil {
		sl.Extent = normaliseExtent(*req.Extent)
	}
	switch {
	case req.ClearMaxZoom:
		sl.MaxZoom = nil
	case req.MaxZoom != nil:
		sl.MaxZoom = req.MaxZoom
	}
	if req.MinZoom != nil {
		sl.MinZoom = *req.MinZoom
	}
	if req.DefaultOpacity != nil {
		sl.DefaultOpacity = *req.DefaultOpacity
	}
	if req.IsQueryable != nil {
		sl.IsQueryable = *req.IsQueryable
	}
	if req.Attribution != nil {
		sl.Attribution = normaliseAttribution(*req.Attribution)
	}
	if req.Proxied != nil {
		sl.Proxied = *req.Proxied
	}
	if req.AuthType != nil {
		sl.AuthType = strings.TrimSpace(*req.AuthType)
	}
	if req.AuthHeader != nil {
		sl.AuthHeader = ptrIfSet(*req.AuthHeader)
	}
	if req.AuthQueryParam != nil {
		sl.AuthQueryParam = ptrIfSet(*req.AuthQueryParam)
	}
	if req.AuthUsername != nil {
		sl.AuthUsername = ptrIfSet(*req.AuthUsername)
	}
	if req.SortOrder != nil {
		sl.SortOrder = *req.SortOrder
	}
	if req.IsEnabled != nil {
		sl.IsEnabled = *req.IsEnabled
	}
	// Switching to 'none' drops any auth params + stored secret.
	if sl.AuthType == "none" {
		sl.AuthHeader = nil
		sl.AuthQueryParam = nil
		sl.AuthUsername = nil
		sl.CredentialEnc = nil
	}

	if err := validate(sl); err != nil {
		return nil, err
	}

	// Credential three-state (see updateRequest doc): absent = keep, present+empty
	// = clear, present+non-empty = re-seal.
	if req.Credential != nil && sl.AuthType != "none" {
		cred := strings.TrimSpace(*req.Credential)
		if cred == "" {
			sl.CredentialEnc = nil
		} else {
			sealed, err := s.sealCredential(cred)
			if err != nil {
				return nil, err
			}
			sl.CredentialEnc = sealed
		}
	}
	// A non-'none' layer must end up with a secret set (kept or supplied now).
	if sl.AuthType != "none" && len(sl.CredentialEnc) == 0 {
		return nil, invalid("credential is required for auth_type %s", sl.AuthType)
	}

	sl.UpdatedAt = time.Now()
	sl.UpdatedBy = nullableID(actor)
	if _, err := s.db.NewUpdate().Model(sl).
		Column("label", "description", "service_type", "url", "layer_ids", "style_url",
			"image_format", "version", "crs", "extent", "max_zoom", "min_zoom",
			"default_opacity", "is_queryable", "attribution", "proxied", "auth_type",
			"auth_header", "auth_query_param", "auth_username", "credential_enc",
			"sort_order", "is_enabled", "updated_at", "updated_by").
		WherePK().
		Exec(ctx); err != nil {
		return nil, fmt.Errorf("servicelayers: update %s: %w", id, err)
	}
	return format(sl), nil
}

func deleteServiceLayer(ctx context.Context, s *store, id string) error {
	res, err := s.db.NewDelete().Model((*ServiceLayer)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("servicelayers: delete %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("servicelayers: delete %s: %w", id, err)
	}
	if n == 0 {
		return notFound("service layer %q not found", id)
	}
	return nil
}

// sealCredential turns a plaintext secret into the stored ciphertext, refusing
// when no encryption key is configured -- we never store a secret in the clear.
func (s *store) sealCredential(plaintext string) ([]byte, error) {
	if s.cipher == nil {
		return nil, invalid("cannot store a credential: the server has no encryption key configured (set %s)", secretbox.EnvKey)
	}
	sealed, err := s.cipher.Encrypt([]byte(plaintext))
	if err != nil {
		return nil, fmt.Errorf("servicelayers: seal credential: %w", err)
	}
	return sealed, nil
}

// validate checks the merged row -- shared by create and update so both reject
// the same bad shapes. Code uniqueness and credential presence are checked by
// the callers (they differ between create and update).
func validate(sl *ServiceLayer) error {
	if strings.TrimSpace(sl.Label) == "" {
		return invalid("label is required")
	}
	if !validServiceTypes[sl.ServiceType] {
		return invalid("service_type must be one of wms, wmts, xyz, vector_tile, esri_map, esri_feature, wfs")
	}
	if err := validateURL(sl.URL, sl.ServiceType); err != nil {
		return err
	}
	if layerIDRequired[sl.ServiceType] && (sl.LayerIDs == nil || strings.TrimSpace(*sl.LayerIDs) == "") {
		return invalid("layer_ids is required for service_type %s", sl.ServiceType)
	}
	if strings.TrimSpace(sl.CRS) == "" {
		return invalid("crs is required")
	}
	if err := validateAuthShape(sl); err != nil {
		return err
	}
	if err := validateZoom(sl.MinZoom, sl.MaxZoom); err != nil {
		return err
	}
	if sl.DefaultOpacity < 0 || sl.DefaultOpacity > 1 {
		return invalid("default_opacity must be between 0 and 1, got %g", sl.DefaultOpacity)
	}
	if sl.Extent != nil && len(sl.Extent) != 4 {
		return invalid("extent must be [minLon, minLat, maxLon, maxLat] (4 numbers), got %d", len(sl.Extent))
	}
	return nil
}

// validateURL requires an http(s) URL with a host; tile-template types (xyz,
// vector_tile) additionally must carry the {z}/{x}/{y} placeholders OL
// substitutes -- without them OL would request one fixed tile forever.
func validateURL(raw, serviceType string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return invalid("url is required")
	}
	u, err := url.Parse(trimmed)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return invalid("url must be an http(s) URL")
	}
	if u.Host == "" {
		return invalid("url must include a host")
	}
	if tileTemplateTypes[serviceType] {
		for _, ph := range []string{"{z}", "{x}", "{y}"} {
			if !strings.Contains(trimmed, ph) {
				return invalid("a %s url must contain the %s placeholder", serviceType, ph)
			}
		}
	}
	return nil
}

// validateAuthShape checks the non-secret auth parameters an auth_type needs are
// present on the (already-merged) row. api_key accepts the secret in a header or
// a query param, so at least one of the two names must be set.
func validateAuthShape(sl *ServiceLayer) error {
	if !validAuthTypes[sl.AuthType] {
		return invalid("auth_type must be one of none, api_key, basic, bearer")
	}
	switch sl.AuthType {
	case "api_key":
		hasHeader := sl.AuthHeader != nil && strings.TrimSpace(*sl.AuthHeader) != ""
		hasParam := sl.AuthQueryParam != nil && strings.TrimSpace(*sl.AuthQueryParam) != ""
		if !hasHeader && !hasParam {
			return invalid("auth_header or auth_query_param is required for auth_type api_key")
		}
	case "basic":
		if sl.AuthUsername == nil || strings.TrimSpace(*sl.AuthUsername) == "" {
			return invalid("auth_username is required for auth_type basic")
		}
	}
	return nil
}

// validateCode enforces a non-empty, lowercase-kebab stable id (letters, digits,
// hyphens) -- it lives in client state and is looked up by the map.
func validateCode(code string) error {
	if code == "" {
		return invalid("code is required")
	}
	for _, r := range code {
		isLower := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'
		if !isLower && !isDigit && r != '-' {
			return invalid("code must be lowercase letters, digits and hyphens, got %q", code)
		}
	}
	return nil
}

// validateZoom keeps both zoom bounds inside the tile-scheme range and min at or
// below max. A nil max means "no cap".
func validateZoom(min int, max *int) error {
	if min < 0 || min > 24 {
		return invalid("min_zoom must be between 0 and 24, got %d", min)
	}
	if max != nil {
		if *max < 0 || *max > 24 {
			return invalid("max_zoom must be between 0 and 24, got %d", *max)
		}
		if min > *max {
			return invalid("min_zoom (%d) cannot exceed max_zoom (%d)", min, *max)
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

// normaliseExtent returns the slice only when it's a usable 4-number bbox; any
// other length (including 0) becomes nil so the column stores NULL. A malformed
// length is caught by validate, which surfaces a 400.
func normaliseExtent(extent []float64) []float64 {
	if len(extent) == 0 {
		return nil
	}
	return extent
}

// ensureCodeFree rejects a code already used by another layer (the UNIQUE(code)
// index). excludeID is skipped so a no-op keeps its own code.
func ensureCodeFree(ctx context.Context, s *store, code, excludeID string) error {
	q := s.db.NewSelect().Model((*ServiceLayer)(nil)).Where("code = ?", code)
	if excludeID != "" {
		q = q.Where("id <> ?", excludeID)
	}
	exists, err := q.Exists(ctx)
	if err != nil {
		return fmt.Errorf("servicelayers: checking code uniqueness: %w", err)
	}
	if exists {
		return invalid("a service layer with code %q already exists", code)
	}
	return nil
}

func format(sl *ServiceLayer) *serviceLayerResponse {
	resp := &serviceLayerResponse{
		ID:             sl.ID,
		Code:           sl.Code,
		Label:          sl.Label,
		ServiceType:    sl.ServiceType,
		URL:            sl.URL,
		CRS:            sl.CRS,
		Extent:         sl.Extent,
		MaxZoom:        sl.MaxZoom,
		MinZoom:        sl.MinZoom,
		DefaultOpacity: sl.DefaultOpacity,
		IsQueryable:    sl.IsQueryable,
		Attribution:    sl.Attribution,
		Proxied:        sl.Proxied,
		AuthType:       sl.AuthType,
		HasCredential:  len(sl.CredentialEnc) > 0,
		SortOrder:      sl.SortOrder,
		IsEnabled:      sl.IsEnabled,
		CreatedAt:      sl.CreatedAt,
		UpdatedAt:      sl.UpdatedAt,
		UpdatedBy:      sl.UpdatedBy,
	}
	if resp.Attribution == nil {
		resp.Attribution = []AttributionLink{}
	}
	resp.Description = deref(sl.Description)
	resp.LayerIDs = deref(sl.LayerIDs)
	resp.StyleURL = deref(sl.StyleURL)
	resp.ImageFormat = deref(sl.ImageFormat)
	resp.Version = deref(sl.Version)
	resp.AuthHeader = deref(sl.AuthHeader)
	resp.AuthQueryParam = deref(sl.AuthQueryParam)
	resp.AuthUsername = deref(sl.AuthUsername)
	return resp
}

// ptrIfSet returns a pointer to the trimmed string, or nil when it's blank -- so
// an optional text column stores NULL rather than an empty string.
func ptrIfSet(v string) *string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

// deref flattens an optional string to its value (empty when nil) for the
// response, where omitempty then drops it.
func deref(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// defaultString returns the trimmed value or a fallback when blank.
func defaultString(v, fallback string) string {
	if trimmed := strings.TrimSpace(v); trimmed != "" {
		return trimmed
	}
	return fallback
}

// nullableID maps an empty actor id to nil so updated_by stores NULL rather than
// an empty string that fails the uuid column.
func nullableID(id string) *string {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	return &id
}
