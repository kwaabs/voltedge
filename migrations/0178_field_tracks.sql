-- 0178_field_tracks.sql
-- Purpose: a FIELD BREADCRUMB AUDIT TRAIL. The field app records where a worker
-- physically was — a continuous movement trail plus a located stamp on every
-- mutating edit (create / update / delete / connect / relate / WAM / fault) — so a
-- supervisor can map how the field work was actually done: were they at the site,
-- how did they get there, where did they pass. Two tables:
--
--   ops.field_track_points      — the movement trail (breadcrumbs), one row per GPS
--                                 fix the device drops while a field session is
--                                 active. High-volume, append-only, loss-tolerant
--                                 (uploaded on sync through a light channel).
--   ops.field_action_waypoints  — where the worker was at the moment of each
--                                 mutating sync op. One row per op (op_id PK), rides
--                                 the DURABLE sync inbox so it is captured offline and
--                                 never lost; the office plots these as the pins on
--                                 the trail, each linking to the feature it changed.
--
-- `field:tracks:read` gates the supervisor view only (seeing others' trails is
-- privacy-sensitive); granted to admin + break-glass-admin out of the box. Pushing
-- one's own trail/waypoints needs no new permission — it rides the field user's
-- existing features:write (the same gate as POST /sync).

INSERT INTO iam.permissions (key, description) VALUES
    ('field:tracks:read', 'View field workers'' movement trails and located edit actions (the supervisor audit map).')
ON CONFLICT (key) DO NOTHING;

INSERT INTO iam.role_permissions (role_id, permission_key)
SELECT r.id, p.key
FROM iam.roles r
CROSS JOIN iam.permissions p
WHERE r.key IN ('admin', 'break-glass-admin')
  AND p.key = 'field:tracks:read'
ON CONFLICT DO NOTHING;

-- The movement trail. id is DEVICE-MINTED (a client uuid per point) so a re-upload
-- is idempotent (ON CONFLICT (id) DO NOTHING). recorded_at is the device clock when
-- the fix was taken; ingested_at is server receipt.
CREATE TABLE IF NOT EXISTS ops.field_track_points (
    id          uuid PRIMARY KEY,                 -- device-minted (idempotent upload)
    user_id     uuid NOT NULL,                    -- the field worker (iam.users, bare ref)
    session_id  uuid NOT NULL,                    -- device-minted field session
    device_id   text,
    recorded_at timestamptz NOT NULL,             -- when the fix was taken (device clock)
    geom        geometry(Point, 4326) NOT NULL,
    accuracy_m  double precision,                 -- horizontal accuracy, metres
    speed_mps   double precision,                 -- instantaneous speed, m/s (null if unknown)
    source      text,                             -- 'gps' / 'rtk' / 'network' …
    ingested_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE ops.field_track_points IS 'Field worker movement trail (0178): one breadcrumb per GPS fix dropped during a field session. Append-only, loss-tolerant.';

CREATE INDEX IF NOT EXISTS field_track_points_user_time_idx ON ops.field_track_points (user_id, recorded_at);
CREATE INDEX IF NOT EXISTS field_track_points_session_idx   ON ops.field_track_points (session_id, recorded_at);
CREATE INDEX IF NOT EXISTS field_track_points_geom_idx      ON ops.field_track_points USING GIST (geom);

-- Where the worker was at each mutating edit. op_id is the sync op idempotency key
-- and the PK, so a re-applied op re-records the same waypoint harmlessly.
CREATE TABLE IF NOT EXISTS ops.field_action_waypoints (
    op_id         text PRIMARY KEY,               -- the sync op id (idempotent)
    user_id       uuid NOT NULL,                  -- the acting field worker
    session_id    uuid,                           -- the field session the edit belongs to
    device_id     text,
    kind          text NOT NULL,                  -- create / update_attrs / delete / establish_* / wam_* / fault_*
    feature_class text,                           -- the edited feature's class (null for graph ops)
    feature_id    text,                           -- the edited feature's id
    recorded_at   timestamptz NOT NULL,           -- when the edit was made on device
    geom          geometry(Point, 4326) NOT NULL,
    accuracy_m    double precision,
    ingested_at   timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE ops.field_action_waypoints IS 'Located field edits (0178): where the worker was when each mutating sync op was made. Rides the durable sync inbox; one row per op.';

CREATE INDEX IF NOT EXISTS field_action_waypoints_user_time_idx ON ops.field_action_waypoints (user_id, recorded_at);
CREATE INDEX IF NOT EXISTS field_action_waypoints_session_idx   ON ops.field_action_waypoints (session_id);
CREATE INDEX IF NOT EXISTS field_action_waypoints_feature_idx   ON ops.field_action_waypoints (feature_class, feature_id);
CREATE INDEX IF NOT EXISTS field_action_waypoints_geom_idx      ON ops.field_action_waypoints USING GIST (geom);
