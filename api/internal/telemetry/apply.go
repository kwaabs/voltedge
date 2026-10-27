package telemetry

// apply.go is the "trust the field" action (blocker #1, slice 3): take a device's
// latest telemetry state and write it onto the MODEL's device_state, so the model
// catches up to reality. It uses the same validated, history-logged crud path
// switching uses, and records a device-operation sourced 'telemetry' for the
// audit trail -- but it deliberately does NOT go through the safety OPERATE
// interlock: this is recording that the device HAS moved in the field, not
// commanding it to move, so a danger tag doesn't bar the model from telling the
// truth. The operator chooses to apply (a disagreement is surfaced by
// reconciliation first); nothing auto-applies.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/deviceops"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

const deviceStateField = "device_state"

// ApplyResult is what an apply did: the device and the model state now set from
// telemetry.
type ApplyResult struct {
	FeatureClassCode string `json:"feature_class_code"`
	FeatureID        string `json:"feature_id"`
	DeviceState      string `json:"device_state"`
}

// ApplyTelemetry writes a device's latest telemetry state onto its model
// device_state (validated via crud.UpdateFeature) and records a telemetry-sourced
// device operation. Refuses when there's no telemetry, no state in it, or the
// class carries no device_state.
func ApplyTelemetry(ctx context.Context, db *bun.DB, class, id string) (*ApplyResult, error) {
	dt, err := GetDeviceTelemetry(ctx, db, class, id)
	if err != nil {
		return nil, err
	}
	if dt == nil {
		return nil, invalidf("no telemetry has been reported for %s/%s", class, id)
	}
	if dt.State == nil || strings.TrimSpace(*dt.State) == "" {
		return nil, invalidf("telemetry for %s/%s carries no state to apply", class, id)
	}
	state := strings.TrimSpace(*dt.State)

	loaded, err := metadata.Load(ctx, db, class)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("device class %q does not exist", class)
		}
		return nil, fmt.Errorf("telemetry: loading class %s: %w", class, err)
	}
	if loaded.FieldByName(deviceStateField) == nil {
		return nil, invalidf("%s does not model a device_state", class)
	}

	err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := crud.UpdateFeature(ctx, tx, loaded, id, crud.UpdateFeatureInput{
			Attributes: map[string]any{deviceStateField: state},
		}); err != nil {
			return fmt.Errorf("telemetry: setting %s %s device_state=%s: %w", class, id, state, err)
		}
		// Audit trail: a telemetry-sourced operation when the state is a switch
		// position (OPEN/CLOSED); a non-position state just updates the model.
		var opType string
		switch strings.ToUpper(state) {
		case "OPEN":
			opType = "open"
		case "CLOSED":
			opType = "close"
		}
		if opType != "" {
			if _, err := deviceops.Record(ctx, tx, deviceops.RecordInput{
				DeviceClass: class,
				DeviceID:    id,
				OpType:      opType,
				Source:      "telemetry",
				OccurredAt:  dt.MeasuredAt,
			}); err != nil {
				return fmt.Errorf("telemetry: recording operation for %s/%s: %w", class, id, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &ApplyResult{FeatureClassCode: class, FeatureID: id, DeviceState: state}, nil
}
