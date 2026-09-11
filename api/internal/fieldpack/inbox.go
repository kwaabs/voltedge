package fieldpack

// The sync inbox: durable receive + a claimable apply queue (Phase 23 Slice 1,
// docs/phase-23-sync-inbox.md).
//
// ops.sync_inbox is the landing zone that decouples RECEIVE from APPLY. POST /sync
// captures a device's ops here with a single idempotent bulk insert (the ack now
// means "durably captured," not "applied to production"); a worker (syncworker.go)
// later claims each 'received' row FOR UPDATE SKIP LOCKED and commits it through the
// existing applySyncOp path. These queue primitives mirror ops.export_jobs' (job.go)
// almost one-for-one -- claim / heartbeat / terminal-mark -- with two differences the
// sync case forces: (1) capture is a bulk insert of many ops, not a single enqueue,
// and (2) the claim is ordered and serialized *per device* so a connect never applies
// before the create it references.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// SyncInboxRow is one row of ops.sync_inbox -- a single captured device op and its
// apply lifecycle. The pointer fields are the columns that stay NULL until they're
// relevant (the optional device grouping, the result/error, the review trail, the
// lease/timing bookkeeping). recv_seq is scan-only: the DB assigns it (IDENTITY).
type SyncInboxRow struct {
	bun.BaseModel `bun:"table:ops.sync_inbox,alias:sync_inbox"`

	OpID        string  `bun:"op_id,pk"`
	RecvSeq     int64   `bun:"recv_seq,scanonly"`
	SubmittedBy string  `bun:"submitted_by"`
	DeviceID    *string `bun:"device_id"`
	SubmissionID *string `bun:"submission_id"`
	DeviceSeq   *int64  `bun:"device_seq"`

	Kind         string          `bun:"kind"`
	FeatureClass *string         `bun:"feature_class"`
	FeatureID    *string         `bun:"feature_id"`
	Payload      json.RawMessage `bun:"payload,type:jsonb,nullzero"`
	BaseVersion  *string         `bun:"base_version"`

	Status        string          `bun:"status"`
	Error         *string         `bun:"error"`
	ResultFeature json.RawMessage `bun:"result_feature,type:jsonb,nullzero"`

	// ConflictOverride is set when a supervisor resolved this op's 'conflict' by
	// "apply anyway" (Phase 23 Slice 4): the op returns to 'received' and the worker
	// re-applies it skipping the base_version check the human has waived.
	ConflictOverride bool `bun:"conflict_override"`

	ReviewedBy *string    `bun:"reviewed_by"`
	ReviewedAt *time.Time `bun:"reviewed_at"`

	Attempts       int        `bun:"attempts"`
	WorkerID       *string    `bun:"worker_id"`
	LeaseExpiresAt *time.Time `bun:"lease_expires_at"`

	ReceivedAt time.Time  `bun:"received_at,nullzero"`
	StartedAt  *time.Time `bun:"started_at"`
	FinishedAt *time.Time `bun:"finished_at"`
}

// receiveInboxOps durably captures a batch of device ops in one idempotent bulk
// INSERT, returning the set of op_ids that were NEWLY captured (the rest were
// already present -- duplicates the device may re-push safely). op_id is the PK, so
// ON CONFLICT (op_id) DO NOTHING makes a re-pushed batch a no-op. Both outcomes are
// "captured" from the device's view; the newly/duplicate split is only for the
// per-op status the handler echoes back. A pre-SELECT (rather than RETURNING, which
// DO NOTHING makes lossy) identifies the pre-existing ids; the tiny race with a
// concurrent insert of the same op is benign -- the op still ends up captured.
func receiveInboxOps(ctx context.Context, db *bun.DB, submittedBy string, deviceID, submissionID *string, ops []syncOp, policy *SyncPolicySet) (map[string]bool, error) {
	if len(ops) == 0 {
		return map[string]bool{}, nil
	}

	ids := make([]string, len(ops))
	for i, op := range ops {
		ids[i] = op.OpID
	}
	var preexisting []string
	if err := db.NewSelect().Model((*SyncInboxRow)(nil)).
		Column("op_id").
		Where("op_id IN (?)", bun.In(ids)).
		Scan(ctx, &preexisting); err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(preexisting))
	for _, id := range preexisting {
		existing[id] = true
	}

	rows := make([]SyncInboxRow, 0, len(ops))
	for _, op := range ops {
		// The apply policy (Phase 23 Slice 3) decides each op's landing status by
		// feature class: 'received' (the worker applies it) or 'pending_review' (a
		// supervisor must post it first). With an empty ops.sync_policies table this
		// is always 'received', so the default is Slice 1 behaviour.
		row := SyncInboxRow{
			OpID:         op.OpID,
			SubmittedBy:  submittedBy,
			DeviceID:     deviceID,
			SubmissionID: submissionID,
			Kind:         op.Kind,
			Payload:      op.Payload,
			Status:       policy.InitialStatus(op.FeatureClass),
		}
		if op.FeatureClass != "" {
			fc := op.FeatureClass
			row.FeatureClass = &fc
		}
		if op.FeatureID != "" {
			fid := op.FeatureID
			row.FeatureID = &fid
		}
		if op.BaseVersion != "" {
			bv := op.BaseVersion
			row.BaseVersion = &bv
		}
		rows = append(rows, row)
	}
	if _, err := db.NewInsert().Model(&rows).
		On("CONFLICT (op_id) DO NOTHING").
		Exec(ctx); err != nil {
		return nil, err
	}

	newly := make(map[string]bool, len(ops))
	for _, op := range ops {
		if !existing[op.OpID] {
			newly[op.OpID] = true
		}
	}
	return newly, nil
}

