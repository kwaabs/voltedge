-- 0132_create_field_pack_downloads.sql
-- Purpose: the shared-pack download LEDGER (docs/shared-field-packs-design.md).
--
-- A shared cell base is pulled by MANY crews and reused for weeks, so -- unlike a
-- personal pack (owner-scoped, 1:1) -- we must record WHO pulled WHICH version and
-- WHEN. Reconciled against incoming syncs (ops.sync_inbox already stamps each op's
-- base_version), this answers the operational question the product owner asked for:
-- per cell, who's holding each version, who has synced, and who has gone quiet. It
-- also drives retention: a cell version is only a reap candidate once no active
-- holder references it.
--
-- One row per pickup. A crew that re-pulls a newer version gets a new row, so the
-- ledger is an append-only history, not a single current-holding state.

CREATE TABLE ops.field_pack_downloads (
    id      uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Who pulled it. CASCADE: a deleted user's download history goes with them.
    user_id uuid NOT NULL REFERENCES iam.users(id) ON DELETE CASCADE,

    -- The exact build (cell version) pulled. CASCADE: once a version is reaped
    -- (retention, which only fires when no active holder references it), its
    -- download rows go too -- there is nothing left to hold.
    pack_id uuid NOT NULL REFERENCES ops.field_packs(id) ON DELETE CASCADE,

    -- The cell the version belongs to, kept for the per-cell usage view even if the
    -- cell definition is later deleted (SET NULL), so historical usage survives.
    cell_id uuid REFERENCES ops.field_pack_cells(id) ON DELETE SET NULL,

    -- The pulling device (optional), so one crew member's two tablets are
    -- distinguishable in the usage view.
    device_id text,

    downloaded_at timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE ops.field_pack_downloads IS 'Append-only ledger of shared-pack pickups: who pulled which cell version, when. Reconciled against ops.sync_inbox.base_version for the per-cell usage view (who is holding / who has synced / who has gone quiet) and to decide when a version is safe to reap. See docs/shared-field-packs-design.md.';

-- A crew member's download history, newest first.
CREATE INDEX field_pack_downloads_user_idx ON ops.field_pack_downloads (user_id, downloaded_at DESC);
-- A cell's holders across versions (the usage dashboard's per-cell read).
CREATE INDEX field_pack_downloads_cell_idx ON ops.field_pack_downloads (cell_id, downloaded_at DESC);
-- The holders of one specific version (retention's "any active holder?" check).
CREATE INDEX field_pack_downloads_pack_idx ON ops.field_pack_downloads (pack_id);
