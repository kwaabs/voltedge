package importer

// Live PostGIS source support (v2-S3, docs/etl-import-engine.md) -- the first
// import source that is NOT a file. Instead of streaming an uploaded artifact,
// the worker opens an outbound, READ-ONLY connection to a remote Postgres/PostGIS
// server and streams rows straight out of a chosen table (or a custom read-only
// SELECT). Each remote row is turned into the SAME *Feature shape the file
// readers produce, so the entire downstream apply spine (BuildAttributes ->
// AssembleGeometry -> crud validate -> upsert) is reused unchanged.
//
// Geometry has two paths, both landing on the shared spine:
//   - table-pick + geometry_mode "native": the reader discovers the table's
//     geometry/geography column and emits it as GeoJSON in the target class's
//     SRID (ST_AsGeoJSON(ST_Transform(col::geometry, dstSRID))), which
//     AssembleGeometry passes straight through as Feature.Geometry.
//   - custom query (or lonlat/wkt modes): the operator's SQL returns plain
//     columns (a WKT text column, or lon/lat numeric columns), and the shared
//     AssembleGeometry handles them exactly like a CSV.
//
// SECURITY. An outbound connection to an admin-authored host is an SSRF surface,
// so this file is deliberately defensive (see resolveDialAddr + the read-only
// posture in openRemotePostgres):
//   - host allow-list (IMPORT_DB_HOST_ALLOWLIST) and private/loopback/link-local
//     range blocking, so a profile can't be pointed at cloud metadata or an
//     internal service unless an operator explicitly allow-lists it;
//   - the connection is opened read-only at the session level
//     (default_transaction_read_only=on) with a statement_timeout, so even a
//     mis-authored query can neither write nor run unbounded;
//   - a custom query is additionally validated to be a single read-only SELECT;
//   - short dial/read/write timeouts and a tiny connection pool.
//
// Pure Go, no new dependency: the remote connection reuses the same
// bun/driver/pgdriver the app already links, driven directly through database/sql.

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun/driver/pgdriver"

	"github.com/kwaabs/voltedge/api/internal/secretbox"
)

const (
	// envDBHostAllowlist optionally restricts which hosts a live-DB import may
	// connect to (comma-separated host names / IPs, matched case-insensitively
	// against the profile's db_host). When set, a host not on it is refused. A
	// host that IS on it may also reach otherwise-blocked private ranges -- the
	// explicit opt-in for importing from an internal database.
	envDBHostAllowlist = "IMPORT_DB_HOST_ALLOWLIST"

	// liveGeomAlias is the reserved output-column name the table-pick native path
	// aliases its GeoJSON geometry to, so the reader can tell it apart from the
	// data columns. Unlikely to collide with a real column.
	liveGeomAlias = "__voltedge_geom__"

	// The live-DB sample size for the synchronous pre-flight dry-run -- the same
	// role gpkgDryRunSample plays for a GeoPackage.
	liveDryRunSample = 100

	dbDialTimeout      = 5 * time.Second
	dbReadTimeout      = 30 * time.Second
	dbWriteTimeout     = 10 * time.Second
	dbStatementTimeout = 60 * time.Second
	defaultPGPort      = 5432
)

// DBConnConfig is the resolved (plaintext) connection to a remote database:
// everything openRemotePostgres needs to dial. It is built either from a saved
// profile (worker: resolveConnConfig unseals the password) or from an inline
// request (admin test-connection / list-tables). The plaintext password lives
// only in memory for the life of one operation; it is never persisted or
// returned in the clear.
type DBConnConfig struct {
	Host     string
	Port     int
	Name     string
	User     string
	Password string
	SSLMode  string
	// Format is the live-DB source_format ("postgis" | "mssql"); it selects the
	// dialect for TestConnection / ListTables so the admin call sites stay
	// dialect-agnostic (they only ever hold a DBConnConfig).
	Format string
}

