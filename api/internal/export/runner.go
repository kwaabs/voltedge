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
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/kwaabs/voltedge/api/internal/metadata"
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

	key := objectKey(job.ID, spec.ext)

	pr, pw := io.Pipe()
	done := make(chan encodeResult, 1)
	go func() {
		n, err := runExport(ctx, db, scope, pw)
		// Closing with the encode error propagates it to the upload's Read,
		// aborting the multipart upload so no partial object is left behind.
		pw.CloseWithError(err)
		done <- encodeResult{count: n, err: err}
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
		log.Printf("export: job %s uploaded but marking complete failed: %v", job.ID, err)
	}
}

// objectKey is the RustFS key for a job's artifact: namespaced by job id so
// keys never collide and a job maps to exactly one object.
func objectKey(jobID, ext string) string {
	return "exports/" + jobID + "/voltedge-export." + ext
}

// markFailed records a job failure, logging if even that write fails.
func markFailed(ctx context.Context, db *bun.DB, id, msg string) {
	if err := failExport(ctx, db, id, msg); err != nil {
		log.Printf("export: job %s failed (%s) and marking failed also failed: %v", id, msg, err)
	}
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
func runExport(ctx context.Context, db *bun.DB, scope ExportScope, w io.Writer) (int, error) {
	bbox, err := parseBBox(scope.Bbox)
	if err != nil {
		return 0, err
	}

	spec := formats[scope.Format]

	// Streaming path: gather + encode both inside the snapshot tx, so the cursor
	// stays live for the whole encode.
	if spec.writeStream != nil {
		count := 0
		txErr := db.RunInTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true},
			func(ctx context.Context, tx bun.Tx) error {
				layers, status, msg := gatherStreamLayers(ctx, tx, scope.FeatureClasses, bbox, scope.TerritoryIDs)
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
			gathered, status, msg := gatherLayers(ctx, tx, scope.FeatureClasses, bbox, scope.TerritoryIDs)
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
		return count, spec.write(w, "VoltEdge export", layers)
	}
	// CIM: the nil-encoder format, encoded with its declarative mapping + topology.
	stats, err := writeCIM(w, "VoltEdge export", layers, mapping, conns, contain, specs, time.Now())
	// Slice 5 trigger signal (docs/phase-17-large-export.md): the persisted-topology
	// optimization was designed but not built because no export has hit the
	// union-find memory ceiling. This line is how we find out when one does --
	// every CIM export records its topology size, so an operator (or an alert on
	// parent_bytes) sees the union-find footprint climb toward the ~240MB
	// projection long before it OOMs, which is the concrete signal to build Slice 5.
	log.Printf("export: CIM topology features=%d conns=%d terminals=%d nodes=%d parent_bytes=%d",
		count, stats.Conns, stats.Endpoints, stats.Nodes, stats.ParentBytes)
	// Phase 55 Slice 2 warn-on-drop: any class with features but no
	// cim_class_mappings row was silently excluded. Log it (WARN) so a dropped
	// class is visible in the export logs, not lost -- the admin editor's
	// coverage view (GET /admin/cim-coverage) is the interactive counterpart.
	if dropped := formatUnmapped(stats.Unmapped); dropped != "" {
		log.Printf("export: CIM WARNING unmapped classes dropped from output (no cim_class_mappings row): %s", dropped)
	}
	return count, err
}
