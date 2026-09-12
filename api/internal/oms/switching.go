package oms

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/trace"
	"github.com/uptrace/bun"
)

// Phase 49 Slice 3 -- switching orders. The ordered open/verify/close program a
// crew executes to isolate an incident's work area and later restore supply.
// This formalises the isolate trace (DU backlog #2) into a persisted step list
// the operator ticks off, WITHOUT coupling to the live network: executing a step
// records executed_at, it does not flip any feature's operational_state (a later
// slice can add that tie-in). The generator reuses the same barrier-free
// class walks the prediction engine uses (api/internal/trace/class_walk.go), so
// switching orders need no trace_configuration.

// SwitchingDeviceClasses is the set of feature classes that can serve as an
// isolation point -- what the generator looks for upstream of the operated
// device to place the source-side safety boundary. A package var (like
// ProtectiveDeviceClasses) so a deployment modelling switching differently can
// override it.
var SwitchingDeviceClasses = []string{"MV_SWITCH", "MV_ISOLATOR", "MV_RECLOSER", "MV_BREAKER"}

// deviceRefLabel is the short human tag for a device in a step instruction --
// class code plus the first 8 of its id, matching the web incident row's
// deviceLabel. (A later slice can resolve the real feature name.)
func deviceRefLabel(class, id string) string {
	if len(id) > 8 {
		id = id[:8]
	}
	return fmt.Sprintf("%s %s", class, id)
}

// GenerateSwitchingOrder builds a draft switching order for an incident from its
// operated device: an isolate half (open the device, verify the de-energized
// cone, earth the section) and a restore half (remove earths, close the device),
// enriched with the nearest upstream isolation point when one exists. It refuses
// an incident with no device (nothing to switch) or one that already has a live
// (non-cancelled) order, and won't generate for a terminal incident.
func GenerateSwitchingOrder(ctx context.Context, db *bun.DB, incidentID string) (*SwitchingOrder, error) {
	inc, err := GetIncident(ctx, db, incidentID)
	if err != nil {
		return nil, err
	}
	if inc.PredictedDeviceClass == nil || inc.PredictedDeviceID == nil {
		return nil, invalidf("incident %q has no device to switch; attribute it first", incidentID)
	}
	if inc.Status == StatusRestored || inc.Status == StatusCancelled {
		return nil, invalidf("incident %q is %s; cannot build a switching order for it", incidentID, inc.Status)
	}

	deviceClass := *inc.PredictedDeviceClass
	deviceID := *inc.PredictedDeviceID
	device := trace.FeatureRef{FeatureClass: deviceClass, FeatureID: deviceID}
	deviceLabel := deviceRefLabel(deviceClass, deviceID)

	// De-energized cone size (for the verify step) and the nearest upstream
	// isolation point (for the source-side earth), both barrier-free.
	sps, err := DownstreamServicePointIDs(ctx, db, deviceClass, deviceID)
	if err != nil {
		return nil, err
	}
	upstream, err := trace.UpstreamFeaturesOfClass(ctx, db, device, SwitchingDeviceClasses)
	if err != nil {
		return nil, err
	}
	var boundary *trace.FeatureRef
	for i := range upstream {
		if upstream[i].FeatureID != deviceID { // skip the device itself if it matched
			boundary = &upstream[i]
			break
		}
	}

	// Build the step drafts in execution order.
	type stepDraft struct {
		phase, action, instruction string
		deviceClass, deviceID      *string
	}
	dc, di := deviceClass, deviceID
	var drafts []stepDraft

	// Isolate.
	drafts = append(drafts, stepDraft{StepPhaseIsolate, StepActionOpen,
		fmt.Sprintf("Open %s to isolate the work area.", deviceLabel), &dc, &di})
	drafts = append(drafts, stepDraft{StepPhaseIsolate, StepActionVerify,
		fmt.Sprintf("Verify %d service point(s) downstream are de-energized.", len(sps)), nil, nil})
	if boundary != nil {
		bc, bi := boundary.FeatureClass, boundary.FeatureID
		drafts = append(drafts, stepDraft{StepPhaseIsolate, StepActionEarth,
			fmt.Sprintf("Apply safety earths at the source-side boundary %s.", deviceRefLabel(bc, bi)), &bc, &bi})
	} else {
		drafts = append(drafts, stepDraft{StepPhaseIsolate, StepActionEarth,
			"Apply safety earths at the isolated section.", &dc, &di})
	}

	// Restore (reverse: earths off, then re-energize).
	drafts = append(drafts, stepDraft{StepPhaseRestore, StepActionNote,
		"Remove all safety earths and confirm the section is clear to re-energize.", nil, nil})
	drafts = append(drafts, stepDraft{StepPhaseRestore, StepActionClose,
		fmt.Sprintf("Close %s to restore supply.", deviceLabel), &dc, &di})

	var out *SwitchingOrder
	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var existing int
		if err := tx.NewSelect().
			Model((*SwitchingOrder)(nil)).
			ColumnExpr("count(*)").
			Where("incident_id = ?", incidentID).
			Where("status <> ?", SwitchingStatusCancelled).
			Scan(ctx, &existing); err != nil {
			return fmt.Errorf("oms: checking existing switching orders: %w", err)
		}
		if existing > 0 {
			return invalidf("incident %q already has a switching order; cancel it before regenerating", incidentID)
		}

		var orderID string
		if err := tx.QueryRowContext(ctx,
			`INSERT INTO oms.switching_orders (incident_id, reference, status) VALUES (?, ?, 'draft') RETURNING id`,
			incidentID, fmt.Sprintf("Isolation of %s", deviceLabel),
		).Scan(&orderID); err != nil {
			return fmt.Errorf("oms: creating switching order: %w", err)
		}

		steps := make([]SwitchingStep, len(drafts))
		for i, d := range drafts {
			steps[i] = SwitchingStep{
				SwitchingOrderID: orderID,
				StepNo:           i + 1,
				Phase:            d.phase,
				Action:           d.action,
				DeviceClass:      d.deviceClass,
				DeviceID:         d.deviceID,
				Instruction:      d.instruction,
			}
		}
		// Exclude id/created_at so the DB defaults (gen_random_uuid / now) fire
		// rather than bun inserting the Go zero values.
		if _, err := tx.NewInsert().Model(&steps).ExcludeColumn("id", "created_at").Exec(ctx); err != nil {
			return fmt.Errorf("oms: inserting switching steps: %w", err)
		}

		out, err = getSwitchingOrder(ctx, tx, orderID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// getSwitchingOrder loads one order with its steps ordered by step_no.
func getSwitchingOrder(ctx context.Context, db bun.IDB, id string) (*SwitchingOrder, error) {
	order := new(SwitchingOrder)
	err := db.NewSelect().
		Model(order).
		Relation("Steps", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Order("step_no ASC")
		}).
		Where("so.id = ?", id).
		Scan(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("switching order %q does not exist", id)
		}
		return nil, fmt.Errorf("oms: loading switching order %s: %w", id, err)
	}
	if order.Steps == nil {
		order.Steps = []SwitchingStep{}
	}
	return order, nil
}

