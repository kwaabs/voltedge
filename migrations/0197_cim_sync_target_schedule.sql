-- 0197_cim_sync_target_schedule.sql
-- Optional auto-run cadence for a CIM sync target. Like the field-pack cell
-- scheduler (api/internal/fieldpack/scheduler.go) this is a simple CADENCE token
-- rather than a cron parser: 'hourly' | 'daily' | 'weekly'; NULL / anything else =
-- manual (run only on demand). The export worker's maintenance loop (RunWorker)
-- enqueues an incremental export for every active, scheduled target whose last
-- SUCCESSFUL run is older than its cadence and which has no run already in flight.

ALTER TABLE ops.cim_sync_targets
    ADD COLUMN IF NOT EXISTS schedule text;
