package oms

// Standalone planned-switching builder (DU switching-gap #2): a switching PROGRAM
// authored for planned work -- a maintenance isolation, a construction cut-over --
// that is NOT tied to an incident. It reuses the oms.switching_orders /
// oms.switching_steps tables and the same execution, clearance and live
// device_state machinery as the incident flow (migration 0186 just made
// incident_id nullable + added a booked window and purpose). An order with
// incident_id IS NULL is a standalone program. Where the incident flow only ever
// GENERATES one fixed order, this file is the author's surface: create (empty or
// seeded from a target device), list, edit/add/delete/reorder steps, retitle, and
// issue. Get / cancel / execute-step / clearance all already work order-keyed, so
// they are reused unchanged.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/trace"
	"github.com/uptrace/bun"
)

// stepPhases / stepActions are the accepted enum values (mirroring the table
// CHECKs) the builder validates a hand-authored step against.
var stepPhases = map[string]bool{StepPhaseIsolate: true, StepPhaseRestore: true}
var stepActions = map[string]bool{
	StepActionOpen: true, StepActionClose: true, StepActionEarth: true,
	StepActionVerify: true, StepActionNote: true,
}

// CreatePlannedSwitchingInput is the body for authoring a standalone program.
// Reference is its title (defaulted from the target device when blank and a
// device is given). TargetDevice, when set, seeds the program with the standard
// isolate+restore steps for operating that device; left nil, the program starts
// empty for the author to fill in step by step.
type CreatePlannedSwitchingInput struct {
	Reference    string
	Purpose      *string
	Notes        *string
	PlannedStart *time.Time
	PlannedEnd   *time.Time
	TargetDevice *trace.FeatureRef
}

// StepInput is one hand-authored or edited step. DeviceClass/DeviceID are
// required for an open/close action (execution flips that device) and optional
// otherwise.
type StepInput struct {
	Phase       string
	Action      string
	Instruction string
	DeviceClass *string
	DeviceID    *string
}

// UpdatePlannedSwitchingInput patches a standalone order's header. A nil field is
// left unchanged; Status, when set, may only move draft->issued (the human "issue
// this program" transition) -- every other status change goes through execution
// or cancel.
type UpdatePlannedSwitchingInput struct {
	Reference    *string
	Purpose      *string
	Notes        *string
	PlannedStart *time.Time
	PlannedEnd   *time.Time
	Status       *string
}

// CreatePlannedSwitching creates a standalone (incident-less) switching program.
func CreatePlannedSwitching(ctx context.Context, db *bun.DB, in CreatePlannedSwitchingInput) (*SwitchingOrder, error) {
	ref := strings.TrimSpace(in.Reference)

	var drafts []switchingStepDraft
	if in.TargetDevice != nil && in.TargetDevice.FeatureID != "" {
		d, err := buildIsolateRestoreDrafts(ctx, db, *in.TargetDevice)
		if err != nil {
			return nil, err
		}
		drafts = d
		if ref == "" {
			ref = fmt.Sprintf("Isolation of %s", deviceRefLabel(in.TargetDevice.FeatureClass, in.TargetDevice.FeatureID))
		}
	}
	if ref == "" {
		return nil, invalidf("a reference (title) is required for a program with no target device")
	}

	var out *SwitchingOrder
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		order := &SwitchingOrder{
			// IncidentID stays nil -- this is a standalone program.
			Reference:    &ref,
			Status:       SwitchingStatusDraft,
			Purpose:      trimToNil(in.Purpose),
			Notes:        trimToNil(in.Notes),
			PlannedStart: in.PlannedStart,
			PlannedEnd:   in.PlannedEnd,
		}
		if _, err := tx.NewInsert().Model(order).
			ExcludeColumn("id", "created_at", "updated_at").
			Returning("*").Exec(ctx); err != nil {
			return fmt.Errorf("oms: creating planned switching order: %w", err)
		}

		if len(drafts) > 0 {
			steps := make([]SwitchingStep, len(drafts))
			for i, d := range drafts {
				steps[i] = SwitchingStep{
					SwitchingOrderID: order.ID,
					StepNo:           i + 1,
					Phase:            d.phase,
					Action:           d.action,
					DeviceClass:      d.deviceClass,
					DeviceID:         d.deviceID,
					Instruction:      d.instruction,
				}
			}
			if _, err := tx.NewInsert().Model(&steps).ExcludeColumn("id", "created_at").Exec(ctx); err != nil {
				return fmt.Errorf("oms: inserting planned switching steps: %w", err)
			}
		}

		var lerr error
		out, lerr = getSwitchingOrder(ctx, tx, order.ID)
		return lerr
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListPlannedSwitching returns the standalone programs (incident_id IS NULL),
// newest first, each with its ordered steps.
func ListPlannedSwitching(ctx context.Context, db bun.IDB) ([]SwitchingOrder, error) {
	var orders []SwitchingOrder
	err := db.NewSelect().
		Model(&orders).
		Relation("Steps", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Order("step_no ASC")
		}).
		Where("so.incident_id IS NULL").
		Order("so.created_at DESC").
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("oms: listing planned switching orders: %w", err)
	}
	for i := range orders {
		if orders[i].Steps == nil {
			orders[i].Steps = []SwitchingStep{}
		}
	}
	return orders, nil
}

