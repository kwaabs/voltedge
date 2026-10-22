package wam

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// resolveAssetLocation returns an on-geometry point (lon/lat, WGS84) for a feature
// asset, so a work order anchored to that asset can inherit a location even when
// the caller gave no explicit coordinate. This is what lets EVERY feature-asset
// work order -- an assigned audit fix, a vegetation trim, an inspection repair --
// pin on the field map (even if the asset feature itself isn't in the crew's pack)
// and be located on the web board.
//
// ok=false (no error) when the class is uncompiled / non-geometric, or the feature
// is missing / deleted / geometry-less -- the order simply stays locationless, as
// before. The point is ST_ClosestPoint(geom, centroid): a point guaranteed to lie
// ON the geometry for any type (a line's on-line point, a polygon's edge, a point
// itself), unlike a bare centroid. The physical table is trusted server metadata
// (physical_table, mirroring faults/connectivity), interpolated; the id is bound.
func resolveAssetLocation(ctx context.Context, db bun.IDB, class, id string) (lon, lat float64, ok bool, err error) {
	var meta struct {
		PhysicalTable sql.NullString `bun:"physical_table"`
		GeometryType  string         `bun:"geometry_type"`
	}
	err = db.NewSelect().
		ColumnExpr("physical_table, geometry_type").
		TableExpr("metadata.feature_classes").
		Where("code = ?", class).
		Limit(1).
		Scan(ctx, &meta)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, false, nil
		}
		return 0, 0, false, fmt.Errorf("wam: looking up table for class %s: %w", class, err)
	}
	if !meta.PhysicalTable.Valid || meta.PhysicalTable.String == "" || meta.GeometryType == "none" {
		return 0, 0, false, nil
	}

	var row struct {
		Lon float64 `bun:"lon"`
		Lat float64 `bun:"lat"`
	}
	err = db.NewSelect().
		ColumnExpr("ST_X(ST_ClosestPoint(geom, ST_Centroid(geom))) AS lon").
		ColumnExpr("ST_Y(ST_ClosestPoint(geom, ST_Centroid(geom))) AS lat").
		TableExpr(meta.PhysicalTable.String).
		Where("id = ?", id).
		Where("deleted_at IS NULL").
		Where("geom IS NOT NULL").
		Limit(1).
		Scan(ctx, &row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, false, nil
		}
		return 0, 0, false, fmt.Errorf("wam: resolving location for %s %s: %w", class, id, err)
	}
	return row.Lon, row.Lat, true, nil
}
