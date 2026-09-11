-- seed/earthing_transformer_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files.
-- EARTHING_XFMR: an earthing (grounding) transformer -- a substation device that
-- provides a neutral/ground reference on an otherwise ungrounded (delta) MV
-- system, giving zero-sequence (ground-fault) current a path to earth. Sits in
-- the "realm of power transformers" alongside POWER_XFMR
-- (seed/power_transformer_example.sql) but plays a very different electrical
-- role: it does NOT transform voltage between two networks and carries no
-- through-load in normal balanced operation -- it is a shunt device that only
-- carries current during a ground fault.
--
-- WHY IT NEEDS NO POWER-FLOW ENGINE CHANGE: api/internal/powerflow is
-- field-name-driven. resolveTransformerModel needs BOTH primary_voltage AND
-- secondary_voltage to treat a node as a voltage-transforming transformer;
-- EARTHING_XFMR carries primary_voltage (the system voltage it earths) but NO
-- secondary_voltage, so the sweep resolves it as a plain pass-through leaf --
-- exactly right: an earthing transformer is a dead-end shunt off the busbar with
-- no downstream network and no real through-power. It has no rated_kva/rated_mva
-- either, so it reports no loading %. Nothing to add to the engine for this to
-- be handled correctly.
--
-- ELECTRICAL DATA NOW DRIVES THE GROUND-FAULT MODEL (ground-fault Slice 2): the
-- single-line-to-ground path in api/internal/powerflow (groundfault.go) reads
-- z0_ohm as this unit's zero-sequence shunt to earth and folds 3× any integral
-- neutral_resistance_ohm into it, so an earthing transformer on an otherwise-
-- ungrounded (delta) bus turns that bus grounded and bounds the SLG current there
-- (and on its downstream cone), in parallel with any transformer-neutral path.
-- The engine identifies the unit by the presence of a positive z0_ohm, not by
-- class -- the same generic-field-name approach the rest of powerflow uses. This
-- is the "record the field first (POWER_XFMR staging), key an engine off it
-- later" plan paying off: the survey columns landed a pass earlier, unchanged.
-- rated_neutral_current_a / rated_time_s stay record-only (thermal withstand,
-- not part of the impedance network).
--
-- WHY A CODED connection_type, NOT A SUBTYPE (unlike POWER_XFMR's winding
-- count): the two grounding constructions (zig-zag / interconnected-star, and
-- grounded-wye-delta) present the SAME connection points to the network -- a
-- three-phase line terminal plus a neutral/earth terminal (a wye-delta bank's
-- delta is closed internally, like a stabilizing winding, not brought out). No
-- terminal or behavioural difference means a subtype would be over-modelling;
-- the construction is a plain coded attribute instead. So EARTHING_XFMR has NO
-- subtypes and its required fields are enforced by nullable = false directly.
--
-- Depends on seed/connectivity_example.sql (MV_CONDUCTOR + VOLTAGE_CLASS /
-- CONDITION_RATING domains). Reuses the 'electrical' and 'maintenance'
-- field_groups (re-asserted idempotently so this seed stands alone) and adds a
-- GROUNDING_TYPE domain.
--
-- Apply with psql after seed/connectivity_example.sql, then compile the class:
--   task seed-earthing-transformer   (or the psql -f invocation directly)
--   task compile CODE=EARTHING_XFMR
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL has
-- been reviewed statically -- a live psql apply + the compile + a trace/load-flow
-- re-check across an EARTHING_XFMR are still owed to call it verified.

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical', 'Electrical', 1),
    ('maintenance', 'Maintenance', 2)
ON CONFLICT (code) DO NOTHING;

-- Domains: reuse VOLTAGE_CLASS + CONDITION_RATING, add GROUNDING_TYPE.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('VOLTAGE_CLASS', 'Voltage class', 'coded'),
    ('CONDITION_RATING', 'Condition rating', 'coded'),
    ('GROUNDING_TYPE', 'Earthing transformer connection', 'coded')
ON CONFLICT (code) DO NOTHING;

-- The two standard grounding-transformer constructions. Values only -- the
-- engine reads none of them (record-only classification); they exist so the
-- connection_type field is selectable and the map/diagram can style by it later.
INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('VOLTAGE_CLASS',    '66KV',  '66 kV',  3),
    ('VOLTAGE_CLASS',    '132KV', '132 kV', 4),
    ('CONDITION_RATING', 'GOOD', 'Good', 1),
    ('CONDITION_RATING', 'FAIR', 'Fair', 2),
    ('CONDITION_RATING', 'POOR', 'Poor', 3),
    ('GROUNDING_TYPE', 'ZIGZAG',    'Zig-zag (interconnected star)', 1),
    ('GROUNDING_TYPE', 'WYE_DELTA', 'Grounded wye-delta',            2)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- EARTHING_XFMR -- junction. geometry_optional + is_diagram_placeable (same
-- reasoning as POWER_XFMR / the MV plant classes: a substation earthing
-- transformer may be placed geographically, on a one-line diagram, or both).
-- No subtypes (see header) -- the construction is a coded field.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable, geometry_optional)
VALUES ('EARTHING_XFMR', 'Earthing Transformer', 'point', 4326, 'junction', 'draft', false, true, true)
ON CONFLICT (code) DO NOTHING;