// ResolveConnConfig builds a live-DB connection from a resolved profile,
// unsealing the stored password with the credential cipher. A profile that has a
// sealed password but no cipher is configured is a clear error rather than a
// silent connect-without-auth. Exported so the admin editor's test-connection /
// list-tables actions can reuse it against a saved profile.
func ResolveConnConfig(rp *ResolvedProfile, cipher *secretbox.Cipher) (DBConnConfig, error) {
	p := rp.Profile
	cfg := DBConnConfig{
		SSLMode: p.DBSSLMode,
		Format:  p.SourceFormat,
	}
	if p.DBHost != nil {
		cfg.Host = *p.DBHost
	}
	if p.DBPort != nil {
		cfg.Port = *p.DBPort
	}
	if p.DBName != nil {
		cfg.Name = *p.DBName
	}
	if p.DBUser != nil {
		cfg.User = *p.DBUser
	}
	if cfg.Host == "" || cfg.Name == "" || cfg.User == "" {
		return DBConnConfig{}, invalidf("profile %q is missing database connection details (host, name, user)", p.Code)
	}
	if len(p.DBPasswordEnc) > 0 {
		if cipher == nil {
			return DBConnConfig{}, invalidf("profile %q has a stored database password but credential encryption is not configured (%s unset)", p.Code, secretbox.EnvKey)
		}
		pw, err := cipher.Decrypt(p.DBPasswordEnc)
		if err != nil {
			return DBConnConfig{}, fmt.Errorf("decrypting database password for profile %q: %w", p.Code, err)
		}
		cfg.Password = string(pw)
	}
	return cfg, nil
}

// resolveDialAddr validates a live-DB host and returns the exact host:port to
// dial. It is the SSRF guard: it resolves the host, refuses one that isn't on the
// allow-list (when one is configured), and refuses one that resolves into a
// private/loopback/link-local range unless the host is explicitly allow-listed.
// It returns a validated IP:port (not host:port) so the subsequent dial hits the
// address that was actually checked, closing the trivial DNS-rebinding window.
func resolveDialAddr(host string, port int) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", invalidf("database host is required")
	}
	if port <= 0 {
		port = defaultPGPort
	}

	allow := parseHostAllowlist(os.Getenv(envDBHostAllowlist))
	hostAllowed := allow[strings.ToLower(host)]
	if len(allow) > 0 && !hostAllowed {
		return "", invalidf("database host %q is not in the import connection allow-list (%s)", host, envDBHostAllowlist)
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return "", invalidf("cannot resolve database host %q: %v", host, err)
	}
	for _, ip := range ips {
		if isBlockedIP(ip) && !hostAllowed {
			continue
		}
		return net.JoinHostPort(ip.String(), strconv.Itoa(port)), nil
	}
	if !hostAllowed {
		return "", invalidf("database host %q resolves only to private/loopback addresses; add it to %s to import from an internal database", host, envDBHostAllowlist)
	}
	return "", invalidf("database host %q resolved to no usable address", host)
}

// parseHostAllowlist turns the comma-separated env value into a lower-cased set.
func parseHostAllowlist(raw string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		if h := strings.ToLower(strings.TrimSpace(part)); h != "" {
			out[h] = true
		}
	}
	return out
}

// isBlockedIP reports whether an address is in a range a live-DB import must not
// reach by default: loopback, RFC1918 / unique-local private, link-local
// (including the cloud metadata 169.254.0.0/16), multicast, or unspecified.
func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// openRemotePostgres opens a validated, read-only connection to the remote
// database. The read-only posture is enforced at the session level via startup
// connection parameters (applied to every pooled connection), so it holds for the
// streaming query without a per-statement wrapper; a statement_timeout bounds any
// single query. TLS follows the profile's sslmode. The connection is verified
// reachable with a ping before returning.
func openRemotePostgres(ctx context.Context, cfg DBConnConfig) (*sql.DB, error) {
	port := cfg.Port
	if port <= 0 {
		port = defaultPGPort
	}
	dialAddr, err := resolveDialAddr(cfg.Host, port)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Name) == "" || strings.TrimSpace(cfg.User) == "" {
		return nil, invalidf("database name and user are required")
	}

	opts := []pgdriver.Option{
		pgdriver.WithAddr(dialAddr),
		pgdriver.WithUser(cfg.User),
		pgdriver.WithDatabase(cfg.Name),
		pgdriver.WithApplicationName("voltedge-import"),
		pgdriver.WithDialTimeout(dbDialTimeout),
		pgdriver.WithReadTimeout(dbReadTimeout),
		pgdriver.WithWriteTimeout(dbWriteTimeout),
		pgdriver.WithConnParams(map[string]interface{}{
			"default_transaction_read_only":       "on",
			"statement_timeout":                   strconv.Itoa(int(dbStatementTimeout / time.Millisecond)),
			"idle_in_transaction_session_timeout": "30000",
		}),
	}
	if cfg.Password != "" {
		opts = append(opts, pgdriver.WithPassword(cfg.Password))
	}
	switch cfg.SSLMode {
	case "disable":
		opts = append(opts, pgdriver.WithInsecure(true))
	case "verify-full":
		opts = append(opts, pgdriver.WithTLSConfig(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}))
	default: // "require" (and any unexpected value): encrypt, don't verify the cert
		opts = append(opts, pgdriver.WithTLSConfig(&tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})) //nolint:gosec // "require" intentionally does not verify the certificate; verify-full does
	}

	sqldb := sql.OpenDB(pgdriver.NewConnector(opts...))
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

