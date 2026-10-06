-- 0131_create_field_pack_cells.sql
-- Purpose: shared, pre-built field packs (docs/shared-field-packs-design.md).
--
-- A CELL is an admin-defined, shareable coverage tile: a bbox + class set + which
-- extras to include, scoped to a territory. A cell is a DEFINITION; each build of
-- it produces a "cell version" -- an ops.field_packs row with kind='shared_base'
-- and cell_id set -- which is the SHARED BASE many crews pull and reuse for weeks.
-- A cell is a shared cache tile, NOT a crew assignment: a roaming crew pulls
-- whatever cells cover where it's going; a fixed crew pulls the same ones.
--
-- The shared base carries PUBLISHED, NON-PERSONAL data only (features + tiles +
-- connectivity/relationships + symbology + tags + the cell's open faults as read
-- context). Personal data -- a crew's WAM work board and their own private drafts
-- -- is a per-user OVERLAY built separately (Slice 2), so the base is safe to hand
-- to anyone whose access covers the cell (pickup gating, Slice 3). Hence the base
-- build sets is_published_only (the reader excludes every private draft) and never
-- includes WAM.

CREATE TABLE ops.field_pack_cells (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name         text NOT NULL,

    -- The access boundary: a crew may pull this cell only if their territory access
    -- covers this territory (enforced at pickup, Slice 3). No FK -- the same FK-less
    -- polymorphic-id pattern runtime.feature_connectivity uses; the pickup gate
    -- resolves it against the live territory tree. NULL = no territory restriction
    -- (the cell is covered by features:read alone).
    territory_id uuid,

    -- The cell's area + content, mirroring PackScope's jsonb shape so building a
    -- cell version is just serializing these into a field_packs scope.
    bbox            jsonb NOT NULL,   -- [minX, minY, maxX, maxY] in EPSG:4326
    feature_classes jsonb NOT NULL,   -- ["POLE", "MV_CONDUCTOR", ...]

    -- Which extras the shared base carries. The personal WAM work board is never
    -- here -- it is the per-user overlay (Slice 2). faults/defects ship as read
    -- context (open items on the cell's assets), not as a personal board.
    include_connectivity  boolean NOT NULL DEFAULT false,
    include_relationships boolean NOT NULL DEFAULT false,
    include_faults        boolean NOT NULL DEFAULT false,
    include_tags          boolean NOT NULL DEFAULT true,

    -- Rebuild cadence as a cron expression; NULL = manual-only. The scheduler
    -- (Slice 4) reads this; Slice 1 builds a cell on demand via the admin endpoint.
    schedule   text,
    -- Version retention policy (Slice 6): 'manual' keeps every version until an
    -- admin deletes it. Other modes ('keep_n', 'pinned') land in Slice 6.
    retention  text NOT NULL DEFAULT 'manual' CHECK (retention IN ('manual', 'keep_n', 'pinned')),

    active     boolean NOT NULL DEFAULT true,
    created_by uuid NOT NULL REFERENCES iam.users(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE ops.field_pack_cells IS 'Admin-defined shared coverage tiles for pre-built field packs. Each build of a cell is an ops.field_packs row (kind=shared_base, cell_id). A cell is a shared cache tile crews pull from, never a crew assignment. See docs/shared-field-packs-design.md.';
COMMENT ON COLUMN ops.field_pack_cells.territory_id IS 'The access boundary: a crew may pull this cell only if their territory access covers this id (enforced at pickup). No FK -- resolved against the live territory tree. NULL = no territory restriction.';

CREATE INDEX field_pack_cells_territory_idx ON ops.field_pack_cells (territory_id) WHERE active;

-- A cell version is an ops.field_packs build distinguished by kind + cell_id. The
-- same worker, builder, queue and SKIP-LOCKED claim serve it -- only the scope
-- differs (published-only, no WAM work board).
ALTER TABLE ops.field_packs
    ADD COLUMN kind text NOT NULL DEFAULT 'personal'
        CHECK (kind IN ('personal', 'shared_base')),
    ADD COLUMN cell_id uuid REFERENCES ops.field_pack_cells(id) ON DELETE SET NULL,
    ADD COLUMN is_published_only boolean NOT NULL DEFAULT false;

COMMENT ON COLUMN ops.field_packs.kind IS 'personal = an on-demand pack owned by requested_by (the default, today''s packs). shared_base = a cell version: the shared, published-only base many crews pull (docs/shared-field-packs-design.md).';
COMMENT ON COLUMN ops.field_packs.cell_id IS 'The cell this build is a version of, when kind=shared_base. NULL for a personal pack.';

-- A cell's version history (its builds), newest first -- the admin pack list and
-- the pickup path read the latest succeeded version per cell.
CREATE INDEX field_packs_cell_idx ON ops.field_packs (cell_id, created_at DESC) WHERE cell_id IS NOT NULL;
