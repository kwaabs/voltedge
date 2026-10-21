-- seed/packed_station_diagram_templates.sql
-- Purpose: NOT a migration -- lives under /seed, same convention as
-- seed/assembly_diagram_templates.sql (which this follows to the letter). The
-- built-in single-line skeletons for the nested packed station
-- (seed/packed_station_assembly.sql). The auto single-line builder
-- (api/internal/diagram/autobuild.go, via assembly/autodiagram.go's
-- assemblyDiagramTemplates map) roots a ONE_LINE diagram on the placed
-- PACKAGED_SUBSTATION and binds each of the kit's geometry-less fan-out
-- components into the matching typed SLOT here (FIFO per class).
--
-- Two templates, one per config (the fixed/mobile tenure doesn't change the kit,
-- so both tenure codes map to the same single-line in autodiagram.go):
--   A. "Packed station — RMU + transformer + pillar"  (PACKED_RMU_FIXED/MOBILE)
--   B. "Packed station — transformer + pillar"         (PACKED_XFMR_FIXED/MOBILE)
--
-- WHAT GETS A SLOT. RMU and LV_PILLAR are CONTAINERS (is_diagram_placeable =
-- false), so they can't be slotted -- the single-line instead shows their
-- INTERNALS (the RMU's bus + ways, the pillar's bus + fuses) with a text label,
-- exactly as seed/assembly_diagram_templates.sql does for the standalone RMU and
-- feeder-pillar kits. The PACKAGED_SUBSTATION anchor is a container too and gets
-- no slot (it's offered to the binder but matches nothing, harmlessly ignored).
-- Slot counts/classes come straight from the fan-out items in
-- seed/packed_station_assembly.sql:
--   config A: 1 MV_BUSBAR + 3 MV_SWITCH + 1 DIST_XFMR + 1 LV_BUSBAR + 4 LV_FUSE
--   config B: 1 DIST_XFMR + 1 LV_BUSBAR + 4 LV_FUSE
-- The ways carry NO external cable slots (unlike the standalone RMU template):
-- this kit's ways are externally fed, so the fan-out creates no MV_CONDUCTOR.
--
-- The two line-drawn classes (MV_BUSBAR, LV_BUSBAR; both diagram_geometry_type
-- 'line') carry a 2-point `path` on their slot; every other slot is a point.
-- `line` elements are thin schematic risers (z 0), slots z 20, labels z 30 --
-- same ordering as the sibling file. NO electrical meaning until features bind.
--
-- Idempotent: each template guarded on its name; the placeable/container UPDATE
-- only flips false rows. No migration, no compile:
--   task seed-packed-station-diagrams   (or psql -f directly)
-- Apply AFTER seed/packed_station_assembly.sql and the component-class seeds.

-- ---------------------------------------------------------------------------
-- Defensive idempotent backstop: every class used below must be diagram-placeable
-- (false -> true only), and the enclosure a diagram container. These are already
-- set by seed/assembly_diagram_templates.sql + the class seeds; repeated here so
-- this file is self-sufficient if applied on its own.
-- ---------------------------------------------------------------------------
UPDATE metadata.feature_classes
SET is_diagram_placeable = true
WHERE is_diagram_placeable = false
  AND code IN ('MV_BUSBAR', 'MV_SWITCH', 'DIST_XFMR', 'LV_BUSBAR', 'LV_FUSE');

UPDATE metadata.feature_classes
SET is_diagram_container = true
WHERE is_diagram_container = false
  AND code IN ('PACKAGED_SUBSTATION', 'LV_PILLAR', 'RMU');

-- =========================================================================
-- A. Packed station — RMU + transformer + pillar  (PACKED_RMU_*)
--    MV: internal bus + 2 ring ways + 1 transformer tee.
--    The tee feeds the transformer; the transformer feeds the pillar bus;
--    four fused LV ways fan out below.
-- =========================================================================
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT
  'Packed station — RMU + transformer + pillar',
  'Nested packed-station single-line: the RMU''s internal MV busbar with two ring-way switches and one transformer tee-off switch, feeding a distribution transformer, feeding the LV feeder pillar''s busbar and four fused outgoing ways. Bind each slot to the station''s fan-out components.',
  $$
  {
    "elements": [
      { "kind": "line", "x": 200, "y": 120, "rotation": 0, "z_index": 0, "path": [[200,120],[200,160]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 360, "y": 120, "rotation": 0, "z_index": 0, "path": [[360,120],[360,160]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 520, "y": 120, "rotation": 0, "z_index": 0, "path": [[520,120],[520,160]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 360, "y": 220, "rotation": 0, "z_index": 0, "path": [[360,220],[360,300]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 360, "y": 360, "rotation": 0, "z_index": 0, "path": [[360,360],[360,440]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 160, "y": 440, "rotation": 0, "z_index": 0, "path": [[160,440],[160,480]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 320, "y": 440, "rotation": 0, "z_index": 0, "path": [[320,440],[320,480]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 480, "y": 440, "rotation": 0, "z_index": 0, "path": [[480,440],[480,480]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 600, "y": 440, "rotation": 0, "z_index": 0, "path": [[600,440],[600,480]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 160, "y": 540, "rotation": 0, "z_index": 0, "path": [[160,540],[160,590]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 320, "y": 540, "rotation": 0, "z_index": 0, "path": [[320,540],[320,590]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 480, "y": 540, "rotation": 0, "z_index": 0, "path": [[480,540],[480,590]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 600, "y": 540, "rotation": 0, "z_index": 0, "path": [[600,540],[600,590]], "style": {"stroke": "var(--foreground)"} },

      { "kind": "slot", "x": 80,  "y": 120, "rotation": 0, "z_index": 20, "slot_class": "MV_BUSBAR", "path": [[80,120],[640,120]] },
      { "kind": "slot", "x": 200, "y": 190, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCH" },
      { "kind": "slot", "x": 520, "y": 190, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCH" },
      { "kind": "slot", "x": 360, "y": 190, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCH" },
      { "kind": "slot", "x": 360, "y": 330, "rotation": 0, "z_index": 20, "slot_class": "DIST_XFMR" },
      { "kind": "slot", "x": 80,  "y": 440, "rotation": 0, "z_index": 20, "slot_class": "LV_BUSBAR", "path": [[80,440],[640,440]] },
      { "kind": "slot", "x": 160, "y": 510, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },
      { "kind": "slot", "x": 320, "y": 510, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },
      { "kind": "slot", "x": 480, "y": 510, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },
      { "kind": "slot", "x": 600, "y": 510, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },

      { "kind": "text", "x": 20,  "y": 112, "rotation": 0, "z_index": 30, "text": "RMU internal bus" },
      { "kind": "text", "x": 160, "y": 245, "rotation": 0, "z_index": 30, "text": "Ring way A" },
      { "kind": "text", "x": 300, "y": 245, "rotation": 0, "z_index": 30, "text": "Transformer tee" },
      { "kind": "text", "x": 480, "y": 245, "rotation": 0, "z_index": 30, "text": "Ring way B" },
      { "kind": "text", "x": 395, "y": 325, "rotation": 0, "z_index": 30, "text": "Distribution transformer" },
      { "kind": "text", "x": 20,  "y": 432, "rotation": 0, "z_index": 30, "text": "LV feeder pillar" },
      { "kind": "text", "x": 135, "y": 615, "rotation": 0, "z_index": 30, "text": "Way 1" },
      { "kind": "text", "x": 295, "y": 615, "rotation": 0, "z_index": 30, "text": "Way 2" },
      { "kind": "text", "x": 455, "y": 615, "rotation": 0, "z_index": 30, "text": "Way 3" },
      { "kind": "text", "x": 575, "y": 615, "rotation": 0, "z_index": 30, "text": "Way 4" }
    ]
  }
  $$::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM runtime.diagram_templates WHERE name = 'Packed station — RMU + transformer + pillar'
);

-- =========================================================================
-- B. Packed station — transformer + pillar  (PACKED_XFMR_*)
--    No RMU: the transformer's MV is externally fed at install.
-- =========================================================================
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT
  'Packed station — transformer + pillar',
  'Nested packed-station single-line (no RMU): an externally-fed distribution transformer feeding the LV feeder pillar''s busbar and four fused outgoing ways. Bind each slot to the station''s fan-out components.',
  $$
  {
    "elements": [
      { "kind": "line", "x": 360, "y": 40,  "rotation": 0, "z_index": 0, "path": [[360,40],[360,100]],  "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 360, "y": 160, "rotation": 0, "z_index": 0, "path": [[360,160],[360,240]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 160, "y": 240, "rotation": 0, "z_index": 0, "path": [[160,240],[160,280]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 320, "y": 240, "rotation": 0, "z_index": 0, "path": [[320,240],[320,280]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 480, "y": 240, "rotation": 0, "z_index": 0, "path": [[480,240],[480,280]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 600, "y": 240, "rotation": 0, "z_index": 0, "path": [[600,240],[600,280]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 160, "y": 340, "rotation": 0, "z_index": 0, "path": [[160,340],[160,390]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 320, "y": 340, "rotation": 0, "z_index": 0, "path": [[320,340],[320,390]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 480, "y": 340, "rotation": 0, "z_index": 0, "path": [[480,340],[480,390]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 600, "y": 340, "rotation": 0, "z_index": 0, "path": [[600,340],[600,390]], "style": {"stroke": "var(--foreground)"} },

      { "kind": "slot", "x": 360, "y": 130, "rotation": 0, "z_index": 20, "slot_class": "DIST_XFMR" },
      { "kind": "slot", "x": 80,  "y": 240, "rotation": 0, "z_index": 20, "slot_class": "LV_BUSBAR", "path": [[80,240],[640,240]] },
      { "kind": "slot", "x": 160, "y": 310, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },
      { "kind": "slot", "x": 320, "y": 310, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },
      { "kind": "slot", "x": 480, "y": 310, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },
      { "kind": "slot", "x": 600, "y": 310, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },

      { "kind": "text", "x": 395, "y": 34,  "rotation": 0, "z_index": 30, "text": "MV incomer" },
      { "kind": "text", "x": 395, "y": 125, "rotation": 0, "z_index": 30, "text": "Distribution transformer" },
      { "kind": "text", "x": 20,  "y": 232, "rotation": 0, "z_index": 30, "text": "LV feeder pillar" },
      { "kind": "text", "x": 135, "y": 415, "rotation": 0, "z_index": 30, "text": "Way 1" },
      { "kind": "text", "x": 295, "y": 415, "rotation": 0, "z_index": 30, "text": "Way 2" },
      { "kind": "text", "x": 455, "y": 415, "rotation": 0, "z_index": 30, "text": "Way 3" },
      { "kind": "text", "x": 575, "y": 415, "rotation": 0, "z_index": 30, "text": "Way 4" }
    ]
  }
  $$::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM runtime.diagram_templates WHERE name = 'Packed station — transformer + pillar'
);

DO $$
BEGIN
    RAISE NOTICE 'seed/packed_station_diagram_templates.sql applied: + 2 packed-station single-line templates (RMU / transformer). No recompile. Placed stations auto-build their one-line; or Apply template on a diagram and bind each slot.';
END $$;
