package fieldpack

// The pack builder (Phase 21 Slice 1, docs/phase-21-field-tool.md).
//
// buildPack is the per-job work the cmd/fieldsync worker runs against a claimed
// ops.field_packs row -- the field-tool analog of export.runClaimedJob. A build
// produces TWO artifacts for a bbox + class set, both under ONE read snapshot:
//
//   * a SQLite "data pack" (the on-device edit truth): a features table of
//     full-precision WKB geometry + attributes, the connectivity/relationship
//     graph, a per-class metadata table, and a manifest -- all indexed
//     server-side so the device opens it queryable on the first frame; and
//   * a content-pruned MBTiles vector tileset (tippecanoe) for fast offline
//     display, fed from per-class newline-delimited GeoJSON written in the same
//     read pass.
//
// The whole feature + graph read runs inside one REPEATABLE READ, read-only
// transaction (like export.runExport): a multi-class pack can take a while, and a
// concurrent edit landing mid-read would otherwise tear the pack -- one class seen
// before an edit, another after. snapshot_at is the transaction's own now(), so a
// future delta refresh (Slice 6) can ask "what changed since this?".
//
// The SQLite file is written with modernc.org/sqlite (pure Go, no cgo -- so
// cmd/fieldsync cross-compiles like every other VoltEdge binary). tippecanoe is
// an external CLI, shelled out to exactly as the design intends; TIPPECANOE_BIN
// overrides its path.

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

const (
	// packSchemaVersion is the on-device pack schema version, recorded in the
	// manifest. Bump it when the SQLite table shapes change so the app can refuse
	// (or migrate) a pack it doesn't understand.
	packSchemaVersion = 1

	defaultMinZoom = 6
	defaultMaxZoom = 16
)

// buildResult carries the finished local artifacts + counts back to buildPack.
type buildResult struct {
	tilesPath    string
	dataPath     string
	featureCount int
	connCount    int
	relCount     int
	snapshotAt   time.Time
}

// buildPack builds one claimed pack's two artifacts, uploads them to RustFS, and
// marks the row succeeded/failed -- the field-tool analog of runClaimedJob. The
// status = 'running' guards in completePack/failPack mean a pack cancelled out
// from under this worker (its heartbeat cancels ctx) is never resurrected.
func buildPack(ctx context.Context, db *bun.DB, store *export.ObjectStore, pack *FieldPack) {
	var scope PackScope
	if err := json.Unmarshal(pack.Scope, &scope); err != nil {
		markPackFailed(ctx, db, pack.ID, fmt.Sprintf("corrupt pack scope: %v", err))
		return
	}

	// A pack's artifacts stream via local temp files (unlike an export, which
	// pipes straight to RustFS): tippecanoe needs seekable input files and writes
	// a seekable .mbtiles, and SQLite needs a real file to build + index. The dir
	// is reaped on return regardless of outcome.
	tmpDir, err := os.MkdirTemp("", "fieldpack-"+pack.ID+"-")
	if err != nil {
		markPackFailed(ctx, db, pack.ID, fmt.Sprintf("creating work dir: %v", err))
		return
	}
	defer os.RemoveAll(tmpDir)

	res, err := buildArtifacts(ctx, db, scope, tmpDir, pack.ID, pack.RequestedBy)
	if err != nil {
		markPackFailed(ctx, db, pack.ID, fmt.Sprintf("building pack: %v", err))
		return
	}

	tilesKey := packObjectKey(pack.ID, "tiles.mbtiles")
	dataKey := packObjectKey(pack.ID, "data.sqlite")

	tilesBytes, err := uploadFile(ctx, store, tilesKey, res.tilesPath)
	if err != nil {
		markPackFailed(ctx, db, pack.ID, fmt.Sprintf("uploading tiles: %v", err))
		return
	}
	dataBytes, err := uploadFile(ctx, store, dataKey, res.dataPath)
	if err != nil {
		markPackFailed(ctx, db, pack.ID, fmt.Sprintf("uploading data pack: %v", err))
		return
	}

	if err := completePack(ctx, db, pack.ID, tilesKey, dataKey, tilesBytes, dataBytes, res.featureCount, res.snapshotAt); err != nil {
		// Both objects are uploaded but the row didn't flip (e.g. cancelled, or a
		// transient DB fault): the objects are now unreferenced. A retention sweep
		// (a later slice, like the export queue's) reaps orphans; nothing to serve.
		log.Printf("fieldpack: pack %s uploaded but marking complete failed: %v", pack.ID, err)
		return
	}
	log.Printf("fieldpack: pack %s built: features=%d conns=%d rels=%d tiles=%dB data=%dB",
		pack.ID, res.featureCount, res.connCount, res.relCount, tilesBytes, dataBytes)
}

