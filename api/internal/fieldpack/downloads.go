package fieldpack

// The shared-pack download LEDGER store (migration 0132,
// docs/shared-field-packs-design.md). Where a personal pack is owner-scoped and
// pulled once by its requester, a shared cell base is pulled by MANY crews and
// reused for weeks -- so every pickup is recorded here: who pulled which cell
// version, when, from which device. Two jobs ride on it:
//
//   1. Accounting (S5): reconciled against ops.sync_inbox.base_version + submitted_by,
//      it answers the supervisor's "who holds each version / who has synced / who has
//      gone quiet" question -- the real need the product owner called out (freshness
//      is not critical; the pack lives ~a month, the crew syncs weekly).
//   2. Retention (S6): a cell version is a reap candidate only once no row here still
//      references it (no active holder).
//
// This slice (S2) writes the ledger on pickup; the reconciliation and retention
// reads land in their own slices.

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

// Download is one row of ops.field_pack_downloads -- a single pickup of a cell
// version by a crew member. Append-only: a crew that re-pulls a newer version
// gets a new row, so the table is a pickup history, not a current-holding state.
type Download struct {
	bun.BaseModel `bun:"table:ops.field_pack_downloads,alias:field_pack_downloads"`

	ID       string  `bun:"id,pk,nullzero"`
	UserID   string  `bun:"user_id"`
	PackID   string  `bun:"pack_id"`
	CellID   *string `bun:"cell_id"`
	DeviceID *string `bun:"device_id"`

	DownloadedAt time.Time `bun:"downloaded_at,nullzero"`
}

// recordDownload appends a pickup to the ledger. cellID ties the row to the cell
// (so the per-cell usage view survives a later pack-version reap), deviceID (may be
// empty) distinguishes one crew member's two tablets. A ledger write failure fails
// the pickup -- an unrecorded download would silently break the accounting the whole
// shared-pack model is built to provide.
func recordDownload(ctx context.Context, db bun.IDB, userID, packID string, cellID *string, deviceID string) error {
	dl := &Download{
		UserID: userID,
		PackID: packID,
		CellID: cellID,
	}
	if deviceID != "" {
		dl.DeviceID = &deviceID
	}
	_, err := db.NewInsert().Model(dl).Exec(ctx)
	return err
}
