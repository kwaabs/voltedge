-- seed/migrate_dist_switch_to_mv_switch.sql
-- ============================================================================
-- DESTRUCTIVE, ONE-WAY MIGRATION. Splits every live DIST_SWITCH feature into its
-- successor class BY SUBTYPE -- manual_switch rows -> MV_SWITCH, automatic_recloser
-- rows -> MV_RECLOSER -- keeping each feature's id, geometry, attributes,
-- connectivity, relationships and history, then DROPS the DIST_SWITCH class and
-- its physical table gis.dist_switch. There is no automatic rollback once
-- COMMITted.
--
--   *** TAKE A DATABASE BACKUP AND DRY-RUN THIS ON A COPY FIRST. ***
--
-- (Filename kept for continuity; this now targets MV_SWITCH *and* MV_RECLOSER.)
--
-- This SQL was authored statically (shell here can't spawn -- bash.exe EPERM) and
-- has NOT been executed against a live database. It is wrapped in a single
-- transaction with hard guards that RAISE (aborting + rolling back the whole
-- thing) on any precondition or post-condition it doesn't like, so a mistake
-- tends to abort cleanly rather than half-migrate -- but that is a safety net,
-- not a substitute for running it against a backup first. Run with:
--   psql -v ON_ERROR_STOP=1 -f seed/migrate_dist_switch_to_mv_switch.sql
--
-- PREREQUISITES (all must be true or a guard aborts):
--   1. seed/mv_lv_switch_example.sql applied + `task compile CODE=MV_SWITCH`
--      (so the MV_SWITCH class, its T1/T2 terminals, its network_rules and the
--      physical gis.mv_switch table all exist).
--   2. seed/mv_lv_switch_parity.sql applied + `task compile CODE=MV_SWITCH`
--      AGAIN (so gis.mv_switch has the circuit_id + operational_state columns,
--      and the PROTECTIVE_RELAY-trips-MV_SWITCH relationship_class exists for the
--      relationship remap below). Without parity, membership + lifecycle values
--      on migrated rows are silently dropped -- the guard checks for the columns.
--   3. IF any live DIST_SWITCH feature uses the automatic_recloser subtype:
--      seed/mv_recloser_example.sql applied + `task compile CODE=MV_RECLOSER`
--      (so the MV_RECLOSER class, its terminals/network_rules/parity columns and
--      the physical gis.mv_recloser table exist). If there are zero recloser
--      features these are not required -- the guard only enforces them when at
--      least one recloser row would need a home.
--
-- WHY id-preserving works: runtime.feature_connectivity / feature_relationships /
-- gis.feature_history all key features by a bare feature uuid with NO foreign
-- key (polymorphic). So a migrated row keeps its id, and those tables stay valid
-- once we repoint only their *class* references (the uuid feature_class_id
-- columns, or the text feature_class_code). We never rewrite a feature_id.
--
-- WHY per-feature routing: because DIST_SWITCH splits into two classes, a class
-- reference alone is ambiguous -- an edge or relationship pointing at a
-- DIST_SWITCH feature must follow THAT feature to its correct successor. We
-- capture every feature's subtype into a temp map (ds_feature_target) BEFORE the
-- table is dropped, then every repoint joins through it to pick MV_SWITCH vs
-- MV_RECLOSER for that specific feature id.
-- ============================================================================

BEGIN;

-- ---------------------------------------------------------------------------
-- GUARDS, subtype->target map, and row copy. plpgsql so we can assert
-- preconditions, build the per-target INSERT column lists dynamically from the
-- columns actually present on BOTH tables, and force the subtype column to each
-- successor's own 'standard' subtype code.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    ds_id          uuid;
    mv_id          uuid;
    rec_id         uuid;
    manual_code    integer;
    recloser_code  integer;
    mv_std         integer;
    rec_std        integer;
    n_recloser     integer := 0;
    cols           text;
    sel            text;
    n_copied       integer;
BEGIN
    SELECT id INTO ds_id  FROM metadata.feature_classes WHERE code = 'DIST_SWITCH';
    SELECT id INTO mv_id  FROM metadata.feature_classes WHERE code = 'MV_SWITCH';
    SELECT id INTO rec_id FROM metadata.feature_classes WHERE code = 'MV_RECLOSER';

    IF ds_id IS NULL THEN
        RAISE EXCEPTION 'DIST_SWITCH class not found -- nothing to migrate (already dropped?).';
    END IF;
    IF mv_id IS NULL THEN
        RAISE EXCEPTION 'MV_SWITCH class not found -- apply seed/mv_lv_switch_example.sql + compile first.';
    END IF;

    -- Physical MV_SWITCH target must exist and already carry the parity columns.
    IF NOT EXISTS (SELECT 1 FROM information_schema.tables
                   WHERE table_schema = 'gis' AND table_name = 'mv_switch') THEN
        RAISE EXCEPTION 'gis.mv_switch table missing -- run `task compile CODE=MV_SWITCH` first.';
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema = 'gis' AND table_name = 'dist_switch' AND column_name = 'circuit_id')
       AND NOT EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema = 'gis' AND table_name = 'mv_switch' AND column_name = 'circuit_id') THEN
        RAISE EXCEPTION 'gis.dist_switch has circuit_id but gis.mv_switch does not -- apply parity seed + recompile MV_SWITCH first (membership would be lost).';
    END IF;
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema = 'gis' AND table_name = 'dist_switch' AND column_name = 'operational_state')
       AND NOT EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_schema = 'gis' AND table_name = 'mv_switch' AND column_name = 'operational_state') THEN
        RAISE EXCEPTION 'gis.dist_switch has operational_state but gis.mv_switch does not -- apply parity seed + recompile MV_SWITCH first (lifecycle would be lost).';
    END IF;

    SELECT code INTO manual_code   FROM metadata.subtypes WHERE feature_class_id = ds_id  AND name = 'manual_switch';
    SELECT code INTO recloser_code FROM metadata.subtypes WHERE feature_class_id = ds_id  AND name = 'automatic_recloser';
    SELECT code INTO mv_std        FROM metadata.subtypes WHERE feature_class_id = mv_id  AND name = 'standard';

    -- How many features live on the recloser subtype? Only if >0 do we require
    -- the MV_RECLOSER class + table + parity to exist.
    IF recloser_code IS NOT NULL THEN
        EXECUTE format('SELECT count(*) FROM gis.dist_switch WHERE subtype = %s', recloser_code)
        INTO n_recloser;
    END IF;

    IF n_recloser > 0 THEN
        IF rec_id IS NULL THEN
            RAISE EXCEPTION 'Found % recloser feature(s) but MV_RECLOSER class not found -- apply seed/mv_recloser_example.sql + `task compile CODE=MV_RECLOSER` first.', n_recloser;
        END IF;
        IF NOT EXISTS (SELECT 1 FROM information_schema.tables
                       WHERE table_schema = 'gis' AND table_name = 'mv_recloser') THEN
            RAISE EXCEPTION 'gis.mv_recloser table missing -- run `task compile CODE=MV_RECLOSER` first.';
        END IF;
        IF EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema = 'gis' AND table_name = 'dist_switch' AND column_name = 'circuit_id')
           AND NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema = 'gis' AND table_name = 'mv_recloser' AND column_name = 'circuit_id') THEN
            RAISE EXCEPTION 'gis.mv_recloser lacks circuit_id -- apply seed/mv_recloser_example.sql + recompile MV_RECLOSER first (membership would be lost).';
        END IF;
        IF EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema = 'gis' AND table_name = 'dist_switch' AND column_name = 'operational_state')
           AND NOT EXISTS (SELECT 1 FROM information_schema.columns
                   WHERE table_schema = 'gis' AND table_name = 'mv_recloser' AND column_name = 'operational_state') THEN
            RAISE EXCEPTION 'gis.mv_recloser lacks operational_state -- apply seed/mv_recloser_example.sql + recompile MV_RECLOSER first (lifecycle would be lost).';
        END IF;
        SELECT code INTO rec_std FROM metadata.subtypes WHERE feature_class_id = rec_id AND name = 'standard';
        IF rec_std IS NULL THEN
            RAISE EXCEPTION 'MV_RECLOSER has no ''standard'' subtype -- reapply seed/mv_recloser_example.sql.';
        END IF;
    END IF;

    -- Per-feature subtype -> successor code map, captured before the drop. A
    -- recloser-coded row maps to MV_RECLOSER; everything else (manual_switch, or
    -- any stray subtype) maps to the generic MV_SWITCH so no feature is orphaned.
    EXECUTE format(
        'CREATE TEMP TABLE ds_feature_target ON COMMIT DROP AS
           SELECT id AS feature_id, subtype,
                  CASE WHEN %L IS NOT NULL AND subtype = %L THEN %L ELSE %L END AS target_code
           FROM gis.dist_switch',
        recloser_code, recloser_code, 'MV_RECLOSER', 'MV_SWITCH');

    -- --- Copy the MV_SWITCH-bound rows (subtype <> recloser). Column list is the
    -- intersection of gis.dist_switch and gis.mv_switch; the subtype column is
    -- forced to MV_SWITCH's standard code. MV_SWITCH's is_fused/fuse_rating_a/
    -- curve_ref are absent from dist_switch so they default NULL.
    SELECT string_agg(quote_ident(c.column_name), ', ' ORDER BY c.ordinal_position),
           string_agg(CASE WHEN c.column_name = 'subtype' THEN mv_std::text
                           ELSE quote_ident(c.column_name) END, ', ' ORDER BY c.ordinal_position)
    INTO cols, sel
    FROM information_schema.columns c
    WHERE c.table_schema = 'gis' AND c.table_name = 'dist_switch'
      AND EXISTS (SELECT 1 FROM information_schema.columns m
                  WHERE m.table_schema = 'gis' AND m.table_name = 'mv_switch'
                    AND m.column_name = c.column_name);

    EXECUTE format(
        'INSERT INTO gis.mv_switch (%s) SELECT %s FROM gis.dist_switch WHERE subtype IS DISTINCT FROM %L',
        cols, sel, recloser_code);
    GET DIAGNOSTICS n_copied = ROW_COUNT;
    RAISE NOTICE 'Copied % row(s) into gis.mv_switch.', n_copied;

    -- --- Copy the MV_RECLOSER-bound rows (subtype = recloser), forcing the
    -- subtype to MV_RECLOSER's standard code. Only when reclosers exist.
    IF n_recloser > 0 THEN
        SELECT string_agg(quote_ident(c.column_name), ', ' ORDER BY c.ordinal_position),
               string_agg(CASE WHEN c.column_name = 'subtype' THEN rec_std::text
                               ELSE quote_ident(c.column_name) END, ', ' ORDER BY c.ordinal_position)
        INTO cols, sel
        FROM information_schema.columns c
        WHERE c.table_schema = 'gis' AND c.table_name = 'dist_switch'
          AND EXISTS (SELECT 1 FROM information_schema.columns m
                      WHERE m.table_schema = 'gis' AND m.table_name = 'mv_recloser'
                        AND m.column_name = c.column_name);

        EXECUTE format(
            'INSERT INTO gis.mv_recloser (%s) SELECT %s FROM gis.dist_switch WHERE subtype = %L',
            cols, sel, recloser_code);
        GET DIAGNOSTICS n_copied = ROW_COUNT;
        RAISE NOTICE 'Copied % row(s) into gis.mv_recloser.', n_copied;
    END IF;
