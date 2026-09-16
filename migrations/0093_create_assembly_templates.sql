-- 0093_create_assembly_templates.sql
-- Purpose: Phase 57 -- the metadata behind a GENERAL "place one anchor feature and
-- get its whole pre-wired kit" engine (api/internal/assembly, POST /assemblies).
-- Generalises the two earlier fan-outs:
--   * RMU (migration 0073)         -- a STAR: N ways wire to one shared bus.
--   * packagedsub (migration 0092) -- a CHAIN: each item wires to the PREVIOUS one.
-- Neither can express the pole-mounted overhead transformer, which BRANCHES: the
-- surge arrester taps the transformer's primary node and the LV board hangs off
-- the secondary, so two items both wire back to the transformer (not to "the
-- previous item"); and the pole holds the kit by spatial_support, not containment
-- (a POLE is network_role 'none', not a container). So each item here names,
-- independently, BOTH its structural parent + relationship kind AND its electrical
-- wiring target by position -- enough to describe a star, a chain OR a branch.
--
-- An assembly template is a per-(anchor class x code) recipe: one parent row, plus
-- one item row per component in creation order. The anchor is the map-placed
-- feature (a POLE, a pad-mount DIST_XFMR); items are geometry-optional children.
-- No feature class or terminal is hardcoded in Go -- the endpoint reads this table.
-- A class/code pairing with no template row is "no kit defined" -> a clear 400.

CREATE TABLE metadata.assembly_templates (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    anchor_feature_class_id uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    -- code identifies the recipe within its anchor class (POLE_MOUNT_STD,
    -- PAD_MOUNT_URD). It is NOT a feature attribute -- the anchor (POLE / DIST_XFMR)
    -- has no config field -- it is purely the selector the POST passes and the menu
    -- labels, so it lives here as plain text rather than a domain_values FK (which
    -- RMU/packagedsub used only because their config was ALSO stored on the unit).
    code                    text NOT NULL,
    name                    text NOT NULL,
    -- When set, forces the anchor's subtype (e.g. PAD_MOUNT_URD anchors a DIST_XFMR
    -- as subtype 2 pad_mounted). NULL lets the placing user pick (e.g. pole wood/
    -- concrete/steel).
    anchor_subtype_code     integer,
    created_at              timestamptz NOT NULL DEFAULT now()
);

-- One recipe per (anchor class, code); doubles as the seed's idempotent ON CONFLICT
-- target.
CREATE UNIQUE INDEX assembly_templates_key
    ON metadata.assembly_templates (anchor_feature_class_id, code);

COMMENT ON TABLE metadata.assembly_templates IS 'Phase 57: one general kit-assembly recipe per (anchor feature class x code). Components live, in creation order, in metadata.assembly_items. Read by api/internal/assembly to place an anchor plus its pre-wired kit (pole-mount overhead transformer, pad-mount URD, and future recloser/capacitor/BESS kits) in one transaction.';

-- One item = one component the recipe creates. position orders creation (1..N; the
-- anchor is conceptual position 0). subtype_code/attributes make the fanned-out
-- feature pass the same create validation a manual create does. The two axes that
-- make this general:
--   * STRUCTURAL: support_kind (relationship kind, e.g. 'spatial_support') links
--     this item to support_parent_position (NULL = the anchor). NULL support_kind =
--     no structural link (e.g. the URD pillar, merely adjacent to its transformer).
--   * ELECTRICAL: wire_to_position names which already-created feature this item
--     connects to (0 = the anchor, >=1 = an earlier item; NULL = no wiring, e.g. an
--     externally-fed incomer). upstream_terminal is the terminal on that target;
--     own_terminal is the terminal on THIS item. Either NULL for a terminal-less leg.
-- Targets must reference position 0 or an EARLIER item so a single forward pass
-- wires the whole kit.
CREATE TABLE metadata.assembly_items (
    id                         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id                uuid NOT NULL REFERENCES metadata.assembly_templates(id) ON DELETE CASCADE,
    component_feature_class_id uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    subtype_code               integer,
    attributes                 jsonb NOT NULL DEFAULT '{}'::jsonb,
    support_kind               text,
    support_parent_position    integer,
    wire_to_position           integer,
    own_terminal               text,
    upstream_terminal          text,
    position                   integer NOT NULL,
    offset_x                   double precision NOT NULL DEFAULT 0,
    offset_y                   double precision NOT NULL DEFAULT 0,
    rotation                   double precision NOT NULL DEFAULT 0,
    created_at                 timestamptz NOT NULL DEFAULT now()
);

-- Deterministic ordering within a template + the seed's idempotent ON CONFLICT
-- target.
CREATE UNIQUE INDEX assembly_items_position
    ON metadata.assembly_items (template_id, position);

-- The endpoint loads every item for one template at once, ordered by position.
CREATE INDEX assembly_items_template_idx
    ON metadata.assembly_items (template_id);

COMMENT ON TABLE metadata.assembly_items IS 'Phase 57: one component per assembly template, in creation order -- class + subtype + default attributes + its structural parent/kind (support_*) + its electrical wiring target by position (wire_to_position + terminals) + mimic offset. Fanned out by api/internal/assembly through the shared crud.CreateFeature, relationship.EstablishRelationship and connectivity.EstablishConnection.';
