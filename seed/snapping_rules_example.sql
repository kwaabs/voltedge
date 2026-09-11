-- seed/snapping_rules_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this
-- lives under /seed on purpose, same reasoning as the other seed files.
-- The first real rows metadata.snapping_rules has ever held: the table
-- has existed since Phase 1 (migrations/0013) but had zero rows anywhere
-- in this project, so its read path (api/internal/rules, the feature
-- panel/admin "Rules" display) only ever showed an empty section. This
-- seeds the SNAP_TARGET_TYPE domain the table's target_type_value_id
-- column references, plus a small set of real digitizing-snap rules over
-- the existing seeded topology (MV_CONDUCTOR / DIST_XFMR / DIST_SWITCH /
-- SERVICE_POINT).
--
-- These are PURELY the metadata half. As of this seed the map's own OL
-- Snap interaction is still a generic snap-to-any-visible-vertex with a
-- single global toggle (docs/phase-3-snapping.md) -- it does not yet
-- consume tolerance_m / target_type / eligibility. So these rows drive
-- the read-only display today; wiring the drawing UI to actually honor
-- them is the next slice of docs/open-items.md's "Metadata-driven
-- snapping_rules" item.
--
-- A snap is purely geometric (two vertices made coincident); it never
-- implies connectivity (that's still an explicit establish action gated
-- by metadata.network_rules -- see migrations/0013's header). Where a
-- rule mirrors a network_rule below, that's deliberate: you snap to the
-- transformer so you *can* then connect to it, but the snap alone
-- connects nothing.
--
-- Depends on seed/connectivity_example.sql (MV_CONDUCTOR, DIST_XFMR),
-- seed/switching_device_example.sql (DIST_SWITCH),
-- seed/service_point_example.sql (SERVICE_POINT), and -- for the LV
-- rules at the end (5-7) -- seed/lv_conductors_example.sql (LV_CONDUCTOR)
-- having been applied. No compile step: snapping_rules is pure metadata,
-- never compiled into a physical table.
--
-- Apply with psql after those seeds:
--   task seed-snapping   (or the psql -f invocation directly)

-- SNAP_TARGET_TYPE -- the domain migrations/0013 names for
-- target_type_value_id (point_feature / endpoint / edge / vertex), reusing
-- the domains mechanism rather than a bespoke enum. All four values are
-- seeded so the domain is complete for later authoring, even though the
-- rules below only use point_feature and endpoint.

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('SNAP_TARGET_TYPE', 'Snap target type', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('SNAP_TARGET_TYPE', 'point_feature', 'Point feature', 1),
    ('SNAP_TARGET_TYPE', 'endpoint', 'Endpoint', 2),
    ('SNAP_TARGET_TYPE', 'edge', 'Edge', 3),
    ('SNAP_TARGET_TYPE', 'vertex', 'Vertex', 4)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- snapping_rules -- no unique constraint on this table (a rule set is
-- authored deliberately, not upserted by code), so each insert guards
-- idempotency with WHERE NOT EXISTS keyed on the from/to class, the
-- target terminal (IS NOT DISTINCT FROM, so a NULL terminal matches a
-- NULL terminal), and the target type -- same pattern the network_rules
-- seeds use.
--
-- priority: lower wins when several targets fall within tolerance at once
-- (migrations/0013). A whole point feature outranks a bare conductor
-- endpoint, so point_feature snaps are priority 1, endpoint snaps 2.
-- tolerance_m is real meters (all these classes are SRID 4326).

-- 1. Digitizing an MV_CONDUCTOR, its endpoint should snap to a DIST_XFMR
-- (the transformer's primary, T1). is_required = true: a distribution
-- conductor that stops short of the transformer it feeds is almost
-- certainly a digitizing miss, not an intentional loose end -- this is
-- migrations/0013's own canonical is_required example. Mirrors the
-- MV_CONDUCTOR -> DIST_XFMR T1 network_rule.
INSERT INTO metadata.snapping_rules (from_feature_class_id, to_feature_class_id, to_terminal_id, target_type_value_id, tolerance_m, priority, is_required)
SELECT frm.id, tgt.id, term.id, tt.id, 0.5, 1, true
FROM metadata.feature_classes frm
CROSS JOIN metadata.feature_classes tgt
JOIN metadata.terminals term ON term.feature_class_id = tgt.id AND term.code = 'T1'
JOIN metadata.domain_values tt ON tt.code = 'point_feature'
  AND tt.domain_id = (SELECT id FROM metadata.domains WHERE code = 'SNAP_TARGET_TYPE')
WHERE frm.code = 'MV_CONDUCTOR' AND tgt.code = 'DIST_XFMR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.snapping_rules sr
    WHERE sr.from_feature_class_id = frm.id AND sr.to_feature_class_id = tgt.id
      AND sr.to_terminal_id IS NOT DISTINCT FROM term.id
      AND sr.target_type_value_id = tt.id
  );

-- 2. Digitizing an MV_CONDUCTOR, snap to a DIST_SWITCH -- either side is
-- valid (a switch sits inline on a run), so no specific to_terminal; the
-- terminal is picked at connect time. Optional: a conductor legitimately
-- passes near a switch it doesn't touch.
INSERT INTO metadata.snapping_rules (from_feature_class_id, to_feature_class_id, to_terminal_id, target_type_value_id, tolerance_m, priority, is_required)
SELECT frm.id, tgt.id, NULL, tt.id, 0.5, 1, false
FROM metadata.feature_classes frm
CROSS JOIN metadata.feature_classes tgt
JOIN metadata.domain_values tt ON tt.code = 'point_feature'
  AND tt.domain_id = (SELECT id FROM metadata.domains WHERE code = 'SNAP_TARGET_TYPE')
WHERE frm.code = 'MV_CONDUCTOR' AND tgt.code = 'DIST_SWITCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.snapping_rules sr
    WHERE sr.from_feature_class_id = frm.id AND sr.to_feature_class_id = tgt.id
      AND sr.to_terminal_id IS NULL
      AND sr.target_type_value_id = tt.id
  );

-- 3. Digitizing an MV_CONDUCTOR, snap its endpoint to another
-- MV_CONDUCTOR's endpoint -- a tap/junction where two runs meet. A
-- self-referential rule (from and to are the same class); target_type is
-- endpoint, not point_feature (a conductor is a line, it has no single
-- point geometry), at a tighter tolerance and lower priority than a real
-- device snap.
INSERT INTO metadata.snapping_rules (from_feature_class_id, to_feature_class_id, to_terminal_id, target_type_value_id, tolerance_m, priority, is_required)
SELECT frm.id, frm.id, NULL, tt.id, 0.3, 2, false
FROM metadata.feature_classes frm
JOIN metadata.domain_values tt ON tt.code = 'endpoint'
  AND tt.domain_id = (SELECT id FROM metadata.domains WHERE code = 'SNAP_TARGET_TYPE')
WHERE frm.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.snapping_rules sr
    WHERE sr.from_feature_class_id = frm.id AND sr.to_feature_class_id = frm.id
      AND sr.to_terminal_id IS NULL
      AND sr.target_type_value_id = tt.id
  );

