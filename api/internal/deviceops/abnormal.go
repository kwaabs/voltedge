package deviceops

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// AbnormalDevice is one switching device whose CURRENT device_state contradicts its
// declared normal_state -- NORMALLY_OPEN but currently CLOSED, or NORMALLY_CLOSED
// but currently OPEN. Those are the points where the network is in an abnormal
// configuration right now (a feeder back-fed through a normally-open tie, a
// normally-closed sectionaliser left open, ...).
type AbnormalDevice struct {
	DeviceClass string     `bun:"device_class" json:"device_class"`
	DeviceID    string     `bun:"device_id" json:"device_id"`
	DeviceState string     `bun:"device_state" json:"device_state"`
	NormalState string     `bun:"normal_state" json:"normal_state"`
	TerritoryID *string    `bun:"territory_id" json:"territory_id,omitempty"`
	LastOpType  *string    `bun:"last_op_type" json:"last_op_type,omitempty"`
	LastOpAt    *time.Time `bun:"last_op_at" json:"last_op_at,omitempty"`
}

// AbnormalFilter narrows the report. Empty = every abnormal device.
type AbnormalFilter struct {
	DeviceClass string
}

// Abnormal lists every switching device currently OFF its normal position, across
// the network -- the "is the network in its normal configuration?" control-room
// view, the switching complement of the live telemetry reconcile. It discovers the
// classes that model BOTH device_state and normal_state (so only true switching
// devices with a declared posture qualify -- a fuse or a breaker with no
// normal_state is simply not in scope), then reads each class's live rows where the
// two disagree. A class whose per-table read fails is skipped, not fatal.
func Abnormal(ctx context.Context, db bun.IDB, f AbnormalFilter) ([]AbnormalDevice, error) {
	var classes []deviceClassRow
	q := db.NewSelect().
		TableExpr("metadata.feature_classes AS fc").
		ColumnExpr("fc.code, fc.physical_table").
		Join("JOIN metadata.fields ds ON ds.feature_class_id = fc.id AND ds.name = 'device_state' AND ds.status = 'active'").
		Join("JOIN metadata.fields ns ON ns.feature_class_id = fc.id AND ns.name = 'normal_state' AND ns.status = 'active'").
		Where("fc.status = 'active'").
		Where("fc.physical_table IS NOT NULL").
		OrderExpr("fc.code")
	if f.DeviceClass != "" {
		q = q.Where("fc.code = ?", f.DeviceClass)
	}
	if err := q.Scan(ctx, &classes); err != nil {
		return nil, err
	}

	out := []AbnormalDevice{}
	for _, c := range classes {
		rows, err := abnormalForClass(ctx, db, c)
		if err != nil {
			continue
		}
		out = append(out, rows...)
	}
	return out, nil
}

func abnormalForClass(ctx context.Context, db bun.IDB, c deviceClassRow) ([]AbnormalDevice, error) {
	rows := []AbnormalDevice{}
	// physical_table is server-controlled metadata, same trust basis the inventory
	// scan uses; the class code binds as a parameter. "Abnormal" = the declared
	// normal posture and the live position disagree.
	q := db.NewSelect().
		TableExpr(fmt.Sprintf("%s AS g", c.PhysicalTable)).
		ColumnExpr("? AS device_class", c.Code).
		ColumnExpr("g.id::text AS device_id").
		ColumnExpr("g.device_state::text AS device_state").
		ColumnExpr("g.normal_state::text AS normal_state").
		ColumnExpr("r.territory_id::text AS territory_id").
		ColumnExpr("lo.op_type AS last_op_type").
		ColumnExpr("lo.occurred_at AS last_op_at").
		Join("JOIN runtime.feature_registry r ON r.feature_id = g.id AND r.deleted_at IS NULL AND r.feature_class_code = ?", c.Code).
		Join("LEFT JOIN LATERAL (SELECT op_type, occurred_at FROM ops.operations o WHERE o.device_class = ? AND o.device_id = g.id ORDER BY o.occurred_at DESC LIMIT 1) lo ON true", c.Code).
		Where("(g.normal_state = 'NORMALLY_OPEN' AND g.device_state = 'CLOSED') OR (g.normal_state = 'NORMALLY_CLOSED' AND g.device_state = 'OPEN')").
		OrderExpr("g.id")
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