END $$;

-- ---------------------------------------------------------------------------
-- DEFENSIVE DEDUPE of the successor classes' terminals. metadata.terminals has a
-- plain UNIQUE(feature_class_id, subtype_id, code); with subtype_id NULL (all
-- these) Postgres treats the NULLs as distinct, so `ON CONFLICT (..., subtype_id,
-- ...) DO NOTHING` in the seeds never fires and re-running a switch seed silently
-- DOUBLES its T1/T2 terminals. A duplicate makes the per-code terminal lookups
-- below ambiguous ("more than one row returned by a subquery"). Nothing but these
-- brand-new classes' own network_rules (and possibly snapping_rules) references
-- their terminals yet -- there is no connectivity on them until this migration --
-- so collapse each (feature_class_id, code) group to its lowest-id terminal,
-- repoint those metadata references, and delete the extras.
CREATE TEMP TABLE term_dedup ON COMMIT DROP AS
SELECT t.id AS dup_id, k.keep_id
FROM metadata.terminals t
JOIN (
    -- uuid has ordering operators but no min() aggregate, so pick the lowest id
    -- via an ordered array_agg rather than min(id).
    SELECT feature_class_id, code, (array_agg(id ORDER BY id))[1] AS keep_id
    FROM metadata.terminals
    WHERE feature_class_id IN (SELECT id FROM metadata.feature_classes WHERE code IN ('MV_SWITCH', 'MV_RECLOSER'))
    GROUP BY feature_class_id, code
) k ON k.feature_class_id = t.feature_class_id AND k.code = t.code
WHERE t.id <> k.keep_id;

