package export

// Auto-run scheduling for CIM sync targets (migration 0197). Modelled on the
// field-pack cell scheduler (api/internal/fieldpack/scheduler.go): rather than a
// cron parser, a target's `schedule` is a cadence token (hourly|daily|weekly), and
// the export worker's maintenance loop enqueues an incremental run for every
// active, scheduled target whose last SUCCESSFUL run is older than its cadence and
// which has no run already in flight. Idempotent + self-skipping: a run already
// queued/running is never re-enqueued, and a scheduled run is attributed to the
// target's creator (export_jobs.requested_by is NOT NULL).

import (
	"context"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/logx"
	"github.com/uptrace/bun"
)

// defaultSyncScheduleInterval is how often the worker checks for due scheduled
// targets (not the cadence itself -- that is per-target). 0 disables the loop.
const defaultSyncScheduleInterval = 5 * time.Minute

// scheduleCadence maps a target's schedule token to its interval, or (0, false)
// when it is not scheduled (NULL / empty / unknown token = manual).
func scheduleCadence(schedule *string) (time.Duration, bool) {
	if schedule == nil {
		return 0, false
	}
	switch strings.ToLower(strings.TrimSpace(*schedule)) {
	case "hourly":
		return time.Hour, true
	case "daily":
		return 24 * time.Hour, true
	case "weekly":
		return 7 * 24 * time.Hour, true
	default:
		return 0, false
	}
}

// syncTargetDue reports whether a scheduled target is due for a run: no run is
// already queued/running for it (so a slow run never stacks duplicates), AND its
// newest succeeded run is older than the cadence (or it has never run). Read in one
// round-trip. The in-flight check keys on the scope jsonb's sync_target_id.
func syncTargetDue(ctx context.Context, db bun.IDB, targetID string, cadence time.Duration) (bool, error) {
	var row struct {
		InFlight int        `bun:"in_flight"`
		LastRun  *time.Time `bun:"last_run"`
	}
	const q = `
SELECT
    (SELECT count(*) FROM ops.export_jobs
       WHERE status IN ('queued', 'running') AND scope->>'sync_target_id' = ?) AS in_flight,
    (SELECT max(created_at) FROM ops.export_jobs
       WHERE status = 'succeeded' AND scope->>'sync_target_id' = ?) AS last_run`
	if err := db.NewRaw(q, targetID, targetID).Scan(ctx, &row); err != nil {
		return false, err
	}
	if row.InFlight > 0 {
		return false, nil
	}
	if row.LastRun == nil {
		return true, nil
	}
	return time.Since(*row.LastRun) >= cadence, nil
}

// runSyncSchedules enqueues an incremental export for every active, scheduled sync
// target that is due, returning how many it enqueued. A per-target failure is
// logged and skipped so one bad target never stalls the sweep. A target with a
// schedule but no creator is skipped (an automated run needs a requested_by).
func runSyncSchedules(ctx context.Context, db *bun.DB) int {
	targets, err := listSyncTargets(ctx, db)
	if err != nil {
		logx.From(ctx).Error("sync scheduler: listing targets failed", "err", err)
		return 0
	}
	enqueued := 0
	for i := range targets {
		t := &targets[i]
		if !t.Active {
			continue
		}
		cadence, ok := scheduleCadence(t.Schedule)
		if !ok {
			continue
		}
		if t.CreatedBy == nil {
			logx.From(ctx).Warn("sync scheduler: scheduled target has no creator, skipping", "target", t.ID, "name", t.Name)
			continue
		}
		due, err := syncTargetDue(ctx, db, t.ID, cadence)
		if err != nil {
			logx.From(ctx).Error("sync scheduler: due check failed", "target", t.ID, "err", err)
			continue
		}
		if !due {
			continue
		}
		if _, err := enqueueExport(ctx, db, *t.CreatedBy, scopeForTarget(t)); err != nil {
			logx.From(ctx).Error("sync scheduler: enqueue failed", "target", t.ID, "err", err)
			continue
		}
		enqueued++
		logx.From(ctx).Info("sync scheduler: enqueued run", "target", t.ID, "name", t.Name)
	}
	return enqueued
}

// runSyncScheduler loops runSyncSchedules on interval until ctx is cancelled. It
// runs once shortly after start, then every interval. Panic-guarded so one bad
// tick can't kill the loop. Safe enough to run in a single export worker; the
// in-flight check also dampens duplicates if more than one worker runs it.
func runSyncScheduler(ctx context.Context, db *bun.DB, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					logx.From(ctx).Error("sync scheduler: recovered from panic", "panic", rec)
				}
			}()
			if n := runSyncSchedules(ctx, db); n > 0 {
				logx.From(ctx).Info("sync scheduler: enqueued scheduled exports", "count", n)
			}
		}()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
