package importer

// GeoPackage (.gpkg) source support -- the first v2 import format and the first
// that DELIBERATELY breaks v1's "the browser normalizes everything to NDJSON,
// the server only ever reads NDJSON" invariant.
//
// A GeoPackage is an OGC-standard SQLite database. The browser can't parse it,
// so it uploads the RAW file to object storage and the server opens it here with
// the pure-Go SQLite driver (modernc.org/sqlite, already in go.mod -- no cgo, no
// new dependency). We read the chosen feature table row by row and yield the
// SAME *Feature shape the NDJSON Reader does, so the entire downstream apply
// spine (BuildAttributes -> AssembleGeometry -> crud validate -> upsert) is
// reused unchanged. The one GeoPackage-specific step -- turning a GeoPackage
// geometry blob into GeoJSON -- is deferred to AssembleGeometry (native mode):
// the reader just hangs the raw blob on the Feature, so a malformed geometry is
// a per-row error there, not a scan-aborting failure here.
//
// Two callers use this file: the load worker (load.go, streaming every row) and
// the HTTP handlers (http.go: layer inspection and the server-side dry-run
// sample). Both need the whole file locally because SQLite is random-access, so
// we download the object to a temp file first.

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	// Registers the pure-Go "sqlite" driver with database/sql. Blank import: we
	// only ever open via sql.Open("sqlite", ...).
	_ "modernc.org/sqlite"

	"github.com/kwaabs/voltedge/api/internal/export"

	"github.com/uptrace/bun"
)

// gpkgDryRunSample caps how many rows the server reads from a GeoPackage for the
// synchronous pre-flight dry-run (the browser can't sample a file it can't
// parse). It is a preview, not the exhaustive check -- the "validate only" full
// run streams every row. Kept modest so the dry-run stays snappy.
const gpkgDryRunSample = 100

// geomKind* discriminate which native blob format a SQLite-family row carries, so
// AssembleGeometry (via decodeNativeGeom) picks the right decoder. GeoPackage and
// SpatiaLite are both SQLite databases but wrap their geometry in different blob
// containers (GPB vs the SpatiaLite BLOB).
const (
	geomKindGPKG       = "gpkg"
	geomKindSpatiaLite = "spatialite"
	// geomKindWKB is a passthrough kind for a source that already hands the reader
	// standard WKB (a live database via STAsBinary()/ST_AsBinary), so decodeNativeGeom
	// is a no-op and the geometry goes straight to wkbToGeoJSON.
	geomKindWKB = "wkb"
)

// GpkgLayer describes one feature table (layer) in a GeoPackage: its table name,
// its geometry column, that column's declared SRID, and its geometry type. The
// run wizard lists these so an operator can pin/override which layer to import.
// FeatureCount is populated only by the inspection path (listGeoPackageLayers
// leaves it zero); the load doesn't need a count.
type GpkgLayer struct {
	Name           string `json:"name"`
	GeometryColumn string `json:"geometry_column"`
	GeometryType   string `json:"geometry_type"`
	SRID           int    `json:"srid"`
	FeatureCount   int    `json:"feature_count"`
}

// sqliteReader streams features from one feature table of an opened SQLite-family
// database (GeoPackage or SpatiaLite), yielding the same *Feature shape the NDJSON
// Reader does. Non-geometry columns become Properties; the geometry column's blob
// is hung raw on the Feature (rawGeom + geomKind) for AssembleGeometry to decode.
// It implements the rowSource interface the load loop consumes.
type sqliteReader struct {
	rows      *sql.Rows
	colNames  []string
	geomIdx   int    // index of the geometry column in colNames, -1 if none
	geomKind  string // geomKindGPKG / geomKindSpatiaLite -- how to decode rawGeom
	layerSRID int
	row       int
}

