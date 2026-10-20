-- 0154_create_fault_modes.sql
-- Purpose: a per-feature-class FAILURE-MODE catalog (FMEA) for fault logging. The
-- faults.fault_events.cause column is deliberately free text (0106) -- this layers
-- a coded, class-aware taxonomy ON TOP so the fault form can offer "the faults that
-- can happen on THIS asset" as a pick-list, while cause stays for free-text
-- elaboration ("Other"). Each mode carries a light FMEA record: the failure
-- dimension, a typical cause, the effect, and a severity -- enough to brief a crew
-- and to report faults BY MODE (which feeds the WAM defect a fault books).
--
-- Keyed per feature_class_code (the admin chose per-class, not per-category), so a
-- transformer's modes differ from a conductor's. No FK to the metadata feature
-- class (fault records already hold class codes as bare refs, and the catalog must
-- survive a class being retired); the code is the join key the API resolves.

-- ---------------------------------------------------------------------------
-- faults.fault_modes -- one failure mode a given feature class can exhibit.
-- ---------------------------------------------------------------------------
CREATE TABLE faults.fault_modes (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The feature class this mode applies to (a metadata.feature_classes.code,
    -- held as a bare ref -- see the file header).
    feature_class_code  text NOT NULL,

    -- Stable mode code, unique within the class (UPPER_SNAKE). What fault_events
    -- .fault_mode stores; the label can change without breaking logged records.
    code                text NOT NULL,
    label               text NOT NULL,

    -- The failure DIMENSION, for grouping/filtering the picker and the reports.
    dimension           text NOT NULL DEFAULT 'other'
                            CHECK (dimension IN (
                                'electrical',    -- insulation, flashover, short, open circuit
                                'mechanical',    -- breakage, linkage, operating-mechanism
                                'thermal',       -- overheating, overload, cooling loss
                                'environmental', -- lightning, flood, vegetation, animal, third-party
                                'ageing',        -- wear-out, corrosion, end-of-life degradation
                                'operational',   -- mis-operation, protection mal-operation, control
                                'other'
                            )),

    -- The light FMEA payload: what usually brings this mode about, what it does to
    -- the network, and how bad. All optional except severity (defaulted).
    typical_cause       text,
    effect              text,
    severity            text NOT NULL DEFAULT 'medium'
                            CHECK (severity IN ('low', 'medium', 'high', 'critical')),

    -- Display order within the class (0 first); active=false retires a mode from
    -- the picker without deleting the rows that reference it.
    sort_order          integer NOT NULL DEFAULT 0,
    active              boolean NOT NULL DEFAULT true,

    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT fault_modes_code_uq UNIQUE (feature_class_code, code)
);

-- The picker's query: a class's active modes in display order.
CREATE INDEX fault_modes_class_idx ON faults.fault_modes (feature_class_code, sort_order) WHERE active;

COMMENT ON TABLE faults.fault_modes IS 'Per-feature-class failure-mode catalog (FMEA) for fault logging: the faults that can happen on each asset class, with dimension/typical cause/effect/severity. Feeds the fault form''s class-aware cause picker (GET /faults/modes?asset_class=). See seed/fault_modes_catalog.sql.';

-- The selected failure mode on a logged fault: a faults.fault_modes.code scoped by
-- the event's asset_class. Nullable (a fault may be logged before its mode is known,
-- or on an asset-less span); no FK so a retired mode never orphans a record. The
-- existing free-text `cause` stays as elaboration / the "Other" escape.
ALTER TABLE faults.fault_events ADD COLUMN fault_mode text;

COMMENT ON COLUMN faults.fault_events.fault_mode IS 'The selected failure mode (faults.fault_modes.code for this event''s asset_class). Nullable; the free-text cause column remains for elaboration or an uncatalogued mode.';