// claimInboxOp atomically claims the next applyable op for workerID, flipping it to
// 'applying' and stamping a fresh lease. It returns (nil, nil) when nothing is
// claimable. Beyond export's claim it adds a per-device single-flight guard: the
// NOT EXISTS refuses to hand out an op while an EARLIER op (lower recv_seq) from the
// same (submitted_by, device_id) is still active -- 'received', 'applying' with a
// live lease, 'pending_review' (awaiting a post, Slice 3), or 'conflict' (awaiting a
// supervisor's resolution, Slice 4) -- so a device's create and connect can't apply
// out of order across two workers, a later auto op can't jump ahead of an earlier op
// a supervisor is still reviewing, and a later op can't apply past an earlier op held
// in conflict (which would let a force-apply of that conflict later regress it). An
// earlier op that is terminal (applied/rejected/superseded) does not block: one poison
// op -- or one op a reviewer rejected -- must not stall the whole device (the dependent
// op just fails too). An earlier op whose lease has lapsed doesn't block either -- it's
// itself reclaimable and, being earlier, is picked first by ORDER BY recv_seq. Note the
// claim itself only ever picks a 'received' (or lease-lapsed 'applying') op: a
// 'pending_review' or 'conflict' op is never claimed until a post/resolve flips it to
// 'received', so those states hold an op purely by keeping it out of this WHERE. FOR
// UPDATE SKIP LOCKED lets workers claim distinct devices' ops with no contention.
func claimInboxOp(ctx context.Context, db bun.IDB, workerID string, lease time.Duration) (*SyncInboxRow, error) {
	const q = `
UPDATE ops.sync_inbox SET
    status = 'applying',
    worker_id = ?,
    started_at = now(),
    lease_expires_at = now() + (? * interval '1 second'),
    attempts = attempts + 1
WHERE op_id = (
    SELECT s.op_id FROM ops.sync_inbox s
    WHERE (s.status = 'received'
           OR (s.status = 'applying' AND s.lease_expires_at < now()))
      AND NOT EXISTS (
          SELECT 1 FROM ops.sync_inbox e
          WHERE e.submitted_by = s.submitted_by
            AND e.device_id IS NOT DISTINCT FROM s.device_id
            AND e.recv_seq < s.recv_seq
            AND (e.status = 'received'
                 OR e.status = 'pending_review'
                 OR e.status = 'conflict'
                 OR (e.status = 'applying' AND e.lease_expires_at >= now()))
      )
    ORDER BY s.recv_seq
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *`

	row := new(SyncInboxRow)
	if err := db.NewRaw(q, workerID, int(lease.Seconds())).Scan(ctx, row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // nothing claimable -- not an error
		}
		return nil, err
	}
	return row, nil
}

