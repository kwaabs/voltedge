-- Materialized energization (network coloring) -- a DERIVED read-model.
--
-- Energization is not an authored attribute: a feature is energized iff a path of
-- conducting elements (closed switchgear, in-service plant) joins it to a live source
-- (functional_role='source'). That is computed by the source-rooted coloring pass
-- (trace.EnergizedSet). This table CACHES the result so the map/schematic read it as
-- a cheap set membership instead of every viewer re-walking the whole network.
--
-- Contract: a row's PRESENCE means that feature is currently DE-ENERGISED. The pass
-- fully replaces the set on each recompute, so ABSENCE means energised (as of the last
-- compute). It is written ONLY by the coloring pass (trace.RecolorNetwork) -- never by
-- hand, never by an edit form -- and recomputed when switch / isolation / source /
-- topology state changes. Dropping it loses nothing but a cache; the next read rebuilds
-- it.
CREATE TABLE IF NOT EXISTS runtime.energization (
    feature_class text NOT NULL,
    feature_id    text NOT NULL,
    PRIMARY KEY (feature_class, feature_id)
);

-- Single-row freshness marker so a read can tell "the set is empty because everything
-- is live" apart from "it was never computed", and apply a staleness TTL. source_rooted
-- records whether the last pass used the correct source flood or the dead-cone fallback
-- (no source modelled).
CREATE TABLE IF NOT EXISTS runtime.energization_state (
    id            boolean PRIMARY KEY DEFAULT true CHECK (id),
    computed_at   timestamptz NOT NULL DEFAULT now(),
    source_rooted boolean NOT NULL DEFAULT false
);
