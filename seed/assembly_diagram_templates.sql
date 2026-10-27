-- seed/assembly_diagram_templates.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same as seed/double_busbar_template.sql (the single existing
-- built-in diagram template, which this file follows to the letter).
--
-- WHY THIS EXISTS
-- The fan-out engines (RMU, packaged substation, the pole/pad guided assemblies,
-- and the switchgear-panel engine) each create ONE map-placed container plus a
-- set of GEOMETRY-LESS internal component features (api/internal/rmu/fanout.go:
-- "the bus and ways are geometry-optional and created without" map geometry). A
-- diagram is populated only by explicit placements, and "Add from map" needs a
-- feature you can click ON the map -- which these internal parts don't have. The
-- supported way to put an EXISTING geometry-less feature onto a schematic is a
-- typed template SLOT that the user binds to it by name (diagram-canvas.tsx
-- SlotBindPanel/bindSlot -> POST /diagrams/{id}/placements). This file seeds one
-- reusable single-line skeleton per kit/config, so the workflow becomes:
--   open (or create) a diagram -> Apply template -> pick the kit -> bind each
--   slot to the kit's fan-out component.
--
-- Everything rides the existing template machinery, no new code path: the payload
-- is the same { "elements": [...] } shape saveAsTemplate writes and applyTemplate
-- instantiates (api/internal/diagram/templates.go).
--
-- SLOTS ARE TYPED. Unlike the portable Double busbar template (whose bays are
-- UNTYPED because it can't know a deployment's feeder class), every kit's exact
-- component classes ARE known, so each slot sets slot_class -- the bind picker is
-- then class-scoped (a "switch" slot only offers MV_SWITCH features). Counts and
-- classes below come straight from each kit's template-item rows, not guesses:
--   RMU               seed/rmu_templates.sql            (metadata.rmu_template_items)
--   Packaged sub      seed/packaged_substation_templates.sql
--   Pole/pad kits     seed/overhead_pole_transformer.sql, pole_mount_recloser.sql,
--                     pole_mount_capacitor_bank.sql, pole_mount_voltage_regulator.sql,
--                     pole_mount_bess.sql, pad_mount_urd.sql (metadata.assembly_items)
--   Switchgear panel  seed/mv_switchgear_panel_templates.sql (metadata.panel_template_items)
--
-- Two classes are line-drawn here and so carry a 2-point `path` on their slot
-- (validatePlacementPath accepts a path only for a line diagram shape): the bus
-- (MV_BUSBAR sets diagram_geometry_type = 'line') and, on the RMU templates, the
-- external cable (MV_CONDUCTOR is a 'line' class -- seed/rmu_templates.sql now
-- fans a real conductor onto each way's cable side, so each RMU way gets an
-- MV_CONDUCTOR drop slot below its switch). Every other slot is a point, no path.
-- All remaining elements are thin `line` risers, `control` (dashed trip) lines,
-- and free `text` labels -- purely schematic, NO electrical/connectivity meaning
-- until real features are bound. z_index orders the draw: lines behind (0), slots
-- (20), labels (30), matching double_busbar_template.sql.
--
-- PLACEABILITY. placeFeature rejects any class with is_diagram_placeable = false
-- (api/internal/diagram/sql.go), and that includes a slot bind. Every component
-- class below is already placeable EXCEPT the three whose class seed omitted the
-- flag (it defaults false and migration 0032's back-fill runs before seeds in a
-- fresh bootstrap): DIST_XFMR, LV_LINK_BOX, PROTECTIVE_RELAY. The UPDATE below
-- flips ONLY those false rows to true -- a runtime metadata flag, so NO recompile
-- is needed (unlike geometry_optional). All three are discrete devices/pillars/
-- relays that legitimately belong on a single-line. MV_CONDUCTOR is in the list
-- too (the RMU cable slot): seed/mv_conductor_diagram_promote.sql is its primary
-- promotion (placeable + geometry_optional + `task compile CODE=MV_CONDUCTOR`),
-- and this UPDATE is a harmless idempotent backstop for its placeable flag only.
--
-- Idempotent: each template is guarded on its name (re-applying never inserts a
-- second copy or clobbers one edited in place), and the placeable UPDATE only
-- touches false rows. Apply after migrations 0001-0053 and the component-class
-- seeds. No migration, no compile:
--   task seed-assembly-diagram-templates   (or the psql -f invocation directly)

-- ---------------------------------------------------------------------------
-- Ensure every kit component class is diagram-placeable (false -> true only).
-- ---------------------------------------------------------------------------
UPDATE metadata.feature_classes
SET is_diagram_placeable = true
WHERE is_diagram_placeable = false
  AND code IN (
    'MV_BUSBAR', 'MV_SWITCH', 'DIST_XFMR', 'LV_BUSBAR', 'MV_FUSE', 'MV_ARRESTER',
    'MV_RECLOSER', 'MV_CAP_BANK', 'MV_VREG', 'MV_BESS', 'LV_LINK_BOX',
    'MV_ISOLATOR', 'MV_BREAKER', 'MV_CT', 'MV_EARTH_SWITCH', 'MV_VT',
    'PROTECTIVE_RELAY', 'LV_FUSE', 'MV_CONDUCTOR'
  );

