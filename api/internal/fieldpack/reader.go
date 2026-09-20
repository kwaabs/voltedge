package fieldpack

// The pack's feature read (Phase 21 Slice 1, docs/phase-21-field-tool.md).
//
// A pack needs each feature TWICE, in two encodings: as OGC WKB (the on-device
// edit truth, stored in the SQLite pack's features table) and as GeoJSON (the
// input tippecanoe reads to build the MBTiles display tileset). readClassFeatures
// gets both in a single pass by selecting ST_AsBinary(geom) and ST_AsGeoJSON(geom)
// side by side, so the class is scanned once rather than once per artifact.
//
// This deliberately does NOT reuse crud.ExportFeatures: that read path emits only
// GeoJSON geometry (ST_AsGeoJSON), and re-encoding GeoJSON back to WKB in Go would
// mean either a new geometry dependency or a hand-rolled binary encoder this
// package can't runtime-test. Letting PostGIS emit the WKB (ST_AsBinary) keeps
// geometry correctness on PostGIS's side. The WHERE clause (soft-delete + optional
// territory + optional bbox + optional private-drafts author filter) is a faithful
// mirror of crud.exportQuery + the drafts predicate -- the same filters the
// export/read path applies -- reproduced here because that builder is unexported
// and this query needs a different column list.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/drafts"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// packRow is one feature read for the pack: the system columns, the geometry in
// both encodings, and the class's own attribute fields. Name/Status/TerritoryID
// are pointers so a NULL column round-trips as NULL into the SQLite pack rather
// than an empty string.
type packRow struct {
	ID          string
	Name        *string
	Status      *string
	Subtype     *int64
	TerritoryID *string
	WKB         []byte // OGC WKB (ST_AsBinary); the on-device edit truth
	GeoJSON     string // ST_AsGeoJSON; the tippecanoe tile input
	Attributes  map[string]any
}

// quoteIdent wraps a Postgres identifier in double quotes -- a local copy of
// crud.quoteIdent (unexported there). Field names are admin-set lower_snake_case,
// never user input, but quoting removes a whole class of reserved-word bugs.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// classQuery builds the SELECT for one class's pack read. It mirrors
// crud.exportQuery's WHERE (deleted_at IS NULL + optional territory + optional
// bbox against the spatial index) and ORDER BY id, but its column list differs:
// system columns, geometry as BOTH wkb and geojson, then every active field. Only
// geometric, already-compiled classes can be packed.
func classQuery(loaded *metadata.Loaded, bbox *crud.ExportBBox, territoryIDs, draftAuthors []string) (string, []any, error) {
	fc := loaded.FeatureClass
	if fc.GeometryType == "none" {
		return "", nil, fmt.Errorf("feature class %q has no geometry, so it can't be packed", fc.Code)
	}
	if fc.PhysicalTable == nil {
		return "", nil, fmt.Errorf("feature class %q has not been compiled yet -- no physical table exists", fc.Code)
	}
	table := *fc.PhysicalTable

	cols := []string{"id", "name", "status"}
	hasSubtype := len(loaded.Subtypes) > 0
	if hasSubtype {
		cols = append(cols, "subtype")
	}
	// territory_id exists only on geometric tables (guaranteed here, geometry != none).
	cols = append(cols, "territory_id", "ST_AsBinary(geom) AS wkb", "ST_AsGeoJSON(geom)::text AS geojson")
	for _, f := range loaded.Fields {
		cols = append(cols, quoteIdent(f.Name))
	}

	where := []string{"deleted_at IS NULL"}
	args := make([]any, 0, 8)
	if territoryIDs != nil {
		if len(territoryIDs) == 0 {
			where = append(where, "1 = 0")
		} else {
			placeholders := make([]string, len(territoryIDs))
			for i := range territoryIDs {
				placeholders[i] = "?"
				args = append(args, territoryIDs[i])
			}
			where = append(where, "territory_id::text IN ("+strings.Join(placeholders, ", ")+")")
		}
	}
	if bbox != nil {
		srid := 4326
		if fc.SRID != nil {
			srid = *fc.SRID
		}
		where = append(where, fmt.Sprintf("ST_Intersects(geom, ST_Transform(ST_MakeEnvelope(?, ?, ?, ?, 4326), %d))", srid))
		args = append(args, bbox.MinX, bbox.MinY, bbox.MaxX, bbox.MaxY)
	}

	// Private-drafts (Phase 61): don't bake another user's unpublished work into
	// this pack. draftAuthors is the enqueuing user's visible-author set, frozen
	// into the scope at request time (the build worker holds no session, so this
	// can't be re-derived here -- see PackScope.DraftAuthors). nil means the filter
	// doesn't apply (gate off / unscoped requester): pack every feature as before.
	// When set it is non-empty, so a row survives if it is public (published or
	// ever-published) OR authored by the requester or one of their subordinates.
	if len(draftAuthors) > 0 {
		if clause, draftArgs := drafts.WhereClause(draftAuthors, true, ""); clause != "" {
			where = append(where, clause)
			args = append(args, draftArgs...)
		}
	}

	query := fmt.Sprintf(
		"SELECT %s FROM %s WHERE %s ORDER BY id",
		strings.Join(cols, ", "), table, strings.Join(where, " AND "),
	)
	return query, args, nil
}

