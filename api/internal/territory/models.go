// Package territory is the read/write layer over the gis.territories and
// gis.territory_levels tables (migrations/0027) -- the hierarchical
// administrative areas a utility operates over (utility -> region ->
// district -> area -> plot, by default). It owns three things nothing
// else in the codebase had before: the level catalog, the territory tree
// (with its structural invariants), and the spatial resolver that decides
// which territory a feature's geometry falls in (resolve.go). Enforcing
// "every geographic feature belongs to at least a district" lives in the
// CRUD write path (api/internal/crud), which calls this package.
package territory

import (
	"time"

	"github.com/uptrace/bun"
)

// Level mirrors gis.territory_levels -- the ordered, admin-configurable
// catalog of levels. Rank orders them (0 = top / utility); exactly one
// level is flagged IsMinFeatureLevel (the shallowest a geographic feature
// must reach). See migrations/0027.
type Level struct {
	bun.BaseModel `bun:"table:gis.territory_levels"`

	ID                string    `bun:"id,pk"`
	Code              string    `bun:"code"`
	Name              string    `bun:"name"`
	Rank              int       `bun:"rank"`
	IsMinFeatureLevel bool      `bun:"is_min_feature_level"`
	CreatedAt         time.Time `bun:"created_at"`
}

// Territory mirrors gis.territories -- one node of the tree. ParentID is
// null only at the root. GeomGeoJSON is the boundary as GeoJSON text,
// populated only by the reads that ask for it (ST_AsGeoJSON, aliased to
// geom_geo_json); it's scanonly because the geometry column is never
// written through bun -- writes go through raw SQL that wraps
// ST_GeomFromGeoJSON (the geometry type can't round-trip through a plain
// bind param), the same approach api/internal/crud uses for feature geom.
type Territory struct {
	bun.BaseModel `bun:"table:gis.territories"`

	ID          string    `bun:"id,pk"`
	ParentID    *string   `bun:"parent_id"`
	LevelID     string    `bun:"level_id"`
	Name        string    `bun:"name"`
	GeomGeoJSON *string   `bun:"geom_geo_json,scanonly"`
	CreatedAt   time.Time `bun:"created_at"`
	UpdatedAt   time.Time `bun:"updated_at"`
}