UPDATE metadata.network_rules nr SET from_terminal_id = d.keep_id FROM term_dedup d WHERE nr.from_terminal_id = d.dup_id;
UPDATE metadata.network_rules nr SET to_terminal_id   = d.keep_id FROM term_dedup d WHERE nr.to_terminal_id   = d.dup_id;
UPDATE metadata.snapping_rules sr SET to_terminal_id  = d.keep_id FROM term_dedup d WHERE sr.to_terminal_id   = d.dup_id;
DELETE FROM metadata.terminals t USING term_dedup d WHERE t.id = d.dup_id;

-- ---------------------------------------------------------------------------
-- Repoint runtime.feature_connectivity's network_rule_id. Each DIST_SWITCH
-- network_rule is remapped to its structural twin ON THE SUCCESSOR CLASS THAT
-- THIS EDGE'S DIST_SWITCH FEATURE MAPS TO (same conductor side, same terminal
-- CODES, DIST_SWITCH class swapped for the per-feature target). Built into a
-- temp table first because it reads network_rule_id while it still points at
-- DIST_SWITCH rules; must run before those rules are deleted. DISTINCT ON (c.id)
-- keeps it one-row-per-edge even if the successor class still carries duplicate
-- (now structurally identical) network_rules from a re-run seed -- either twin
-- encodes the same from/to/terminal-code pattern, so picking the lowest id is
-- safe.
-- ---------------------------------------------------------------------------
CREATE TEMP TABLE conn_rule_remap ON COMMIT DROP AS
SELECT DISTINCT ON (c.id) c.id AS conn_id, rn.id AS new_rule_id
FROM runtime.feature_connectivity c
JOIN metadata.network_rules ro ON ro.id = c.network_rule_id
CROSS JOIN (SELECT id FROM metadata.feature_classes WHERE code = 'DIST_SWITCH') ds
-- the DIST_SWITCH-side feature of this edge (whichever end the rule marks as DS)
JOIN ds_feature_target m
     ON m.feature_id = CASE WHEN ro.from_feature_class_id = ds.id THEN c.from_feature_id
                            ELSE c.to_feature_id END
