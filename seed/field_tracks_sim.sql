-- field_tracks_sim.sql
-- Field-trail demo seed (supervisor view blocker): populate the breadcrumb audit
-- trail (migration 0178) so /field-tracks has real data to show WITHOUT needing the
-- Flutter field app + live GPS. It writes, for a couple of field users:
--   ops.field_track_points      -- a continuous movement trail: an ORGANIC wandering
--                                  walk (random walk with momentum), one GPS fix every
--                                  ~45s with realistic accuracy/speed -- NOT a ruler line.
--   ops.field_action_waypoints  -- an "edit made here" pin at a few points along the
--                                  walk, each linked to a REAL feature so the pin opens it.
--
-- The walk is anchored near the ACTUAL network (first real point feature, else the map's
-- default centre) so it overlays the data, but the path itself is synthesised so it reads
-- like a person walking streets rather than a straight segment between stacked features
-- (co-located pole/substation features would otherwise collapse every pin onto one spot).
--
-- Everything is tagged device_id LIKE 'SIM-FIELD-%' so the whole lot removes cleanly:
--     DELETE FROM ops.field_track_points     WHERE device_id LIKE 'SIM-FIELD-%';
--     DELETE FROM ops.field_action_waypoints WHERE device_id LIKE 'SIM-FIELD-%';
--   (or: task clean-field-tracks-sim)
--
-- NOTE: re-running ADDS another set of sessions (fresh session ids each run); run
-- `task clean-field-tracks-sim` first if you want a clean reset.

-- One session's worth of trail: a worker wandering out from a start point, dropping a
-- GPS fix every 45s and stamping an edit pin at a few spread-out stops along the way.
-- Lives in pg_temp so it's auto-dropped at the end of the psql session.
CREATE FUNCTION pg_temp.sim_field_session(
    p_user     uuid,
    p_session  uuid,
    p_device   text,
    p_start    timestamptz,
    p_base_lng float8,
    p_base_lat float8,
    p_heading  float8,        -- initial walk heading, radians
    p_classes  text[],        -- real features to link the edit pins to (may hold NULLs)
    p_ids      text[]
) RETURNS void LANGUAGE plpgsql AS $fn$
DECLARE
    steps   int := 24;                 -- breadcrumbs in the session
    nfeat   int := COALESCE(array_length(p_classes, 1), 0);
    kinds   text[] := ARRAY['update_attrs', 'establish_connection', 'fault_report', 'wam_status', 'create'];
    pin_ix  int[] := '{}';             -- which breadcrumb indices carry an edit pin
    k       int;
    i       int;
    t       timestamptz := p_start;
    lng     float8 := p_base_lng;
    lat     float8 := p_base_lat;
    heading float8 := p_heading;
    step_m  float8;
    mlat    float8 := 111320.0;        -- metres per degree latitude
    mlng    float8;                    -- metres per degree longitude at this lat
BEGIN
    -- Schedule the edit pins at spread-out breadcrumb indices (never bunched).
    FOR k IN 1 .. nfeat LOOP
        pin_ix := pin_ix || GREATEST(0, LEAST(steps - 1, round(steps::float8 * k / (nfeat + 1))::int));
    END LOOP;

    FOR i IN 0 .. steps - 1 LOOP
        -- Drop the fix at the current position.
        INSERT INTO ops.field_track_points
            (id, user_id, session_id, device_id, recorded_at, geom, accuracy_m, speed_mps, source)
        VALUES (gen_random_uuid(), p_user, p_session, p_device, t,
                ST_SetSRID(ST_MakePoint(lng, lat), 4326),
                3.0 + random() * 6.0, 0.6 + random() * 1.4, 'gps');

        -- An edit pin if this breadcrumb is a scheduled stop.
        FOR k IN 1 .. nfeat LOOP
            IF pin_ix[k] = i AND p_classes[k] IS NOT NULL THEN
                INSERT INTO ops.field_action_waypoints
                    (op_id, user_id, session_id, device_id, kind, feature_class, feature_id, recorded_at, geom, accuracy_m)
                VALUES (p_session::text || ':wp:' || k::text, p_user, p_session, p_device,
                        kinds[1 + ((k - 1) % array_length(kinds, 1))],
                        p_classes[k], p_ids[k], t,
                        ST_SetSRID(ST_MakePoint(lng, lat), 4326), 3.0 + random() * 4.0)
                ON CONFLICT (op_id) DO NOTHING;
            END IF;
        END LOOP;

        -- Advance: a walking step with a gently drifting heading (organic, not straight).
        -- Occasionally a sharper "street corner" turn; sometimes barely move (lingering).
        heading := heading + (random() - 0.5) * 0.9;
        IF random() < 0.15 THEN
            heading := heading + (random() - 0.5) * 1.8;
        END IF;
        step_m := CASE WHEN random() < 0.2 THEN 1.0 + random() * 4.0   -- pausing on site
                       ELSE 10.0 + random() * 32.0 END;                 -- walking
        mlng := mlat * cos(radians(lat));
        lng  := lng + (step_m * cos(heading)) / mlng;
        lat  := lat + (step_m * sin(heading)) / mlat;
        t    := t + interval '45 seconds';
    END LOOP;
