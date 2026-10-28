// Package deviceops is the device-OPERATION event log (migration 0147): an
// append-only record of every time a protective/switching device operated on
// the network -- a trip, lockout, reclose, manual open/close, earth. It fills
// the gap the other surfaces leave: device_state is current-state-only,
// gis.feature_history is an untyped jsonb snapshot of a committed flip,
// oms.incidents is the customer outage, and faults.fault_events is a fault
// INVESTIGATION (not every operation is a fault). The log feeds from crew entry
// now and -- via correlations + the webhook ingestion engine's "operation"
// target (a later slice) -- from executed switching, booked faults, and SCADA.
package deviceops

import (
	"time"

	"github.com/uptrace/bun"
)

// opTypes / sources mirror the CHECK constraints in migration 0147. Validated in
// the store so a bad value is a clean 400 rather than a raw constraint violation.
var opTypes = map[string]bool{
	"trip": true, "lockout": true, "reclose": true, "open": true,
	"close": true, "earth": true, "alarm": true, "other": true,
}

var sources = map[string]bool{
	"crew": true, "scada": true, "relay": true,
	"switching_order": true, "derived": true, "telemetry": true, "other": true,
}

// Operation is one row of ops.operations -- one device operation. Bare device
// refs (device_class + device_id), no FK, matching oms/faults. The optional
// *_id correlations and source_* provenance are null for a plain manual entry.
type Operation struct {
	bun.BaseModel `bun:"table:ops.operations,alias:o"`

	ID          string    `bun:"id,pk" json:"id"`
	DeviceClass string    `bun:"device_class" json:"device_class"`
	DeviceID    string    `bun:"device_id" json:"device_id"`
	OpType      string    `bun:"op_type" json:"op_type"`
	Source      string    `bun:"source" json:"source"`
	OccurredAt  time.Time `bun:"occurred_at" json:"occurred_at"`
	Cause       *string   `bun:"cause" json:"cause,omitempty"`
	Notes       *string   `bun:"notes" json:"notes,omitempty"`

	FaultID         *string `bun:"fault_id" json:"fault_id,omitempty"`
	IncidentID      *string `bun:"incident_id" json:"incident_id,omitempty"`
	SwitchingStepID *string `bun:"switching_step_id" json:"switching_step_id,omitempty"`

	SourceSystem  *string `bun:"source_system" json:"source_system,omitempty"`
	SourceEventID *string `bun:"source_event_id" json:"source_event_id,omitempty"`

	ActorID   *string   `bun:"actor_id" json:"actor_id,omitempty"`
	CreatedAt time.Time `bun:"created_at,scanonly" json:"created_at"`
}