JOIN metadata.feature_classes tgt ON tgt.code = m.target_code
LEFT JOIN metadata.terminals rof ON rof.id = ro.from_terminal_id
LEFT JOIN metadata.terminals rot ON rot.id = ro.to_terminal_id
JOIN metadata.network_rules rn
     ON rn.from_feature_class_id = CASE WHEN ro.from_feature_class_id = ds.id THEN tgt.id ELSE ro.from_feature_class_id END
    AND rn.to_feature_class_id   = CASE WHEN ro.to_feature_class_id   = ds.id THEN tgt.id ELSE ro.to_feature_class_id END
LEFT JOIN metadata.terminals rnf ON rnf.id = rn.from_terminal_id
LEFT JOIN metadata.terminals rnt ON rnt.id = rn.to_terminal_id
WHERE (ro.from_feature_class_id = ds.id OR ro.to_feature_class_id = ds.id)
  AND rn.id <> ro.id
  AND rof.code IS NOT DISTINCT FROM rnf.code
  AND rot.code IS NOT DISTINCT FROM rnt.code
ORDER BY c.id, rn.id;

UPDATE runtime.feature_connectivity c
SET network_rule_id = r.new_rule_id
FROM conn_rule_remap r
WHERE c.id = r.conn_id;

-- Repoint the FROM endpoint per-feature: class id -> that feature's successor,
-- terminal id -> the same-coded terminal on the successor (correlated subquery
-- returns NULL when the edge had no terminal).
UPDATE runtime.feature_connectivity c
SET from_feature_class_id = tgt.id,
    from_terminal_id = (
        SELECT tt.id FROM metadata.terminals dst
        JOIN metadata.terminals tt ON tt.feature_class_id = tgt.id AND tt.code = dst.code
        WHERE dst.id = c.from_terminal_id
    )
