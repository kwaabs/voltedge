-- 0072_create_panel_templates.sql
-- Purpose: Phase 42 Slice 2 -- the metadata behind "place one indoor switchgear
-- panel and get its whole standard set of devices." A panel template is a
-- per-(panel class × panel role) recipe: one parent row naming the recipe, plus
-- one item row per device the recipe fans out. Placing a FEEDER panel then means
-- "create the panel, then create the device set the FEEDER template lists and
-- wire each into the panel by containment" -- all in one transaction (the
-- composite endpoint in api/internal/panel).
--
-- Keyed by (panel_feature_class_id, role_value_id), NOT role alone: the role
-- codes are the shared BAY_ROLE domain values (INCOMER / FEEDER / BUS_SECTION /
-- BUS_COUPLER), and a future second panel class (e.g. an LV panel) would carry
-- its own distinct device set for the same role, so the panel class is part of
-- the key -- the same reasoning diagram_placement_rules (migration 0068) keys by
-- (container × type) rather than type alone.
--
-- No feature class or device list is hardcoded in Go: the composite endpoint
-- reads this table to learn what a panel of a given role contains, exactly the
-- way the CRUD engine reads metadata.fields to learn a class's columns. A panel
-- class/role pairing with NO template row is simply "no fan-out defined" -- the
-- endpoint rejects it with a clear 400 rather than silently creating an empty
-- panel.

CREATE TABLE metadata.panel_templates (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    panel_feature_class_id uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    role_value_id          uuid NOT NULL REFERENCES metadata.domain_values(id) ON DELETE CASCADE,
    name                   text NOT NULL,
    created_at             timestamptz NOT NULL DEFAULT now()
);

-- One template per (panel class, role) -- a duplicate is a data-entry mistake,
-- not a second distinct recipe. Doubles as the ON CONFLICT target the seed
-- relies on to stay idempotent.
CREATE UNIQUE INDEX panel_templates_key
    ON metadata.panel_templates (panel_feature_class_id, role_value_id);

COMMENT ON TABLE metadata.panel_templates IS 'Phase 42: one device-fan-out recipe per (panel feature class × panel role). Items in metadata.panel_template_items. Read by api/internal/panel to create a panel plus its standard device set in one transaction.';

-- One item = one device the recipe creates. device_feature_class_id is the class
-- to create (MV_BREAKER, MV_CT, ...); subtype_code picks its subtype; attributes
-- carries the default coded values the device's required fields need (device_state,
-- condition_rating, ct_ratio, ...) so the fanned-out feature passes the same
-- create-time validation a manual create does -- the crew edits the real values
-- afterward. offset_x/offset_y/rotation are the device's place on the board mimic
-- relative to the panel's own column (Phase 42 Slice 3 draws them; Slice 2 only
-- stores and returns them). position orders the items deterministically.
CREATE TABLE metadata.panel_template_items (
    id                      uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id             uuid NOT NULL REFERENCES metadata.panel_templates(id) ON DELETE CASCADE,
    device_feature_class_id uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    subtype_code            integer,
    attributes              jsonb NOT NULL DEFAULT '{}'::jsonb,
    position                integer NOT NULL,
    offset_x                double precision NOT NULL DEFAULT 0,
    offset_y                double precision NOT NULL DEFAULT 0,
    rotation                double precision NOT NULL DEFAULT 0,
    created_at              timestamptz NOT NULL DEFAULT now()
);

-- Deterministic ordering within a template, and the ON CONFLICT target the seed
-- upserts against so re-running it is idempotent.
CREATE UNIQUE INDEX panel_template_items_position
    ON metadata.panel_template_items (template_id, position);

-- The composite endpoint loads every item for one template at once, ordered by
-- position, so index the leading column.
CREATE INDEX panel_template_items_template_idx
    ON metadata.panel_template_items (template_id);

COMMENT ON TABLE metadata.panel_template_items IS 'Phase 42: one device per panel template -- class + subtype + default attributes + mimic offset. Fanned out by api/internal/panel through the shared crud.CreateFeature, wired into the panel by containment.';
