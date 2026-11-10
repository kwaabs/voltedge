package oms

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/safety"
)

// switching_validate.go is the ADMS "study"/dry-run for a switching order: before an
// order is approved and executed, walk its steps IN ORDER over a SIMULATED device state
// (seeded from the live network) and report whether the sequence is sound. It proves
// the switching on paper -- the same checks the per-step execution interlock enforces
// at run time (switching.go), but over the whole plan at once and without touching the
// network.
//
// Per open/close step it checks: the device exists and models a switchable state; the
// device is in the EXPECTED pre-state in the simulation (open expects CLOSED, close
// expects OPEN -- a mismatch means the network isn't where the order assumes, or an
// earlier step is wrong); no safety tag/clearance blocks operating it. A step that
// targets a device already in its target state is flagged redundant (a warning, not a
// block). Ready is true when no ERROR-level finding stands -- the gate ApproveSwitching
// Order requires.

// ValidationFinding is one problem (or note) the study found. Severity "error" blocks
// approval; "warning" is advisory. StepNo/StepID point at the step it concerns (0/""
// for an order-level finding).
type ValidationFinding struct {
	StepID   string `json:"step_id,omitempty"`
	StepNo   int    `json:"step_no,omitempty"`
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// SwitchingValidation is the study result: the findings and whether the order is ready
// to approve (no error findings).
type SwitchingValidation struct {
	OrderID  string              `json:"order_id"`
	Ready    bool                `json:"ready"`
	Findings []ValidationFinding `json:"findings"`
}

const (
	sevError   = "error"
	sevWarning = "warning"
)

// ValidateSwitchingOrder runs the dry-run study over an order. Read-only: it never
// changes the order or the network.
func ValidateSwitchingOrder(ctx context.Context, db bun.IDB, orderID string) (*SwitchingValidation, error) {
	order, err := GetSwitchingOrder(ctx, db, orderID)
	if err != nil {
		return nil, err
	}
	out := &SwitchingValidation{OrderID: orderID, Findings: []ValidationFinding{}}
	add := func(stepID string, stepNo int, sev, code, msg string) {
		out.Findings = append(out.Findings, ValidationFinding{StepID: stepID, StepNo: stepNo, Severity: sev, Code: code, Message: msg})
	}

	if len(order.Steps) == 0 {
		add("", 0, sevError, "no_steps", "the switching order has no steps")
		out.Ready = false
		return out, nil
	}

	// Simulated device state, seeded lazily from the live network per device and then
	// advanced as the walk applies each step -- so step N is checked against what the
	// steps before it would have left, exactly as execution will see it.
	sim := make(map[string]string)
	key := func(class, id string) string { return class + "\x00" + id }

	for _, st := range order.Steps {
		if st.Action != StepActionOpen && st.Action != StepActionClose {
			continue // verify/earth/note move no device -- nothing to interlock
		}
		if st.DeviceClass == nil || st.DeviceID == nil {
			add(st.ID, st.StepNo, sevError, "no_device", fmt.Sprintf("step %d is an %s but names no device", st.StepNo, st.Action))
			continue
		}
		class, id := *st.DeviceClass, *st.DeviceID
		k := key(class, id)
		cur, seen := sim[k]
		if !seen {
			state, modelsState, exists, derr := deviceCurrentState(ctx, db, class, id)
			if derr != nil {
				return nil, derr
			}
			if !exists {
				add(st.ID, st.StepNo, sevError, "device_missing", fmt.Sprintf("step %d: %s is missing or removed", st.StepNo, deviceRefLabel(class, id)))
				continue
			}
			if !modelsState {
				add(st.ID, st.StepNo, sevError, "no_device_state", fmt.Sprintf("step %d: %s does not model a switchable device_state", st.StepNo, class))
				continue
			}
			cur = state
			sim[k] = cur
		}

		// Safety interlock -- a blocking danger/out-of-service tag or clearance.
		blocker, berr := safety.OperationBlocked(ctx, db, class, id)
		if berr != nil {
			return nil, berr
		}
		if blocker != nil {
			add(st.ID, st.StepNo, sevError, "safety_tag", fmt.Sprintf("step %d: a %q safety tag blocks operating %s", st.StepNo, blocker.TagType, deviceRefLabel(class, id)))
		}

		// Expected pre-state: open needs the device CLOSED, close needs it OPEN.
		expected, target := deviceStateClosed, deviceStateOpen
		if st.Action == StepActionClose {
			expected, target = deviceStateOpen, deviceStateClosed
		}
		switch {
		case cur == target:
			add(st.ID, st.StepNo, sevWarning, "redundant", fmt.Sprintf("step %d: %s is already %s -- the step is redundant", st.StepNo, deviceRefLabel(class, id), target))
		case cur != expected:
			add(st.ID, st.StepNo, sevError, "state_mismatch", fmt.Sprintf("step %d expects %s to be %s but it is %s -- the network is not where the order assumes", st.StepNo, deviceRefLabel(class, id), expected, cur))
		}
		sim[k] = target // advance the simulation past this step
	}

	out.Ready = true
	for _, f := range out.Findings {
		if f.Severity == sevError {
			out.Ready = false
			break
		}
	}
	return out, nil
}

// deviceCurrentState reads one device's live device_state. exists is false when the
// class or the feature is gone; modelsState is false when the class carries no
// device_state field. Both are tolerated (reported as findings), never fatal.
func deviceCurrentState(ctx context.Context, db bun.IDB, class, id string) (state string, modelsState, exists bool, err error) {
	loaded, err := metadata.Load(ctx, db, class)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, false, nil
		}
		return "", false, false, fmt.Errorf("oms: loading device class %s: %w", class, err)
	}
	if loaded.FieldByName(deviceStateField) == nil {
		return "", false, true, nil // class exists but models no switchable state
	}
	row, err := crud.GetFeature(ctx, db, loaded, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", true, false, nil // class models state, but the device is gone
		}
		return "", false, false, fmt.Errorf("oms: loading device %s %s: %w", class, id, err)
	}
	s, _ := row[deviceStateField].(string)
	return s, true, true, nil
}
