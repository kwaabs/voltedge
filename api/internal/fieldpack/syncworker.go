package fieldpack

// The sync-apply worker loop (Phase 23 Slice 1, docs/phase-23-sync-inbox.md).
//
// RunSyncWorker is the drain half of the receive/apply split: POST /sync captures
// device ops into ops.sync_inbox (inbox.go); this loop claims each 'received' row
// FOR UPDATE SKIP LOCKED and commits it to production through the *unchanged*
// applySyncOp path (sync.go) -- the same per-op transaction + shared crud/graph
// engines the old inline endpoint used. It mirrors export.RunWorker (worker.go):
// acquire a slot, claim, run under a heartbeat lease, drain in-flight work on
// shutdown. It lives in the fieldpack package (not cmd/) so it can drive the
// unexported queue primitives directly; cmd/fieldsync just starts the goroutine.
//
// "Worker-immediate" apply (the locked Slice 1 decision): the endpoint only
// receives + acks, and this loop applies moments later. A short Poll keeps that lag
// sub-second in trusted mode, so the field UX feels like today's inline apply but
// with a durable receive stage and an audit trail in front of it.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/logx"
	"github.com/uptrace/bun"
)

const (
	defaultSyncConcurrency = 2

	// defaultSyncLease mirrors the export lease: a crashed worker's claimed op
	// becomes reclaimable this soon after its last heartbeat. It must comfortably
	// exceed the heartbeat interval (an op heartbeats at lease/3). A double-claim
	// after a lapse is harmless -- every apply path is idempotent.
	defaultSyncLease = 60 * time.Second

	// defaultSyncPoll is short so a just-received op applies near-immediately
	// (worker-immediate mode), keeping the received->applied lag sub-second.
	defaultSyncPoll = 1 * time.Second
)

// SyncWorkerConfig tunes RunSyncWorker. Same shape as export.WorkerConfig minus the
// retention sweep (an inbox retention sweep is a later slice).
type SyncWorkerConfig struct {
	ID          string
	Concurrency int
	Lease       time.Duration
	Poll        time.Duration
}

// SyncWorkerConfigFromEnv reads SYNC_WORKER_CONCURRENCY and SYNC_JOB_LEASE, falling
// back to sane defaults, and derives a stable-per-process worker id (host + pid) so
// two workers on one host don't collide. Mirrors export.WorkerConfigFromEnv.
func SyncWorkerConfigFromEnv() SyncWorkerConfig {
	cfg := SyncWorkerConfig{
		Concurrency: defaultSyncConcurrency,
		Lease:       defaultSyncLease,
		Poll:        defaultSyncPoll,
	}
	if v := os.Getenv("SYNC_WORKER_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Concurrency = n
		} else {
			slog.Warn("ignoring invalid env var", "worker", "sync", "var", "SYNC_WORKER_CONCURRENCY", "value", v)
		}
	}
	if v := os.Getenv("SYNC_JOB_LEASE"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Lease = d
		} else {
			slog.Warn("ignoring invalid env var", "worker", "sync", "var", "SYNC_JOB_LEASE", "value", v)
		}
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "worker"
	}
	cfg.ID = "sync-" + host + "-" + strconv.Itoa(os.Getpid())
	return cfg
}

// RunSyncWorker drives the claim loop until ctx is cancelled, then drains the ops
// already in flight before returning. A slot is acquired before each claim so the
// worker never holds more claimed ops than it can run. It returns ctx.Err() on a
// clean shutdown. Structure is identical to export.RunWorker.
func RunSyncWorker(ctx context.Context, db *bun.DB, cfg SyncWorkerConfig) error {
	lg := slog.Default().With(slog.String("worker", "sync"), slog.String("worker_id", cfg.ID))
	ctx = logx.Into(ctx, lg)
	lg.Info("worker started", "concurrency", cfg.Concurrency, "lease", cfg.Lease.String())

	sem := make(chan struct{}, cfg.Concurrency)
	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case sem <- struct{}{}:
		}

		row, err := claimInboxOp(ctx, db, cfg.ID, cfg.Lease)
		if err != nil {
			if ctx.Err() != nil { // shutdown races the claim -- expected
				<-sem
				wg.Wait()
				return ctx.Err()
			}
			lg.Error("claim failed", "err", err)
			<-sem
			syncSleep(ctx, cfg.Poll)
			continue
		}
		if row == nil { // nothing claimable
			<-sem
			syncSleep(ctx, cfg.Poll)
			continue
		}

		wg.Add(1)
		go func(r *SyncInboxRow) {
			defer wg.Done()
			defer func() { <-sem }()
			applyInboxOpWithHeartbeat(ctx, db, cfg, r)
		}(row)
	}
}

