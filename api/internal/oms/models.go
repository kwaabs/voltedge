// Package oms is the Outage Management System operational store and engine
// (Phase 49). An outage is not a GIS feature drawn on the map; it is an
// operational transaction about the live network -- a customer rang in, the
// prediction engine blamed a protective device, N customers went dark, a crew
// restored it. So OMS owns its own `oms` schema (migration 0087) and this
// package, mirroring api/internal/circuit in shape: hand-written tables + CRUD,
// deliberately outside the metadata compiler.
//
// Slice 1a (this file) is the data model only. The prediction rollup
// (PredictIncidents), impact trace (AffectedCustomers), REST surface, and web
// UI arrive in later 1b-1d slices; reliability indices (SAIDI/SAIFI/CAIDI/ENS)
// in Slice 2.
package oms

import (
	"time"

	"github.com/uptrace/bun"
)

// Trouble-call type and lifecycle. A call may reference a known SERVICE_POINT
// or carry only a raw address / lat-lng (SP-optional intake); only a call with
// a resolved service point feeds the prediction rollup.
const (
	CallTypeNoSupply = "no_supply"
	CallTypePartial  = "partial"
	CallTypeFlicker  = "flicker"

	CallStatusOpen       = "open"       // reported, not yet rolled into an incident
	CallStatusAssociated = "associated" // linked to an incident
	CallStatusCleared    = "cleared"    // customer confirmed back / false alarm
	CallStatusCancelled  = "cancelled"
)

// Incident type and lifecycle. A prediction writes an incident at
// StatusPredicted and touches nothing in the live network; only a human
// confirm/restore advances it, and this phase keeps OMS a pure overlay (no
// operational_state coupling).
const (
	OutageTypeUnplanned = "unplanned"
	OutageTypePlanned   = "planned"

	StatusPredicted  = "predicted"
	StatusScheduled  = "scheduled" // planned outage, booked but not yet executed
	StatusConfirmed  = "confirmed"
	StatusDispatched = "dispatched"
	StatusRestored   = "restored"
	StatusCancelled  = "cancelled"
)

// Switching-order and step lifecycles (Slice 3). A switching order is the
// ordered open/close program a crew executes to isolate an incident's work area
// and later restore it; a step is one operation in that program.
const (
	SwitchingStatusDraft      = "draft"
	SwitchingStatusApproved   = "approved" // authorised (ADMS gate), ready to issue
	SwitchingStatusIssued     = "issued"
	SwitchingStatusInProgress = "in_progress"
	SwitchingStatusCompleted  = "completed"
	SwitchingStatusCancelled  = "cancelled"

	StepPhaseIsolate = "isolate"
	StepPhaseRestore = "restore"

	StepActionOpen   = "open"
	StepActionClose  = "close"
	StepActionEarth  = "earth"
	StepActionVerify = "verify"
	StepActionNote   = "note"
)

// TroubleCall mirrors oms.trouble_calls -- one customer report of no/partial
// supply. Longitude/Latitude are the JSON projection of the PostGIS `location`
// point: they are scan-only (populated from `ST_X(location)`/`ST_Y(location)`
// aliases by the query layer) and never written back through bun -- writes go
// through an explicit ST_SetSRID(ST_MakePoint(...)) in the insert builder, the
// same split the crud package uses for feature geometry.
type TroubleCall struct {
	bun.BaseModel `bun:"table:oms.trouble_calls"`

	ID             string    `bun:"id,pk" json:"id"`
	ServicePointID *string   `bun:"service_point_id" json:"service_point_id"`
	Address        *string   `bun:"address" json:"address"`
	Longitude      *float64  `bun:"longitude,scanonly" json:"longitude"`
	Latitude       *float64  `bun:"latitude,scanonly" json:"latitude"`
	ReportedAt     time.Time `bun:"reported_at" json:"reported_at"`
	CallerRef      *string   `bun:"caller_ref" json:"caller_ref"`
	// AccountRef / Phone carry the caller's CIS premise id and callback number when
	// an IVR/CIS customer_report filed the call (0179); null for an operator-logged
	// call, AMI last-gasp, or a map click. The CIS owns the customer model.
	AccountRef     *string   `bun:"account_ref" json:"account_ref"`
	Phone          *string   `bun:"phone" json:"phone"`
	CallType       string    `bun:"call_type" json:"call_type"`
	Status         string    `bun:"status" json:"status"`
	IncidentID     *string   `bun:"incident_id" json:"incident_id"`
	Notes          *string   `bun:"notes" json:"notes"`
	CreatedAt      time.Time `bun:"created_at" json:"created_at"`
	UpdatedAt      time.Time `bun:"updated_at" json:"updated_at"`
}

