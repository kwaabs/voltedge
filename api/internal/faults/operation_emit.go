package faults

import (
	"context"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/deviceops"
	"github.com/uptrace/bun"
)

// emitFaultTrip logs a device-operation 'trip' (api/internal/deviceops) for a
// fault whose protective device is known -- that device operated to clear the
// fault, which is exactly a trip. It's IDEMPOTENT per fault
// (source_system='fault', source_event_id=fault id): create-time and a later
// device-identifying update, or a replayed capture, never double-log; the first
// identified device wins (a subsequent device correction is tracked in the
// fault's own history, not re-logged as a second trip). A fault with no
// protective device yet is a no-op. Runs in the fault's own db handle, so on a
// tx-carrying caller it commits atomically with the fault write; an error fails
// the enclosing write (an operation we failed to log would be a silent gap).
func emitFaultTrip(ctx context.Context, db bun.IDB, f *FaultEvent) error {
	if f.ProtectiveDeviceClass == nil || f.ProtectiveDeviceID == nil ||
		strings.TrimSpace(*f.ProtectiveDeviceClass) == "" || strings.TrimSpace(*f.ProtectiveDeviceID) == "" {
		return nil
	}

	// Map the fault's detected_by to an operation source: a relay or SCADA report
	// is attributable; anything else is a human/analysis attribution (derived).
	source := "derived"
	if f.DetectedBy != nil {
		switch strings.TrimSpace(*f.DetectedBy) {
		case "protection_relay", "relay":
			source = "relay"
		case "scada":
			source = "scada"
		}
	}

	occurred := f.DetectedAt
	if occurred.IsZero() {
		occurred = time.Now()
	}

	// Prefer the fault's free-text cause, else its electrical classification.
	cause := f.Cause
	if cause == nil {
		cause = f.FaultType
	}

	faultID := f.ID
	system := "fault"
	_, err := deviceops.Record(ctx, db, deviceops.RecordInput{
		DeviceClass:   strings.TrimSpace(*f.ProtectiveDeviceClass),
		DeviceID:      strings.TrimSpace(*f.ProtectiveDeviceID),
		OpType:        "trip",
		Source:        source,
		OccurredAt:    occurred,
		Cause:         cause,
		FaultID:       &faultID,
		SourceSystem:  &system,
		SourceEventID: &faultID,
	})
	return err
}
