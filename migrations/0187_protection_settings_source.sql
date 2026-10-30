-- 0187_protection_settings_source.sql
-- Purpose: provenance for the protection-settings register (migration 0185), so
-- an externally-fed setting is distinguishable from a hand entry.
--
-- A protection-settings row can now be written two ways: by hand through the
-- register UI, or ingested from an authoritative external system (a relay-setting
-- database / SCADA) through the webhook engine's new "protection" target. `source`
-- records which wrote it LAST -- 'manual' for a UI edit, the external system's
-- code (config.system_code) for an ingested one -- so the register can show, and
-- a human can trust, where each device's settings came from. Null on rows written
-- before this column existed (treated as 'manual' for display).

ALTER TABLE ops.protection_settings ADD COLUMN IF NOT EXISTS source text;