// Read returns the next feature and its 1-based row number, or io.EOF at the end
// of the table. A scan failure is fatal (the row iteration is broken), mirroring
// how the NDJSON reader treats a stream decode error; a per-row GEOMETRY problem
// is not raised here -- the blob rides on the Feature and fails (if at all) in
// AssembleGeometry, so one bad geometry doesn't abort the whole import.
func (r *sqliteReader) Read() (*Feature, int, error) {
	if !r.rows.Next() {
		if err := r.rows.Err(); err != nil {
			return nil, r.row, fmt.Errorf("reading GeoPackage row: %w", err)
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
		return nil, r.row, fmt.Errorf("scanning GeoPackage row %d: %w", r.row, err)
	}

	f := &Feature{Type: "Feature", Properties: make(map[string]any, len(r.colNames))}
	for i, name := range r.colNames {
		if i == r.geomIdx {
			if blob, ok := vals[i].([]byte); ok && len(blob) > 0 {
				// Copy: the driver may reuse the backing array on the next Next().
				f.rawGeom = append([]byte(nil), blob...)
				f.rawGeomKind = r.geomKind
				f.rawSRID = r.layerSRID
			}
			continue
		}
		f.Properties[name] = normalizeSQLiteValue(vals[i])
	}
	return f, r.row, nil
}

// normalizeSQLiteValue maps a raw SQLite value into the same Go shapes the NDJSON
// path produces, so type coercion (mapping.go) behaves identically regardless of
// source: TEXT/BLOB -> string, INTEGER -> float64 (matching JSON's number
// convention, so a whole-number cell coerces like a GeoJSON number everywhere),
// REAL stays float64, NULL stays nil.
func normalizeSQLiteValue(v any) any {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case int64:
		return float64(t)
	default:
		return v
	}
}

// geomKindForFormat maps a source_format to the native geometry-blob kind its
// rows carry, so the reader can tag each Feature for the right decoder.
func geomKindForFormat(format string) string {
	if format == "spatialite" {
		return geomKindSpatiaLite
	}
	return geomKindGPKG
}

// listSQLiteLayers returns a SQLite-family file's feature tables, dispatching to
// the format's own metadata catalog (GeoPackage's gpkg_* tables vs SpatiaLite's
// geometry_columns).
func listSQLiteLayers(ctx context.Context, sdb *sql.DB, format string) ([]GpkgLayer, error) {
	if format == "spatialite" {
		return listSpatiaLiteLayers(ctx, sdb)
	}
	return listGeoPackageLayers(ctx, sdb)
}

// openSQLiteSource downloads the uploaded SQLite-family file (GeoPackage or
// SpatiaLite) to a temp file, opens it, resolves the layer to import (the run-time
// override or profile default; if blank and the file has exactly one feature
// layer, that one), and returns a streaming reader over it. The returned cleanup
// closes the rows + database and removes the temp file; the caller must always
// defer it.
func openSQLiteSource(ctx context.Context, store *export.ObjectStore, objectKey, layerName, format string) (*sqliteReader, func(), error) {
	path, rm, err := downloadToTemp(ctx, store, objectKey)
	if err != nil {
		return nil, nil, err
	}

	sdb, err := openSQLite(path)
	if err != nil {
		rm()
		return nil, nil, err
	}

	layers, err := listSQLiteLayers(ctx, sdb, format)
	if err != nil {
		_ = sdb.Close()
		rm()
		return nil, nil, err
	}
	layer, err := pickLayer(layers, layerName)
	if err != nil {
		_ = sdb.Close()
		rm()
		return nil, nil, err
	}

	rows, err := sdb.QueryContext(ctx, "SELECT * FROM "+quoteIdent(layer.Name))
	if err != nil {
		_ = sdb.Close()
		rm()
		return nil, nil, fmt.Errorf("querying layer %q: %w", layer.Name, err)
	}
	cols, err := rows.Columns()
	if err != nil {
		_ = rows.Close()
		_ = sdb.Close()
		rm()
		return nil, nil, fmt.Errorf("reading columns of layer %q: %w", layer.Name, err)
	}

	geomIdx := -1
	for i, c := range cols {
		if strings.EqualFold(c, layer.GeometryColumn) {
			geomIdx = i
			break
		}
	}

	cleanup := func() {
		_ = rows.Close()
		_ = sdb.Close()
		rm()
	}
	return &sqliteReader{
		rows:      rows,
		colNames:  cols,
		geomIdx:   geomIdx,
		geomKind:  geomKindForFormat(format),
		layerSRID: layer.SRID,
	}, cleanup, nil
}

// pickLayer chooses which feature table to import. An explicit name (the run-time
// override or profile default) must match one of the file's layers; an empty name
// is allowed only when the file has exactly one feature layer (the single-layer
// case), otherwise the operator must choose.
func pickLayer(layers []GpkgLayer, name string) (GpkgLayer, error) {
	if len(layers) == 0 {
		return GpkgLayer{}, invalidf("this source has no feature layers to import")
	}
	if strings.TrimSpace(name) == "" {
		if len(layers) == 1 {
			return layers[0], nil
		}
		return GpkgLayer{}, invalidf("this source has %d feature layers; choose which one to import", len(layers))
	}
	for _, l := range layers {
		if strings.EqualFold(l.Name, name) {
			return l, nil
		}
	}
	available := make([]string, len(layers))
	for i, l := range layers {
		available[i] = l.Name
	}
	return GpkgLayer{}, invalidf("layer %q is not in this source (available: %s)", name, strings.Join(available, ", "))
}

