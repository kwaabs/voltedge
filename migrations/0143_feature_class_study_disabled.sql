-- 0143_feature_class_study_disabled.sql
--
-- Admin prune layer for analysis-study applicability (see api/internal/studies).
-- Which studies a feature class offers is decided in code by role (the technical
-- floor); this table lets an admin turn an otherwise-applicable study OFF for a
-- class they don't use it on. Prune-only: a row removes a study from a class's
-- effective set. There is no "enable" row -- a study the floor forbids can never
-- be switched on, so no one can run arc-flash on a meter.
--
-- study_id is a stable code from the studies registry (not an FK -- the registry
-- lives in Go, not the database). Unknown ids are simply ignored when the
-- effective set is computed, so a registry rename never corrupts this table.
CREATE TABLE metadata.feature_class_study_disabled (
    feature_class_id uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    study_id         text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (feature_class_id, study_id)
);

COMMENT ON TABLE metadata.feature_class_study_disabled IS
    'Per-class admin prune of analysis studies: one row = this study is hidden/refused for this class even though its role makes it applicable. The applicable set itself is code-defined (api/internal/studies).';
