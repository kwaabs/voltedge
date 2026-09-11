-- seed/diagram_example.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations, this
-- lives under /seed on purpose, same reasoning as the other seed
-- files. Schema-level groundwork for the first real diagram: a
-- DIAGRAM_TYPE domain ("just another row in domains, not a new lookup
-- table" -- docs/metadata-core.md), and flipping SUBSTATION to a
-- real diagram container (is_diagram_container was already a column on
-- metadata.feature_classes since Phase 1's very first migration, just
-- never set true on anything until now).
--
-- Deliberately does NOT seed the actual runtime.diagram_instances/
-- diagram_placements rows here -- every other runtime.* instance table
-- in this project (feature_connectivity, feature_relationships) is
-- always populated through the real API, never a raw INSERT, so a
-- seeded "fact" is never speculative even in demo data. See
-- docs/phase-5-diagrams.md's "Try it yourself" for the curl sequence
-- that creates a real diagram instance and places features on it.
--
-- Apply with psql after migrations 0001-0019 have run.

INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('DIAGRAM_TYPE', 'Diagram type', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, 'ONE_LINE', 'One-line diagram', 1
FROM metadata.domains d
WHERE d.code = 'DIAGRAM_TYPE'
  AND NOT EXISTS (
    SELECT 1 FROM metadata.domain_values
    WHERE domain_id = d.id AND code = 'ONE_LINE'
  );

UPDATE metadata.feature_classes
SET is_diagram_container = true
WHERE code = 'SUBSTATION';
