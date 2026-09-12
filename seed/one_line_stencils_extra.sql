-- seed/one_line_stencils_extra.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same as seed/ansi_stencils.sql (its sibling: the mechanism
-- -- symbology_icons + diagram_stencils -- is unchanged, the palette just grows
-- by adding rows). Extends the one-line stencil palette with three families a
-- distribution/substation schematic commonly needs but ansi_stencils.sql didn't
-- cover:
--   * Cable        -- cable termination (stress cone), straight joint, tee joint
--   * Metering     -- kWh meter, metering cubicle, summation CT
--   * Transformers -- auto-transformer, earthing (zig-zag) transformer,
--                     on-load tap changer, neutral earthing resistor (NER)
--
-- Same authoring rules as ansi_stencils.sql: every SVG carries explicit
-- width/height (migration 0023's Chromium intrinsic-size bug), single-colour art
-- via the {{COLOR}} placeholder so a stamped glyph tints to its element's
-- style.color, drawn upright (rotation 0 = "up"). Both parts idempotent
-- (ON CONFLICT DO NOTHING), so re-applying never clobbers a glyph or catalog row
-- edited in place.
--
-- DECORATION ONLY for now: no promote_class is set here, so these are pure
-- sketch furniture (like most of the ANSI palette). Wiring a glyph to promote
-- into a real feature is a separate guarded UPDATE (see ansi_stencils.sql /
-- mv_symbology_example.sql) and only makes sense once the target class is
-- geometry_optional + is_diagram_placeable -- e.g. metering-cubicle ->
-- MV_METERING_CUBICLE, earthing-transformer -> EARTHING_XFMR, summation-ct ->
-- MV_CT would all be candidates. Left for a follow-up so a mapping is never
-- added against a class that would reject the promote at request time.
--
-- Apply with psql after seed/ansi_stencils.sql (shares the icon_ref namespace;
-- these refs don't overlap it). Nothing to compile -- stencils resolve entirely
-- client-side.

INSERT INTO metadata.symbology_icons (icon_ref, svg) VALUES
  ('cable-termination',    '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="2" x2="12" y2="7"/><polygon points="11,7 13,7 15,16 9,16" fill="{{COLOR}}" stroke="none"/><line x1="12" y1="16" x2="12" y2="22"/></g></svg>'),
  ('cable-joint-straight', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="2" x2="12" y2="22"/><polygon points="12,7 15,10 15,14 12,17 9,14 9,10"/></g></svg>'),
  ('cable-joint-tee',      '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round"><line x1="12" y1="2" x2="12" y2="22"/><line x1="12" y1="12" x2="21" y2="12"/><circle cx="12" cy="12" r="2.4" fill="{{COLOR}}" stroke="none"/></g></svg>'),
  ('meter-kwh',            '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6"><circle cx="12" cy="12" r="9"/><text x="12" y="15" font-size="7" font-family="sans-serif" font-weight="600" text-anchor="middle" fill="{{COLOR}}" stroke="none">Wh</text></g></svg>'),
  ('metering-cubicle',     '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6"><rect x="4" y="4" width="16" height="16" rx="1.5"/><circle cx="12" cy="12" r="4.5"/><text x="12" y="14.4" font-size="5.5" font-family="sans-serif" text-anchor="middle" fill="{{COLOR}}" stroke="none">M</text></g></svg>'),
  ('summation-ct',         '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6"><line x1="3" y1="7" x2="21" y2="7"/><circle cx="12" cy="14" r="5.5"/><text x="12" y="16.5" font-size="6" font-family="sans-serif" font-weight="600" text-anchor="middle" fill="{{COLOR}}" stroke="none">&#931;</text></g></svg>'),
  ('auto-transformer',     '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round"><circle cx="11" cy="13" r="7"/><line x1="11" y1="2" x2="11" y2="6"/><line x1="11" y1="20" x2="11" y2="22"/><line x1="16" y1="10" x2="21" y2="10"/></g></svg>'),
  ('earthing-transformer', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="2" x2="12" y2="5"/><polyline points="12,5 8,8 16,11 8,14 16,17 12,19"/><line x1="8" y1="20" x2="16" y2="20"/><line x1="9.5" y1="22" x2="14.5" y2="22"/></g></svg>'),
  ('tap-changer',          '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="9" r="5.5"/><circle cx="12" cy="15" r="5.5"/><line x1="5" y1="20" x2="20" y2="6"/><polyline points="20,10.5 20,6 15.5,6"/></g></svg>'),
  ('ner',                  '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round"><line x1="12" y1="2" x2="12" y2="6"/><rect x="8.5" y="6" width="7" height="9"/><line x1="12" y1="15" x2="12" y2="17"/><line x1="8" y1="17" x2="16" y2="17"/><line x1="9.5" y1="19.5" x2="14.5" y2="19.5"/><line x1="11" y1="22" x2="13" y2="22"/></g></svg>')
ON CONFLICT (icon_ref) DO NOTHING;

INSERT INTO metadata.diagram_stencils (icon_ref, label, category, sort_order) VALUES
  ('cable-termination',    'Cable termination',              'Cable',        610),
  ('cable-joint-straight', 'Straight cable joint',           'Cable',        620),
  ('cable-joint-tee',      'Tee cable joint',                'Cable',        630),
  ('meter-kwh',            'kWh meter',                      'Metering',     710),
  ('metering-cubicle',     'Metering cubicle',               'Metering',     720),
  ('summation-ct',         'Summation CT',                   'Metering',     730),
  ('auto-transformer',     'Auto-transformer',               'Transformers', 122),
  ('tap-changer',          'On-load tap changer',            'Transformers', 124),
  ('earthing-transformer', 'Earthing (zig-zag) transformer', 'Grounding',    25),
  ('ner',                  'Neutral earthing resistor',      'Grounding',    27)
ON CONFLICT (icon_ref) DO NOTHING;
