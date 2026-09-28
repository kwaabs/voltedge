-- seed/substation_templates.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same as seed/double_busbar_template.sql. Seeds TWO more
-- built-in reusable diagram layout skeletons into runtime.diagram_templates
-- (migration 0049), additive to the existing "Double busbar" template (which is
-- left untouched):
--
--   1. "33/11 kV substation"     -- a two-voltage transformer substation: a 33 kV
--                                   incoming bus feeding two transformer bays down
--                                   to a sectionalised 11 kV bus (with a bus
--                                   coupler) and its outgoing feeders.
--   2. "Switching station (33 kV)" -- a single-voltage switching station: one
--                                   sectionalised 33 kV bus, incomers + feeders,
--                                   NO transformer (a pure switching substation
--                                   transforms nothing -- one voltage, no
--                                   secondary; see seed/substation_yard_example.sql).
--
-- Both ride the existing template + bus/slot machinery, no new code path: the
-- payload is the same { "elements": [...] } shape saveAsTemplate writes, so
-- applyTemplate instantiates each exactly like a user-authored template. The
-- arrangements are PURELY SCHEMATIC -- heavy `bus` lines (promote to MV_BUSBAR),
-- thin `line` risers (promote to MV_CONDUCTOR), free `text` labels, and empty
-- `slot`s for the bays. They carry NO electrical or connectivity meaning; traces
-- are unaffected until real features are bound.
--
-- TYPED slots (unlike the portable "Double busbar" template, whose bays are
-- deliberately untyped because it can't know a deployment's feeder class code):
-- these slots set `slot_class` to VoltEdge's core, universal substation classes,
-- so the bind picker (diagram-canvas.tsx SlotBindPanel, which filters results to
-- r.featureClassCode === slot_class) is scoped to exactly the right kind:
--   * bus segments  -> promote to MV_BUSBAR   (line-drawn: MV_BUSBAR is
--                      diagram_geometry_type='line' + geometry_optional, so a bus
--                      renders as a segment and can be a geometry-less part).
--   * bay slots     -> slot_class "MV_SWITCHGEAR_PANEL" (a bay = a switchgear
--                      panel; the class is is_diagram_placeable, flipped true in
--                      seed/mv_switchgear_panel_example.sql).
--   * transformer   -> slot_class "POWER_XFMR" (junction, is_diagram_placeable +
--                      geometry_optional, seed/power_transformer_example.sql).
-- The (SUBSTATION x ONE_LINE) placement pairing is UNCONSTRAINED (no
-- metadata.diagram_placement_rules rows -- the only rules seeded are for
-- MV_SWITCHBOARD x BOARD_SCHEMATIC in seed/mv_switchgear_panel_example.sql, and
-- seed/substation_panel_symbology.sql notes SUBSTATION x ONE_LINE is
-- unconstrained), so every placeable slot class above binds with no extra rule.
--
-- Coordinates live in the diagram's own abstract local x/y space (the canvas
-- auto-fits its viewBox to content, so absolute magnitudes don't matter, only the
-- layout's proportions; y grows downward). z_index orders the draw: buses/risers
-- behind (0), slots (20), labels (30).
--
-- Idempotent: each INSERT is guarded on its template name, so re-applying never
-- inserts a second copy or clobbers one edited in place. Dependencies: migrations
-- 0001-0049 applied. For binds to resolve, the slot classes (MV_BUSBAR,
-- MV_SWITCHGEAR_PANEL, POWER_XFMR) should exist and be placeable, and the diagram
-- must be rooted on a SUBSTATION container (flipped by seed/diagram_example.sql);
-- but the template rows themselves are class-free JSON and load regardless.

