-- 0098_create_wam_inspections.sql
-- Purpose: Phase 59 Slice 2 -- inspection templates & execution, layered on the
-- WAM core (migration 0096). This is what makes the 'inspection' work TYPE do
-- something structured: a reusable checklist (template) is filled out against an
-- asset (an inspection), and a failing checklist item that carries a severity
-- auto-raises a wam.defects row (source='inspection') -- which then feeds the
-- existing defect -> corrective-work-order loop. No new permissions: inspections
-- read/write through the same wam:read / wam:write as the rest of the module.
--
-- Four tables, in dependency order:
--
--   inspection_templates        a reusable checklist definition (e.g.
--                               "MV switchgear annual inspection").
--   inspection_template_items   the ordered questions on a template. An item's
--                               fail_severity (when set) is what turns a failing
--                               answer into an auto-raised defect.
--   inspections                 one execution of a checklist against an asset,
--                               usually while completing an inspection work
--                               order. overall_result rolls up the items.
--   inspection_item_results     the answered questions. Prompt/response_type are
--                               SNAPSHOTTED off the template item (templates
--                               evolve; a historical record must not). defect_id
--                               links the defect a failing item raised.
--
-- FK convention (matches 0096): references WITHIN the wam schema are real FKs
-- (template_id, inspection_id, work_order_id, defect_id, template_item_id); the
-- asset (asset_class + asset_id) stays a bare ref to a compiled per-class table.

-- ---------------------------------------------------------------------------
-- inspection_templates
-- ---------------------------------------------------------------------------
CREATE TABLE wam.inspection_templates (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Stable human slug (e.g. MV_SWITCHGEAR_ANNUAL) so callers can reference a
    -- template without knowing its uuid.
    code         text NOT NULL UNIQUE,
    name         text NOT NULL,
    description  text,
    -- Optional scope: the feature class this checklist is meant for (null = any
    -- asset). Bare ref, informational -- not enforced.
    asset_class  text,
    -- A retired template stays for history but is hidden from the "record"
    -- picker rather than deleted (its inspections still reference it).
    active       boolean NOT NULL DEFAULT true,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX inspection_templates_active_idx ON wam.inspection_templates (active) WHERE active;

-- ---------------------------------------------------------------------------
-- inspection_template_items
-- ---------------------------------------------------------------------------
CREATE TABLE wam.inspection_template_items (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    template_id   uuid NOT NULL REFERENCES wam.inspection_templates (id) ON DELETE CASCADE,
    -- 1-based display order within the template.
    position      integer NOT NULL,
    prompt        text NOT NULL,
    -- How the answer is captured. pass_fail is the common case; numeric/rating
    -- capture a reading; text a free note. Any item can still be marked failing
    -- (which is what drives the defect) regardless of response_type.
    response_type text NOT NULL DEFAULT 'pass_fail'
                      CHECK (response_type IN ('pass_fail', 'rating', 'numeric', 'text')),
    -- When set, a FAILING answer on this item auto-raises a defect at this
    -- severity. Null = the item is informational and never raises a defect.
    fail_severity text CHECK (fail_severity IN ('low', 'medium', 'high', 'critical')),
    guidance      text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (template_id, position)
);

CREATE INDEX inspection_template_items_template_idx ON wam.inspection_template_items (template_id);

-- ---------------------------------------------------------------------------
-- inspections
-- ---------------------------------------------------------------------------
CREATE TABLE wam.inspections (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- The inspection work order this fulfills, if any. SET NULL so deleting the
    -- order doesn't erase the inspection record.
    work_order_id  uuid REFERENCES wam.work_orders (id) ON DELETE SET NULL,
    -- The template used. SET NULL so a template can be pruned without losing the
    -- inspection (whose items are snapshotted anyway).
    template_id    uuid REFERENCES wam.inspection_templates (id) ON DELETE SET NULL,
    -- The asset inspected (feature class code + id). Required. Bare refs, no FK.
    asset_class    text NOT NULL,
    asset_id       uuid NOT NULL,
    overall_result text NOT NULL DEFAULT 'pass'
                       CHECK (overall_result IN ('pass', 'fail', 'conditional')),
    inspected_by   text,
    inspected_at   timestamptz NOT NULL DEFAULT now(),
    notes          text,
    -- Where the inspection happened (optional pin), for a later map overlay.
    location       geometry(Point, 4326),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX inspections_asset_idx ON wam.inspections (asset_class, asset_id);
CREATE INDEX inspections_work_order_idx ON wam.inspections (work_order_id) WHERE work_order_id IS NOT NULL;
CREATE INDEX inspections_template_idx ON wam.inspections (template_id) WHERE template_id IS NOT NULL;
CREATE INDEX inspections_when_idx ON wam.inspections (inspected_at);
CREATE INDEX inspections_location_idx ON wam.inspections USING gist (location);

-- ---------------------------------------------------------------------------
-- inspection_item_results
-- ---------------------------------------------------------------------------
CREATE TABLE wam.inspection_item_results (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    inspection_id    uuid NOT NULL REFERENCES wam.inspections (id) ON DELETE CASCADE,
    -- The template item this answers. SET NULL for an ad-hoc item, or if the
    -- template item is later removed (prompt/response_type are snapshotted below,
    -- so the record stays legible regardless).
    template_item_id uuid REFERENCES wam.inspection_template_items (id) ON DELETE SET NULL,
    position         integer NOT NULL,
    prompt           text NOT NULL,
    response_type    text NOT NULL,
    -- pass/fail outcome. Null = not applicable / not answered as pass-fail. A
    -- false here on an item with a fail_severity is what raised defect_id.
    passed           boolean,
    value_text       text,
    value_numeric    double precision,
    -- The defect this failing item auto-raised, if any. SET NULL so pruning a
    -- defect doesn't erase the inspection result.
    defect_id        uuid REFERENCES wam.defects (id) ON DELETE SET NULL,
    notes            text,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX inspection_item_results_inspection_idx ON wam.inspection_item_results (inspection_id);
CREATE INDEX inspection_item_results_defect_idx ON wam.inspection_item_results (defect_id) WHERE defect_id IS NOT NULL;
