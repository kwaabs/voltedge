-- cim_reference_profile_members.sql
-- The PROFILE-MEMBERSHIP layer of the CIM reference dictionary (migration 0120):
-- which CIM classes/attributes are "in profile" for each export target, so the
-- mapping editor can DEFAULT to the safe in-profile subset and let an admin opt
-- into the wider full-CIM reference (cim_ref_classes/_attributes) knowingly.
--
-- DERIVED, not hand-authored. The source of truth is VoltEdge's own CIM export
-- mapping (metadata.cim_class_mappings + metadata.cim_attribute_mappings from
-- migration 0034): "in-profile" here means "a CIM class/attribute this export
-- actually targets" -- the known-good subset our encoder is proven to emit. This
-- honours the same rule as the classes/attributes seeds: no CIM vocabulary is
-- invented; every membership row is traceable to a real mapping row.
--
-- Re-runnable: it re-derives from live mapping data each time, so re-apply it
-- after editing the CIM mappings (Admin -> CIM Mappings) to keep membership in
-- sync. Run AFTER migration 0120 (needs the cim_ref_profiles / _members tables).
--
-- NOTE: this is the pragmatic "our-usage subset". It does NOT enumerate the full
-- formal IEC 61968-13 CDPSM/CIM16 standard membership -- that needs an authoritative
-- CDPSM RDFS profile file (none is in schema/cim/ yet). Drop one in and we can add
-- a second, file-parsed membership source alongside this without disturbing it.

BEGIN;

-- Idempotent reset. Deleting the profiles would cascade to members, but we clear
-- members first explicitly so re-running is order-independent and obvious.
DELETE FROM metadata.cim_ref_profile_members;
DELETE FROM metadata.cim_ref_profiles;

-- 1. One reference-profile row per export profile. code matches
-- metadata.cim_profiles.code so the editor can tie "the profile you are
-- exporting" to this membership set.
INSERT INTO metadata.cim_ref_profiles (code, name, source)
SELECT p.code,
       p.name,
       'derived from metadata.cim_class_mappings + cim_attribute_mappings (migration 0034)'
FROM metadata.cim_profiles p;

-- 2. Whole-class membership (attribute_name NULL): every CIM class our export
-- touches, whether via a class mapping or as the owning class of an attribute
-- mapping. Union so classes that only appear through their attributes (e.g.
-- PowerTransformerEnd, BaseVoltage) are still marked in-profile at class level.
INSERT INTO metadata.cim_ref_profile_members (profile_code, class_name, attribute_name)
SELECT DISTINCT p.code, c.class_name, NULL
FROM metadata.cim_profiles p
CROSS JOIN (
    SELECT cim_class AS class_name
    FROM metadata.cim_class_mappings
    WHERE cim_class IS NOT NULL AND cim_class <> ''
    UNION
    SELECT split_part(cim_attribute, '.', 1)
    FROM metadata.cim_attribute_mappings
    WHERE cim_attribute LIKE '%.%'
      AND split_part(cim_attribute, '.', 1) <> ''
) c;

-- 3. Attribute membership: each dotted cim_attribute target split into its
-- (ClassName, attributeName). split_part(.,2) takes the attribute after the
-- first dot, so VoltEdge sentinel targets like BaseVoltage.nominalVoltage.secondary
-- collapse to the real CIM attribute BaseVoltage.nominalVoltage.
INSERT INTO metadata.cim_ref_profile_members (profile_code, class_name, attribute_name)
SELECT DISTINCT p.code,
       split_part(am.cim_attribute, '.', 1) AS class_name,
       split_part(am.cim_attribute, '.', 2) AS attribute_name
FROM metadata.cim_profiles p
CROSS JOIN metadata.cim_attribute_mappings am
WHERE am.cim_attribute LIKE '%.%'
  AND split_part(am.cim_attribute, '.', 1) <> ''
  AND split_part(am.cim_attribute, '.', 2) <> ''
ON CONFLICT (profile_code, class_name, attribute_name) DO NOTHING;

COMMIT;
