package export

// Export job execution core (Phase 17, docs/phase-17-large-export.md).
//
// This is the per-job work shared by every driver of the ops.export_jobs
// queue: claim a job, encode its scope, stream the bytes into RustFS, mark it
// succeeded/failed. The Slice 2 throwaway in-process runner used it; the
// standalone cmd/exportworker (Slice 3b, via RunWorker in worker.go) uses the
// exact same code -- only the claim loop and process boundary differ. runExport
// (the format-agnostic gather + encode) and runClaimedJob (its RustFS wrapper)
// have no knowledge of who is driving them.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"
	"time"

	"github.com/kwaabs/voltedge/api/internal/logx"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/webhookout"
	"github.com/uptrace/bun"
)

// sleep waits d, or returns early if ctx is cancelled.
func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// encodeResult carries the encoder goroutine's outcome back to runClaimedJob
// over a channel, so reading count/err after the upload returns is ordered by
// the channel receive rather than racing the pipe's internals.
type encodeResult struct {
	count int
	err   error
}

// runClaimedJob encodes one claimed job straight into RustFS and marks it
// succeeded/failed. The encoder writes into a pipe whose read end feeds the
// multipart upload, so the artifact streams out in constant memory and never
// lands on local disk; object_key holds the RustFS key the download path
// presigns.
func runClaimedJob(ctx context.Context, db *bun.DB, store *ObjectStore, job *ExportJob) {
	// Asset-register jobs share this queue but build a tabular .xlsx via the
	// injected boq builder, not a streamed geometry encode (migration 0153).
	if job.Kind == "asset_register" {
		runAssetRegisterJob(ctx, db, store, job)
		return
	}

	var scope ExportScope
	if err := json.Unmarshal(job.Scope, &scope); err != nil {
		markFailed(ctx, db, job.ID, fmt.Sprintf("corrupt job scope: %v", err))
		return
	}

	spec, ok := formats[scope.Format]
	if !ok {
		markFailed(ctx, db, job.ID, fmt.Sprintf("unsupported export format %q", scope.Format))
		return
	}

	// Incremental CIM export for a named sync target: a buffered diff flow rather
	// than the streamed single-artifact path (it must hold two models to diff and
	// advance a baseline after delivery).
	if scope.Format == "cim" && scope.SyncTargetID != nil {
		runCIMSyncJob(ctx, db, store, job, scope)
		return
	}

	key := objectKey(job.ID, spec.ext)

	pr, pw := io.Pipe()
	done := make(chan encodeResult, 1)
	go func() {
		var n int
		var err error
		// A panic inside the encoder (e.g. a malformed geometry) would otherwise
		// crash the whole worker process from this goroutine. Recover it into an
		// error so it aborts the upload and fails only THIS job.
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("export encoder panicked: %v", r)
				logx.From(ctx).Error("export encoder panicked", "panic", r, "stack", string(debug.Stack()))
			}
			// Closing with the encode error propagates it to the upload's Read,
			// aborting the multipart upload so no partial object is left behind.
			pw.CloseWithError(err)
			done <- encodeResult{count: n, err: err}
		}()
		n, err = runExport(ctx, db, scope, pw, job.CreatedAt)
	}()

	size, upErr := store.Upload(ctx, key, pr, spec.contentType)
	if upErr != nil {
		// If the upload died first (e.g. RustFS unreachable), unblock the still
		// -writing encoder so its goroutine can exit and report on done.
		pr.CloseWithError(upErr)
	}
	res := <-done

	// The encode error is the root cause when both fail (a failed encode makes
	// the upload fail too), so it is reported first.
	if res.err != nil {
		markFailed(ctx, db, job.ID, fmt.Sprintf("encoding export: %v", res.err))
		return
	}
	if upErr != nil {
		markFailed(ctx, db, job.ID, fmt.Sprintf("uploading export: %v", upErr))
		return
	}

	if err := completeExport(ctx, db, job.ID, key, size, res.count); err != nil {
		// The object is uploaded but unreferenced; retention cleanup (an open
		// question in the design doc) will reap it. Nothing to serve, so log.
		logx.From(ctx).Error("uploaded but marking complete failed", "err", err)
	}
}

