-- seed/pole_mount_capacitor_bank.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files. Assembly-backlog
-- Tier-2 #6 (part 2 of 2) -- the POLE_MOUNT_CAP_BANK kit for the general
-- assembly engine (api/internal/assembly, migration 0093). Part 1 is the class
-- seed/mv_capacitor_bank_example.sql; the volt-VAR analysis integration lives in
-- api/internal/powerflow (resolveCapacitor + voltvar.go).
--
-- Models a pole-mounted SWITCHED MV capacitor bank as an ASSEMBLY: place one
-- POLE and get its pole-top kit -- a cap switch and the capacitor bank behind
-- it -- spatially supported by the pole and internally wired.
--
-- Uses only EXISTING classes -- POLE (seed/pole_example.sql), MV_SWITCH
-- (seed/mv_lv_switch_example.sql), and MV_CAP_BANK (seed/mv_capacitor_bank_example.sql,
-- which this seed's part 1 adds). Like the recloser kit this adds NO new Go: the
-- general engine already drives chain kits off per-item structural-parent +
-- wire-to-position, so a new kit is pure metadata -- the two structural support
-- classes, the internal switch->bank leg, and the template recipe.
--
-- Why a cap switch in front of the bank: a distribution capacitor bank is almost
-- always SWITCHED (in on peak load / high demand for VAr support and voltage,
-- out off-peak to avoid over-voltage). The MV_SWITCH is the physical switching
-- device; the bank itself is the switched subtype whose device_state the
-- volt-VAR study reads to decide whether it is contributing. Placing the kit
-- with the bank born OPEN (switched out, not yet commissioned) is exactly the
-- state the volt-VAR analysis flags as a candidate to switch IN.
--
-- Kit (creation/wiring order):
--   pos 1 MV_SWITCH    the cap switch -- fed EXTERNALLY (MV line -> T1, the rule
--                      MV_CONDUCTOR -> MV_SWITCH T1 ships with the switch class);
--                      no internal wire. Born CLOSED (the tap point is live).
--   pos 2 MV_CAP_BANK  the switched bank -- MV_SWITCH.T2 (load side) ->
--                      MV_CAP_BANK.T1 (leg added here). Born switched OUT
--                      (device_state OPEN) so it reads as an available volt-VAR
--                      resource, not yet injecting. T2 is the earth return
--                      (shunt dead-end), so the bank carries no trace onward.
-- Both items are spatial_support-held by the pole (one_to_many).
--
-- Apply with psql after the dependency seeds above, migration 0093, and BOTH
-- part 1 (seed/mv_capacitor_bank_example.sql) and MV_SWITCH:
--   task seed-pole-mount-capacitor-bank
-- No compile step -- pure metadata rows; the component classes are compiled by
-- their own seeds (MV_CAP_BANK via `task compile CODE=MV_CAP_BANK`).
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + placing the kit via the
-- Assemblies menu + a volt-VAR run against it are still owed to call it verified.

-- ---------------------------------------------------------------------------
-- Structural relationship classes -- the pole SUPPORTS each kit component
-- (kind 'spatial_support', already in the 0010 CHECK). one_to_many: one pole
-- carries many mounted items, each mounted item sits on exactly one pole.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'POLE supports ' || child.code, pole.id, child.id, 'spatial_support', 'one_to_many'
FROM metadata.feature_classes pole
JOIN metadata.feature_classes child ON child.code IN ('MV_SWITCH', 'MV_CAP_BANK')
WHERE pole.code = 'POLE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes rc
    WHERE rc.origin_feature_class_id = pole.id AND rc.destination_feature_class_id = child.id AND rc.kind = 'spatial_support'
  );

-- ---------------------------------------------------------------------------
-- network_rules -- the internal leg the kit wires along: the cap switch's load
-- side (MV_SWITCH T2) feeds the capacitor bank's primary tap (MV_CAP_BANK T1).
-- phase_rule NULL: an internal factory leg is authorized structurally, not by a
-- voltage-class match, exactly as the pole-transformer / packaged-substation /
-- recloser internal legs are. EstablishConnection matches a rule in EITHER
-- direction, so one rule per leg suffices; both terminal joins reference an
-- already-joined class (all-explicit joins), sidestepping the comma-then-JOIN
-- "invalid reference to FROM-clause entry" gotcha.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_SWITCH T2 -> MV_CAP_BANK T1', sw.id, swt2.id, cb.id, cbt1.id, NULL, true
FROM metadata.feature_classes sw
JOIN metadata.feature_classes cb ON cb.code = 'MV_CAP_BANK'
JOIN metadata.terminals swt2 ON swt2.feature_class_id = sw.id AND swt2.code = 'T2'
JOIN metadata.terminals cbt1 ON cbt1.feature_class_id = cb.id AND cbt1.code = 'T1'
WHERE sw.code = 'MV_SWITCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules nr
    WHERE nr.from_feature_class_id = sw.id AND nr.from_terminal_id = swt2.id
      AND nr.to_feature_class_id = cb.id AND nr.to_terminal_id = cbt1.id
  );

-- ---------------------------------------------------------------------------
-- The recipe: POLE_MOUNT_CAP_BANK on the POLE anchor. anchor_subtype_code NULL
-- -- the placing crew picks the pole material, same as POLE_MOUNT_STD /
-- POLE_MOUNT_RECLOSER.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.assembly_templates (anchor_feature_class_id, code, name, anchor_subtype_code)
SELECT fc.id, 'POLE_MOUNT_CAP_BANK', 'Pole-mounted capacitor bank', NULL
FROM metadata.feature_classes fc
WHERE fc.code = 'POLE'
ON CONFLICT (anchor_feature_class_id, code) DO NOTHING;

-- Items. support_parent_position 0 = the anchor pole (spatial_support on every
-- item). wire_to_position 0 = anchor, >=1 = an earlier item; NULL = externally
-- fed. own_terminal is on THIS item; upstream_terminal is on the wire_to target.
INSERT INTO metadata.assembly_items
    (template_id, component_feature_class_id, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, position, offset_x, offset_y, rotation)
SELECT t.id, cfc.id, v.subtype_code, v.attributes::jsonb, v.support_kind, v.support_parent_position, v.wire_to_position, v.own_terminal, v.upstream_terminal, v.position, v.offset_x, v.offset_y, v.rotation
FROM (VALUES
    (1, 'MV_SWITCH',   1, '{"device_state":"CLOSED","condition_rating":"GOOD"}',                        'spatial_support', 0, NULL, NULL, NULL,  0.0,  0.0, 0.0),
    (2, 'MV_CAP_BANK', 2, '{"rated_kvar":300,"device_state":"OPEN","condition_rating":"GOOD"}',          'spatial_support', 0, 1,    'T1', 'T2',  0.0, -1.5, 0.0)
) AS v(position, component_code, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, offset_x, offset_y, rotation)
JOIN metadata.feature_classes cfc ON cfc.code = v.component_code
JOIN metadata.assembly_templates t ON t.code = 'POLE_MOUNT_CAP_BANK'
  AND t.anchor_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'POLE')
ON CONFLICT (template_id, position) DO NOTHING;
