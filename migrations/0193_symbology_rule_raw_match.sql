-- 0193_symbology_rule_raw_match.sql
-- Purpose: let a symbology rule target a feature by a RAW (field, value) pair, not
-- only by subtype_id / domain_value_id. Until now a rule's attribute match had to go
-- through metadata.domain_values (migration 0012), so it could key only on a
-- domain-coded field's value. The assembly map-symbol feature (0192) needs to key a
-- rule on the plain `assembly_ref` system column (= the assembly recipe code), which is
-- not a domain-coded field. Rather than force assembly_ref into a domain + per-code
-- domain values, this generalises symbology_rules: match_field/match_value name the
-- field and the string value to compare directly. The client matcher already compares
-- attributes[field] === value (web/src/lib/symbology.ts); resolveSymbologySchema
-- (cmd/server/main.go) just passes match_field/match_value through as the
-- domain_field/domain_value_code it already emits, so NO client change is needed.
--
-- template_id ties a managed rule to the assembly_templates row that owns it, so the
-- rule is upserted when the template's map symbol changes and CASCADE-deleted when the
-- template is deleted. NULL for every ordinary (hand-authored) rule.

ALTER TABLE metadata.symbology_rules
    ADD COLUMN IF NOT EXISTS match_field text,
    ADD COLUMN IF NOT EXISTS match_value text,
    ADD COLUMN IF NOT EXISTS template_id uuid
        REFERENCES metadata.assembly_templates (id) ON DELETE CASCADE;

-- Both-or-neither: a raw match needs a field AND a value (an incomplete pair would
-- match nothing / everything by accident).
ALTER TABLE metadata.symbology_rules
    ADD CONSTRAINT symbology_rules_raw_match_both
    CHECK ((match_field IS NULL) = (match_value IS NULL));

-- One managed map rule per template (the assembly's map glyph). A hand-authored rule
-- has template_id NULL and isn't constrained here.
CREATE UNIQUE INDEX IF NOT EXISTS symbology_rules_template_key
    ON metadata.symbology_rules (template_id)
    WHERE template_id IS NOT NULL;

COMMENT ON COLUMN metadata.symbology_rules.match_field IS 'Raw attribute match: the feature property name to compare (alternative to domain_value_id''s domain-resolved field). Used by the assembly map-symbol feature to key on the plain assembly_ref column. Pairs with match_value.';
COMMENT ON COLUMN metadata.symbology_rules.match_value IS 'Raw attribute match: the exact string value match_field must equal for this rule to apply.';
COMMENT ON COLUMN metadata.symbology_rules.template_id IS 'When set, this rule is MANAGED by the assembly template with this id (its map symbol). Upserted on template save, CASCADE-deleted with the template. NULL for hand-authored rules.';