// crsWKT returns the CRS WKT to stamp into a shapefile .prj. With srid nil the
// export kept its stored SRID (4326), so the canonical ESRI-flavoured WGS84 WKT
// is used -- unchanged from before reprojection existed. With a target SRID the
// WKT comes from PostGIS spatial_ref_sys.srtext, so a projected export (e.g. a
// UTM zone in metres) is labelled with its true CRS. srtext is OGC WKT, which
// QGIS/ArcGIS read from a .prj; the SRID was validated to exist at enqueue, so a
// missing/empty srtext here is a genuine data problem worth surfacing.
func crsWKT(ctx context.Context, db *bun.DB, srid *int) (string, error) {
	if srid == nil {
		return wgs84WKT, nil
	}
	var srtext string
	if err := db.NewRaw("SELECT srtext FROM spatial_ref_sys WHERE srid = ?", *srid).Scan(ctx, &srtext); err != nil {
		return "", fmt.Errorf("export: reading WKT for SRID %d: %w", *srid, err)
	}
	if srtext == "" {
		return "", fmt.Errorf("export: SRID %d has no WKT in spatial_ref_sys, cannot write a .prj", *srid)
	}
	return srtext, nil
}

// objectKey is the RustFS key for a job's artifact: namespaced by job id so
// keys never collide and a job maps to exactly one object.
func objectKey(jobID, ext string) string {
	return "exports/" + jobID + "/voltedge-export." + ext
}

// markFailed records a job failure, logging if even that write fails.
func markFailed(ctx context.Context, db *bun.DB, id, msg string) {
	if err := failExport(ctx, db, id, msg); err != nil {
		logx.From(ctx).Error("job failed and marking failed also failed", "reason", msg, "err", err)
	}
}

// runCIMSyncJob encodes an incremental CIM export for a named sync target: build
// the current full model, diff it against the target's stored baseline (cimdiff
// .go), upload the resulting CIM difference model as the job artifact, and -- only
// once that upload + completeExport succeed -- advance the baseline. The first run
// (no baseline) uploads the full model. Buffered, not streamed: both models must be
// in memory to diff, and sync scopes are bounded (a feeder/zone of published data).
func runCIMSyncJob(ctx context.Context, db *bun.DB, store *ObjectStore, job *ExportJob, scope ExportScope) {
	if baselineStore == nil {
		markFailed(ctx, db, job.ID, "CIM sync export is not configured (no baseline store)")
		return
	}
	target, err := getSyncTarget(ctx, db, *scope.SyncTargetID)
	if err != nil {
		markFailed(ctx, db, job.ID, fmt.Sprintf("loading sync target: %v", err))
		return
	}

	// 1. The current full model, into a buffer (runExport ignores SyncTargetID and
	//    just encodes the full CIM model for this scope).
	var current bytes.Buffer
	count, err := runExport(ctx, db, scope, &current, job.CreatedAt)
	if err != nil {
		markFailed(ctx, db, job.ID, fmt.Sprintf("encoding current model: %v", err))
		return
	}

	// 2. The artifact: a difference model vs the baseline, or the full model on the
	//    first run.
	artifact := current.Bytes()
	isFullModel := true
	var diff DiffResult
	if target.BaselineObjectKey != nil {
		d, res, err := buildDifference(ctx, db, scope.CimProfile, *target.BaselineObjectKey, current.Bytes(), job.CreatedAt)
		if err != nil {
			markFailed(ctx, db, job.ID, err.Error())
			return
		}
		artifact, diff, isFullModel = d, res, false
	}

	// 3. Upload the artifact as the job object, then mark complete.
	spec := formats["cim"]
	key := objectKey(job.ID, spec.ext)
	size, err := store.Upload(ctx, key, bytes.NewReader(artifact), spec.contentType)
	if err != nil {
		markFailed(ctx, db, job.ID, fmt.Sprintf("uploading export: %v", err))
		return
	}
	if err := completeExport(ctx, db, job.ID, key, size, count); err != nil {
		logx.From(ctx).Error("uploaded but marking complete failed", "err", err)
		return
	}

	// Push an export.ready webhook (Slice 2) so a subscribed external consumer is
	// told to pull. Best-effort: the artifact is already delivered, so a notify
	// failure only costs the push, not the export.
	emitExportReady(ctx, db, store, job, target, isFullModel, count, diff, size)

	// 4. Advance the baseline -- only now the diff is delivered. A failure here
	//    leaves the old baseline, so the next run re-emits the SAME delta (safe to
	//    re-apply) rather than skipping it.
	baselineKey := fmt.Sprintf("baselines/%s/model-%s.xml", target.ID, job.ID)
	if _, err := baselineStore.Upload(ctx, baselineKey, bytes.NewReader(current.Bytes()), spec.contentType); err != nil {
		logx.From(ctx).Error("sync export delivered but baseline not advanced (upload)", "job", job.ID, "err", err)
		return
	}
	if err := advanceBaseline(ctx, db, target.ID, baselineKey, extractModelMRID(current.Bytes()), count, job.ID); err != nil {
		logx.From(ctx).Error("sync export delivered but baseline not advanced (db)", "job", job.ID, "err", err)
	}
}

// buildDifference fetches the baseline model, parses both it and the current model
// into statement sets, diffs them, and serialises a CIM difference model (also
// returning the add/remove counts for the completion notification).
func buildDifference(ctx context.Context, db *bun.DB, profileCode, baselineKey string, current []byte, generated time.Time) ([]byte, DiffResult, error) {
	rc, err := baselineStore.Open(ctx, baselineKey)
	if err != nil {
		return nil, DiffResult{}, fmt.Errorf("opening baseline: %v", err)
	}
	baseStmts, err := parseStatements(rc)
	rc.Close()
	if err != nil {
		return nil, DiffResult{}, fmt.Errorf("parsing baseline: %v", err)
	}
	curStmts, err := parseStatements(bytes.NewReader(current))
	if err != nil {
		return nil, DiffResult{}, fmt.Errorf("parsing current model: %v", err)
	}
	mapping, err := metadata.LoadCIMMapping(ctx, db, profileCode)
	if err != nil {
		return nil, DiffResult{}, fmt.Errorf("loading CIM profile: %v", err)
	}
	fwd, rev := diffStatements(baseStmts, curStmts)
	var buf bytes.Buffer
	res, err := writeDifferenceModel(&buf, fwd, rev, mapping.Profile, generated)
	if err != nil {
		return nil, DiffResult{}, fmt.Errorf("serialising difference model: %v", err)
	}
	return buf.Bytes(), res, nil
}

// exportReadyPayload is the webhook `data` for an export.ready event (Slice 2).
type exportReadyPayload struct {
	JobID          string `json:"job_id"`
	SyncTargetID   string `json:"sync_target_id"`
	SyncTargetName string `json:"sync_target_name"`
	Format         string `json:"format"`
	Kind           string `json:"kind"` // "full" (initial load) | "difference"
	FeatureCount   int    `json:"feature_count"`
	ForwardCount   *int   `json:"forward_count,omitempty"` // difference only
	ReverseCount   *int   `json:"reverse_count,omitempty"`
	ByteSize       int64  `json:"byte_size"`
	// DownloadURL is a time-limited presigned URL an external consumer can GET with
	// no VoltEdge session. It expires (EXPORT_URL_TTL); job_id is the stable ref.
	DownloadURL string `json:"download_url,omitempty"`
}

