-- Operations-owned layout overrides for the LIVE operations one-line.
--
-- The ADMS operations schematic is a live view: its layout is computed from the
-- current network on demand (schematic.BuildNetworkOneLine), not a persisted GIS
-- diagram. Each row here nudges ONE feature's position in that view's abstract
-- one-line coordinate space, applied on top of the deterministic computed layout so
-- an operator's hand-arrangement survives across network changes.
--
-- This is deliberately NOT a GIS diagram: it never touches feature geometry, carries
-- no edit session, and is keyed by the (feature_class, feature_id) pair convention
-- the rest of the operations surface uses. It is owned by the operations surface
-- (operations:read / operations:write), not the diagram engine.
CREATE TABLE IF NOT EXISTS ops.oneline_overrides (
    feature_class text NOT NULL,
    feature_id    text NOT NULL,
    x             double precision NOT NULL,
    y             double precision NOT NULL,
    updated_by    uuid,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (feature_class, feature_id)
);
