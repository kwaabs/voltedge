-- 0029_seed_territory_enforcement_flag.sql
-- Purpose: Phase 11 Slice D -- seed the runtime toggle that gates the
-- "every geographic feature must belong to at least a district" invariant.
--
-- The CRUD write path (api/internal/territory.Assign) always tries to
-- resolve and store a feature's territory, but whether a *failure* to
-- resolve one rejects the write is governed by this flag. It ships OFF so
-- the staged rollout is safe: features keep saving (just possibly
-- unassigned) while territories are authored and existing rows are
-- backfilled. A break-glass admin flips it to 'true' at runtime -- no
-- redeploy -- once coverage exists, exactly the pattern local_auth_enabled
-- established (migration 0024).
--
-- Reuses iam.system_settings rather than a new table: one global key/value
-- store for every runtime toggle. ON CONFLICT DO NOTHING keeps this
-- idempotent and preserves an operator's chosen value on re-run.

INSERT INTO iam.system_settings (key, value) VALUES ('territory_enforcement_enabled', 'false')
ON CONFLICT (key) DO NOTHING;