// TestConnection opens and pings a remote database, then closes it -- the
// authoring "Test connection" action. A failure is a validationError (400) with
// the underlying reason, so the admin can fix the host/credentials. It dispatches
// on cfg.Format so both live-DB dialects share the one admin endpoint.
func TestConnection(ctx context.Context, cfg DBConnConfig) error {
	if cfg.Format == "mssql" {
		return testMSSQLConnection(ctx, cfg)
	}
	sqldb, err := openRemotePostgres(ctx, cfg)
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

// ListTables returns the remote database's base tables and views (excluding the
// system schemas), each with its geometry/geography column if it has one, so the
// profile editor can offer a source-table picker. Names are schema-qualified only
// when not in the public schema, matching how buildLiveDBQuery parses source_layer
// back. FeatureCount is left zero -- counting every table would be too costly for
// a picker.
func ListTables(ctx context.Context, cfg DBConnConfig) ([]GpkgLayer, error) {
	if cfg.Format == "mssql" {
		return listMSSQLTables(ctx, cfg)
	}
	sqldb, err := openRemotePostgres(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer sqldb.Close()

	const q = `
SELECT t.table_schema, t.table_name,
       COALESCE((
           SELECT c.column_name FROM information_schema.columns c
           WHERE c.table_schema = t.table_schema
             AND c.table_name = t.table_name
             AND c.udt_name IN ('geometry', 'geography')
           ORDER BY c.ordinal_position
           LIMIT 1
       ), '') AS geom_col
FROM information_schema.tables t
WHERE t.table_type IN ('BASE TABLE', 'VIEW')
  AND t.table_schema NOT IN ('pg_catalog', 'information_schema')
ORDER BY t.table_schema, t.table_name`

	rows, err := sqldb.QueryContext(ctx, q)
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
		if schema != "public" {
			name = schema + "." + table
		}
		out = append(out, GpkgLayer{Name: name, GeometryColumn: geomCol})
	}
	return out, rows.Err()
}

// dbReader streams features from a remote database query, yielding the same
// *Feature shape the file readers do. A column named liveGeomAlias (present only
// on the table-pick native path) becomes Feature.Geometry (already GeoJSON in the
// target SRID); every other column becomes a Property. It implements rowSource.
type dbReader struct {
	rows      *sql.Rows
	colNames  []string
	geomAlias string // "" when the query carries no pre-built GeoJSON geometry column
	row       int
}

// Read returns the next remote row, or io.EOF at the end. A scan failure is
// fatal (mirrors the other readers); geometry problems don't arise here because
// the geometry was already rendered to GeoJSON (or is assembled downstream from
// plain columns).
func (r *dbReader) Read() (*Feature, int, error) {
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
		if r.geomAlias != "" && name == r.geomAlias {
			if gj := geoJSONFromScan(vals[i]); len(gj) > 0 {
				f.Geometry = gj
			}
			continue
		}
		f.Properties[name] = normalizePGValue(vals[i])
	}
	return f, r.row, nil
}

