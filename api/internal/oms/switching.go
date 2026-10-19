package oms

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/deviceops"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/trace"
	"github.com/uptrace/bun"
)

// Live device_state (Phase 49 Slice 3, wired up 2026-09-24): executing an
// open/close switching step now flips the operated device's device_state so
// the connectivity graph -- every trace, the power-flow engine, and the outage
// map -- reflects what the crew has actually done. These are the codes the
// switch family's compiled device_state domain carries; a device class that
// doesn't model a live position (no device_state field) is left untouched.
const (
	deviceStateField  = "device_state"
	deviceStateOpen   = "OPEN"
	deviceStateClosed = "CLOSED"
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
var SwitchingDeviceClasses = []string{"MV_SWITCH", "MV_ISOLATOR", "MV_SECTIONALIZER", "MV_RECLOSER", "MV_BREAKER"}

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
	supplies, err := DownstreamSupplies(ctx, db, deviceClass, deviceID)
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
		fmt.Sprintf("Verify %d supply(ies) downstream are de-energized.", len(supplies)), nil, nil})
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
//
// Executing (or reverting) an open/close step also flips the operated device's
// live device_state (applyStepDeviceState), so the switching order actually
// moves the network -- traces, power flow and the outage map all read the new
// position. The whole thing runs in one transaction, so the step tick and the
// device flip commit together or not at all.
func SetStepExecuted(ctx context.Context, db *bun.DB, stepID string, executed bool) (*SwitchingOrder, error) {
	var out *SwitchingOrder
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		step := new(SwitchingStep)
		if err := tx.NewSelect().Model(step).
			Where("id = ?", stepID).
			Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return invalidf("switching step %q does not exist", stepID)
			}
			return fmt.Errorf("oms: loading switching step %s: %w", stepID, err)
		}
		orderID := step.SwitchingOrderID

		upd := tx.NewUpdate().Model((*SwitchingStep)(nil)).Where("id = ?", stepID)
		if executed {
			upd = upd.Set("executed_at = now()")
		} else {
			upd = upd.Set("executed_at = NULL")
		}
		if _, err := upd.Exec(ctx); err != nil {
			return fmt.Errorf("oms: updating switching step %s: %w", stepID, err)
		}

		// Move the network to match the operation the crew just recorded.
		if err := applyStepDeviceState(ctx, tx, step, executed); err != nil {
			return err
		}

		// A FRESH execution (not a re-tick) of an open/close step is a device
		// operation -- log it to the device-operation event log (deviceops) in
		// this same tx, so a planned switching op appears in the trip/operation
		// history alongside crew- and SCADA-sourced ones. The loaded `step` still
		// holds its pre-update ExecutedAt, so `== nil` means this call is the
		// transition into executed.
		if executed && step.ExecutedAt == nil {
			if err := emitStepOperation(ctx, tx, step); err != nil {
				return err
			}
		}

		// Recompute the parent order's status from its steps.
		var counts struct {
			Total    int `bun:"total"`
			Executed int `bun:"executed"`
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

// applyStepDeviceState flips the operated device's live device_state to match a
// switching step being executed or reverted, so the network graph reflects the
// crew's actions. Only open/close steps that name a device move state --
// verify/earth/note steps carry no device or don't change its position.
// Executing an "open" sets device_state=OPEN and executing a "close" sets
// CLOSED; reverting either (executed=false) restores the opposite, so ticking a
// step off and back on is a clean round trip. The write goes through
// crud.UpdateFeature rather than a raw column poke, so it is validated and
// history-logged (gis.feature_history) like any other edit -- auditable and
// reversible. A device class that models no live position (no device_state
// field) or a since-purged device is left untouched, never failing the step.
func applyStepDeviceState(ctx context.Context, db bun.IDB, step *SwitchingStep, executed bool) error {
	if step.DeviceClass == nil || step.DeviceID == nil {
		return nil
	}

	var target string
	switch step.Action {
	case StepActionOpen:
		if executed {
			target = deviceStateOpen
		} else {
			target = deviceStateClosed
		}
	case StepActionClose:
		if executed {
			target = deviceStateClosed
		} else {
			target = deviceStateOpen
		}
	default:
		return nil // verify/earth/note don't move a device's live position
	}

	loaded, err := metadata.Load(ctx, db, *step.DeviceClass)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // device class is gone; nothing to flip
		}
		return fmt.Errorf("oms: loading device class %s: %w", *step.DeviceClass, err)
	}
	if loaded.FieldByName(deviceStateField) == nil {
		return nil // this class doesn't model a live device_state
	}

	if _, err := crud.UpdateFeature(ctx, db, loaded, *step.DeviceID, crud.UpdateFeatureInput{
		Attributes: map[string]any{deviceStateField: target},
	}); err != nil {
		return fmt.Errorf("oms: setting %s %s device_state=%s: %w", *step.DeviceClass, *step.DeviceID, target, err)
	}
	return nil
}

// emitStepOperation logs a device-operation event (api/internal/deviceops) for a
// freshly-executed open/close switching step, so a planned operation lands in the
// device-operation log the same as a crew- or SCADA-sourced one. source is
// 'switching_order' and switching_step_id links back to the step. Only open/close
// steps name an operation (verify/earth/note don't move a device). A step with no
// device is a no-op. Runs in the step's tx -- an error rolls the whole thing back.
func emitStepOperation(ctx context.Context, db bun.IDB, step *SwitchingStep) error {
	if step.DeviceClass == nil || step.DeviceID == nil {
		return nil
	}
	var opType string
	switch step.Action {
	case StepActionOpen:
		opType = "open"
	case StepActionClose:
		opType = "close"
	default:
		return nil
	}
	stepID := step.ID
	_, err := deviceops.Record(ctx, db, deviceops.RecordInput{
		DeviceClass:     *step.DeviceClass,
		DeviceID:        *step.DeviceID,
		OpType:          opType,
		Source:          "switching_order",
		OccurredAt:      time.Now(),
		SwitchingStepID: &stepID,
	})
	return err
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
