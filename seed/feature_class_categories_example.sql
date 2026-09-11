-- seed/feature_class_categories_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
--
-- Gives every feature class a sensible starting **category** (migration
-- 0074), the admin-defined grouping that sections the map's "New feature"
-- menu. Without this the menu still works -- every class simply lands in the
-- single "Other" bucket -- so this is a convenience default, not a
-- dependency of the feature.
--
-- Strategy: seed the category from the class's existing `network` (MV/LV,
-- migration 0064) for any class that carries one and has no category yet.
-- That immediately splits the bulk of the electrical catalog into an "MV"
-- and an "LV" section. Everything structural / cross-network (POLE, BAY,
-- SUBSTATION, STRUCTURE, annotations, ...) has no network, so it stays
-- uncategorised ("Other") for an admin to name deliberately. Idempotent: the
-- `category IS NULL` guard means re-applying never overwrites a category an
-- admin has since set by hand.
--
-- Refine per-class afterwards in Admin -> Feature classes -> (a class) ->
-- Class settings -> Menu category, e.g. splitting "MV" into "MV plant",
-- "MV switchgear", "MV lines". Pure presentation metadata: no compile, no
-- effect on topology or the engine.

UPDATE metadata.feature_classes
SET category = network
WHERE category IS NULL
  AND network IS NOT NULL;
