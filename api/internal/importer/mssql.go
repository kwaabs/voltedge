package importer

// Live SQL Server / MSSQL source support (v2-S4, docs/etl-import-engine.md) --
// the second live-database connector after PostGIS (postgis.go). It mirrors that
// file's shape: an outbound connection to an admin-authored server, streaming
// rows out of a chosen table (or a custom read-only SELECT) into the same
// *Feature the file readers produce, so the whole downstream apply spine is
// reused unchanged. Everything that is dialect-independent -- the SSRF guard
// (resolveDialAddr / isBlockedIP / the host allow-list), the identifier guard
// (validIdent), the read-only custom-query validator (validateReadOnlyQuery), the
// resolved DBConnConfig and its ResolveConnConfig unsealer -- lives in postgis.go
// and is shared verbatim; this file adds only the SQL Server dialect.
//
// GEOMETRY. SQL Server has no ST_AsGeoJSON and no in-SQL reprojection, so unlike
// PostGIS the reader can't emit GeoJSON in the target SRID directly. Instead it
// selects the spatial column as standard WKB (col.STAsBinary()) plus its SRID
// (col.STSrid); the WKB rides on the Feature (rawGeom, geomKindWKB) and is
// converted + reprojected downstream by the shared wkbToGeoJSON PostGIS round-trip
// -- the same deferred, per-row-failure path GeoPackage/SpatiaLite use. SQL
// Server's GEOGRAPHY type emits WKB in latitude-longitude order, so a geography
// column sets rawGeomFlipXY to have the coordinates swapped during conversion.
//
// READ-ONLY. SQL Server has no session-level read-only transaction toggle like
// Postgres's default_transaction_read_only, so the read-only posture here rests on
// (a) the connector only ever issuing SELECTs for table reads, (b) a custom query
// being validated to a single read-only SELECT, and (c) the strong recommendation
// (docs) that the profile connect with a least-privileged, read-only login. TLS
// follows the profile's sslmode the same way PostGIS's does.
//
// Dependency: github.com/microsoft/go-mssqldb (pure Go, cgo-free), registered as
// the database/sql driver "sqlserver".

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	_ "github.com/microsoft/go-mssqldb"

	"github.com/kwaabs/voltedge/api/internal/secretbox"
)

const (
	// liveSRIDAlias is the reserved output-column name the MSSQL native path aliases
	// each row's geometry SRID to (from col.STSrid), so the reader can pick it apart
	// from the data columns alongside liveGeomAlias.
	liveSRIDAlias = "__voltedge_srid__"

	// mssqlDefaultSchema is SQL Server's default schema, used when a source_layer
	// carries a bare table name (no "schema." prefix).
	mssqlDefaultSchema = "dbo"

	defaultMSSQLPort = 1433
)

// openRemoteMSSQL opens a validated connection to a remote SQL Server. The host is
// resolved and SSRF-checked (resolveDialAddr), and the connection is made to the
// validated IP directly (no re-resolution), with the real host name carried in
// hostNameInCertificate for verify-full cert validation. TLS follows sslmode. The
// connection is verified reachable with a ping before returning.
func openRemoteMSSQL(ctx context.Context, cfg DBConnConfig) (*sql.DB, error) {
	port := cfg.Port
	if port <= 0 {
		port = defaultMSSQLPort
	}
	dialAddr, err := resolveDialAddr(cfg.Host, port)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Name) == "" || strings.TrimSpace(cfg.User) == "" {
		return nil, invalidf("database name and user are required")
	}

	q := url.Values{}
	q.Set("database", cfg.Name)
	q.Set("app name", "voltedge-import")
	q.Set("dial timeout", strconv.Itoa(int(dbDialTimeout/time.Second)))
	q.Set("connection timeout", strconv.Itoa(int((dbDialTimeout+dbReadTimeout)/time.Second)))
	switch cfg.SSLMode {
	case "disable":
		q.Set("encrypt", "disable")
	case "verify-full":
		q.Set("encrypt", "true")
		q.Set("TrustServerCertificate", "false")
		q.Set("hostNameInCertificate", cfg.Host)
	default: // "require" (and any unexpected value): encrypt, don't verify the cert
		q.Set("encrypt", "true")
		q.Set("TrustServerCertificate", "true")
	}

	dsn := (&url.URL{
		Scheme:   "sqlserver",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     dialAddr,
		RawQuery: q.Encode(),
	}).String()

	sqldb, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, invalidf("opening SQL Server connection: %v", err)
	}
	sqldb.SetMaxOpenConns(2)
	sqldb.SetMaxIdleConns(1)
	sqldb.SetConnMaxLifetime(5 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, dbDialTimeout+dbReadTimeout)
	defer cancel()
	if err := sqldb.PingContext(pingCtx); err != nil {
		_ = sqldb.Close()
		return nil, invalidf("cannot connect to database %q on %s: %v", cfg.Name, cfg.Host, err)
	}
	return sqldb, nil
}