-- 4. Digitizing a SERVICE_POINT, snap to a DIST_XFMR's secondary (T2) --
-- the meter is fed off the transformer's LV bus (there's no distinct LV
-- conductor class yet, see docs/open-items.md). Mirrors the DIST_XFMR T2
-- -> SERVICE_POINT network_rule. Optional rather than required: a service
-- point can legitimately exist before its feed is wired in.
INSERT INTO metadata.snapping_rules (from_feature_class_id, to_feature_class_id, to_terminal_id, target_type_value_id, tolerance_m, priority, is_required)
SELECT frm.id, tgt.id, term.id, tt.id, 0.5, 1, false
FROM metadata.feature_classes frm
CROSS JOIN metadata.feature_classes tgt
JOIN metadata.terminals term ON term.feature_class_id = tgt.id AND term.code = 'T2'
JOIN metadata.domain_values tt ON tt.code = 'point_feature'
  AND tt.domain_id = (SELECT id FROM metadata.domains WHERE code = 'SNAP_TARGET_TYPE')
WHERE frm.code = 'SERVICE_POINT' AND tgt.code = 'DIST_XFMR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.snapping_rules sr
    WHERE sr.from_feature_class_id = frm.id AND sr.to_feature_class_id = tgt.id
      AND sr.to_terminal_id IS NOT DISTINCT FROM term.id
      AND sr.target_type_value_id = tt.id
  );

