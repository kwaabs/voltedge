-- seed/ansi_stencils.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same as seed/symbology_icons.sql. Seeds the Phase 24
-- Slice 2 symbol stencil library (docs/phase-24-diagram-templates.md): a
-- curated set of ANSI/IEEE one-line schematic glyphs. Grown after Slice 5 with
-- the common protection/switching content a real one-line needs -- circuit
-- breaker, disconnect/load-break/grounding switches, three-winding transformer,
-- series components (capacitor/reactor/battery/motor), and ANSI device-number
-- relay circles (50/51/87/67/27/59/81/79/86/25). Pure content: the mechanism
-- (symbology_icons + diagram_stencils) is unchanged, so the library grows by
-- adding rows here -- never a migration, never code.
--
-- Two parts, both idempotent (ON CONFLICT DO NOTHING, so re-applying never
-- clobbers a glyph or catalog row edited in place):
--   1. metadata.symbology_icons  -- the SVG artwork (migration 0023). This is
--      the SAME asset table feature symbology rules resolve against, so every
--      glyph here is reusable by a render_context = 'diagram' rule too, not
--      only by the stencil palette (the "both" scope for Slice 2).
--   2. metadata.diagram_stencils -- marks each glyph as a palette stencil with
--      a label, category, and order (migration 0048).
--
-- Every SVG carries explicit width/height (not just a viewBox) -- see
-- migrations/0023's column comment for the Chromium intrinsic-size bug that
-- requires it. Single-colour glyphs use the {{COLOR}} placeholder so a stamped
-- symbol tints to its element's style.color (iconDataUri), exactly like a
-- feature icon tints to its rule's colour. Glyphs are drawn upright (rotation
-- 0 points "up"), matching how the canvas rotates placement icons, so the
-- symbol rotate handle behaves the same way.
--
-- Also wires the first stencil->feature promotion mapping (Phase 25, migration
-- 0051): the ansi-relay stencil promotes to PROTECTIVE_RELAY, the one
-- geometry_type='none' diagram-native class -- so double-clicking a stamped
-- relay glyph offers to turn it into a real relay feature (see the guarded
-- UPDATEs at the foot of this file). The switching/protection/instrument glyphs
-- (breaker/disconnect/load-break/fuse/arrester/CT/VT) get their promote_class in
-- seed/mv_symbology_example.sql (Phase 27), not here. Every other glyph stays
-- decoration until a matching class is authored and mapped the same way.
--
-- Apply with psql after migrations 0001-0051 (and after seed/symbology_icons.sql,
-- though these icon_refs don't overlap that file's five shapes).

