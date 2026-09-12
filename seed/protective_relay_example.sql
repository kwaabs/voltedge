-- seed/protective_relay_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this
-- lives under /seed on purpose, same reasoning as the other seed
-- files. The first real geometry_type = 'none' feature class -- a
-- feature whose only visual representation is ever a diagram
-- placement, never a point/line/polygon on the map. The compiler and
-- CRUD engine have supported this since Phase 1 (see
-- docs/phase-5-diagrams.md's "What was already there"), just never had
-- an actual class exercising the path until now.
--
-- Deliberately minimal: one subtype (the ANSI device number field
-- already captures the functional variant a subtype would otherwise
-- model), two fields, no terminals/network_rules -- protection-scheme
-- trip/control wiring is schematic-only annotation, not real
-- feature_connectivity, and modeling that is a separate, larger open
-- item (docs/open-items.md's "Dual line semantics").
--
-- Also seeds this class's own render_context = 'diagram' symbology
-- rule -- the first one actually authored anywhere in this project.
-- Every other class's diagram rendering has fallen back to its map
-- rule (diagram-canvas.tsx tries 'diagram' first, 'map' second); this
-- class has no map rule to fall back to at all, since it never
-- appears on the map.
--
-- Apply with psql after migrations 0001-0019, then compile:
--   DATABASE_URL=... go run ./cmd/compile PROTECTIVE_RELAY

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('protection', 'Protection', 1)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('ANSI_DEVICE_NUMBER', 'ANSI/IEEE device function number', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('27', '27 - Undervoltage', 1),
    ('59', '59 - Overvoltage', 2),
    ('50_51', '50/51 - Phase overcurrent', 3),
    ('50_51N', '50/51N - Ground overcurrent', 4),
    ('86', '86 - Lockout', 5),
    ('87', '87 - Differential', 6),
    -- Added 2026-09-11: common distribution/substation protection & control
    -- functions so a relay instance can declare its primary role. A modern
    -- numerical relay is multi-function; device_number captures its PRIMARY
    -- ANSI function (the single-value model is unchanged -- one row per relay).
    ('25', '25 - Synchronism check', 7),
    ('32', '32 - Directional power', 8),
    ('37', '37 - Undercurrent / underpower', 9),
    ('46', '46 - Negative-sequence / phase unbalance', 10),
    ('49', '49 - Thermal overload', 11),
    ('64', '64 - Earth-fault / restricted earth-fault', 12),
    ('67', '67 - Directional phase overcurrent', 13),
    ('67N', '67N - Directional ground overcurrent', 14),
    ('79', '79 - Auto-reclose', 15),
    ('81', '81 - Over/under frequency', 16)
) AS v(code, label, sort_order) ON true
WHERE d.code = 'ANSI_DEVICE_NUMBER'
ON CONFLICT (domain_id, code) DO NOTHING;

-- geometry_type = 'none' -- the compiler skips the geom column
-- entirely (api/internal/compiler/ddl.go's buildCreateTable), and the
-- CRUD engine rejects any create request that includes one
-- (api/internal/crud/validate.go). network_role = 'none': no
-- connectivity graph participation in this slice.

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container)
VALUES ('PROTECTIVE_RELAY', 'Protective Relay', 'none', NULL, 'none', 'draft', false)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, 1, 'standard'
FROM metadata.feature_classes fc
WHERE fc.code = 'PROTECTIVE_RELAY'
ON CONFLICT (feature_class_id, code) DO NOTHING;

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('device_number', 'text', 'ANSI_DEVICE_NUMBER', 'protection', false),
    ('manufacturer', 'text', NULL, 'protection', true)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'PROTECTIVE_RELAY'
ON CONFLICT (feature_class_id, name) DO NOTHING;

INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, true
FROM metadata.subtypes s
JOIN metadata.fields f ON f.feature_class_id = s.feature_class_id
WHERE s.feature_class_id = (SELECT id FROM metadata.feature_classes WHERE code = 'PROTECTIVE_RELAY')
  AND f.name = 'device_number'
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- render_context = 'diagram' -- see this file's own header comment.
-- Orange, distinct from every other seeded class's color
-- (seed/symbology_example.sql: #92400e, #2563eb, #7c3aed, #3f8fa6,
-- #64748b, #16a34a, #dc2626).

INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "circle", "color": "#ea580c"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'PROTECTIVE_RELAY'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );
