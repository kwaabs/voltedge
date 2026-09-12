-- seed/double_busbar_diagram_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as every other seed file.
--
-- The DRAW half of "substation construction", the faithful/live flavour. Its
-- companion seed/double_busbar_network_example.sql builds the electrical MODEL
-- (real gis.* rows + runtime.feature_connectivity for a working double busbar);
-- this file DRAWS that exact model on a one-line by placing those SAME real
-- features onto a diagram. Nothing decorative: every glyph on the resulting
-- one-line is a live feature, so the Phase 48 amber "normally open" ring fires on
-- the real N/O selectors (SEL-1B, SEL-2A) and the canvas reads the actual
-- device/normal state -- unlike the purely schematic seed/double_busbar_template.sql,
-- which is furniture with no electrical meaning.
--
-- WHY A SUBSTATION HOST IS CREATED HERE. A diagram is never freestanding: a
-- runtime.diagram_instance must hang off a container feature whose class has
-- is_diagram_container = true (migration 0019; the API enforces it in
-- createInstance, diagram/sql.go). SUBSTATION is that container (flipped true by
-- seed/diagram_example.sql). The network seed deliberately made the equipment
-- freestanding (buses/coupler/selectors/feeders, no site), so there is no host
-- yet -- this seed adds one: DBB-SUB, a real gis.substation polygon over the same
-- map area (~-0.360, 5.760). Placement itself is NOT containment-gated (a one-line
-- "stays open to any placeable class", diagram/sql.go), so the substation serves
-- purely as the diagram's host; the placed features need not be contained by it.
--
-- WHAT IT WRITES (all idempotent, fixed uuids, ON CONFLICT DO NOTHING):
--   1. gis.substation DBB-SUB          -- the diagram container (host)
--   2. runtime.diagram_instances       -- one ONE_LINE diagram on DBB-SUB
--   3. runtime.diagram_placements x11  -- the eleven model features, laid out as a
--      legible double-busbar one-line in the diagram's own local x/y space:
--        * BUS-A / BUS-B      -> horizontal main bus lines (MV_BUSBAR draws as a
--          'line' on a diagram via diagram_geometry_type='line', migration 0053)
--        * CPLR-1             -> coupler breaker glyph bridging the two buses
--        * SEL-1A/1B, 2A/2B   -> selector isolator glyphs in two bay columns
--        * BAYBUS-1/2         -> short per-bay bus lines
--        * FEEDER-1/2         -> feeder conductor lines dropping out the bottom
--   Point classes (breaker/isolator) carry NULL path; line classes (busbar/
--   conductor) carry a jsonb [[x,y],...] polyline. Coordinates are abstract local
--   diagram units -- the canvas auto-fits its viewBox, so only proportions matter.
--   (Layout mirrors seed/double_busbar_template.sql's proportions and the target
--   render in docs/diagrams/double-busbar-one-line.svg.)
--
-- NOTE ON RAW runtime INSERTS. seed/diagram_example.sql's own header notes that
-- runtime.* instance tables are normally populated through the API, never a raw
-- INSERT. That convention was already relaxed by the network seeds, which raw-
-- INSERT runtime.feature_connectivity behind hard guards; this seed follows that
-- same precedent for runtime.diagram_instances/diagram_placements. The API's
-- write-time gates (is_diagram_container, is_diagram_placeable, the allow-list)
-- are re-asserted here as explicit guards so a raw apply can't smuggle in a state
-- the API would have rejected -- except the placeable/allow-list checks, which are
-- moot: all four placed classes are is_diagram_placeable and the SUBSTATION x
-- ONE_LINE pairing carries no allow-list rows (unconstrained).
--
-- HARD PREREQUISITES (each fails LOUDLY below, naming the missing piece):
--   seed/double_busbar_network_example.sql  -> the eleven features to place (BUS-A ...
--                                              FEEDER-2) MUST already exist
--   seed/substation_yard_example.sql        -> SUBSTATION class (+ task compile
--                                              CODE=SUBSTATION for gis.substation)
--   seed/diagram_example.sql                -> DIAGRAM_TYPE/ONE_LINE domain value
--                                              AND SUBSTATION.is_diagram_container=true
--
-- Apply with psql (NOT the migration runner), after the prerequisites above:
--   task seed-double-busbar-diagram
-- then open DBB-SUB's one-line in the app: the double busbar draws with the amber
-- NOP ring on SEL-1B and SEL-2A (requires seed/switch_normal_state_fields.sql
-- applied + MV_ISOLATOR compiled, which the network seed already needed).
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + opening the one-line is owed to
-- call it verified.

DO $$
DECLARE
    -- container class + placed-feature classes
    fc_substation uuid;
    fc_mv_busbar  uuid;
    fc_mv_breaker uuid;
    fc_mv_iso     uuid;
    fc_mv_cond    uuid;

    st_sub_dist   integer;  -- SUBSTATION 'distribution_substation' subtype code
    dtv_one_line  uuid;     -- domain_values id for DIAGRAM_TYPE / ONE_LINE
    is_container  boolean;

    -- fixed ids so the whole draw is idempotent and cross-referable
    sub_id  constant uuid := 'dbb50000-0000-0000-0000-0000000000a0';
    inst_id constant uuid := 'dbb50000-0000-0000-0000-0000000000d0';
BEGIN
    -- =====================================================================
    -- 1. Resolve the container class and confirm it is a compiled diagram
    --    container. gis.substation must exist (compiled) because we insert a
    --    real host feature row; is_diagram_container must be true or the API
    --    would reject a diagram on it (so we refuse to seed one either).
    -- =====================================================================
    SELECT id, is_diagram_container INTO fc_substation, is_container
      FROM metadata.feature_classes WHERE code = 'SUBSTATION';
    IF fc_substation IS NULL THEN RAISE EXCEPTION 'missing feature_class SUBSTATION -- apply seed/substation_yard_example.sql then: task compile CODE=SUBSTATION'; END IF;
    IF to_regclass('gis.substation') IS NULL THEN RAISE EXCEPTION 'table gis.substation does not exist -- run: task compile CODE=SUBSTATION'; END IF;
    IF is_container IS NOT TRUE THEN RAISE EXCEPTION 'SUBSTATION is not a diagram container (is_diagram_container=false) -- apply seed/diagram_example.sql'; END IF;

    SELECT code INTO st_sub_dist FROM metadata.subtypes
     WHERE feature_class_id = fc_substation AND name = 'distribution_substation';
    IF st_sub_dist IS NULL THEN RAISE EXCEPTION 'SUBSTATION subtype ''distribution_substation'' not found -- re-apply seed/substation_yard_example.sql'; END IF;

    -- =====================================================================
    -- 2. Resolve the ONE_LINE diagram type (a DIAGRAM_TYPE domain value).
    -- =====================================================================
    SELECT dv.id INTO dtv_one_line
      FROM metadata.domain_values dv
      JOIN metadata.domains d ON d.id = dv.domain_id
     WHERE d.code = 'DIAGRAM_TYPE' AND dv.code = 'ONE_LINE';
    IF dtv_one_line IS NULL THEN RAISE EXCEPTION 'DIAGRAM_TYPE/ONE_LINE domain value not found -- apply seed/diagram_example.sql'; END IF;

    -- =====================================================================
    -- 3. Resolve the placed-feature classes (need their ids for the
    --    diagram_placements rows).
    -- =====================================================================
    SELECT id INTO fc_mv_busbar  FROM metadata.feature_classes WHERE code = 'MV_BUSBAR';
    SELECT id INTO fc_mv_breaker FROM metadata.feature_classes WHERE code = 'MV_BREAKER';
    SELECT id INTO fc_mv_iso     FROM metadata.feature_classes WHERE code = 'MV_ISOLATOR';
    SELECT id INTO fc_mv_cond    FROM metadata.feature_classes WHERE code = 'MV_CONDUCTOR';
    IF fc_mv_busbar IS NULL OR fc_mv_breaker IS NULL OR fc_mv_iso IS NULL OR fc_mv_cond IS NULL THEN
        RAISE EXCEPTION 'one of MV_BUSBAR/MV_BREAKER/MV_ISOLATOR/MV_CONDUCTOR is missing -- apply the double-busbar network prerequisites (see seed/double_busbar_network_example.sql)'; END IF;

    -- =====================================================================
    -- 4. The eleven model features MUST already exist -- this seed only DRAWS
    --    them, it does not create them. A placement to a non-existent feature
    --    would silently vanish from the canvas (livePlacementKeys filters it),
    --    so fail loudly instead. Spot-check both ends of the model.
    -- =====================================================================
    IF NOT EXISTS (SELECT 1 FROM gis.mv_busbar   WHERE id = 'dbb50000-0000-0000-0000-000000000001') THEN
        RAISE EXCEPTION 'BUS-A (double-busbar model) not found -- apply seed/double_busbar_network_example.sql (task seed-double-busbar-network) first'; END IF;
    IF NOT EXISTS (SELECT 1 FROM gis.mv_conductor WHERE id = 'dbb50000-0000-0000-0000-000000000024') THEN
        RAISE EXCEPTION 'FEEDER-2 (double-busbar model) not found -- apply seed/double_busbar_network_example.sql (task seed-double-busbar-network) first'; END IF;

    -- =====================================================================
    -- 5. Create the SUBSTATION host (DBB-SUB) over the model's map area. It is
    --    only a diagram host, so we set just the columns present on the base
    --    compiled gis.substation (id/name/subtype/status/geom + the two
    --    original required fields enclosure_type/condition_rating). We
    --    DELIBERATELY do NOT set primary_voltage/secondary_voltage: those are
    --    the later-added SUBSTATION identity fields, which may not be compiled
    --    onto gis.substation on every DB -- and this draw has no need of them.
    --    Leaving them out keeps the draw decoupled from the SUBSTATION
    --    voltage-field recompile entirely. Polygon geom is required (SUBSTATION
    --    is geometry_type 'polygon'); published so it is live on the map.
    -- =====================================================================
    INSERT INTO gis.substation
        (id, name, subtype, status, geom, enclosure_type, condition_rating)
    VALUES
        (sub_id, 'DBB-SUB', st_sub_dist, 'published',
         ST_SetSRID(ST_GeomFromText('POLYGON((-0.3615 5.7608, -0.3525 5.7608, -0.3525 5.7572, -0.3615 5.7572, -0.3615 5.7608))'), 4326),
         'FENCED', 'GOOD')
    ON CONFLICT (id) DO NOTHING;

    -- =====================================================================
    -- 6. Create the ONE_LINE diagram on DBB-SUB. Fixed id so placements can
    --    reference it and re-runs never insert a second diagram.
    -- =====================================================================
    INSERT INTO runtime.diagram_instances
        (id, container_feature_class_id, container_feature_id, diagram_type_value_id, name)
    VALUES
        (inst_id, fc_substation, sub_id, dtv_one_line, 'DBB-SUB double busbar one-line')
    ON CONFLICT (id) DO NOTHING;

    -- =====================================================================
    -- 7. Place the eleven model features. Local diagram x/y (y increases down):
    --    two stacked main buses, two bay columns of selectors, a coupler
    --    bridging the buses at the right, feeders dropping out the bottom.
    --    Line classes (busbar/conductor) carry a path polyline; point classes
    --    (breaker/isolator) carry NULL path. Upsert on the one-per-diagram
    --    unique key so a re-run is a no-op, never a duplicate.
    -- =====================================================================
    INSERT INTO runtime.diagram_placements
        (diagram_instance_id, feature_class_id, feature_id, x, y, rotation, path)
    VALUES
        -- main buses (horizontal lines)
        (inst_id, fc_mv_busbar,  'dbb50000-0000-0000-0000-000000000001', 100, 120, 0, '[[100,120],[720,120]]'::jsonb),  -- BUS-A
        (inst_id, fc_mv_busbar,  'dbb50000-0000-0000-0000-000000000002', 100, 220, 0, '[[100,220],[720,220]]'::jsonb),  -- BUS-B

        -- bus coupler (glyph bridging Bus A and Bus B on the right)
        (inst_id, fc_mv_breaker, 'dbb50000-0000-0000-0000-000000000003', 650, 170, 0, NULL),                            -- CPLR-1

        -- bay 1 column (x=250): selector onto Bus A (closed), onto Bus B (open, NOP ring)
        (inst_id, fc_mv_iso,     'dbb50000-0000-0000-0000-000000000011', 250, 165, 0, NULL),                            -- SEL-1A
        (inst_id, fc_mv_iso,     'dbb50000-0000-0000-0000-000000000012', 250, 255, 0, NULL),                            -- SEL-1B (N/O)
        (inst_id, fc_mv_busbar,  'dbb50000-0000-0000-0000-000000000013', 190, 310, 0, '[[190,310],[310,310]]'::jsonb),  -- BAYBUS-1
        (inst_id, fc_mv_cond,    'dbb50000-0000-0000-0000-000000000014', 250, 310, 0, '[[250,310],[250,400]]'::jsonb),  -- FEEDER-1

        -- bay 2 column (x=450): selector onto Bus A (open, NOP ring), onto Bus B (closed)
        (inst_id, fc_mv_iso,     'dbb50000-0000-0000-0000-000000000021', 450, 165, 0, NULL),                            -- SEL-2A (N/O)
        (inst_id, fc_mv_iso,     'dbb50000-0000-0000-0000-000000000022', 450, 255, 0, NULL),                            -- SEL-2B
        (inst_id, fc_mv_busbar,  'dbb50000-0000-0000-0000-000000000023', 390, 310, 0, '[[390,310],[510,310]]'::jsonb),  -- BAYBUS-2
        (inst_id, fc_mv_cond,    'dbb50000-0000-0000-0000-000000000024', 450, 310, 0, '[[450,310],[450,400]]'::jsonb)   -- FEEDER-2
    ON CONFLICT (diagram_instance_id, feature_class_id, feature_id) DO NOTHING;

    RAISE NOTICE 'double_busbar_diagram_example: hosted a ONE_LINE on SUBSTATION DBB-SUB and placed the 11 model features (BUS-A/BUS-B, CPLR-1, SEL-1A/1B/2A/2B, BAYBUS-1/2, FEEDER-1/2). Open DBB-SUB''s one-line: the amber NOP ring fires on SEL-1B and SEL-2A.';
END $$;
