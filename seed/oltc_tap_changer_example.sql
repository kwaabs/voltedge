-- seed/oltc_tap_changer_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this lives under
-- /seed on purpose, same reasoning as the other seed files.
--
-- THE TAP CHANGER / AVR AS A DISCRETE DEVICE. The substation-modelling review
-- graded the tap changer PARTIAL: the transformer's tap is modelled, but only as
-- nameplate fields on POWER_XFMR (tap_position / tap_step_pct / tap_min / tap_max,
-- seed/power_transformer_example.sql) -- there was no discrete on-load tap changer
-- / automatic voltage regulator (AVR) OBJECT with its own control settings and its
-- own maintained-asset identity. This seed adds that as ONE new class:
--
--   * TAP_CHANGER -- the transformer's tap-changing mechanism together with its
--     AVR controller. It carries the CONTROL intent (target voltage, bandwidth /
--     deadband, time delay, line-drop compensation R/X, auto/manual mode) and the
--     ASSET/maintenance identity (operation counter, condition) that a tap changer
--     has as a real, separately-maintained piece of plant (contacts, oil,
--     diverter, motor drive) -- none of which belongs on the transformer nameplate.
--
-- WHY THIS SHAPE (and why NOT a network junction like MV_VREG):
--
--   * The LIVE ELECTRICAL TAP STAYS ON POWER_XFMR. The field-name-driven load flow
--     already reads tap_position + tap_step_pct off the transformer and applies
--     1 + tap*step/100 to the LV turns ratio (api/internal/powerflow --
--     resolveTransformerModel -> tapFactor). Those fields are the live electrical
--     state the sweep consumes, and they are LEFT WHERE THEY ARE: moving them onto
--     this device would demand an engine change and create two writers for one
--     value. This seed therefore needs ZERO engine change -- the transformer keeps
--     doing the physics; TAP_CHANGER is the controller and the asset that drives it.
--
--   * geometry_type = 'none' + network_role = 'none'. An OLTC lives INSIDE the
--     transformer tank -- it has no independent map location and it is NOT a series
--     node on the connectivity graph. Making it a series junction (as MV_VREG, a
--     standalone line regulator, legitimately is) would DOUBLE-COUNT the ratio the
--     transformer already applies. So it takes the P&C-DC-layer stance exactly
--     (seed/pnc_control_devices_example.sql): a contained, non-conducting control
--     device whose only visual form is a diagram placement. The AVR "senses the bus
--     VT and commands the tap" link is control wiring (annotation), the same larger
--     open item the relays/DC layer defer (docs/open-items.md "Dual line semantics").
--
--   * COMPOSITE, not containment. A tap changer is PART OF its transformer, so the
--     metadata pairing is kind 'composite' POWER_XFMR -> TAP_CHANGER (one_to_one --
--     one gang mechanism / AVR per transformer), distinct from the SUBSTATION
--     'containment' rows. CIM reads this so a RatioTapChanger nests on its
--     PowerTransformer (foot of file).
--
-- MODELLING NOTES (recorded so they're not silent):
--
--   1. Single subtype 'standard'. On-load (OLTC) vs off-circuit (DETC) is a field
--     (tap_changer_type), not a subtype axis -- it changes no terminals and nothing
--     the network sees; "a subtype is for differences the network can see,
--     everything else is a field" (docs/substation-guide.md 2.x). Same stance the
--     relay's ANSI device_number and the IED's ied_role take.
--
--   2. All fields nullable at the DB level; requiredness is enforced at the app
--     layer via subtype_field_rules (the additive-compile rule: the compiler emits
--     NOT NULL for nullable=false, which would fail on a populated table --
--     [[compiler-additive-nullable-required]]). The identifying coded fields
--     (tap_changer_type, control_mode) and condition_rating are required; the AVR
--     setpoints are optional so an un-surveyed / manual unit degrades gracefully.
--
--   3. No terminals, no network_rules -- network_role 'none' carries no power path.
--
-- SYMBOLOGY: a render_context = 'diagram' rule only. A geometry 'none' class has NO
-- map rule to fall back to (diagram-canvas.tsx tries 'diagram' then 'map'), so
-- without it the device would render with no symbol. icon_ref 'circle' in a
-- distinct purple; a dedicated tap-changer glyph (an arrow through a winding) is a
-- nice-to-have follow-up, same convention as one_line_stencils_extra.sql.
--
-- CIM/CDPSM (migration 0034): TAP_CHANGER -> RatioTapChanger, cim_kind 'asset'
-- (attached equipment on the PowerTransformer, not a conducting node -- the step /
-- stepVoltageIncrement themselves come from POWER_XFMR's tap fields). Its control
-- setpoints map to the TapChangerControl attributes. Guarded, so absent the cim_*
-- tables the rows just skip.
--
-- Depends on: the core metadata migrations, plus seed/power_transformer_example.sql
-- (POWER_XFMR) for the composite pairing -- absent, that JOIN simply skips. The
-- domains + field_groups are re-asserted idempotently so this seed stands alone.
--
-- Apply, then compile the new class so its gis table lands:
--   task seed-oltc-tap-changer
--   task compile CODE=TAP_CHANGER
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL was
-- reviewed statically -- a live psql apply + `task compile CODE=TAP_CHANGER` + a
-- create spot-check (a TAP_CHANGER composite-linked to a POWER_XFMR) are still owed
-- to call it verified.

-- ===========================================================================
-- Field groups + domains
-- ===========================================================================

INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('electrical',  'Electrical',           1),
    ('maintenance', 'Maintenance',          2),
    ('tap_control', 'Tap Changer Control',  42)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('TAP_CHANGER_TYPE', 'Tap changer type',      'coded'),
    ('TAP_CONTROL_MODE', 'Tap control mode',      'coded'),
    ('CONDITION_RATING', 'Condition rating',      'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('TAP_CHANGER_TYPE', 'on_load',     'On-load (OLTC)',       1),
    ('TAP_CHANGER_TYPE', 'off_circuit', 'Off-circuit (DETC)',   2),
    ('TAP_CONTROL_MODE', 'auto',        'Automatic (AVR)',      1),
    ('TAP_CONTROL_MODE', 'manual',      'Manual',               2),
    ('TAP_CONTROL_MODE', 'off',         'Off / locked',         3),
    ('CONDITION_RATING', 'GOOD',        'Good',                 1),
    ('CONDITION_RATING', 'FAIR',        'Fair',                 2),
    ('CONDITION_RATING', 'POOR',        'Poor',                 3)
) AS v(domain_code, code, label, sort_order) ON v.domain_code = d.code
ON CONFLICT (domain_id, code) DO NOTHING;

-- ===========================================================================
-- Feature class + subtype
-- ===========================================================================

INSERT INTO metadata.feature_classes (code, name, geometry_type, srid, network_role, status, is_diagram_container, is_diagram_placeable)
VALUES ('TAP_CHANGER', 'Transformer Tap Changer', 'none', NULL, 'none', 'draft', false, true)
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.subtypes (feature_class_id, code, name)
SELECT fc.id, 1, 'standard'
FROM metadata.feature_classes fc
WHERE fc.code = 'TAP_CHANGER'
ON CONFLICT (feature_class_id, code) DO NOTHING;

-- ===========================================================================
-- Fields (all nullable at DB level; requiredness via subtype_field_rules below)
--   tap_changer_type  -- OLTC vs off-circuit, the identifying field.
--   control_mode      -- auto (AVR in service) / manual / off.
--   target_voltage_kv -- the AVR setpoint it regulates the controlled bus toward.
--   bandwidth_pct     -- the deadband either side of target before it taps.
--   time_delay_s      -- intentional delay before an out-of-band excursion taps.
--   ldc_r_ohm/ldc_x_ohm -- line-drop compensation, so it regulates a remote point.
--   operation_count   -- lifetime tap operations (a maintenance/wear driver).
--   condition_rating  -- maintenance state, as every physical asset.
-- The live electrical tap itself (tap_position / tap_step_pct / tap_min / tap_max)
-- is NOT duplicated here -- it stays on POWER_XFMR where the sweep reads it.
-- ===========================================================================

INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, v.name, v.data_type, d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN (VALUES
    ('tap_changer_type',  'text',    'TAP_CHANGER_TYPE', 'electrical'),
    ('control_mode',      'text',    'TAP_CONTROL_MODE', 'tap_control'),
    ('target_voltage_kv', 'numeric', NULL,               'tap_control'),
    ('bandwidth_pct',     'numeric', NULL,               'tap_control'),
    ('time_delay_s',      'numeric', NULL,               'tap_control'),
    ('ldc_r_ohm',         'numeric', NULL,               'tap_control'),
    ('ldc_x_ohm',         'numeric', NULL,               'tap_control'),
    ('operation_count',   'integer', NULL,               'maintenance'),
    ('condition_rating',  'text',    'CONDITION_RATING', 'maintenance')
) AS v(name, data_type, domain_code, group_code) ON true
LEFT JOIN metadata.domains d ON d.code = v.domain_code
JOIN metadata.field_groups fg ON fg.code = v.group_code
WHERE fc.code = 'TAP_CHANGER'
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- Requiredness: the two identifying coded fields + condition_rating are required;
-- the AVR setpoints stay optional (a manual or un-surveyed unit need not carry them).
INSERT INTO metadata.subtype_field_rules (subtype_id, field_id, is_required)
SELECT s.id, f.id, (f.name IN ('tap_changer_type', 'control_mode', 'condition_rating'))
FROM metadata.subtypes s
JOIN metadata.feature_classes fc ON fc.id = s.feature_class_id
JOIN metadata.fields f ON f.feature_class_id = fc.id
WHERE fc.code = 'TAP_CHANGER'
ON CONFLICT (subtype_id, field_id) DO NOTHING;

-- ===========================================================================
-- Diagram symbology (no map rule to fall back to -- geometry 'none')
-- ===========================================================================

INSERT INTO metadata.symbology_rules (feature_class_id, render_context, style_def)
SELECT fc.id, 'diagram', '{"icon_ref": "circle", "color": "#7e22ce"}'::jsonb
FROM metadata.feature_classes fc
WHERE fc.code = 'TAP_CHANGER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.symbology_rules
    WHERE feature_class_id = fc.id AND subtype_id IS NULL AND domain_value_id IS NULL AND render_context = 'diagram'
  );

-- ===========================================================================
-- Composite pairing -- a tap changer is PART OF its power transformer (kind
-- 'composite', one_to_one). Distinct from SUBSTATION 'containment'; CIM reads this
-- to nest the RatioTapChanger on its PowerTransformer. Guarded on (origin,
-- destination, kind); skipped if POWER_XFMR is absent.
-- ===========================================================================

INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'POWER_XFMR has TAP_CHANGER', xf.id, tc.id, 'composite', 'one_to_one'
FROM metadata.feature_classes xf, metadata.feature_classes tc
WHERE xf.code = 'POWER_XFMR' AND tc.code = 'TAP_CHANGER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes
    WHERE origin_feature_class_id = xf.id AND destination_feature_class_id = tc.id AND kind = 'composite'
  );

