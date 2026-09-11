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
	"io"
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
	// manifest. Bump it when the SQLite table shapes or metadata blob shape change
	// so the app can refuse (or migrate) a pack it doesn't understand. v2 added the
	// domain_values table (coded-field pick lists for the offline editor); v3 added
	// each field's RequiredForSubtypes to the metadata.fields blob (per-subtype
	// requiredness for the edit form's required marker + validation); v4 added the
	// symbology table (map-context symbology_rules resolved to codes/field-names,
	// so the on-device map styles features by metadata instead of a placeholder
	// per-class color) plus the domain-field attribute values those rules key off,
	// carried in the tile properties so MapLibre's data-driven paint can match them;
	// v5 added the icons table (the metadata.symbology_icons SVG markup a rule's
	// icon_ref points at, so the device renders a point as its real shape -- incl.
	// admin-authored custom icons -- instead of falling back to a built-in circle);
	// v6 added the attachments table (image attachment metadata + the server-side
	// JPEG thumbnail bytes for every packed feature's photos, so the field tool
	// browses a feature's images offline -- full-res is fetched on-demand from the
	// fieldsync attachments surface when the device is back online, Phase 22 S4);
	// v7 added the relationship_classes + network_rules tables (Slice 14b), the
	// metadata.relationship_classes / metadata.network_rules rows for the packed
	// classes with their feature-class / subtype / terminal UUIDs pre-resolved to
	// the codes the pack stores, so the offline editor picks a relationship KIND
	// and soft-warns before an illegal connection. These are advisory on-device;
	// the server re-validates every edge on sync (guide-offline + backstop-on-sync).
	packSchemaVersion = 7

	defaultMinZoom = 6
	defaultMaxZoom = 16
)

// buildResult carries the finished local artifacts + counts back to buildPack.
type buildResult struct {
	tilesPath        string
	dataPath         string
	featureCount     int
	connCount        int
	relCount         int
	domainValueCount int
	symbologyCount   int
	iconCount        int
	attachmentCount  int
	relClassCount    int
	netRuleCount     int
	snapshotAt       time.Time
}

// packAttachmentMeta is one image attachment collected under the pack snapshot,
// before its thumbnail bytes are fetched from RustFS (done after the DB tx closes,
// so a slow object read doesn't hold the snapshot open). ThumbKey is nil when the
// image committed without a thumbnail; the row still ships so the device knows an
// image exists and can fetch full-res online.
type packAttachmentMeta struct {
	ID           string
	FeatureClass string
	FeatureID    string
	Filename     string
	ContentType  string
	ByteSize     int64
	ThumbKey     *string
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

	res, err := buildArtifacts(ctx, db, store, scope, tmpDir, pack.ID, pack.RequestedBy)
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

	if err := completePack(ctx, db, pack.ID, tilesKey, dataKey, tilesBytes, dataBytes, res.featureCount, res.attachmentCount, res.snapshotAt); err != nil {
		// Both objects are uploaded but the row didn't flip (e.g. cancelled, or a
		// transient DB fault): the objects are now unreferenced. A retention sweep
		// (a later slice, like the export queue's) reaps orphans; nothing to serve.
		log.Printf("fieldpack: pack %s uploaded but marking complete failed: %v", pack.ID, err)
		return
	}
	log.Printf("fieldpack: pack %s built: features=%d conns=%d rels=%d domainvals=%d symbology=%d icons=%d attachments=%d relclasses=%d netrules=%d tiles=%dB data=%dB",
		pack.ID, res.featureCount, res.connCount, res.relCount, res.domainValueCount, res.symbologyCount, res.iconCount, res.attachmentCount, res.relClassCount, res.netRuleCount, tilesBytes, dataBytes)
}

