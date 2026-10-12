-- Purpose: Correct the metadata.symbology_rules table comment (migration 0012).
--
-- 0012 said the schema compiler's validation pass enforces "a class has a
-- default rule". It does not: cmd/compile and internal/compiler contain no
-- symbology check, the rule editor does not enforce it, and there is no DB
-- constraint. A class with no default simply falls back to plain geometry
-- styling in the web matcher (web/src/lib/symbology.ts resolveSymbology).
-- Comment only; no schema or data change.

COMMENT ON TABLE metadata.symbology_rules IS 'Resolution order at read time is most-specific-wins: (feature_class + subtype + domain_value) > (feature_class + subtype) > (feature_class + domain_value) > (feature_class alone, the default). Every class SHOULD have one rule with subtype_id and domain_value_id both null, but nothing enforces it (no DB constraint, no editor check, no compiler check); a class without one falls back to plain geometry styling in the web matcher.';