// heartbeatInboxOp extends a claimed op's lease, returning whether this worker still
// owns it. Guarded on worker_id + status='applying': a false return means the op was
// reclaimed or already finished underneath us, and the caller should stop. Mirror of
// heartbeatExport.
func heartbeatInboxOp(ctx context.Context, db bun.IDB, opID, workerID string, lease time.Duration) (bool, error) {
	res, err := db.NewUpdate().Model((*SyncInboxRow)(nil)).
		Set("lease_expires_at = now() + (? * interval '1 second')", int(lease.Seconds())).
		Where("op_id = ?", opID).
		Where("worker_id = ?", workerID).
		Where("status = 'applying'").
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

// markInboxApplied marks a claimed op applied, recording the server row it produced.
// Guarded on worker_id + status='applying' so a late finish after the op was reclaimed
// (and re-applied by another worker -- safe, apply is idempotent) matches no row and
// is a no-op rather than clobbering the winner's result.
func markInboxApplied(ctx context.Context, db bun.IDB, opID, workerID string, feature map[string]any) error {
	q := db.NewUpdate().Model((*SyncInboxRow)(nil)).
		Set("status = 'applied'").
		Set("error = NULL").
		Set("finished_at = now()").
		Set("lease_expires_at = NULL").
		Where("op_id = ?", opID).
		Where("worker_id = ?", workerID).
		Where("status = 'applying'")
	if feature != nil {
		raw, err := json.Marshal(feature)
		if err != nil {
			return err
		}
		q = q.Set("result_feature = ?", json.RawMessage(raw))
	}
	_, err := q.Exec(ctx)
	return err
}

// markInboxRejected quarantines a claimed op that failed to apply: status='rejected'
// with a human message, and NOT retried. This is the poison-op fix -- a bad op is
// held for triage (Slice 2 surfaces it to the device) instead of retrying forever.
// Same worker_id + status='applying' guard as markInboxApplied.
func markInboxRejected(ctx context.Context, db bun.IDB, opID, workerID, msg string) error {
	_, err := db.NewUpdate().Model((*SyncInboxRow)(nil)).
		Set("status = 'rejected'").
		Set("error = ?", msg).
		Set("finished_at = now()").
		Set("lease_expires_at = NULL").
		Where("op_id = ?", opID).
		Where("worker_id = ?", workerID).
		Where("status = 'applying'").
		Exec(ctx)
	return err
}

// markInboxConflict holds a claimed op whose base_version diverged from the current
// production row (Phase 23 Slice 4): status='conflict' with a human message naming both
// sides. NOT terminal -- it awaits a supervisor's resolve (apply-anyway or keep), so
// worker_id/lease are cleared but finished_at is left NULL. A held conflict blocks the
// device's later ops (claimInboxOp), so resolving it can't regress a newer op. Same
// worker_id + status='applying' guard as markInboxApplied: a late mark after the op was
// reclaimed is a no-op.
func markInboxConflict(ctx context.Context, db bun.IDB, opID, workerID, msg string) error {
	_, err := db.NewUpdate().Model((*SyncInboxRow)(nil)).
		Set("status = 'conflict'").
		Set("error = ?", msg).
		Set("worker_id = NULL").
		Set("lease_expires_at = NULL").
		Where("op_id = ?", opID).
		Where("worker_id = ?", workerID).
		Where("status = 'applying'").
		Exec(ctx)
	return err
}

// markInboxSuperseded terminally retires a claimed op that a LATER op for the same
// feature has obsoleted before it applied (Phase 23 Slice 4): status='superseded',
// terminal, never applied. Used when a conflict is detected but a newer queued op
// targets the same feature -- only the newest edit needs surfacing, so the older one
// steps aside silently (it's not an error the device must act on, unlike 'rejected').
// Same guard as the other terminal marks.
func markInboxSuperseded(ctx context.Context, db bun.IDB, opID, workerID string) error {
	_, err := db.NewUpdate().Model((*SyncInboxRow)(nil)).
		Set("status = 'superseded'").
		Set("finished_at = now()").
		Set("worker_id = NULL").
		Set("lease_expires_at = NULL").
		Where("op_id = ?", opID).
		Where("worker_id = ?", workerID).
		Where("status = 'applying'").
		Exec(ctx)
	return err
}

// hasLaterActiveOpForFeature reports whether a later op (higher recv_seq) from the same
// device targets the same feature and is still active (received/pending_review/conflict/
// applying). When true of an op the worker just found in conflict, that op is superseded
// by the newer edit -- so only the latest edit to a feature ever surfaces as a conflict.
// device_id/feature_class use IS NOT DISTINCT FROM so NULLs group the same way the claim
// guard does.
func hasLaterActiveOpForFeature(ctx context.Context, db bun.IDB, row *SyncInboxRow) (bool, error) {
	return db.NewSelect().Model((*SyncInboxRow)(nil)).
		Where("submitted_by = ?", row.SubmittedBy).
		Where("device_id IS NOT DISTINCT FROM ?", row.DeviceID).
		Where("feature_class IS NOT DISTINCT FROM ?", row.FeatureClass).
		Where("feature_id = ?", row.FeatureID).
		Where("recv_seq > ?", row.RecvSeq).
		Where("status IN ('received', 'pending_review', 'conflict', 'applying')").
		Exists(ctx)
}

// resolveConflictApply resolves a 'conflict' op by "apply anyway": it returns to
// 'received' with conflict_override set, so the drain worker re-claims it (it's the
// earliest active op for the device -- the conflict was blocking the queue) and applies
// it skipping the base_version check. Stamps the reviewer. Guarded on status='conflict'
// so a stale/double resolve is a safe no-op returning false. (Phase 23 Slice 4.)
func resolveConflictApply(ctx context.Context, db bun.IDB, opID, reviewerID string) (bool, error) {
	res, err := db.NewUpdate().Model((*SyncInboxRow)(nil)).
		Set("status = 'received'").
		Set("conflict_override = true").
		Set("error = NULL").
		Set("attempts = 0").
		Set("reviewed_by = ?", reviewerID).
		Set("reviewed_at = now()").
		Where("op_id = ?", opID).
		Where("status = 'conflict'").
		Exec(ctx)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// resolveConflictKeep resolves a 'conflict' op by keeping the production value: the
// device's edit is discarded -> status='rejected' with the reviewer's reason, the same
// terminal state an apply failure uses, so the device surfaces it through the existing
// "needs attention" channel (Slice 2). Unblocks the device queue. Guarded on
// status='conflict'. (Phase 23 Slice 4.)
func resolveConflictKeep(ctx context.Context, db bun.IDB, opID, reviewerID, reason string) (bool, error) {
	res, err := db.NewUpdate().Model((*SyncInboxRow)(nil)).
		Set("status = 'rejected'").
		Set("error = ?", reason).
		Set("reviewed_by = ?", reviewerID).
		Set("reviewed_at = now()").
		Set("finished_at = now()").
		Where("op_id = ?", opID).
		Where("status = 'conflict'").
		Exec(ctx)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// getInboxRow loads one op by id (the review detail read). Returns (nil, nil) when no
// such op exists so the handler can 404 rather than error.
func getInboxRow(ctx context.Context, db bun.IDB, opID string) (*SyncInboxRow, error) {
	row := new(SyncInboxRow)
	err := db.NewSelect().Model(row).Where("op_id = ?", opID).Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return row, nil
}

// postInboxOp promotes a 'pending_review' op to 'received' so the drain worker picks
// it up, stamping the reviewer who posted it (Phase 23 Slice 3). Guarded on
// status='pending_review' so a double-post, or a post of an op that's already been
// applied/rejected/superseded, matches no row and is a safe no-op -- it returns false
// (not found in the postable state) rather than resurrecting a terminal op. The
// worker then applies it in recv_seq order exactly as an auto-mode op, so causal
// ordering with the device's other ops is preserved.
func postInboxOp(ctx context.Context, db bun.IDB, opID, reviewerID string) (bool, error) {
	res, err := db.NewUpdate().Model((*SyncInboxRow)(nil)).
		Set("status = 'received'").
		Set("reviewed_by = ?", reviewerID).
		Set("reviewed_at = now()").
		Where("op_id = ?", opID).
		Where("status = 'pending_review'").
		Exec(ctx)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// rejectInboxReview quarantines a 'pending_review' op a reviewer declined to post:
// status='rejected' with the reviewer's reason, stamped and terminal (never applied,
// never retried). It reuses the same 'rejected' state the worker's poison-op path
// uses, so the device surfaces a review rejection through the exact same "needs
// attention" channel as an apply failure (Slice 2). Same pending_review guard as
// postInboxOp -- a stale reject is a no-op.
func rejectInboxReview(ctx context.Context, db bun.IDB, opID, reviewerID, reason string) (bool, error) {
	res, err := db.NewUpdate().Model((*SyncInboxRow)(nil)).
		Set("status = 'rejected'").
		Set("error = ?", reason).
		Set("reviewed_by = ?", reviewerID).
		Set("reviewed_at = now()").
		Set("finished_at = now()").
		Where("op_id = ?", opID).
		Where("status = 'pending_review'").
		Exec(ctx)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// getInboxStatuses loads the current status of specific ops for a status pull-back,
// scoped to submittedBy so a device can only ever read its own ops' outcomes. Only
// the columns the device needs are selected (op_id, status, error, result_feature).
func getInboxStatuses(ctx context.Context, db bun.IDB, submittedBy string, opIDs []string) ([]SyncInboxRow, error) {
	if len(opIDs) == 0 {
		return nil, nil
	}
	var rows []SyncInboxRow
	err := db.NewSelect().Model(&rows).
		Column("op_id", "status", "error", "result_feature").
		Where("submitted_by = ?", submittedBy).
		Where("op_id IN (?)", bun.In(opIDs)).
		Scan(ctx)
	return rows, err
}
