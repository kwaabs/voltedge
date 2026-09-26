-- cim_mapping_target_fixes.sql
--
-- Corrective, one-shot re-targeting of CIM attribute mappings that the CIM
-- reference dictionary (migration 0120) flagged as non-canonical or invalid.
-- This UPDATES existing rows in place because the example seeds that created them
-- guard with ON CONFLICT (field_id, cim_attribute) DO NOTHING -- re-running those
-- seeds after editing their targets would ADD the new row and leave the stale one,
-- emitting BOTH. Running this once brings a live DB in line with the edited seeds.
--
-- Fully idempotent: after it runs the WHERE clauses match nothing. Each fix first
-- drops any canonical duplicate a prior reseed may have inserted, so the UPDATE
-- can't collide with the UNIQUE (field_id, cim_attribute) index.
--
-- The three inheritance normalisations are behaviour-correct: CIM RDF/XML
-- serializes a property under the class that DECLARES it, so a strict consumer
-- (CIMTool / CGMES importers) silently drops a subclass-qualified property such as
-- cim:RatioTapChanger.step. See docs/cim-export-guide.md and the encoder's
-- cw.lit in api/internal/export/cim.go, which emits cim_attribute verbatim.

BEGIN;

-- Helper pattern per fix: DELETE canonical dup (if a reseed already added it),
-- then UPDATE the old target to the canonical one.

-- 1. RatioTapChanger.step -> TapChanger.step  (step declared on TapChanger)
DELETE FROM metadata.cim_attribute_mappings a
WHERE a.cim_attribute = 'TapChanger.step'
  AND EXISTS (SELECT 1 FROM metadata.cim_attribute_mappings b
              WHERE b.field_id = a.field_id AND b.cim_attribute = 'RatioTapChanger.step');
UPDATE metadata.cim_attribute_mappings
   SET cim_attribute = 'TapChanger.step'
 WHERE cim_attribute = 'RatioTapChanger.step';

-- 2. TapChangerControl.targetValue -> RegulatingControl.targetValue
DELETE FROM metadata.cim_attribute_mappings a
WHERE a.cim_attribute = 'RegulatingControl.targetValue'
  AND EXISTS (SELECT 1 FROM metadata.cim_attribute_mappings b
              WHERE b.field_id = a.field_id AND b.cim_attribute = 'TapChangerControl.targetValue');
UPDATE metadata.cim_attribute_mappings
   SET cim_attribute = 'RegulatingControl.targetValue'
 WHERE cim_attribute = 'TapChangerControl.targetValue';

-- 3. TapChangerControl.targetDeadband -> RegulatingControl.targetDeadband
DELETE FROM metadata.cim_attribute_mappings a
WHERE a.cim_attribute = 'RegulatingControl.targetDeadband'
  AND EXISTS (SELECT 1 FROM metadata.cim_attribute_mappings b
              WHERE b.field_id = a.field_id AND b.cim_attribute = 'TapChangerControl.targetDeadband');
UPDATE metadata.cim_attribute_mappings
   SET cim_attribute = 'RegulatingControl.targetDeadband'
 WHERE cim_attribute = 'TapChangerControl.targetDeadband';

-- 4. ShuntCompensator.nomQ -> ShuntCompensatorInfo.ratedReactivePower
--    nomQ is a pre-CIM16 attribute absent from current CIM; ratedReactivePower is
--    the real, semantically-exact "rated reactive power" attribute (range VAr).
--    (It lives on the AssetInfo class; bPerSection would be the fully powerflow-
--    correct field but needs a kVAr -> susceptance conversion -- see the cap-bank seed.)
DELETE FROM metadata.cim_attribute_mappings a
WHERE a.cim_attribute = 'ShuntCompensatorInfo.ratedReactivePower'
  AND EXISTS (SELECT 1 FROM metadata.cim_attribute_mappings b
              WHERE b.field_id = a.field_id AND b.cim_attribute = 'ShuntCompensator.nomQ');
UPDATE metadata.cim_attribute_mappings
   SET cim_attribute = 'ShuntCompensatorInfo.ratedReactivePower'
 WHERE cim_attribute = 'ShuntCompensator.nomQ';

COMMIT;