// Incident mirrors oms.incidents -- one inferred (or manually raised) outage.
// PredictedDeviceClass/PredictedDeviceID name the blamed protective device
// (feature class code + id), both nullable and not DB foreign keys. Confidence
// (0..1) is null for a manually raised incident. RestoredAt null = still out.
type Incident struct {
	bun.BaseModel `bun:"table:oms.incidents,alias:inc"`

	ID                    string     `bun:"id,pk" json:"id"`
	PredictedDeviceClass  *string    `bun:"predicted_device_class" json:"predicted_device_class"`
	PredictedDeviceID     *string    `bun:"predicted_device_id" json:"predicted_device_id"`
	CircuitID             *string    `bun:"circuit_id" json:"circuit_id"`
	// TerritoryID is the DOMINANT territory of the affected set (0169) -- the
	// board's scope/rollup key. TerritoryName is resolved by a LEFT JOIN on read
	// (scan-only, never stored), null when unassigned or the territory was removed.
	TerritoryID           *string    `bun:"territory_id" json:"territory_id"`
	TerritoryName         *string    `bun:"territory_name,scanonly" json:"territory_name"`
	OutageType            string     `bun:"outage_type" json:"outage_type"`
	CauseCode             *string    `bun:"cause_code" json:"cause_code"`
	// CauseStatus is how certain the cause is: unknown -> suspected -> confirmed
	// (0168). Always set (defaults 'unknown'), refined by a crew or a raised fault.
	CauseStatus string `bun:"cause_status" json:"cause_status"`
	// DetectionSource is HOW the utility learned of the outage (0168): customer /
	// ami / scada / crew / planned / upstream / analytics -- stamped by the raising
	// path, not inferred. DetectionDetail is an optional free provenance note
	// (channel, external id). DetectedAt is when the first signal arrived.
	DetectionSource *string    `bun:"detection_source" json:"detection_source"`
	DetectionDetail *string    `bun:"detection_detail" json:"detection_detail"`
	DetectedAt      *time.Time `bun:"detected_at" json:"detected_at"`
	Status          string     `bun:"status" json:"status"`
	Confidence            *float64   `bun:"confidence" json:"confidence"`
	StartedAt             time.Time  `bun:"started_at" json:"started_at"`
	PlannedStart          *time.Time `bun:"planned_start" json:"planned_start"`
	PlannedEnd            *time.Time `bun:"planned_end" json:"planned_end"`
	ETR                   *time.Time `bun:"etr" json:"etr"`
	RestoredAt            *time.Time `bun:"restored_at" json:"restored_at"`
	AffectedCustomers     *int       `bun:"affected_customers" json:"affected_customers"`
	AffectedServicePoints *int       `bun:"affected_service_points" json:"affected_service_points"`
	CrewRef               *string    `bun:"crew_ref" json:"crew_ref"`
	LastActorUserID       *string    `bun:"last_actor_user_id" json:"last_actor_user_id"`
	// LastActorName is the display name of last_actor_user_id, resolved by a LEFT
	// JOIN in the read queries (scan-only -- never a stored column). Null when no
	// actor, or when the acting account was since removed. It lets the board and
	// the webhook payload show "restored by Ama" without the viewer needing
	// admin:users to resolve the id themselves.
	LastActorName *string `bun:"last_actor_name,scanonly" json:"last_actor_name"`

	Notes     *string   `bun:"notes" json:"notes"`
	// OriginSystem / ExternalID (migration 0181): a READ-ONLY MIRROR tag. Null origin
	// = a locally-owned incident (editable, pushed out on change). A system_code =
	// mirrored from that external OMS — read-only in VoltEdge, and never re-emitted
	// outbound. ExternalID is the external system's own incident id (the upsert key).
	OriginSystem *string   `bun:"origin_system" json:"origin_system"`
	ExternalID   *string   `bun:"external_id" json:"external_id"`
	CreatedAt    time.Time `bun:"created_at" json:"created_at"`
	UpdatedAt    time.Time `bun:"updated_at" json:"updated_at"`
}