// buildArtifacts does the read + encode: it writes the SQLite pack and the
// per-class GeoJSON under one snapshot, then runs tippecanoe. It returns the two
// local file paths + counts; the caller uploads them.
func buildArtifacts(ctx context.Context, db *bun.DB, store *export.ObjectStore, scope PackScope, tmpDir, packID, requestedBy string) (buildResult, error) {
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
	domStmt, err := stx.PrepareContext(ctx, `INSERT INTO domain_values
		(domain_id, code, label, sort_order)
		VALUES (?, ?, ?, ?)`)
	if err != nil {
		stx.Rollback()
		return buildResult{}, err
	}
	defer domStmt.Close()
	symStmt, err := stx.PrepareContext(ctx, `INSERT INTO symbology
		(feature_class, subtype_code, domain_field, domain_value_code, min_zoom, max_zoom, style_def)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		stx.Rollback()
		return buildResult{}, err
	}
	defer symStmt.Close()
	iconStmt, err := stx.PrepareContext(ctx, `INSERT INTO icons (icon_ref, svg) VALUES (?, ?)`)
	if err != nil {
		stx.Rollback()
		return buildResult{}, err
	}
	defer iconStmt.Close()
	relClassStmt, err := stx.PrepareContext(ctx, `INSERT INTO relationship_classes
		(origin_class, destination_class, kind, cardinality, name)
		VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		stx.Rollback()
		return buildResult{}, err
	}
	defer relClassStmt.Close()
	netRuleStmt, err := stx.PrepareContext(ctx, `INSERT INTO network_rules
		(from_class, from_subtype, from_terminal, to_class, to_subtype, to_terminal, phase_rule, is_valid, name)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		stx.Rollback()
		return buildResult{}, err
	}
	defer netRuleStmt.Close()
	// Prepared here but used AFTER the DB tx closes -- the attachment rows are
	// collected under the snapshot, then their thumbnail bytes are read from RustFS
	// and inserted, so a slow object read never holds the Postgres snapshot open.
	attStmt, err := stx.PrepareContext(ctx, `INSERT INTO attachments
		(id, feature_class, feature_id, filename, content_type, byte_size, thumb)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		stx.Rollback()
		return buildResult{}, err
	}
	defer attStmt.Close()

	packed := map[string]struct{}{}
	// The coded domains referenced by any packed field, collected across all
	// classes so a domain shared by several classes is written to the pack once.
	domainIDs := map[string]struct{}{}
	// The icon_refs any packed class's map-context rules point at, collected across
	// classes so a shared icon (e.g. every class's default circle) ships once. The
	// referenced symbology_icons SVG rows are written to the pack after the loop.
	iconRefs := map[string]struct{}{}
	// Image attachments for the packed features, collected under the snapshot; their
	// thumbnail bytes are fetched from RustFS after the tx (see writePackAttachments).
	var attachMetas []packAttachmentMeta
	// UUID -> code lookups accumulated across every packed class, so the
	// relationship_classes / network_rules loaders (which reference classes,
	// subtypes, and terminals by UUID) can resolve those to the codes the pack
	// actually stores. A rule pointing at a class outside the pack is dropped.
	classCodeByID := map[string]string{}
	subtypeCodeByID := map[string]int{}
	terminalCodeByID := map[string]string{}
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

				// Record this class's UUID->code lookups so the post-loop
				// relationship_classes / network_rules loaders can resolve their
				// UUID references to the codes the pack stores.
				classCodeByID[loaded.FeatureClass.ID] = loaded.FeatureClass.Code
				for _, s := range loaded.Subtypes {
					subtypeCodeByID[s.ID] = s.Code
				}
				for _, t := range loaded.Terminals {
					terminalCodeByID[t.ID] = t.Code
				}

				lw, err := layerWriterFor(layers, tmpDir, code)
				if err != nil {
					return err
				}

				// Resolve this class's map-context symbology BEFORE its features:
				// the returned symFields are the attribute names those rules match
				// on, which writeFeature must fold into each feature's tile props so
				// MapLibre's data-driven paint can key off them on-device.
				symN, symFields, err := loadClassSymbology(ctx, tx, loaded, symStmt, iconRefs)
				if err != nil {
					return fmt.Errorf("resolving symbology for %q: %w", code, err)
				}
				res.symbologyCount += symN

				_, err = readClassFeatures(ctx, tx, loaded, bbox, scope.TerritoryIDs, func(row packRow) error {
					attrs, err := json.Marshal(row.Attributes)
					if err != nil {
						return fmt.Errorf("encoding attributes for %s/%s: %w", code, row.ID, err)
					}
					if _, err := featStmt.ExecContext(ctx, row.ID, code, row.Name, row.Status, row.Subtype, row.TerritoryID, row.WKB, string(attrs)); err != nil {
						return fmt.Errorf("writing feature %s/%s: %w", code, row.ID, err)
					}
					if err := lw.writeFeature(row, symFields); err != nil {
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

				// metadata.Load only populates DomainValues for CODED domains, so its
				// keys are exactly the pick-list domains this class's fields reference.
				// Range/other domains have no value list and stay free-text on-device.
				for id := range loaded.DomainValues {
					domainIDs[id] = struct{}{}
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
				// The relationship-class definitions (origin/destination pair ->
				// kind, cardinality) for the packed classes, so the offline editor
				// offers a KIND picker when a pair allows more than one.
				res.relClassCount, err = loadPackRelationshipClasses(ctx, tx, classCodeByID, relClassStmt)
				if err != nil {
					return fmt.Errorf("loading relationship classes: %w", err)
				}
			}
			if scope.IncludeConnectivity {
				// The connectivity rules (from/to class + optional subtype/terminal
				// -> valid|invalid) for the packed classes, so the offline editor
				// soft-warns before an illegal connection. Advisory only; the server
				// re-validates on sync.
				res.netRuleCount, err = loadPackNetworkRules(ctx, tx, classCodeByID, subtypeCodeByID, terminalCodeByID, netRuleStmt)
				if err != nil {
					return fmt.Errorf("loading network rules: %w", err)
				}
			}

			// The pick lists for every coded field the pack ships, so the offline
			// editor renders coded fields as dropdowns (code -> label) instead of
			// free text and can validate against the domain before sync.
			if len(domainIDs) > 0 {
				res.domainValueCount, err = loadDomainValues(ctx, tx, domainIDs, domStmt)
				if err != nil {
					return fmt.Errorf("loading domain values: %w", err)
				}
			}

			// The SVG markup for every icon_ref the packed rules point at, so the
			// device renders a point as its real shape (incl. an admin-authored
			// custom icon like a transformer's double-circle) instead of a built-in
			// fallback shape. Ships only the referenced icons, keyed by ref.
			if len(iconRefs) > 0 {
				res.iconCount, err = writePackIcons(ctx, tx, iconRefs, iconStmt)
				if err != nil {
					return fmt.Errorf("loading symbology icons: %w", err)
				}
			}

			// Collect the image attachments belonging to the packed features under the
			// same snapshot. Only the metadata + thumb key is read here; the thumbnail
			// bytes come from RustFS after this tx closes.
			attachMetas, err = loadPackAttachments(ctx, tx, packed)
			if err != nil {
				return fmt.Errorf("loading attachments: %w", err)
			}
			return nil
		})
	if txErr != nil {
		stx.Rollback()
		return buildResult{}, txErr
	}

	// The snapshot is closed; now pull each attachment's thumbnail bytes from RustFS
	// and write them into the pack. Done outside the DB tx so a slow object store
	// never holds the Postgres snapshot open, and still inside the SQLite tx (stx) so
	// the attachment rows commit atomically with the rest of the pack.
	if len(attachMetas) > 0 {
		res.attachmentCount, err = writePackAttachments(ctx, store, attStmt, attachMetas)
		if err != nil {
			stx.Rollback()
			return buildResult{}, fmt.Errorf("writing attachment thumbnails: %w", err)
		}
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
		`CREATE TABLE domain_values (
			domain_id  TEXT NOT NULL,
			code       TEXT NOT NULL,
			label      TEXT,
			sort_order INTEGER,
			PRIMARY KEY (domain_id, code)
		)`,
		// Map-context symbology rules resolved to codes/field-names (pack schema
		// v4). One row per rule; the device compiles a class's rows into MapLibre
		// data-driven paint. subtype_code / domain_field+domain_value_code are the
		// match axes (either or both may be null -- the class-level default rule
		// has both null); min_zoom/max_zoom are the rule's zoom bounds; style_def
		// is the raw JSON the renderer reads (color/width/dash/fill_color/etc.).
		`CREATE TABLE symbology (
			feature_class     TEXT NOT NULL,
			subtype_code      INTEGER,
			domain_field      TEXT,
			domain_value_code TEXT,
			min_zoom          INTEGER,
			max_zoom          INTEGER,
			style_def         TEXT NOT NULL
		)`,
		// The SVG markup behind a symbology rule's icon_ref (pack schema v5), from
		// metadata.symbology_icons. One row per referenced icon; the device
		// substitutes the rule's resolved color into {{COLOR}} and rasterizes it to
		// a map image. A missing icon_ref (older pack, or an unshipped ref) falls
		// back to a built-in drawn shape on-device, so this table is additive.
		`CREATE TABLE icons (
			icon_ref TEXT PRIMARY KEY,
			svg      TEXT NOT NULL
		)`,
		// Image attachments for the packed features (pack schema v6). One row per
		// committed image; thumb is the server-generated JPEG thumbnail bytes (NULL
		// when the image committed without one -- the device shows a placeholder and
		// fetches full-res online). Full-resolution images never ride the pack; the
		// device pulls them on demand from the fieldsync attachments surface.
		`CREATE TABLE attachments (
			id            TEXT PRIMARY KEY,
			feature_class TEXT NOT NULL,
			feature_id    TEXT NOT NULL,
			filename      TEXT,
			content_type  TEXT,
			byte_size     INTEGER,
			thumb         BLOB
		)`,
		// The relationship-class catalogue for the packed classes (pack schema v7),
		// with the origin/destination feature-class UUIDs resolved to codes. One row
		// per legal (origin, destination) pairing; kind disambiguates a pair that
		// allows more than one. The device offers a KIND picker from these before
		// establishing a relationship offline. Additive: an older pack has no rows
		// and the editor falls back to no picker.
		`CREATE TABLE relationship_classes (
			origin_class      TEXT NOT NULL,
			destination_class TEXT NOT NULL,
			kind              TEXT NOT NULL,
			cardinality       TEXT NOT NULL,
			name              TEXT
		)`,
		// The connectivity rules for the packed classes (pack schema v7), with the
		// class UUIDs resolved to codes, subtype UUIDs to their integer codes, and
		// terminal UUIDs to their codes. A null subtype/terminal is a wildcard; the
		// device matches most-specific-first and treats is_valid = 0 (or no match)
		// as a soft warning. Advisory only -- the server re-validates every edge on
		// sync. Additive: an older pack has no rows and the editor skips the warning.
		`CREATE TABLE network_rules (
			from_class    TEXT NOT NULL,
			from_subtype  INTEGER,
			from_terminal TEXT,
			to_class      TEXT NOT NULL,
			to_subtype    INTEGER,
			to_terminal   TEXT,
			phase_rule    TEXT,
			is_valid      INTEGER NOT NULL,
			name          TEXT
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
		`CREATE INDEX symbology_class_idx      ON symbology (feature_class)`,
		`CREATE INDEX attachments_feature_idx  ON attachments (feature_id)`,
		`CREATE INDEX relationship_classes_pair_idx ON relationship_classes (origin_class, destination_class)`,
		`CREATE INDEX network_rules_pair_idx        ON network_rules (from_class, to_class)`,
	}
	for _, s := range stmts {
		if _, err := sq.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("building pack indexes: %w", err)
		}
	}
	return nil
}

// packField is one field's pack serialization: the metadata.Field verbatim (Go's
// default capitalized JSON keys, which the device's parser reads) plus the
// subtype codes the field is required for. The latter is the field-tool
// equivalent of the web schema's required_for_subtypes, so the offline editor
// marks a field required for the feature's actual subtype instead of inferring
// requiredness from column nullability. Embedding metadata.Field keeps every
// existing key identical (the device ignores the added one on an older build).
type packField struct {
	metadata.Field
	RequiredForSubtypes []int `json:"RequiredForSubtypes"`
}

// insertClassMetadata writes one class's metadata row -- its shape + the JSON
// blobs (fields/subtypes/terminals) the device reconstructs the class from.
func insertClassMetadata(ctx context.Context, stmt *sql.Stmt, loaded *metadata.Loaded) error {
	packFields := make([]packField, len(loaded.Fields))
	for i, f := range loaded.Fields {
		packFields[i] = packField{Field: f, RequiredForSubtypes: loaded.RequiredSubtypeCodes(f.ID)}
	}
	fields, err := json.Marshal(packFields)
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

// loadPackRelationshipClasses writes the relationship-class catalogue for the
// packed classes into the pack, resolving each row's origin/destination feature
// class UUIDs to the codes the pack stores. Only rows whose BOTH ends are inside
// the packed class set are kept -- a definition pointing at a class outside the
// pack can never be exercised offline. The device reads these to offer a KIND
// picker when an (origin, destination) pair allows more than one relationship.
func loadPackRelationshipClasses(ctx context.Context, db bun.IDB, classCodeByID map[string]string, stmt *sql.Stmt) (int, error) {
	classIDs := make([]string, 0, len(classCodeByID))
	for id := range classCodeByID {
		classIDs = append(classIDs, id)
	}
	if len(classIDs) == 0 {
		return 0, nil
	}
	var classes []metadata.RelationshipClass
	if err := db.NewSelect().Model(&classes).
		Where("origin_feature_class_id IN (?)", bun.In(classIDs)).
		Where("destination_feature_class_id IN (?)", bun.In(classIDs)).
		Order("origin_feature_class_id").
		Order("destination_feature_class_id").
		Scan(ctx); err != nil {
		return 0, err
	}
	n := 0
	for _, rc := range classes {
		origin, ok := classCodeByID[rc.OriginFeatureClassID]
		if !ok {
			continue
		}
		dest, ok := classCodeByID[rc.DestinationFeatureClassID]
		if !ok {
			continue
		}
		if _, err := stmt.ExecContext(ctx, origin, dest, rc.Kind, rc.Cardinality, rc.Name); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// loadPackNetworkRules writes the connectivity rules for the packed classes into
// the pack, resolving each row's class UUIDs to codes, subtype UUIDs to their
// integer codes, and terminal UUIDs to their codes. Only rows whose BOTH class
// ends are packed are kept. A subtype/terminal UUID that can't be resolved (the
// class is packed but the sub-part is unexpectedly missing) is written as NULL --
// i.e. it degrades to a wildcard match rather than dropping an otherwise-valid
// rule. The device matches these most-specific-first as an advisory soft warning;
// the server re-validates every edge on sync.
func loadPackNetworkRules(ctx context.Context, db bun.IDB, classCodeByID map[string]string, subtypeCodeByID map[string]int, terminalCodeByID map[string]string, stmt *sql.Stmt) (int, error) {
	classIDs := make([]string, 0, len(classCodeByID))
	for id := range classCodeByID {
		classIDs = append(classIDs, id)
	}
	if len(classIDs) == 0 {
		return 0, nil
	}
	var rules []metadata.NetworkRule
	if err := db.NewSelect().Model(&rules).
		Where("from_feature_class_id IN (?)", bun.In(classIDs)).
		Where("to_feature_class_id IN (?)", bun.In(classIDs)).
		Order("from_feature_class_id").
		Order("to_feature_class_id").
		Scan(ctx); err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rules {
		fromClass, ok := classCodeByID[r.FromFeatureClassID]
		if !ok {
			continue
		}
		toClass, ok := classCodeByID[r.ToFeatureClassID]
		if !ok {
			continue
		}
		var isValid int
		if r.IsValid {
			isValid = 1
		}
		if _, err := stmt.ExecContext(ctx,
			fromClass,
			subtypeCode(subtypeCodeByID, r.FromSubtypeID),
			terminalCode(terminalCodeByID, r.FromTerminalID),
			toClass,
			subtypeCode(subtypeCodeByID, r.ToSubtypeID),
			terminalCode(terminalCodeByID, r.ToTerminalID),
			r.PhaseRule,
			isValid,
			r.Name,
		); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// subtypeCode resolves an optional subtype UUID to its integer code for the pack,
// returning nil (SQL NULL, i.e. a wildcard match on-device) when the id is absent
// or unresolvable.
func subtypeCode(byID map[string]int, id *string) *int {
	if id == nil {
		return nil
	}
	if code, ok := byID[*id]; ok {
		return &code
	}
	return nil
}

// terminalCode resolves an optional terminal UUID to its code for the pack,
// returning nil (SQL NULL, i.e. a wildcard match on-device) when the id is absent
// or unresolvable.
func terminalCode(byID map[string]string, id *string) *string {
	if id == nil {
		return nil
	}
	if code, ok := byID[*id]; ok {
		return &code
	}
	return nil
}

// loadDomainValues writes the coded pick lists (code, label, sort_order) for the
// referenced coded domains into the pack, ordered so the device can render a
// dropdown without re-sorting. Values are read from metadata.domain_values under
// the same snapshot as everything else in the pack.
func loadDomainValues(ctx context.Context, db bun.IDB, domainIDs map[string]struct{}, stmt *sql.Stmt) (int, error) {
	ids := make([]string, 0, len(domainIDs))
	for id := range domainIDs {
		ids = append(ids, id)
	}
	var values []metadata.DomainValue
	if err := db.NewSelect().Model(&values).
		Where("domain_id IN (?)", bun.In(ids)).
		Order("domain_id").
		Order("sort_order").
		Scan(ctx); err != nil {
		return 0, err
	}
	n := 0
	for _, v := range values {
		if _, err := stmt.ExecContext(ctx, v.DomainID, v.Code, v.Label, v.SortOrder); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// loadClassSymbology resolves one feature class's symbology rules the same way the
// web schema endpoint does (api/cmd/server/main.go's resolveSymbologySchema) --
// subtype_id -> subtype code, domain_value_id -> (field name, value code) -- and
// writes each MAP-context rule to the pack's symbology table. The field app renders
// only a map, so diagram-context rules are dropped entirely. It returns the number
// of rules written plus the set of domain field NAMES any rule keys off, so the
// caller can fold those attribute values into the tile props (writeFeature) for
// MapLibre's data-driven paint to match on-device. It also collects each
// map-context rule's style_def.icon_ref into [iconRefs] (shared across classes),
// so the caller ships exactly the referenced symbology_icons SVG rows. Runs under
// the same pack snapshot tx as every other read.
func loadClassSymbology(ctx context.Context, tx bun.IDB, loaded *metadata.Loaded, stmt *sql.Stmt, iconRefs map[string]struct{}) (int, map[string]struct{}, error) {
	rules, err := metadata.LoadSymbologyRules(ctx, tx, loaded.FeatureClass.ID)
	if err != nil {
		return 0, nil, err
	}

	subtypeCodeByID := make(map[string]int, len(loaded.Subtypes))
	for _, s := range loaded.Subtypes {
		subtypeCodeByID[s.ID] = s.Code
	}

	valueIDs := make([]string, 0, len(rules))
	for _, r := range rules {
		if r.DomainValueID != nil {
			valueIDs = append(valueIDs, *r.DomainValueID)
		}
	}
	valueByID := make(map[string]metadata.DomainValue, len(valueIDs))
	if len(valueIDs) > 0 {
		var values []metadata.DomainValue
		if err := tx.NewSelect().Model(&values).Where("id IN (?)", bun.In(valueIDs)).Scan(ctx); err != nil {
			return 0, nil, err
		}
		for _, v := range values {
			valueByID[v.ID] = v
		}
	}

	// A field's name, keyed by the coded domain it draws from -- so a rule that
	// targets a domain value resolves to "the field whose value to compare".
	fieldNameByDomainID := make(map[string]string, len(loaded.Fields))
	for _, f := range loaded.Fields {
		if f.DomainID != nil {
			fieldNameByDomainID[*f.DomainID] = f.Name
		}
	}

	domainFields := map[string]struct{}{}
	n := 0
	for _, r := range rules {
		if r.RenderContext != "map" {
			continue
		}
		var subtypeCode *int
		if r.SubtypeID != nil {
			if code, ok := subtypeCodeByID[*r.SubtypeID]; ok {
				c := code
				subtypeCode = &c
			}
		}
		var domainField, domainValueCode *string
		if r.DomainValueID != nil {
			if v, ok := valueByID[*r.DomainValueID]; ok {
				if name, ok := fieldNameByDomainID[v.DomainID]; ok {
					fn := name
					domainField = &fn
					domainFields[name] = struct{}{}
				}
				code := v.Code
				domainValueCode = &code
			}
		}
		if _, err := stmt.ExecContext(ctx, loaded.FeatureClass.Code, subtypeCode, domainField, domainValueCode, r.MinZoom, r.MaxZoom, string(r.StyleDef)); err != nil {
			return n, nil, err
		}
		// Collect the icon this rule draws with (if any), so the pack ships its
		// SVG. style_def is opaque JSON here; only icon_ref matters for shipping.
		var sd struct {
			IconRef string `json:"icon_ref"`
		}
		if err := json.Unmarshal(r.StyleDef, &sd); err == nil && sd.IconRef != "" {
			iconRefs[sd.IconRef] = struct{}{}
		}
		n++
	}
	return n, domainFields, nil
}

// writePackIcons writes the metadata.symbology_icons SVG markup for the given
// icon_refs into the pack's icons table -- the field-tool counterpart of the
// web's use-symbology-icons.ts fetch. Only referenced icons are shipped (an
// admin icon set can grow without bloating every pack). A ref with no matching
// row is simply absent from the pack, and the device falls back to a built-in
// drawn shape for it. Reads under the same snapshot as everything else.
func writePackIcons(ctx context.Context, db bun.IDB, iconRefs map[string]struct{}, stmt *sql.Stmt) (int, error) {
	refs := make([]string, 0, len(iconRefs))
	for ref := range iconRefs {
		refs = append(refs, ref)
	}
	var icons []metadata.SymbologyIcon
	if err := db.NewSelect().Model(&icons).
		Where("icon_ref IN (?)", bun.In(refs)).
		Scan(ctx); err != nil {
		return 0, err
	}
	n := 0
	for _, ic := range icons {
		if _, err := stmt.ExecContext(ctx, ic.IconRef, ic.SVG); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// loadPackAttachments streams every ready image attachment and keeps the ones
// whose feature is inside the packed set -- the same stream-and-membership-filter
// pattern loadConnectivity uses, so a big attachment table doesn't become a giant
// IN (...) over the packed ids. Only metadata + the thumb key is read here; the
// thumbnail bytes are fetched from RustFS after the snapshot tx closes
// (writePackAttachments). Documents are excluded -- the field tool is images-only.
func loadPackAttachments(ctx context.Context, db bun.IDB, packed map[string]struct{}) ([]packAttachmentMeta, error) {
	const q = `
SELECT a.id::text, a.feature_class, a.feature_id::text, a.filename, a.content_type, a.byte_size, a.thumb_key
FROM files.attachments a
WHERE a.kind = 'image' AND a.status = 'ready' AND a.deleted_at IS NULL`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metas []packAttachmentMeta
	for rows.Next() {
		var m packAttachmentMeta
		var thumbKey sql.NullString
		if err := rows.Scan(&m.ID, &m.FeatureClass, &m.FeatureID, &m.Filename, &m.ContentType, &m.ByteSize, &thumbKey); err != nil {
			return nil, err
		}
		if _, ok := packed[m.FeatureID]; !ok {
			continue
		}
		if thumbKey.Valid {
			k := thumbKey.String
			m.ThumbKey = &k
		}
		metas = append(metas, m)
	}
	return metas, rows.Err()
}

// writePackAttachments reads each collected attachment's thumbnail bytes from
// RustFS and inserts the row into the pack. A thumb that can't be read (missing
// key, transient object-store error) is written as a NULL blob rather than
// failing the whole build -- the device still lists the image and fetches full-res
// online; only the offline preview degrades to a placeholder.
func writePackAttachments(ctx context.Context, store *export.ObjectStore, stmt *sql.Stmt, metas []packAttachmentMeta) (int, error) {
	n := 0
	for _, m := range metas {
		var thumb []byte
		if m.ThumbKey != nil {
			if rc, err := store.Open(ctx, *m.ThumbKey); err == nil {
				b, rerr := io.ReadAll(rc)
				rc.Close()
				if rerr == nil {
					thumb = b
				}
			}
		}
		// database/sql writes a nil []byte as SQL NULL, which is what we want for a
		// missing thumbnail.
		if _, err := stmt.ExecContext(ctx, m.ID, m.FeatureClass, m.FeatureID, m.Filename, m.ContentType, m.ByteSize, thumb); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
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
		{"domain_value_count", strconv.Itoa(res.domainValueCount)},
		{"symbology_count", strconv.Itoa(res.symbologyCount)},
		{"icon_count", strconv.Itoa(res.iconCount)},
		{"attachment_count", strconv.Itoa(res.attachmentCount)},
		{"relationship_class_count", strconv.Itoa(res.relClassCount)},
		{"network_rule_count", strconv.Itoa(res.netRuleCount)},
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

func (lw *layerWriter) writeFeature(row packRow, symFields map[string]struct{}) error {
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
	// Only the attribute values a symbology rule actually keys off ride in the
	// tile props -- kept minimal so tiles stay small, but enough for MapLibre's
	// data-driven paint to resolve a domain-value rule on-device. A feature that
	// lacks the value simply doesn't get the prop (the rule then won't match it).
	for name := range symFields {
		if v, ok := row.Attributes[name]; ok && v != nil {
			props[name] = v
		}
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
