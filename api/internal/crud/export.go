package crud

import (
	"context"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/drafts"
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
// to a MATERIALIZED export. The materialized readers (shapefile, CIM) hold every
// matching feature in memory to encode -- shapefile needs a full pass to compute
// its header offsets, CIM needs the whole connectivity graph for topology -- so
// this ceiling keeps a whole-dataset export of a huge class (or a hostile
// request) from exhausting memory. The streaming reader (ExportFeaturesStream,
// used by KMZ/DXF) applies no such cap: it encodes each feature as it arrives
// and never holds them all at once (Phase 17 Slice 3d).
const maxExportFeatures = 50000

// exportQuery builds the SELECT for one class's export read -- columns, table,
// WHERE (soft-delete + optional territory + optional bbox + optional
// private-drafts author filter), and a stable ORDER BY id -- WITHOUT a LIMIT
// tail, which each reader appends (or omits) for itself. Only geometric classes
// can be exported: a geometry-less class has nothing to place in a DXF/KMZ/shapefile.
//
// draftAuthors (Phase 61) is the requester's visible-draft author set, frozen
// into the export job scope at enqueue. nil = no draft filter; non-empty keeps
// only public features plus those authored by the requester or a subordinate.
//
// territoryIDs is the export's territory scope, ALREADY EXPANDED to include
// descendants by the caller (territory.DescendantIDs). Its three states: nil
// means "no territory filter"; a non-empty slice keeps only rows whose
// territory_id is in the set; an empty (non-nil) slice matches nothing (the
// requested territories resolved to no ids) rather than silently falling back
// to no filter.
func exportQuery(loaded *metadata.Loaded, bbox *ExportBBox, territoryIDs, draftAuthors []string) (string, []any, error) {
	if loaded.FeatureClass.GeometryType == "none" {
		return "", nil, invalid("feature class %q has no geometry, so it can't be exported to a geographic format", loaded.FeatureClass.Code)
	}
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return "", nil, err
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

	// Private-drafts (Phase 61): keep another user's unpublished work out of the
	// export. draftAuthors is the requester's visible-author set, frozen into the
	// job scope at enqueue (the export runner is detached and holds no session, so
	// this can't be re-derived here). nil = filter doesn't apply (gate off /
	// unscoped requester): export every feature as before. When set it is
	// non-empty, so a row survives if public (published or ever-published) OR
	// authored by the requester or one of their subordinates.
	if len(draftAuthors) > 0 {
		if clause, draftArgs := drafts.WhereClause(draftAuthors, true, ""); clause != "" {
			where = append(where, clause)
			args = append(args, draftArgs...)
		}
	}

	// A stable id sort keeps repeated exports of the same extent byte-identical.
	query := fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s ORDER BY id",
		strings.Join(selectColumns(loaded), ", "), table, strings.Join(where, " AND "),
	)
	return query, args, nil
}

// ExportFeatures reads one feature class's rows for a MATERIALIZED geographic
// export (shapefile, CIM): the same read path as list/query
// (selectColumns/scanRows/formatRow, so each row carries its GeoJSON geometry
// and attributes), capped at maxExportFeatures. See exportQuery for the filters.
func ExportFeatures(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, bbox *ExportBBox, territoryIDs, draftAuthors []string) ([]map[string]any, error) {
	query, args, err := exportQuery(loaded, bbox, territoryIDs, draftAuthors)
	if err != nil {
		return nil, err
	}
	// maxExportFeatures is an int constant, not a bind param -- spliced as %d.
	query += fmt.Sprintf(" LIMIT %d", maxExportFeatures)

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

// ExportFeaturesStream reads one class's export rows one at a time, invoking fn
// per formatted row, and returns how many rows it streamed. Unlike
// ExportFeatures it applies NO maxExportFeatures cap and never materializes the
// result set: rows are scanned and handed off individually, so the streaming
// export path (KMZ/DXF, Phase 17 Slice 3d) encodes each feature as it arrives
// at constant memory. The caller runs this inside a REPEATABLE READ snapshot
// tx, so the cursor is a consistent point-in-time view for its whole lifetime.
// If fn returns an error, iteration stops and that error is returned (with the
// count so far).
func ExportFeaturesStream(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, bbox *ExportBBox, territoryIDs, draftAuthors []string, fn func(map[string]any) error) (int, error) {
	query, args, err := exportQuery(loaded, bbox, territoryIDs, draftAuthors)
	if err != nil {
		return 0, err
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("crud: export stream read: %w", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return 0, fmt.Errorf("crud: reading columns: %w", err)
	}

	count := 0
	for rows.Next() {
		// Scan generically, one row at a time -- the physical table's shape is
		// only known at runtime, same as scanRows, but here nothing is retained
		// past this iteration.
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return count, fmt.Errorf("crud: scanning row: %w", err)
		}
		row := make(map[string]any, len(columns))
		for i, col := range columns {
			// Same []byte->string normalization scanRows does (uuids et al.).
			if b, ok := values[i].([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = values[i]
			}
		}
		formatted, err := formatRow(loaded, row)
		if err != nil {
			return count, err
		}
		if err := fn(formatted); err != nil {
			return count, err
		}
		count++
	}
	return count, rows.Err()
}
