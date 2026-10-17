package studies

import (
	"context"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// Allowed reports whether a feature of class fc may run study id: APPLICABLE by
// role (the code floor) AND not admin-DISABLED. The analysis endpoints call it to
// refuse a study the menu wouldn't have offered -- so the gate is authoritative,
// not merely a hidden button. An unknown study id is not allowed.
func Allowed(ctx context.Context, db bun.IDB, fc *metadata.FeatureClass, id string) (bool, error) {
	if !AppliesTo(id, fc) {
		return false, nil
	}
	disabled, err := LoadDisabled(ctx, db, fc.ID)
	if err != nil {
		return false, err
	}
	return !disabled[id], nil
}

// disabledRow mirrors metadata.feature_class_study_disabled (migration 0143).
type disabledRow struct {
	bun.BaseModel `bun:"table:metadata.feature_class_study_disabled"`

	FeatureClassID string `bun:"feature_class_id"`
	StudyID        string `bun:"study_id"`
}

// LoadDisabled returns the set of study ids an admin has pruned for one class,
// keyed for O(1) lookup. Empty when nothing is pruned.
func LoadDisabled(ctx context.Context, db bun.IDB, featureClassID string) (map[string]bool, error) {
	var rows []disabledRow
	if err := db.NewSelect().Model(&rows).
		Column("study_id").
		Where("feature_class_id = ?", featureClassID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("studies: loading disabled set: %w", err)
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[r.StudyID] = true
	}
	return out, nil
}

// SetDisabled replaces the whole pruned-study set for a class with exactly
// studyIDs (clear-then-insert, in one transaction). Only KNOWN, APPLICABLE study
// ids are stored -- disabling a study the floor already forbids is a no-op, and
// an unknown id is dropped, so the table never holds meaningless rows. An empty
// slice re-enables every applicable study.
func SetDisabled(ctx context.Context, db *bun.DB, featureClassID string, applicable func(id string) bool, studyIDs []string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().
			Model((*disabledRow)(nil)).
			Where("feature_class_id = ?", featureClassID).
			Exec(ctx); err != nil {
			return fmt.Errorf("studies: clearing disabled set: %w", err)
		}
		rows := make([]disabledRow, 0, len(studyIDs))
		seen := map[string]bool{}
		for _, id := range studyIDs {
			if id == "" || seen[id] || !Known(id) || !applicable(id) {
				continue
			}
			seen[id] = true
			rows = append(rows, disabledRow{FeatureClassID: featureClassID, StudyID: id})
		}
		if len(rows) == 0 {
			return nil
		}
		if _, err := tx.NewInsert().Model(&rows).Exec(ctx); err != nil {
			return fmt.Errorf("studies: setting disabled set: %w", err)
		}
		return nil
	})
}
