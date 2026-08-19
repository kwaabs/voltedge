-- seed/lv_metering_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase 9, Slice 3 (metering as network + meter chamber). Three classes
-- plus the relationship rows that finally make the multi-occupancy
-- apartment case -- "20 meters on one service point" -- expressible, the
-- question that kicked off the whole phase:
--   * METER        -- an INLINE two-terminal junction (line/load), NOT a
--     geometry-less billing record. Power flows through it, so it's a
--     real traced node. Carries the CIS/MDM join keys (meter_serial,
--     account_ref) and a CT-vs-whole-current subtype; 1ph vs 3ph is the
--     `phase` field, not a subtype.
--   * LV_BUSBAR    -- the chamber's distribution board: one incoming way
--     (T1) plus a single equipotential tap side (T2) that fans out to
--     every tenant meter (see the N-meters note below).
--   * METER_CHAMBER -- the physical container asset (network_role
--     'container'), the FIRST genuine use of the `containment`
--     relationship kind (migrations/0010). Contains the busbar and the
--     meters.
--
-- KEY MODELLING DECISIONS (recorded here so they're not silent):
--
--   1. METER geometry is 'point', not 'none'. The design doc floated
--      geometry 'none' (a meter's real position is the chamber's). Chosen
--      'point' instead: it keeps the meter a first-class placeable +
--      traceable junction, uniform with every other junction
--      (DIST_XFMR/DIST_SWITCH/SERVICE_CUTOUT/LV_LINK_BOX/SERVICE_POINT).
--      "Lives inside the chamber" is expressed by the containment
--      relationship, not by a null geometry. N meters at one chamber
--      simply share ~the same point.
--
--   2. LV_BUSBAR has just T1 (incoming) + T2 (the bar). A busbar is a
--      solid equipotential bar, NOT a set of individually switchable ways
--      (that's LV_LINK_BOX, which is why *it* has discrete T1..T4). So the
--      20-tenant apartment case needs NO 20-terminal schema: many meters
--      hang off the one T2 tap, each a distinct feature_connectivity row
--      ((busbar, T2, meter_i, T1) -- unique because to_feature_id differs,
--      migrations/0016). One rule `LV_BUSBAR T2 -> METER T1` scales to any
--      N. This is the crux of the apartment case.
--
--   3. The meter is an OPTIONAL inline node -- the direct
--      SERVICE_CUTOUT/LV_CONDUCTOR -> SERVICE_POINT rules from Slices 1-2
--      are LEFT VALID, not retired. Not every deployment models the meter
--      as a graph node, and unmetered supplies exist. A service is metered
--      by *wiring a METER into the chain*, not by the absence of a
--      shortcut rule -- so this slice ADDS the metered path rather than
--      destructively repointing the way Slice 1 had to (there the LV run
--      was a structural necessity; here the meter is genuinely optional).
--      SERVICE_POINT needs no schema change to "finish its move to
--      point-of-supply leaf" -- it already is a single-T1 leaf; the move
--      completes the moment supply can flow through METER.T2 into it.
--
--   4. SERVICE_POINT -> METER is seeded as a `composite` (ownership)
--      relationship_class, one_to_one -- the single-dwelling contrast to
--      the chamber's `containment` (spatial). Both kinds now have a real
--      user, per docs/phase-9's "Decisions made".
--
-- Depends on seed/lv_devices_example.sql (SERVICE_CUTOUT), and through it
-- seed/lv_conductors_example.sql (LV_CONDUCTOR) and
-- seed/service_point_example.sql (SERVICE_POINT), having been applied. The
-- PHASE / CONDITION_RATING domains and the field_groups are re-asserted
-- idempotently so this seed is self-sufficient regardless of order.
--
-- Apply with psql, then compile the three new classes:
--   task seed-lv-metering   (or the psql -f invocation directly)
--   task compile CODE=METER
--   task compile CODE=LV_BUSBAR
--   task compile CODE=METER_CHAMBER
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL
-- has been reviewed statically -- a live psql apply + the three `task
-- compile` runs + a downstream-trace-through-a-meter check are still owed
-- to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('maintenance', 'Maintenance', 2),
    ('service', 'Service', 1)
ON CONFLICT (code) DO NOTHING;

-- PHASE + CONDITION_RATING -- normally seeded upstream; re-asserted
-- idempotently so the phase / condition fields below always have a domain.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('PHASE', 'Phase', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('PHASE', 'L1',      'L1',                     1),
    ('PHASE', 'L2',      'L2',                     2),
    ('PHASE', 'L3',      'L3',                     3),
    ('PHASE', 'N',       'Neutral',                4),
    ('PHASE', 'L1N',     'L1 + N (1-phase)',       5),
    ('PHASE', 'L2N',     'L2 + N (1-phase)',       6),
    ('PHASE', 'L3N',     'L3 + N (1-phase)',       7),
    ('PHASE', 'L1L2L3N', 'L1 L2 L3 + N (3-phase)', 8),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ------------------------------------------------------------- METER --
-- Inline junction, T1 line-side / T2 load-side, so it sits between the
-- service cutout (or busbar) and the point of supply. Subtypes are the
-- metering arrangement (whole-current vs CT-metered); 1ph/3ph is `phase`.

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('METER', 'Meter', 'point', 4326, 'junction', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'whole_current'), (2, 'ct_metered')) AS v(code, name) ON true
WHERE fc.code = 'METER'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- meter_serial + account_ref are the CIS/MDM join keys (GIS holds keys,
-- the external system stays the record of truth -- the connector itself is
-- a later, auth-gated open item). meter_serial is the device identity
-- (required); account_ref is the billing link and is nullable -- a meter
-- can exist unassigned (in stock, or a de-energised supply). phase is
-- required, like every phase-bearing class since Slice 1.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('meter_serial', 'text', NULL,    'service',    false),
    ('account_ref',  'text', NULL,    'service',    true),
    ('phase',        'text', 'PHASE', 'electrical', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'METER'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- Required per subtype: the two non-nullable fields. account_ref stays
-- optional, so it gets no rule.
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'METER')
  AND f.name IN ('meter_serial', 'phase')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'secondary', true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'METER'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- --------------------------------------------------------- LV_BUSBAR --
-- The chamber's distribution board. T1 incoming, T2 the equipotential bar
-- that fans out to every tenant meter (see decision 2 in the header).

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('LV_BUSBAR', 'LV Busbar', 'point', 4326, 'junction', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'lv_distribution_board'), (2, 'busbar_riser')) AS v(code, name) ON true
WHERE fc.code = 'LV_BUSBAR'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('phase', 'text', 'PHASE', 'electrical', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'LV_BUSBAR'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'LV_BUSBAR')
  AND f.name = 'phase'
ON CONFLICT (subtype_id, field_id) DO NOTHING;

INSERT INTO metadata.terminals (feature_class_id, subtype_id, code, role, phase_capable)
SELECT fc.id, NULL, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('T1', 'primary', true),
    ('T2', 'other', true)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'LV_BUSBAR'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ----------------------------------------------------- METER_CHAMBER --
-- Physical container asset -- network_role 'container' (NOT a traced node,
-- no terminals). geometry 'point' (a chamber has a footprint; polygon is
-- available but point is enough to locate it). The containment
-- relationship_classes below are what tie it to its busbar + meters.

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('METER_CHAMBER', 'Meter Chamber', 'point', 4326, 'container', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, v.code, v.name
FROM metadata.feature_classes fc
JOIN (VALUES (1, 'cabinet'), (2, 'room')) AS v(code, name) ON true
WHERE fc.code = 'METER_CHAMBER'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- chamber_ref is the asset tag (nullable -- may be unlabelled);
-- condition_rating matches every other physical asset class.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('chamber_ref',      'text', NULL,               'service',     true),
    ('condition_rating', 'text', 'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'METER_CHAMBER'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'METER_CHAMBER')
  AND f.name = 'condition_rating'
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- network_rules -- guarded with WHERE NOT EXISTS, all phase_rule NULL
-- (device hops, not the phase-matched edge splice the LV_CONDUCTOR ->
-- LV_CONDUCTOR rule gates). METER_CHAMBER has none: it's containment, not
-- a traced node.

-- The metered chain, single-dwelling: cutout load side, or a bare LV run,
-- into the meter's line side (T1).
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'SERVICE_CUTOUT T2 -> METER T1', sc.id, t2.id, m.id, t1.id, NULL, true
FROM metadata.feature_classes sc CROSS JOIN metadata.feature_classes m
JOIN metadata.terminals t2 ON t2.feature_class_id = sc.id AND t2.code = 'T2'
JOIN metadata.terminals t1 ON t1.feature_class_id = m.id AND t1.code = 'T1'
WHERE sc.code = 'SERVICE_CUTOUT' AND m.code = 'METER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = sc.id AND from_terminal_id = t2.id AND to_feature_class_id = m.id AND to_terminal_id = t1.id
  );

INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'LV_CONDUCTOR -> METER T1', lv.id, m.id, t1.id, NULL, true
FROM metadata.feature_classes lv CROSS JOIN metadata.feature_classes m
JOIN metadata.terminals t1 ON t1.feature_class_id = m.id AND t1.code = 'T1'
WHERE lv.code = 'LV_CONDUCTOR' AND m.code = 'METER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = lv.id AND to_feature_class_id = m.id AND to_terminal_id = t1.id
  );

-- The meter's load side to the point of supply -- this is what makes
-- SERVICE_POINT a metered leaf.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'METER T2 -> SERVICE_POINT T1', m.id, t2.id, sp.id, t1.id, NULL, true
FROM metadata.feature_classes m CROSS JOIN metadata.feature_classes sp
JOIN metadata.terminals t2 ON t2.feature_class_id = m.id AND t2.code = 'T2'
JOIN metadata.terminals t1 ON t1.feature_class_id = sp.id AND t1.code = 'T1'
WHERE m.code = 'METER' AND sp.code = 'SERVICE_POINT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = m.id AND from_terminal_id = t2.id AND to_feature_class_id = sp.id AND to_terminal_id = t1.id
  );

