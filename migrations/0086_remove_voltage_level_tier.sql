-- 0086_remove_voltage_level_tier.sql
-- Purpose: Phase 47 minimal-model correction (docs/phase-47-substation-site-model.md)
-- -- retire the VOLTAGE_LEVEL feature class and collapse the two "dual legal parent"
-- containment idioms Phase 47 introduced, leaving a single-parent substation model:
--
--     SUBSTATION (polygon)
--      +- CONTROL_BUILDING (optional polygon, NOT a container)
--      +- SUBSTATION_PANEL   (single parent: SUBSTATION)
--      +- MV_BUSBAR          (single parent: SUBSTATION)
--      +- BAY                (single parent: SUBSTATION) -> plant
--
-- Why: the container tree has exactly one consumer -- CIM export -- and the trace/
-- power-flow/fault engines ride entirely on terminals + runtime.feature_connectivity,
-- never on containment. So modeling a VOLTAGE_LEVEL as its own object bought zero
-- analytical value while adding a whole class plus a two-legal-parents ambiguity
-- (bus/bay could hang off either the level or the substation). Voltage level is
-- DERIVABLE -- every piece of equipment already carries its own nominal voltage --
-- so the CIM export conveys voltage through each equipment's BaseVoltage reference
-- (unchanged since Phase 16), and the modeled BAY tier is kept as the one real
-- grouping. Net CIM shape: Substation -> Bay -> equipment, with BaseVoltage refs.
--
-- BAY stays (a bay is a genuine named operational grouping, not derivable), but now
-- with SUBSTATION as its sole parent -- the SUBSTATION contains BAY row was always
-- kept as the fallback, so dropping the VOLTAGE_LEVEL contains BAY edge just leaves
-- that sole parent standing. Same for MV_BUSBAR. And the CONTROL_BUILDING contains
-- SUBSTATION_PANEL edge is dropped so a panel's sole parent is SUBSTATION; the
-- building remains an optional map polygon, no longer a containment parent.
--
-- Idempotent + data-aware. Each destructive step is guarded so a re-run, or a DB
-- that never gained these Slice-3/Slice-1 rows, is a clean no-op. Runtime instance
-- rows (a placed voltage level, a panel actually filed under a building instance)
-- are handled explicitly rather than left to dangle or to block a metadata delete.
--
-- No compile step -- this only drops a class + relationship rows. (The dropped
-- gis.voltage_level table is a none-geometry class with no Martin tile function.)

DO $$
DECLARE
    vl_id   uuid;
    vl_pt   text;
    inst    bigint;
BEGIN
    -- ---------------------------------------------------------------------------
    -- 1) Drop the CONTROL_BUILDING contains SUBSTATION_PANEL dual-parent edge.
    --    Guarded on runtime instances: if a panel was actually filed under a
    --    building instance we must NOT orphan it, so we leave the edge for manual
    --    resolution and raise a NOTICE. On a normal DB (no such instance) the
    --    metadata row is deleted cleanly, leaving SUBSTATION contains
    --    SUBSTATION_PANEL as the panel's sole parent.
    -- ---------------------------------------------------------------------------
    IF EXISTS (
        SELECT 1
          FROM metadata.relationship_classes rc
          JOIN metadata.feature_classes o ON o.id = rc.origin_feature_class_id
          JOIN metadata.feature_classes d ON d.id = rc.destination_feature_class_id
          JOIN runtime.feature_relationships fr ON fr.relationship_class_id = rc.id
         WHERE rc.kind = 'containment'
           AND o.code = 'CONTROL_BUILDING' AND d.code = 'SUBSTATION_PANEL'
    ) THEN
        RAISE NOTICE 'migration 0086: CONTROL_BUILDING contains SUBSTATION_PANEL has live instances -- edge left in place, re-home those panels under SUBSTATION by hand then re-run';
    ELSE
        DELETE FROM metadata.relationship_classes rc
         USING metadata.feature_classes o, metadata.feature_classes d
         WHERE rc.origin_feature_class_id = o.id
           AND rc.destination_feature_class_id = d.id
           AND rc.kind = 'containment'
           AND o.code = 'CONTROL_BUILDING' AND d.code = 'SUBSTATION_PANEL';
    END IF;

    -- ---------------------------------------------------------------------------
    -- 2) Retire VOLTAGE_LEVEL. Nothing to migrate onto its children: a voltage
    --    level is pure grouping and every child (bus/bay/equipment) already carries
    --    its own nominal voltage, which the CIM export reads directly. So we unhook
    --    any instances and drop the class; the kept SUBSTATION contains MV_BUSBAR /
    --    contains BAY fallbacks become the sole parents automatically.
    -- ---------------------------------------------------------------------------
    SELECT id, physical_table INTO vl_id, vl_pt
      FROM metadata.feature_classes WHERE code = 'VOLTAGE_LEVEL';

    IF vl_id IS NOT NULL THEN
        -- Delete runtime containment instances on any VOLTAGE_LEVEL relationship
        -- class (both directions: SUBSTATION contains VOLTAGE_LEVEL, and
        -- VOLTAGE_LEVEL contains MV_BUSBAR/BAY). These are non-cascade, so they
        -- must go before the relationship classes and the feature class. The
        -- children are unaffected -- they simply revert to their SUBSTATION parent.
        DELETE FROM runtime.feature_relationships fr
         USING metadata.relationship_classes rc
         WHERE fr.relationship_class_id = rc.id
           AND rc.kind = 'containment'
           AND (rc.origin_feature_class_id = vl_id OR rc.destination_feature_class_id = vl_id);

        -- Drop the containment relationship CLASSES touching VOLTAGE_LEVEL.
        DELETE FROM metadata.relationship_classes
         WHERE origin_feature_class_id = vl_id OR destination_feature_class_id = vl_id;

        -- Any placed VOLTAGE_LEVEL feature rows are grouping-only and reconstructable
        -- from equipment voltage; surface the count for the record, then let the
        -- table drop below remove them. (None-geom class: no geometry, no tiles.)
        inst := 0;
        IF vl_pt IS NOT NULL AND to_regclass(vl_pt) IS NOT NULL THEN
            EXECUTE format('SELECT count(*) FROM %s', vl_pt) INTO inst;
            IF inst > 0 THEN
                RAISE NOTICE 'migration 0086: dropping % VOLTAGE_LEVEL feature row(s) -- grouping only, voltage is re-derived per equipment at CIM export', inst;
            END IF;
            EXECUTE format('DROP TABLE IF EXISTS %s CASCADE', vl_pt);
        END IF;

        -- Drop the class row. FK ON DELETE CASCADE sweeps its subtypes, fields,
        -- subtype_field_rules, cim_class_mappings, cim_attribute_mappings, terminals;
        -- the non-cascade containment classes were already removed above, and a
        -- none-geom container seeds no network/snapping/diagram/symbology rows.
        DELETE FROM metadata.feature_classes WHERE id = vl_id;
    END IF;
END $$;
