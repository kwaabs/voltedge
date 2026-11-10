// Package dbexplorer is the in-app DATABASE EXPLORER: a schema browser and SQL
// runner for the highest-privilege operators. It is mounted behind admin:db
// (granted only to admin / break-glass-admin — migration 0188), so the whole
// surface is already gated to people who, by design, have full authority over the
// instance.
//
//	GET  /objects                           the schema catalog (schemas, tables,
//	                                         views, matviews, functions, procedures)
//	GET  /relations/{schema}/{name}/columns  a table/view's columns
//	GET  /routines/{schema}/{name}           a function/procedure's definition
//	POST /query                              run SQL and return rows (or rows-affected)
//
// Guardrails on the runner, in spite of the trusted audience: it is STRICTLY READ
// ONLY — every statement runs inside a READ ONLY transaction, so Postgres itself
// rejects any INSERT/UPDATE/DELETE/DDL (there is no write path and nothing is ever
// committed). On top of that each run gets a 15s statement_timeout and a row cap
// (default 1000, max 5000) with a `truncated` flag, and every execution — success
// or failure — is written to the structured audit log with the acting user.
package dbexplorer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
)

const (
	defaultMaxRows   = 1000
	hardMaxRows      = 5000
	queryHardTimeout = 30 * time.Second
	auditSQLMax      = 4000 // cap the SQL we copy into the audit log
)

// Routes returns the /db-explorer router. The mount gates every method on admin:db
// (see cmd/server/main.go), so no per-route gate is needed here.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/objects", objectsHandler(db))
	r.Get("/relations/{schema}/{name}/columns", columnsHandler(db))
	r.Get("/routines/{schema}/{name}", routineHandler(db))
	r.Post("/query", queryHandler(db))
	return r
}

// ---- catalog browsing ----

type relation struct {
	Schema      string `json:"schema"`
	Name        string `json:"name"`
	Kind        string `json:"kind"` // table / view / materialized_view / partitioned_table / foreign_table
	RowEstimate *int64 `json:"row_estimate"`
}

type routine struct {
	Schema string `json:"schema"`
	Name   string `json:"name"`
	Kind   string `json:"kind"` // function / procedure / aggregate / window
	Args   string `json:"args"`
}

type catalog struct {
	Schemas   []string   `json:"schemas"`
	Relations []relation `json:"relations"`
	Routines  []routine  `json:"routines"`
}

func relkindName(k string) string {
	switch k {
	case "r":
		return "table"
	case "v":
		return "view"
	case "m":
		return "materialized_view"
	case "p":
		return "partitioned_table"
	case "f":
		return "foreign_table"
	default:
		return k
	}
}

func prokindName(k string) string {
	switch k {
	case "f":
		return "function"
	case "p":
		return "procedure"
	case "a":
		return "aggregate"
	case "w":
		return "window"
	default:
		return k
	}
}

// objectsHandler returns the whole catalog of user (non-system) schema objects, so
// the client can render the browser tree in one read.
func objectsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		out := catalog{Schemas: []string{}, Relations: []relation{}, Routines: []routine{}}

		schemaRows, err := db.DB.QueryContext(ctx, `
SELECT nspname FROM pg_namespace
WHERE nspname NOT IN ('pg_catalog','information_schema') AND nspname NOT LIKE 'pg\_%' ESCAPE '\'
ORDER BY nspname`)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "listing schemas: "+err.Error())
			return
		}
		for schemaRows.Next() {
			var s string
			if err := schemaRows.Scan(&s); err == nil {
				out.Schemas = append(out.Schemas, s)
			}
		}
		schemaRows.Close()

		relRows, err := db.DB.QueryContext(ctx, `
SELECT n.nspname, c.relname, c.relkind,
       CASE WHEN c.relkind IN ('r','m','p') THEN c.reltuples::bigint ELSE NULL END
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r','v','m','p','f')
  AND n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg\_%' ESCAPE '\'
ORDER BY n.nspname, c.relname`)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "listing relations: "+err.Error())
			return
		}
		for relRows.Next() {
			var rel relation
			var kind string
			var est sql.NullInt64
			if err := relRows.Scan(&rel.Schema, &rel.Name, &kind, &est); err == nil {
				rel.Kind = relkindName(kind)
				if est.Valid {
					v := est.Int64
					rel.RowEstimate = &v
				}
				out.Relations = append(out.Relations, rel)
			}
		}
		relRows.Close()

		proRows, err := db.DB.QueryContext(ctx, `
SELECT n.nspname, p.proname, p.prokind, pg_get_function_identity_arguments(p.oid)
FROM pg_proc p
JOIN pg_namespace n ON n.oid = p.pronamespace
WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname NOT LIKE 'pg\_%' ESCAPE '\'
ORDER BY n.nspname, p.proname`)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "listing routines: "+err.Error())
			return
		}
		for proRows.Next() {
			var ro routine
			var kind string
			if err := proRows.Scan(&ro.Schema, &ro.Name, &kind, &ro.Args); err == nil {
				ro.Kind = prokindName(kind)
				out.Routines = append(out.Routines, ro)
			}
		}
		proRows.Close()

		writeJSON(w, http.StatusOK, out)
	}
}