// geoJSONFromScan turns a scanned ST_AsGeoJSON value (text, delivered as []byte
// or string by the driver) into a raw JSON message. A NULL geometry scans to nil
// and yields no geometry.
func geoJSONFromScan(v any) json.RawMessage {
	switch t := v.(type) {
	case []byte:
		if len(t) == 0 {
			return nil
		}
		return json.RawMessage(append([]byte(nil), t...))
	case string:
		if t == "" {
			return nil
		}
		return json.RawMessage(t)
	default:
		return nil
	}
}

// normalizePGValue maps a raw driver value into the Go shapes the coercion step
// (mapping.go) expects, matching the file readers: TEXT/BYTEA -> string, integers
// -> float64 (JSON's number convention), timestamps -> RFC3339 text, NULL -> nil.
func normalizePGValue(v any) any {
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

// openLiveDBSource opens a streaming reader over a live database per the resolved
// profile: a custom read-only query if the profile carries one, else the chosen
// table (the run-time override or profile default, frozen in scope.SourceLayer).
// The returned cleanup closes the rows and the remote connection; the caller must
// always defer it. limit > 0 bounds the row count (the dry-run sample); 0 streams
// the whole source.
func openLiveDBSource(ctx context.Context, rp *ResolvedProfile, scope *ImportScope, cipher *secretbox.Cipher, limit int) (rowSource, func(), error) {
	if rp.Profile.SourceFormat == "mssql" {
		return openMSSQLLiveSource(ctx, rp, scope, cipher, limit)
	}
	cfg, err := ResolveConnConfig(rp, cipher)
	if err != nil {
		return nil, nil, err
	}
	sqldb, err := openRemotePostgres(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}

	query, geomAlias, err := buildLiveDBQuery(ctx, sqldb, rp, scope.SourceLayer, limit)
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
	return &dbReader{rows: rows, colNames: cols, geomAlias: geomAlias}, cleanup, nil
}

// buildLiveDBQuery produces the SQL the live import runs plus the geometry alias
// (empty when geometry is assembled downstream). A profile with a custom
// source_query uses it verbatim (validated read-only, wrapped for the sample
// limit); otherwise it builds a SELECT over the chosen table, discovering the
// data columns and, for geometry_mode native, the geometry column -- which it
// emits as GeoJSON in the target class SRID.
func buildLiveDBQuery(ctx context.Context, sqldb *sql.DB, rp *ResolvedProfile, layer string, limit int) (query, geomAlias string, err error) {
	if rp.Profile.SourceQuery != nil && strings.TrimSpace(*rp.Profile.SourceQuery) != "" {
		q := strings.TrimSpace(*rp.Profile.SourceQuery)
		if err := validateReadOnlyQuery(q); err != nil {
			return "", "", err
		}
		q = strings.TrimRight(q, "; \t\r\n")
		if limit > 0 {
			// Wrap so the sample limit is safe regardless of the query's own shape.
			q = fmt.Sprintf("SELECT * FROM (%s) AS __voltedge_sample LIMIT %d", q, limit)
		}
		return q, "", nil
	}

	table := strings.TrimSpace(layer)
	if table == "" {
		return "", "", invalidf("this profile imports from a live database but no source table is set")
	}
	return buildTableQuery(ctx, sqldb, rp, table, limit)
}

// buildTableQuery introspects one remote table and builds the streaming SELECT:
// every non-geometry column as-is, plus (for native geometry) its geometry column
// rendered to GeoJSON in the target SRID.
func buildTableQuery(ctx context.Context, sqldb *sql.DB, rp *ResolvedProfile, table string, limit int) (query, geomAlias string, err error) {
	schema, name := "public", table
	if i := strings.Index(table, "."); i >= 0 {
		schema, name = table[:i], table[i+1:]
	}
	if !validIdent(schema) || !validIdent(name) {
		return "", "", invalidf("source table %q is not a valid table name", table)
	}

	cols, geomCol, err := introspectTable(ctx, sqldb, schema, name)
	if err != nil {
		return "", "", err
	}
	if len(cols) == 0 && geomCol == "" {
		return "", "", invalidf("table %q was not found or has no columns", table)
	}

	native := rp.Profile.GeometryMode == "native"
	var sb strings.Builder
	sb.WriteString("SELECT ")
	first := true
	for _, c := range cols {
		if !first {
			sb.WriteString(", ")
		}
		sb.WriteString(quoteIdent(c))
		first = false
	}
	if native && geomCol != "" {
		if !first {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "ST_AsGeoJSON(ST_Transform(%s::geometry, %d)) AS %s", quoteIdent(geomCol), rp.srid(), quoteIdent(liveGeomAlias))
		geomAlias = liveGeomAlias
		first = false
	}
	if first {
		return "", "", invalidf("table %q has no importable columns", table)
	}
	fmt.Fprintf(&sb, " FROM %s.%s", quoteIdent(schema), quoteIdent(name))
	if limit > 0 {
		fmt.Fprintf(&sb, " LIMIT %d", limit)
	}
	return sb.String(), geomAlias, nil
}

// introspectTable returns a table's non-geometry column names and its first
// geometry/geography column (empty if none), from information_schema.
func introspectTable(ctx context.Context, sqldb *sql.DB, schema, name string) (cols []string, geomCol string, err error) {
	rows, err := sqldb.QueryContext(ctx, `
SELECT column_name, udt_name
FROM information_schema.columns
WHERE table_schema = $1 AND table_name = $2
ORDER BY ordinal_position`, schema, name)
	if err != nil {
		return nil, "", invalidf("inspecting table %q.%q: %v", schema, name, err)
	}
	defer rows.Close()

	for rows.Next() {
		var col, udt string
		if err := rows.Scan(&col, &udt); err != nil {
			return nil, "", fmt.Errorf("scanning column metadata: %w", err)
		}
		if geomCol == "" && (udt == "geometry" || udt == "geography") {
			geomCol = col
			continue
		}
		cols = append(cols, col)
	}
	return cols, geomCol, rows.Err()
}

// sampleLiveDB reads up to n rows from a live database source for the synchronous
// pre-flight dry-run -- the live-DB analogue of sampleSQLite.
func sampleLiveDB(ctx context.Context, rp *ResolvedProfile, scope *ImportScope, cipher *secretbox.Cipher, n int) ([]*Feature, error) {
	src, cleanup, err := openLiveDBSource(ctx, rp, scope, cipher, n)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	out := make([]*Feature, 0, n)
	for len(out) < n {
		f, _, readErr := src.Read()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
		out = append(out, f)
	}
	return out, nil
}

// isLiveDBSourceFormat reports whether a source format is a live outbound
// database connection rather than a file: PostGIS (S3) and MSSQL (S4).
func isLiveDBSourceFormat(format string) bool {
	return format == "postgis" || format == "mssql"
}

// validIdent guards a schema/table identifier taken from the profile's
// source_layer before it is quoted into SQL: a conservative subset (letters,
// digits, underscore, starting with a letter/underscore). Defensive only -- the
// value is admin-authored and also quoted -- but it keeps a surprising name from
// reaching the query builder.
func validIdent(s string) bool {
	return identPattern.MatchString(s)
}

var identPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// writeKeywordPattern matches data-modifying / DDL keywords as whole words. The
// real read-only guarantee is the read-only session (openRemotePostgres); this is
// a fast, legible second line that rejects an obviously-wrong custom query before
// it is ever sent.
var writeKeywordPattern = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|alter|create|truncate|grant|revoke|copy|call|do|vacuum|merge|comment|reindex|cluster|lock)\b`)

// validateReadOnlyQuery checks a custom source query is a single read-only SELECT
// (or WITH ... SELECT). It rejects multiple statements and any write/DDL keyword.
func validateReadOnlyQuery(q string) error {
	s := strings.TrimSpace(q)
	s = strings.TrimRight(s, "; \t\r\n")
	if s == "" {
		return invalidf("custom query is empty")
	}
	if strings.Contains(s, ";") {
		return invalidf("custom query must be a single statement (remove the ';')")
	}
	low := strings.ToLower(s)
	if !strings.HasPrefix(low, "select") && !strings.HasPrefix(low, "with") {
		return invalidf("custom query must be a read-only SELECT")
	}
	if m := writeKeywordPattern.FindString(s); m != "" {
		return invalidf("custom query must be read-only (found %q)", strings.ToLower(m))
	}
	return nil
}
