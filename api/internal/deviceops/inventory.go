package deviceops

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// InventoryDevice is one operable device in the device inventory: its class/id,
// current live state (device_state), territory, and its most recent operation
// (null when it has never operated). This is the device-CENTRIC report -- every
// protective/switching device and where it stands -- as opposed to the
// operation-centric log (List/Counts).
type InventoryDevice struct {
	DeviceClass  string     `bun:"device_class" json:"device_class"`
	DeviceID     string     `bun:"device_id" json:"device_id"`
	DeviceState  *string    `bun:"device_state" json:"device_state,omitempty"`
	TerritoryID  *string    `bun:"territory_id" json:"territory_id,omitempty"`
	LastOpType   *string    `bun:"last_op_type" json:"last_op_type,omitempty"`
	LastOpAt     *time.Time `bun:"last_op_at" json:"last_op_at,omitempty"`
}

// deviceClassRow is the metadata lookup: a class that models a live device_state
// (so it's an operable device -- breaker/switch/recloser/fuse) and is compiled
// (physical_table set). This derives the device taxonomy from the schema rather
// than a hard-coded class list, so a deployment that adds a device class gets it
// in the inventory for free.
type deviceClassRow struct {
	Code          string `bun:"code"`
	PhysicalTable string `bun:"physical_table"`
}

// InventoryFilter narrows the report. Empty fields are ignored.
type InventoryFilter struct {
	DeviceClass string // only this class
	State       string // only devices at this device_state (e.g. OPEN)
}

// Inventory lists every operable device with its current state and last
// operation. It discovers device classes from metadata (those with an active
// device_state field + a physical table), then reads each class's live rows
// joined to the latest operation. A class whose per-table read fails (a
// half-migrated table) is skipped rather than failing the whole report.
func Inventory(ctx context.Context, db bun.IDB, f InventoryFilter) ([]InventoryDevice, error) {
	var classes []deviceClassRow
	q := db.NewSelect().
		TableExpr("metadata.feature_classes AS fc").
		ColumnExpr("fc.code, fc.physical_table").
		Join("JOIN metadata.fields fld ON fld.feature_class_id = fc.id AND fld.name = 'device_state' AND fld.status = 'active'").
		Where("fc.status = 'active'").
		Where("fc.physical_table IS NOT NULL").
		OrderExpr("fc.code")
	if f.DeviceClass != "" {
		q = q.Where("fc.code = ?", f.DeviceClass)
	}
	if err := q.Scan(ctx, &classes); err != nil {
		return nil, err
	}

	out := []InventoryDevice{}
	for _, c := range classes {
		rows, err := inventoryForClass(ctx, db, c, f.State)
		if err != nil {
			// A single class's table being unreadable shouldn't sink the report.
			continue
		}
		out = append(out, rows...)
	}
	return out, nil
}

func inventoryForClass(ctx context.Context, db bun.IDB, c deviceClassRow, state string) ([]InventoryDevice, error) {
	rows := []InventoryDevice{}
	// physical_table is server-controlled metadata (e.g. "gis.mv_breaker"), the
	// same trust basis crud.physicalTable interpolates on. The class code binds as
	// a parameter.
	q := db.NewSelect().
		TableExpr(fmt.Sprintf("%s AS g", c.PhysicalTable)).
		ColumnExpr("? AS device_class", c.Code).
		ColumnExpr("g.id::text AS device_id").
		ColumnExpr("g.device_state::text AS device_state").
		ColumnExpr("r.territory_id::text AS territory_id").
		ColumnExpr("lo.op_type AS last_op_type").
		ColumnExpr("lo.occurred_at AS last_op_at").
		Join("JOIN runtime.feature_registry r ON r.feature_id = g.id AND r.deleted_at IS NULL AND r.feature_class_code = ?", c.Code).
		Join("LEFT JOIN LATERAL (SELECT op_type, occurred_at FROM ops.operations o WHERE o.device_class = ? AND o.device_id = g.id ORDER BY o.occurred_at DESC LIMIT 1) lo ON true", c.Code).
		OrderExpr("g.id")
	if state != "" {
		q = q.Where("g.device_state = ?", state)
	}
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