FROM ds_feature_target m
JOIN metadata.feature_classes tgt ON tgt.code = m.target_code
JOIN metadata.feature_classes ds ON ds.code = 'DIST_SWITCH'
WHERE c.from_feature_class_id = ds.id AND c.from_feature_id = m.feature_id;

-- Repoint the TO endpoint, same shape.
UPDATE runtime.feature_connectivity c
SET to_feature_class_id = tgt.id,
    to_terminal_id = (
        SELECT tt.id FROM metadata.terminals dst
        JOIN metadata.terminals tt ON tt.feature_class_id = tgt.id AND tt.code = dst.code
        WHERE dst.id = c.to_terminal_id
    )
FROM ds_feature_target m
JOIN metadata.feature_classes tgt ON tgt.code = m.target_code
JOIN metadata.feature_classes ds ON ds.code = 'DIST_SWITCH'
WHERE c.to_feature_class_id = ds.id AND c.to_feature_id = m.feature_id;

-- ---------------------------------------------------------------------------
-- Ensure a successor TWIN relationship_class exists for every relationship_class
-- that references DIST_SWITCH, before repointing instances at it. The parity +
-- recloser seeds create these twins (PROTECTIVE_RELAY trips MV_SWITCH / MV_RECLOSER)
-- BUT only if PROTECTIVE_RELAY already existed when they ran -- a seed applied
-- before mv_relay_example.sql silently skips its twin, and then the repoint below
-- finds no class to point at (the "UPDATE 0 yet 1 leftover" abort). Re-assert them
-- here, order-independently: substitute DIST_SWITCH -> each successor class that
-- exists, keeping origin/dest(other side)/kind/cardinality; NOT EXISTS on
-- (origin, dest, kind) makes it a no-op when the twin is already present, and the
-- generated name matches the seeds' canonical 'PROTECTIVE_RELAY trips <succ>'
-- (relationship_classes.name is UNIQUE, so we only ever insert a name that does
-- not exist yet). Covers both directions though DIST_SWITCH is only ever a
-- destination today.
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT CASE WHEN rc.name LIKE '%DIST_SWITCH%' THEN replace(rc.name, 'DIST_SWITCH', succ.code)
            ELSE rc.name || ' (' || succ.code || ')' END,
       CASE WHEN rc.origin_feature_class_id      = ds.id THEN succ.id ELSE rc.origin_feature_class_id END,
       CASE WHEN rc.destination_feature_class_id = ds.id THEN succ.id ELSE rc.destination_feature_class_id END,
       rc.kind, rc.cardinality
FROM metadata.relationship_classes rc
JOIN metadata.feature_classes ds ON ds.code = 'DIST_SWITCH'
JOIN metadata.feature_classes succ ON succ.code IN ('MV_SWITCH', 'MV_RECLOSER')
WHERE (rc.origin_feature_class_id = ds.id OR rc.destination_feature_class_id = ds.id)
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes x
    WHERE x.kind = rc.kind
      AND x.origin_feature_class_id      = CASE WHEN rc.origin_feature_class_id      = ds.id THEN succ.id ELSE rc.origin_feature_class_id END
      AND x.destination_feature_class_id = CASE WHEN rc.destination_feature_class_id = ds.id THEN succ.id ELSE rc.destination_feature_class_id END
  );