-- LV_CONDUCTOR snap rules (added after this seed's original four, which
-- predate the LV_CONDUCTOR class -- rule 4 above even notes "there's no
-- distinct LV conductor class yet"). These mirror LV_CONDUCTOR's three
-- network_rules in seed/lv_conductors_example.sql, so a digitized LV run
-- snaps to exactly the things it may legally connect to. Depends on that
-- seed having been applied; the CROSS JOINs simply find no LV_CONDUCTOR
-- row and insert nothing if it hasn't.

-- 5. Digitizing an LV_CONDUCTOR, its endpoint should snap to a DIST_XFMR's
-- secondary (T2) -- the LV run is fed off the transformer's LV bus.
-- Mirrors the DIST_XFMR T2 -> LV_CONDUCTOR network_rule. Optional: an LV
-- run can be drawn before its transformer feed is wired in.
INSERT INTO metadata.snapping_rules (from_feature_class_id, to_feature_class_id, to_terminal_id, target_type_value_id, tolerance_m, priority, is_required)
SELECT frm.id, tgt.id, term.id, tt.id, 0.5, 1, false
FROM metadata.feature_classes frm
CROSS JOIN metadata.feature_classes tgt
JOIN metadata.terminals term ON term.feature_class_id = tgt.id AND term.code = 'T2'
JOIN metadata.domain_values tt ON tt.code = 'point_feature'
  AND tt.domain_id = (SELECT id FROM metadata.domains WHERE code = 'SNAP_TARGET_TYPE')
WHERE frm.code = 'LV_CONDUCTOR' AND tgt.code = 'DIST_XFMR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.snapping_rules sr
    WHERE sr.from_feature_class_id = frm.id AND sr.to_feature_class_id = tgt.id
      AND sr.to_terminal_id IS NOT DISTINCT FROM term.id
      AND sr.target_type_value_id = tt.id
  );

-- 6. Digitizing an LV_CONDUCTOR, snap its endpoint to another
-- LV_CONDUCTOR's endpoint -- a main splicing to a service drop, or two
-- mains joined (mirrors the self-referential LV_CONDUCTOR -> LV_CONDUCTOR
-- phase-matched network_rule). Self-referential, target_type endpoint (a
-- conductor is a line, no single point), tighter tolerance and lower
-- priority than a device snap -- same shape as rule 3 for MV_CONDUCTOR.
INSERT INTO metadata.snapping_rules (from_feature_class_id, to_feature_class_id, to_terminal_id, target_type_value_id, tolerance_m, priority, is_required)
SELECT frm.id, frm.id, NULL, tt.id, 0.3, 2, false
FROM metadata.feature_classes frm
JOIN metadata.domain_values tt ON tt.code = 'endpoint'
  AND tt.domain_id = (SELECT id FROM metadata.domains WHERE code = 'SNAP_TARGET_TYPE')
WHERE frm.code = 'LV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.snapping_rules sr
    WHERE sr.from_feature_class_id = frm.id AND sr.to_feature_class_id = frm.id
      AND sr.to_terminal_id IS NULL
      AND sr.target_type_value_id = tt.id
  );

-- 7. Digitizing an LV_CONDUCTOR, snap its endpoint to a SERVICE_POINT's
-- single terminal (T1) -- the run terminates at the meter/service.
-- Mirrors the LV_CONDUCTOR -> SERVICE_POINT T1 network_rule. Optional: a
-- service point can exist before its feed is drawn.
INSERT INTO metadata.snapping_rules (from_feature_class_id, to_feature_class_id, to_terminal_id, target_type_value_id, tolerance_m, priority, is_required)
SELECT frm.id, tgt.id, term.id, tt.id, 0.5, 1, false
FROM metadata.feature_classes frm
CROSS JOIN metadata.feature_classes tgt
JOIN metadata.terminals term ON term.feature_class_id = tgt.id AND term.code = 'T1'
JOIN metadata.domain_values tt ON tt.code = 'point_feature'
  AND tt.domain_id = (SELECT id FROM metadata.domains WHERE code = 'SNAP_TARGET_TYPE')
WHERE frm.code = 'LV_CONDUCTOR' AND tgt.code = 'SERVICE_POINT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.snapping_rules sr
    WHERE sr.from_feature_class_id = frm.id AND sr.to_feature_class_id = tgt.id
      AND sr.to_terminal_id IS NOT DISTINCT FROM term.id
      AND sr.target_type_value_id = tt.id
  );