// buildArtifacts does the read + encode: it writes the SQLite pack and the
// per-class GeoJSON under one snapshot, then runs tippecanoe. It returns the two
// local file paths + counts; the caller uploads them.
func buildArtifacts(ctx context.Context, db *bun.DB, scope PackScope, tmpDir, packID, requestedBy string) (buildResult, error) {
	bbox, err := scopeBBox(scope.Bbox)
	if err != nil {
		return buildResult{}, err
	}
	if len(scope.FeatureClasses) == 0 {
		return buildResult{}, fmt.Errorf("no feature classes selected for the pack")
	}

	dataPath := filepath.Join(tmpDir, "data.sqlite")
	tilesPath := filepath.Join(tmpDir, "tiles.mbtiles")

	sq, err := sql.Open("sqlite", dataPath)
	if err != nil {
		return buildResult{}, fmt.Errorf("opening pack file: %w", err)
	}
	// One connection so the tx + PRAGMAs below apply to every write.
	sq.SetMaxOpenConns(1)
	defer sq.Close()
	if err := initPackSchema(ctx, sq); err != nil {
		return buildResult{}, err
	}

	// One newline-delimited GeoJSON file per class, written in the read pass and
	// handed to tippecanoe as a named layer. Closed (flushed) before tippecanoe.
	layers := map[string]*layerWriter{}
	defer closeLayers(layers)

	stx, err := sq.BeginTx(ctx, nil)
	if err != nil {
		return buildResult{}, fmt.Errorf("beginning pack tx: %w", err)
	}
	featStmt, err := stx.PrepareContext(ctx, `INSERT INTO features
		(id, feature_class, name, status, subtype, territory_id, geom, attrs)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		stx.Rollback()
		return buildResult{}, err
	}
	defer featStmt.Close()
	metaStmt, err := stx.PrepareContext(ctx, `INSERT INTO metadata
		(code, name, geometry_type, srid, network_role, fields, subtypes, terminals)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		stx.Rollback()
		return buildResult{}, err
	}
	defer metaStmt.Close()
	connStmt, err := stx.PrepareContext(ctx, `INSERT INTO connectivity
		(id, from_class, from_id, from_terminal, to_class, to_id, to_terminal)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		stx.Rollback()
		return buildResult{}, err
	}
	defer connStmt.Close()
	relStmt, err := stx.PrepareContext(ctx, `INSERT INTO relationships
		(id, relationship_class_id, origin_class, origin_id, destination_class, destination_id)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		stx.Rollback()
		return buildResult{}, err
	}
	defer relStmt.Close()

	packed := map[string]struct{}{}
	res := buildResult{tilesPath: tilesPath, dataPath: dataPath}

	txErr := db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true},
		func(ctx context.Context, tx bun.Tx) error {
			// The snapshot instant this pack reflects: under REPEATABLE READ, now()
			// is the transaction's start time -- the exact point-in-time a later
			// delta refresh compares against.
			if err := tx.NewRaw("SELECT now()").Scan(ctx, &res.snapshotAt); err != nil {
				return fmt.Errorf("reading snapshot time: %w", err)
			}

			for _, code := range scope.FeatureClasses {
				loaded, err := metadata.Load(ctx, tx, code)
				if err != nil {
					if errors.Is(err, sql.ErrNoRows) {
						return fmt.Errorf("feature class %q does not exist", code)
					}
					return err
				}
				if loaded.FeatureClass.GeometryType == "none" {
					return fmt.Errorf("feature class %q has no geometry, so it can't be packed", code)
				}

				lw, err := layerWriterFor(layers, tmpDir, code)
				if err != nil {
					return err
				}

				_, err = readClassFeatures(ctx, tx, loaded, bbox, scope.TerritoryIDs, func(row packRow) error {
					attrs, err := json.Marshal(row.Attributes)
					if err != nil {
						return fmt.Errorf("encoding attributes for %s/%s: %w", code, row.ID, err)
					}
					if _, err := featStmt.ExecContext(ctx, row.ID, code, row.Name, row.Status, row.Subtype, row.TerritoryID, row.WKB, string(attrs)); err != nil {
						return fmt.Errorf("writing feature %s/%s: %w", code, row.ID, err)
					}
					if err := lw.writeFeature(row); err != nil {
						return err
					}
					packed[row.ID] = struct{}{}
					res.featureCount++
					return nil
				})
				if err != nil {
					return err
				}

				if err := insertClassMetadata(ctx, metaStmt, loaded); err != nil {
					return fmt.Errorf("writing metadata for %q: %w", code, err)
				}
			}

			// The graph tables are included only when the requester's RBAC allowed
			// it (frozen into the scope at enqueue). Both endpoints must be inside the
			// packed feature set -- a dangling edge to a feature outside the bbox is
			// useless to an offline trace and is dropped.
			if scope.IncludeConnectivity {
				res.connCount, err = loadConnectivity(ctx, tx, packed, connStmt)
				if err != nil {
					return fmt.Errorf("loading connectivity: %w", err)
				}
			}
			if scope.IncludeRelationships {
				res.relCount, err = loadRelationships(ctx, tx, packed, relStmt)
				if err != nil {
					return fmt.Errorf("loading relationships: %w", err)
				}
			}
			return nil
		})
	if txErr != nil {
		stx.Rollback()
		return buildResult{}, txErr
	}

	if err := writeManifest(ctx, stx, packID, requestedBy, scope, res); err != nil {
		stx.Rollback()
		return buildResult{}, err
	}
	if err := stx.Commit(); err != nil {
		return buildResult{}, fmt.Errorf("committing pack: %w", err)
	}

	// Indexes are built AFTER the bulk insert (much faster than maintaining them
	// per row) and shipped inside the file, so the device queries with no import.
	if err := createPackIndexes(ctx, sq); err != nil {
		return buildResult{}, err
	}

	if err := closeLayers(layers); err != nil {
		return buildResult{}, err
	}

	if res.featureCount == 0 {
		return buildResult{}, fmt.Errorf("no features found in the requested scope")
	}

	if err := runTippecanoe(ctx, tilesPath, layers); err != nil {
		return buildResult{}, err
	}

	return res, nil
}