-- ---------------------------------------------------------------------------
-- Repoint runtime.feature_relationships where DIST_SWITCH is the DESTINATION
-- (PROTECTIVE_RELAY trips DIST_SWITCH). Move each instance's destination class +
-- relationship_class to the structural twin (same origin + kind) on the
-- successor THAT DESTINATION FEATURE maps to -- MV_SWITCH or MV_RECLOSER.
-- ---------------------------------------------------------------------------
UPDATE runtime.feature_relationships r
SET destination_feature_class_id = tgt.id,
    relationship_class_id = tgtrc.id
FROM ds_feature_target m
JOIN metadata.feature_classes tgt ON tgt.code = m.target_code
JOIN metadata.feature_classes ds ON ds.code = 'DIST_SWITCH'
JOIN metadata.relationship_classes dsrc ON dsrc.destination_feature_class_id = ds.id
JOIN metadata.relationship_classes tgtrc
     ON tgtrc.origin_feature_class_id = dsrc.origin_feature_class_id
    AND tgtrc.destination_feature_class_id = tgt.id
    AND tgtrc.kind = dsrc.kind
WHERE r.destination_feature_class_id = ds.id
  AND r.relationship_class_id = dsrc.id
  AND r.destination_feature_id = m.feature_id;

-- Symmetric repoint for the ORIGIN side (none exist today, but future-proof and
-- keeps the post-condition guard satisfiable if one is ever added).
UPDATE runtime.feature_relationships r
SET origin_feature_class_id = tgt.id,
    relationship_class_id = tgtrc.id
FROM ds_feature_target m
JOIN metadata.feature_classes tgt ON tgt.code = m.target_code
JOIN metadata.feature_classes ds ON ds.code = 'DIST_SWITCH'
JOIN metadata.relationship_classes dsrc ON dsrc.origin_feature_class_id = ds.id
JOIN metadata.relationship_classes tgtrc
     ON tgtrc.destination_feature_class_id = dsrc.destination_feature_class_id
    AND tgtrc.origin_feature_class_id = tgt.id
    AND tgtrc.kind = dsrc.kind
WHERE r.origin_feature_class_id = ds.id
  AND r.relationship_class_id = dsrc.id
  AND r.origin_feature_id = m.feature_id;

-- ---------------------------------------------------------------------------
-- Repoint the OTHER per-feature tables that reference the class by id (each NO
-- ON DELETE, so each would block the class delete) or by code. Same id-preserving
-- logic as the graph tables: the feature keeps its id, only its class reference
-- moves to the successor it maps to.
--   * runtime.diagram_placements     -- a feature positioned on a schematic
--   * runtime.diagram_instances      -- a container's diagram (DIST_SWITCH is not
--     a container, so 0 rows expected -- repointed defensively)
--   * runtime.feature_external_links -- outbound links on a feature
--   * files.attachments              -- files on a feature (class stored as CODE)
-- ---------------------------------------------------------------------------
UPDATE runtime.diagram_placements p
SET feature_class_id = tgt.id
FROM ds_feature_target m
JOIN metadata.feature_classes tgt ON tgt.code = m.target_code
JOIN metadata.feature_classes ds ON ds.code = 'DIST_SWITCH'
WHERE p.feature_class_id = ds.id AND p.feature_id = m.feature_id;

UPDATE runtime.diagram_instances di
SET container_feature_class_id = tgt.id
FROM ds_feature_target m
JOIN metadata.feature_classes tgt ON tgt.code = m.target_code
JOIN metadata.feature_classes ds ON ds.code = 'DIST_SWITCH'
WHERE di.container_feature_class_id = ds.id AND di.container_feature_id = m.feature_id;

UPDATE runtime.feature_external_links el
SET feature_class_id = tgt.id
FROM ds_feature_target m
JOIN metadata.feature_classes tgt ON tgt.code = m.target_code
JOIN metadata.feature_classes ds ON ds.code = 'DIST_SWITCH'
WHERE el.feature_class_id = ds.id AND el.feature_id = m.feature_id;

UPDATE files.attachments a
SET feature_class = m.target_code
FROM ds_feature_target m
WHERE a.feature_class = 'DIST_SWITCH' AND a.feature_id = m.feature_id;
UPDATE files.attachments SET feature_class = 'MV_SWITCH' WHERE feature_class = 'DIST_SWITCH';

