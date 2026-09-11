-- 0062_create_circuits.sql
-- Purpose: Phase 37 Slice 1 -- the circuits catalog. A circuit is a named,
-- network-wide electrical identity (e.g. the MV feeder "Old Age 2") that many
-- features across many classes belong to -- its fuses, conductors, isolators,
-- transformers, and so on. This is the persistent parent entity; membership on
-- individual features (a later slice's `circuit_id` field) is derived by a
-- downstream trace from the circuit's source, with manual override.
--
-- Modelled on gis.territories (migration 0027): a lookup entity that features
-- reference by id, curated in-app by an operator through a CRUD API + admin UI.
-- It lives in the `gis` schema alongside territories for the same reason -- it
-- is operational reference data about the real network, and a later slice's
-- assign-by-trace reads the physical feature tables that live here.
--
-- Unlike territories, circuits are FLAT (no level catalog, no parent tree) and
-- carry no geometry of their own -- a circuit's extent is the union of the
-- features stamped onto it, not a drawn boundary. MV and LV are separate
-- namespaces (an "Old Age 2" MV circuit is unrelated to any LV circuit of the
-- same name), so uniqueness is per (network, name).
--
-- voltage_kv is a property OF the circuit (an MV circuit "could be 11 or 33"),
-- nullable until known. source_feature_class/source_feature_id point at the
-- feature the assign-by-trace starts from (the substation breaker / transformer
-- LV terminal); both nullable and NOT a DB foreign key -- the same
-- no-enforced-join convention territory_id and created_by follow, since the
-- referenced row lives in a per-class physical table the catalog can't FK to.

CREATE TABLE gis.circuits (
    id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name                 text NOT NULL,
    network              text NOT NULL CHECK (network IN ('MV', 'LV')),
    voltage_kv           numeric CHECK (voltage_kv IS NULL OR voltage_kv > 0),
    source_feature_class text,
    source_feature_id    uuid,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

-- One circuit name per network (MV "Old Age 2" is distinct from any LV circuit,
-- but two MV "Old Age 2" rows are a data-entry mistake, not two circuits).
CREATE UNIQUE INDEX circuits_network_name_key ON gis.circuits (network, lower(name));

-- The circuit list is filtered by network everywhere it's picked (an MV class's
-- circuit_id picker shows only MV circuits).
CREATE INDEX circuits_network_idx ON gis.circuits (network);
