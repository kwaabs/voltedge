-- 0079_create_feature_gps_captures.sql
-- Purpose: Phase gnss-quality Slice 3 -- an append-only record of the GNSS
-- survey quality captured at the moment a field crew placed (or moved) a feature
-- at their device's position. The map, REST and MCP write paths never carry this
-- (a map-tap has no GPS behind it); only the field tool's "place at my location"
-- stamps a sample, so this is the office's window into how good the fix was for
-- each GPS-sourced point.
--
-- One shared table for every feature class (not a per-class table), mirroring
-- gis.feature_history (migration 0067): feature_class_code + feature_id identify
-- the feature and the fix quality is stored as plain columns. One row per GPS
-- capture -- a feature placed then later re-placed gets two rows, newest first.
--
-- Every DOP / satellite / accuracy column is NULLABLE: an iPhone's internal
-- receiver reports only accuracy (no NMEA => no DOP/sats/fix-type), so those come
-- through null. source ('internal' | 'external') and captured_at are the only
-- always-present facts.
--
-- Deliberately NO foreign keys, exactly as feature_history:
--   * feature_id can't FK to one table (it spans every gis.* feature table);
--   * a purge of the feature must NOT cascade away its capture record;
--   * captured_by is a soft reference to iam.users (resolved by LEFT JOIN at read
--     time), left a bare uuid so deleting a user never erases the record.

CREATE TABLE gis.feature_gps_captures (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    feature_class_code text        NOT NULL,
    feature_id         uuid        NOT NULL,
    -- Dilution-of-precision figures from the receiver's NMEA (GGA/GSA); null on a
    -- source that doesn't expose NMEA (iOS internal).
    hdop               double precision,
    pdop               double precision,
    vdop               double precision,
    -- OS-reported estimates (geolocator); horizontal is usually present, the
    -- others often null.
    h_accuracy_m       double precision,
    v_accuracy_m       double precision,
    altitude_m         double precision,
    sats_used          integer,
    sats_in_view       integer,
    -- none | gps | dgps | rtk_float | rtk_fixed | unknown (the mobile GnssFixType
    -- name); null when the source reported no fix-type.
    fix_type           text,
    -- internal | external (which receiver produced the sample).
    source             text        NOT NULL,
    captured_by        uuid,       -- the device's submitting user; NULL if unknown
    captured_at        timestamptz NOT NULL,        -- when the crew stood at the point
    recorded_at        timestamptz NOT NULL DEFAULT now()  -- when the server persisted it
);

-- The one access pattern: a single feature's capture record, newest first.
CREATE INDEX feature_gps_captures_feature_idx
    ON gis.feature_gps_captures (feature_class_code, feature_id, captured_at DESC);
