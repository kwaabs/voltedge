-- seed/mv_relay_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as the other seed files.
-- Phase (protection), Slice A. Promotes PROTECTIVE_RELAY from a bare
-- diagram glyph (seed/protective_relay_example.sql) into a device with
-- identity AND the "what protects what" wiring:
--
--   * Two more identity fields on PROTECTIVE_RELAY -- `model` and
--     `condition_rating`. Both NULLABLE on purpose: the compiler is
--     additive-only, and adding a NOT NULL column to a class that already
--     has rows fails (see docs/phase-27-mv-network.md's "adding a required
--     field" note). Identity is optional metadata anyway; the required
--     functional axis is already the existing `device_number` field.
--
--   * A `protection_trip` relationship class per switching device the relay
--     can operate (MV_BREAKER, DIST_SWITCH), and a `protection_sensing`
--     relationship class per instrument transformer that feeds it (MV_CT,
--     MV_VT). These are metadata.relationship_classes rows -- the SAME
--     machinery POLE-supports-MV_CONDUCTOR uses (seed/relationship_example.sql),
--     NOT network_rules: a relay's trip/sense wiring is a documented
--     association between features, never an electrically traced hop. Phase
--     2's feature_relationships records the specific established instances;
--     these rows only declare the pairings legal.
--
-- Cardinality is many_to_many throughout: a relay can trip several breakers
-- (a bus lockout) and a breaker can be tripped by several relays (main +
-- backup); a relay reads several instrument transformers and a CT/VT can
-- feed several relays. one_to_many would wrongly cap either side.
--
-- Depends on:
--   * seed/protective_relay_example.sql  (PROTECTIVE_RELAY exists)
--   * migration 0075                     (relationship kind CHECK widened to
--                                          admit protection_trip/_sensing)
--   * seed/mv_switching_example.sql      (MV_BREAKER; DIST_SWITCH from
--                                          seed/switching_device_example.sql)
--   * seed/mv_measuring_example.sql      (MV_CT, MV_VT)
-- Any destination class that isn't present simply seeds zero rows for it
-- (the FROM ... WHERE finds nothing) -- graceful, like Phase 27's wiring.
--
-- Apply with psql, then recompile the relay so the two new columns land:
--   task seed-mv-relay
--   task compile CODE=PROTECTIVE_RELAY
-- (relationship_classes has no physical footprint -- nothing to compile for
-- the trip/sensing rows; they're read directly by api/internal/relationship.)
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL
-- has been reviewed statically -- a live psql apply + `task compile
-- CODE=PROTECTIVE_RELAY` + a relationship establish check are still owed to
-- call it verified.

-- ---------------------------------------------------------------------------
-- Extra identity fields on PROTECTIVE_RELAY (both nullable -- see header).
-- The `protection` field group and CONDITION_RATING domain already exist
-- (protective_relay_example.sql / the MV slices); re-asserted idempotently.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('protection', 'Protection', 1),
    ('maintenance', 'Maintenance', 2)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('CONDITION_RATING', 'Condition rating', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('model', 'text', NULL, 'protection', true),
    ('condition_rating', 'text', 'CONDITION_RATING', 'maintenance', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'PROTECTIVE_RELAY'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- Attach the two optional fields to the single 'standard' subtype as
-- not-required (is_required = false), matching their nullable data columns.
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, false
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'PROTECTIVE_RELAY')
  AND f.name IN ('model', 'condition_rating')
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- ---------------------------------------------------------------------------
-- protection_trip -- PROTECTIVE_RELAY operates a switching device. One
-- relationship class per destination that exists (MV_BREAKER, DIST_SWITCH),
-- authored as a set-generating INSERT. UNIQUE name per pairing.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'PROTECTIVE_RELAY trips ' || dev.code, relay.id, dev.id, 'protection_trip', 'many_to_many'
FROM metadata.feature_classes relay
JOIN metadata.feature_classes dev ON dev.code IN ('MV_BREAKER', 'DIST_SWITCH')
WHERE relay.code = 'PROTECTIVE_RELAY'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = relay.id AND destination_feature_class_id = dev.id AND kind = 'protection_trip'
  );

-- ---------------------------------------------------------------------------
-- protection_sensing -- PROTECTIVE_RELAY is fed by an instrument
-- transformer. One class per destination that exists (MV_CT, MV_VT).
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'PROTECTIVE_RELAY senses ' || it.code, relay.id, it.id, 'protection_sensing', 'many_to_many'
FROM metadata.feature_classes relay
JOIN metadata.feature_classes it ON it.code IN ('MV_CT', 'MV_VT')
WHERE relay.code = 'PROTECTIVE_RELAY'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = relay.id AND destination_feature_class_id = it.id AND kind = 'protection_sensing'
  );
