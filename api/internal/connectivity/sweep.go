package connectivity

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/uptrace/bun"
)

// sweep.go is Step 3 of incremental validation: the worker that makes validation
// CONTINUOUS instead of a manual sweep. It needs no change to the write path -- it
// tails gis.feature_history, which already records one row per feature write, as the
// dirty-feature log: read the history rows above the cursor, re-validate exactly those
// features (the scoped audit of Step 1 + the reconcile of Step 2), advance the cursor.
// So every edit re-validates its feature, and fixed problems auto-leave the queue.
//
// Re-processing a batch is harmless (ReconcileIssues is idempotent and the cursor only
// moves forward), so the worker needs no per-item lease -- a crash just re-does the last
// batch. Run a SINGLE instance; it is lightweight. NOTE: feature_history ids can commit
// slightly out of order under concurrency, so a burst edit can rarely be skipped by the
// tail -- the next edit to that feature re-validates it, and a periodic WHOLE-DB reconcile
// (POST /connectivity/audit/reconcile with no body) is the backstop that catches any
// straggler.

const (
	defaultSweepInterval = 15 * time.Second
	defaultSweepBatch    = 500
)

// SweepResult reports one batch.
type SweepResult struct {
	Features     int   // distinct features re-validated this batch
	Upserted     int   // findings written/refreshed
	Resolved     int   // issues auto-resolved (fixed)
	MaxHistoryID int64 // the cursor's new position
}

// WorkerConfig tunes the worker. Zero values fall back to the defaults.
type WorkerConfig struct {
	Interval  time.Duration // how often to wake when caught up
	BatchSize int           // history rows per sweep
}

// WorkerConfigFromEnv reads VALIDATION_SWEEP_INTERVAL (a Go duration) and
// VALIDATION_SWEEP_BATCH (an int), falling back to the defaults.
func WorkerConfigFromEnv() WorkerConfig {
	cfg := WorkerConfig{Interval: defaultSweepInterval, BatchSize: defaultSweepBatch}
	if v := os.Getenv("VALIDATION_SWEEP_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Interval = d
		}
	}
	if v := os.Getenv("VALIDATION_SWEEP_BATCH"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.BatchSize = n
		}
	}
	return cfg
}

// ValidationSweep processes one batch: read the cursor, take the next history rows,
// re-validate those features (scoped audit + reconcile), and advance the cursor. It
// returns a zero-Features result when there is nothing new.
func ValidationSweep(ctx context.Context, db *bun.DB, batch int) (*SweepResult, error) {
	if batch <= 0 {
		batch = defaultSweepBatch
	}

	var cursor int64
	if err := db.NewRaw(`SELECT last_history_id FROM ops.validation_cursor WHERE singleton`).Scan(ctx, &cursor); err != nil {
		return nil, fmt.Errorf("connectivity: validation sweep: reading cursor: %w", err)
	}

	rows, err := db.QueryContext(ctx,
		`SELECT id, feature_id FROM gis.feature_history WHERE id > ? ORDER BY id LIMIT ?`, cursor, batch)
	if err != nil {
		return nil, fmt.Errorf("connectivity: validation sweep: tailing history: %w", err)
	}
	defer rows.Close()

	seen := map[string]bool{}
	ids := []string{}
	var maxID int64
	for rows.Next() {
		var id int64
		var featureID string
		if err := rows.Scan(&id, &featureID); err != nil {
			return nil, fmt.Errorf("connectivity: validation sweep: scanning history: %w", err)
		}
		if id > maxID {
			maxID = id
		}
		if featureID != "" && !seen[featureID] {
			seen[featureID] = true
			ids = append(ids, featureID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("connectivity: validation sweep: reading history: %w", err)
	}
	if len(ids) == 0 {
		return &SweepResult{}, nil // caught up
	}

	report, err := AuditAll(ctx, db, AuditInput{FeatureIDs: ids})
	if err != nil {
		return nil, fmt.Errorf("connectivity: validation sweep: audit: %w", err)
	}
	recon, err := ReconcileIssues(ctx, db, report, ids)
	if err != nil {
		return nil, fmt.Errorf("connectivity: validation sweep: reconcile: %w", err)
	}

	// Advance the cursor (forward only, so a concurrent sweep can't rewind it).
	if _, err := db.ExecContext(ctx,
		`UPDATE ops.validation_cursor SET last_history_id = ?, updated_at = now() WHERE singleton AND last_history_id < ?`,
		maxID, maxID); err != nil {
		return nil, fmt.Errorf("connectivity: validation sweep: advancing cursor: %w", err)
	}

	return &SweepResult{Features: len(ids), Upserted: recon.Upserted, Resolved: recon.Resolved, MaxHistoryID: maxID}, nil
}

// RunWorker runs the incremental validation loop until ctx is cancelled. Each wake it
// DRAINS the backlog (sweeps until caught up), then waits for the next tick. A panic in
// one sweep is contained (logged, backed off to the next tick) so a single crafted row
// can't take the worker down.
func RunWorker(ctx context.Context, db *bun.DB, cfg WorkerConfig) error {
	if cfg.Interval <= 0 {
		cfg.Interval = defaultSweepInterval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultSweepBatch
	}
	slog.Info("validation worker: starting", "interval", cfg.Interval.String(), "batch", cfg.BatchSize)

	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		drainBacklog(ctx, db, cfg.BatchSize)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// drainBacklog sweeps repeatedly until caught up (a sweep touches no features) or an
// error/cancel stops it; an error backs off to the caller's next tick.
func drainBacklog(ctx context.Context, db *bun.DB, batch int) {
	for {
		if ctx.Err() != nil {
			return
		}
		res, err := safeSweep(ctx, db, batch)
		if err != nil {
			slog.Error("validation sweep failed", "err", err)
			return
		}
		if res == nil || res.Features == 0 {
			return
		}
		slog.Info("validation sweep", "features", res.Features, "upserted", res.Upserted, "auto_resolved", res.Resolved, "cursor", res.MaxHistoryID)
	}
}

// safeSweep wraps one sweep in a panic recovery -- an unexpected panic becomes an
// error the loop logs and backs off from, rather than crashing the worker.
func safeSweep(ctx context.Context, db *bun.DB, batch int) (res *SweepResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("connectivity: validation sweep panicked: %v", r)
		}
	}()
	return ValidationSweep(ctx, db, batch)
}
