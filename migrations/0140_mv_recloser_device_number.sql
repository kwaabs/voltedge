-- 0140_mv_recloser_device_number.sql
--
-- Give MV_RECLOSER its own ANSI/IEEE device function number, coded to the shared
-- ANSI_DEVICE_NUMBER domain (same field PROTECTIVE_RELAY carries). A self-contained
-- recloser performs its own protection (typically 50/51 + 79 auto-reclose) without
-- a separate relay, so it should be able to declare its primary function.
--
-- This inserts METADATA only. The physical gis.mv_recloser.device_number column is
-- created by the compiler, so after this migration RECOMPILE the class:
--     task compile CODE=MV_RECLOSER
--
-- The field is NULLABLE (optional): additive-safe on an already-populated table
-- (the compiler can't add a NOT NULL column to existing rows), and semantically a
-- recloser may be modelled before its primary function is set.
--
-- The INNER JOIN on the domain is deliberate: if ANSI_DEVICE_NUMBER somehow does
-- not exist, the field is simply NOT created (a loud, visible absence) rather than
-- created as a silent free-text column. In any DB with protection seeded the domain
-- is present, so the field is created bound.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'device_number', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d ON d.code = 'ANSI_DEVICE_NUMBER'
JOIN metadata.field_groups fg ON fg.code = 'protection'
WHERE fc.code = 'MV_RECLOSER'
ON CONFLICT (feature_class_id, name) DO NOTHING;
