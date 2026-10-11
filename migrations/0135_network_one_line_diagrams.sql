-- Network one-line diagrams: a diagram that spans a map viewport / selection,
-- NOT bound to one owning container feature. Until now every runtime.diagram_
-- instances row required a container (NOT NULL + is_diagram_container gate), so
-- an area/network one-line spanning many (or no) containers was inexpressible.
--
-- This relaxes the two container columns to nullable -- an unowned diagram
-- leaves them NULL -- and seeds the NETWORK_ONE_LINE DIAGRAM_TYPE value the
-- generator creates these under. Owned diagrams (substation / assembly single-
-- lines) are unchanged: they still set both columns, and placeFeature still
-- applies the per-container placement allow-list for them.

ALTER TABLE runtime.diagram_instances ALTER COLUMN container_feature_class_id DROP NOT NULL;
ALTER TABLE runtime.diagram_instances ALTER COLUMN container_feature_id DROP NOT NULL;

COMMENT ON COLUMN runtime.diagram_instances.container_feature_class_id IS
  'The owning container feature class (is_diagram_container), or NULL for an unowned network one-line (migration 0135) that spans a viewport/selection rather than one container.';

-- DIAGRAM_TYPE is an ordinary coded domain ("just another row in domains") --
-- seeded idempotently here too, because a fresh database runs migrations before
-- the seed files that also create it (seed/diagram_example.sql). domain_values
-- uses `label`, not `name`.
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('DIAGRAM_TYPE', 'Diagram type', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, 'NETWORK_ONE_LINE', 'Network one-line', 50
FROM metadata.domains d
WHERE d.code = 'DIAGRAM_TYPE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.domain_values
    WHERE domain_id = d.id AND code = 'NETWORK_ONE_LINE'
  );
