-- seed/pole_mount_bess.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files. Assembly-backlog
-- Tier-2 #4 (part 2 of 2) -- the POLE_MOUNT_BESS kit for the general assembly
-- engine (api/internal/assembly, migration 0093). Part 1 is the class
-- seed/mv_bess_example.sql; the storage-dispatch analysis integration lives in
-- api/internal/powerflow (resolveStorage + storage.go).
--
-- Models a pole-mounted grid battery (a small grid-edge / community storage unit)
-- as an ASSEMBLY: place one POLE and get its kit -- an isolating switch, the
-- battery-storage unit behind it, and a surge arrester protecting the inverter --
-- spatially supported by the pole and internally wired.
--
-- Uses only EXISTING classes -- POLE (seed/pole_example.sql), MV_SWITCH
-- (seed/mv_lv_switch_example.sql), MV_ARRESTER (seed/mv_arrester_example.sql), and
-- MV_BESS (seed/mv_bess_example.sql, which this seed's part 1 adds). Like the
-- recloser / capacitor kits this adds NO new Go: the general engine already drives
-- these kits off per-item structural-parent + wire-to-position, so a new kit is
-- pure metadata -- the structural support classes, the internal legs, and the
-- template recipe.
--
-- Why a switch in front of the battery, an arrester beside it: the switch isolates
-- the unit for maintenance and gives the dispatch study a clean point of common
-- coupling; the arrester shunts surges away from the sensitive power-electronics
-- inverter, exactly as it protects the recloser / regulator in those kits. The
-- battery is placed IDLE (storage_dispatch_kw 0) at mid state-of-charge, which is
-- exactly the state the storage-dispatch analysis treats as a free resource to
-- dispatch for voltage support or peak-shaving.
--
-- Kit (creation/wiring order):
--   pos 1 MV_SWITCH    the isolating switch -- fed EXTERNALLY (MV line -> T1, the
--                      rule MV_CONDUCTOR -> MV_SWITCH T1 ships with the switch
--                      class); no internal wire. Born CLOSED (the tap is live).
--   pos 2 MV_BESS      the battery -- MV_SWITCH.T2 (load side) -> MV_BESS.T1 (leg
--                      added here). Born idle (storage_dispatch_kw 0) at 50% SoC,
--                      a full-window dispatch candidate. T1 is its only terminal
--                      (a leaf), so it carries no trace onward.
--   pos 3 MV_ARRESTER  surge protection -- MV_SWITCH.T2 (same load-side node) ->
--                      MV_ARRESTER.T1 (leg added here), a shunt tap. T2 is the
--                      earth return (shunt dead-end), no trace onward.
-- All three items are spatial_support-held by the pole (one_to_many).
--
-- Apply with psql after the dependency seeds above, migration 0093, and part 1
-- (seed/mv_bess_example.sql), MV_SWITCH and MV_ARRESTER:
--   task seed-pole-mount-bess
-- No compile step -- pure metadata rows; the component classes are compiled by
-- their own seeds (MV_BESS via `task compile CODE=MV_BESS`).
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + placing the kit via the
-- Assemblies menu + a storage-dispatch run against it are still owed to call it
-- verified.

-- ---------------------------------------------------------------------------
-- Structural relationship classes -- the pole SUPPORTS each kit component
-- (kind 'spatial_support', already in the 0010 CHECK). one_to_many: one pole
-- carries many mounted items, each mounted item sits on exactly one pole.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'POLE supports ' || child.code, pole.id, child.id, 'spatial_support', 'one_to_many'
FROM metadata.feature_classes pole
JOIN metadata.feature_classes child ON child.code IN ('MV_SWITCH', 'MV_BESS', 'MV_ARRESTER')
WHERE pole.code = 'POLE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes rc
    WHERE rc.origin_feature_class_id = pole.id AND rc.destination_feature_class_id = child.id AND rc.kind = 'spatial_support'
  );