// testMSSQLConnection opens, pings, and test-queries a remote SQL Server, then
// closes it -- the MSSQL side of the "Test connection" action.
func testMSSQLConnection(ctx context.Context, cfg DBConnConfig) error {
	sqldb, err := openRemoteMSSQL(ctx, cfg)
	if err != nil {
		return err
	}
	defer sqldb.Close()

	var one int
	if err := sqldb.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return invalidf("connected to %q but a test query failed: %v", cfg.Name, err)
	}
	return nil
}

// listMSSQLTables returns the remote database's base tables and views (excluding
// the system schemas), each with its first spatial column, for the source-table
// picker. Names are schema-qualified only when not in the default (dbo) schema,
// matching how buildMSSQLTableQuery parses source_layer back. FeatureCount is left
// zero -- counting every table would be too costly for a picker.
func listMSSQLTables(ctx context.Context, cfg DBConnConfig) ([]GpkgLayer, error) {
	sqldb, err := openRemoteMSSQL(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer sqldb.Close()

	const query = `
SELECT t.TABLE_SCHEMA, t.TABLE_NAME,
       COALESCE((
           SELECT TOP 1 c.COLUMN_NAME FROM INFORMATION_SCHEMA.COLUMNS c
           WHERE c.TABLE_SCHEMA = t.TABLE_SCHEMA
             AND c.TABLE_NAME = t.TABLE_NAME
             AND c.DATA_TYPE IN ('geometry', 'geography')
           ORDER BY c.ORDINAL_POSITION
       ), '') AS geom_col
FROM INFORMATION_SCHEMA.TABLES t
WHERE t.TABLE_TYPE IN ('BASE TABLE', 'VIEW')
  AND t.TABLE_SCHEMA NOT IN ('sys', 'INFORMATION_SCHEMA')
ORDER BY t.TABLE_SCHEMA, t.TABLE_NAME`

	rows, err := sqldb.QueryContext(ctx, query)
	if err != nil {
		return nil, invalidf("listing tables in %q: %v", cfg.Name, err)
	}
	defer rows.Close()

	var out []GpkgLayer
	for rows.Next() {
		var schema, table, geomCol string
		if err := rows.Scan(&schema, &table, &geomCol); err != nil {
			return nil, fmt.Errorf("scanning table metadata: %w", err)
		}
		name := table
		if schema != mssqlDefaultSchema {
			name = schema + "." + table
		}
		out = append(out, GpkgLayer{Name: name, GeometryColumn: geomCol})
	}
	return out, rows.Err()
}

// mssqlReader streams features from a remote SQL Server query, yielding the same
// *Feature shape the other readers do. The reserved liveGeomAlias column carries
// standard WKB (from STAsBinary) -> Feature.rawGeom (geomKindWKB); the reserved
// liveSRIDAlias column carries that geometry's SRID -> Feature.rawSRID; every other
// column becomes a Property. flipXY (set for a geography source) is stamped on each
// Feature so the shared conversion swaps its lat/long axis order. It implements
// rowSource.
type mssqlReader struct {
	rows      *sql.Rows
	colNames  []string
	geomAlias string // "" when the query carries no WKB geometry column
	sridAlias string // "" when no SRID column is present
	flipXY    bool
	row       int
}

// Read returns the next remote row, or io.EOF at the end. A scan failure is fatal
// (mirrors the other readers); a geometry problem doesn't arise here -- the WKB
// rides on the Feature and fails, if at all, downstream in AssembleGeometry.
func (r *mssqlReader) Read() (*Feature, int, error) {
	if !r.rows.Next() {
		if err := r.rows.Err(); err != nil {
			return nil, r.row, fmt.Errorf("reading source database row: %w", err)
		}
		return nil, r.row, io.EOF
	}
	r.row++

	vals := make([]any, len(r.colNames))
	ptrs := make([]any, len(r.colNames))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := r.rows.Scan(ptrs...); err != nil {
		return nil, r.row, fmt.Errorf("scanning source database row %d: %w", r.row, err)
	}

	f := &Feature{Type: "Feature", Properties: make(map[string]any, len(r.colNames))}
	for i, name := range r.colNames {
		switch {
		case r.geomAlias != "" && name == r.geomAlias:
			if blob, ok := vals[i].([]byte); ok && len(blob) > 0 {
				f.rawGeom = append([]byte(nil), blob...)
				f.rawGeomKind = geomKindWKB
				f.rawGeomFlipXY = r.flipXY
			}
		case r.sridAlias != "" && name == r.sridAlias:
			if srid, ok := toIntValue(vals[i]); ok {
				f.rawSRID = srid
			}
		default:
			f.Properties[name] = normalizeMSSQLValue(vals[i])
		}
	}
	return f, r.row, nil
}

// normalizeMSSQLValue maps a raw go-mssqldb value into the Go shapes the coercion
// step (mapping.go) expects, matching the other readers: text/binary -> string,
// integers -> float64 (JSON's number convention), timestamps -> RFC3339 text,
// bool/float stay as-is, NULL -> nil.
func normalizeMSSQLValue(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case int64:
		return float64(t)
	case int32:
		return float64(t)
	case time.Time:
		return t.Format(time.RFC3339)
	default:
		return v
	}
}