-- Fields. primary_voltage (system voltage it earths) and connection_type
-- (construction) and condition_rating are required (nullable = false); the
-- zero-sequence electrical data is nullable so an un-surveyed unit degrades
-- gracefully (no z0_ohm → the ground-fault model simply finds no earth path here,
-- as if the unit weren't there). Crucially there is NO secondary_voltage -- its
-- absence is what keeps the power-flow engine treating this as a pass-through
-- shunt, not a voltage-transforming transformer.
--   Identity / classification:
--     primary_voltage    coded VOLTAGE_CLASS   -- system nominal it earths (req)
--     connection_type    coded GROUNDING_TYPE  -- zig-zag / wye-delta (req)
--   Zero-sequence (drives the ground-fault SLG model, groundfault.go):
--     z0_ohm                   numeric  -- zero-sequence impedance per phase, ohm
--     rated_neutral_current_a  numeric  -- short-time neutral/earth current rating, A
--     rated_time_s             numeric  -- short-time duration for that rating, s
--     neutral_resistance_ohm   numeric  -- integral neutral earthing resistor, if any
--   Maintenance:
--     condition_rating   coded CONDITION_RATING  (req)
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, v.nullable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('primary_voltage',         'text',    'VOLTAGE_CLASS',    'electrical',  false),
    ('connection_type',         'text',    'GROUNDING_TYPE',   'electrical',  false),
    ('z0_ohm',                  'numeric', NULL,               'electrical',  true),
    ('rated_neutral_current_a', 'numeric', NULL,               'electrical',  true),
    ('rated_time_s',            'numeric', NULL,               'electrical',  true),
    ('neutral_resistance_ohm',  'numeric', NULL,               'electrical',  true),
    ('condition_rating',        'text',    'CONDITION_RATING', 'maintenance', false)
) AS v(name, data_type, domain_code, group_code, nullable) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'EARTHING_XFMR'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- Terminals -- NOT subtype-scoped (the class has no subtypes), so subtype_id is
-- NULL, the historic default. LINE is the three-phase connection onto the
-- system; N is the neutral/earth point (a ground terminal, not phase-capable).
INSERT INTO metadata.terminals (feature_class_id, code, role, phase_capable)
SELECT fc.id, v.code, v.role, v.phase_capable
FROM metadata.feature_classes fc
JOIN (VALUES
    ('LINE', 'primary', true),
    ('N',    'ground',  false)
) AS v(code, role, phase_capable) ON true
WHERE fc.code = 'EARTHING_XFMR'
ON CONFLICT (feature_class_id, subtype_id, code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- network_rules -- an earthing transformer taps onto the MV busbar/feeder at its
-- LINE terminal and goes no further (its N connects to the earth grid, not a
-- traceable network element). So there is exactly ONE rule, inbound only:
--   MV_CONDUCTOR -> EARTHING_XFMR LINE   (voltage-matched)
-- With no outgoing rule the device is a dead-end shunt: a DOWNSTREAM trace or a
-- load flow reaching it descends into it as a leaf that contributes nothing,
-- which is exactly its electrical behaviour. No unique key on network_rules, so
-- guarded WHERE NOT EXISTS like every other seed.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.network_rules (name, from_feature_class_id, to_feature_class_id, to_terminal_id, phase_rule, is_valid)
SELECT 'MV_CONDUCTOR -> EARTHING_XFMR LINE', mv.id, ex.id, t.id,
       'from.voltage_class = to.primary_voltage', true
FROM metadata.feature_classes mv
JOIN metadata.feature_classes ex ON ex.code = 'EARTHING_XFMR'
JOIN metadata.terminals t ON t.feature_class_id = ex.id AND t.code = 'LINE'
WHERE mv.code = 'MV_CONDUCTOR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.network_rules
    WHERE from_feature_class_id = mv.id AND to_feature_class_id = ex.id AND to_terminal_id = t.id
  );

-- ---------------------------------------------------------------------------
-- Symbology -- the mandatory default map rule (docs/metadata-core.md: every
-- class needs one subtype/domain-null rule before publish). A square like the
-- other transformer classes, but a distinct amber colour so the earthing
-- transformer reads apart from POWER_XFMR (teal) and DIST_XFMR (blue). The ANSI
-- earthing-transformer one-line stencil (diagram render_context) is a follow-up,
-- not part of this pass -- until then a placed EARTHING_XFMR uses the default
-- diagram node.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'map', '{"icon_ref": "square", "color": "#b45309"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'EARTHING_XFMR'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'map'
  );