-- metadata.trace_configurations.target_class_id (migrations/0018): if any trace
-- profile stops at DIST_SWITCH as its target class, move it to the generic
-- MV_SWITCH (matches the section-trace defaultBoundaryClass flip). No ON DELETE,
-- so it would otherwise block the class delete. Config, not per-feature.
UPDATE metadata.trace_configurations
SET target_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'MV_SWITCH')
WHERE target_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'DIST_SWITCH');

-- ---------------------------------------------------------------------------
-- POST-CONDITION GUARD: nothing that FKs the class with NO ON DELETE may still
-- reference DIST_SWITCH. If anything does (an origin-side relationship class we
-- didn't anticipate, a network_rule/feature/placement/link/attachment with no
-- successor twin, ...), abort the whole transaction rather than hit the raw FK
-- error mid-delete.
-- ---------------------------------------------------------------------------
DO $$
DECLARE
    ds_id  uuid;
    n_conn integer;
    n_rel  integer;
    n_rule integer;
    n_diag integer;
    n_inst integer;
    n_link integer;
    n_att  integer;
    n_tc   integer;
BEGIN
    SELECT id INTO ds_id FROM metadata.feature_classes WHERE code = 'DIST_SWITCH';

    SELECT count(*) INTO n_conn FROM runtime.feature_connectivity
    WHERE from_feature_class_id = ds_id OR to_feature_class_id = ds_id;
    SELECT count(*) INTO n_rel FROM runtime.feature_relationships
    WHERE origin_feature_class_id = ds_id OR destination_feature_class_id = ds_id;
    SELECT count(*) INTO n_rule FROM runtime.feature_connectivity c
    JOIN metadata.network_rules nr ON nr.id = c.network_rule_id
    WHERE nr.from_feature_class_id = ds_id OR nr.to_feature_class_id = ds_id;
    SELECT count(*) INTO n_diag FROM runtime.diagram_placements WHERE feature_class_id = ds_id;
    SELECT count(*) INTO n_inst FROM runtime.diagram_instances WHERE container_feature_class_id = ds_id;
    SELECT count(*) INTO n_link FROM runtime.feature_external_links WHERE feature_class_id = ds_id;
    SELECT count(*) INTO n_att  FROM files.attachments WHERE feature_class = 'DIST_SWITCH';
    SELECT count(*) INTO n_tc   FROM metadata.trace_configurations WHERE target_class_id = ds_id;

    IF n_conn > 0 OR n_rel > 0 OR n_rule > 0 OR n_diag > 0 OR n_inst > 0 OR n_link > 0 OR n_att > 0 OR n_tc > 0 THEN
        RAISE EXCEPTION 'Leftover DIST_SWITCH references after repoint (connectivity=%, relationships=%, via-network-rule=%, diagram-placements=%, diagram-instances=%, external-links=%, attachments=%, trace-target=%). Aborting -- a reference would be orphaned.',
            n_conn, n_rel, n_rule, n_diag, n_inst, n_link, n_att, n_tc;
    END IF;
END $$;

-- ---------------------------------------------------------------------------
-- No-FK text references: history + circuits carry the class as a plain string.
-- Rename per-feature to the matching successor (migrated rows keep their
-- feature_id), then default any leftover 'DIST_SWITCH' rows whose feature is no
-- longer in the table (hard-deleted before this ran) to the generic MV_SWITCH.
-- ---------------------------------------------------------------------------
UPDATE gis.feature_history h
SET feature_class_code = m.target_code
FROM ds_feature_target m
WHERE h.feature_class_code = 'DIST_SWITCH' AND h.feature_id = m.feature_id;
UPDATE gis.feature_history SET feature_class_code = 'MV_SWITCH' WHERE feature_class_code = 'DIST_SWITCH';

UPDATE gis.circuits ci
SET source_feature_class = m.target_code
FROM ds_feature_target m
WHERE ci.source_feature_class = 'DIST_SWITCH' AND ci.source_feature_id = m.feature_id;
UPDATE gis.circuits SET source_feature_class = 'MV_SWITCH' WHERE source_feature_class = 'DIST_SWITCH';