// readClassFeatures streams one class's features through fn one row at a time and
// returns how many it read. Like crud.ExportFeaturesStream it applies NO feature
// cap and never materializes the result set -- the caller runs it inside a
// REPEATABLE READ snapshot tx, so the cursor is a consistent point-in-time view.
// If fn returns an error, iteration stops and that error is returned.
func readClassFeatures(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, bbox *crud.ExportBBox, territoryIDs, draftAuthors []string, fn func(packRow) error) (int, error) {
	query, args, err := classQuery(loaded, bbox, territoryIDs, draftAuthors)
	if err != nil {
		return 0, err
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("fieldpack: reading class %q: %w", loaded.FeatureClass.Code, err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return 0, fmt.Errorf("fieldpack: reading columns: %w", err)
	}
	// The set of attribute field names, so scanning can route unknown columns into
	// Attributes without re-checking against the fixed system columns each row.
	fieldSet := make(map[string]struct{}, len(loaded.Fields))
	for _, f := range loaded.Fields {
		fieldSet[f.Name] = struct{}{}
	}

	count := 0
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return count, fmt.Errorf("fieldpack: scanning row: %w", err)
		}

		row := packRow{Attributes: make(map[string]any, len(loaded.Fields))}
		for i, col := range columns {
			v := values[i]
			switch col {
			case "id":
				if s := asText(v); s != nil {
					row.ID = *s
				}
			case "name":
				row.Name = asText(v)
			case "status":
				row.Status = asText(v)
			case "territory_id":
				row.TerritoryID = asText(v)
			case "subtype":
				row.Subtype = asInt(v)
			case "wkb":
				b, ok := v.([]byte)
				if !ok && v != nil {
					return count, fmt.Errorf("fieldpack: wkb column was not bytea (got %T)", v)
				}
				// Copy: the driver may reuse the backing array on the next Scan.
				row.WKB = append([]byte(nil), b...)
			case "geojson":
				if s := asText(v); s != nil {
					row.GeoJSON = *s
				}
			default:
				if _, ok := fieldSet[col]; ok {
					// Normalize []byte -> string the same way crud.scanRows does, so a
					// uuid/text field serializes as text (not base64) into the attrs JSON.
					if b, ok := v.([]byte); ok {
						row.Attributes[col] = string(b)
					} else {
						row.Attributes[col] = v
					}
				}
			}
		}

		if err := fn(row); err != nil {
			return count, err
		}
		count++
	}
	return count, rows.Err()
}

// asText coerces a generically-scanned value to *string, normalizing the []byte
// pgdriver hands back for text/uuid columns. NULL stays nil.
func asText(v any) *string {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		s := string(t)
		return &s
	case string:
		return &t
	default:
		s := fmt.Sprint(t)
		return &s
	}
}

// asInt coerces a generically-scanned integer column (subtype) to *int64. NULL
// stays nil. pgdriver returns integer columns as int64, but the other widths and
// a text fallback are handled defensively.
func asInt(v any) *int64 {
	switch t := v.(type) {
	case nil:
		return nil
	case int64:
		return &t
	case int32:
		x := int64(t)
		return &x
	case int:
		x := int64(t)
		return &x
	case []byte:
		if n, err := strconv.ParseInt(string(t), 10, 64); err == nil {
			return &n
		}
		return nil
	case string:
		if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			return &n
		}
		return nil
	default:
		return nil
	}
}