// toIntValue coerces a scanned SRID cell (which the driver may deliver as any
// integer width) to an int.
func toIntValue(v any) (int, bool) {
	switch t := v.(type) {
	case int64:
		return int(t), true
	case int32:
		return int(t), true
	case int:
		return t, true
	default:
		return 0, false
	}
}

// openMSSQLLiveSource opens a streaming reader over a remote SQL Server per the
// resolved profile: a custom read-only query if the profile carries one, else the
// chosen table. The returned cleanup closes the rows and the connection; the caller
// must always defer it. limit > 0 bounds the row count (the dry-run sample); 0
// streams the whole source.
func openMSSQLLiveSource(ctx context.Context, rp *ResolvedProfile, scope *ImportScope, cipher *secretbox.Cipher, limit int) (rowSource, func(), error) {
	cfg, err := ResolveConnConfig(rp, cipher)
	if err != nil {
		return nil, nil, err
	}
	sqldb, err := openRemoteMSSQL(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}

	query, geomAlias, sridAlias, flipXY, err := buildMSSQLQuery(ctx, sqldb, rp, scope.SourceLayer, limit)
	if err != nil {
		_ = sqldb.Close()
		return nil, nil, err
	}

	rows, err := sqldb.QueryContext(ctx, query)
	if err != nil {
		_ = sqldb.Close()
		return nil, nil, invalidf("querying source database: %v", err)
	}
	cols, err := rows.Columns()
	if err != nil {
		_ = rows.Close()
		_ = sqldb.Close()
		return nil, nil, fmt.Errorf("reading source columns: %w", err)
	}

	cleanup := func() {
		_ = rows.Close()
		_ = sqldb.Close()
	}
	return &mssqlReader{rows: rows, colNames: cols, geomAlias: geomAlias, sridAlias: sridAlias, flipXY: flipXY}, cleanup, nil
}

// buildMSSQLQuery produces the SQL a live MSSQL import runs plus the geometry/SRID
// aliases and the axis-flip flag. A custom source_query is used verbatim (validated
// read-only; the dry-run sample is bounded by the reader stopping early, not by a
// TOP wrapper, so a query with its own ORDER BY still runs). Otherwise it builds a
// SELECT over the chosen table, discovering the data columns and -- for geometry_mode
// native -- the spatial column, emitted as WKB + SRID.
func buildMSSQLQuery(ctx context.Context, sqldb *sql.DB, rp *ResolvedProfile, layer string, limit int) (query, geomAlias, sridAlias string, flipXY bool, err error) {
	if rp.Profile.SourceQuery != nil && strings.TrimSpace(*rp.Profile.SourceQuery) != "" {
		q := strings.TrimSpace(*rp.Profile.SourceQuery)
		if err := validateReadOnlyQuery(q); err != nil {
			return "", "", "", false, err
		}
		return strings.TrimRight(q, "; \t\r\n"), "", "", false, nil
	}

	table := strings.TrimSpace(layer)
	if table == "" {
		return "", "", "", false, invalidf("this profile imports from a live database but no source table is set")
	}
	return buildMSSQLTableQuery(ctx, sqldb, rp, table, limit)
}

