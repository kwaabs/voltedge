package importer

// Async import job model + queue operations (S4, docs/etl-import-engine.md).
// Every committed (or full-file dry) import is a row in ops.import_jobs: POST
// /imports/jobs enqueues one; a worker claims it with FOR UPDATE SKIP LOCKED,
// streams the uploaded NDJSON artifact, and applies each row. These queue
// primitives are the mirror image of ops.export_jobs (api/internal/export/job.go)
// -- same claim/heartbeat/lease shape -- differing only in the per-row tallies an
// import accrues as it streams and the per-row error rows it records.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// ImportJob is one row of ops.import_jobs. The pointer fields are the columns
// that stay NULL until the worker has run (total_rows, error, lease, timing).
type ImportJob struct {
	bun.BaseModel `bun:"table:ops.import_jobs,alias:import_jobs"`

	ID               string          `bun:"id,pk,nullzero"`
	Status           string          `bun:"status"`
	ProfileID        *string         `bun:"profile_id"`
	FeatureClassCode string          `bun:"feature_class_code"`
	RequestedBy      string          `bun:"requested_by"`
	ObjectKey        string          `bun:"object_key"`
	Scope            json.RawMessage `bun:"scope,type:jsonb"`
	DryRun           bool            `bun:"dry_run"`

	TotalRows     *int `bun:"total_rows"`
	InsertedCount int  `bun:"inserted_count"`
	UpdatedCount  int  `bun:"updated_count"`
	SkippedCount  int  `bun:"skipped_count"`
	ErrorCount    int  `bun:"error_count"`

	Error *string `bun:"error"`

	Attempts       int        `bun:"attempts"`
	WorkerID       *string    `bun:"worker_id"`
	LeaseExpiresAt *time.Time `bun:"lease_expires_at"`

	CreatedAt  time.Time  `bun:"created_at,nullzero"`
	StartedAt  *time.Time `bun:"started_at"`
	FinishedAt *time.Time `bun:"finished_at"`
}

// ImportJobError is one per-row failure recorded during a run (validation,
// geometry assembly, or an ambiguous upsert match). The row is skipped and the
// job proceeds; these rows back the run's error report (ops.import_job_errors).
type ImportJobError struct {
	bun.BaseModel `bun:"table:ops.import_job_errors,alias:import_job_errors"`

	ID        string    `bun:"id,pk,nullzero"`
	JobID     string    `bun:"job_id"`
	RowNumber int       `bun:"row_number"`
	Message   string    `bun:"message"`
	CreatedAt time.Time `bun:"created_at,nullzero"`
}

// ImportScope is the resolved run + authz snapshot stored in the scope jsonb.
// ProfileCode names the profile the worker re-resolves (via ResolveProfile) so
// the mapping/validation/upsert logic is the exact code the S3 dry-run shares.
// TerritoryIDs + Unscoped are the caller's Phase 58 spatial write scope captured
// at enqueue, so the worker's CreateFeature/UpdateFeature calls enforce exactly
// the requester's rights and never re-derive them. TerritoryIDs is tri-state,
// matching the read path (nil = no filter, empty = scoped to nothing, populated
// = the expanded set); json round-trips it faithfully.
type ImportScope struct {
	ProfileCode  string   `json:"profile_code"`
	TerritoryIDs []string `json:"territory_ids"`
	Unscoped     bool     `json:"unscoped"`
}

// importTally accumulates the per-row outcomes as the worker streams; it is
// flushed to the job row periodically (progress) and at completion (final).
type importTally struct {
	total    int
	inserted int
	updated  int
	skipped  int
	errored  int
}

// EnqueueImport inserts a queued job and returns its id. profileID may be nil
// (the FK is ON DELETE SET NULL), but the scope's ProfileCode is what the worker
// resolves from -- the id is retained only for display/audit.
func EnqueueImport(ctx context.Context, db bun.IDB, requestedBy string, profileID *string, featureClassCode, objectKey string, dryRun bool, scope ImportScope) (string, error) {
	raw, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	job := &ImportJob{
		Status:           "queued",
		ProfileID:        profileID,
		FeatureClassCode: featureClassCode,
		RequestedBy:      requestedBy,
		ObjectKey:        objectKey,
		Scope:            raw,
		DryRun:           dryRun,
	}
	if _, err := db.NewInsert().Model(job).Returning("id").Exec(ctx); err != nil {
		return "", err
	}
	return job.ID, nil
}

// claimImport atomically claims the oldest queued (or lease-expired running) job
// for workerID, flipping it to running and stamping a fresh lease. Returns
// (nil, nil) when the queue holds nothing claimable. Identical mechanics to
// claimExport: FOR UPDATE SKIP LOCKED for contention-free parallel claims, the
// lease-expiry arm to reclaim a crashed worker's job, attempts++ to cap the loop.
func claimImport(ctx context.Context, db bun.IDB, workerID string, lease time.Duration) (*ImportJob, error) {
	const q = `
UPDATE ops.import_jobs SET
    status = 'running',
    worker_id = ?,
    started_at = now(),
    lease_expires_at = now() + (? * interval '1 second'),
    attempts = attempts + 1
WHERE id = (
    SELECT id FROM ops.import_jobs
    WHERE status = 'queued'
       OR (status = 'running' AND lease_expires_at < now())
    ORDER BY created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *`

	job := new(ImportJob)
	if err := db.NewRaw(q, workerID, int(lease.Seconds())).Scan(ctx, job); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // empty queue -- not an error
		}
		return nil, err
	}
	return job, nil
}

// heartbeatImport extends a running job's lease, returning whether this worker
// still owns it. A false return means the job was reclaimed (lease lapsed) or
// moved out of 'running' underneath us, and the caller should abort. Same guard
// clause as heartbeatExport.
func heartbeatImport(ctx context.Context, db bun.IDB, id, workerID string, lease time.Duration) (bool, error) {
	res, err := db.NewUpdate().Model((*ImportJob)(nil)).
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

// updateImportProgress flushes the running tallies mid-stream so the run is
// observable while it works (the wizard polls these). total_rows carries the
// count seen so far; completeImport writes the final figure. Guarded on
// status = 'running' so a progress write can't resurrect a cancelled job.
func updateImportProgress(ctx context.Context, db bun.IDB, id string, t importTally) error {
	_, err := db.NewUpdate().Model((*ImportJob)(nil)).
		Set("total_rows = ?", t.total).
		Set("inserted_count = ?", t.inserted).
		Set("updated_count = ?", t.updated).
		Set("skipped_count = ?", t.skipped).
		Set("error_count = ?", t.errored).
		Where("id = ?", id).
		Where("status = 'running'").
		Exec(ctx)
	return err
}

// completeImport writes the final tallies and terminal status (succeeded |
// failed), stamping finished_at and clearing the lease. errMsg is the fatal
// job-level message on failure (per-row failures live in ops.import_job_errors,
// not here). Guarded on status = 'running' for the same reason completeExport is:
// a job cancelled out from under the worker must stay cancelled.
func completeImport(ctx context.Context, db bun.IDB, id string, t importTally, status string, errMsg *string) error {
	_, err := db.NewUpdate().Model((*ImportJob)(nil)).
		Set("status = ?", status).
		Set("total_rows = ?", t.total).
		Set("inserted_count = ?", t.inserted).
		Set("updated_count = ?", t.updated).
		Set("skipped_count = ?", t.skipped).
		Set("error_count = ?", t.errored).
		Set("error = ?", errMsg).
		Set("finished_at = now()").
		Set("lease_expires_at = NULL").
		Where("id = ?", id).
		Where("status = 'running'").
		Exec(ctx)
	return err
}

// recordRowError appends one per-row failure to ops.import_job_errors. A failure
// to record is returned to the caller, which logs and continues -- a lost error
// row must never abort the whole run.
func recordRowError(ctx context.Context, db bun.IDB, jobID string, row int, msg string) error {
	_, err := db.NewInsert().
		Model(&ImportJobError{JobID: jobID, RowNumber: row, Message: msg}).
		Exec(ctx)
	return err
}

// getImportJob loads one job scoped to its owner, so a caller can never poll
// another user's run. Returns sql.ErrNoRows when the id doesn't exist OR belongs
// to someone else (indistinguishable on purpose, as getExportJob is).
func getImportJob(ctx context.Context, db bun.IDB, id, requestedBy string) (*ImportJob, error) {
	job := new(ImportJob)
	err := db.NewSelect().Model(job).
		Where("id = ?", id).
		Where("requested_by = ?", requestedBy).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return job, nil
}

// listImportJobs returns a caller's most recent runs, newest first, for the
// history panel.
func listImportJobs(ctx context.Context, db bun.IDB, requestedBy string, limit int) ([]ImportJob, error) {
	var jobs []ImportJob
	err := db.NewSelect().Model(&jobs).
		Where("requested_by = ?", requestedBy).
		OrderExpr("created_at DESC").
		Limit(limit).
		Scan(ctx)
	return jobs, err
}

// listImportJobErrors returns a job's per-row failures in row order (capped), the
// body of its error report.
func listImportJobErrors(ctx context.Context, db bun.IDB, jobID string, limit int) ([]ImportJobError, error) {
	var errs []ImportJobError
	err := db.NewSelect().Model(&errs).
		Where("job_id = ?", jobID).
		OrderExpr("row_number").
		Limit(limit).
		Scan(ctx)
	return errs, err
}
