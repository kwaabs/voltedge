package fieldpack

// Field-pack build job model + queue operations (Phase 21 Slice 1,
// docs/phase-21-field-tool.md). A field-tool "region pack" is built asynchronously:
// POST /packs enqueues a row in ops.field_packs; the cmd/fieldsync worker claims
// it with FOR UPDATE SKIP LOCKED, builds the two artifacts (an MBTiles display
// tileset + a SQLite edit-truth data pack), uploads both to RustFS, and marks it
// succeeded/failed.
//
// This is a deliberate structural twin of the Phase 17 export queue
// (internal/export/job.go): the same status / lease / heartbeat / attempts
// machinery, so the worker loop is the same shape. The one shape difference is the
// result -- a pack produces TWO artifacts, so the row carries two object keys and
// two byte sizes, plus snapshot_at (the point-in-time the pack reflects, for a
// future delta refresh).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// FieldPack is one row of ops.field_packs. The pointer fields stay NULL until the
// build finishes (the two artifact pointers, sizes, feature count, snapshot, error,
// lease, and timing).
type FieldPack struct {
	bun.BaseModel `bun:"table:ops.field_packs,alias:field_packs"`

	ID          string          `bun:"id,pk,nullzero"`
	Status      string          `bun:"status"`
	RequestedBy string          `bun:"requested_by"`
	Scope       json.RawMessage `bun:"scope,type:jsonb"`

	TilesObjectKey *string `bun:"tiles_object_key"`
	DataObjectKey  *string `bun:"data_object_key"`
	TilesBytes     *int64  `bun:"tiles_bytes"`
	DataBytes      *int64  `bun:"data_bytes"`

	FeatureCount *int       `bun:"feature_count"`
	// AttachmentCount is how many image attachments the pack carried (migration
	// 0042). NULL for packs built before that column existed; the true count still
	// rides those packs' own manifests.
	AttachmentCount *int       `bun:"attachment_count"`
	SnapshotAt      *time.Time `bun:"snapshot_at"`
	Error           *string    `bun:"error"`

	Attempts       int        `bun:"attempts"`
	WorkerID       *string    `bun:"worker_id"`
	LeaseExpiresAt *time.Time `bun:"lease_expires_at"`

	// DisabledAt is an admin "hide from the device" flag (migration 0040): a
	// disabled pack is filtered out of listPacks so it stops crowding the field
	// tech's tablet, but its artifacts stay in RustFS so it can be re-enabled.
	// Set/cleared only by the admin:field-packs endpoints (admin.go); NULL =
	// visible.
	DisabledAt *time.Time `bun:"disabled_at"`

	CreatedAt  time.Time  `bun:"created_at,nullzero"`
	StartedAt  *time.Time `bun:"started_at"`
	FinishedAt *time.Time `bun:"finished_at"`
}

