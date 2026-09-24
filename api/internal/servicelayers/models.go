// Package servicelayers is the read/write layer over iam.service_layers
// (migration 0113) -- the admin-managed catalog of external/3rd-party map
// service layers the geographic map overlays: OGC WMS/WMTS, XYZ raster or vector
// tiles, Esri MapServer/FeatureServer, and OGC WFS.
//
// It is the operational sibling of api/internal/basemap: a basemap is the single
// XYZ raster tile source *under* everything, curated the same way; a service
// layer is a toggleable *overlay* that stacks and can be any of several service
// types (the service_type discriminator). Both are pure map-presentation config
// in the iam schema, governed by the same features:read / metadata:write gate.
//
// Auth is per-layer and optional -- many public services need none. When one
// does, the secret is encrypted at rest via api/internal/secretbox exactly like
// metadata.external_systems: write-only, surfaced only as has_credential, and
// decrypted server-side by the later proxy slice, never returned to the browser.
package servicelayers

import (
	"time"

	"github.com/uptrace/bun"
)

// AttributionLink is one required credit line for a layer, rendered by the map's
// status bar (OpenLayers' own attribution control is disabled). Stored as a JSON
// array on the row -- the same shape as api/internal/basemap.AttributionLink.
type AttributionLink struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

// ServiceLayer mirrors iam.service_layers. CredentialEnc is the sealed secret and
// is never serialized to a response -- handlers format a serviceLayerResponse
// (below) that carries only has_credential.
type ServiceLayer struct {
	bun.BaseModel `bun:"table:iam.service_layers,alias:service_layers"`

	ID             string            `bun:"id,pk,nullzero"`
	Code           string            `bun:"code"`
	Label          string            `bun:"label"`
	Description    *string           `bun:"description"`
	ServiceType    string            `bun:"service_type"`
	URL            string            `bun:"url"`
	LayerIDs       *string           `bun:"layer_ids"`
	StyleURL       *string           `bun:"style_url"`
	ImageFormat    *string           `bun:"image_format"`
	Version        *string           `bun:"version"`
	CRS            string            `bun:"crs"`
	Extent         []float64         `bun:"extent,type:jsonb,nullzero"`
	MaxZoom        *int              `bun:"max_zoom"`
	MinZoom        int               `bun:"min_zoom"`
	DefaultOpacity float64           `bun:"default_opacity"`
	IsQueryable    bool              `bun:"is_queryable"`
	Attribution    []AttributionLink `bun:"attribution,type:jsonb"`
	Proxied        bool              `bun:"proxied"`
	AuthType       string            `bun:"auth_type"`
	AuthHeader     *string           `bun:"auth_header"`
	AuthQueryParam *string           `bun:"auth_query_param"`
	AuthUsername   *string           `bun:"auth_username"`
	CredentialEnc  []byte            `bun:"credential_enc"`
	SortOrder      int               `bun:"sort_order"`
	IsEnabled      bool              `bun:"is_enabled"`
	CreatedAt      time.Time         `bun:"created_at,nullzero"`
	UpdatedAt      time.Time         `bun:"updated_at,nullzero"`
	UpdatedBy      *string           `bun:"updated_by"`
}