// initPackSchema creates the on-device tables and sets build-time PRAGMAs. The
// file is a throwaway build artifact, so durability PRAGMAs are relaxed for speed;
// the device opens it read-mostly regardless. Statements are executed one at a
// time rather than as one multi-statement string, since a database/sql driver's
// Exec is only guaranteed to run the first statement of a batch.
func initPackSchema(ctx context.Context, sq *sql.DB) error {
	stmts := []string{
		`PRAGMA journal_mode = MEMORY`,
		`PRAGMA synchronous = OFF`,
		`CREATE TABLE features (
			id            TEXT PRIMARY KEY,
			feature_class TEXT NOT NULL,
			name          TEXT,
			status        TEXT,
			subtype       INTEGER,
			territory_id  TEXT,
			geom          BLOB,
			attrs         TEXT
		)`,
		`CREATE TABLE connectivity (
			id            TEXT PRIMARY KEY,
			from_class    TEXT, from_id TEXT, from_terminal TEXT,
			to_class      TEXT, to_id   TEXT, to_terminal   TEXT
		)`,
		`CREATE TABLE relationships (
			id                    TEXT PRIMARY KEY,
			relationship_class_id TEXT,
			origin_class          TEXT, origin_id      TEXT,
			destination_class     TEXT, destination_id TEXT
		)`,
		`CREATE TABLE metadata (
			code          TEXT PRIMARY KEY,
			name          TEXT,
			geometry_type TEXT,
			srid          INTEGER,
			network_role  TEXT,
			fields        TEXT,
			subtypes      TEXT,
			terminals     TEXT
		)`,
		`CREATE TABLE manifest (
			key   TEXT PRIMARY KEY,
			value TEXT
		)`,
	}
	for _, s := range stmts {
		if _, err := sq.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("initializing pack schema: %w", err)
		}
	}
	return nil
}

