-- seed/meter_service_point_mounted.sql
-- Purpose: NOT a migration -- lives under /seed, same convention as the other
-- seed files. The data-model foundation (Slice 1) for ATTACHING A METER TO A
-- SERVICE POINT: a meter installed at a delivery point that has no independent
-- survey location of its own -- it rides the service point. The mirror of the
-- pole-mount pattern (seed/street_light_pole_mounted.sql,
-- seed/pole_mount_recloser.sql et al.), with SERVICE_POINT as the host instead
-- of POLE.
--
-- KEY: this does NOT force every meter geometry-less. geometry_optional is a
-- PER-INSTANCE choice (exactly as STREET_LIGHT's column/wall lamps keep their
-- point while pole-mounted ones ride the pole): a meter SURVEYED at its own
-- location keeps its geometry and is created the normal way; a meter installed
-- at a service point is created WITHOUT geometry and rides the SP via the support
-- link below. Both coexist in gis.meter.
--
-- Two additive parts (one forces a recompile):
--
--   1. METER made geometry_optional, so a service-point-mounted meter is created
--      with NO map geometry -- it has no independent location, it rides its SP
--      (via the support relationship below). A standalone surveyed meter keeps
--      its point. This DROPs NOT NULL on gis.meter.geom -> RECOMPILE METER.
--   2. a SERVICE_POINT supports METER relationship class (spatial_support,
--      one_to_many -- a service point may host its PRIMARY billing meter AND a
--      CHECK meter), the same machinery the pole-mount assemblies use.
--
-- The electrical feed already exists: the METER -> SERVICE_POINT network rule
-- ships (seed/meter_load_leaf.sql keeps it), so a geometry-less SP-mounted meter
-- is still wired to its service point (and so into the LV above it). No subtype
-- is added -- unlike the street light (whose mounting STYLE was a real
-- attribute), "does this meter ride an SP" is simply "has no geometry + a support
-- link", so a subtype would carry no information.
--
-- THE LOAD CONTRACT (seed/meter_load_leaf.sql, read by trace/service.go): load
-- lives in exactly ONE place per supply -- a PRIMARY meter counts, an unmetered
-- SERVICE_POINT counts, never both. So the Slice 2 "Attach meter" action, when it
-- attaches a PRIMARY meter to an SP that still carries load, MOVES the SP's
-- demand_kw / power_factor / load_priority onto the new meter and zeroes the
-- SP's, keeping load single-sited (the same thing meter_load_leaf_migrate.sql
-- does in bulk). A CHECK / role-unset meter is inert and safe to attach as-is.
-- This seed is only the metadata foundation; the action enforces the contract.
--
-- Slice 2 (the "Attach meter" action: create the geometry-less meter + the
-- support link + the meter->SP network connection + the primary-load move) and
-- Slice 3 (the SP-panel button) build on this.
--
-- Static-review note: shell here can't spawn (bash.exe EPERM), so this SQL is
-- reviewed statically -- a live psql apply + `task compile CODE=METER` are owed
-- to call it verified.
--   task seed-meter-service-point + task compile CODE=METER

-- ---------------------------------------------------------------------------
-- 1. geometry_optional: a service-point-mounted meter has no geometry of its
--    own; it rides its service point. Flips geom to nullable on recompile.
--    Idempotent. A surveyed standalone meter keeps its point.
-- ---------------------------------------------------------------------------
UPDATE metadata.feature_classes
SET geometry_optional = true
WHERE code = 'METER' AND geometry_optional = false;

-- ---------------------------------------------------------------------------
-- 2. SERVICE_POINT supports METER -- the structural attachment, like POLE
--    supports STREET_LIGHT / the pole-mount kit components. one_to_many: one
--    service point, potentially a primary + a check meter; each mounted meter on
--    exactly one SP.
-- ---------------------------------------------------------------------------
INSERT INTO metadata.relationship_classes (name, origin_feature_class_id, destination_feature_class_id, kind, cardinality)
SELECT 'SERVICE_POINT supports METER', sp.id, m.id, 'spatial_support', 'one_to_many'
FROM metadata.feature_classes sp
JOIN metadata.feature_classes m ON m.code = 'METER'
WHERE sp.code = 'SERVICE_POINT'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.relationship_classes rc
    WHERE rc.origin_feature_class_id = sp.id AND rc.destination_feature_class_id = m.id AND rc.kind = 'spatial_support'
  );

DO $$
BEGIN
    RAISE NOTICE 'seed/meter_service_point_mounted.sql applied: + METER geometry_optional, + SERVICE_POINT supports METER relationship class. RECOMPILE: task compile CODE=METER (geometry_optional drops NOT NULL on geom). Slice 2 Attach action + Slice 3 panel button build on this.';
END $$;