// emitExportReady records an export.ready outbound event for a finished sync-target
// export. Best-effort (the artifact is already delivered): a failure only loses the
// push. The acting user isn't on the detached worker's ctx, so the event has no
// actor -- correct for a system-completed export.
func emitExportReady(ctx context.Context, db *bun.DB, store *ObjectStore, job *ExportJob, target *SyncTarget, isFull bool, featureCount int, diff DiffResult, size int64) {
	kind := "difference"
	if isFull {
		kind = "full"
	}
	p := exportReadyPayload{
		JobID:          job.ID,
		SyncTargetID:   target.ID,
		SyncTargetName: target.Name,
		Format:         "cim",
		Kind:           kind,
		FeatureCount:   featureCount,
		ByteSize:       size,
	}
	if !isFull {
		fwd, rev := diff.Forward, diff.Reverse
		p.ForwardCount, p.ReverseCount = &fwd, &rev
	}
	if url, err := store.PresignGet(ctx, objectKey(job.ID, formats["cim"].ext), target.Name+".xml"); err == nil {
		p.DownloadURL = url
	}
	if err := webhookout.Emit(ctx, db, webhookout.EventExportReady, webhookout.KindExport, job.ID, p); err != nil {
		logx.From(ctx).Warn("export.ready webhook not emitted", "job", job.ID, "err", err)
	}
}

// extractModelMRID pulls the md:FullModel identity out of a serialised model, for
// recording which model version a baseline holds.
func extractModelMRID(doc []byte) string {
	const marker = `<md:FullModel rdf:about="urn:uuid:`
	_, after, ok := bytes.Cut(doc, []byte(marker))
	if !ok {
		return ""
	}
	id, _, ok := bytes.Cut(after, []byte(`"`))
	if !ok {
		return ""
	}
	return string(id)
}

// runExport reads the scope's feature classes and encodes them to w in the
// requested format, returning the feature count. This is the format-agnostic
// core shared by every queue driver (cmd/exportworker via RunWorker) -- the same
// gather + encode the synchronous handler used to do inline, minus the HTTP.
//
// Every read runs inside one REPEATABLE READ, read-only snapshot transaction
// (Phase 17 Slice 3c): a multi-class export can take minutes, and without a
// snapshot a concurrent edit landing mid-read could tear the file -- one class
// seen before an edit, another after, an inconsistent cross-class picture the
// consumer can't detect. The snapshot pins every read to the same point in time.
//
// There are two encode paths (Phase 17 Slice 3d), dispatched on the format:
//   - Streaming (KMZ/DXF, spec.writeStream): the feature read is a DB cursor
//     that stays open across the encode, so the whole gather+encode runs INSIDE
//     the snapshot tx and the features never materialize. The tx is therefore
//     held across the (slow, upload-bound) writer -- the cost of an uncapped,
//     constant-memory export.
//   - Materialized (shapefile + CIM): the layers are fully read into memory
//     (capped at maxExportFeatures) so the tx closes before the encode; these
//     encoders need a full pass (shapefile header offsets, CIM whole-graph
//     topology) and can't stream.
// generated stamps the CIM md:FullModel's Model.created (the job's created_at --
// a real, meaningful timestamp, but stable per job rather than a fresh clock read
// inside the encoder). The model IDENTITY mRID is content-derived, not from this.
// Ignored by the geometry formats.
func runExport(ctx context.Context, db *bun.DB, scope ExportScope, w io.Writer, generated time.Time) (int, error) {
	bbox, err := parseBBox(scope.Bbox)
	if err != nil {
		return 0, err
	}

	spec := formats[scope.Format]

	// Export reprojection: only a format validated as reprojectable at enqueue may
	// carry a target SRID into the read path. Re-checking here (rather than
	// trusting the frozen scope) keeps KMZ at its spec-mandated 4326 and CIM at
	// 4326-until-its-slice even if a scope were hand-crafted, so the transform can
	// never leak into a format that must not carry it.
	readSRID := scope.TargetSRID
	if !reprojectableFormats[scope.Format] {
		readSRID = nil
	}

	// Streaming path: gather + encode both inside the snapshot tx, so the cursor
	// stays live for the whole encode.
	if spec.writeStream != nil {
		count := 0
		txErr := db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true},
			func(ctx context.Context, tx bun.Tx) error {
				layers, status, msg := gatherStreamLayers(ctx, tx, scope.FeatureClasses, bbox, readSRID, scope.TerritoryIDs, scope.DraftAuthors)
				if status != 0 {
					return fmt.Errorf("%s", msg)
				}
				n, err := spec.writeStream(w, "VoltEdge export", layers)
				count = n
				return err
			})
		return count, txErr
	}

	// Materialized path (shapefile, CIM): read everything under the snapshot,
	// then encode after the tx closes.
	var (
		layers  []Layer
		mapping *metadata.CIMMapping
		conns   []connRow
		contain map[string]string // child feature id -> container feature id (Phase 47 Slice 4)
		specs   map[string]*metadata.MaterialSpec
	)
	err = db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true},
		func(ctx context.Context, tx bun.Tx) error {
			gathered, status, msg := gatherLayers(ctx, tx, scope.FeatureClasses, bbox, readSRID, scope.TerritoryIDs, scope.DraftAuthors)
			if status != 0 {
				return fmt.Errorf("%s", msg)
			}
			layers = gathered

			// CIM's extra inputs (declarative mapping, connectivity, material
			// specs) are read under the same snapshot as the features they
			// describe. The geometry formats need none of this.
			if scope.Format == "cim" {
				if mapping, err = metadata.LoadCIMMapping(ctx, tx, scope.CimProfile); err != nil {
					return fmt.Errorf("CIM export is not configured: %w", err)
				}
				if conns, err = loadConnectivity(ctx, tx, layers); err != nil {
					return err
				}
				if contain, err = loadContainment(ctx, tx, layers); err != nil {
					return err
				}
				if specs, err = loadMaterialSpecs(ctx, tx); err != nil {
					return err
				}
			}
			return nil
		})
	if err != nil {
		return 0, err
	}

	count := 0
	for _, l := range layers {
		count += len(l.Features)
	}

	if spec.write != nil {
		// Shapefile carries its CRS in a .prj sidecar: with a reprojection target
		// that is the target's WKT, otherwise the canonical WGS84 (the stored SRID).
		prj, err := crsWKT(ctx, db, readSRID)
		if err != nil {
			return 0, err
		}
		return count, spec.write(w, "VoltEdge export", layers, prj)
	}
	// CIM: the nil-encoder format, encoded with its declarative mapping + topology.
	// readSRID (nil unless a reprojection target was requested) tells the encoder
	// which CRS its GL PositionPoints are now in, so it declares it on the
	// CoordinateSystem's crsUrn instead of the profile's stored default.
	stats, err := writeCIM(w, "VoltEdge export", layers, mapping, conns, contain, specs, readSRID, generated)
	// Slice 5 trigger signal (docs/phase-17-large-export.md): the persisted-topology
	// optimization was designed but not built because no export has hit the
	// union-find memory ceiling. This line is how we find out when one does --
	// every CIM export records its topology size, so an operator (or an alert on
	// parent_bytes) sees the union-find footprint climb toward the ~240MB
	// projection long before it OOMs, which is the concrete signal to build Slice 5.
	logx.From(ctx).Info("CIM topology",
		"features", count, "conns", stats.Conns, "terminals", stats.Endpoints,
		"nodes", stats.Nodes, "parent_bytes", stats.ParentBytes)
	// Phase 55 Slice 2 warn-on-drop: any class with features but no
	// cim_class_mappings row was silently excluded. Log it (WARN) so a dropped
	// class is visible in the export logs, not lost -- the admin editor's
	// coverage view (GET /admin/cim-coverage) is the interactive counterpart.
	if dropped := formatUnmapped(stats.Unmapped); dropped != "" {
		logx.From(ctx).Warn("CIM unmapped classes dropped from output (no cim_class_mappings row)", "classes", dropped)
	}
	return count, err
}
