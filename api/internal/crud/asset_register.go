package crud

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// Asset-register scoped scan (Slice 1, the per-feature sibling of the BoQ
// quantity roll-up). Where ClassQuantities groups a class into (subtype,
// material) buckets to price, this returns one row per in-scope feature -- an
// itemised asset register, exportable as a spreadsheet. It reuses the exact same
// boqWhere narrowing (soft-delete, optional spatial/id scope, Phase 58 territory
// read-enforcement, Phase 61 private-drafts), so a scoped user can never register
// an asset they couldn't list.

// AssetRegisterRow is one feature's row in the register: its identity, a flat set
// of common columns present on every compiled class, and -- for a linear class --
// its own geodesic LengthKm (a discrete feature's quantity is 1). Optional
// columns a class lacks come back as the zero NullXxx (scanned from a typed NULL),
// so the scan shape is uniform whatever the class carries.
type AssetRegisterRow struct {
	FeatureID    string
	Subtype      sql.NullInt64
	Name         sql.NullString
	Status       sql.NullString
	MaterialCode sql.NullString
	LengthKm     float64 // 0 for a discrete class (quantity is 1 each)
	// Lon/Lat is a point asset's location, a line's START endpoint, or a
	// polygon's point-on-surface. ToLon/ToLat is a line's END endpoint (NULL for
	// points and polygons): a line's spatial fact is its two endpoints, not a
	// single representative point.
	Lon         sql.NullFloat64
	Lat         sql.NullFloat64
	ToLon       sql.NullFloat64
	ToLat       sql.NullFloat64
	TerritoryID sql.NullString
	CircuitID    sql.NullString
	Ownership    sql.NullString
	ExternalRef  sql.NullString // external_ref jsonb rendered as compact text
	CreatedAt    sql.NullString
}