-- ---------------------------------------------------------------------------
-- Ensure every assembly ANCHOR is a diagram container (false -> true only).
-- ---------------------------------------------------------------------------
-- The auto single-line builder (api/internal/diagram/autobuild.go) roots the
-- instance on the assembly's own feature, and createInstance rejects any class
-- with is_diagram_container = false (api/internal/diagram/sql.go). RMU and
-- PACKAGED_SUBSTATION are already containers; POLE (pole-mount kits), DIST_XFMR
-- (the pad-mount URD anchor) and MV_SWITCHGEAR_PANEL (fanned-out bay devices)
-- were not, so their kits could never get a diagram. Flip ONLY those false rows
-- to true -- a runtime metadata flag, so NO recompile is needed. Idempotent.
UPDATE metadata.feature_classes
SET is_diagram_container = true
WHERE is_diagram_container = false
  AND code IN ('POLE', 'DIST_XFMR', 'MV_SWITCHGEAR_PANEL', 'LV_PILLAR');

-- =========================================================================
-- RMU -- 2 ring + 1 transformer  (config 2W_1T: 1x MV_BUSBAR + 3x MV_SWITCH
--   + 3x MV_CONDUCTOR, one external cable per way -- seed/rmu_templates.sql)
-- =========================================================================
-- UPDATE-in-place then INSERT-if-missing (not the bare INSERT ... WHERE NOT
-- EXISTS the other kits use): existing RMU diagrams reference this row via
-- source_template_id (FK ON DELETE SET NULL, migration 0122), so the row must be
-- edited in place -- never deleted+recreated -- to add the cable slots without
-- orphaning those regenerate links. The lower-riser lines the cable slots replace
-- were plain schematic drops; each is now an MV_CONDUCTOR slot at the SAME x as
-- its switch, left-to-right, so the binder pairs each cable under its own way.
WITH tmpl(name, description, payload) AS (
  VALUES (
    'RMU — 2 ring + 1 transformer',
    'Ring main unit single-line: internal MV busbar, two ring-way switches and one transformer tee-off switch, each with its external cable. Bind each slot to the RMU''s fan-out components.',
    $$
    {
      "elements": [
        { "kind": "line", "x": 160, "y": 120, "rotation": 0, "z_index": 0, "path": [[160,120],[160,230]], "style": {"stroke": "var(--foreground)"} },
        { "kind": "line", "x": 360, "y": 120, "rotation": 0, "z_index": 0, "path": [[360,120],[360,230]], "style": {"stroke": "var(--foreground)"} },
        { "kind": "line", "x": 560, "y": 120, "rotation": 0, "z_index": 0, "path": [[560,120],[560,230]], "style": {"stroke": "var(--foreground)"} },

        { "kind": "slot", "x": 100, "y": 120, "rotation": 0, "z_index": 20, "slot_class": "MV_BUSBAR", "path": [[100,120],[620,120]] },
        { "kind": "slot", "x": 160, "y": 260, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCH" },
        { "kind": "slot", "x": 360, "y": 260, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCH" },
        { "kind": "slot", "x": 560, "y": 260, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCH" },

        { "kind": "slot", "x": 160, "y": 290, "rotation": 0, "z_index": 20, "slot_class": "MV_CONDUCTOR", "path": [[160,290],[160,380]] },
        { "kind": "slot", "x": 360, "y": 290, "rotation": 0, "z_index": 20, "slot_class": "MV_CONDUCTOR", "path": [[360,290],[360,380]] },
        { "kind": "slot", "x": 560, "y": 290, "rotation": 0, "z_index": 20, "slot_class": "MV_CONDUCTOR", "path": [[560,290],[560,380]] },

        { "kind": "text", "x": 30, "y": 112, "rotation": 0, "z_index": 30, "text": "Internal bus" },
        { "kind": "text", "x": 120, "y": 405, "rotation": 0, "z_index": 30, "text": "Ring way A cable" },
        { "kind": "text", "x": 315, "y": 405, "rotation": 0, "z_index": 30, "text": "Transformer tee cable" },
        { "kind": "text", "x": 520, "y": 405, "rotation": 0, "z_index": 30, "text": "Ring way B cable" }
      ]
    }
    $$::jsonb
  )
),
upd AS (
  UPDATE runtime.diagram_templates d
  SET description = t.description, payload = t.payload
  FROM tmpl t
  WHERE d.name = t.name
  RETURNING d.id
)
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT t.name, t.description, t.payload FROM tmpl t
WHERE NOT EXISTS (SELECT 1 FROM upd)
  AND NOT EXISTS (SELECT 1 FROM runtime.diagram_templates WHERE name = t.name);

-- =========================================================================
-- RMU -- 2 ring + 2 transformer  (config 2W_2T: 1x MV_BUSBAR + 4x MV_SWITCH
--   + 4x MV_CONDUCTOR, one external cable per way -- seed/rmu_templates.sql)
-- =========================================================================
-- Same UPDATE-in-place-then-INSERT pattern and cable-slot reasoning as the
-- 2W_1T template above (see that block's comment).
WITH tmpl(name, description, payload) AS (
  VALUES (
    'RMU — 2 ring + 2 transformer',
    'Ring main unit single-line: internal MV busbar, two ring-way switches and two transformer tee-off switches, each with its external cable. Bind each slot to the RMU''s fan-out components.',
    $$
    {
      "elements": [
        { "kind": "line", "x": 140, "y": 120, "rotation": 0, "z_index": 0, "path": [[140,120],[140,230]], "style": {"stroke": "var(--foreground)"} },
        { "kind": "line", "x": 300, "y": 120, "rotation": 0, "z_index": 0, "path": [[300,120],[300,230]], "style": {"stroke": "var(--foreground)"} },
        { "kind": "line", "x": 460, "y": 120, "rotation": 0, "z_index": 0, "path": [[460,120],[460,230]], "style": {"stroke": "var(--foreground)"} },
        { "kind": "line", "x": 620, "y": 120, "rotation": 0, "z_index": 0, "path": [[620,120],[620,230]], "style": {"stroke": "var(--foreground)"} },

        { "kind": "slot", "x": 80, "y": 120, "rotation": 0, "z_index": 20, "slot_class": "MV_BUSBAR", "path": [[80,120],[680,120]] },
        { "kind": "slot", "x": 140, "y": 260, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCH" },
        { "kind": "slot", "x": 300, "y": 260, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCH" },
        { "kind": "slot", "x": 460, "y": 260, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCH" },
        { "kind": "slot", "x": 620, "y": 260, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCH" },

        { "kind": "slot", "x": 140, "y": 290, "rotation": 0, "z_index": 20, "slot_class": "MV_CONDUCTOR", "path": [[140,290],[140,380]] },
        { "kind": "slot", "x": 300, "y": 290, "rotation": 0, "z_index": 20, "slot_class": "MV_CONDUCTOR", "path": [[300,290],[300,380]] },
        { "kind": "slot", "x": 460, "y": 290, "rotation": 0, "z_index": 20, "slot_class": "MV_CONDUCTOR", "path": [[460,290],[460,380]] },
        { "kind": "slot", "x": 620, "y": 290, "rotation": 0, "z_index": 20, "slot_class": "MV_CONDUCTOR", "path": [[620,290],[620,380]] },

        { "kind": "text", "x": 20, "y": 112, "rotation": 0, "z_index": 30, "text": "Internal bus" },
        { "kind": "text", "x": 100, "y": 405, "rotation": 0, "z_index": 30, "text": "Ring way A cable" },
        { "kind": "text", "x": 255, "y": 405, "rotation": 0, "z_index": 30, "text": "Transformer tee 1 cable" },
        { "kind": "text", "x": 415, "y": 405, "rotation": 0, "z_index": 30, "text": "Transformer tee 2 cable" },
        { "kind": "text", "x": 580, "y": 405, "rotation": 0, "z_index": 30, "text": "Ring way B cable" }
      ]
    }
    $$::jsonb
  )
),
upd AS (
  UPDATE runtime.diagram_templates d
  SET description = t.description, payload = t.payload
  FROM tmpl t
  WHERE d.name = t.name
  RETURNING d.id
)
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT t.name, t.description, t.payload FROM tmpl t
WHERE NOT EXISTS (SELECT 1 FROM upd)
  AND NOT EXISTS (SELECT 1 FROM runtime.diagram_templates WHERE name = t.name);

-- =========================================================================
-- Packaged substation (compact)
--   config COMPACT: 1x MV_SWITCH -> 1x DIST_XFMR -> 1x LV_BUSBAR (chain)
-- =========================================================================
-- Upsert (not insert-if-absent): the layout here is revised over time -- the LV
-- busbar is now a horizontal bar (a "path" slot) instead of rendering as a long
-- vertical line -- so re-running this seed must UPDATE an already-seeded template,
-- not skip it. (runtime.diagram_templates has no unique(name), so this is an
-- explicit update-else-insert rather than ON CONFLICT.) After updating the
-- template, Regenerate an existing packaged-substation diagram to pick up the new
-- layout (migration 0122: Regenerate re-applies source_template_id); new builds
-- get it automatically.
WITH tmpl(name, description, payload) AS (
  VALUES (
    'Packaged substation (compact)',
    'Compact secondary substation single-line: MV switch, distribution transformer, LV busbar in a chain. Bind each slot to the packaged-substation fan-out components.',
    $$
    {
      "elements": [
        { "kind": "line", "x": 200, "y": 30,  "rotation": 0, "z_index": 0, "path": [[200,30],[200,95]],   "style": {"stroke": "var(--foreground)"} },
        { "kind": "line", "x": 200, "y": 145, "rotation": 0, "z_index": 0, "path": [[200,145],[200,215]], "style": {"stroke": "var(--foreground)"} },
        { "kind": "line", "x": 200, "y": 265, "rotation": 0, "z_index": 0, "path": [[200,265],[200,335]], "style": {"stroke": "var(--foreground)"} },

        { "kind": "slot", "x": 200, "y": 120, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCH" },
        { "kind": "slot", "x": 200, "y": 240, "rotation": 0, "z_index": 20, "slot_class": "DIST_XFMR" },
        { "kind": "slot", "x": 120, "y": 360, "rotation": 0, "z_index": 20, "slot_class": "LV_BUSBAR", "path": [[120,360],[280,360]] },

        { "kind": "text", "x": 235, "y": 24,  "rotation": 0, "z_index": 30, "text": "MV incomer" },
        { "kind": "text", "x": 235, "y": 114, "rotation": 0, "z_index": 30, "text": "MV switch" },
        { "kind": "text", "x": 235, "y": 234, "rotation": 0, "z_index": 30, "text": "Distribution transformer" },
        { "kind": "text", "x": 300, "y": 354, "rotation": 0, "z_index": 30, "text": "LV busbar" }
      ]
    }
    $$::jsonb
  )
),
upd AS (
  UPDATE runtime.diagram_templates t
     SET payload = tmpl.payload, description = tmpl.description
    FROM tmpl
   WHERE t.name = tmpl.name
  RETURNING t.id
)
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT name, description, payload FROM tmpl
WHERE NOT EXISTS (SELECT 1 FROM runtime.diagram_templates WHERE name = tmpl.name);

-- =========================================================================
-- Pole-mount transformer (overhead)  (POLE_MOUNT_STD)
--   1x MV_FUSE + 1x MV_ARRESTER + 1x DIST_XFMR + 1x LV_BUSBAR
-- =========================================================================
-- Upsert (see the compact template above for why): revising the busbar to a
-- horizontal bar must update an already-seeded row, and Regenerate re-applies it.
WITH tmpl(name, description, payload) AS (
  VALUES (
    'Pole-mount transformer (overhead)',
    'Overhead pole-mounted transformer single-line: MV fuse (cutout), surge arrester, distribution transformer, LV busbar. Bind each slot to the assembly''s fan-out components.',
    $$
    {
      "elements": [
        { "kind": "line", "x": 200, "y": 40,  "rotation": 0, "z_index": 0, "path": [[200,40],[200,100]],  "style": {"stroke": "var(--foreground)"} },
        { "kind": "line", "x": 200, "y": 130, "rotation": 0, "z_index": 0, "path": [[200,130],[200,230]], "style": {"stroke": "var(--foreground)"} },
        { "kind": "line", "x": 200, "y": 115, "rotation": 0, "z_index": 0, "path": [[200,115],[330,115]], "style": {"stroke": "var(--foreground)"} },
        { "kind": "line", "x": 330, "y": 145, "rotation": 0, "z_index": 0, "path": [[330,145],[330,175]], "style": {"stroke": "var(--foreground)"} },
        { "kind": "line", "x": 200, "y": 300, "rotation": 0, "z_index": 0, "path": [[200,300],[200,400]], "style": {"stroke": "var(--foreground)"} },

        { "kind": "slot", "x": 200, "y": 115, "rotation": 0, "z_index": 20, "slot_class": "MV_FUSE" },
        { "kind": "slot", "x": 330, "y": 130, "rotation": 0, "z_index": 20, "slot_class": "MV_ARRESTER" },
        { "kind": "slot", "x": 200, "y": 270, "rotation": 0, "z_index": 20, "slot_class": "DIST_XFMR" },
        { "kind": "slot", "x": 120, "y": 410, "rotation": 0, "z_index": 20, "slot_class": "LV_BUSBAR", "path": [[120,410],[280,410]] },

        { "kind": "text", "x": 235, "y": 34,  "rotation": 0, "z_index": 30, "text": "MV line" },
        { "kind": "text", "x": 120, "y": 110, "rotation": 0, "z_index": 30, "text": "MV fuse" },
        { "kind": "text", "x": 360, "y": 135, "rotation": 0, "z_index": 30, "text": "Surge arrester" },
        { "kind": "text", "x": 235, "y": 275, "rotation": 0, "z_index": 30, "text": "Distribution transformer" },
        { "kind": "text", "x": 300, "y": 404, "rotation": 0, "z_index": 30, "text": "LV busbar" }
      ]
    }
    $$::jsonb
  )
),
upd AS (
  UPDATE runtime.diagram_templates t
     SET payload = tmpl.payload, description = tmpl.description
    FROM tmpl
   WHERE t.name = tmpl.name
  RETURNING t.id
)
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT name, description, payload FROM tmpl
WHERE NOT EXISTS (SELECT 1 FROM runtime.diagram_templates WHERE name = tmpl.name);

-- =========================================================================
-- Pole-mount recloser  (POLE_MOUNT_RECLOSER)
--   1x MV_RECLOSER + 2x MV_ARRESTER (source-side + load-side)
-- =========================================================================
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT
  'Pole-mount recloser',
  'Overhead pole-mounted recloser single-line: an in-line recloser with a source-side and a load-side surge arrester. Bind each slot to the assembly''s fan-out components.',
  $$
  {
    "elements": [
      { "kind": "line", "x": 80,  "y": 150, "rotation": 0, "z_index": 0, "path": [[80,150],[520,150]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 160, "y": 150, "rotation": 0, "z_index": 0, "path": [[160,150],[160,210]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 440, "y": 150, "rotation": 0, "z_index": 0, "path": [[440,150],[440,210]], "style": {"stroke": "var(--foreground)"} },

      { "kind": "slot", "x": 300, "y": 150, "rotation": 0, "z_index": 20, "slot_class": "MV_RECLOSER" },
      { "kind": "slot", "x": 160, "y": 240, "rotation": 0, "z_index": 20, "slot_class": "MV_ARRESTER" },
      { "kind": "slot", "x": 440, "y": 240, "rotation": 0, "z_index": 20, "slot_class": "MV_ARRESTER" },

      { "kind": "text", "x": 50,  "y": 135, "rotation": 0, "z_index": 30, "text": "Source side" },
      { "kind": "text", "x": 470, "y": 135, "rotation": 0, "z_index": 30, "text": "Load side" },
      { "kind": "text", "x": 275, "y": 130, "rotation": 0, "z_index": 30, "text": "Recloser" },
      { "kind": "text", "x": 100, "y": 265, "rotation": 0, "z_index": 30, "text": "Arrester" },
      { "kind": "text", "x": 470, "y": 265, "rotation": 0, "z_index": 30, "text": "Arrester" }
    ]
  }
  $$::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM runtime.diagram_templates WHERE name = 'Pole-mount recloser'
);

-- =========================================================================
-- Pole-mount capacitor bank  (POLE_MOUNT_CAP_BANK)
--   1x MV_SWITCH + 1x MV_CAP_BANK
-- =========================================================================
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT
  'Pole-mount capacitor bank',
  'Overhead pole-mounted capacitor bank single-line: a switch tapping the MV line down to the capacitor bank. Bind each slot to the assembly''s fan-out components.',
  $$
  {
    "elements": [
      { "kind": "line", "x": 100, "y": 80,  "rotation": 0, "z_index": 0, "path": [[100,80],[320,80]],  "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 80,  "rotation": 0, "z_index": 0, "path": [[200,80],[200,140]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 200, "rotation": 0, "z_index": 0, "path": [[200,200],[200,280]], "style": {"stroke": "var(--foreground)"} },

      { "kind": "slot", "x": 200, "y": 170, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCH" },
      { "kind": "slot", "x": 200, "y": 310, "rotation": 0, "z_index": 20, "slot_class": "MV_CAP_BANK" },

      { "kind": "text", "x": 235, "y": 74,  "rotation": 0, "z_index": 30, "text": "MV line" },
      { "kind": "text", "x": 235, "y": 175, "rotation": 0, "z_index": 30, "text": "MV switch" },
      { "kind": "text", "x": 235, "y": 315, "rotation": 0, "z_index": 30, "text": "Capacitor bank" }
    ]
  }
  $$::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM runtime.diagram_templates WHERE name = 'Pole-mount capacitor bank'
);

-- =========================================================================
-- Pole-mount voltage regulator  (POLE_MOUNT_REGULATOR)
--   1x MV_VREG + 2x MV_ARRESTER (source-side + load-side)
-- =========================================================================
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT
  'Pole-mount voltage regulator',
  'Overhead pole-mounted voltage regulator single-line: an in-line regulator with a source-side and a load-side surge arrester. Bind each slot to the assembly''s fan-out components.',
  $$
  {
    "elements": [
      { "kind": "line", "x": 80,  "y": 150, "rotation": 0, "z_index": 0, "path": [[80,150],[520,150]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 160, "y": 150, "rotation": 0, "z_index": 0, "path": [[160,150],[160,210]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 440, "y": 150, "rotation": 0, "z_index": 0, "path": [[440,150],[440,210]], "style": {"stroke": "var(--foreground)"} },

      { "kind": "slot", "x": 300, "y": 150, "rotation": 0, "z_index": 20, "slot_class": "MV_VREG" },
      { "kind": "slot", "x": 160, "y": 240, "rotation": 0, "z_index": 20, "slot_class": "MV_ARRESTER" },
      { "kind": "slot", "x": 440, "y": 240, "rotation": 0, "z_index": 20, "slot_class": "MV_ARRESTER" },

      { "kind": "text", "x": 50,  "y": 135, "rotation": 0, "z_index": 30, "text": "Source side" },
      { "kind": "text", "x": 470, "y": 135, "rotation": 0, "z_index": 30, "text": "Load side" },
      { "kind": "text", "x": 255, "y": 130, "rotation": 0, "z_index": 30, "text": "Voltage regulator" },
      { "kind": "text", "x": 100, "y": 265, "rotation": 0, "z_index": 30, "text": "Arrester" },
      { "kind": "text", "x": 470, "y": 265, "rotation": 0, "z_index": 30, "text": "Arrester" }
    ]
  }
  $$::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM runtime.diagram_templates WHERE name = 'Pole-mount voltage regulator'
);

-- =========================================================================
-- Pole-mount battery storage  (POLE_MOUNT_BESS)
--   1x MV_SWITCH + 1x MV_ARRESTER + 1x MV_BESS
-- =========================================================================
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT
  'Pole-mount battery storage',
  'Overhead pole-mounted battery energy storage single-line: a switch tapping the MV line down to the battery, with a surge arrester. Bind each slot to the assembly''s fan-out components.',
  $$
  {
    "elements": [
      { "kind": "line", "x": 100, "y": 80,  "rotation": 0, "z_index": 0, "path": [[100,80],[320,80]],  "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 80,  "rotation": 0, "z_index": 0, "path": [[200,80],[200,140]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 95,  "rotation": 0, "z_index": 0, "path": [[200,95],[330,95]],  "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 200, "rotation": 0, "z_index": 0, "path": [[200,200],[200,280]], "style": {"stroke": "var(--foreground)"} },

      { "kind": "slot", "x": 200, "y": 170, "rotation": 0, "z_index": 20, "slot_class": "MV_SWITCH" },
      { "kind": "slot", "x": 330, "y": 110, "rotation": 0, "z_index": 20, "slot_class": "MV_ARRESTER" },
      { "kind": "slot", "x": 200, "y": 310, "rotation": 0, "z_index": 20, "slot_class": "MV_BESS" },

      { "kind": "text", "x": 235, "y": 74,  "rotation": 0, "z_index": 30, "text": "MV line" },
      { "kind": "text", "x": 360, "y": 115, "rotation": 0, "z_index": 30, "text": "Surge arrester" },
      { "kind": "text", "x": 235, "y": 175, "rotation": 0, "z_index": 30, "text": "MV switch" },
      { "kind": "text", "x": 235, "y": 315, "rotation": 0, "z_index": 30, "text": "Battery storage" }
    ]
  }
  $$::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM runtime.diagram_templates WHERE name = 'Pole-mount battery storage'
);

-- =========================================================================
-- Pad-mount transformer + feeder pillar (URD)  (PAD_MOUNT_URD)
--   Anchor = DIST_XFMR (pad-mounted); internal item = 1x LV_LINK_BOX.
--   The transformer is the anchor, so it gets a slot here too (bind it to the
--   assembly's anchor DIST_XFMR).
-- =========================================================================
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT
  'Pad-mount transformer + feeder pillar (URD)',
  'Underground residential distribution single-line: pad-mount transformer feeding an LV feeder pillar (link box). Bind the transformer slot to the assembly anchor and the pillar slot to its fan-out component.',
  $$
  {
    "elements": [
      { "kind": "line", "x": 200, "y": 40,  "rotation": 0, "z_index": 0, "path": [[200,40],[200,100]],  "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 160, "rotation": 0, "z_index": 0, "path": [[200,160],[200,240]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 300, "rotation": 0, "z_index": 0, "path": [[200,300],[200,360]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 120, "y": 360, "rotation": 0, "z_index": 0, "path": [[120,360],[280,360]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 120, "y": 360, "rotation": 0, "z_index": 0, "path": [[120,360],[120,410]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 280, "y": 360, "rotation": 0, "z_index": 0, "path": [[280,360],[280,410]], "style": {"stroke": "var(--foreground)"} },

      { "kind": "slot", "x": 200, "y": 130, "rotation": 0, "z_index": 20, "slot_class": "DIST_XFMR" },
      { "kind": "slot", "x": 200, "y": 270, "rotation": 0, "z_index": 20, "slot_class": "LV_LINK_BOX" },

      { "kind": "text", "x": 235, "y": 34,  "rotation": 0, "z_index": 30, "text": "MV incomer" },
      { "kind": "text", "x": 235, "y": 135, "rotation": 0, "z_index": 30, "text": "Pad-mount transformer" },
      { "kind": "text", "x": 235, "y": 275, "rotation": 0, "z_index": 30, "text": "Feeder pillar (link box)" },
      { "kind": "text", "x": 150, "y": 430, "rotation": 0, "z_index": 30, "text": "LV feeders" }
    ]
  }
  $$::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM runtime.diagram_templates WHERE name = 'Pad-mount transformer + feeder pillar (URD)'
);

-- =========================================================================
-- MV switchgear panel -- Incomer  (panel role INCOMER)
--   2x MV_ISOLATOR + 1x MV_BREAKER + 1x MV_CT + 1x MV_EARTH_SWITCH
--   + 1x MV_VT + 1x PROTECTIVE_RELAY
-- =========================================================================
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT
  'MV switchgear panel — Incomer',
  'Metal-clad incomer bay single-line: bus isolator, breaker, CT, cable isolator, earth switch, VT and protective relay. Bind each slot to the panel''s fan-out devices.',
  $$
  {
    "elements": [
      { "kind": "line", "x": 200, "y": 60,  "rotation": 0, "z_index": 0, "path": [[80,60],[320,60]],   "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 60,  "rotation": 0, "z_index": 0, "path": [[200,60],[200,90]],   "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 130, "rotation": 0, "z_index": 0, "path": [[200,130],[200,160]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 200, "rotation": 0, "z_index": 0, "path": [[200,200],[200,230]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 270, "rotation": 0, "z_index": 0, "path": [[200,270],[200,300]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 340, "rotation": 0, "z_index": 0, "path": [[200,340],[200,400]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 250, "rotation": 0, "z_index": 0, "path": [[200,250],[110,250]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 320, "rotation": 0, "z_index": 0, "path": [[200,320],[300,320]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "control", "x": 200, "y": 180, "rotation": 0, "z_index": 0, "path": [[240,180],[360,180]], "style": {"stroke": "var(--foreground)"} },

      { "kind": "slot", "x": 200, "y": 110, "rotation": 0, "z_index": 20, "slot_class": "MV_ISOLATOR" },
      { "kind": "slot", "x": 200, "y": 180, "rotation": 0, "z_index": 20, "slot_class": "MV_BREAKER" },
      { "kind": "slot", "x": 200, "y": 250, "rotation": 0, "z_index": 20, "slot_class": "MV_CT" },
      { "kind": "slot", "x": 200, "y": 320, "rotation": 0, "z_index": 20, "slot_class": "MV_ISOLATOR" },
      { "kind": "slot", "x": 110, "y": 250, "rotation": 0, "z_index": 20, "slot_class": "MV_VT" },
      { "kind": "slot", "x": 300, "y": 320, "rotation": 0, "z_index": 20, "slot_class": "MV_EARTH_SWITCH" },
      { "kind": "slot", "x": 360, "y": 180, "rotation": 0, "z_index": 20, "slot_class": "PROTECTIVE_RELAY" },

      { "kind": "text", "x": 30,  "y": 54,  "rotation": 0, "z_index": 30, "text": "Busbar" },
      { "kind": "text", "x": 235, "y": 112, "rotation": 0, "z_index": 30, "text": "Bus isolator" },
      { "kind": "text", "x": 235, "y": 182, "rotation": 0, "z_index": 30, "text": "Breaker" },
      { "kind": "text", "x": 235, "y": 252, "rotation": 0, "z_index": 30, "text": "CT" },
      { "kind": "text", "x": 235, "y": 322, "rotation": 0, "z_index": 30, "text": "Cable isolator" },
      { "kind": "text", "x": 40,  "y": 252, "rotation": 0, "z_index": 30, "text": "VT" },
      { "kind": "text", "x": 300, "y": 342, "rotation": 0, "z_index": 30, "text": "Earth switch" },
      { "kind": "text", "x": 360, "y": 172, "rotation": 0, "z_index": 30, "text": "Relay" },
      { "kind": "text", "x": 175, "y": 420, "rotation": 0, "z_index": 30, "text": "Cable" }
    ]
  }
  $$::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM runtime.diagram_templates WHERE name = 'MV switchgear panel — Incomer'
);

-- =========================================================================
-- MV switchgear panel -- Feeder  (panel roles FEEDER / TRANSFORMER /
--   BUS_SECTION / BUS_COUPLER -- identical device composition)
--   2x MV_ISOLATOR + 1x MV_BREAKER + 1x MV_CT + 1x MV_EARTH_SWITCH
--   + 1x PROTECTIVE_RELAY  (no VT)
-- =========================================================================
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT
  'MV switchgear panel — Feeder',
  'Metal-clad feeder bay single-line (also covers transformer, bus-section and bus-coupler panels — same devices): bus isolator, breaker, CT, cable isolator, earth switch and protective relay. Bind each slot to the panel''s fan-out devices.',
  $$
  {
    "elements": [
      { "kind": "line", "x": 200, "y": 60,  "rotation": 0, "z_index": 0, "path": [[80,60],[320,60]],   "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 60,  "rotation": 0, "z_index": 0, "path": [[200,60],[200,90]],   "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 130, "rotation": 0, "z_index": 0, "path": [[200,130],[200,160]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 200, "rotation": 0, "z_index": 0, "path": [[200,200],[200,230]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 270, "rotation": 0, "z_index": 0, "path": [[200,270],[200,300]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 340, "rotation": 0, "z_index": 0, "path": [[200,340],[200,400]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 200, "y": 320, "rotation": 0, "z_index": 0, "path": [[200,320],[300,320]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "control", "x": 200, "y": 180, "rotation": 0, "z_index": 0, "path": [[240,180],[360,180]], "style": {"stroke": "var(--foreground)"} },

      { "kind": "slot", "x": 200, "y": 110, "rotation": 0, "z_index": 20, "slot_class": "MV_ISOLATOR" },
      { "kind": "slot", "x": 200, "y": 180, "rotation": 0, "z_index": 20, "slot_class": "MV_BREAKER" },
      { "kind": "slot", "x": 200, "y": 250, "rotation": 0, "z_index": 20, "slot_class": "MV_CT" },
      { "kind": "slot", "x": 200, "y": 320, "rotation": 0, "z_index": 20, "slot_class": "MV_ISOLATOR" },
      { "kind": "slot", "x": 300, "y": 320, "rotation": 0, "z_index": 20, "slot_class": "MV_EARTH_SWITCH" },
      { "kind": "slot", "x": 360, "y": 180, "rotation": 0, "z_index": 20, "slot_class": "PROTECTIVE_RELAY" },

      { "kind": "text", "x": 30,  "y": 54,  "rotation": 0, "z_index": 30, "text": "Busbar" },
      { "kind": "text", "x": 235, "y": 112, "rotation": 0, "z_index": 30, "text": "Bus isolator" },
      { "kind": "text", "x": 235, "y": 182, "rotation": 0, "z_index": 30, "text": "Breaker" },
      { "kind": "text", "x": 235, "y": 252, "rotation": 0, "z_index": 30, "text": "CT" },
      { "kind": "text", "x": 235, "y": 322, "rotation": 0, "z_index": 30, "text": "Cable isolator" },
      { "kind": "text", "x": 300, "y": 342, "rotation": 0, "z_index": 30, "text": "Earth switch" },
      { "kind": "text", "x": 360, "y": 172, "rotation": 0, "z_index": 30, "text": "Relay" },
      { "kind": "text", "x": 175, "y": 420, "rotation": 0, "z_index": 30, "text": "Cable" }
    ]
  }
  $$::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM runtime.diagram_templates WHERE name = 'MV switchgear panel — Feeder'
);

-- =========================================================================
-- LV feeder pillar -- 4-way  (config WAYS_4: 1x LV_BUSBAR + 4x LV_FUSE)
--   Anchor = LV_PILLAR (a diagram container, no slot of its own). The bus is
--   the one line-drawn slot; each fuse way hangs below it and feeds a feeder.
-- =========================================================================
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT
  'Feeder pillar — 4-way',
  'LV feeder pillar single-line: an incoming main onto the internal LV busbar, fanning out to four fused outgoing ways. Bind the bus slot and each fuse slot to the pillar''s fan-out components.',
  $$
  {
    "elements": [
      { "kind": "line", "x": 400, "y": 40,  "rotation": 0, "z_index": 0, "path": [[400,40],[400,120]],  "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 160, "y": 120, "rotation": 0, "z_index": 0, "path": [[160,120],[160,180]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 320, "y": 120, "rotation": 0, "z_index": 0, "path": [[320,120],[320,180]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 480, "y": 120, "rotation": 0, "z_index": 0, "path": [[480,120],[480,180]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 640, "y": 120, "rotation": 0, "z_index": 0, "path": [[640,120],[640,180]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 160, "y": 240, "rotation": 0, "z_index": 0, "path": [[160,240],[160,340]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 320, "y": 240, "rotation": 0, "z_index": 0, "path": [[320,240],[320,340]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 480, "y": 240, "rotation": 0, "z_index": 0, "path": [[480,240],[480,340]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 640, "y": 240, "rotation": 0, "z_index": 0, "path": [[640,240],[640,340]], "style": {"stroke": "var(--foreground)"} },

      { "kind": "slot", "x": 80,  "y": 120, "rotation": 0, "z_index": 20, "slot_class": "LV_BUSBAR", "path": [[80,120],[720,120]] },
      { "kind": "slot", "x": 160, "y": 210, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },
      { "kind": "slot", "x": 320, "y": 210, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },
      { "kind": "slot", "x": 480, "y": 210, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },
      { "kind": "slot", "x": 640, "y": 210, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },

      { "kind": "text", "x": 420, "y": 34,  "rotation": 0, "z_index": 30, "text": "Incoming main" },
      { "kind": "text", "x": 20,  "y": 112, "rotation": 0, "z_index": 30, "text": "Pillar busbar" },
      { "kind": "text", "x": 130, "y": 365, "rotation": 0, "z_index": 30, "text": "Way 1" },
      { "kind": "text", "x": 290, "y": 365, "rotation": 0, "z_index": 30, "text": "Way 2" },
      { "kind": "text", "x": 450, "y": 365, "rotation": 0, "z_index": 30, "text": "Way 3" },
      { "kind": "text", "x": 610, "y": 365, "rotation": 0, "z_index": 30, "text": "Way 4" }
    ]
  }
  $$::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM runtime.diagram_templates WHERE name = 'Feeder pillar — 4-way'
);

-- =========================================================================
-- LV feeder pillar -- 6-way  (config WAYS_6: 1x LV_BUSBAR + 6x LV_FUSE)
-- =========================================================================
INSERT INTO runtime.diagram_templates (name, description, payload)
SELECT
  'Feeder pillar — 6-way',
  'LV feeder pillar single-line: an incoming main onto the internal LV busbar, fanning out to six fused outgoing ways. Bind the bus slot and each fuse slot to the pillar''s fan-out components.',
  $$
  {
    "elements": [
      { "kind": "line", "x": 400, "y": 40,  "rotation": 0, "z_index": 0, "path": [[400,40],[400,120]],  "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 100, "y": 120, "rotation": 0, "z_index": 0, "path": [[100,120],[100,180]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 220, "y": 120, "rotation": 0, "z_index": 0, "path": [[220,120],[220,180]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 340, "y": 120, "rotation": 0, "z_index": 0, "path": [[340,120],[340,180]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 460, "y": 120, "rotation": 0, "z_index": 0, "path": [[460,120],[460,180]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 580, "y": 120, "rotation": 0, "z_index": 0, "path": [[580,120],[580,180]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 700, "y": 120, "rotation": 0, "z_index": 0, "path": [[700,120],[700,180]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 100, "y": 240, "rotation": 0, "z_index": 0, "path": [[100,240],[100,340]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 220, "y": 240, "rotation": 0, "z_index": 0, "path": [[220,240],[220,340]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 340, "y": 240, "rotation": 0, "z_index": 0, "path": [[340,240],[340,340]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 460, "y": 240, "rotation": 0, "z_index": 0, "path": [[460,240],[460,340]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 580, "y": 240, "rotation": 0, "z_index": 0, "path": [[580,240],[580,340]], "style": {"stroke": "var(--foreground)"} },
      { "kind": "line", "x": 700, "y": 240, "rotation": 0, "z_index": 0, "path": [[700,240],[700,340]], "style": {"stroke": "var(--foreground)"} },

      { "kind": "slot", "x": 60,  "y": 120, "rotation": 0, "z_index": 20, "slot_class": "LV_BUSBAR", "path": [[60,120],[740,120]] },
      { "kind": "slot", "x": 100, "y": 210, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },
      { "kind": "slot", "x": 220, "y": 210, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },
      { "kind": "slot", "x": 340, "y": 210, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },
      { "kind": "slot", "x": 460, "y": 210, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },
      { "kind": "slot", "x": 580, "y": 210, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },
      { "kind": "slot", "x": 700, "y": 210, "rotation": 0, "z_index": 20, "slot_class": "LV_FUSE" },

      { "kind": "text", "x": 420, "y": 34,  "rotation": 0, "z_index": 30, "text": "Incoming main" },
      { "kind": "text", "x": 5,   "y": 112, "rotation": 0, "z_index": 30, "text": "Pillar busbar" },
      { "kind": "text", "x": 75,  "y": 365, "rotation": 0, "z_index": 30, "text": "Way 1" },
      { "kind": "text", "x": 195, "y": 365, "rotation": 0, "z_index": 30, "text": "Way 2" },
      { "kind": "text", "x": 315, "y": 365, "rotation": 0, "z_index": 30, "text": "Way 3" },
      { "kind": "text", "x": 435, "y": 365, "rotation": 0, "z_index": 30, "text": "Way 4" },
      { "kind": "text", "x": 555, "y": 365, "rotation": 0, "z_index": 30, "text": "Way 5" },
      { "kind": "text", "x": 675, "y": 365, "rotation": 0, "z_index": 30, "text": "Way 6" }
    ]
  }
  $$::jsonb
WHERE NOT EXISTS (
  SELECT 1 FROM runtime.diagram_templates WHERE name = 'Feeder pillar — 6-way'
);
