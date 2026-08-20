package crud

import (
	"context"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// ExportBBox is a lon/lat (EPSG:4326) bounding box for the geographic
// export's spatial filter -- the map viewport, reprojected to 4326 by the
// client (the map view is EPSG:3857). A nil *ExportBBox means "no spatial
// filter": the whole-dataset export.
type ExportBBox struct {
	MinX, MinY, MaxX, MaxY float64
}

// maxExportFeatures caps how many features a single feature class contributes
// to one export. A geographic export holds every matching feature in memory
// to encode it, so this is the ceiling that keeps a whole-dataset export of a
// huge class (or a hostile request) from exhausting memory -- generous enough
// that a real map-extent export never approaches it.
const maxExportFeatures = 50000

// ExportFeatures reads one feature class's rows for a geographic export
// (docs/phase-14-export.md): the same read path as list/query
// (selectColumns/scanRows/formatRow, so each row carries its GeoJSON
// geometry and attributes), with an optional bbox spatial filter, an optional
// territory filter, and no attribute filter. Soft-deleted rows are excluded,
// exactly as list does. Only geometric classes can be exported -- a
// geometry-less class has nothing to place in a DXF/KMZ/shapefile.
//
// territoryIDs is the export's territory scope, ALREADY EXPANDED to include
// descendants by the caller (territory.DescendantIDs). Its three states: nil
// means "no territory filter"; a non-empty slice keeps only rows whose
// territory_id is in the set; an empty (non-nil) slice matches nothing (the
// requested territories resolved to no ids) rather than silently falling back
// to no filter.
func ExportFeatures(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, bbox *ExportBBox, territoryIDs []string) ([]map[string]any, error) {
	if loaded.FeatureClass.GeometryType == "none" {
		return nil, invalid("feature class %q has no geometry, so it can't be exported to a geographic format", loaded.FeatureClass.Code)
	}
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return nil, err
	}

	where := []string{"deleted_at IS NULL"}
	args := make([]any, 0, 4)
	if territoryIDs != nil {
		if len(territoryIDs) == 0 {
			// A territory filter that resolved to no ids: match nothing.
			where = append(where, "1 = 0")
		} else {
			// territory_id is uuid; cast to text so the compare is exact
			// against the canonical id::text the descendant expansion emits
			// (the column is unindexed anyway, so no index is forgone). Each
			// id is a positional bind param, same as the bbox below.
			placeholders := make([]string, len(territoryIDs))
			for i := range territoryIDs {
				placeholders[i] = "?"
				args = append(args, territoryIDs[i])
			}
			where = append(where, "territory_id::text IN ("+strings.Join(placeholders, ", ")+")")
		}
	}
	if bbox != nil {
		// The envelope is built in 4326 (the client's coordinates) then
		// transformed to the class's own SRID so the compare runs against the
		// stored geometry and keeps using its spatial index. Every seeded class
		// is 4326, where ST_Transform is a no-op, but a class with a projected
		// SRID still filters correctly. SRID is always set for a geometric
		// class (admin validation guarantees it); default to 4326 defensively.
		srid := 4326
		if loaded.FeatureClass.SRID != nil {
			srid = *loaded.FeatureClass.SRID
		}
		where = append(where, fmt.Sprintf("ST_Intersects(geom, ST_Transform(ST_MakeEnvelope(?, ?, ?, ?, 4326), %d))", srid))
		args = append(args, bbox.MinX, bbox.MinY, bbox.MaxX, bbox.MaxY)
	}

	// A stable id sort keeps repeated exports of the same extent byte-identical.
	// maxExportFeatures is an int constant, not a bind param -- spliced as %d.
	query := fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s ORDER BY id LIMIT %d",
		strings.Join(selectColumns(loaded), ", "), table, strings.Join(where, " AND "), maxExportFeatures,
	)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("crud: export read: %w", err)
	}
	results, err := scanRows(rows)
	if err != nil {
		return nil, err
	}

	out := make([]map[string]any, 0, len(results))
	for _, r := range results {
		formatted, err := formatRow(loaded, r)
		if err != nil {
			return nil, err
		}
		out = append(out, formatted)
	}
	return out, nil
}