-- The apartment chain: building main (cutout, or a bare LV run) into the
-- busbar's incoming way (T1).
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'SERVICE_CUTOUT T2 -> LV_BUSBAR T1', sc.id, t2.id, b.id, t1.id, NULL, true
FROM metadata.feature_classes sc CROSS JOIN metadata.feature_classes b
JOIN metadata.terminals t2 ON t2.feature_class_id = sc.id AND t2.code = 'T2'
JOIN metadata.terminals t1 ON t1.feature_class_id = b.id AND t1.code = 'T1'
WHERE sc.code = 'SERVICE_CUTOUT' AND b.code = 'LV_BUSBAR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = sc.id AND from_terminal_id = t2.id AND to_feature_class_id = b.id AND to_terminal_id = t1.id
  );

INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'LV_CONDUCTOR -> LV_BUSBAR T1', lv.id, b.id, t1.id, NULL, true
FROM metadata.feature_classes lv CROSS JOIN metadata.feature_classes b
JOIN metadata.terminals t1 ON t1.feature_class_id = b.id AND t1.code = 'T1'
WHERE lv.code = 'LV_CONDUCTOR' AND b.code = 'LV_BUSBAR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = lv.id AND to_feature_class_id = b.id AND to_terminal_id = t1.id
  );

-- The bar (T2) fans out to every tenant meter -- ONE rule, any number of
-- meters (decision 2 in the header). Each connection is a distinct
-- feature_connectivity row on the same terminal.
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'LV_BUSBAR T2 -> METER T1', b.id, t2.id, m.id, t1.id, NULL, true
FROM metadata.feature_classes b CROSS JOIN metadata.feature_classes m
JOIN metadata.terminals t2 ON t2.feature_class_id = b.id AND t2.code = 'T2'
JOIN metadata.terminals t1 ON t1.feature_class_id = m.id AND t1.code = 'T1'
WHERE b.code = 'LV_BUSBAR' AND m.code = 'METER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = b.id AND from_terminal_id = t2.id AND to_feature_class_id = m.id AND to_terminal_id = t1.id
  );

