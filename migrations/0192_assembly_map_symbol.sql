-- 0192_assembly_map_symbol.sql
-- Purpose: let a PLACED assembly carry its own symbol on the geographic map, without
-- turning the assembly into a feature class. An assembly stays what it is -- multiple
-- real feature classes composed together (a geometric ANCHOR + geometry-less
-- components, see migration 0093 / api/internal/assembly). Only the ANCHOR draws on
-- the map, styled by its own real class. To give the ANCHOR a distinct glyph WHEN it
-- was placed as part of an assembly, we add:
--
--   1. a per-template MAP SYMBOL on metadata.assembly_templates (icon_ref + color),
--      authored once per recipe; and
--   2. a SYSTEM COLUMN `assembly_ref` on every feature table (exactly like
--      external_ref / lifecycle_status -- added by the compiler, served by Martin),
--      stamped at placement with the template `code`. A `map`-context symbology rule
--      keyed on assembly_ref = <code> then renders the anchor's MAP glyph, leaving the
--      `diagram` context (the internal/mimic symbology) untouched.
--
-- The anchor's class, subtype and attributes are unchanged; assembly_ref is just a
-- marker, not a new class or subtype. Because the Martin tile function (migration 0038
-- gis.sync_tile_function) rebuilds from information_schema.columns, re-syncing it after
-- adding the column makes assembly_ref an MVT property the client symbology matcher can
-- target (web/src/lib/symbology.ts). The compiler slice that follows adds the column to
-- newly compiled classes; this migration backfills it onto every already-compiled one.

-- 1. The per-template map symbol. icon_ref references a metadata.symbology_icons.ref
-- (the same library the admin symbology editor uses); color is a hex string. Both
-- NULL = "no assembly map symbol" -> the anchor keeps its plain class symbology.
ALTER TABLE metadata.assembly_templates
    ADD COLUMN IF NOT EXISTS map_icon_ref text,
    ADD COLUMN IF NOT EXISTS map_color    text;

COMMENT ON COLUMN metadata.assembly_templates.map_icon_ref IS 'Optional: the symbology icon (metadata.symbology_icons.ref) an anchor placed by this assembly template shows on the geographic MAP. NULL = no assembly-specific map symbol (anchor keeps its class symbology). Drives a managed map-context symbology_rules row keyed on the anchor''s assembly_ref.';
COMMENT ON COLUMN metadata.assembly_templates.map_color IS 'Optional hex color for the assembly''s map symbol (pairs with map_icon_ref).';

-- 2. The marker system column on every compiled feature table, plus a tile-function
-- re-sync so Martin serves it. Added nullable with no default -- every existing row is
-- a non-assembly (or pre-existing) feature and reads back NULL, i.e. no assembly rule
-- matches it. Mirrors the external_ref backfill shape (0145) + the tile re-sync loop
-- (0038). A geometry-less table (no geom column) has no tile function, so it only gets
-- the column, not a re-sync.
DO $$
DECLARE
    r   record;
    tbl text;
BEGIN
    FOR r IN
        SELECT physical_table, srid
        FROM metadata.feature_classes
        WHERE physical_table IS NOT NULL
    LOOP
        tbl := split_part(r.physical_table, '.', 2);
        EXECUTE format('ALTER TABLE %s ADD COLUMN IF NOT EXISTS assembly_ref text', r.physical_table);
        IF r.srid IS NOT NULL AND EXISTS (
            SELECT 1 FROM information_schema.columns
            WHERE table_schema = 'gis' AND table_name = tbl AND column_name = 'geom'
        ) THEN
            PERFORM gis.sync_tile_function(tbl, r.srid);
        END IF;
    END LOOP;
END;
$$;
