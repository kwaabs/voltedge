package export

// Async export job model + queue operations (Phase 17 Slice 2,
// docs/phase-17-large-export.md). Every export is now a row in ops.export_jobs:
// POST /export enqueues one; a worker claims it with FOR UPDATE SKIP LOCKED,
// runs the encoder, and marks it succeeded/failed. These queue primitives are
// deliberately worker-agnostic -- the throwaway in-process runner (runner.go)
// uses them today, and the real cmd/exportworker (Slice 3) will use the exact
// same ones from its own process.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// ExportJob is one row of ops.export_jobs. The pointer fields are the columns
// that stay NULL until the job finishes (result pointer, error, lease, timing).
type ExportJob struct {
	bun.BaseModel `bun:"table:ops.export_jobs,alias:export_jobs"`

	ID          string          `bun:"id,pk,nullzero"`
	Status      string          `bun:"status"`
	Format      string          `bun:"format"`
	RequestedBy string          `bun:"requested_by"`
	Scope       json.RawMessage `bun:"scope,type:jsonb"`

	ObjectKey    *string `bun:"object_key"`
	ByteSize     *int64  `bun:"byte_size"`
	FeatureCount *int    `bun:"feature_count"`
	Error        *string `bun:"error"`

	Attempts       int        `bun:"attempts"`
	WorkerID       *string    `bun:"worker_id"`
	LeaseExpiresAt *time.Time `bun:"lease_expires_at"`

	CreatedAt  time.Time  `bun:"created_at,nullzero"`
	StartedAt  *time.Time `bun:"started_at"`
	FinishedAt *time.Time `bun:"finished_at"`
}

// ExportScope is the resolved request snapshot stored in the scope jsonb -- the
// authz snapshot captured at enqueue (the caller's already-permission-checked
// intent). The worker exports exactly this and never re-derives the caller's
// rights. TerritoryIDs is tri-state, matching the read path: nil = no territory
// filter, empty (non-nil) = filter to nothing, populated = the expanded
// descendant set. json round-trips this faithfully (nil->null->nil, []->[]).
type ExportScope struct {
	Format         string    `json:"format"`
	FeatureClasses []string  `json:"feature_classes"`
	Bbox           []float64 `json:"bbox,omitempty"`
	TerritoryIDs   []string  `json:"territory_ids"`
	CimProfile     string    `json:"cim_profile,omitempty"`

	// TargetSRID (export reprojection engine) is the SRID the OUTPUT geometry is
	// reprojected to via PostGIS ST_Transform. nil = no reprojection: emit the
	// stored SRID (4326), the historical behaviour. Validated against
	// spatial_ref_sys at enqueue and permitted only for formats that can carry a
	// CRS (shapefile/DXF; CIM lands later) -- never KMZ, which is spec-locked to
	// WGS84. Rides in the scope jsonb, so no schema change was needed to add it.
	TargetSRID *int `json:"target_srid,omitempty"`

	// DraftAuthors is the Private-drafts (Phase 61) author allow-list, resolved at
	// enqueue from the requester's session (their own id plus their subordinates)
	// and frozen into the scope for the same detached-worker reason as
	// TerritoryIDs: the export runner holds no SessionUser, so it can't re-derive
	// which unpublished drafts the requester may see. nil = filter doesn't apply
	// (rollout gate off, or the requester is unscoped / break-glass): export every
	// feature as before. When non-nil it is always non-empty (contains at least the
	// requester's own id): only public features plus features authored by someone
	// in this set are exported.
	DraftAuthors []string `json:"draft_authors"`
}

// enqueueExport inserts a queued job for requestedBy and returns its id.
func enqueueExport(ctx context.Context, db bun.IDB, requestedBy string, scope ExportScope) (string, error) {
	raw, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	job := &ExportJob{
		Status:      "queued",
		Format:      scope.Format,
		RequestedBy: requestedBy,
		Scope:       raw,
	}
	if _, err := db.NewInsert().Model(job).Returning("id").Exec(ctx); err != nil {
		return "", err
	}
	return job.ID, nil
}

// claimExport atomically claims the oldest queued (or lease-expired running)
// job for workerID, flipping it to running and stamping a fresh lease. It
// returns (nil, nil) when the queue holds no claimable job. FOR UPDATE SKIP
// LOCKED lets any number of workers/goroutines claim distinct jobs with no
// contention; the lease-expiry arm reclaims a job whose worker died mid-run,
// and attempts++ caps that requeue loop.
func claimExport(ctx context.Context, db bun.IDB, workerID string, lease time.Duration) (*ExportJob, error) {
	const q = `
UPDATE ops.export_jobs SET
    status = 'running',
    worker_id = ?,
    started_at = now(),
    lease_expires_at = now() + (? * interval '1 second'),
    attempts = attempts + 1
WHERE id = (
    SELECT id FROM ops.export_jobs
    WHERE status = 'queued'
       OR (status = 'running' AND lease_expires_at < now())
    ORDER BY created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *`

	job := new(ExportJob)
	if err := db.NewRaw(q, workerID, int(lease.Seconds())).Scan(ctx, job); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // empty queue -- not an error
		}
		return nil, err
	}
	return job, nil
}