-- relationship_classes -- metadata only (no physical footprint, read by
-- api/internal/relationship), guarded on the UNIQUE name via NOT EXISTS.
-- The chamber CONTAINS (spatial) its busbar and its meters; one chamber,
-- many meters, so one_to_many. This is the first real use of
-- `containment` (migrations/0010's comment names it as the intended
-- example and notes nothing used it yet).

INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'METER_CHAMBER contains LV_BUSBAR', mc.id, b.id, 'containment', 'one_to_many'
FROM metadata.feature_classes mc, metadata.feature_classes b
WHERE mc.code = 'METER_CHAMBER' AND b.code = 'LV_BUSBAR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = mc.id AND destination_feature_class_id = b.id AND kind = 'containment'
  );

INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'METER_CHAMBER contains METER', mc.id, m.id, 'containment', 'one_to_many'
FROM metadata.feature_classes mc, metadata.feature_classes m
WHERE mc.code = 'METER_CHAMBER' AND m.code = 'METER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = mc.id AND destination_feature_class_id = m.id AND kind = 'containment'
  );

-- The single-dwelling contrast: a point of supply OWNS its meter --
-- `composite` (ownership), one_to_one. Gives that kind its first real
-- user alongside containment's, per docs/phase-9's "Decisions made".
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'SERVICE_POINT owns METER (composite)', sp.id, m.id, 'composite', 'one_to_one'
FROM metadata.feature_classes sp, metadata.feature_classes m
WHERE sp.code = 'SERVICE_POINT' AND m.code = 'METER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = sp.id AND destination_feature_class_id = m.id AND kind = 'composite'
  );
