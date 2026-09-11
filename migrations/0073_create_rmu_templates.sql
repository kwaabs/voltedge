-- 0073_create_rmu_templates.sql
-- Purpose: Phase RMU Slice 2 -- the metadata behind "place one Ring Main Unit
-- and get its whole assembly of switched ways + internal bus." An RMU template
-- is a per-(RMU class × configuration) recipe: one parent row naming the recipe
-- and the internal bus class to create once, plus one item row per switched way
-- the recipe fans out. Placing a 2+1 RMU then means "create the RMU, create its
-- one busbar, create the ways the 2+1 template lists, wire each way into the RMU
-- by containment, and connect each way's bus-side terminal to the busbar" -- all
-- in one transaction (the composite endpoint in api/internal/rmu).
--
-- This is the panel_templates pattern (migration 0072) with ONE addition the
-- panel fan-out never needed: internal connectivity. A panel only wires
-- containment; an RMU is an electrical assembly, so its ways must actually
-- connect to the shared bus for isolation traces to be correct. The way's
-- bus-side terminal is named per item (bus_terminal) rather than hardcoded in
-- Go, keeping the fan-out metadata-driven the same way the class codes are.
--
-- Keyed by (rmu_feature_class_id, config_value_id), NOT config alone: the config
-- codes are RMU_CONFIG domain values (2W_1T, 2W_2T, ...), and a future second RMU
-- class would carry its own distinct way set for the same config, so the RMU
-- class is part of the key -- exactly as panel_templates keys by (panel class ×
-- role) and diagram_placement_rules (migration 0068) keys by (container × type).
--
-- No feature class, way set or terminal is hardcoded in Go: the composite
-- endpoint reads this table to learn what an RMU of a given config contains. An
-- RMU class/config pairing with NO template row is "no fan-out defined" -- the
-- endpoint rejects it with a clear 400 rather than building an empty RMU.

CREATE TABLE metadata.rmu_templates (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    rmu_feature_class_id   uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    config_value_id        uuid NOT NULL REFERENCES metadata.domain_values(id) ON DELETE CASCADE,
    busbar_feature_class_id uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    name                   text NOT NULL,
    created_at             timestamptz NOT NULL DEFAULT now()
);

-- One template per (RMU class, config) -- a duplicate is a data-entry mistake,
-- not a second distinct recipe. Doubles as the ON CONFLICT target the seed
-- relies on to stay idempotent.
CREATE UNIQUE INDEX rmu_templates_key
    ON metadata.rmu_templates (rmu_feature_class_id, config_value_id);

COMMENT ON TABLE metadata.rmu_templates IS 'Phase RMU: one assembly recipe per (RMU feature class × configuration). Names the single internal bus class to create; ways live in metadata.rmu_template_items. Read by api/internal/rmu to build an RMU plus its ways and internal bus wiring in one transaction.';

-- One item = one switched way the recipe creates. way_feature_class_id is the
-- class to create (RMU_WAY); subtype_code picks its subtype (ring / transformer);
-- attributes carries the default coded values its required fields need
-- (device_state, condition_rating) so the fanned-out feature passes the same
-- create-time validation a manual create does -- the crew edits the real values
-- afterward. bus_terminal is the way terminal wired to the busbar (T2, the
-- bus/internal side) -- named here, not hardcoded, so the fan-out stays
-- metadata-driven. offset_x/offset_y/rotation are the way's place on the future
-- RMU mimic (Slice 3 draws them; Slice 2 only stores and returns them). position
-- orders the items deterministically.
CREATE TABLE metadata.rmu_template_items (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id         uuid NOT NULL REFERENCES metadata.rmu_templates(id) ON DELETE CASCADE,
    way_feature_class_id uuid NOT NULL REFERENCES metadata.feature_classes(id) ON DELETE CASCADE,
    subtype_code        integer,
    attributes          jsonb NOT NULL DEFAULT '{}'::jsonb,
    bus_terminal        text NOT NULL,
    position            integer NOT NULL,
    offset_x            double precision NOT NULL DEFAULT 0,
    offset_y            double precision NOT NULL DEFAULT 0,
    rotation            double precision NOT NULL DEFAULT 0,
    created_at          timestamptz NOT NULL DEFAULT now()
);

-- Deterministic ordering within a template, and the ON CONFLICT target the seed
-- upserts against so re-running it is idempotent.
CREATE UNIQUE INDEX rmu_template_items_position
    ON metadata.rmu_template_items (template_id, position);

-- The composite endpoint loads every way for one template at once, ordered by
-- position, so index the leading column.
CREATE INDEX rmu_template_items_template_idx
    ON metadata.rmu_template_items (template_id);

COMMENT ON TABLE metadata.rmu_template_items IS 'Phase RMU: one switched way per RMU template -- class + subtype + default attributes + the terminal wired to the bus + mimic offset. Fanned out by api/internal/rmu through the shared crud.CreateFeature, wired into the RMU by containment and to the busbar by connectivity.';
