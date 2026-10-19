-- seed/fuse_curves_library.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
--
-- The deployment's FUSE CURVE LIBRARY. A fuse's curve_ref field (MV_FUSE,
-- seed/protection_curves_example.sql) stores a metadata.protection_curves.code;
-- the feature form's curve picker lists every ampere-quantity curve here, and
-- the coordination engine (api/internal/powerflow/curves.go) reads a fuse's
-- melt/clear points off the rows this file loads. So: a fuse size you want to
-- be pickable has to exist here first. seed/protection_curves_example.sql only
-- ships ONE example fuse (FUSE_100T_EXAMPLE); this file is where real sizes go.
--
-- Each fuse curve is TWO bounding bands at a set of (current_A, time_s) points:
--   * minimum_melt -- the earliest (lowest-time) the element starts to melt at
--                     a given current. Coordination uses this as the fuse's
--                     fastest credible operate time when it is the UPSTREAM/
--                     backup device.
--   * total_clear  -- the latest the fuse has fully cleared the fault. Used when
--                     the fuse is the DOWNSTREAM/primary device being protected.
-- More points = a truer curve; the engine log-log interpolates between them.
--
-- !!! The numbers below are ILLUSTRATIVE, not a manufacturer sheet !!!
-- They are a plausible shape so a 400 A fuse is pickable and the study runs.
-- For an accurate protection study REPLACE each band's points with the melt /
-- clear values read off your actual fuse datasheet (every vendor publishes
-- them). The code + rating stay; only the points change.
--
-- Depends on:
--   * migration 0076                    (protection_curves / _points tables)
--   * seed/protection_curves_example.sql (not strictly required, but that's
--     where the MV_FUSE curve_ref field itself is defined)
--
-- Apply with psql (no compile needed -- these are plain metadata rows the
-- engine reads at run time; nothing to recompile):
--   task seed-fuse-curves
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + a curve-point spot check are
-- still owed to call it verified.

-- ---------------------------------------------------------------------------
-- Curve headers. quantity = 'ampere' is what tags these as FUSE curves (vs a
-- relay curve's 'multiple') and what the form's picker filters on. Add a row
-- here per fuse size you carry; the name is what shows in the dropdown.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.protection_curves (code, name, standard, quantity) VALUES
    ('FUSE_400A_CL_EXAMPLE', 'Example 400 A current-limiting fuse', 'FUSE', 'ampere')
ON CONFLICT (code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Points. REPLACE with datasheet values -- these are an illustrative 400 A
-- current-limiting shape only (a ~4x current scaling of the example 100 A T
-- fuse, so the coordination path has real data to interpolate).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.protection_curve_points (curve_id, band, current, time_s)
SELECT c.id, v.band, v.current, v.time_s
FROM metadata.protection_curves c
JOIN (VALUES
    ('minimum_melt',  800.0,   300.0),
    ('minimum_melt',  1200.0,  30.0),
    ('minimum_melt',  2000.0,  3.5),
    ('minimum_melt',  4000.0,  0.30),
    ('minimum_melt',  8000.0,  0.045),
    ('minimum_melt',  16000.0, 0.011),
    ('total_clear',   800.0,   600.0),
    ('total_clear',   1200.0,  60.0),
    ('total_clear',   2000.0,  7.0),
    ('total_clear',   4000.0,  0.60),
    ('total_clear',   8000.0,  0.090),
    ('total_clear',   16000.0, 0.022)
) AS v(band, current, time_s) ON true
WHERE c.code = 'FUSE_400A_CL_EXAMPLE'
ON CONFLICT (curve_id, band, current) DO NOTHING;