-- ===========================================================================
-- CIM/CDPSM export mapping (migration 0034) -- so TAP_CHANGER is NOT silently
-- dropped from a CIM export. A tap changer is a CIM RatioTapChanger; cim_kind
-- 'asset' (attached equipment on the PowerTransformer, not a conducting node --
-- the step / stepVoltageIncrement come from POWER_XFMR's own tap fields). Its
-- control setpoints map to TapChangerControl. Class-default row (subtype_id NULL),
-- guarded with NOT EXISTS because UNIQUE (feature_class_id, subtype_id) treats NULL
-- subtype_ids as DISTINCT so ON CONFLICT would never fire and would duplicate.
-- Depends on migration 0034 (cim_class_mappings); harmless to re-run.
-- ===========================================================================

INSERT INTO metadata.cim_class_mappings (feature_class_id, subtype_id, cim_class, cim_kind)
SELECT fc.id, NULL, 'RatioTapChanger', 'asset'
FROM metadata.feature_classes fc
WHERE fc.code = 'TAP_CHANGER'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.cim_class_mappings m
    WHERE m.feature_class_id = fc.id AND m.subtype_id IS NULL
  );

INSERT INTO metadata.cim_attribute_mappings (field_id, cim_attribute, transform, unit)
SELECT f.id, v.cim_attribute, 'direct', v.unit
FROM metadata.fields f
JOIN metadata.feature_classes fc ON fc.id = f.feature_class_id
JOIN (VALUES
    ('target_voltage_kv', 'TapChangerControl.targetValue',    'kV'),
    ('bandwidth_pct',     'TapChangerControl.targetDeadband', 'percent')
) AS v(field_name, cim_attribute, unit) ON v.field_name = f.name
WHERE fc.code = 'TAP_CHANGER'
ON CONFLICT (field_id, cim_attribute) DO NOTHING;