// UpdatePlannedSwitching patches a standalone order's header (title / purpose /
// notes / window, and the draft->issued transition). It refuses a completed or
// cancelled order.
func UpdatePlannedSwitching(ctx context.Context, db *bun.DB, orderID string, in UpdatePlannedSwitchingInput) (*SwitchingOrder, error) {
	var out *SwitchingOrder
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		order, err := getStandaloneOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if err := assertEditable(order); err != nil {
			return err
		}

		upd := tx.NewUpdate().Model((*SwitchingOrder)(nil)).Where("id = ?", orderID).Set("updated_at = now()")
		if in.Reference != nil {
			ref := strings.TrimSpace(*in.Reference)
			if ref == "" {
				return invalidf("reference may not be blank")
			}
			upd = upd.Set("reference = ?", ref)
		}
		if in.Purpose != nil {
			upd = upd.Set("purpose = ?", trimToNil(in.Purpose))
		}
		if in.Notes != nil {
			upd = upd.Set("notes = ?", trimToNil(in.Notes))
		}
		if in.PlannedStart != nil {
			upd = upd.Set("planned_start = ?", in.PlannedStart)
		}
		if in.PlannedEnd != nil {
			upd = upd.Set("planned_end = ?", in.PlannedEnd)
		}
		if in.Status != nil {
			if *in.Status != SwitchingStatusIssued || order.Status != SwitchingStatusDraft {
				return invalidf("status can only move from draft to issued here (use execute/cancel otherwise)")
			}
			upd = upd.Set("status = ?", SwitchingStatusIssued)
		}
		if _, err := upd.Exec(ctx); err != nil {
			return fmt.Errorf("oms: updating planned switching order %s: %w", orderID, err)
		}

		out, err = getSwitchingOrder(ctx, tx, orderID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// AddSwitchingStep appends a step to a standalone program (step_no = max+1).
func AddSwitchingStep(ctx context.Context, db *bun.DB, orderID string, in StepInput) (*SwitchingOrder, error) {
	if err := validateStepInput(in); err != nil {
		return nil, err
	}
	var out *SwitchingOrder
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		order, err := getStandaloneOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if err := assertEditable(order); err != nil {
			return err
		}

		var maxNo int
		if err := tx.NewSelect().Model((*SwitchingStep)(nil)).
			ColumnExpr("COALESCE(MAX(step_no), 0)").
			Where("switching_order_id = ?", orderID).
			Scan(ctx, &maxNo); err != nil {
			return fmt.Errorf("oms: finding last step_no for order %s: %w", orderID, err)
		}
		step := &SwitchingStep{
			SwitchingOrderID: orderID,
			StepNo:           maxNo + 1,
			Phase:            in.Phase,
			Action:           in.Action,
			DeviceClass:      in.DeviceClass,
			DeviceID:         in.DeviceID,
			Instruction:      strings.TrimSpace(in.Instruction),
		}
		if _, err := tx.NewInsert().Model(step).ExcludeColumn("id", "created_at").Exec(ctx); err != nil {
			return fmt.Errorf("oms: inserting switching step: %w", err)
		}

		out, err = getSwitchingOrder(ctx, tx, orderID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// UpdateSwitchingStep edits one not-yet-executed step of a standalone program.
func UpdateSwitchingStep(ctx context.Context, db *bun.DB, stepID string, in StepInput) (*SwitchingOrder, error) {
	if err := validateStepInput(in); err != nil {
		return nil, err
	}
	var out *SwitchingOrder
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		order, step, err := loadStandaloneStep(ctx, tx, stepID)
		if err != nil {
			return err
		}
		if err := assertEditable(order); err != nil {
			return err
		}
		if step.ExecutedAt != nil {
			return invalidf("step %d has already been executed; revert it before editing", step.StepNo)
		}
		if _, err := tx.NewUpdate().Model((*SwitchingStep)(nil)).
			Set("phase = ?", in.Phase).
			Set("action = ?", in.Action).
			Set("instruction = ?", strings.TrimSpace(in.Instruction)).
			Set("device_class = ?", in.DeviceClass).
			Set("device_id = ?", in.DeviceID).
			Where("id = ?", stepID).Exec(ctx); err != nil {
			return fmt.Errorf("oms: updating switching step %s: %w", stepID, err)
		}
		out, err = getSwitchingOrder(ctx, tx, order.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DeleteSwitchingStep removes a not-yet-executed step and compacts the remaining
// step_no values so they stay a 1..n run.
func DeleteSwitchingStep(ctx context.Context, db *bun.DB, stepID string) (*SwitchingOrder, error) {
	var out *SwitchingOrder
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		order, step, err := loadStandaloneStep(ctx, tx, stepID)
		if err != nil {
			return err
		}
		if err := assertEditable(order); err != nil {
			return err
		}
		if step.ExecutedAt != nil {
			return invalidf("step %d has already been executed; revert it before deleting", step.StepNo)
		}
		if _, err := tx.NewDelete().Model((*SwitchingStep)(nil)).Where("id = ?", stepID).Exec(ctx); err != nil {
			return fmt.Errorf("oms: deleting switching step %s: %w", stepID, err)
		}
		// Close the gap: every later step drops one number.
		if _, err := tx.NewUpdate().Model((*SwitchingStep)(nil)).
			Set("step_no = step_no - 1").
			Where("switching_order_id = ?", order.ID).
			Where("step_no > ?", step.StepNo).Exec(ctx); err != nil {
			return fmt.Errorf("oms: renumbering steps after delete: %w", err)
		}
		out, err = getSwitchingOrder(ctx, tx, order.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReorderSwitchingSteps sets the step order of a standalone program from a fully
// specified list of its step ids. The list must be exactly the order's steps
// (same set), so the result is an unambiguous 1..n renumber.
func ReorderSwitchingSteps(ctx context.Context, db *bun.DB, orderID string, orderedStepIDs []string) (*SwitchingOrder, error) {
	var out *SwitchingOrder
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		order, err := getStandaloneOrder(ctx, tx, orderID)
		if err != nil {
			return err
		}
		if err := assertEditable(order); err != nil {
			return err
		}
		if len(orderedStepIDs) != len(order.Steps) {
			return invalidf("the reorder list must name every step exactly once (%d given, %d in the order)", len(orderedStepIDs), len(order.Steps))
		}
		have := make(map[string]bool, len(order.Steps))
		for i := range order.Steps {
			have[order.Steps[i].ID] = true
		}
		for _, id := range orderedStepIDs {
			if !have[id] {
				return invalidf("step %q is not part of this order", id)
			}
			delete(have, id) // also catches duplicates (second hit misses)
		}
		if len(have) != 0 {
			return invalidf("the reorder list is missing some of the order's steps")
		}
		// Two-pass to dodge the UNIQUE(order, step_no) collision: park at negative
		// numbers, then set the finals.
		for i, id := range orderedStepIDs {
			if _, err := tx.NewUpdate().Model((*SwitchingStep)(nil)).
				Set("step_no = ?", -(i + 1)).Where("id = ?", id).Exec(ctx); err != nil {
				return fmt.Errorf("oms: reordering (park) step %s: %w", id, err)
			}
		}
		for i, id := range orderedStepIDs {
			if _, err := tx.NewUpdate().Model((*SwitchingStep)(nil)).
				Set("step_no = ?", i+1).Where("id = ?", id).Exec(ctx); err != nil {
				return fmt.Errorf("oms: reordering step %s: %w", id, err)
			}
		}
		out, err = getSwitchingOrder(ctx, tx, orderID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// --- helpers ---

// getStandaloneOrder loads a switching order and asserts it is a standalone
// (incident-less) program -- the builder refuses to edit an incident-backed order
// (that one is engine-generated and edited through the incident flow).
func getStandaloneOrder(ctx context.Context, db bun.IDB, orderID string) (*SwitchingOrder, error) {
	order, err := getSwitchingOrder(ctx, db, orderID)
	if err != nil {
		return nil, err
	}
	if order.IncidentID != nil {
		return nil, invalidf("switching order %q belongs to an incident; edit it through the incident, not the planned-switching builder", orderID)
	}
	return order, nil
}

// loadStandaloneStep loads a step and its (standalone) parent order.
func loadStandaloneStep(ctx context.Context, db bun.IDB, stepID string) (*SwitchingOrder, *SwitchingStep, error) {
	step := new(SwitchingStep)
	if err := db.NewSelect().Model(step).Where("id = ?", stepID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, invalidf("switching step %q does not exist", stepID)
		}
		return nil, nil, fmt.Errorf("oms: loading switching step %s: %w", stepID, err)
	}
	order, err := getStandaloneOrder(ctx, db, step.SwitchingOrderID)
	if err != nil {
		return nil, nil, err
	}
	return order, step, nil
}

// assertEditable refuses structural edits to a completed or cancelled order (its
// program is history at that point).
func assertEditable(order *SwitchingOrder) error {
	switch order.Status {
	case SwitchingStatusCompleted:
		return invalidf("switching order %q is completed; its steps are a record and cannot be changed", order.ID)
	case SwitchingStatusCancelled:
		return invalidf("switching order %q is cancelled", order.ID)
	}
	return nil
}

// validateStepInput checks a hand-authored step's enums and that an open/close
// action names the device it will operate.
func validateStepInput(in StepInput) error {
	if !stepPhases[in.Phase] {
		return invalidf("phase must be %q or %q", StepPhaseIsolate, StepPhaseRestore)
	}
	if !stepActions[in.Action] {
		return invalidf("action must be one of open/close/earth/verify/note")
	}
	if strings.TrimSpace(in.Instruction) == "" {
		return invalidf("instruction is required")
	}
	if in.Action == StepActionOpen || in.Action == StepActionClose {
		if in.DeviceClass == nil || strings.TrimSpace(*in.DeviceClass) == "" ||
			in.DeviceID == nil || strings.TrimSpace(*in.DeviceID) == "" {
			return invalidf("an %s step must name the device it operates", in.Action)
		}
	}
	return nil
}
