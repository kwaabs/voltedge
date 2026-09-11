-- 0042_field_packs_attachment_count.sql
-- Purpose: Phase 22 Slice 6 -- record how many image attachments a field pack
-- carried, so the admin pack-management UI (fieldpack.AdminRoutes) can surface it
-- alongside feature_count. The count already rides the pack's own manifest
-- (attachment_count, added in Slice 4's v6 builder), but the admin list reads
-- ops.field_packs columns, not the built artifact -- so the worker now also
-- persists it here at completePack, mirroring how feature_count is stored.
--
-- Nullable and unset for packs built before this column existed (their manifests
-- still hold the true count); the UI treats NULL as "unknown", not zero.

ALTER TABLE ops.field_packs
ADD COLUMN IF NOT EXISTS attachment_count integer;

COMMENT ON COLUMN ops.field_packs.attachment_count IS 'Number of image attachments packed into this field pack (thumbnails ride inside the pack; see docs/phase-22-attachments.md). Set by the build worker at completion, mirroring feature_count. NULL for packs built before this column existed.';