// PackScope is the resolved build request stored in the scope jsonb -- the authz
// snapshot captured at enqueue (the caller's already-permission-checked intent).
// The worker builds exactly this and never re-derives the caller's rights.
// TerritoryIDs is tri-state, matching the export/read path: nil = no territory
// filter, empty (non-nil) = filter to nothing, populated = the expanded descendant
// set. Bbox is [minX, minY, maxX, maxY] in EPSG:4326 (a field pack is always
// bbox-bounded -- there is no whole-dataset field pack).
type PackScope struct {
	Bbox           []float64 `json:"bbox"`
	FeatureClasses []string  `json:"feature_classes"`
	TerritoryIDs   []string  `json:"territory_ids"`

	// IncludeConnectivity / IncludeRelationships are the graph-inclusion decision,
	// resolved at enqueue from the requester's RBAC (connectivity:read /
	// relationships:read) and frozen into the scope. The build worker runs
	// detached -- it holds no SessionUser to re-check -- so it must honor exactly
	// what the handler authorized. A feature-only viewer (features:read but not
	// connectivity:read) gets a pack with no connectivity table rather than a
	// privilege leak; the pack is still fully usable for view + attribute/move edit.
	IncludeConnectivity  bool `json:"include_connectivity"`
	IncludeRelationships bool `json:"include_relationships"`

	// IncludeWAM is the work-&-asset-management inclusion decision (Phase 59
	// offline WAM), resolved at enqueue from the requester's wam:read and frozen
	// into the scope for the same reason as the graph flags: the detached build
	// worker holds no SessionUser to re-check. When set, the pack carries the open
	// defects on the packed assets, the requester's own open work orders (their
	// field "work board"), and the active inspection templates -- so a
	// field-technician works their jobs, logs findings, and runs checklists
	// offline. A field-surveyor (features:read but not wam:read) gets no WAM
	// tables rather than a privilege leak.
	IncludeWAM bool `json:"include_wam"`

	// IncludeFaults is the faults-module inclusion decision (offline faults),
	// resolved at enqueue from the requester's faults:read and frozen into the
	// scope for the same detached-worker reason as IncludeWAM. When set, the pack
	// carries the open fault events on the packed assets so a fault-response crew
	// sees live faults offline, raises new ones, marks them cleared, and books
	// defects from them -- captures ride back through the faults_* sync ops. A
	// crew without faults:read gets no fault tables rather than a privilege leak.
	// Orthogonal to IncludeWAM: a pack can carry either, both, or neither, which
	// is how one download serves a survey-only, a works-only, a fault-only, or an
	// all-rounder crew (the "permitted modules" set is exactly these include flags).
	IncludeFaults bool `json:"include_faults"`

	// IncludeTags is the asset-tagging inclusion decision (asset-tagging module S5),
	// resolved at enqueue from the requester's tags:read and frozen into the scope
	// for the same detached-worker reason as the other flags. When set, the pack
	// carries every resolvable tag code (active + superseded) on the packed assets,
	// so a field worker scans a QR/Code128/typed tag and the device resolves it to a
	// feature with no signal. tags:read is viewer-tier (any field tech scans), so
	// this is set for virtually every pack; a caller without it gets no tag index
	// and the device falls back to online-only resolution.
	IncludeTags bool `json:"include_tags"`

	// DraftAuthors is the Private-drafts (Phase 61) author allow-list, resolved at
	// enqueue from the requester's session (their own id plus their subordinates)
	// and frozen into the scope for the same detached-worker reason as the flags
	// above: the build worker holds no SessionUser, so drafts.VisibleAuthors --
	// which reads the session off the context -- would see NO session and bake
	// every private draft into the pack, leaking other users' unpublished work.
	// nil means the draft filter does not apply (rollout gate off, or the requester
	// is unscoped / break-glass): the pack carries every feature as before. When
	// non-nil it is always non-empty (it contains at least the requester's own id),
	// and only public features (published, or ever-published) plus features
	// authored by someone in this set are packed.
	DraftAuthors []string `json:"draft_authors"`
}

// enqueuePack inserts a queued build for requestedBy and returns its id.
func enqueuePack(ctx context.Context, db bun.IDB, requestedBy string, scope PackScope) (string, error) {
	raw, err := json.Marshal(scope)
	if err != nil {
		return "", err
	}
	pack := &FieldPack{
		Status:      "queued",
		RequestedBy: requestedBy,
		Scope:       raw,
	}
	if _, err := db.NewInsert().Model(pack).Returning("id").Exec(ctx); err != nil {
		return "", err
	}
	return pack.ID, nil
}

// claimPack atomically claims the oldest queued (or lease-expired running) build
// for workerID, flipping it to running and stamping a fresh lease. Returns
// (nil, nil) when the queue holds no claimable job. Identical to the export
// queue's claim: FOR UPDATE SKIP LOCKED lets any number of workers claim distinct
// jobs with no contention, the lease-expiry arm reclaims a crashed worker's job,
// and attempts++ caps the requeue loop.
func claimPack(ctx context.Context, db bun.IDB, workerID string, lease time.Duration) (*FieldPack, error) {
	const q = `
UPDATE ops.field_packs SET
    status = 'running',
    worker_id = ?,
    started_at = now(),
    lease_expires_at = now() + (? * interval '1 second'),
    attempts = attempts + 1
WHERE id = (
    SELECT id FROM ops.field_packs
    WHERE status = 'queued'
       OR (status = 'running' AND lease_expires_at < now())
    ORDER BY created_at
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *`

	pack := new(FieldPack)
	if err := db.NewRaw(q, workerID, int(lease.Seconds())).Scan(ctx, pack); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // empty queue -- not an error
		}
		return nil, err
	}
	return pack, nil
}

