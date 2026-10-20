// Package faults is the fault-event operational store and engine. A fault is an
// ELECTRICAL FAULT EVENT on the network -- an earth fault, a phase-to-phase
// short, a protection operation -- not a GIS feature drawn on the map. It is the
// causal hinge between the two subsystems that already exist: it EXPLAINS an OMS
// outage (the incident it caused) and TRIGGERS a WAM defect (the asset failure
// booked for repair). So faults owns its own `faults` schema (migration 0106)
// and this package, mirroring api/internal/oms and api/internal/wam in shape:
// hand-written tables + CRUD, deliberately outside the metadata compiler.
//
// Slice 1 is the core: the fault_events table with CRUD, plus the two composites
// that wire a fault to its neighbours -- RaiseFaultFromIncident (mint a fault
// from an OMS outage) and BookDefectFromFault (mint a WAM defect + link it back).
// Reliability roll-ups (fault-caused SAIFI contribution) and a map overlay are
// later slices.
package faults

import (
	"time"

	"github.com/uptrace/bun"
)

// Fault type and lifecycle. FaultType is the electrical classification the
// fault-current / protection engines already speak. Status walks detected ->
// investigating -> cleared (off the network) -> closed (follow-up wrapped up).
const (
	FaultTypeSLG     = "SLG"     // single line-to-ground
	FaultTypeLL      = "LL"      // line-to-line
	FaultTypeLLG     = "LLG"     // double line-to-ground
	FaultTypeLLL     = "LLL"     // three-phase
	FaultTypeLLLG    = "LLLG"    // three-phase-to-ground
	FaultTypeEarth   = "earth"   // generic earth fault
	FaultTypeUnknown = "unknown" // not yet classified

	StatusDetected      = "detected"
	StatusInvestigating = "investigating"
	StatusCleared       = "cleared"
	StatusClosed        = "closed"
)

// FaultEvent mirrors faults.fault_events -- one electrical fault event.
// AssetClass/AssetID name the affected feature (feature class code + id, bare
// refs not FKs); both nullable, since a fault on a span or one known only by its
// incident may carry no asset. ProtectiveDeviceClass/ID name the device that
// operated to clear it. IncidentID links the OMS outage this fault caused;
// DefectID links the WAM defect it booked -- both bare cross-schema refs.
// Longitude/Latitude are the JSON projection of the PostGIS `location` point:
// scan-only (populated from ST_X/ST_Y aliases), never written back through bun
// -- writes go through an explicit ST_SetSRID(ST_MakePoint(...)) in the insert
// builder, the same split oms/wam/crud use. ClearedAt null = still on the
// network.
type FaultEvent struct {
	bun.BaseModel `bun:"table:faults.fault_events"`

	ID                    string     `bun:"id,pk" json:"id"`
	Reference             *string    `bun:"reference" json:"reference"`
	AssetClass            *string    `bun:"asset_class" json:"asset_class"`
	AssetID               *string    `bun:"asset_id" json:"asset_id"`
	Longitude             *float64   `bun:"longitude,scanonly" json:"longitude"`
	Latitude              *float64   `bun:"latitude,scanonly" json:"latitude"`
	FaultType             *string    `bun:"fault_type" json:"fault_type"`
	FaultMode             *string    `bun:"fault_mode" json:"fault_mode"`
	Cause                 *string    `bun:"cause" json:"cause"`
	FaultCurrentA         *float64   `bun:"fault_current_a" json:"fault_current_a"`
	ProtectiveDeviceClass *string    `bun:"protective_device_class" json:"protective_device_class"`
	ProtectiveDeviceID    *string    `bun:"protective_device_id" json:"protective_device_id"`
	ClearingTimeMS        *float64   `bun:"clearing_time_ms" json:"clearing_time_ms"`
	Status                string     `bun:"status" json:"status"`
	DetectedBy            *string    `bun:"detected_by" json:"detected_by"`
	DetectedAt            time.Time  `bun:"detected_at" json:"detected_at"`
	ClearedAt             *time.Time `bun:"cleared_at" json:"cleared_at"`
	IncidentID            *string    `bun:"incident_id" json:"incident_id"`
	DefectID              *string    `bun:"defect_id" json:"defect_id"`
	Title                 string     `bun:"title" json:"title"`
	Description           *string    `bun:"description" json:"description"`
	Notes                 *string    `bun:"notes" json:"notes"`
	CreatedAt             time.Time  `bun:"created_at" json:"created_at"`
	UpdatedAt             time.Time  `bun:"updated_at" json:"updated_at"`
}

// FaultMode mirrors faults.fault_modes -- one failure mode a given feature class
// can exhibit (migration 0154), the per-class FMEA catalog behind the fault
// form's class-aware cause picker. Dimension/TypicalCause/Effect/Severity are the
// light FMEA payload; Code is what FaultEvent.FaultMode stores.
type FaultMode struct {
	bun.BaseModel `bun:"table:faults.fault_modes"`

	ID               string `bun:"id,pk" json:"id"`
	FeatureClassCode string `bun:"feature_class_code" json:"feature_class_code"`
	Code             string `bun:"code" json:"code"`
	Label            string `bun:"label" json:"label"`
	Dimension        string `bun:"dimension" json:"dimension"`
	TypicalCause     *string `bun:"typical_cause" json:"typical_cause"`
	Effect           *string `bun:"effect" json:"effect"`
	Severity         string `bun:"severity" json:"severity"`
	SortOrder        int    `bun:"sort_order" json:"sort_order"`
	Active           bool   `bun:"active" json:"active"`
}
