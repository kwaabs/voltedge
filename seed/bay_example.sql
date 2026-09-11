-- seed/bay_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives
-- under /seed on purpose, same reasoning as seed/protective_relay_example.sql
-- and the other seed files.
--
-- Phase 34 Slice 1 -- the BAY feature class: a substation "bay" (a.k.a. the
-- control-building panel for one circuit -- incomer, feeder, bus coupler,
-- transformer). A bay is the intermediate grouping between SUBSTATION and
-- its plant: it CONTAINS a circuit's breaker/isolators/CTs/VTs/fuses/relay
-- (wired in Slice 2, seed/bay_containment_example.sql) and is itself contained
-- by the yard. That two-level containment (yard -> bay -> device) is new to
-- this repo; containment today is flat (yard -> device). See
-- docs/phase-34-substation-bays.md.
--
-- Shape decisions, all grounded in existing classes:
--   * geometry_type = 'none' -- a bay has no map footprint; it exists only as
--     a logical grouping placed on a diagram, exactly like PROTECTIVE_RELAY
--     (seed/protective_relay_example.sql). The compiler skips the geom +
--     territory_id columns for 'none' (api/internal/compiler/ddl.go
--     buildCreateTable) and never reads network_role, so 'none' + 'container'
--     compiles cleanly.
--   * network_role = 'container' -- the bay exists to contain other features,
--     the same honest label METER_CHAMBER carries
--     (seed/lv_metering_example.sql). It is not a traced network node and has
--     no terminals/network_rules.
--   * one 'standard' subtype -- the functional variant (incomer vs feeder vs
--     coupler) is the bay_role FIELD, not a subtype, the same call
--     PROTECTIVE_RELAY made with device_number. Symbology can still key on
--     bay_role's domain_value_id for role-specific glyphs (Slice 3).
--
-- Apply with psql after migrations 0001-0019, then compile:
--   DATABASE_URL=... go run ./cmd/compile BAY

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('bay', 'Bay', 1)
ON CONFLICT (code) DO NOTHING;

-- BAY_ROLE is new here. VOLTAGE_CLASS and CONDITION_RATING already exist
-- (seed/connectivity_example.sql, seed/substation_yard_example.sql); they are
-- re-asserted defensively so this file is self-contained and order-independent
-- against the other seeds, same pattern substation_yard_example.sql uses for
-- CONDITION_RATING.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('BAY_ROLE', 'Bay role', 'coded'),
    ('VOLTAGE_CLASS', 'Voltage class', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('BAY_ROLE', 'INCOMER', 'Incomer', 1),
    ('BAY_ROLE', 'FEEDER', 'Outgoing feeder', 2),
    ('BAY_ROLE', 'BUS_COUPLER', 'Bus coupler', 3),
    ('BAY_ROLE', 'BUS_SECTION', 'Bus section', 4),
    ('BAY_ROLE', 'TRANSFORMER', 'Transformer', 5),
    ('VOLTAGE_CLASS', '11KV', '11 kV', 1),
    ('VOLTAGE_CLASS', '33KV', '33 kV', 2),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('BAY', 'Bay', 'none', NULL, 'container', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, 1, 'standard'
FROM metadata.feature_classes fc
WHERE fc.code = 'BAY'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- bay_ref is the asset/panel tag (nullable -- a bay may be unlabelled),
-- matching METER_CHAMBER.chamber_ref. bay_role + voltage_class are the two
-- required identifiers of the circuit; condition_rating is optional here (a
-- logical grouping doesn't always carry its own condition).
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('bay_role', 'text', 'BAY_ROLE', 'bay', false),
    ('voltage_class', 'text', 'VOLTAGE_CLASS', 'bay', false),
    ('bay_ref', 'text', NULL, 'bay', true),
    ('condition_rating', 'text', 'CONDITION_RATING', 'bay', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'BAY'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'BAY')
  AND f.name IN ('bay_role', 'voltage_class')
ON CONFLICT (subtype_id, field_id) DO NOTHING;
