-- 0074_add_feature_class_category.sql
-- Purpose: give a feature class an admin-defined **category** -- a free
-- display grouping ("MV plant", "LV service", "Structures", ...) used to
-- section the map's "New feature" menu, which had grown into one long flat
-- scroll of every drawable class. Purely a presentation grouping: it does
-- not touch topology, voltage, or the compiler.
--
-- Why a plain nullable text column rather than a lookup table: the category
-- is a label an admin types, not a keyed entity anything else references --
-- there are no per-category rules, no FKs, nothing joins on it. Distinct
-- values are simply the set of categories that exist. NULL means
-- "uncategorised", which the menu buckets under an "Other" section. This
-- mirrors `network` (migration 0064): pure metadata the compiler's DDL
-- never reads, editable at any class status, riding along on metadata.Load's
-- struct-model select. seed/feature_class_categories_example.sql seeds a
-- sensible default (the MV/LV network) for classes that already carry a
-- network; everything else stays NULL until an admin sets it.

ALTER TABLE metadata.feature_classes
    ADD COLUMN category text;

COMMENT ON COLUMN metadata.feature_classes.category IS 'Admin-defined display grouping for the map "New feature" menu (e.g. "MV plant", "LV service"). Free text, nullable (NULL = uncategorised, shown under "Other"). Pure presentation metadata -- unrelated to network_role (topology) or network (voltage); the compiler never reads it.';
