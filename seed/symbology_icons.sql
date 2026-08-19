-- seed/symbology_icons.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose (same reasoning as the other seed files). Seeds
-- metadata.symbology_icons (migrations/0023) with the five hand-authored
-- shapes that used to live only in web/src/lib/symbology-icons.ts, so the
-- existing seeded symbology rules (seed/symbology_example.sql, which
-- reference icon_ref circle/square/triangle/triangle-outline/diamond)
-- keep resolving to the exact same markup they always did -- now from the
-- DB rather than a hardcoded frontend registry. The frontend keeps these
-- same five as a fallback, so this seed reproduces, not replaces, the
-- prior behavior; real utility artwork gets added as new rows here (or via
-- a future admin editor) without touching the frontend.
--
-- Each {{COLOR}} is substituted with a rule's resolved color at render
-- time (iconDataUri). triangle-outline is the one deliberately hollow
-- shape (stroke, no fill) -- DIST_SWITCH's OPEN state, a real shape change
-- vs. the solid CLOSED triangle, not a color-only difference. Every SVG
-- carries explicit width/height (not just a viewBox) -- see migrations/0023's
-- column comment for the Chromium intrinsic-size bug that requires it.
--
-- Idempotent: ON CONFLICT DO NOTHING, so re-applying never clobbers an
-- icon that's since been edited in place.

INSERT INTO metadata.symbology_icons (icon_ref, svg) VALUES
  ('circle', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" fill="{{COLOR}}"/></svg>'),
  ('square', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="2" fill="{{COLOR}}"/></svg>'),
  ('triangle', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><polygon points="12,3 21,20 3,20" fill="{{COLOR}}"/></svg>'),
  ('triangle-outline', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><polygon points="12,3 21,20 3,20" fill="none" stroke="{{COLOR}}" stroke-width="2.5" stroke-linejoin="round"/></svg>'),
  ('diamond', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><polygon points="12,2 22,12 12,22 2,12" fill="{{COLOR}}"/></svg>')
ON CONFLICT (icon_ref) DO NOTHING;