// buildMSSQLTableQuery introspects one remote table and builds the streaming
// SELECT: every non-spatial column as-is, plus (for native geometry) its spatial
// column rendered to WKB (STAsBinary) with its SRID (STSrid). A geography column
// requests an axis flip downstream.
func buildMSSQLTableQuery(ctx context.Context, sqldb *sql.DB, rp *ResolvedProfile, table string, limit int) (query, geomAlias, sridAlias string, flipXY bool, err error) {
	schema, name := mssqlDefaultSchema, table
	if i := strings.Index(table, "."); i >= 0 {
		schema, name = table[:i], table[i+1:]
	}
	if !validIdent(schema) || !validIdent(name) {
		return "", "", "", false, invalidf("source table %q is not a valid table name", table)
	}

	cols, geomCol, geomType, err := introspectMSSQLTable(ctx, sqldb, schema, name)
	if err != nil {
		return "", "", "", false, err
	}
	if len(cols) == 0 && geomCol == "" {
		return "", "", "", false, invalidf("table %q was not found or has no columns", table)
	}

	native := rp.Profile.GeometryMode == "native"
	var sb strings.Builder
	sb.WriteString("SELECT ")
	if limit > 0 {
		fmt.Fprintf(&sb, "TOP %d ", limit)
	}
	first := true
	for _, c := range cols {
		if !first {
			sb.WriteString(", ")
		}
		sb.WriteString(bracketIdent(c))
		first = false
	}
	if native && geomCol != "" {
		if !first {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "%s.STAsBinary() AS %s, %s.STSrid AS %s",
			bracketIdent(geomCol), bracketIdent(liveGeomAlias), bracketIdent(geomCol), bracketIdent(liveSRIDAlias))
		geomAlias = liveGeomAlias
		sridAlias = liveSRIDAlias
		flipXY = strings.EqualFold(geomType, "geography")
		first = false
	}
	if first {
		return "", "", "", false, invalidf("table %q has no importable columns", table)
	}
	fmt.Fprintf(&sb, " FROM %s.%s", bracketIdent(schema), bracketIdent(name))
	return sb.String(), geomAlias, sridAlias, flipXY, nil
}

// introspectMSSQLTable returns a table's non-spatial column names and its first
// spatial column with that column's type (geometry/geography), from
// INFORMATION_SCHEMA.
func introspectMSSQLTable(ctx context.Context, sqldb *sql.DB, schema, name string) (cols []string, geomCol, geomType string, err error) {
	rows, err := sqldb.QueryContext(ctx, `
SELECT COLUMN_NAME, DATA_TYPE
FROM INFORMATION_SCHEMA.COLUMNS
WHERE TABLE_SCHEMA = @p1 AND TABLE_NAME = @p2
ORDER BY ORDINAL_POSITION`, schema, name)
	if err != nil {
		return nil, "", "", invalidf("inspecting table %q.%q: %v", schema, name, err)
	}
	defer rows.Close()

	for rows.Next() {
		var col, dataType string
		if err := rows.Scan(&col, &dataType); err != nil {
			return nil, "", "", fmt.Errorf("scanning column metadata: %w", err)
		}
		if geomCol == "" && (strings.EqualFold(dataType, "geometry") || strings.EqualFold(dataType, "geography")) {
			geomCol, geomType = col, dataType
			continue
		}
		cols = append(cols, col)
	}
	return cols, geomCol, geomType, rows.Err()
}

// bracketIdent quotes a SQL Server identifier with [ ], escaping any ] as ]].
func bracketIdent(s string) string {
	return "[" + strings.ReplaceAll(s, "]", "]]") + "]"
}
