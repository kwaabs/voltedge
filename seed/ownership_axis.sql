-- seed/ownership_axis.sql
-- Purpose: NOT a migration -- apply.sh only scans /migrations; this lives under
-- /seed on purpose, same reasoning as every other class/field seed. It adds the
-- ASSET OWNERSHIP axis (Slice 1): a coded `ownership` attribute distinguishing
-- UTILITY-owned plant from PRIVATELY-owned plant on the grid -- the case where
-- the utility supplies and meters an MV bulk supply (MV_METERING_POINT, already
-- modelled) and the customer owns everything below: their own distribution
-- transformer and their own LV network.
--
-- DESIGN (agreed with the user):
--   * The flag is coded `utility` (the default) / `private`. A NULL value reads
--     as `utility`, so every existing feature stays utility-owned with no backfill.
--   * You SET it by hand only at the boundary -- typically the private DIST_XFMR.
--     Everything electrically downstream is then stamped `private` by the Slice 2
--     propagation action ("mark downstream private" + a re-sync), which is why the
--     column must exist on the whole downstream LV set, not just the transformer.
--   * v1 scope = the transformers + the LV set (the "private transformer -> private
--     LV" case). MV asset classes (MV_CONDUCTOR/MV_SWITCH/MV_BUSBAR/RMU...) are a
--     fast follow when a site owns MV plant too.
--
-- Pure metadata like material_specs/load_flow_fields -- additive, idempotent. But
-- it adds a COLUMN to each listed class, so after applying, RECOMPILE every one:
--   task compile CODE=DIST_XFMR   (and POWER_XFMR, LV_CONDUCTOR, LV_SWITCH,
--   LV_LINK_BOX, LV_BUSBAR, LV_PILLAR, LV_FUSE, SERVICE_CUTOUT, SERVICE_POINT,
--   METER, STREET_LIGHT, METER_CHAMBER) -- each class this seed touched.
--
-- Fields are added NULLABLE with no subtype_field_rules, so existing features stay
-- valid (an unset ownership = utility). A class the IN-list names but that isn't
-- seeded on this database is simply skipped (the INSERT...SELECT matches nothing).
--
-- Apply with psql (NOT the migration runner), then recompile:
--   task seed-ownership-axis
--
-- Static-review note: shell here can't spawn (bash.exe EPERM); a live psql apply +
-- the recompiles + a map/trace re-check are still owed to call it verified.

-- =====================================================================
-- 1. The OWNERSHIP coded domain (utility default, private).
-- =====================================================================
INSERT INTO metadata.domains (code, name, domain_type) VALUES
    ('OWNERSHIP', 'Asset ownership', 'coded')
ON CONFLICT (code) DO NOTHING;

INSERT INTO metadata.domain_values (domain_id, code, label, sort_order)
SELECT d.id, v.code, v.label, v.sort_order
FROM metadata.domains d
JOIN (VALUES
    ('utility', 'Utility-owned', 1),
    ('private', 'Privately owned', 2)
) AS v(code, label, sort_order) ON true
WHERE d.code = 'OWNERSHIP'
ON CONFLICT (domain_id, code) DO NOTHING;

-- =====================================================================
-- 2. A field group for the ownership/custodianship attribute.
-- =====================================================================
INSERT INTO metadata.field_groups (code, name, sort_order) VALUES
    ('ownership', 'Ownership', 20)
ON CONFLICT (code) DO NOTHING;

-- =====================================================================
-- 3. The `ownership` field on every class that can be a boundary or sit
--    below one. Nullable (NULL = utility); no subtype rule.
-- =====================================================================
INSERT INTO metadata.fields (feature_class_id, name, data_type, domain_id, group_id, nullable)
SELECT fc.id, 'ownership', 'text', d.id, fg.id, true
FROM metadata.feature_classes fc
JOIN metadata.domains d ON d.code = 'OWNERSHIP'
JOIN metadata.field_groups fg ON fg.code = 'ownership'
WHERE fc.code IN (
    -- boundary assets (where you declare it by hand)
    'DIST_XFMR', 'POWER_XFMR',
    -- the LV set (stamped by downstream propagation)
    'LV_CONDUCTOR', 'LV_SWITCH', 'LV_LINK_BOX', 'LV_BUSBAR', 'LV_PILLAR',
    'LV_FUSE', 'SERVICE_CUTOUT', 'SERVICE_POINT', 'METER', 'STREET_LIGHT',
    'METER_CHAMBER'
)
ON CONFLICT (feature_class_id, name) DO NOTHING;

-- =====================================================================
-- 4. Symbology: NONE. A privately-owned feature keeps the SAME standard colours
--    and symbology as every other feature of its class -- ownership is a DATA
--    flag and an attribute / trace / load-flow label, not a map restyle. (An
--    earlier revision greyed private plant with a muted-slate override; that was
--    removed on request.) This DELETE clears any such private-ownership map
--    override a prior apply of this seed left in the DB, so re-running the seed
--    restores the standard look. Idempotent -- a no-op when none exist.
-- =====================================================================
DELETE FROM metadata.symbology_rules
WHERE domain_value_id IN (
    SELECT dv.id
    FROM metadata.domain_values dv
    JOIN metadata.domains d ON d.id = dv.domain_id
    WHERE d.code = 'OWNERSHIP' AND dv.code = 'private'
);

-- =====================================================================
-- 5. The trace configuration ownership propagation walks (Slice 2). It is a
--    DOWNSTREAM trace that (a) does NOT stop at open devices and (b) DOES cross
--    transformers -- because ownership is a CONNECTIVITY property, not an
--    energisation one: an asset beyond a normally-open switch inside the private
--    network is still privately owned, and the private LV lives across the
--    (private) transformer from where you declare it. respect_phase=false -- the
--    whole galvanic island below the boundary is private regardless of phase.
--    Depends on the TRACE_DIRECTION domain (seed/trace_configurations_example.sql).
-- =====================================================================
-- Named "Ownership domain" -- NOT "... downstream" -- so it reads as a distinct
-- concept in the trace menu's Direction submenu rather than a near twin of the
-- electrical "Downstream trace" (it's a connectivity walk: ignores open switches,
-- crosses transformers, ignores phase -- see the flags below).
-- DO UPDATE SET name so a re-seed renames an already-installed row (the display
-- label only; the behaviour flags are left as authored).
INSERT INTO metadata.trace_configurations (code, name, direction_value_id, stop_at_open_devices, cross_transformers, respect_phase)
SELECT 'OWNERSHIP_DOWNSTREAM', 'Ownership domain', dv.id, false, true, false
FROM metadata.domain_values dv
JOIN metadata.domains d ON d.id = dv.domain_id
WHERE d.code = 'TRACE_DIRECTION' AND dv.code = 'DOWNSTREAM'
ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name;

DO $$
BEGIN
    RAISE NOTICE 'seed/ownership_axis.sql applied: OWNERSHIP domain + ownership field on 13 classes (no map restyle -- private keeps standard symbology) + OWNERSHIP_DOWNSTREAM trace config. RECOMPILE the touched classes (task compile CODE=...).';
END $$;
