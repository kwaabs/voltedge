-- seed/switch_normal_state_fields.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
--
-- Phase 48 Slice 1b -- extend `normal_state` to the switch family that carries
-- genuine normally-open points (NOPs), so the diagram's NOP marker
-- (web/src/components/map/diagram-canvas.tsx, Phase 48 Slice 1) fires on more
-- than just the bus coupler.
--
-- Slice 1 gave the diagram an amber "normally open" marker keyed off a placed
-- feature's `normal_state` field. But that field only existed on MV_BREAKER --
-- seed/bus_coupler_fields.sql added it there for the coupler. So today only a
-- coupler can show the marker; a double-busbar's bus-selector isolators, tie
-- switches, and sectionalizers cannot, even though those are exactly the places
-- an operator scans a one-line for open points.
--
-- This seed adds the SAME `normal_state` coded field (reusing the existing
-- NORMAL_STATE domain and its NORMALLY_CLOSED / NORMALLY_OPEN values from
-- bus_coupler_fields.sql) to the switch classes where the choice is real:
--
--   * MV_ISOLATOR     -- disconnector; the double-busbar bus SELECTOR. A bay's
--                        two selectors are one NORMALLY_CLOSED (selected bus) +
--                        one NORMALLY_OPEN (the un-selected bus) -- this is the
--                        field that makes "which bus is this feeder on" legible.
--   * MV_SWITCH       -- generic MV switch (tie / sectionalizing); the classic
--                        distribution normally-open point lives here.
--   * MV_SECTIONALIZER
--   * MV_RECLOSER
--
-- DELIBERATELY NARROW. It is NOT added to every switchable device: for an
-- ordinary in-line feeder switch "normally closed" is trivially true and carries
-- no information, so badging them all would just paint the network with dots
-- (the map-clutter problem re-created at symbol level) and bury the one marker
-- that matters. The NOP marker only draws for NORMALLY_OPEN (Slice 1), so the
-- default posture stays unmarked regardless. LV_SWITCH is left out on purpose
-- (this thread is the MV one-line); adding it later is a one-line change here.
--
-- Field is NULLABLE with NO subtype rules and NO default_override, matching
-- bus_coupler_fields.sql: this adds a column to ALREADY-COMPILED tables that may
-- hold live rows, so a NOT NULL column would fail to add. An unset normal_state
-- simply reads as "no declared open point" -- absence is harmless (nothing in
-- the engine consumes this field; it is display + a hook for later
-- switching-order / operational work). A per-subtype default so the create form
-- pre-fills NORMALLY_CLOSED is a deliberate later nicety, not done here to keep
-- this purely additive.
--
-- IMPORTANT -- declared, not live. normal_state is the DESIGN posture. It is not
-- the live device_state (a "later nicety" the coupler seed left undone) nor
-- powerflow energization. A NORMALLY_CLOSED selector can legitimately be sitting
-- open during abnormal switching; the marker means "normally open", never "open
-- right now". Live device_state is Phase 48 Slice 3.
--
-- Depends on seed/bus_coupler_fields.sql (the NORMAL_STATE domain + values, the
-- 'electrical' field group -- all re-asserted idempotently below so this seed is
-- self-sufficient regardless of order) and on the switch classes existing
-- (seed/mv_switching_example.sql for MV_ISOLATOR/MV_SECTIONALIZER,
-- seed/mv_lv_switch_example.sql for MV_SWITCH/MV_RECLOSER).
--
-- Apply with psql, then recompile each touched class:
--   task seed-switch-normal-state   (or the psql -f invocation directly)
--   task compile CODE=MV_ISOLATOR
--   task compile CODE=MV_SWITCH
--   task compile CODE=MV_SECTIONALIZER
--   task compile CODE=MV_RECLOSER
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + the `task compile` runs + a
-- create/edit round-trip of the new field + a diagram render check (one bay with
-- one selector NORMALLY_OPEN, one NORMALLY_CLOSED) are still owed to call it
-- verified.

-- Re-assert the field group + domain + values idempotently (owned by
-- bus_coupler_fields.sql) so this seed stands alone regardless of apply order.
INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('NORMAL_STATE', 'Normal state', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('NORMAL_STATE', 'NORMALLY_CLOSED', 'Normally closed', 1),
    ('NORMAL_STATE', 'NORMALLY_OPEN',   'Normally open',   2)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- The narrow switch family (see header). Nullable, no subtype rules -- an unset
-- normal_state reads as "no declared open point". Set-generating over the four
-- classes; a class that isn't seeded yet is simply skipped (the join yields no
-- row), same tolerance the busbar containment seed uses.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'normal_state', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d ON d.code = 'NORMAL_STATE'
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code IN ('MV_ISOLATOR', 'MV_SWITCH', 'MV_SECTIONALIZER', 'MV_RECLOSER')
ON CONFLICT (feature_class_id, name) DO NOTHING;
