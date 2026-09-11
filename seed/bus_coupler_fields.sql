-- seed/bus_coupler_fields.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
--
-- Bus coupler / bus tie -- Slice 1 (the role + normal-state fields).
--
-- A bus coupler is NOT a new feature class here. Electrically it IS a breaker
-- (a switchable tie between two MV_BUSBAR instances), so it is modelled as an
-- ordinary MV_BREAKER wired bus <-> bus through the existing busbar network
-- rules C and D (seed/mv_busbar_example.sql) -- which means every engine that
-- already understands a breaker (trace barriers, isolation, load flow, fault,
-- protection coordination) handles a coupler for free, with no new class,
-- terminals, network_rules, or engine code. Giving it its own class would only
-- duplicate MV_BREAKER and fragment the switch family.
--
-- The genuinely missing thing is SEMANTICS: a bare breaker cannot say "I am the
-- coupler" or "I am a normally-open point". This seed adds exactly two coded
-- fields to MV_BREAKER to carry that, the same call BAY_ROLE and
-- PROTECTIVE_RELAY.device_number already made (a functional variant is a FIELD,
-- not a subtype or a class):
--
--   * switch_function -- what this breaker is FOR: FEEDER (the default, an
--     ordinary outgoing feeder breaker), INCOMER, BUS_COUPLER, BUS_SECTION,
--     TRANSFORMER. Deliberately the SAME value set as BAY_ROLE
--     (seed/bay_example.sql) so a coupler breaker and its coupler bay read
--     consistently. A section switch (splits one bus into two instances) is
--     just BUS_SECTION -- same topology, different label, so this one field
--     covers both coupler and section with no extra work.
--   * normal_state -- the coupler's OPERATIONAL default, distinct from its live
--     device_state: NORMALLY_CLOSED (default posture) or NORMALLY_OPEN (the
--     classic distribution "open point"). This is the hook the later
--     switching-order / operational-state / reliability work will read; a live
--     device_state=OPEN on a NORMALLY_CLOSED tie is an abnormal switching state,
--     which is a different fact from a designed open point.
--
-- Both fields are NULLABLE and carry NO subtype rules on purpose, matching
-- seed/load_flow_fields.sql: this seed adds columns to an ALREADY-COMPILED
-- MV_BREAKER table that may hold live rows, so a NOT NULL column would fail to
-- add. An unset switch_function simply reads as an ordinary (feeder) breaker --
-- nothing consumes these fields in the engine, so absence is harmless. A
-- per-subtype default_override (so the create form pre-fills FEEDER /
-- NORMALLY_CLOSED) is a deliberate later nicety, not done here to keep Slice 1
-- purely additive. Slice 2 (the POST /couplers placement composite) sets both
-- values explicitly at creation, so it does not depend on a default either.
--
-- Symbology keying a distinct coupler glyph off switch_function's
-- domain_value_id is also deferred -- the breaker already symbolizes; a
-- role-specific glyph is a Slice 3 concern, exactly as BAY deferred its
-- role glyphs.
--
-- Depends on seed/mv_switching_example.sql (MV_BREAKER, and the 'electrical'
-- field group, both re-asserted idempotently so this seed is self-sufficient
-- regardless of order).
--
-- Apply with psql, then recompile the touched class:
--   task seed-bus-coupler-fields   (or the psql -f invocation directly)
--   task compile CODE=MV_BREAKER
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + the `task compile` run + a
-- create/edit round-trip of the two new fields are still owed to call it
-- verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1)
ON CONFLICT (code) DO NOTHING;

-- SWITCH_FUNCTION mirrors BAY_ROLE's value set (minus BAY_ROLE's own reuse) so
-- a coupler breaker and a coupler bay agree; NORMAL_STATE is new here.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('SWITCH_FUNCTION', 'Switch function', 'coded'),
    ('NORMAL_STATE', 'Normal state', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('SWITCH_FUNCTION', 'FEEDER',      'Outgoing feeder', 1),
    ('SWITCH_FUNCTION', 'INCOMER',     'Incomer',         2),
    ('SWITCH_FUNCTION', 'BUS_COUPLER', 'Bus coupler',     3),
    ('SWITCH_FUNCTION', 'BUS_SECTION', 'Bus section',     4),
    ('SWITCH_FUNCTION', 'TRANSFORMER', 'Transformer',     5),
    ('NORMAL_STATE',    'NORMALLY_CLOSED', 'Normally closed', 1),
    ('NORMAL_STATE',    'NORMALLY_OPEN',   'Normally open',   2)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- MV_BREAKER: the coupler-role + normal-state fields. Nullable, no subtype
-- rules (see header) -- an unset switch_function reads as an ordinary feeder
-- breaker.
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN (VALUES
    ('switch_function', 'text', 'SWITCH_FUNCTION'),
    ('normal_state',    'text', 'NORMAL_STATE')
) AS v(name, data_type, domain_code) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'MV_BREAKER'
ON CONFLICT (feature_class_id, name) DO NOTHING;
