-- 0092_create_packaged_substation_templates.sql
-- Purpose: Phase 56 -- the metadata behind "place one packaged substation and
-- get its whole pre-wired internal kit (MV switch + transformer + LV board)."
-- A packaged-substation template is a per-(packaged class x configuration)
-- recipe: one parent row naming the recipe, plus one item row per internal
-- component, in the ORDER they wire together as a series chain.
--
-- This is the metadata.rmu_templates pattern (migration 0073) with ONE change:
-- RMU fans out a STAR -- every switched way wires to a single shared internal
-- bus (rmu_template_items.bus_terminal names the one bus-side terminal). A
-- packaged substation is a series CHAIN -- MV switch -> transformer -> LV board
-- -- so an item wires to the PREVIOUS item, not to a shared bus. Each item
-- therefore carries TWO terminal names instead of one:
--   * upstream_terminal -- the terminal on the PREVIOUS item this item connects
--     to (e.g. MV_SWITCH.T2 for the transformer, DIST_XFMR.T2 for the LV board).
--   * own_terminal      -- the terminal on THIS item that connects to it (e.g.
--     DIST_XFMR.T1, LV_BUSBAR.T1).
-- Either may be NULL for a terminal-less (bus-style) leg, exactly as RMU wires
-- the bus side terminal-less. The first item in a chain (position 1) has no
-- upstream to wire, so both terminals are ignored for it.
--
-- Keyed by (packaged_feature_class_id, config_value_id), NOT config alone: the
-- config codes are PACKAGED_SUB_CONFIG domain values, and a future second
-- packaged class could carry its own kit for the same config, so the class is
-- part of the key -- exactly as rmu_templates keys by (RMU class x config).
--
-- No feature class or terminal is hardcoded in Go: the composite endpoint
-- (api/internal/packagedsub) reads this table to learn what a packaged
-- substation of a given config contains. A class/config pairing with NO template
-- row is "no fan-out defined" -- the endpoint rejects it with a clear 400 rather
-- than building an empty enclosure.

CREATE TABLE metadata.packaged_substation_templates (
    id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    packaged_feature_class_id uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    config_value_id           uuid NOT NULL REFERENCES metadata.domain_values(id) ON DELETE CASCADE,
    name                      text NOT NULL,
    created_at                timestamptz NOT NULL DEFAULT now()
);

-- One template per (packaged class, config) -- a duplicate is a data-entry
-- mistake, not a second distinct recipe. Doubles as the ON CONFLICT target the
-- seed relies on to stay idempotent.
CREATE UNIQUE INDEX packaged_substation_templates_key
    ON metadata.packaged_substation_templates (packaged_feature_class_id, config_value_id);

COMMENT ON TABLE metadata.packaged_substation_templates IS 'Phase 56: one assembly recipe per (packaged-substation feature class x configuration). The internal components live, in chain order, in metadata.packaged_substation_items. Read by api/internal/packagedsub to build a packaged substation plus its pre-wired MV switch / transformer / LV board in one transaction.';

-- One item = one internal component the recipe creates, in chain order.
-- component_feature_class_id is the class to create (MV_SWITCH / DIST_XFMR /
-- LV_BUSBAR); subtype_code picks its subtype; attributes carries the default
-- coded values its required fields need so the fanned-out feature passes the
-- same create-time validation a manual create does -- the crew edits the real
-- values afterward. own_terminal / upstream_terminal name the two ends of the
-- leg that wires this item to the previous one (NULL = terminal-less), named
-- here not hardcoded so the fan-out stays metadata-driven. offset_x/offset_y/
-- rotation are the component's place on the future mimic. position orders the
-- chain deterministically (position 1 is the MV incomer; its terminals are
-- unused as it has no upstream item).
CREATE TABLE metadata.packaged_substation_items (
    id                        uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id               uuid NOT NULL REFERENCES metadata.packaged_substation_templates(id) ON DELETE CASCADE,
    component_feature_class_id uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    subtype_code              integer,
    attributes                jsonb NOT NULL DEFAULT '{}'::jsonb,
    own_terminal              text,
    upstream_terminal         text,
    position                  integer NOT NULL,
    offset_x                  double precision NOT NULL DEFAULT 0,
    offset_y                  double precision NOT NULL DEFAULT 0,
    rotation                  double precision NOT NULL DEFAULT 0,
    created_at                timestamptz NOT NULL DEFAULT now()
);

-- Deterministic ordering within a template, and the ON CONFLICT target the seed
-- upserts against so re-running it is idempotent.
CREATE UNIQUE INDEX packaged_substation_items_position
    ON metadata.packaged_substation_items (template_id, position);

-- The composite endpoint loads every item for one template at once, ordered by
-- position, so index the leading column.
CREATE INDEX packaged_substation_items_template_idx
    ON metadata.packaged_substation_items (template_id);

COMMENT ON TABLE metadata.packaged_substation_items IS 'Phase 56: one internal component per packaged-substation template, in chain order -- class + subtype + default attributes + the terminals wiring it to the previous component + mimic offset. Fanned out by api/internal/packagedsub through the shared crud.CreateFeature, wired into the enclosure by containment and to the previous component by connectivity.';
