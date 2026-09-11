package attachment

import (
	"context"
	"time"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// featureExists reports whether a live (non-soft-deleted) feature row with this
// id exists in the class's physical table. An attachment may only hang off a real
// feature, so register checks this first. An uncompiled class (no physical_table)
// has no data table, so nothing can be attached to it -> false.
func featureExists(ctx context.Context, db *bun.DB, loaded *metadata.Loaded, id string) (bool, error) {
	if loaded.FeatureClass.PhysicalTable == nil {
		return false, nil
	}
	return db.NewSelect().
		TableExpr(*loaded.FeatureClass.PhysicalTable).
		ColumnExpr("1").
		Where("id = ?", id).
		Where("deleted_at IS NULL").
		Exists(ctx)
}

// insert writes a freshly-registered (pending) attachment row.
func insert(ctx context.Context, db *bun.DB, a *Attachment) error {
	_, err := db.NewInsert().Model(a).Exec(ctx)
	return err
}

// listReady returns a feature's committed attachments, newest first. Pending rows
// (registered but never committed) are deliberately excluded -- an in-flight or
// abandoned upload never shows in the panel.
func listReady(ctx context.Context, db *bun.DB, featureClass, featureID string) ([]Attachment, error) {
	var out []Attachment
	err := db.NewSelect().
		Model(&out).
		Where("feature_class = ?", featureClass).
		Where("feature_id = ?", featureID).
		Where("status = ?", StatusReady).
		Where("deleted_at IS NULL").
		Order("created_at DESC").
		Scan(ctx)
	return out, err
}

// get loads one live attachment scoped to its feature, so a caller can never
// address an attachment through the wrong feature's URL. Returns sql.ErrNoRows
// when absent (the handler maps that to 404).
func get(ctx context.Context, db *bun.DB, featureClass, featureID, id string) (*Attachment, error) {
	a := new(Attachment)
	err := db.NewSelect().
		Model(a).
		Where("id = ?", id).
		Where("feature_class = ?", featureClass).
		Where("feature_id = ?", featureID).
		Where("deleted_at IS NULL").
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// markReady flips a pending row to ready at commit, recording the true stored
// size and (for images) the thumbnail key. Only the columns the commit path
// actually settles are written.
func markReady(ctx context.Context, db *bun.DB, a *Attachment) error {
	a.Status = StatusReady
	a.UpdatedAt = time.Now()
	_, err := db.NewUpdate().
		Model(a).
		Column("status", "byte_size", "thumb_key", "updated_at").
		WherePK().
		Exec(ctx)
	return err
}

// softDelete hides an attachment (sets deleted_at); the RustFS objects are left
// for a later retention sweep, mirroring how feature deletes work. Idempotent:
// a row already deleted matches no live row and is a no-op.
func softDelete(ctx context.Context, db *bun.DB, id string) error {
	_, err := db.NewUpdate().
		Model((*Attachment)(nil)).
		Set("deleted_at = now()").
		Set("updated_at = now()").
		Where("id = ?", id).
		Where("deleted_at IS NULL").
		Exec(ctx)
	return err
}
