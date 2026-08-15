-- 0020_add_domain_range_bounds.sql
-- Purpose: give a range domain (metadata.domains.domain_type = 'range')
-- somewhere to actually store its bounds. Nothing modeled this before --
-- domain_values only ever holds coded-domain rows -- so a range domain
-- has always been legal to create (domain_type's own CHECK already
-- allowed it) but had no real min/max anywhere in the schema, and
-- api/internal/compiler/ddl.go's columnDefinition has had its own
-- comment flagging exactly this gap since Phase 6 slice 1.

ALTER TABLE metadata.domains
    ADD COLUMN range_min numeric,
    ADD COLUMN range_max numeric,
    ADD CONSTRAINT domains_range_bounds_check CHECK (
        range_min IS NULL OR range_max IS NULL OR range_min <= range_max
    );

COMMENT ON COLUMN metadata.domains.range_min IS 'Inclusive lower bound for a range domain (domain_type = ''range''); NULL and unused for a coded domain.';
COMMENT ON COLUMN metadata.domains.range_max IS 'Inclusive upper bound for a range domain (domain_type = ''range''); NULL and unused for a coded domain.';
