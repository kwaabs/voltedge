-- seed/street_light_supply_mode.sql
-- Solar / off-grid street lights. A streetlight can be grid-fed, or self-powered
-- by an integral PV panel + battery (standalone), or grid-connected with PV backup
-- (hybrid). The utility owns and maintains all of them (ownership — utility vs
-- private — is the orthogonal ownership axis, seed/ownership_axis.sql), but a
-- SOLAR_STANDALONE lamp is NOT part of the connected electrical grid: it's a
-- self-contained island (source + load co-located), with no LV feed.
--
-- One metadata lever drives the whole treatment: a coded supply_mode field on
-- STREET_LIGHT. It governs the auto-wire (api/internal/streetlight/attach.go skips
-- wiring a SOLAR_STANDALONE lamp), and because the lamp then has NO connectivity
-- edge, the electrical engines exclude it for free — feeder/supply traces never
-- reach it, OMS never marks it "affected", load-flow counts no grid load — with no
-- engine changes (the engines-stay-electrical decision paying off). The lamp still
-- lives fully in the ASSET network: register, WAM, ownership, "On this pole".
--
-- Metadata-only. Apply, then recompile so the physical column exists (the compiler
-- ADDs the nullable column; existing rows read as no-value → grid):
--   task seed-street-light-supply-mode
--   task compile CODE=STREET_LIGHT
-- Idempotent (ON CONFLICT DO NOTHING). supply_mode is nullable: a null/absent value
-- is treated as grid everywhere (the create/attach form defaults the picker to GRID).

-- ---------------------------------------------------------------------------
-- The coded supply-mode domain.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('STREET_LIGHT_SUPPLY_MODE', 'Street light supply mode', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('STREET_LIGHT_SUPPLY_MODE', 'GRID',             'Grid-fed',                       1),
    ('STREET_LIGHT_SUPPLY_MODE', 'SOLAR_STANDALONE', 'Solar — standalone (off-grid)',  2),
    ('STREET_LIGHT_SUPPLY_MODE', 'SOLAR_HYBRID',     'Solar — hybrid (grid + PV)',     3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- The supply_mode field on STREET_LIGHT. Nullable (null → grid), in the
-- 'electrical' group STREET_LIGHT's other load fields already use.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'supply_mode', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d       ON d.code  = 'STREET_LIGHT_SUPPLY_MODE'
JOIN metadata.field_groups fg ON fg.code = 'electrical'
WHERE fc.code = 'STREET_LIGHT'
ON CONFLICT (feature_class_id, name) DO NOTHING;

DO $$
BEGIN
    RAISE NOTICE 'street_light_supply_mode.sql applied: + STREET_LIGHT_SUPPLY_MODE domain, + supply_mode field on STREET_LIGHT. RECOMPILE: task compile CODE=STREET_LIGHT (adds the nullable column).';
END $$;