// heartbeatPack extends a running build's lease, returning whether this worker
// still owns it. A false return means the job was reclaimed (its lease lapsed) or
// moved out of 'running' (completed/cancelled) underneath us, and the caller
// should stop. Called on a timer well inside the lease window.
func heartbeatPack(ctx context.Context, db bun.IDB, id, workerID string, lease time.Duration) (bool, error) {
	res, err := db.NewUpdate().Model((*FieldPack)(nil)).
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

// completePack marks a claimed build succeeded with both artifact pointers, their
// sizes, the feature count, and the snapshot instant. The status = 'running' guard
// means a build cancelled out from under the worker is never resurrected to
// 'succeeded' by a late-finishing build -- the WHERE simply matches no row, and
// the already-uploaded artifacts are left to a retention sweep to reap as orphans.
func completePack(ctx context.Context, db bun.IDB, id, tilesKey, dataKey string, tilesBytes, dataBytes int64, featureCount, attachmentCount int, snapshotAt time.Time) error {
	_, err := db.NewUpdate().Model((*FieldPack)(nil)).
		Set("status = 'succeeded'").
		Set("tiles_object_key = ?", tilesKey).
		Set("data_object_key = ?", dataKey).
		Set("tiles_bytes = ?", tilesBytes).
		Set("data_bytes = ?", dataBytes).
		Set("feature_count = ?", featureCount).
		Set("attachment_count = ?", attachmentCount).
		Set("snapshot_at = ?", snapshotAt).
		Set("finished_at = now()").
		Set("lease_expires_at = NULL").
		Where("id = ?", id).
		Where("status = 'running'").
		Exec(ctx)
	return err
}

// failPack marks a claimed build failed with a human-readable message. Guarded on
// status = 'running' for the same reason as completePack: a build cancelled
// mid-run (whose build then errors on the cancelled context) must stay
// 'cancelled', not be overwritten with 'failed'.
func failPack(ctx context.Context, db bun.IDB, id, msg string) error {
	_, err := db.NewUpdate().Model((*FieldPack)(nil)).
		Set("status = 'failed'").
		Set("error = ?", msg).
		Set("finished_at = now()").
		Set("lease_expires_at = NULL").
		Where("id = ?", id).
		Where("status = 'running'").
		Exec(ctx)
	return err
}

// cancelPack cancels the caller's own queued or running build, returning whether
// it actually flipped a row. A queued build is cancelled outright; a running one's
// status change is noticed by its worker's next heartbeat (which then aborts the
// in-flight build/upload). A build already in a terminal state, or not the
// caller's, matches no row and returns false (the handler turns that into 409/404).
// Owner-scoped so a caller can never cancel another user's pack.
func cancelPack(ctx context.Context, db bun.IDB, id, requestedBy string) (bool, error) {
	res, err := db.NewUpdate().Model((*FieldPack)(nil)).
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

// getPack loads one build, scoped to its owner so a caller can never poll or
// download another user's pack. Returns sql.ErrNoRows when the id doesn't exist OR
// belongs to someone else (indistinguishable on purpose).
func getPack(ctx context.Context, db bun.IDB, id, requestedBy string) (*FieldPack, error) {
	pack := new(FieldPack)
	err := db.NewSelect().Model(pack).
		Where("id = ?", id).
		Where("requested_by = ?", requestedBy).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return pack, nil
}

// listPacks returns a caller's most recent builds, newest first, for the device's
// pack history. Admin-disabled packs (disabled_at set, migration 0040) are
// filtered out so a pack an administrator hid stops crowding the field tech's
// tablet -- the pack's artifacts remain in RustFS and re-enabling it makes it
// reappear here.
func listPacks(ctx context.Context, db bun.IDB, requestedBy string, limit int) ([]FieldPack, error) {
	var packs []FieldPack
	err := db.NewSelect().Model(&packs).
		Where("requested_by = ?", requestedBy).
		Where("disabled_at IS NULL").
		OrderExpr("created_at DESC").
		Limit(limit).
		Scan(ctx)
	return packs, err
}
