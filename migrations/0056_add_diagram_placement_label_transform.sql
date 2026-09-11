-- Phase 31: per-placement diagram label transform.
--
-- Until now a placed feature's name label was pinned to a hardcoded spot
-- below/at its glyph and could only be hidden (migration 0054). These four
-- columns let each label be repositioned, rotated and resized independently
-- of the glyph, while still travelling with it.
--
--   label_dx / label_dy  offset from the label's natural anchor, in the
--                        glyph-LOCAL frame (so the offset rotates with the
--                        glyph -- "offset follows glyph"). Default 0 = the
--                        existing pinned position, so every current label is
--                        unchanged.
--   label_rotation       degrees, applied to the label text on top of the
--                        glyph's own rotation. Default 0.
--   label_scale          multiplier on the label font size. Default 1.
--
-- All geometry-free and owned by PATCH /diagrams/{id}/placements/{id}, never
-- the place/move upsert, so dragging or rotating the feature never resets a
-- label the user has arranged (same discipline as label_hidden).

ALTER TABLE runtime.diagram_placements
  ADD COLUMN label_dx       double precision NOT NULL DEFAULT 0,
  ADD COLUMN label_dy       double precision NOT NULL DEFAULT 0,
  ADD COLUMN label_rotation double precision NOT NULL DEFAULT 0,
  ADD COLUMN label_scale    double precision NOT NULL DEFAULT 1;