END
$fn$;

DO $$
DECLARE
    c          RECORD;
    rec        RECORD;
    v_classes  text[] := '{}';
    v_ids      text[] := '{}';
    base_lng   float8;
    base_lat   float8;
    users      uuid[];
    u1         uuid;
    u2         uuid;
    want       int := 6;
BEGIN
    -- Anchor coordinate + a handful of REAL features to link the edit pins to.
    FOR c IN
        SELECT fc.code, fc.physical_table
        FROM metadata.feature_classes fc
        WHERE fc.status = 'active'
          AND fc.physical_table IS NOT NULL
          AND fc.geometry_type = 'point'
        ORDER BY fc.code
    LOOP
        FOR rec IN EXECUTE format(
            'SELECT id::text AS id, ST_X(ST_Centroid(geom)) AS lng, ST_Y(ST_Centroid(geom)) AS lat
             FROM %s WHERE deleted_at IS NULL AND geom IS NOT NULL LIMIT 2', c.physical_table)
        LOOP
            IF base_lng IS NULL THEN
                base_lng := rec.lng;
                base_lat := rec.lat;
            END IF;
            v_classes := v_classes || c.code;
            v_ids     := v_ids     || rec.id;
            EXIT WHEN array_length(v_ids, 1) >= want;
        END LOOP;
        EXIT WHEN array_length(v_ids, 1) >= want;
    END LOOP;

    -- Fallback anchor: the map's default centre (then a global default), when the DB
    -- has no point features yet. Pins just carry no feature link in that case.
    IF base_lng IS NULL THEN
        SELECT value::float8 INTO base_lng FROM iam.system_settings WHERE key = 'map_default_center_lng';
        SELECT value::float8 INTO base_lat FROM iam.system_settings WHERE key = 'map_default_center_lat';
        base_lng := COALESCE(base_lng, -0.1870);   -- Accra-ish fallback
        base_lat := COALESCE(base_lat, 5.6037);
        v_classes := ARRAY[NULL, NULL, NULL]::text[];
        v_ids     := ARRAY[NULL, NULL, NULL]::text[];
    END IF;

    -- Two field workers (the earliest-created active users) so the picker shows real names.
    SELECT array_agg(id) INTO users
    FROM (SELECT id FROM iam.users WHERE status = 'active' ORDER BY created_at LIMIT 2) s;

    IF users IS NULL OR array_length(users, 1) IS NULL THEN
        RAISE NOTICE 'field_tracks_sim: no active users -- nothing seeded.';
        RETURN;
    END IF;
    u1 := users[1];
    u2 := COALESCE(users[2], users[1]);

    -- Three distinct walks (different start nudge + heading) so no two look cloned.
    PERFORM pg_temp.sim_field_session(
        u1, gen_random_uuid(), 'SIM-FIELD-01', now() - interval '2 days 3 hours',
        base_lng + 0.0004, base_lat - 0.0003, 0.6,
        v_classes[1:3], v_ids[1:3]);

    PERFORM pg_temp.sim_field_session(
        u1, gen_random_uuid(), 'SIM-FIELD-01', now() - interval '1 day 2 hours',
        base_lng - 0.0005, base_lat + 0.0004, 2.4,
        v_classes[3:6], v_ids[3:6]);

    PERFORM pg_temp.sim_field_session(
        u2, gen_random_uuid(), 'SIM-FIELD-02', now() - interval '4 hours',
        base_lng + 0.0002, base_lat + 0.0006, 4.1,
        v_classes[1:4], v_ids[1:4]);

    RAISE NOTICE 'field_tracks_sim: seeded 3 walks for % user(s), anchored near %, %.',
        array_length(users, 1), round(base_lng::numeric, 5), round(base_lat::numeric, 5);
END $$;

DROP FUNCTION IF EXISTS pg_temp.sim_field_session(uuid, uuid, text, timestamptz, float8, float8, float8, text[], text[]);