// AssetRegisterRows returns the in-scope features of one class as itemised rows.
// materialField is the class's material-backed coded field (or "" when it has
// none); linear asks for each row's geodesic length (true only for a
// line-geometry class). limit caps the rows returned (0 = no cap); the engine
// passes the remaining budget so a whole-network register stays bounded.
func AssetRegisterRows(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, materialField string, linear bool, scope BoQScope, limit int) ([]AssetRegisterRow, error) {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return nil, err
	}

	whereSQL, args, err := boqWhere(ctx, db, loaded, scope)
	if err != nil {
		return nil, err
	}

	hasGeom := loaded.FeatureClass.GeometryType != "none"

	sel := []string{"id::text AS feature_id"}
	if len(loaded.Subtypes) > 0 {
		sel = append(sel, "subtype")
	} else {
		sel = append(sel, "NULL::integer AS subtype")
	}
	sel = append(sel, "name", "status")
	if materialField != "" {
		sel = append(sel, quoteIdent(materialField)+"::text AS material_code")
	} else {
		sel = append(sel, "NULL::text AS material_code")
	}
	if linear && hasGeom {
		sel = append(sel, "COALESCE(ST_Length(ST_Transform(geom, 4326)::geography), 0)::double precision / 1000 AS length_km")
	} else {
		sel = append(sel, "0::double precision AS length_km")
	}
	lonE, latE, toLonE, toLatE := geomPointExprs(loaded.FeatureClass.GeometryType)
	sel = append(sel, lonE+" AS lon", latE+" AS lat", toLonE+" AS to_lon", toLatE+" AS to_lat")
	if hasGeom {
		sel = append(sel, "territory_id::text AS territory_id")
	} else {
		sel = append(sel, "NULL::text AS territory_id")
	}
	sel = append(sel, colText(loaded, "circuit_id")+" AS circuit_id")
	sel = append(sel, colText(loaded, "ownership")+" AS ownership")
	sel = append(sel, "external_ref::text AS external_ref", "created_at::text AS created_at")

	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY name NULLS LAST, id", strings.Join(sel, ", "), table, whereSQL)
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("crud: asset register: %w", err)
	}
	defer rows.Close()

	var out []AssetRegisterRow
	for rows.Next() {
		var r AssetRegisterRow
		if err := rows.Scan(
			&r.FeatureID, &r.Subtype, &r.Name, &r.Status, &r.MaterialCode, &r.LengthKm,
			&r.Lon, &r.Lat, &r.ToLon, &r.ToLat, &r.TerritoryID, &r.CircuitID, &r.Ownership, &r.ExternalRef, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("crud: asset register scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("crud: asset register rows: %w", err)
	}
	return out, nil
}

// colText renders a qualified ::text reference to an optional metadata field, or
// a typed NULL when the class doesn't carry it -- the asset-register twin of the
// spatial package's colOrNull, so the flat column set stays uniform across
// classes that have circuit_id / ownership and classes that don't.
func colText(loaded *metadata.Loaded, field string) string {
	if loaded.FieldByName(field) != nil {
		return quoteIdent(field) + "::text"
	}
	return "NULL::text"
}

// geomPointExprs returns the (lon, lat, toLon, toLat) select expressions for a
// class's geometry type. A point reports its own coordinates; a line reports its
// two ENDPOINTS (start in lon/lat, end in toLon/toLat) -- a single centroid
// misrepresents a line, whose spatial facts are its endpoints and its length; a
// polygon reports a guaranteed-interior point-on-surface. A geometry-less class
// (or a NULL geom) yields NULLs throughout. All coordinates are WGS84 (4326).
func geomPointExprs(geomType string) (lon, lat, toLon, toLat string) {
	null := "NULL::double precision"
	switch geomType {
	case "point":
		p := "ST_Centroid(ST_Transform(geom, 4326))"
		return "ST_X(" + p + ")", "ST_Y(" + p + ")", null, null
	case "line":
		g := "ST_Transform(geom, 4326)"
		return "ST_X(ST_StartPoint(" + g + "))", "ST_Y(ST_StartPoint(" + g + "))",
			"ST_X(ST_EndPoint(" + g + "))", "ST_Y(ST_EndPoint(" + g + "))"
	case "polygon":
		p := "ST_PointOnSurface(ST_Transform(geom, 4326))"
		return "ST_X(" + p + ")", "ST_Y(" + p + ")", null, null
	default:
		return null, null, null, null
	}
}

// AttrRow is one feature's row for the per-class (full-attribute) register used
// by the XLSX export: the common context columns plus every requested metadata
// field, each field rendered as text (Fields, parallel to the fieldNames passed
// in). Every value is nullable; the engine decides numeric-vs-text per column
// from the field's data_type. LengthKm is set only for a linear class.
type AttrRow struct {
	ID          string
	Name        sql.NullString
	Status      sql.NullString
	Subtype     sql.NullInt64
	LengthKm    sql.NullFloat64
	// Lon/Lat = point location / line start / polygon point-on-surface; ToLon/ToLat
	// = line end endpoint (NULL otherwise). See geomPointExprs.
	Lon         sql.NullFloat64
	Lat         sql.NullFloat64
	ToLon       sql.NullFloat64
	ToLat       sql.NullFloat64
	TerritoryID sql.NullString
	ExternalRef sql.NullString
	CreatedAt   sql.NullString
	Fields      []sql.NullString // parallel to the fieldNames argument, all ::text
}

// AssetRegisterAttributeRows returns the in-scope features of one class as
// full-attribute rows: the common context columns plus every field in fieldNames
// (rendered ::text, in order). It is the per-class-sheet counterpart to
// AssetRegisterRows and applies the identical boqWhere scoping. fieldNames must
// be real, active field columns of the class (the engine passes loaded.Fields),
// so each quotes safely to an existing column.
func AssetRegisterAttributeRows(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, fieldNames []string, linear bool, scope BoQScope, limit int) ([]AttrRow, error) {
	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return nil, err
	}
	whereSQL, args, err := boqWhere(ctx, db, loaded, scope)
	if err != nil {
		return nil, err
	}

	hasGeom := loaded.FeatureClass.GeometryType != "none"

	sel := []string{"id::text AS feature_id", "name", "status"}
	if len(loaded.Subtypes) > 0 {
		sel = append(sel, "subtype")
	} else {
		sel = append(sel, "NULL::integer AS subtype")
	}
	if linear && hasGeom {
		sel = append(sel, "COALESCE(ST_Length(ST_Transform(geom, 4326)::geography), 0)::double precision / 1000 AS length_km")
	} else {
		sel = append(sel, "NULL::double precision AS length_km")
	}
	lonE, latE, toLonE, toLatE := geomPointExprs(loaded.FeatureClass.GeometryType)
	sel = append(sel, lonE+" AS lon", latE+" AS lat", toLonE+" AS to_lon", toLatE+" AS to_lat")
	if hasGeom {
		sel = append(sel, "territory_id::text AS territory_id")
	} else {
		sel = append(sel, "NULL::text AS territory_id")
	}
	sel = append(sel, "external_ref::text AS external_ref", "created_at::text AS created_at")
	for _, f := range fieldNames {
		sel = append(sel, quoteIdent(f)+"::text")
	}

	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY name NULLS LAST, id", strings.Join(sel, ", "), table, whereSQL)
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("crud: asset register attributes: %w", err)
	}
	defer rows.Close()

	var out []AttrRow
	for rows.Next() {
		r := AttrRow{Fields: make([]sql.NullString, len(fieldNames))}
		dest := []any{&r.ID, &r.Name, &r.Status, &r.Subtype, &r.LengthKm, &r.Lon, &r.Lat, &r.ToLon, &r.ToLat, &r.TerritoryID, &r.ExternalRef, &r.CreatedAt}
		for i := range r.Fields {
			dest = append(dest, &r.Fields[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("crud: asset register attributes scan: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("crud: asset register attributes rows: %w", err)
	}
	return out, nil
}