INSERT INTO metadata.symbology_icons (icon_ref, svg) VALUES
  ('ansi-ground', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round"><line x1="12" y1="3" x2="12" y2="12"/><line x1="5" y1="12" x2="19" y2="12"/><line x1="7.5" y1="16" x2="16.5" y2="16"/><line x1="10" y1="20" x2="14" y2="20"/></g></svg>'),
  ('ansi-chassis-ground', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round"><line x1="12" y1="3" x2="12" y2="13"/><line x1="6" y1="13" x2="18" y2="13"/><line x1="6" y1="13" x2="3" y2="19"/><line x1="12" y1="13" x2="9" y2="19"/><line x1="18" y1="13" x2="15" y2="19"/></g></svg>'),
  ('ansi-node', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><circle cx="12" cy="12" r="4" fill="{{COLOR}}"/></svg>'),
  ('ansi-terminal', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><circle cx="12" cy="12" r="4.5" fill="none" stroke="{{COLOR}}" stroke-width="1.8"/></svg>'),
  ('ansi-relay', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><circle cx="12" cy="12" r="8" fill="none" stroke="{{COLOR}}" stroke-width="1.8"/></svg>'),
  ('ansi-fuse', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round"><line x1="12" y1="2" x2="12" y2="22"/><rect x="8" y="6" width="8" height="12" rx="1"/></g></svg>'),
  ('ansi-surge-arrester', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="2" x2="12" y2="6"/><rect x="8" y="6" width="8" height="9"/><polyline points="9.5,9 12,12.5 14.5,9"/><line x1="12" y1="15" x2="12" y2="18"/><line x1="8" y1="18" x2="16" y2="18"/><line x1="9.5" y1="20.5" x2="14.5" y2="20.5"/></g></svg>'),
  ('ansi-ct', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.8"><line x1="3" y1="7" x2="21" y2="7"/><circle cx="12" cy="14" r="5"/></g></svg>'),
  ('ansi-vt', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.8" stroke-linecap="round"><line x1="12" y1="2" x2="12" y2="6"/><circle cx="12" cy="12" r="6"/><line x1="12" y1="18" x2="12" y2="22"/></g></svg>'),
  ('ansi-transformer-2w', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.8"><circle cx="12" cy="9" r="6"/><circle cx="12" cy="15" r="6"/></g></svg>'),
  ('ansi-delta', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><polygon points="12,5 19,18 5,18" fill="none" stroke="{{COLOR}}" stroke-width="1.8" stroke-linejoin="round"/></svg>'),
  ('ansi-wye', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.8" stroke-linecap="round"><line x1="12" y1="12" x2="12" y2="4"/><line x1="12" y1="12" x2="5" y2="19"/><line x1="12" y1="12" x2="19" y2="19"/></g></svg>'),
  ('ansi-generator', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.8"><circle cx="12" cy="12" r="8"/><path d="M8 13 q2 -5 4 0 t4 0" stroke-linecap="round"/></g></svg>'),
  ('ansi-arrow', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><path d="M12 3 L17 11 H13.5 V21 H10.5 V11 H7 Z" fill="{{COLOR}}"/></svg>'),
  ('ansi-current-arrow', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><line x1="12" y1="21" x2="12" y2="5"/><polyline points="8,10 12,4 16,10"/></g></svg>'),
  ('ansi-north-arrow', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g stroke="{{COLOR}}" stroke-width="1.2" fill="{{COLOR}}" stroke-linejoin="round"><polygon points="12,3 15.5,13 12,10.5 8.5,13"/><text x="12" y="21.5" font-size="6" text-anchor="middle" stroke="none">N</text></g></svg>'),
  ('ansi-breaker', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round" fill="{{COLOR}}"><line x1="12" y1="2" x2="12" y2="22"/><polygon points="8,7 16,7 12,12"/><polygon points="8,17 16,17 12,12"/></g></svg>'),
  ('ansi-disconnect', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.8" stroke-linecap="round"><line x1="12" y1="2" x2="12" y2="7"/><circle cx="12" cy="7" r="1.1" fill="{{COLOR}}" stroke="none"/><line x1="12" y1="7" x2="17.5" y2="16"/><circle cx="12" cy="17" r="1.1" fill="{{COLOR}}" stroke="none"/><line x1="12" y1="17" x2="12" y2="22"/></g></svg>'),
  ('ansi-load-break', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round"><line x1="12" y1="2" x2="12" y2="7"/><circle cx="12" cy="7" r="1.1" fill="{{COLOR}}" stroke="none"/><line x1="12" y1="7" x2="16" y2="15"/><rect x="9.5" y="15" width="5" height="4" rx="0.6"/><line x1="12" y1="19" x2="12" y2="22"/></g></svg>'),
  ('ansi-grounding-switch', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round"><line x1="8" y1="3" x2="8" y2="8"/><circle cx="8" cy="8" r="1" fill="{{COLOR}}" stroke="none"/><line x1="8" y1="8" x2="15" y2="14"/><line x1="15" y1="12" x2="15" y2="16"/><line x1="11" y1="16" x2="19" y2="16"/><line x1="12.5" y1="18.5" x2="17.5" y2="18.5"/><line x1="14" y1="21" x2="16" y2="21"/></g></svg>'),
  ('ansi-transformer-3w', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.5"><circle cx="9" cy="9" r="5"/><circle cx="15" cy="9" r="5"/><circle cx="12" cy="15" r="5"/></g></svg>'),
  ('ansi-capacitor', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.8" stroke-linecap="round"><line x1="12" y1="3" x2="12" y2="10"/><line x1="5" y1="10" x2="19" y2="10"/><line x1="5" y1="14" x2="19" y2="14"/><line x1="12" y1="14" x2="12" y2="21"/></g></svg>'),
  ('ansi-reactor', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round"><line x1="12" y1="2" x2="12" y2="5"/><path d="M12 5 c4 0 4 4 0 4 c4 0 4 4 0 4 c4 0 4 4 0 4"/><line x1="12" y1="17" x2="12" y2="22"/></g></svg>'),
  ('ansi-battery', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.6" stroke-linecap="round"><line x1="12" y1="3" x2="12" y2="7"/><line x1="6" y1="7" x2="18" y2="7"/><line x1="9" y1="10" x2="15" y2="10"/><line x1="6" y1="13" x2="18" y2="13"/><line x1="9" y1="16" x2="15" y2="16"/><line x1="12" y1="16" x2="12" y2="21"/></g></svg>'),
  ('ansi-motor', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g fill="none" stroke="{{COLOR}}" stroke-width="1.8"><circle cx="12" cy="12" r="8"/><text x="12" y="15.5" font-size="9" font-family="sans-serif" font-weight="600" text-anchor="middle" fill="{{COLOR}}" stroke="none">M</text></g></svg>'),
  ('ansi-dev-50', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g><circle cx="12" cy="12" r="9" fill="none" stroke="{{COLOR}}" stroke-width="1.6"/><text x="12" y="15.5" font-size="9" font-family="sans-serif" font-weight="600" text-anchor="middle" fill="{{COLOR}}" stroke="none">50</text></g></svg>'),
  ('ansi-dev-51', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g><circle cx="12" cy="12" r="9" fill="none" stroke="{{COLOR}}" stroke-width="1.6"/><text x="12" y="15.5" font-size="9" font-family="sans-serif" font-weight="600" text-anchor="middle" fill="{{COLOR}}" stroke="none">51</text></g></svg>'),
  ('ansi-dev-87', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g><circle cx="12" cy="12" r="9" fill="none" stroke="{{COLOR}}" stroke-width="1.6"/><text x="12" y="15.5" font-size="9" font-family="sans-serif" font-weight="600" text-anchor="middle" fill="{{COLOR}}" stroke="none">87</text></g></svg>'),
  ('ansi-dev-67', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g><circle cx="12" cy="12" r="9" fill="none" stroke="{{COLOR}}" stroke-width="1.6"/><text x="12" y="15.5" font-size="9" font-family="sans-serif" font-weight="600" text-anchor="middle" fill="{{COLOR}}" stroke="none">67</text></g></svg>'),
  ('ansi-dev-27', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g><circle cx="12" cy="12" r="9" fill="none" stroke="{{COLOR}}" stroke-width="1.6"/><text x="12" y="15.5" font-size="9" font-family="sans-serif" font-weight="600" text-anchor="middle" fill="{{COLOR}}" stroke="none">27</text></g></svg>'),
  ('ansi-dev-59', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g><circle cx="12" cy="12" r="9" fill="none" stroke="{{COLOR}}" stroke-width="1.6"/><text x="12" y="15.5" font-size="9" font-family="sans-serif" font-weight="600" text-anchor="middle" fill="{{COLOR}}" stroke="none">59</text></g></svg>'),
  ('ansi-dev-81', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g><circle cx="12" cy="12" r="9" fill="none" stroke="{{COLOR}}" stroke-width="1.6"/><text x="12" y="15.5" font-size="9" font-family="sans-serif" font-weight="600" text-anchor="middle" fill="{{COLOR}}" stroke="none">81</text></g></svg>'),
  ('ansi-dev-79', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g><circle cx="12" cy="12" r="9" fill="none" stroke="{{COLOR}}" stroke-width="1.6"/><text x="12" y="15.5" font-size="9" font-family="sans-serif" font-weight="600" text-anchor="middle" fill="{{COLOR}}" stroke="none">79</text></g></svg>'),
  ('ansi-dev-86', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g><circle cx="12" cy="12" r="9" fill="none" stroke="{{COLOR}}" stroke-width="1.6"/><text x="12" y="15.5" font-size="9" font-family="sans-serif" font-weight="600" text-anchor="middle" fill="{{COLOR}}" stroke="none">86</text></g></svg>'),
  ('ansi-dev-25', '<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><g><circle cx="12" cy="12" r="9" fill="none" stroke="{{COLOR}}" stroke-width="1.6"/><text x="12" y="15.5" font-size="9" font-family="sans-serif" font-weight="600" text-anchor="middle" fill="{{COLOR}}" stroke="none">25</text></g></svg>')
ON CONFLICT (icon_ref) DO NOTHING;

INSERT INTO metadata.diagram_stencils (icon_ref, label, category, sort_order) VALUES
  ('ansi-ground',         'Earth ground',      'Grounding',    10),
  ('ansi-chassis-ground', 'Chassis ground',    'Grounding',    20),
  ('ansi-node',           'Junction node',     'Nodes',        30),
  ('ansi-terminal',       'Terminal',          'Nodes',        40),
  ('ansi-relay',          'Protective relay',  'Protection',   50),
  ('ansi-fuse',           'Fuse',              'Protection',   60),
  ('ansi-surge-arrester', 'Surge arrester',    'Protection',   70),
  ('ansi-ct',             'Current transformer', 'Instrument', 80),
  ('ansi-vt',             'Voltage transformer', 'Instrument', 90),
  ('ansi-transformer-2w', 'Two-winding transformer', 'Transformers', 100),
  ('ansi-delta',          'Delta winding',     'Transformers', 110),
  ('ansi-wye',            'Wye winding',       'Transformers', 120),
  ('ansi-generator',      'AC source',         'Sources',      130),
  ('ansi-arrow',          'Flow arrow',        'Annotation',   140),
  ('ansi-current-arrow',  'Current direction', 'Annotation',   150),
  ('ansi-north-arrow',    'North arrow',       'Annotation',   160),
  ('ansi-transformer-3w', 'Three-winding transformer', 'Transformers', 105),
  ('ansi-breaker',          'Circuit breaker (draw-out)', 'Switching',      210),
  ('ansi-disconnect',       'Disconnect switch',          'Switching',      220),
  ('ansi-load-break',       'Load-break switch',          'Switching',      230),
  ('ansi-grounding-switch', 'Grounding switch',           'Switching',      240),
  ('ansi-capacitor',        'Capacitor bank',             'Components',     310),
  ('ansi-reactor',          'Series reactor',             'Components',     320),
  ('ansi-battery',          'Battery',                    'Components',     330),
  ('ansi-motor',            'Motor',                      'Components',     340),
  ('ansi-dev-50',           'ANSI 50 - Instantaneous OC', 'Device numbers', 410),
  ('ansi-dev-51',           'ANSI 51 - Time OC',          'Device numbers', 420),
  ('ansi-dev-87',           'ANSI 87 - Differential',     'Device numbers', 430),
  ('ansi-dev-67',           'ANSI 67 - Directional OC',   'Device numbers', 440),
  ('ansi-dev-27',           'ANSI 27 - Undervoltage',     'Device numbers', 450),
  ('ansi-dev-59',           'ANSI 59 - Overvoltage',      'Device numbers', 460),
  ('ansi-dev-81',           'ANSI 81 - Frequency',        'Device numbers', 470),
  ('ansi-dev-79',           'ANSI 79 - Recloser',         'Device numbers', 480),
  ('ansi-dev-86',           'ANSI 86 - Lockout',          'Device numbers', 490),
  ('ansi-dev-25',           'ANSI 25 - Sync-check',       'Device numbers', 500)
ON CONFLICT (icon_ref) DO NOTHING;

-- Demonstrates the "both" scope: the same ANSI artwork the palette uses is
-- reusable by feature diagram symbology. Repoint PROTECTIVE_RELAY's
-- render_context = 'diagram' rule (seed/protective_relay_example.sql) from its
-- placeholder 'circle' to the real 'ansi-relay' glyph -- but only while it's
-- still that untouched default, so a hand-edited rule is never clobbered.
-- No-op if the PROTECTIVE_RELAY seed hasn't been applied.
UPDATE metadata.symbology_rules
SET style_def = jsonb_set(style_def, '{icon_ref}', '"ansi-relay"')
WHERE render_context = 'diagram'
  AND style_def->>'icon_ref' = 'circle'
  AND feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'PROTECTIVE_RELAY');

-- Phase 25 -- stencil->feature promotion. Map the ansi-relay stencil to the
-- PROTECTIVE_RELAY feature class so double-clicking a stamped relay glyph
-- offers to promote it into a real relay (create the feature at the symbol's
-- spot, place it, drop the glyph). Guarded to the untouched default: only sets
-- a mapping that's still NULL and only for a class that actually exists, so a
-- hand-authored promote_class is never clobbered and this is a no-op if the
-- PROTECTIVE_RELAY seed hasn't been applied.
UPDATE metadata.diagram_stencils
SET promote_class = 'PROTECTIVE_RELAY'
WHERE icon_ref = 'ansi-relay'
  AND promote_class IS NULL
  AND EXISTS (SELECT 1 FROM metadata.feature_classes WHERE code = 'PROTECTIVE_RELAY');

-- A promoted feature is a *placed* feature, so its class must pass placeFeature's
-- is_diagram_placeable gate (migration 0032). PROTECTIVE_RELAY is geometry-less
-- and lives only on diagrams, so it belongs on that list -- but 0032 seeds the
-- flag from network_role for classes present at migration time, and this class
-- is seeded later, so enable it here. Guarded so an admin who deliberately
-- turned it off isn't overridden on re-apply... it only flips a still-false flag.
UPDATE metadata.feature_classes
SET is_diagram_placeable = true
WHERE code = 'PROTECTIVE_RELAY'
  AND is_diagram_placeable = false;
