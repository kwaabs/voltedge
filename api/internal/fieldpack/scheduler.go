package fieldpack

// Shared-cell SCHEDULING + the maintenance loop (S4 + S6 sweep,
// docs/shared-field-packs-design.md).
//
// A cell can be rebuilt automatically so crews keep getting a fresh base without an
// admin clicking "build" -- the product owner's "build overnight when there's less
// CPU pressure." Rather than take a cron-parser dependency, the cell's `schedule`
// column is a simple CADENCE token (hourly | daily | weekly; anything else / NULL =
// not scheduled), and builds are confined to an optional off-peak UTC hour WINDOW
// (FIELDSYNC_BUILD_WINDOW, e.g. "0-5") so the heavy tippecanoe builds land overnight.
//
// The maintenance loop (RunMaintenance, a sibling of the build + sync workers in
// cmd/fieldsync) fires on a timer and does two jobs: enqueue the cells whose rebuild
// is due (within the window), and run the retention sweep (reapAllCells, S6). Both
// are idempotent and self-skipping -- a cell with a build already in flight is not
// re-enqueued, and retention only reaps versions no crew holds.

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/kwaabs/voltedge/api/internal/logx"
	"github.com/uptrace/bun"
)

// scheduleCadence maps a cell's schedule token to the rebuild interval, or
// (0, false) when the cell is not scheduled (NULL / empty / unknown token).
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

// cellBuildDue reports whether a scheduled cell is due for a rebuild: no build is
// already queued/running for it (so a slow build never stacks duplicates), AND its
// newest succeeded version is older than the cadence (or it has never built). Read in
// one round-trip.
func cellBuildDue(ctx context.Context, db bun.IDB, cellID string, cadence time.Duration) (bool, error) {
	var row struct {
		InFlight  int        `bun:"in_flight"`
		LastBuilt *time.Time `bun:"last_built"`
	}
	const q = `
SELECT
    (SELECT count(*) FROM ops.field_packs
       WHERE cell_id = ? AND status IN ('queued', 'running')) AS in_flight,
    (SELECT max(created_at) FROM ops.field_packs
       WHERE cell_id = ? AND status = 'succeeded') AS last_built`
	if err := db.NewRaw(q, cellID, cellID).Scan(ctx, &row); err != nil {
		return false, err
	}
	if row.InFlight > 0 {
		return false, nil // a build is already in flight; don't stack another
	}
	if row.LastBuilt == nil {
		return true, nil // never built -> due
	}
	return time.Since(*row.LastBuilt) >= cadence, nil
}

// runSchedules enqueues a shared_base build for every active, scheduled cell whose
// rebuild is due. A scheduled build is attributed to the cell's creator (the admin
// who defined it) -- field_packs.requested_by is NOT NULL, and the creator is the
// natural provenance for an automated rebuild of their cell. Returns how many builds
// it enqueued. A per-cell failure is logged and skipped so one bad cell never stalls
// the sweep.
func runSchedules(ctx context.Context, db *bun.DB) int {
	cells, err := listCells(ctx, db)
	if err != nil {
		logx.From(ctx).Error("scheduler: listing cells failed", "err", err)
		return 0
	}
	enqueued := 0
	for i := range cells {
		cell := &cells[i]
		if !cell.Active {
			continue
		}
		cadence, ok := scheduleCadence(cell.Schedule)
		if !ok {
			continue
		}
		due, err := cellBuildDue(ctx, db, cell.ID, cadence)
		if err != nil {
			logx.From(ctx).Error("scheduler: due check failed", "cell", cell.ID, "err", err)
			continue
		}
		if !due {
			continue
		}
		if _, err := enqueueCellBuild(ctx, db, cell, cell.CreatedBy); err != nil {
			logx.From(ctx).Error("scheduler: enqueue failed", "cell", cell.ID, "err", err)
			continue
		}
		enqueued++
		logx.From(ctx).Info("scheduler: enqueued rebuild", "cell", cell.ID, "name", cell.Name)
	}
	return enqueued
}

// MaintenanceConfig configures the periodic maintenance loop.
type MaintenanceConfig struct {
	// Interval between ticks (default 15m). Each tick runs retention and, when inside
	// the build window, the schedule check.
	Interval time.Duration
	// WindowStart/WindowEnd bound the UTC hours [start, end) in which scheduled builds
	// may be enqueued -- the off-peak window the heavy builds are confined to. Both -1
	// means "no window": scheduled builds may fire any hour.
	WindowStart int
	WindowEnd   int
}

// MaintenanceConfigFromEnv reads the loop's config: FIELDSYNC_MAINTENANCE_INTERVAL
// (a Go duration, default 15m) and FIELDSYNC_BUILD_WINDOW ("start-end" UTC hours,
// e.g. "0-5"; unset = any hour).
func MaintenanceConfigFromEnv() MaintenanceConfig {
	cfg := MaintenanceConfig{Interval: 15 * time.Minute, WindowStart: -1, WindowEnd: -1}
	if v := os.Getenv("FIELDSYNC_MAINTENANCE_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Interval = d
		}
	}
	if v := os.Getenv("FIELDSYNC_BUILD_WINDOW"); v != "" {
		if start, end, ok := parseHourWindow(v); ok {
			cfg.WindowStart, cfg.WindowEnd = start, end
		}
	}
	return cfg
}

// parseHourWindow parses "start-end" into two UTC hours in [0,24], or (0,0,false)
// when malformed.
func parseHourWindow(s string) (int, int, bool) {
	parts := strings.SplitN(strings.TrimSpace(s), "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || start < 0 || start > 24 || end < 0 || end > 24 {
		return 0, 0, false
	}
	return start, end, true
}

// inWindow reports whether hour (0-23) is inside the config's build window. A window
// of (-1,-1) means "always". A window that wraps midnight (start > end, e.g. 22-5) is
// handled as the union of [start,24) and [0,end).
func (c MaintenanceConfig) inWindow(hour int) bool {
	if c.WindowStart < 0 || c.WindowEnd < 0 {
		return true
	}
	if c.WindowStart <= c.WindowEnd {
		return hour >= c.WindowStart && hour < c.WindowEnd
	}
	return hour >= c.WindowStart || hour < c.WindowEnd
}

// RunMaintenance is the periodic maintenance loop (cmd/fieldsync): on a timer it runs
// retention (always) and the schedule check (only inside the off-peak build window).
// It runs once immediately, then every cfg.Interval, until ctx is cancelled. Each
// tick's work is panic-guarded so one bad cell can never kill the loop.
func RunMaintenance(ctx context.Context, db *bun.DB, store *export.ObjectStore, cfg MaintenanceConfig) error {
	tick := func() {
		defer func() {
			if rec := recover(); rec != nil {
				logx.From(ctx).Error("maintenance: recovered from panic", "panic", rec)
			}
		}()
		// Retention runs every tick (cheap; reaps only unheld, over-N versions).
		if n, err := reapAllCells(ctx, db, store); err != nil {
			logx.From(ctx).Error("maintenance: retention sweep failed", "err", err)
		} else if n > 0 {
			logx.From(ctx).Info("maintenance: reaped versions", "count", n)
		}
		// Scheduling runs only in the off-peak window, so the heavy builds land overnight.
		if cfg.inWindow(time.Now().UTC().Hour()) {
			if n := runSchedules(ctx, db); n > 0 {
				logx.From(ctx).Info("maintenance: scheduled rebuilds", "count", n)
			}
		}
	}

	tick()
	t := time.NewTicker(cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			tick()
		}
	}
}
