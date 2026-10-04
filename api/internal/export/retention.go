package export

// Export retention sweep (Phase 17 Slice 6, docs/phase-17-large-export.md).
//
// A succeeded export's artifact lives in RustFS forever unless something reaps
// it -- every download leaves an object behind, so the bucket grows without
// bound. This sweep, run periodically by the worker, deletes the artifacts (and
// their job rows) of exports that finished longer ago than the retention window,
// keeping storage bounded. It also reaps the rare orphan: an object whose upload
// succeeded but whose completeExport write didn't, so the object exists at the
// deterministic key while its (now failed) job row never recorded it.

import (
	"context"
	"time"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/logx"
)

const (
	// defaultRetention is how long a finished export's row + artifact are kept
	// before the sweep reaps them, when EXPORT_RETENTION is unset. A downloaded
	// export is ephemeral -- a week is generous for "I'll grab it later" without
	// letting the bucket accrete indefinitely. Set EXPORT_RETENTION=0 to disable
	// the sweep entirely (keep everything).
	defaultRetention = 7 * 24 * time.Hour

	// defaultSweepInterval is how often the worker runs the sweep when
	// EXPORT_SWEEP_INTERVAL is unset.
	defaultSweepInterval = time.Hour

	// sweepBatch bounds how many aged jobs one pass reaps, so a large backlog is
	// drained over several passes rather than one long-running delete.
	sweepBatch = 500
)

// runRetentionSweep loops the sweep on cfg's interval until ctx is cancelled. It
// runs once shortly after start (a worker that boots, sweeps, and is restarted
// often still makes progress) and then every interval. It is safe to run in
// more than one worker at once: object deletes are idempotent and the row delete
// only ever removes already-terminal, already-aged rows, so two sweepers racing
// the same batch just repeat harmless work.
func runRetentionSweep(ctx context.Context, db *bun.DB, store *ObjectStore, retention, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		n, err := sweepExpiredExports(ctx, db, store, retention)
		switch {
		case err != nil && ctx.Err() != nil:
			return // shutdown raced the sweep -- expected
		case err != nil:
			logx.From(ctx).Error("retention sweep failed", "err", err)
		case n > 0:
			logx.From(ctx).Info("retention sweep reaped expired exports", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// agedJob is the projection the sweep needs: enough to find and delete the
// artifact, then the row.
type agedJob struct {
	ID        string  `bun:"id"`
	Format    string  `bun:"format"`
	ObjectKey *string `bun:"object_key"`
}

// sweepExpiredExports deletes the artifacts and rows of terminal jobs that
// finished more than retention ago, and returns how many it reaped. Only
// terminal jobs are eligible: a queued job has no artifact and a running one is
// mid-upload, so neither is ever touched (finished_at is NULL for both, which
// the age predicate excludes anyway). An artifact whose delete fails leaves its
// row in place so the next pass retries -- the row is the only handle on the
// object, so dropping it before the object is gone would orphan the object
// permanently.
func sweepExpiredExports(ctx context.Context, db *bun.DB, store *ObjectStore, retention time.Duration) (int, error) {
	var aged []agedJob
	err := db.NewSelect().Model((*ExportJob)(nil)).
		Column("id", "format", "object_key").
		Where("status IN ('succeeded', 'failed', 'cancelled')").
		Where("finished_at < now() - (? * interval '1 second')", int(retention.Seconds())).
		OrderExpr("finished_at").
		Limit(sweepBatch).
		Scan(ctx, &aged)
	if err != nil {
		return 0, err
	}

	reaped := make([]string, 0, len(aged))
	for _, j := range aged {
		// A succeeded job points at its artifact via object_key. A failed/cancelled
		// one records no key, but may still have orphaned an object at the
		// deterministic key (upload landed, completeExport didn't) -- try that key
		// too. Delete is idempotent, so a key with no object behind it is a no-op.
		key := ""
		if j.ObjectKey != nil {
			key = *j.ObjectKey
		} else if spec, ok := formats[j.Format]; ok {
			key = objectKey(j.ID, spec.ext)
		}
		if key != "" {
			if err := store.Delete(ctx, key); err != nil {
				if ctx.Err() != nil {
					return len(reaped), ctx.Err()
				}
				// Keep the row; retry the artifact next pass rather than orphan it.
				logx.From(ctx).Error("could not delete artifact", "key", key, "job_id", j.ID, "err", err)
				continue
			}
		}
		reaped = append(reaped, j.ID)
	}

	if len(reaped) == 0 {
		return 0, nil
	}
	if _, err := db.NewDelete().Model((*ExportJob)(nil)).
		Where("id IN (?)", bun.In(reaped)).
		Exec(ctx); err != nil {
		return len(reaped), err
	}
	return len(reaped), nil
}