// heartbeatExport extends a running job's lease, returning whether this worker
// still owns it. The WHERE clause guards on both worker_id and status: a false
// return means the job was reclaimed by another worker (its lease lapsed) or
// moved out of 'running' (completed/cancelled) underneath us, and the caller
// should stop working it. Called on a timer well inside the lease window so a
// long export is never mistaken for a dead worker.
func heartbeatExport(ctx context.Context, db bun.IDB, id, workerID string, lease time.Duration) (bool, error) {
	res, err := db.NewUpdate().Model((*ExportJob)(nil)).
		Set("lease_expires_at = now() + (? * interval '1 second')", int(lease.Seconds())).
		Where("id = ?", id).
		Where("worker_id = ?", workerID).
		Where("status = 'running'").
		Exec(ctx)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// completeExport marks a claimed job succeeded with its result pointer. The
// status = 'running' guard means a job cancelled out from under the worker (see
// cancelExport) is never resurrected to 'succeeded' by a late-finishing encode
// -- the WHERE simply matches no row, and the already-uploaded artifact is left
// to the retention sweep to reap as an orphan.
func completeExport(ctx context.Context, db bun.IDB, id, objectKey string, byteSize int64, featureCount int) error {
	_, err := db.NewUpdate().Model((*ExportJob)(nil)).
		Set("status = 'succeeded'").
		Set("object_key = ?", objectKey).
		Set("byte_size = ?", byteSize).
		Set("feature_count = ?", featureCount).
		Set("finished_at = now()").
		Set("lease_expires_at = NULL").
		Where("id = ?", id).
		Where("status = 'running'").
		Exec(ctx)
	return err
}

// failExport marks a claimed job failed with a human-readable message. Guarded
// on status = 'running' for the same reason as completeExport: a job cancelled
// mid-encode (whose encode then errors on the cancelled context) must stay
// 'cancelled', not be overwritten with 'failed'.
func failExport(ctx context.Context, db bun.IDB, id, msg string) error {
	_, err := db.NewUpdate().Model((*ExportJob)(nil)).
		Set("status = 'failed'").
		Set("error = ?", msg).
		Set("finished_at = now()").
		Set("lease_expires_at = NULL").
		Where("id = ?", id).
		Where("status = 'running'").
		Exec(ctx)
	return err
}

// cancelExport cancels the caller's own queued or running job, returning whether
// it actually flipped a row. A queued job is cancelled outright (no worker is
// involved yet). A running job's status change is noticed by its worker's next
// heartbeat -- heartbeatExport guards on status = 'running', so it returns
// "not ours" and the worker aborts the in-flight encode/upload. A job that has
// already reached a terminal state, or isn't the caller's, matches no row and
// returns false (the handler turns that into a 409/404). Owner-scoped here so a
// caller can never cancel another user's export.
func cancelExport(ctx context.Context, db bun.IDB, id, requestedBy string) (bool, error) {
	res, err := db.NewUpdate().Model((*ExportJob)(nil)).
		Set("status = 'cancelled'").
		Set("finished_at = now()").
		Set("lease_expires_at = NULL").
		Where("id = ?", id).
		Where("requested_by = ?", requestedBy).
		Where("status IN ('queued', 'running')").
		Exec(ctx)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// getExportJob loads one job, scoped to its owner so a caller can never poll or
// download another user's export. Returns sql.ErrNoRows when the id doesn't
// exist OR belongs to someone else (indistinguishable on purpose).
func getExportJob(ctx context.Context, db bun.IDB, id, requestedBy string) (*ExportJob, error) {
	job := new(ExportJob)
	err := db.NewSelect().Model(job).
		Where("id = ?", id).
		Where("requested_by = ?", requestedBy).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return job, nil
}

// listExportJobs returns a caller's most recent jobs, newest first, for the
// history panel.
func listExportJobs(ctx context.Context, db bun.IDB, requestedBy string, limit int) ([]ExportJob, error) {
	var jobs []ExportJob
	err := db.NewSelect().Model(&jobs).
		Where("requested_by = ?", requestedBy).
		OrderExpr("created_at DESC").
		Limit(limit).
		Scan(ctx)
	return jobs, err
}