-- ---------------------------------------------------------------------------
-- diagram_elements (migrations/0046): bound_class / slot_class hold the class as
-- a plain code with NO FK -- they won't block the delete, but rename for
-- continuity. A BOUND slot follows its bound feature to the right successor; an
-- unbound TEMPLATE slot that expected DIST_SWITCH now expects generic MV_SWITCH.
-- ---------------------------------------------------------------------------
UPDATE runtime.diagram_elements e
SET bound_class = m.target_code
FROM ds_feature_target m
WHERE e.bound_class = 'DIST_SWITCH' AND e.bound_id = m.feature_id;
UPDATE runtime.diagram_elements SET bound_class = 'MV_SWITCH' WHERE bound_class = 'DIST_SWITCH';
UPDATE runtime.diagram_elements SET slot_class  = 'MV_SWITCH' WHERE slot_class  = 'DIST_SWITCH';

-- ---------------------------------------------------------------------------
-- diagram_placement_rules (migrations/0068): a per-(container x diagram type)
-- allow-list keyed by feature_class_id with ON DELETE CASCADE -- the DIST_SWITCH
-- rows vanish when the class row is deleted below. Synthesize a successor twin
-- for each first so the allow-list survives the drop (idempotent on the unique
-- key). DIST_SWITCH is never a container, so only the placeable-side
-- feature_class_id can reference it.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.diagram_placement_rules (container_feature_class_id, diagram_type_value_id, feature_class_id)
SELECT r.container_feature_class_id, r.diagram_type_value_id, succ.id
FROM metadata.diagram_placement_rules r
JOIN metadata.feature_classes ds   ON ds.code = 'DIST_SWITCH'
JOIN metadata.feature_classes succ ON succ.code IN ('MV_SWITCH', 'MV_RECLOSER')
WHERE r.feature_class_id = ds.id
  AND NOT EXISTS (
    SELECT 1 FROM metadata.diagram_placement_rules x
    WHERE x.container_feature_class_id = r.container_feature_class_id
      AND x.diagram_type_value_id = r.diagram_type_value_id
      AND x.feature_class_id = succ.id
  );

-- ---------------------------------------------------------------------------
-- Remove DIST_SWITCH's metadata rows that FK the class with NO ON DELETE (they
-- would each block the class-row delete), then delete the class row itself. The
-- class-row delete CASCADEs terminals, subtypes, fields, symbology_rules,
-- cim_class_mappings and (via fields) cim_attribute_mappings -- all now
-- unreferenced because connectivity/relationships were repointed above.
-- ---------------------------------------------------------------------------
DELETE FROM metadata.trace_barrier_rules
WHERE feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'DIST_SWITCH');

DELETE FROM metadata.snapping_rules
WHERE from_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'DIST_SWITCH')
   OR to_feature_class_id   = (SELECT id FROM metadata.feature_classes WHERE code = 'DIST_SWITCH');

DELETE FROM metadata.network_rules
WHERE from_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'DIST_SWITCH')
   OR to_feature_class_id   = (SELECT id FROM metadata.feature_classes WHERE code = 'DIST_SWITCH');

DELETE FROM metadata.relationship_classes
WHERE origin_feature_class_id      = (SELECT id FROM metadata.feature_classes WHERE code = 'DIST_SWITCH')
   OR destination_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'DIST_SWITCH');

DELETE FROM metadata.feature_classes WHERE code = 'DIST_SWITCH';

-- ---------------------------------------------------------------------------
-- Finally drop the now-empty physical table AND its per-class Martin tile
-- function. The tile function gis.dist_switch(z,x,y)->bytea was created at
-- compile time (migration 0038 / gis.sync_tile_function); it is NOT a tracked
-- dependency of the table, so DROP TABLE alone leaves it behind. Martin
-- auto-discovers it and keeps advertising `dist_switch` as a tile source, then
-- 500s on every /tiles/dist_switch/z/x/y request because the backing table is
-- gone. Drop the function first (same order as Uncompile / the RMU collapse
-- seeds) so a failure rolls back rather than orphaning a function.
-- ---------------------------------------------------------------------------
DROP FUNCTION IF EXISTS gis.dist_switch(integer, integer, integer);
DROP TABLE IF EXISTS gis.dist_switch;

COMMIT;