// createPackIndexes builds the by-id / by-class / by-endpoint indexes the device
// relies on for <1ms lookups, after the bulk load.
func createPackIndexes(ctx context.Context, sq *sql.DB) error {
	stmts := []string{
		`CREATE INDEX features_class_idx       ON features (feature_class)`,
		`CREATE INDEX connectivity_from_idx    ON connectivity (from_id)`,
		`CREATE INDEX connectivity_to_idx      ON connectivity (to_id)`,
		`CREATE INDEX relationships_origin_idx ON relationships (origin_id)`,
		`CREATE INDEX relationships_dest_idx   ON relationships (destination_id)`,
	}
	for _, s := range stmts {
		if _, err := sq.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("building pack indexes: %w", err)
		}
	}
	return nil
}

// insertClassMetadata writes one class's metadata row -- its shape + the JSON
// blobs (fields/subtypes/terminals) the device reconstructs the class from.
func insertClassMetadata(ctx context.Context, stmt *sql.Stmt, loaded *metadata.Loaded) error {
	fields, err := json.Marshal(loaded.Fields)
	if err != nil {
		return err
	}
	subtypes, err := json.Marshal(loaded.Subtypes)
	if err != nil {
		return err
	}
	terminals, err := json.Marshal(loaded.Terminals)
	if err != nil {
		return err
	}
	srid := 4326
	if loaded.FeatureClass.SRID != nil {
		srid = *loaded.FeatureClass.SRID
	}
	_, err = stmt.ExecContext(ctx,
		loaded.FeatureClass.Code, loaded.FeatureClass.Name, loaded.FeatureClass.GeometryType,
		srid, loaded.FeatureClass.NetworkRole, string(fields), string(subtypes), string(terminals))
	return err
}

