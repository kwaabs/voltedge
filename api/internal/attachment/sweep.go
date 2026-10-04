package attachment

// Soft-deleted attachment retention sweep (Phase 22 Slice 6,
// docs/phase-22-attachments.md).
//
// Deleting an attachment (or the feature it hangs off) only sets deleted_at --
// the RustFS objects (the full file and, for an image, its thumbnail) are left in
// place so a delete is cheap and reversible for a window. This sweep, run
// periodically alongside the export retention sweep, reaps the objects of rows
// soft-deleted longer ago than the retention window and then hard-deletes those
// rows, keeping the bucket (and the files.attachments table) bounded. It mirrors
// export's runRetentionSweep: object deletes are idempotent, and a row is dropped
// only after its objects are gone, so a failed object delete just retries next
// pass rather than orphaning the object.

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/kwaabs/voltedge/api/internal/logx"
	"github.com/uptrace/bun"
)

const (
	// defaultRetention is how long a soft-deleted attachment's row + objects are
	// kept before this sweep reaps them, when ATTACHMENT_RETENTION is unset. A
	// week matches the export sweep and gives an "undo my delete" window without
	// letting deleted files accrete. Set ATTACHMENT_RETENTION=0 to disable.
	defaultRetention = 7 * 24 * time.Hour

	// defaultSweepInterval is how often the sweep runs when
	// ATTACHMENT_SWEEP_INTERVAL is unset.
	defaultSweepInterval = time.Hour

	// sweepBatch bounds how many aged rows one pass reaps, so a large backlog is
	// drained over several passes rather than one long-running delete.
	sweepBatch = 500
)

// SweepConfigFromEnv reads the retention window and interval from the environment,
// falling back to the defaults. A zero (or negative) retention disables the sweep;
// the caller checks retention > 0 before starting RunDeletedSweep.
func SweepConfigFromEnv() (retention, interval time.Duration) {
	retention, interval = defaultRetention, defaultSweepInterval
	if v := os.Getenv("ATTACHMENT_RETENTION"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			retention = d
		} else {
			slog.Warn("ignoring invalid env var", "worker", "attachment-sweep", "var", "ATTACHMENT_RETENTION", "value", v)
		}
	}
	if v := os.Getenv("ATTACHMENT_SWEEP_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			interval = d
		} else {
			slog.Warn("ignoring invalid env var", "worker", "attachment-sweep", "var", "ATTACHMENT_SWEEP_INTERVAL", "value", v)
		}
	}
	return retention, interval
}

// RunDeletedSweep loops the sweep on the given interval until ctx is cancelled. It
// runs once shortly after start and then every interval. Safe to run in more than
// one process at a time: object deletes are idempotent and the row delete only
// ever removes already-soft-deleted, already-aged rows, so two sweepers racing the
// same batch just repeat harmless work.
func RunDeletedSweep(ctx context.Context, db *bun.DB, store *export.ObjectStore, retention, interval time.Duration) {
	ctx = logx.Into(ctx, slog.Default().With(slog.String("worker", "attachment-sweep")))
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		n, err := sweepDeletedAttachments(ctx, db, store, retention)
		switch {
		case err != nil && ctx.Err() != nil:
			return // shutdown raced the sweep -- expected
		case err != nil:
			logx.From(ctx).Error("sweep failed", "err", err)
		case n > 0:
			logx.From(ctx).Info("sweep reaped deleted attachments", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// agedAttachment is the projection the sweep needs: the id to delete the row and
// the object keys to delete the bytes.
type agedAttachment struct {
	ID        string  `bun:"id"`
	ObjectKey string  `bun:"object_key"`
	ThumbKey  *string `bun:"thumb_key"`
}

// sweepDeletedAttachments deletes the RustFS objects and rows of attachments
// soft-deleted more than retention ago, returning how many rows it reaped. An
// object whose delete fails leaves its row in place so the next pass retries --
// the row is the only handle on the object, so dropping it first would orphan the
// object permanently.
func sweepDeletedAttachments(ctx context.Context, db *bun.DB, store *export.ObjectStore, retention time.Duration) (int, error) {
	var aged []agedAttachment
	err := db.NewSelect().Model((*Attachment)(nil)).
		Column("id", "object_key", "thumb_key").
		Where("deleted_at IS NOT NULL").
		Where("deleted_at < now() - (? * interval '1 second')", int(retention.Seconds())).
		OrderExpr("deleted_at").
		Limit(sweepBatch).
		Scan(ctx, &aged)
	if err != nil {
		return 0, err
	}

	reaped := make([]string, 0, len(aged))
	for _, a := range aged {
		// Delete the full object then the thumbnail (if any). Delete is idempotent,
		// so a key with no object behind it is a harmless no-op.
		keys := []string{a.ObjectKey}
		if a.ThumbKey != nil && *a.ThumbKey != "" {
			keys = append(keys, *a.ThumbKey)
		}
		failed := false
		for _, key := range keys {
			if key == "" {
				continue
			}
			if err := store.Delete(ctx, key); err != nil {
				if ctx.Err() != nil {
					return len(reaped), ctx.Err()
				}
				// Keep the row; retry the object next pass rather than orphan it.
				logx.From(ctx).Error("could not delete object", "key", key, "attachment_id", a.ID, "err", err)
				failed = true
				break
			}
		}
		if !failed {
			reaped = append(reaped, a.ID)
		}
	}

	if len(reaped) == 0 {
		return 0, nil
	}
	if _, err := db.NewDelete().Model((*Attachment)(nil)).
		Where("id IN (?)", bun.In(reaped)).
		Exec(ctx); err != nil {
		return len(reaped), err
	}
	return len(reaped), nil
}