// IsMirrored reports whether this incident is a read-only mirror of an external
// OMS's record (migration 0181) — its lifecycle is owned there, not here.
func (i *Incident) IsMirrored() bool {
	return i.OriginSystem != nil && *i.OriginSystem != ""
}

// CustomerInterruption mirrors oms.customer_interruptions -- the per-SP fact the
// reliability indices sum over (Slice 2). Customers is the snapshot of
// SERVICE_POINT.customers_served at interruption time (a null on the SP was
// read as 1), frozen so re-modelling the field later can't retro-change a
// historical index. RestoredAt null = still interrupted.
type CustomerInterruption struct {
	bun.BaseModel `bun:"table:oms.customer_interruptions"`

	ID             string     `bun:"id,pk" json:"id"`
	IncidentID     string     `bun:"incident_id" json:"incident_id"`
	ServicePointID string     `bun:"service_point_id" json:"service_point_id"`
	Customers      int        `bun:"customers" json:"customers"`
	InterruptedAt  time.Time  `bun:"interrupted_at" json:"interrupted_at"`
	RestoredAt     *time.Time `bun:"restored_at" json:"restored_at"`
	CreatedAt      time.Time  `bun:"created_at" json:"created_at"`
}

// SwitchingOrder mirrors oms.switching_orders -- one open/close program a crew
// executes. IncidentID ties it to an incident (typically a planned outage); it is
// NULL for a STANDALONE planned switching program authored for planned work
// (migration 0186). PlannedStart/PlannedEnd (the booked window) and Purpose are
// carried by a standalone program -- an incident-backed order inherits that
// context from its incident, so they are null there. Steps holds its ordered
// operations when loaded together (bun `rel:has-many`, not a stored column).
type SwitchingOrder struct {
	bun.BaseModel `bun:"table:oms.switching_orders,alias:so"`

	ID           string          `bun:"id,pk" json:"id"`
	IncidentID   *string         `bun:"incident_id" json:"incident_id"`
	Reference    *string         `bun:"reference" json:"reference"`
	Status       string          `bun:"status" json:"status"`
	Purpose      *string         `bun:"purpose" json:"purpose"`
	PlannedStart *time.Time      `bun:"planned_start" json:"planned_start"`
	PlannedEnd   *time.Time      `bun:"planned_end" json:"planned_end"`
	Notes        *string         `bun:"notes" json:"notes"`
	ApprovedAt   *time.Time      `bun:"approved_at" json:"approved_at"`
	ApprovedBy   *string         `bun:"approved_by" json:"approved_by"`
	CreatedAt    time.Time       `bun:"created_at" json:"created_at"`
	UpdatedAt    time.Time       `bun:"updated_at" json:"updated_at"`
	Steps        []SwitchingStep `bun:"rel:has-many,join:id=switching_order_id" json:"steps"`
}

// SwitchingStep mirrors oms.switching_steps -- one operation in a switching
// order. DeviceClass/DeviceID name the operated device (bare refs, nullable: a
// verify/note step carries none). ExecutedAt is set when the operator marks the
// step done.
type SwitchingStep struct {
	bun.BaseModel `bun:"table:oms.switching_steps"`

	ID               string     `bun:"id,pk" json:"id"`
	SwitchingOrderID string     `bun:"switching_order_id" json:"switching_order_id"`
	StepNo           int        `bun:"step_no" json:"step_no"`
	Phase            string     `bun:"phase" json:"phase"`
	Action           string     `bun:"action" json:"action"`
	DeviceClass      *string    `bun:"device_class" json:"device_class"`
	DeviceID         *string    `bun:"device_id" json:"device_id"`
	Instruction      string     `bun:"instruction" json:"instruction"`
	ExecutedAt       *time.Time `bun:"executed_at" json:"executed_at"`
	CreatedAt        time.Time  `bun:"created_at" json:"created_at"`
}