// loadConnectivity streams every established connection, resolves each endpoint's
// class code, and writes the ones whose BOTH endpoints are inside the packed
// feature set. Streaming + in-Go membership filter keeps memory flat and avoids a
// giant IN (...) over the packed id set; a bbox-scoped edge query is a later
// optimization (docs/phase-21-field-tool.md, open items).
func loadConnectivity(ctx context.Context, db bun.IDB, packed map[string]struct{}, stmt *sql.Stmt) (int, error) {
	const q = `
SELECT c.id::text, ffrom.code, c.from_feature_id::text, c.from_terminal_id::text,
       fto.code, c.to_feature_id::text, c.to_terminal_id::text
FROM runtime.feature_connectivity c
JOIN metadata.feature_classes ffrom ON ffrom.id = c.from_feature_class_id
JOIN metadata.feature_classes fto   ON fto.id   = c.to_feature_class_id`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	n := 0
	for rows.Next() {
		var id, fromClass, fromID, toClass, toID string
		var fromTerm, toTerm sql.NullString
		if err := rows.Scan(&id, &fromClass, &fromID, &fromTerm, &toClass, &toID, &toTerm); err != nil {
			return n, err
		}
		if _, ok := packed[fromID]; !ok {
			continue
		}
		if _, ok := packed[toID]; !ok {
			continue
		}
		if _, err := stmt.ExecContext(ctx, id, fromClass, fromID, fromTerm, toClass, toID, toTerm); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// loadRelationships is loadConnectivity's counterpart for
// runtime.feature_relationships (origin/destination instead of from/to; no
// terminals; carries the relationship_class_id).
func loadRelationships(ctx context.Context, db bun.IDB, packed map[string]struct{}, stmt *sql.Stmt) (int, error) {
	const q = `
SELECT r.id::text, r.relationship_class_id::text,
       ro.code, r.origin_feature_id::text,
       rd.code, r.destination_feature_id::text
FROM runtime.feature_relationships r
JOIN metadata.feature_classes ro ON ro.id = r.origin_feature_class_id
JOIN metadata.feature_classes rd ON rd.id = r.destination_feature_class_id`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	n := 0
	for rows.Next() {
		var id, relClass, originClass, originID, destClass, destID string
		if err := rows.Scan(&id, &relClass, &originClass, &originID, &destClass, &destID); err != nil {
			return n, err
		}
		if _, ok := packed[originID]; !ok {
			continue
		}
		if _, ok := packed[destID]; !ok {
			continue
		}
		if _, err := stmt.ExecContext(ctx, id, relClass, originClass, originID, destClass, destID); err != nil {
			return n, err
		}
		n++
	}
	return n, rows.Err()
}

// writeManifest records the single-row build facts the device reads on open: the
// schema version, snapshot instant, scope, and counts.
func writeManifest(ctx context.Context, stx *sql.Tx, packID, requestedBy string, scope PackScope, res buildResult) error {
	bboxJSON, err := json.Marshal(scope.Bbox)
	if err != nil {
		return err
	}
	classesJSON, err := json.Marshal(scope.FeatureClasses)
	if err != nil {
		return err
	}
	entries := [][2]string{
		{"schema_version", strconv.Itoa(packSchemaVersion)},
		{"pack_id", packID},
		{"generated_by", requestedBy},
		{"snapshot_at", res.snapshotAt.UTC().Format(time.RFC3339Nano)},
		{"bbox", string(bboxJSON)},
		{"feature_classes", string(classesJSON)},
		{"feature_count", strconv.Itoa(res.featureCount)},
		{"connectivity_count", strconv.Itoa(res.connCount)},
		{"relationship_count", strconv.Itoa(res.relCount)},
		{"include_connectivity", strconv.FormatBool(scope.IncludeConnectivity)},
		{"include_relationships", strconv.FormatBool(scope.IncludeRelationships)},
	}
	stmt, err := stx.PrepareContext(ctx, `INSERT INTO manifest (key, value) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err := stmt.ExecContext(ctx, e[0], e[1]); err != nil {
			return err
		}
	}
	return nil
}

// layerWriter buffers one class's newline-delimited GeoJSON features for
// tippecanoe. count lets the caller skip an empty layer (tippecanoe rejects one).
type layerWriter struct {
	code   string
	path   string
	file   *os.File
	buf    *bufio.Writer
	count  int
	closed bool
}

// layerWriterFor lazily creates the writer for a class code.
func layerWriterFor(layers map[string]*layerWriter, tmpDir, code string) (*layerWriter, error) {
	if lw, ok := layers[code]; ok {
		return lw, nil
	}
	path := filepath.Join(tmpDir, "layer-"+code+".geojsonl")
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("creating tile input for %q: %w", code, err)
	}
	lw := &layerWriter{code: code, path: path, file: f, buf: bufio.NewWriterSize(f, 64*1024)}
	layers[code] = lw
	return lw, nil
}

// tileFeature is one GeoJSON Feature line. Geometry is embedded verbatim (already
// a JSON object from ST_AsGeoJSON), and Properties are pruned to only what tiles
// draw/label + the id (so a tap resolves tile -> id -> SQLite record).
type tileFeature struct {
	Type       string          `json:"type"`
	Geometry   json.RawMessage `json:"geometry"`
	Properties map[string]any  `json:"properties"`
}

func (lw *layerWriter) writeFeature(row packRow) error {
	props := map[string]any{"id": row.ID}
	if row.Name != nil {
		props["name"] = *row.Name
	}
	if row.Status != nil {
		props["status"] = *row.Status
	}
	if row.Subtype != nil {
		props["subtype"] = *row.Subtype
	}
	b, err := json.Marshal(tileFeature{Type: "Feature", Geometry: json.RawMessage(row.GeoJSON), Properties: props})
	if err != nil {
		return fmt.Errorf("encoding tile feature %s/%s: %w", lw.code, row.ID, err)
	}
	if _, err := lw.buf.Write(b); err != nil {
		return err
	}
	if err := lw.buf.WriteByte('\n'); err != nil {
		return err
	}
	lw.count++
	return nil
}

// closeLayers flushes + closes every layer writer. Idempotent (safe as both an
// explicit pre-tippecanoe flush and a defer).
func closeLayers(layers map[string]*layerWriter) error {
	var firstErr error
	for _, lw := range layers {
		if lw.closed {
			continue
		}
		lw.closed = true
		if err := lw.buf.Flush(); err != nil && firstErr == nil {
			firstErr = err
		}
		if err := lw.file.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// runTippecanoe builds the MBTiles from the per-class GeoJSON layers. Each class
// becomes its own named vector-tile layer (so a MapLibre style targets it by
// name), and --drop-densest-as-needed keeps dense areas within tile size limits.
func runTippecanoe(ctx context.Context, out string, layers map[string]*layerWriter) error {
	bin := os.Getenv("TIPPECANOE_BIN")
	if bin == "" {
		bin = "tippecanoe"
	}
	minZ, maxZ := zoomRange()

	args := []string{"-o", out, "--force", "--drop-densest-as-needed",
		"-Z", strconv.Itoa(minZ), "-z", strconv.Itoa(maxZ)}
	added := 0
	for code, lw := range layers {
		if lw.count == 0 {
			continue
		}
		// -L name:file makes tippecanoe read file as a layer called name.
		args = append(args, "-L", code+":"+lw.path)
		added++
	}
	if added == 0 {
		return fmt.Errorf("no non-empty layers to tile")
	}

	cmd := exec.CommandContext(ctx, bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if len(msg) > 2000 {
			msg = msg[:2000]
		}
		return fmt.Errorf("tippecanoe (%s) failed: %w: %s", bin, err, msg)
	}
	return nil
}

// zoomRange reads FIELDPACK_MIN_ZOOM / FIELDPACK_MAX_ZOOM, falling back to sane
// defaults. min is clamped below max so a misconfiguration can't invert the range.
func zoomRange() (int, int) {
	minZ, maxZ := defaultMinZoom, defaultMaxZoom
	if v := os.Getenv("FIELDPACK_MIN_ZOOM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 24 {
			minZ = n
		}
	}
	if v := os.Getenv("FIELDPACK_MAX_ZOOM"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 24 {
			maxZ = n
		}
	}
	if minZ > maxZ {
		minZ = maxZ
	}
	return minZ, maxZ
}

// scopeBBox validates the pack's bounding box. A field pack is always
// bbox-bounded (there is no whole-dataset pack), so an absent or malformed bbox
// is an error, not a "no spatial filter". Swapped min/max are normalized.
func scopeBBox(b []float64) (*crud.ExportBBox, error) {
	if len(b) != 4 {
		return nil, fmt.Errorf("pack bbox must be [minX, minY, maxX, maxY] (4 numbers), got %d", len(b))
	}
	for _, v := range b {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("pack bbox contains a non-finite number")
		}
	}
	return &crud.ExportBBox{
		MinX: math.Min(b[0], b[2]),
		MinY: math.Min(b[1], b[3]),
		MaxX: math.Max(b[0], b[2]),
		MaxY: math.Max(b[1], b[3]),
	}, nil
}

// packObjectKey is the RustFS key for one of a pack's artifacts, namespaced by
// pack id so keys never collide.
func packObjectKey(packID, name string) string {
	return "packs/" + packID + "/" + name
}

// uploadFile streams a local artifact into RustFS under key and returns its size.
func uploadFile(ctx context.Context, store *export.ObjectStore, key, path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	return store.Upload(ctx, key, f, "application/octet-stream")
}

// markPackFailed records a build failure, logging if even that write fails.
func markPackFailed(ctx context.Context, db *bun.DB, id, msg string) {
	if err := failPack(ctx, db, id, msg); err != nil {
		log.Printf("fieldpack: pack %s failed (%s) and marking failed also failed: %v", id, msg, err)
	}
}