// applyInboxOpWithHeartbeat applies one claimed op while a background ticker keeps
// its lease fresh. Mirrors export.runJobWithHeartbeat: a heartbeat that finds the op
// no longer ours cancels the op's context so an in-flight apply aborts rather than
// racing a reclaiming worker (the reclaim is safe regardless -- apply is idempotent).
func applyInboxOpWithHeartbeat(parent context.Context, db *bun.DB, cfg SyncWorkerConfig, row *SyncInboxRow) {
	ol := logx.From(parent).With(slog.String("op_id", row.OpID))
	opCtx, cancel := context.WithCancel(logx.Into(parent, ol))
	defer cancel()

	interval := cfg.Lease / 3
	if interval <= 0 {
		interval = time.Second
	}

	var hb sync.WaitGroup
	hb.Add(1)
	go func() {
		defer hb.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-opCtx.Done():
				return
			case <-t.C:
				ok, err := heartbeatInboxOp(opCtx, db, row.OpID, cfg.ID, cfg.Lease)
				if err != nil {
					if opCtx.Err() != nil {
						return // the op finished; this beat lost the race
					}
					ol.Error("heartbeat failed", "err", err)
					continue
				}
				if !ok {
					ol.Warn("lost lease, aborting op")
					cancel()
					return
				}
			}
		}
	}()

	// Recover a panic from the apply so one poison op (a crafted device payload
	// tripping a nil-deref or bad type assertion deep in applySyncOp's crud/graph
	// replay) fails just that op instead of crashing the whole sync worker -- an
	// unrecovered panic in this goroutine takes the process down, halting all
	// field sync, and leaves the op stuck 'applying' to be reclaimed and panic
	// again on restart (a crash loop). A panic is treated as a terminal poison-op
	// failure, exactly like any other apply error (this worker's every-failure-is-
	// terminal model): markInboxRejected quarantines it so it's never reclaimed.
	// The apply tx has already rolled back by the time the panic reaches here
	// (applySyncOp's per-op RunInTx rolls back then re-panics), so nothing was
	// committed. The mark runs on a non-cancel context so it survives opCtx being
	// torn down, and still matches the worker_id + status='applying' guard.
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				ol.Error("sync op apply panicked", "err", rec, "stack", string(debug.Stack()))
				if err := markInboxRejected(context.WithoutCancel(parent), db, row.OpID, cfg.ID, fmt.Sprintf("apply panicked: %v", rec)); err != nil {
					ol.Error("mark op rejected after panic failed", "err", err)
				}
			}
		}()
		applyInboxOp(opCtx, db, cfg, row)
	}()
	cancel()
	hb.Wait()
}

// applyInboxOp commits one claimed op to production and records the outcome. It
// rebuilds the syncOp wire shape from the row and replays it through the unchanged
// applySyncOp (per-op tx, idempotent, shared engines) with a fresh per-op metaCache.
// Success -> markInboxApplied with the server row; a business/validation failure ->
// markInboxRejected (quarantine, no retry -- the poison-op fix).
//
// A context cancellation (shutdown, or a lost lease) is NOT a rejection: the op is
// left 'applying' so its lease lapses and another worker reclaims it. Only a genuine
// apply error quarantines the op.
func applyInboxOp(ctx context.Context, db *bun.DB, cfg SyncWorkerConfig, row *SyncInboxRow) {
	op := syncOp{OpID: row.OpID, Kind: row.Kind, Payload: row.Payload}
	if row.FeatureClass != nil {
		op.FeatureClass = *row.FeatureClass
	}
	if row.FeatureID != nil {
		op.FeatureID = *row.FeatureID
	}
	if row.BaseVersion != nil {
		op.BaseVersion = *row.BaseVersion
	}

	cache := newMetaCache(db)

	// Optimistic-concurrency conflict detection (Phase 23 Slice 4). For an
	// update/delete of a class with detection on, a base_version that predates the
	// current production row means someone edited it after this device's snapshot --
	// hold it as 'conflict' (or 'superseded' if a newer op for the same feature is
	// queued) instead of silently overwriting. A detection error is non-fatal: log and
	// fall through to the normal apply (detection is a guard, not a gate).
	if handled, err := detectSyncConflict(ctx, db, cache, row, cfg.ID); err != nil {
		if ctx.Err() != nil {
			return
		}
		logx.From(ctx).Error("conflict check failed (applying anyway)", "err", err)
	} else if handled {
		return
	}

	res := applySyncOp(ctx, db, cache, op, row.SubmittedBy)

	if ctx.Err() != nil {
		// We were cancelled mid-apply (shutdown or reclaimed lease). Don't record a
		// terminal outcome -- leave the op for the lease-expiry reclaim.
		return
	}

	if res.Status == "applied" {
		if err := markInboxApplied(ctx, db, row.OpID, cfg.ID, res.Feature); err != nil {
			logx.From(ctx).Error("mark op applied failed", "err", err)
		}
		return
	}
	// Any apply error quarantines the op (Slice 1 treats every failure as terminal,
	// faithful to export's fail-on-error; transient-vs-permanent classification with
	// bounded retry is a later refinement).
	if err := markInboxRejected(ctx, db, row.OpID, cfg.ID, res.Error); err != nil {
		logx.From(ctx).Error("mark op rejected failed", "err", err)
	}
}