-- --------------------------------------------------- 33/11 kV substation -----
-- 33 kV bus (y=180) with two incomer bays feeding in from above (x=200/800).
-- Two transformer strings (x=400/600) drop from the 33 kV bus through an HV
-- switchgear-panel bay and a POWER_XFMR down to a sectionalised 11 kV bus
-- (y=620). The 11 kV bus is two segments (100..470 and 530..900) tied by a bus
-- coupler bay at x=500 -- each transformer lands on one section (400 in A, 600 in
-- B). Four outgoing feeder bays hang below (x=180/340 on A, 660/820 on B).
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT
  '33/11 kV substation',
  'Two-voltage transformer substation: a 33 kV bus with incomers, two transformer bays (HV panel + transformer), and a sectionalised 11 kV bus with a coupler and outgoing feeders. Typed slots; purely schematic.',
  $$
  {
    "elements": [
      { "kind": "bus", "x": 100, "y": 180, "rotation": 0, "z_index": 0, "path": [[100,180],[900,180]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_BUSBAR"} },

      { "kind": "line", "x": 200, "y": 70,  "rotation": 0, "z_index": 0, "path": [[200,70],[200,180]],  "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },
      { "kind": "line", "x": 800, "y": 70,  "rotation": 0, "z_index": 0, "path": [[800,70],[800,180]],  "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },

      { "kind": "line", "x": 400, "y": 180, "rotation": 0, "z_index": 0, "path": [[400,180],[400,250]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },
      { "kind": "line", "x": 400, "y": 290, "rotation": 0, "z_index": 0, "path": [[400,290],[400,360]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },
      { "kind": "line", "x": 400, "y": 440, "rotation": 0, "z_index": 0, "path": [[400,440],[400,620]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },
      { "kind": "line", "x": 600, "y": 180, "rotation": 0, "z_index": 0, "path": [[600,180],[600,250]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },
      { "kind": "line", "x": 600, "y": 290, "rotation": 0, "z_index": 0, "path": [[600,290],[600,360]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },
      { "kind": "line", "x": 600, "y": 440, "rotation": 0, "z_index": 0, "path": [[600,440],[600,620]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },

      { "kind": "bus", "x": 100, "y": 620, "rotation": 0, "z_index": 0, "path": [[100,620],[470,620]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_BUSBAR"} },
      { "kind": "bus", "x": 530, "y": 620, "rotation": 0, "z_index": 0, "path": [[530,620],[900,620]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_BUSBAR"} },

      { "kind": "line", "x": 180, "y": 620, "rotation": 0, "z_index": 0, "path": [[180,620],[180,730]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },
      { "kind": "line", "x": 340, "y": 620, "rotation": 0, "z_index": 0, "path": [[340,620],[340,730]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },
      { "kind": "line", "x": 660, "y": 620, "rotation": 0, "z_index": 0, "path": [[660,620],[660,730]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },
      { "kind": "line", "x": 820, "y": 620, "rotation": 0, "z_index": 0, "path": [[820,620],[820,730]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },

      { "kind": "slot", "x": 200, "y": 100, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCHGEAR_PANEL" },
      { "kind": "slot", "x": 800, "y": 100, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCHGEAR_PANEL" },

      { "kind": "slot", "x": 400, "y": 270, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCHGEAR_PANEL" },
      { "kind": "slot", "x": 400, "y": 400, "rotation": 0, "z_index": 20, "slot_class": "POWER_XFMR" },
      { "kind": "slot", "x": 600, "y": 270, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCHGEAR_PANEL" },
      { "kind": "slot", "x": 600, "y": 400, "rotation": 0, "z_index": 20, "slot_class": "POWER_XFMR" },

      { "kind": "slot", "x": 500, "y": 620, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCHGEAR_PANEL" },

      { "kind": "slot", "x": 180, "y": 750, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCHGEAR_PANEL" },
      { "kind": "slot", "x": 340, "y": 750, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCHGEAR_PANEL" },
      { "kind": "slot", "x": 660, "y": 750, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCHGEAR_PANEL" },
      { "kind": "slot", "x": 820, "y": 750, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCHGEAR_PANEL" },

      { "kind": "text", "x": 40,  "y": 186, "rotation": 0, "z_index": 30, "text": "33 kV" },
      { "kind": "text", "x": 40,  "y": 626, "rotation": 0, "z_index": 30, "text": "11 kV" },
      { "kind": "text", "x": 150, "y": 60,  "rotation": 0, "z_index": 30, "text": "Incomer 1" },
      { "kind": "text", "x": 750, "y": 60,  "rotation": 0, "z_index": 30, "text": "Incomer 2" },
      { "kind": "text", "x": 350, "y": 300, "rotation": 0, "z_index": 30, "text": "TX 1" },
      { "kind": "text", "x": 550, "y": 300, "rotation": 0, "z_index": 30, "text": "TX 2" },
      { "kind": "text", "x": 456, "y": 660, "rotation": 0, "z_index": 30, "text": "Coupler" },
      { "kind": "text", "x": 156, "y": 790, "rotation": 0, "z_index": 30, "text": "Feeder 1" },
      { "kind": "text", "x": 316, "y": 790, "rotation": 0, "z_index": 30, "text": "Feeder 2" },
      { "kind": "text", "x": 636, "y": 790, "rotation": 0, "z_index": 30, "text": "Feeder 3" },
      { "kind": "text", "x": 796, "y": 790, "rotation": 0, "z_index": 30, "text": "Feeder 4" }
    ]
  }
  $$::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM runtime.diagram_templates WHERE name = '33/11 kV substation'
);

-- ------------------------------------------- Switching station (33 kV) -------
-- One voltage (33 kV), no transformer. The bus is sectionalised into two
-- segments (100..470 and 530..900) tied by a bus-section bay at x=500. Two
-- incomer bays feed in from above (x=200 on A, 800 on B); four feeder bays hang
-- below (x=180/340 on A, 660/820 on B).
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT
  'Switching station (33 kV)',
  'Single-voltage 33 kV switching station: one sectionalised bus tied by a bus-section bay, with incomers and outgoing feeders and no transformer. Typed slots; purely schematic.',
  $$
  {
    "elements": [
      { "kind": "bus", "x": 100, "y": 320, "rotation": 0, "z_index": 0, "path": [[100,320],[470,320]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_BUSBAR"} },
      { "kind": "bus", "x": 530, "y": 320, "rotation": 0, "z_index": 0, "path": [[530,320],[900,320]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_BUSBAR"} },

      { "kind": "line", "x": 200, "y": 200, "rotation": 0, "z_index": 0, "path": [[200,200],[200,320]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },
      { "kind": "line", "x": 800, "y": 200, "rotation": 0, "z_index": 0, "path": [[800,200],[800,320]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },

      { "kind": "line", "x": 180, "y": 320, "rotation": 0, "z_index": 0, "path": [[180,320],[180,430]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },
      { "kind": "line", "x": 340, "y": 320, "rotation": 0, "z_index": 0, "path": [[340,320],[340,430]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },
      { "kind": "line", "x": 660, "y": 320, "rotation": 0, "z_index": 0, "path": [[660,320],[660,430]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },
      { "kind": "line", "x": 820, "y": 320, "rotation": 0, "z_index": 0, "path": [[820,320],[820,430]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },

      { "kind": "slot", "x": 200, "y": 220, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCHGEAR_PANEL" },
      { "kind": "slot", "x": 800, "y": 220, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCHGEAR_PANEL" },

      { "kind": "slot", "x": 500, "y": 320, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCHGEAR_PANEL" },

      { "kind": "slot", "x": 180, "y": 450, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCHGEAR_PANEL" },
      { "kind": "slot", "x": 340, "y": 450, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCHGEAR_PANEL" },
      { "kind": "slot", "x": 660, "y": 450, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCHGEAR_PANEL" },
      { "kind": "slot", "x": 820, "y": 450, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCHGEAR_PANEL" },

      { "kind": "text", "x": 40,  "y": 326, "rotation": 0, "z_index": 30, "text": "33 kV" },
      { "kind": "text", "x": 150, "y": 190, "rotation": 0, "z_index": 30, "text": "Incomer 1" },
      { "kind": "text", "x": 750, "y": 190, "rotation": 0, "z_index": 30, "text": "Incomer 2" },
      { "kind": "text", "x": 456, "y": 360, "rotation": 0, "z_index": 30, "text": "Bus section" },
      { "kind": "text", "x": 156, "y": 490, "rotation": 0, "z_index": 30, "text": "Feeder 1" },
      { "kind": "text", "x": 316, "y": 490, "rotation": 0, "z_index": 30, "text": "Feeder 2" },
      { "kind": "text", "x": 636, "y": 490, "rotation": 0, "z_index": 30, "text": "Feeder 3" },
      { "kind": "text", "x": 796, "y": 490, "rotation": 0, "z_index": 30, "text": "Feeder 4" }
    ]
  }
  $$::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM runtime.diagram_templates WHERE name = 'Switching station (33 kV)'
);