-- ---------------------------------------------------------------------------
-- network_rules -- the two internal legs the kit wires along, both off the
-- switch's load side (MV_SWITCH T2, the common point of coupling): to the
-- battery's primary (MV_BESS T1) and to the arrester's tap (MV_ARRESTER T1).
-- phase_rule NULL: internal factory legs are authorized structurally, not by a
-- voltage-class match, exactly as the recloser / capacitor internal legs are.
-- EstablishConnection matches a rule in EITHER direction, so one rule per leg
-- suffices; both terminal joins reference an already-joined class (all-explicit
-- joins), sidestepping the comma-then-JOIN "invalid reference to FROM-clause
-- entry" gotcha.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_SWITCH T2 -> MV_BESS T1', sw.id, swt2.id, b.id, bt1.id, NULL, true
FROM metadata.feature_classes sw
JOIN metadata.feature_classes b ON b.code = 'MV_BESS'
JOIN metadata.terminals swt2 ON swt2.feature_class_id = sw.id AND swt2.code = 'T2'
JOIN metadata.terminals bt1 ON bt1.feature_class_id = b.id AND bt1.code = 'T1'
WHERE sw.code = 'MV_SWITCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules nr
    WHERE nr.from_feature_class_id = sw.id AND nr.from_terminal_id = swt2.id
      AND nr.to_feature_class_id = b.id AND nr.to_terminal_id = bt1.id
  );

INSERT INTO metadata.network_rules (name, from_feature_class_id, from_terminal_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_SWITCH T2 -> MV_ARRESTER T1', sw.id, swt2.id, ar.id, art1.id, NULL, true
FROM metadata.feature_classes sw
JOIN metadata.feature_classes ar ON ar.code = 'MV_ARRESTER'
JOIN metadata.terminals swt2 ON swt2.feature_class_id = sw.id AND swt2.code = 'T2'
JOIN metadata.terminals art1 ON art1.feature_class_id = ar.id AND art1.code = 'T1'
WHERE sw.code = 'MV_SWITCH'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules nr
    WHERE nr.from_feature_class_id = sw.id AND nr.from_terminal_id = swt2.id
      AND nr.to_feature_class_id = ar.id AND nr.to_terminal_id = art1.id
  );

-- ---------------------------------------------------------------------------
-- The recipe: POLE_MOUNT_BESS on the POLE anchor. anchor_subtype_code NULL --
-- the placing crew picks the pole material, same as the sibling pole kits.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.assembly_templates (anchor_feature_class_id, code, name, anchor_subtype_code)
SELECT fc.id, 'POLE_MOUNT_BESS', 'Pole-mounted battery storage', NULL
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
    (1, 'MV_SWITCH',   1, '{"device_state":"CLOSED","condition_rating":"GOOD"}',                                                                              'spatial_support', 0, NULL, NULL, NULL,  0.0,  0.0, 0.0),
    (2, 'MV_BESS',     1, '{"rated_power_kw":250,"storage_dispatch_kw":0,"energy_capacity_kwh":500,"state_of_charge_pct":50,"power_factor":1,"condition_rating":"GOOD"}', 'spatial_support', 0, 1,    'T1', 'T2',  0.0, -1.5, 0.0),
    (3, 'MV_ARRESTER', 1, '{"condition_rating":"GOOD"}',                                                                                                       'spatial_support', 0, 1,    'T1', 'T2',  1.2, -1.5, 0.0)
) AS v(position, component_code, subtype_code, attributes, support_kind, support_parent_position, wire_to_position, own_terminal, upstream_terminal, offset_x, offset_y, rotation)
JOIN metadata.feature_classes cfc ON cfc.code = v.component_code
JOIN metadata.assembly_templates t ON t.code = 'POLE_MOUNT_BESS'
  AND t.anchor_feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'POLE')
ON CONFLICT (template_id, position) DO NOTHING;
