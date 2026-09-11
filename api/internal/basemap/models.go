// Package basemap is the read/write layer over iam.basemaps (migration 0077) --
// the admin-curated set of raster tile basemaps the geographic map can show
// (OpenStreetMap, Carto styles, Esri satellite, or an in-house tile server).
// Before this the list was a hardcoded frontend constant; now it is data an
// admin edits through the Map settings page, and the map reads it from the
// server at GET /basemaps.
//
// It deliberately mirrors api/internal/circuit / api/internal/mapsettings in
// shape: a flat lookup entity in a non-gis schema (iam, alongside the
// default-view store), curated in-app through a CRUD API + admin UI. There is
// no engine or topology involvement -- a basemap is pure map-presentation
// config.
package basemap

import (
	"time"

	"github.com/uptrace/bun"
)

// AttributionLink is one required credit line for a basemap, rendered by the
// map's status bar (OpenLayers' own attribution control is disabled, so this is
// the only place attribution ever shows). Stored as a JSON array on the row.
type AttributionLink struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

// Basemap mirrors iam.basemaps -- one raster XYZ tile source. URLTemplate
// carries {x}/{y}/{z} placeholders in whatever order the provider uses (Esri is
// {z}/{y}/{x}, cartocdn/OSM {z}/{x}/{y}); OpenLayers' XYZ source substitutes
// whichever are present. MaxZoom is the provider's zoom ceiling (nil = no cap).
// TMS flips the Y axis for the rarer TMS-scheme servers. IsBuiltin marks the
// five seeded defaults, which the API refuses to delete (an admin may still
// disable, reorder or edit them).
type Basemap struct {
	bun.BaseModel `bun:"table:iam.basemaps"`

	ID          string            `bun:"id,pk" json:"id"`
	Code        string            `bun:"code" json:"code"`
	Label       string            `bun:"label" json:"label"`
	URLTemplate string            `bun:"url_template" json:"url_template"`
	Attribution []AttributionLink `bun:"attribution,type:jsonb" json:"attribution"`
	MaxZoom     *int              `bun:"max_zoom" json:"max_zoom"`
	MinZoom     int               `bun:"min_zoom" json:"min_zoom"`
	TMS         bool              `bun:"tms" json:"tms"`
	SortOrder   int               `bun:"sort_order" json:"sort_order"`
	IsEnabled   bool              `bun:"is_enabled" json:"is_enabled"`
	IsBuiltin   bool              `bun:"is_builtin" json:"is_builtin"`
	CreatedAt   time.Time         `bun:"created_at" json:"created_at"`
	UpdatedAt   time.Time         `bun:"updated_at" json:"updated_at"`
	UpdatedBy   *string           `bun:"updated_by" json:"updated_by"`
}
