package territory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// Match is the territory a feature's geometry resolved into: the deepest
// (highest-rank) territory whose boundary contains the feature's centroid.
type Match struct {
	TerritoryID   string
	TerritoryName string
	LevelCode     string
	LevelRank     int
}

// ResolveContaining finds the deepest territory whose boundary contains
// the centroid of the given feature geometry, or nil if none does. This
// is the repo's first point-in-polygon query.
//
// The feature geometry arrives as GeoJSON in the feature class's own SRID
// (srid) -- exactly the value api/internal/crud is about to store via
// ST_SetSRID(ST_GeomFromGeoJSON(...), srid), coordinates interpreted in
// that SRID, not reprojected. So this mirrors that construction and then
// ST_Transform's to 4326 to compare against territory boundaries (always
// stored in 4326). Centroid, not the whole geometry: a line or polygon
// that straddles a boundary resolves to exactly one territory -- whichever
// contains its centroid -- rather than being ambiguous or rejected
// (docs/phase-11-territories.md, "straddle rule").
//
// Deepest wins (ORDER BY rank DESC LIMIT 1): if a point sits inside a
// district which sits inside a region, the district is the meaningful
// assignment. The caller decides whether that depth is sufficient (see
// MinFeatureLevel) -- this only reports what actually contains the point.
func ResolveContaining(ctx context.Context, db bun.IDB, geomGeoJSON string, srid int) (*Match, error) {
	query := fmt.Sprintf(`
		SELECT t.id, t.name, l.code, l.rank
		FROM gis.territories t
		JOIN gis.territory_levels l ON l.id = t.level_id
		WHERE t.geom IS NOT NULL
		  AND ST_Contains(
		        t.geom,
		        ST_Centroid(ST_Transform(ST_SetSRID(ST_GeomFromGeoJSON(?), %d), 4326))
		      )
		ORDER BY l.rank DESC
		LIMIT 1`, srid)

	var m Match
	err := db.QueryRowContext(ctx, query, geomGeoJSON).Scan(&m.TerritoryID, &m.TerritoryName, &m.LevelCode, &m.LevelRank)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("territory: resolving containing territory: %w", err)
	}
	return &m, nil
}