type column struct {
	Name    string  `json:"name"`
	Type    string  `json:"type"`
	NotNull bool    `json:"not_null"`
	Default *string `json:"default"`
}

// columnsHandler returns a relation's columns. The schema/name come straight from
// the catalog listing; they're quoted as identifiers via format('%I.%I', …) and
// resolved with to_regclass (NULL, not an error, when absent), so there's no SQL
// injection surface even though they're path params.
func columnsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		schema := chi.URLParam(r, "schema")
		name := chi.URLParam(r, "name")
		rows, err := db.DB.QueryContext(r.Context(), `
SELECT a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull,
       pg_get_expr(ad.adbin, ad.adrelid)
FROM pg_attribute a
LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
WHERE a.attrelid = to_regclass(format('%I.%I', $1::text, $2::text))
  AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`, schema, name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "reading columns: "+err.Error())
			return
		}
		defer rows.Close()
		out := []column{}
		for rows.Next() {
			var c column
			var def sql.NullString
			if err := rows.Scan(&c.Name, &c.Type, &c.NotNull, &def); err == nil {
				if def.Valid {
					v := def.String
					c.Default = &v
				}
				out = append(out, c)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"columns": out})
	}
}

// routineHandler returns a function/procedure's full CREATE definition (the first
// overload when the name is overloaded).
func routineHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		schema := chi.URLParam(r, "schema")
		name := chi.URLParam(r, "name")
		var def string
		err := db.DB.QueryRowContext(r.Context(), `
SELECT pg_get_functiondef(p.oid)
FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
WHERE n.nspname = $1 AND p.proname = $2
ORDER BY p.oid LIMIT 1`, schema, name).Scan(&def)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "no such routine")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "reading routine: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"definition": def})
	}
}

// ---- the SQL runner ----

type queryRequest struct {
	SQL     string `json:"sql"`
	MaxRows int    `json:"max_rows"`
}

type queryResponse struct {
	Columns    []queryColumn `json:"columns"`
	Rows       [][]any       `json:"rows"`
	RowCount   int           `json:"row_count"`
	Truncated  bool          `json:"truncated"`
	Command    string        `json:"command"`
	DurationMs int64         `json:"duration_ms"`
}

type queryColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// returningFirstWord decides whether a statement yields a result set (so we Query)
// or not (so we Exec and report rows-affected). A conservative keyword sniff on the
// first token is enough for the admin use it serves.
func isReturningStatement(sqlText string) bool {
	t := strings.ToUpper(strings.TrimLeft(sqlText, " \t\r\n("))
	for _, kw := range []string{"SELECT", "WITH", "TABLE", "VALUES", "SHOW", "EXPLAIN"} {
		if strings.HasPrefix(t, kw) {
			return true
		}
	}
	return false
}

func queryHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req queryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		sqlText := strings.TrimSpace(req.SQL)
		if sqlText == "" {
			writeError(w, http.StatusBadRequest, "sql is required")
			return
		}
		maxRows := req.MaxRows
		if maxRows <= 0 {
			maxRows = defaultMaxRows
		}
		if maxRows > hardMaxRows {
			maxRows = hardMaxRows
		}

		actor := auth.ActorID(r.Context())
		started := time.Now()
		resp, httpErr, runErr := runQuery(r.Context(), db, sqlText, maxRows)
		dur := time.Since(started)

		// Audit every execution, success or failure.
		logSQL := sqlText
		if len(logSQL) > auditSQLMax {
			logSQL = logSQL[:auditSQLMax] + "…"
		}
		if runErr != nil {
			slog.Warn("db-explorer query failed",
				"component", "dbexplorer", "user", actor,
				"duration_ms", dur.Milliseconds(), "sql", logSQL, "err", runErr.Error())
			writeError(w, httpErr, runErr.Error())
			return
		}
		resp.DurationMs = dur.Milliseconds()
		slog.Info("db-explorer query",
			"component", "dbexplorer", "user", actor,
			"duration_ms", dur.Milliseconds(), "row_count", resp.RowCount,
			"truncated", resp.Truncated, "sql", logSQL)
		writeJSON(w, http.StatusOK, resp)
	}
}

// runQuery executes one statement inside a READ ONLY transaction with a statement
// timeout. The transaction is never committed (and read-only anyway), so the tool
// cannot change data: Postgres rejects any write with a clear error. Returns the
// response, the HTTP status to use on error, and the error (nil on success).
func runQuery(ctx context.Context, db *bun.DB, sqlText string, maxRows int) (*queryResponse, int, error) {
	ctx, cancel := context.WithTimeout(ctx, queryHardTimeout)
	defer cancel()

	// Use the raw *sql.DB (db.DB), not bun's wrapper: bun reformats the query through
	// its own placeholder processor, which would mangle a user's literal '?' (e.g. the
	// jsonb ? operator) and rebind positional params. The explorer runs SQL verbatim.
	tx, err := db.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, http.StatusInternalServerError, err
	}
	defer func() { _ = tx.Rollback() }() // read-only: nothing to keep

	if _, err := tx.ExecContext(ctx, "SET LOCAL statement_timeout = 15000"); err != nil {
		return nil, http.StatusInternalServerError, err
	}

	if !isReturningStatement(sqlText) {
		// A non-SELECT in a read-only transaction either does nothing meaningful
		// (SET/EXPLAIN-less) or is a write Postgres refuses — run it and report the
		// outcome; a write error surfaces as a 400 with the DB's own message.
		if _, err := tx.ExecContext(ctx, sqlText); err != nil {
			return nil, http.StatusBadRequest, err
		}
		return &queryResponse{
			Columns: []queryColumn{},
			Rows:    [][]any{},
			Command: firstWord(sqlText),
		}, 0, nil
	}

	rows, err := tx.QueryContext(ctx, sqlText)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	defer rows.Close()

	colNames, err := rows.Columns()
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	colTypes, _ := rows.ColumnTypes()
	cols := make([]queryColumn, len(colNames))
	for i, n := range colNames {
		c := queryColumn{Name: n}
		if colTypes != nil && i < len(colTypes) {
			c.Type = colTypes[i].DatabaseTypeName()
		}
		cols[i] = c
	}

	out := make([][]any, 0, maxRows)
	truncated := false
	for rows.Next() {
		if len(out) >= maxRows {
			truncated = true
			break
		}
		cells := make([]any, len(colNames))
		ptrs := make([]any, len(colNames))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		for i := range cells {
			cells[i] = normalize(cells[i])
		}
		out = append(out, cells)
	}
	if err := rows.Err(); err != nil {
		return nil, http.StatusBadRequest, err
	}

	return &queryResponse{
		Columns:   cols,
		Rows:      out,
		RowCount:  len(out),
		Truncated: truncated,
		Command:   firstWord(sqlText),
	}, 0, nil
}

// normalize converts a driver-scanned value into something JSON-friendly: []byte
// (text/json/numeric the driver returns raw) becomes a string, time becomes
// RFC3339; everything else passes through.
func normalize(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	case time.Time:
		return t.Format(time.RFC3339Nano)
	default:
		return v
	}
}

func firstWord(s string) string {
	s = strings.TrimLeft(s, " \t\r\n(")
	if i := strings.IndexAny(s, " \t\r\n("); i >= 0 {
		return strings.ToUpper(s[:i])
	}
	return strings.ToUpper(s)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
