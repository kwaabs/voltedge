package fieldpack

// Field-pack CELLS: the admin-defined shared coverage tiles behind shared,
// pre-built packs (migration 0131, docs/shared-field-packs-design.md). A cell is a
// DEFINITION (area + classes + extras, scoped to a territory); each build of it is
// a "cell version" -- an ops.field_packs row with kind='shared_base' + cell_id --
// the shared, published-only base many crews pull and reuse for weeks.
//
// This file is the cell DEFINITION store (CRUD) + the version lookup. The build
// itself reuses the existing queue + worker + builder via enqueueCellBuild
// (pack.go); the base/overlay split, pickup gating, scheduling and reconciliation
// land in later slices.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/uptrace/bun"
)

// Cell is one row of ops.field_pack_cells -- a shareable coverage tile. Bbox and
// FeatureClasses are stored as jsonb (mirroring PackScope), so building a version
// is just copying them into a field_packs scope.
type Cell struct {
	bun.BaseModel `bun:"table:ops.field_pack_cells,alias:field_pack_cells"`

	ID   string `bun:"id,pk,nullzero"`
	Name string `bun:"name"`

	// The access boundary: a crew may pull this cell only if their territory access
	// covers this id (enforced at pickup, a later slice). Nil = no restriction.
	TerritoryID *string `bun:"territory_id"`

	Bbox           []float64 `bun:"bbox,type:jsonb"`
	FeatureClasses []string  `bun:"feature_classes,type:jsonb"`

	IncludeConnectivity  bool `bun:"include_connectivity"`
	IncludeRelationships bool `bun:"include_relationships"`
	IncludeFaults        bool `bun:"include_faults"`
	IncludeTags          bool `bun:"include_tags"`

	// Schedule is a cron expression driving periodic rebuilds (a later slice); nil
	// = manual-only. Retention is the version-reaping policy ('manual' for now).
	Schedule  *string `bun:"schedule"`
	Retention string  `bun:"retention"`

	Active    bool      `bun:"active"`
	CreatedBy string    `bun:"created_by"`
	CreatedAt time.Time `bun:"created_at,nullzero"`
	UpdatedAt time.Time `bun:"updated_at,nullzero"`
}

// CellInput is the create/update payload for a cell -- the admin-editable fields.
// The pointers that aren't here (id/created_by/timestamps) are set by the store.
type CellInput struct {
	Name                 string
	TerritoryID          *string
	Bbox                 []float64
	FeatureClasses       []string
	IncludeConnectivity  bool
	IncludeRelationships bool
	IncludeFaults        bool
	IncludeTags          bool
	Schedule             *string
	Retention            string
	Active               bool
}

// listCells returns every cell, newest first -- the admin catalog.
func listCells(ctx context.Context, db bun.IDB) ([]Cell, error) {
	var cells []Cell
	err := db.NewSelect().Model(&cells).OrderExpr("created_at DESC").Scan(ctx)
	return cells, err
}

// getCell loads one cell by id. Returns sql.ErrNoRows when it doesn't exist.
func getCell(ctx context.Context, db bun.IDB, id string) (*Cell, error) {
	cell := new(Cell)
	if err := db.NewSelect().Model(cell).Where("id = ?", id).Scan(ctx); err != nil {
		return nil, err
	}
	return cell, nil
}

// createCell inserts a new cell definition and returns it.
func createCell(ctx context.Context, db bun.IDB, in CellInput, createdBy string) (*Cell, error) {
	cell := &Cell{
		Name:                 in.Name,
		TerritoryID:          in.TerritoryID,
		Bbox:                 in.Bbox,
		FeatureClasses:       in.FeatureClasses,
		IncludeConnectivity:  in.IncludeConnectivity,
		IncludeRelationships: in.IncludeRelationships,
		IncludeFaults:        in.IncludeFaults,
		IncludeTags:          in.IncludeTags,
		Schedule:             in.Schedule,
		Retention:            defaultRetention(in.Retention),
		Active:               in.Active,
		CreatedBy:            createdBy,
	}
	if _, err := db.NewInsert().Model(cell).Returning("*").Exec(ctx); err != nil {
		return nil, err
	}
	return cell, nil
}

// updateCell overwrites a cell's editable fields and bumps updated_at. Returns
// sql.ErrNoRows when the id doesn't exist.
func updateCell(ctx context.Context, db bun.IDB, id string, in CellInput) (*Cell, error) {
	bboxJSON, err := json.Marshal(in.Bbox)
	if err != nil {
		return nil, err
	}
	classesJSON, err := json.Marshal(in.FeatureClasses)
	if err != nil {
		return nil, err
	}
	res, err := db.NewUpdate().Model((*Cell)(nil)).
		Set("name = ?", in.Name).
		Set("territory_id = ?", in.TerritoryID).
		Set("bbox = ?", json.RawMessage(bboxJSON)).
		Set("feature_classes = ?", json.RawMessage(classesJSON)).
		Set("include_connectivity = ?", in.IncludeConnectivity).
		Set("include_relationships = ?", in.IncludeRelationships).
		Set("include_faults = ?", in.IncludeFaults).
		Set("include_tags = ?", in.IncludeTags).
		Set("schedule = ?", in.Schedule).
		Set("retention = ?", defaultRetention(in.Retention)).
		Set("active = ?", in.Active).
		Set("updated_at = now()").
		Where("id = ?", id).
		Exec(ctx)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, sql.ErrNoRows
	}
	return getCell(ctx, db, id)
}

// deleteCell removes a cell definition. Its built versions (ops.field_packs rows)
// keep their cell_id as NULL (ON DELETE SET NULL) -- they're reaped by the normal
// pack-admin delete / retention path, not cascaded here. Returns whether a row went.
func deleteCell(ctx context.Context, db bun.IDB, id string) (bool, error) {
	res, err := db.NewDelete().Model((*Cell)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// latestCellVersion returns the newest SUCCEEDED build of a cell (the base crews
// pull), or (nil, nil) when the cell has never built successfully.
func latestCellVersion(ctx context.Context, db bun.IDB, cellID string) (*FieldPack, error) {
	pack := new(FieldPack)
	err := db.NewSelect().Model(pack).
		Where("cell_id = ?", cellID).
		Where("status = 'succeeded'").
		OrderExpr("created_at DESC").
		Limit(1).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return pack, nil
}

// defaultRetention normalizes an empty/unknown retention mode to 'manual' (the
// migration's CHECK allows manual|keep_n|pinned; later slices add the others).
func defaultRetention(mode string) string {
	switch mode {
	case "keep_n", "pinned":
		return mode
	default:
		return "manual"
	}
}
