-- seed/double_busbar_template.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same as seed/ansi_stencils.sql. Seeds one built-in reusable
-- diagram layout skeleton into runtime.diagram_templates (migration 0049): a
-- "Double busbar" substation arrangement the apply-template picker (Phase 24
-- Slice 4) drops onto a fresh one-line, then the user binds each bay to a real
-- feeder feature.
--
-- Everything here rides the existing template + bus/tap + slot machinery, no new
-- code path: the payload is the same { "elements": [...] } shape saveAsTemplate
-- writes, so applyTemplate instantiates it exactly like a user-authored template.
-- The arrangement is PURELY SCHEMATIC -- heavy `bus` lines, thin `line` risers,
-- ANSI `symbol` glyphs (disconnect selectors + a coupler breaker, drawn from the
-- ansi-disconnect/ansi-breaker stencils in seed/ansi_stencils.sql), free `text`
-- labels, and three empty `slot`s for the feeder bays. It carries NO electrical
-- or connectivity meaning; traces are unaffected until real features are bound.
--
-- The bay slots are deliberately UNTYPED (no slot_class): a portable built-in
-- template can't know a given deployment's feeder class code, so each bay accepts
-- any feature and binds to whatever class the pick belongs to (the any-class slot
-- path in diagram-canvas.tsx's SlotBindPanel/bindSlot). Selectors and the coupler
-- breaker are decorative glyphs, not slots -- promote or replace them by hand if a
-- deployment models its switchgear as real features.
--
-- Coordinates live in the diagram's own abstract local x/y space (the canvas
-- auto-fits its viewBox to content, so absolute magnitudes don't matter, only the
-- layout's proportions): two horizontal buses at y=140 (Bus A) and y=340 (Bus B),
-- three feeder bays at x=250/470/690, and a bus coupler at x=850. z_index orders
-- the draw: buses/risers behind (0), glyphs (10), slots (20), labels (30).
--
-- Idempotent: guarded on the template name, so re-applying never inserts a second
-- copy or clobbers one edited in place. Apply with psql after migrations 0001-0049
-- and seed/ansi_stencils.sql (for the disconnect/breaker glyphs to resolve).

INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT
  'Double busbar',
  'Two-busbar substation arrangement: Bus A / Bus B, a bus coupler, and three empty feeder bays to bind. Purely schematic.',
  $$
  {
    "elements": [
      { "kind": "bus", "x": 100, "y": 140, "rotation": 0, "z_index": 0, "path": [[100,140],[900,140]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_BUSBAR"} },
      { "kind": "bus", "x": 100, "y": 340, "rotation": 0, "z_index": 0, "path": [[100,340],[900,340]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_BUSBAR"} },

      { "kind": "line", "x": 250, "y": 140, "rotation": 0, "z_index": 0, "path": [[250,140],[250,500]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },
      { "kind": "line", "x": 470, "y": 140, "rotation": 0, "z_index": 0, "path": [[470,140],[470,500]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },
      { "kind": "line", "x": 690, "y": 140, "rotation": 0, "z_index": 0, "path": [[690,140],[690,500]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },
      { "kind": "line", "x": 850, "y": 140, "rotation": 0, "z_index": 0, "path": [[850,140],[850,340]], "style": {"stroke": "var(--foreground)", "promoteClass": "MV_CONDUCTOR"} },

      { "kind": "symbol", "x": 250, "y": 230, "rotation": 0, "z_index": 10, "style": {"iconRef": "ansi-disconnect", "color": "#334155"} },
      { "kind": "symbol", "x": 250, "y": 380, "rotation": 0, "z_index": 10, "style": {"iconRef": "ansi-disconnect", "color": "#334155"} },
      { "kind": "symbol", "x": 250, "y": 450, "rotation": 0, "z_index": 10, "style": {"iconRef": "ansi-breaker", "color": "#334155"} },

      { "kind": "symbol", "x": 470, "y": 230, "rotation": 0, "z_index": 10, "style": {"iconRef": "ansi-disconnect", "color": "#334155"} },
      { "kind": "symbol", "x": 470, "y": 380, "rotation": 0, "z_index": 10, "style": {"iconRef": "ansi-disconnect", "color": "#334155"} },
      { "kind": "symbol", "x": 470, "y": 450, "rotation": 0, "z_index": 10, "style": {"iconRef": "ansi-breaker", "color": "#334155"} },

      { "kind": "symbol", "x": 690, "y": 230, "rotation": 0, "z_index": 10, "style": {"iconRef": "ansi-disconnect", "color": "#334155"} },
      { "kind": "symbol", "x": 690, "y": 380, "rotation": 0, "z_index": 10, "style": {"iconRef": "ansi-disconnect", "color": "#334155"} },
      { "kind": "symbol", "x": 690, "y": 450, "rotation": 0, "z_index": 10, "style": {"iconRef": "ansi-breaker", "color": "#334155"} },

      { "kind": "symbol", "x": 850, "y": 200, "rotation": 0, "z_index": 10, "style": {"iconRef": "ansi-disconnect", "color": "#334155"} },
      { "kind": "symbol", "x": 850, "y": 240, "rotation": 0, "z_index": 10, "style": {"iconRef": "ansi-breaker", "color": "#334155"} },
      { "kind": "symbol", "x": 850, "y": 290, "rotation": 0, "z_index": 10, "style": {"iconRef": "ansi-disconnect", "color": "#334155"} },

      { "kind": "slot", "x": 250, "y": 500, "rotation": 0, "z_index": 20 },
      { "kind": "slot", "x": 470, "y": 500, "rotation": 0, "z_index": 20 },
      { "kind": "slot", "x": 690, "y": 500, "rotation": 0, "z_index": 20 },

      { "kind": "text", "x": 30, "y": 146, "rotation": 0, "z_index": 30, "text": "Bus A" },
      { "kind": "text", "x": 30, "y": 346, "rotation": 0, "z_index": 30, "text": "Bus B" },
      { "kind": "text", "x": 226, "y": 120, "rotation": 0, "z_index": 30, "text": "Bay 1" },
      { "kind": "text", "x": 446, "y": 120, "rotation": 0, "z_index": 30, "text": "Bay 2" },
      { "kind": "text", "x": 666, "y": 120, "rotation": 0, "z_index": 30, "text": "Bay 3" },
      { "kind": "text", "x": 812, "y": 128, "rotation": 0, "z_index": 30, "text": "Coupler" }
    ]
  }
  $$::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM runtime.diagram_templates WHERE name = 'Double busbar'
);
