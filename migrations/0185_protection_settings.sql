-- 0185: the per-device protection (relay) settings register.
--
-- The as-built OVERCURRENT protection settings for a protective device -- the
-- numeric pickups, time dials, instantaneous thresholds, CT ratio and reclose
-- scheme that a TCC curve alone does not capture. One row per device
-- (feature_class_code, feature_id); UPSERTed, so a device has at most one record.

CREATE TABLE ops.protection_settings (
    id                     uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    feature_class_code     text NOT NULL,
    feature_id             uuid NOT NULL,

    relay_model            text,          -- e.g. "SEL-751", "ABB REF615"
    ct_ratio               text,          -- e.g. "600/5"

    -- Phase overcurrent (ANSI 51 / 50)
    phase_pickup_a         numeric,       -- 51 pickup, primary amps
    phase_time_dial        numeric,       -- 51 time dial / TMS
    phase_curve            text,          -- curve family (e.g. IEC_SI, IEEE_MI, DT)
    phase_instantaneous_a  numeric,       -- 50 instantaneous pickup, primary amps

    -- Ground / earth fault overcurrent (ANSI 51N / 50N)
    ground_pickup_a        numeric,
    ground_time_dial       numeric,
    ground_curve           text,
    ground_instantaneous_a numeric,

    -- Reclosing scheme (reclosers)
    reclose_operations     integer,       -- operations to lockout
    reclose_intervals      text,          -- e.g. "0, 5, 15 s"

    notes                  text,
    updated_by             uuid,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now(),

    UNIQUE (feature_class_code, feature_id)
);

COMMENT ON TABLE ops.protection_settings IS
    'As-built relay / overcurrent protection settings per protective device (one row per feature): the numeric pickups / time dials / instantaneous / CT ratio / reclose scheme a TCC curve does not capture. See migration 0185.';