// serviceLayerResponse is the shape returned for one layer. It deliberately has
// no credential field: the sealed secret is write-only, surfaced only as
// has_credential.
type serviceLayerResponse struct {
	ID             string            `json:"id"`
	Code           string            `json:"code"`
	Label          string            `json:"label"`
	Description    string            `json:"description,omitempty"`
	ServiceType    string            `json:"service_type"`
	URL            string            `json:"url"`
	LayerIDs       string            `json:"layer_ids,omitempty"`
	StyleURL       string            `json:"style_url,omitempty"`
	ImageFormat    string            `json:"image_format,omitempty"`
	Version        string            `json:"version,omitempty"`
	CRS            string            `json:"crs"`
	Extent         []float64         `json:"extent,omitempty"`
	MaxZoom        *int              `json:"max_zoom"`
	MinZoom        int               `json:"min_zoom"`
	DefaultOpacity float64           `json:"default_opacity"`
	IsQueryable    bool              `json:"is_queryable"`
	Attribution    []AttributionLink `json:"attribution"`
	Proxied        bool              `json:"proxied"`
	AuthType       string            `json:"auth_type"`
	AuthHeader     string            `json:"auth_header,omitempty"`
	AuthQueryParam string            `json:"auth_query_param,omitempty"`
	AuthUsername   string            `json:"auth_username,omitempty"`
	HasCredential  bool              `json:"has_credential"`
	SortOrder      int               `json:"sort_order"`
	IsEnabled      bool              `json:"is_enabled"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	UpdatedBy      *string           `json:"updated_by"`
}

// createRequest is the POST body. Optional numeric/bool fields are pointers so an
// omitted value can take the server/DB default (is_enabled -> true, proxied ->
// true, default_opacity -> 1). Credential is the plaintext secret; it's sealed
// server-side and this is the only direction it ever travels.
type createRequest struct {
	Code           string            `json:"code"`
	Label          string            `json:"label"`
	Description    string            `json:"description"`
	ServiceType    string            `json:"service_type"`
	URL            string            `json:"url"`
	LayerIDs       string            `json:"layer_ids"`
	StyleURL       string            `json:"style_url"`
	ImageFormat    string            `json:"image_format"`
	Version        string            `json:"version"`
	CRS            string            `json:"crs"`
	Extent         []float64         `json:"extent"`
	MaxZoom        *int              `json:"max_zoom"`
	MinZoom        int               `json:"min_zoom"`
	DefaultOpacity *float64          `json:"default_opacity"`
	IsQueryable    bool              `json:"is_queryable"`
	Attribution    []AttributionLink `json:"attribution"`
	Proxied        *bool             `json:"proxied"`
	AuthType       string            `json:"auth_type"`
	AuthHeader     string            `json:"auth_header"`
	AuthQueryParam string            `json:"auth_query_param"`
	AuthUsername   string            `json:"auth_username"`
	Credential     string            `json:"credential"`
	SortOrder      int               `json:"sort_order"`
	IsEnabled      *bool             `json:"is_enabled"`
}

// updateRequest is the PATCH body. Every field is optional -- only what's present
// changes. clear_max_zoom removes the zoom cap (distinct from "leave it alone");
// code is absent because it's the immutable stable id. Credential is three-state:
// absent = leave the stored secret untouched; present and non-empty = re-seal and
// replace; present and empty = clear it.
type updateRequest struct {
	Label          *string            `json:"label"`
	Description    *string            `json:"description"`
	ServiceType    *string            `json:"service_type"`
	URL            *string            `json:"url"`
	LayerIDs       *string            `json:"layer_ids"`
	StyleURL       *string            `json:"style_url"`
	ImageFormat    *string            `json:"image_format"`
	Version        *string            `json:"version"`
	CRS            *string            `json:"crs"`
	Extent         *[]float64         `json:"extent"`
	MaxZoom        *int               `json:"max_zoom"`
	ClearMaxZoom   bool               `json:"clear_max_zoom"`
	MinZoom        *int               `json:"min_zoom"`
	DefaultOpacity *float64           `json:"default_opacity"`
	IsQueryable    *bool              `json:"is_queryable"`
	Attribution    *[]AttributionLink `json:"attribution"`
	Proxied        *bool              `json:"proxied"`
	AuthType       *string            `json:"auth_type"`
	AuthHeader     *string            `json:"auth_header"`
	AuthQueryParam *string            `json:"auth_query_param"`
	AuthUsername   *string            `json:"auth_username"`
	Credential     *string            `json:"credential"`
	SortOrder      *int               `json:"sort_order"`
	IsEnabled      *bool              `json:"is_enabled"`
}

// apiError is the JSON body returned for any 4xx/5xx response.
type apiError struct {
	Error string `json:"error"`
}
