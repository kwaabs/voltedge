-- seed/rmu_diagram_placement.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase RMU (diagram allow-list). Constrains what may be placed on an RMU's
-- one-line diagram so the (RMU x ONE_LINE) pairing stops accepting arbitrary
-- classes (e.g. a POWER_XFMR). Same machinery the meter chamber and switchboard
-- already use (metadata.diagram_placement_rules, migration 0068): a
-- (container class x diagram type) pairing with ANY rows is restricted to
-- exactly those classes; a pairing with NO rows stays unconstrained. Keying on
-- the container means this touches ONLY RMU one-lines -- a substation-yard
-- ONE_LINE (different container, no rows) is unaffected.
--
-- Allowed set (user decision 2026-09-07): MV_BUSBAR + MV_SWITCH + MV_FUSE +
-- MV_CONDUCTOR. The RMU's actual contained parts are just MV_BUSBAR (internal
-- bus) + MV_SWITCH (the ways -- a fused tee way is an MV_SWITCH with is_fused,
-- not a separate class); MV_FUSE is included so a discrete fuse symbol can still
-- be hand-drawn, and MV_CONDUCTOR so the incoming/outgoing feeder lines can be
-- shown on the one-line.
--
-- Depends on: seed/rmu_example.sql (RMU container class),
-- seed/mv_busbar_example.sql (MV_BUSBAR), seed/mv_lv_switch_example.sql
-- (MV_SWITCH), seed/mv_protection_example.sql (MV_FUSE),
-- seed/connectivity_example.sql (MV_CONDUCTOR) and the ONE_LINE DIAGRAM_TYPE
-- value (seed/diagram_example.sql -- re-asserted idempotently below so this
-- seed is self-sufficient). Apply with psql after those; no compile needed
-- (this file only writes metadata rows, adds no columns to any class).

-- ------------------------------------------------- ONE_LINE diagram type --
-- DIAGRAM_TYPE is an ordinary coded domain ("just another row in domains" --
-- docs/metadata-core.md), re-asserted idempotently here so this seed stands on
-- its own whether or not diagram_example.sql ran first.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('DIAGRAM_TYPE', 'Diagram type', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, 'ONE_LINE', 'One-line diagram', 1
FROM metadata.domains d
WHERE d.code = 'DIAGRAM_TYPE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.domain_values
    WHERE domain_id = d.id AND code = 'ONE_LINE'
  );

-- ------------------------------------------- MV_CONDUCTOR placeable fix --
-- MV_CONDUCTOR was seeded (seed/connectivity_example.sql) without setting
-- is_diagram_placeable, so it defaults to false -- and migration 0032's
-- edge/junction backfill only touched rows that existed when it ran, not a
-- later-seeded conductor. Without this flip the allow-list row below would be
-- inert, because api/internal/diagram placeFeature also enforces the GLOBAL
-- is_diagram_placeable gate (a rule can restrict, never widen). Note this makes
-- MV_CONDUCTOR placeable on ANY unconstrained diagram too (e.g. a substation
-- one-line), which is the intended, sensible behaviour for a feeder line.
UPDATE metadata.feature_classes
   SET is_diagram_placeable = true
 WHERE code = 'MV_CONDUCTOR';

-- ------------------------------------------- diagram placement allow-list --
-- Restrict (RMU x ONE_LINE) to exactly the four classes an RMU one-line should
-- carry. Because this pairing now has rows, placeFeature rejects any other
-- class on it -- even a globally is_diagram_placeable one (migration 0068).
-- Guarded on the (container, type, class) unique index so re-applying is a
-- no-op.
INSERT INTO metadata.diagram_placement_rules (container_feature_class_id, diagram_type_value_id, feature_class_id)
SELECT rmu.id, dv.id, allowed.id
FROM metadata.feature_classes rmu
CROSS JOIN metadata.domain_values dv
JOIN metadata.domains d ON d.id = dv.domain_id AND d.code = 'DIAGRAM_TYPE'
JOIN metadata.feature_classes allowed
    ON allowed.code IN ('MV_BUSBAR', 'MV_SWITCH', 'MV_FUSE', 'MV_CONDUCTOR')
WHERE rmu.code = 'RMU'
  AND dv.code = 'ONE_LINE'
ON CONFLICT (container_feature_class_id, diagram_type_value_id, feature_class_id) DO NOTHING;