// detectSyncConflict decides whether a claimed op should be held rather than applied
// (Phase 23 Slice 4). It returns handled=true when it has terminally routed the op
// (marked 'conflict' or 'superseded'); the caller then skips applySyncOp. handled=false
// means "no conflict, apply normally."
//
// Only update_attrs/delete are checked: a create mints a new row (nothing to conflict
// with) and an establish is an idempotent graph edit with no base_version semantics.
// The check is gated by the per-class detect_conflicts policy (default off = today's
// silent LWW), skipped when a supervisor already force-resolved this op
// (conflict_override), and skipped when the op carries no base_version (nothing to
// compare). The comparison: the production row's updated_at is AFTER the op's
// base_version (someone edited since the device's snapshot) AND its created_at is at or
// before base_version (the row existed at snapshot -- so this isn't the device's own
// just-applied create bumping updated_at past the snapshot, which would be a false
// positive). A missing row (the create hasn't applied, or it was hard-deleted) is not a
// conflict. When a conflict is found but a LATER op for the same feature is queued, this
// op is superseded by that newer edit instead.
func detectSyncConflict(ctx context.Context, db *bun.DB, cache *metaCache, row *SyncInboxRow, workerID string) (bool, error) {
	if row.ConflictOverride {
		return false, nil // human already waived the check
	}
	if row.Kind != "update_attrs" && row.Kind != "delete" {
		return false, nil
	}
	if row.BaseVersion == nil || *row.BaseVersion == "" || row.FeatureClass == nil || row.FeatureID == nil {
		return false, nil
	}
	base, err := time.Parse(time.RFC3339, *row.BaseVersion)
	if err != nil {
		return false, nil // an unparseable base_version can't be compared -- apply as before
	}

	policy, err := loadSyncPolicy(ctx, db)
	if err != nil {
		return false, err
	}
	if !policy.DetectFor(*row.FeatureClass) {
		return false, nil
	}

	loaded, err := cache.get(ctx, *row.FeatureClass)
	if err != nil {
		return false, err // metadata load failed -- applySyncOp will reject it with the same error
	}
	created, updated, exists, err := crud.FeatureVersions(ctx, db, loaded, *row.FeatureID)
	if err != nil {
		return false, err
	}
	if !exists || !updated.After(base) || created.After(base) {
		return false, nil // no divergence, or a row that didn't exist at the device's snapshot
	}

	// A concurrent production edit diverged from the device's base. If a newer op for
	// the same feature is queued, this op is obsolete -- retire it silently as superseded
	// and let the newer one be the surfaced conflict; otherwise hold this one for review.
	later, err := hasLaterActiveOpForFeature(ctx, db, row)
	if err != nil {
		return false, err
	}
	if later {
		if err := markInboxSuperseded(ctx, db, row.OpID, workerID); err != nil {
			return false, err
		}
		return true, nil
	}
	msg := fmt.Sprintf("edit conflict: feature was modified in production (updated %s) after this device's snapshot (%s)",
		updated.UTC().Format(time.RFC3339), base.UTC().Format(time.RFC3339))
	if err := markInboxConflict(ctx, db, row.OpID, workerID, msg); err != nil {
		return false, err
	}
	return true, nil
}

// syncSleep waits d, or returns early if ctx is cancelled -- so an idle worker
// shuts down promptly instead of sitting out a full Poll. (export has the same
// helper as unexported sleep; duplicated here to keep the packages independent.)
func syncSleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
