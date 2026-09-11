-- 0076_create_protection_curves.sql
-- Purpose: the tabulated time-current curve (TCC) catalog the coordination
-- engine (Protection Slice C) reads. Two tables:
--
--   * protection_curves       -- one row per named curve (a relay IDMT
--                                characteristic, or a specific fuse's
--                                melt/clear curve).
--   * protection_curve_points -- the (current, time) samples that DEFINE
--                                that curve. This is the whole point of the
--                                "tabulated, not parametric" decision: the
--                                curve IS its stored points, and the engine
--                                interpolates between them -- it never
--                                evaluates an IDMT formula at runtime. A real
--                                manufacturer fuse curve is entered here as
--                                measured points exactly like a standard
--                                relay curve is.
--
-- quantity says how a point's `current` axis is read, so relay and fuse
-- curves can share one table:
--   * 'multiple' -- current as a MULTIPLE of the relay's pickup setting, and
--                   `time_s` is the operate time at time_dial = 1. The engine
--                   scales: t = time_dial * interp(I / pickup_current_a).
--                   This is how a single stored IEC/IEEE shape serves every
--                   relay regardless of its pickup/TMS settings.
--   * 'ampere'   -- current as ABSOLUTE amps; `time_s` is read directly.
--                   Fuse melt/clear curves are intrinsic to the fuse, not
--                   scaled by any setting.
--
-- band lets a fuse carry its two bounding curves in one logical curve:
--   * 'operate'      -- the single operating curve of a relay (default).
--   * 'minimum_melt' -- a fuse's lower bound (starts to melt).
--   * 'total_clear'  -- a fuse's upper bound (fully cleared).
-- Coordination checks a downstream fuse's total_clear against an upstream
-- device's operate/minimum_melt.

CREATE TABLE metadata.protection_curves (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code        text NOT NULL UNIQUE,
    name        text NOT NULL,
    standard    text NOT NULL,
    quantity    text NOT NULL CHECK (quantity IN ('multiple', 'ampere')),
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE metadata.protection_curve_points (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    curve_id    uuid NOT NULL REFERENCES metadata.protection_curves(id) ON DELETE CASCADE,
    band        text NOT NULL DEFAULT 'operate' CHECK (band IN ('operate', 'minimum_melt', 'total_clear')),
    current     numeric NOT NULL CHECK (current > 0),
    time_s      numeric NOT NULL CHECK (time_s >= 0),
    UNIQUE (curve_id, band, current)
);

CREATE INDEX idx_protection_curve_points_curve ON metadata.protection_curve_points (curve_id, band, current);

COMMENT ON TABLE metadata.protection_curves IS 'A named time-current curve (TCC): a relay IDMT characteristic (quantity=multiple, scaled by pickup + time_dial) or a specific fuse melt/clear curve (quantity=ampere, absolute). The coordination engine interpolates the stored points -- it never evaluates an IDMT formula. See migration 0076.';
COMMENT ON COLUMN metadata.protection_curves.quantity IS 'multiple = current axis is a multiple of relay pickup, time is at time_dial=1 (engine scales by pickup + time_dial); ampere = current axis is absolute amps, time read directly (fuses).';
COMMENT ON COLUMN metadata.protection_curve_points.band IS 'operate = a relay operating curve (default); minimum_melt / total_clear = a fuse''s two bounding curves.';