// listGeoPackageLayers returns the file's feature tables from the standard
// gpkg_contents + gpkg_geometry_columns metadata. Only data_type = 'features'
// rows are layers we can import (a GeoPackage may also hold 'attributes' or
// 'tiles'). FeatureCount is left zero here; callers that want it (inspection)
// call countLayerFeatures.
func listGeoPackageLayers(ctx context.Context, sdb *sql.DB) ([]GpkgLayer, error) {
	rows, err := sdb.QueryContext(ctx, `
SELECT c.table_name, g.column_name, COALESCE(g.geometry_type_name, ''), g.srs_id
FROM gpkg_contents c
JOIN gpkg_geometry_columns g ON g.table_name = c.table_name
WHERE c.data_type = 'features'
ORDER BY c.table_name`)
	if err != nil {
		return nil, invalidf("could not read GeoPackage layer list -- is this a valid .gpkg file? (%v)", err)
	}
	defer rows.Close()

	var out []GpkgLayer
	for rows.Next() {
		var l GpkgLayer
		if err := rows.Scan(&l.Name, &l.GeometryColumn, &l.GeometryType, &l.SRID); err != nil {
			return nil, fmt.Errorf("scanning GeoPackage layer metadata: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// countLayerFeatures counts rows in one feature table (used by the inspection
// endpoint so an operator can confirm the layer). The table name comes from the
// file's own gpkg_contents metadata; it is still quoted defensively.
func countLayerFeatures(ctx context.Context, sdb *sql.DB, table string) (int, error) {
	var n int
	if err := sdb.QueryRowContext(ctx, "SELECT count(*) FROM "+quoteIdent(table)).Scan(&n); err != nil {
		return 0, fmt.Errorf("counting features in layer %q: %w", table, err)
	}
	return n, nil
}

// inspectSQLiteLayers downloads and opens an uploaded SQLite-family file and
// returns its feature layers with counts -- what the run wizard shows to
// pick/override the source layer.
func inspectSQLiteLayers(ctx context.Context, store *export.ObjectStore, objectKey, format string) ([]GpkgLayer, error) {
	path, rm, err := downloadToTemp(ctx, store, objectKey)
	if err != nil {
		return nil, err
	}
	defer rm()

	sdb, err := openSQLite(path)
	if err != nil {
		return nil, err
	}
	defer sdb.Close()

	layers, err := listSQLiteLayers(ctx, sdb, format)
	if err != nil {
		return nil, err
	}
	for i := range layers {
		n, err := countLayerFeatures(ctx, sdb, layers[i].Name)
		if err != nil {
			return nil, err
		}
		layers[i].FeatureCount = n
	}
	return layers, nil
}

// sampleSQLite reads up to n features from one layer for the server-side dry-run
// preview. Geometry is left as the raw blob on each Feature (decoded later by
// AssembleGeometry inside DryRun), so this touches only SQLite.
func sampleSQLite(ctx context.Context, store *export.ObjectStore, objectKey, layerName string, n int, format string) ([]*Feature, error) {
	src, cleanup, err := openSQLiteSource(ctx, store, objectKey, layerName, format)
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

// wkbToGeoJSON converts a WKB geometry to GeoJSON in the database, reprojecting
// from its source SRID to the target class's SRID when they differ. It mirrors
// wktToGeoJSON's PostGIS round-trip (the accepted, documented per-row cost) and
// runs outside any transaction, so a bad geometry is a per-row problem, not a
// batch-poisoning error. A source SRID <= 0 (undefined) is taken to already be
// in the target SRID and merely stamped, not transformed. When flipXY is set the
// coordinates are swapped (ST_FlipCoordinates) before any transform -- needed for
// SQL Server geography WKB, which arrives in latitude-longitude order.
func wkbToGeoJSON(ctx context.Context, db bun.IDB, wkb []byte, srcSRID, dstSRID int, flipXY bool) (json.RawMessage, error) {
	var gj string
	q := db.NewSelect()
	if srcSRID > 0 && srcSRID != dstSRID {
		inner := "ST_GeomFromWKB(?, ?)"
		if flipXY {
			inner = "ST_FlipCoordinates(" + inner + ")"
		}
		q = q.ColumnExpr("ST_AsGeoJSON(ST_Transform("+inner+", ?))", wkb, srcSRID, dstSRID)
	} else {
		inner := "ST_SetSRID(ST_GeomFromWKB(?), ?)"
		if flipXY {
			inner = "ST_FlipCoordinates(" + inner + ")"
		}
		q = q.ColumnExpr("ST_AsGeoJSON("+inner+")", wkb, dstSRID)
	}
	if err := q.Scan(ctx, &gj); err != nil {
		return nil, fmt.Errorf("invalid WKB geometry: %w", err)
	}
	return json.RawMessage(gj), nil
}

// decodeNativeGeom decodes a SQLite-family native geometry blob into standard WKB,
// dispatching on the row's geometry kind (which reader produced it). It normalizes
// the declared SRID to an int and returns whether the geometry is empty, so
// AssembleGeometry stays decoder-agnostic.
func decodeNativeGeom(kind string, blob []byte) (wkb []byte, srid int, empty bool, err error) {
	switch kind {
	case geomKindWKB:
		// Already standard WKB (a live DB via STAsBinary): nothing to decode. The
		// SRID travels separately on the Feature (rawSRID), so return 0 here.
		return blob, 0, false, nil
	case geomKindSpatiaLite:
		return decodeSpatiaLiteBlob(blob)
	default: // geomKindGPKG
		w, s, e, decErr := decodeGPB(blob)
		return w, int(s), e, decErr
	}
}

// decodeGPB parses a GeoPackage geometry blob (GeoPackage spec, "GeoPackageBinary"
// header) into the standard WKB it wraps. Layout: magic "GP" (2 bytes) | version
// (1) | flags (1) | srs_id (int32) | envelope (0/32/48/64 bytes per the flags'
// envelope indicator) | WKB. Byte order of the srs_id/envelope fields is per the
// flags' low bit. It returns the WKB, the declared srs_id, and whether the empty
// flag is set.
func decodeGPB(blob []byte) (wkb []byte, srsID int32, empty bool, err error) {
	if len(blob) < 8 {
		return nil, 0, false, fmt.Errorf("geometry blob too short (%d bytes)", len(blob))
	}
	if blob[0] != 'G' || blob[1] != 'P' {
		return nil, 0, false, fmt.Errorf("not a GeoPackage geometry blob (bad magic)")
	}

	flags := blob[3]
	var order binary.ByteOrder = binary.BigEndian
	if flags&0x01 == 0x01 {
		order = binary.LittleEndian
	}
	empty = (flags>>4)&0x01 == 0x01
	envCode := (flags >> 1) & 0x07

	srsID = int32(order.Uint32(blob[4:8]))

	var envSize int
	switch envCode {
	case 0:
		envSize = 0
	case 1:
		envSize = 32 // [minx,maxx,miny,maxy]
	case 2, 3:
		envSize = 48 // + z or m range
	case 4:
		envSize = 64 // + z and m ranges
	default:
		return nil, 0, false, fmt.Errorf("invalid GeoPackage envelope indicator %d", envCode)
	}

	off := 8 + envSize
	if off > len(blob) {
		return nil, 0, false, fmt.Errorf("geometry blob truncated (need %d bytes, have %d)", off, len(blob))
	}
	return blob[off:], srsID, empty, nil
}

// downloadToTemp streams an object-store object to a local temp file (SQLite
// needs random access to the whole file). It returns the path and a cleanup that
// removes it; the caller must always call cleanup.
func downloadToTemp(ctx context.Context, store *export.ObjectStore, objectKey string) (path string, cleanup func(), err error) {
	rc, err := store.Open(ctx, objectKey)
	if err != nil {
		return "", nil, fmt.Errorf("opening uploaded file: %w", err)
	}
	defer rc.Close()

	tmp, err := os.CreateTemp("", "voltedge-import-*.gpkg")
	if err != nil {
		return "", nil, fmt.Errorf("creating temp file for GeoPackage: %w", err)
	}
	if _, err := io.Copy(tmp, rc); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", nil, fmt.Errorf("downloading GeoPackage: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", nil, fmt.Errorf("finalizing GeoPackage temp file: %w", err)
	}
	name := tmp.Name()
	return name, func() { _ = os.Remove(name) }, nil
}

// openSQLite opens a local SQLite file for querying. It opens by plain path (not
// a file: URI) to avoid cross-platform DSN-parameter quirks; the file is our own
// throwaway temp copy and we only ever SELECT, so a read-write handle is
// harmless (nothing here writes to it).
func openSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening GeoPackage: %w", err)
	}
	return db, nil
}

// quoteIdent quotes a SQLite identifier (table/column name) by wrapping it in
// double quotes and doubling any embedded quote.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