// GetSwitchingOrder is the exported single-order read (with steps).
func GetSwitchingOrder(ctx context.Context, db bun.IDB, id string) (*SwitchingOrder, error) {
	return getSwitchingOrder(ctx, db, id)
}

// ListSwitchingOrders returns every switching order for an incident, newest
// first, each with its ordered steps.
func ListSwitchingOrders(ctx context.Context, db bun.IDB, incidentID string) ([]SwitchingOrder, error) {
	var orders []SwitchingOrder
	err := db.NewSelect().
		Model(&orders).
		Relation("Steps", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Order("step_no ASC")
		}).
		Where("so.incident_id = ?", incidentID).
		Order("so.created_at DESC").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("oms: listing switching orders for incident %s: %w", incidentID, err)
	}
	for i := range orders {
		if orders[i].Steps == nil {
			orders[i].Steps = []SwitchingStep{}
		}
	}
	return orders, nil
}

// SetStepExecuted marks a switching step done (executed=true, executed_at=now)
// or outstanding again (executed=false, executed_at=null). It nudges the parent
// order's status to reflect progress: the first executed step moves a draft/issued
// order to in_progress, and executing the last outstanding step completes it.
func SetStepExecuted(ctx context.Context, db *bun.DB, stepID string, executed bool) (*SwitchingOrder, error) {
	var out *SwitchingOrder
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var orderID string
		if err := tx.NewSelect().Model((*SwitchingStep)(nil)).
			ColumnExpr("switching_order_id").
			Where("id = ?", stepID).
			Scan(ctx, &orderID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return invalidf("switching step %q does not exist", stepID)
			}
			return fmt.Errorf("oms: loading switching step %s: %w", stepID, err)
		}

		upd := tx.NewUpdate().Model((*SwitchingStep)(nil)).Where("id = ?", stepID)
		if executed {
			upd = upd.Set("executed_at = now()")
		} else {
			upd = upd.Set("executed_at = NULL")
		}
		if _, err := upd.Exec(ctx); err != nil {
			return fmt.Errorf("oms: updating switching step %s: %w", stepID, err)
		}

		// Recompute the parent order's status from its steps.
		var counts struct {
			Total     int `bun:"total"`
			Executed  int `bun:"executed"`
		}
		if err := tx.NewSelect().Model((*SwitchingStep)(nil)).
			ColumnExpr("count(*) AS total").
			ColumnExpr("count(executed_at) AS executed").
			Where("switching_order_id = ?", orderID).
			Scan(ctx, &counts); err != nil {
			return fmt.Errorf("oms: counting switching steps for order %s: %w", orderID, err)
		}

		var status string
		switch {
		case counts.Total > 0 && counts.Executed == counts.Total:
			status = SwitchingStatusCompleted
		case counts.Executed > 0:
			status = SwitchingStatusInProgress
		default:
			status = SwitchingStatusIssued
		}
		if _, err := tx.NewUpdate().Model((*SwitchingOrder)(nil)).
			Set("status = ?", status).
			Set("updated_at = now()").
			Where("id = ?", orderID).
			// Don't resurrect a cancelled order.
			Where("status <> ?", SwitchingStatusCancelled).
			Exec(ctx); err != nil {
			return fmt.Errorf("oms: updating switching order %s status: %w", orderID, err)
		}

		loaded, err := getSwitchingOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		out = loaded
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CancelSwitchingOrder marks an order cancelled (steps are kept for the record).
func CancelSwitchingOrder(ctx context.Context, db *bun.DB, id string) (*SwitchingOrder, error) {
	order, err := GetSwitchingOrder(ctx, db, id)
	if err != nil {
		return nil, err
	}
	if order.Status == SwitchingStatusCancelled {
		return nil, invalidf("switching order %q is already cancelled", id)
	}
	if order.Status == SwitchingStatusCompleted {
		return nil, invalidf("switching order %q is completed and cannot be cancelled", id)
	}
	if _, err := db.NewUpdate().Model((*SwitchingOrder)(nil)).
		Set("status = ?", SwitchingStatusCancelled).
		Set("updated_at = now()").
		Where("id = ?", id).Exec(ctx); err != nil {
		return nil, fmt.Errorf("oms: cancelling switching order %s: %w", id, err)
	}
	return GetSwitchingOrder(ctx, db, id)
}
