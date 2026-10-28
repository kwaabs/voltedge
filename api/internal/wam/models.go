// Package wam is the Work & Asset Management operational store and engine
// (Phase 59). A defect or a work order is not a GIS feature drawn on the map;
// it is an operational record ABOUT the assets that are -- a problem found on a
// transformer, a job a crew executes to fix it, an inspection run on a cycle.
// So WAM owns its own `wam` schema (migration 0096) and this package, mirroring
// api/internal/oms in shape: hand-written tables + CRUD, deliberately outside
// the metadata compiler.
//
// Slice 1 is the core: asset defects and the one typed work order engine, with
// the defect->corrective-order link and the defect<-OMS incident link. Slice 2
// (migration 0098) adds inspection templates & execution: a reusable checklist
// filled out against an asset, where a failing checklist item auto-raises a
// defect -- feeding the same defect->corrective-order loop from the other end.
// Recurring maintenance plans (which auto-generate work orders) are a later slice.
package wam

import (
	"time"

	"github.com/uptrace/bun"
)

// Defect classification and lifecycle. A defect is the "what is wrong" record
// on an asset; severity drives triage, source records how it was found, and
// status walks open -> work_ordered (a corrective order was raised) ->
// resolved | dismissed.
const (
	SeverityLow      = "low"
	SeverityMedium   = "medium"
	SeverityHigh     = "high"
	SeverityCritical = "critical"

	DefectStatusOpen        = "open"
	DefectStatusWorkOrdered = "work_ordered"
	DefectStatusResolved    = "resolved"
	DefectStatusDismissed   = "dismissed"

	SourceInspection          = "inspection"
	SourceOutage              = "outage"
	SourceConditionMonitoring = "condition_monitoring"
	SourceAdHoc               = "ad_hoc"
	// Work-order origins (wam.work_orders.source, migration 0161): where a job was
	// raised from, so the board can be filtered by provenance.
	SourceValidation = "validation" // a data-quality / connectivity audit fix
	SourceVegetation = "vegetation" // a vegetation-clearance trim
)

// Work order type and lifecycle. The "one typed work order engine": every unit
// of work -- an inspection, a corrective repair, a preventive task, a
// construction job -- is a work order distinguished by WorkType, sharing one
// lifecycle: draft -> scheduled -> assigned -> in_progress -> completed |
// cancelled.
const (
	WorkTypeInspection   = "inspection"
	WorkTypeCorrective   = "corrective"
	WorkTypePreventive   = "preventive"
	WorkTypeConstruction = "construction"

	WorkStatusDraft      = "draft"
	WorkStatusScheduled  = "scheduled"
	WorkStatusAssigned   = "assigned"
	WorkStatusInProgress = "in_progress"
	WorkStatusCompleted  = "completed"
	WorkStatusCancelled  = "cancelled"

	PriorityLow    = "low"
	PriorityMedium = "medium"
	PriorityHigh   = "high"
	PriorityUrgent = "urgent"
)

// Inspection checklist response types and the rolled-up inspection outcome. A
// template item captures its answer as pass/fail, a rating, a number, or free
// text; any item can be marked failing, and a failing item that carries a
// fail_severity auto-raises a defect. OverallResult rolls the item outcomes up.
const (
	ResponseTypePassFail = "pass_fail"
	ResponseTypeRating   = "rating"
	ResponseTypeNumeric  = "numeric"
	ResponseTypeText     = "text"

	InspectionResultPass        = "pass"
	InspectionResultFail        = "fail"
	InspectionResultConditional = "conditional"
)

// Defect mirrors wam.defects -- one asset-health finding. AssetClass/AssetID
// name the affected feature (feature class code + id, bare refs not FKs).
// IncidentID optionally links to the OMS incident this defect explains
// (source='outage'). Longitude/Latitude are the JSON projection of the PostGIS
// `location` point: scan-only (populated from ST_X/ST_Y aliases), never written
// back through bun -- writes go through an explicit ST_SetSRID(ST_MakePoint(...))
// in the insert builder, the same split oms/crud use. ResolvedAt null = not yet
// resolved.
type Defect struct {
	bun.BaseModel `bun:"table:wam.defects"`

	ID          string     `bun:"id,pk" json:"id"`
	AssetClass  string     `bun:"asset_class" json:"asset_class"`
	AssetID     string     `bun:"asset_id" json:"asset_id"`
	DefectType  *string    `bun:"defect_type" json:"defect_type"`
	Severity    string     `bun:"severity" json:"severity"`
	Status      string     `bun:"status" json:"status"`
	Source      string     `bun:"source" json:"source"`
	IncidentID  *string    `bun:"incident_id" json:"incident_id"`
	Title       string     `bun:"title" json:"title"`
	Description *string    `bun:"description" json:"description"`
	Longitude   *float64   `bun:"longitude,scanonly" json:"longitude"`
	Latitude    *float64   `bun:"latitude,scanonly" json:"latitude"`
	ReportedBy  *string    `bun:"reported_by" json:"reported_by"`
	ReportedAt  time.Time  `bun:"reported_at" json:"reported_at"`
	ResolvedAt  *time.Time `bun:"resolved_at" json:"resolved_at"`
	Notes       *string    `bun:"notes" json:"notes"`
	CreatedAt   time.Time  `bun:"created_at" json:"created_at"`
	UpdatedAt   time.Time  `bun:"updated_at" json:"updated_at"`
}

// WorkOrder mirrors wam.work_orders -- the one typed unit of work. AssetClass/
// AssetID (bare refs, nullable) name the primary asset the work targets;
// DefectID links a corrective order back to the defect it addresses (real FK,
// same schema). AssignedToUserID names the internal field worker the order is
// assigned to (FK to iam.users); AssignedTo stays a free-text fallback label for
// external crews. Longitude/Latitude are the scan-only projection of `location`.
// CompletedAt null = not yet completed.
type WorkOrder struct {
	bun.BaseModel `bun:"table:wam.work_orders"`

	ID               string     `bun:"id,pk" json:"id"`
	Reference        *string    `bun:"reference" json:"reference"`
	WorkType         string     `bun:"work_type" json:"work_type"`
	Status           string     `bun:"status" json:"status"`
	Priority         string     `bun:"priority" json:"priority"`
	Title            string     `bun:"title" json:"title"`
	Description      *string    `bun:"description" json:"description"`
	AssetClass       *string    `bun:"asset_class" json:"asset_class"`
	AssetID          *string    `bun:"asset_id" json:"asset_id"`
	Longitude        *float64   `bun:"longitude,scanonly" json:"longitude"`
	Latitude         *float64   `bun:"latitude,scanonly" json:"latitude"`
	Source           *string    `bun:"source" json:"source,omitempty"`
	DefectID         *string    `bun:"defect_id" json:"defect_id"`
	IncidentID       *string    `bun:"incident_id" json:"incident_id"`
	AssignedTo       *string    `bun:"assigned_to" json:"assigned_to"`
	AssignedToUserID *string    `bun:"assigned_to_user_id" json:"assigned_to_user_id"`
	CrewID           *string    `bun:"crew_id" json:"crew_id"`
	SubcontractorID  *string    `bun:"subcontractor_id" json:"subcontractor_id"`
	ScheduledFor     *time.Time `bun:"scheduled_for" json:"scheduled_for"`
	DueBy            *time.Time `bun:"due_by" json:"due_by"`
	StartedAt        *time.Time `bun:"started_at" json:"started_at"`
	CompletedAt      *time.Time `bun:"completed_at" json:"completed_at"`
	CompletionNotes  *string    `bun:"completion_notes" json:"completion_notes"`
	// OriginSystem / ExternalID (migration 0182): a READ-ONLY MIRROR tag. Null origin
	// = a locally-owned order (editable, assignable, pushed out). A system_code =
	// mirrored from that external WAM — read-only here, no crew/fan-out, never
	// re-emitted. ExternalID is the external system's own order id (the upsert key).
	OriginSystem *string    `bun:"origin_system" json:"origin_system"`
	ExternalID   *string    `bun:"external_id" json:"external_id"`
	CreatedAt    time.Time  `bun:"created_at" json:"created_at"`
	UpdatedAt    time.Time  `bun:"updated_at" json:"updated_at"`

	// CrewName is the assigned crew's name, joined on read (scan-only) so the board
	// can show "Team B" instead of a uuid. Never written back through bun.
	CrewName *string `bun:"crew_name,scanonly" json:"crew_name"`
	// SubcontractorName is the tasked external firm's name, joined on read.
	SubcontractorName *string `bun:"subcontractor_name,scanonly" json:"subcontractor_name"`
}

// IsMirrored reports whether this work order is a read-only mirror of an external
// WAM's record (migration 0182) — its lifecycle is owned there, not here.
func (o *WorkOrder) IsMirrored() bool {
	return o.OriginSystem != nil && *o.OriginSystem != ""
}

// InspectionTemplate mirrors wam.inspection_templates -- a reusable checklist
// definition. Code is a stable slug; AssetClass (optional) scopes the checklist
// to a feature class. Items is populated by GetTemplate (bun:"-" -- loaded with
// a second query, not a column), left empty by the list query.
type InspectionTemplate struct {
	bun.BaseModel `bun:"table:wam.inspection_templates"`

	ID          string    `bun:"id,pk" json:"id"`
	Code        string    `bun:"code" json:"code"`
	Name        string    `bun:"name" json:"name"`
	Description *string   `bun:"description" json:"description"`
	AssetClass  *string   `bun:"asset_class" json:"asset_class"`
	Active      bool      `bun:"active" json:"active"`
	CreatedAt   time.Time `bun:"created_at" json:"created_at"`
	UpdatedAt   time.Time `bun:"updated_at" json:"updated_at"`

	Items []InspectionTemplateItem `bun:"-" json:"items,omitempty"`
}

// InspectionTemplateItem mirrors wam.inspection_template_items -- one ordered
// checklist question. FailSeverity, when set, makes a failing answer to this
// item auto-raise a defect at that severity; null = the item is informational.
type InspectionTemplateItem struct {
	bun.BaseModel `bun:"table:wam.inspection_template_items"`

	ID           string    `bun:"id,pk" json:"id"`
	TemplateID   string    `bun:"template_id" json:"template_id"`
	Position     int       `bun:"position" json:"position"`
	Prompt       string    `bun:"prompt" json:"prompt"`
	ResponseType string    `bun:"response_type" json:"response_type"`
	FailSeverity *string   `bun:"fail_severity" json:"fail_severity"`
	Guidance     *string   `bun:"guidance" json:"guidance"`
	CreatedAt    time.Time `bun:"created_at" json:"created_at"`
	UpdatedAt    time.Time `bun:"updated_at" json:"updated_at"`
}

// Inspection mirrors wam.inspections -- one execution of a checklist against an
// asset, usually while completing an inspection work order. AssetClass/AssetID
// (bare refs) name the inspected feature; OverallResult rolls up the item
// results. Longitude/Latitude are the scan-only projection of `location`. Items
// is populated by GetInspection (bun:"-"), empty on the list query.
type Inspection struct {
	bun.BaseModel `bun:"table:wam.inspections"`

	ID            string    `bun:"id,pk" json:"id"`
	WorkOrderID   *string   `bun:"work_order_id" json:"work_order_id"`
	TemplateID    *string   `bun:"template_id" json:"template_id"`
	AssetClass    string    `bun:"asset_class" json:"asset_class"`
	AssetID       string    `bun:"asset_id" json:"asset_id"`
	OverallResult string    `bun:"overall_result" json:"overall_result"`
	InspectedBy   *string   `bun:"inspected_by" json:"inspected_by"`
	InspectedAt   time.Time `bun:"inspected_at" json:"inspected_at"`
	Notes         *string   `bun:"notes" json:"notes"`
	Longitude     *float64  `bun:"longitude,scanonly" json:"longitude"`
	Latitude      *float64  `bun:"latitude,scanonly" json:"latitude"`
	CreatedAt     time.Time `bun:"created_at" json:"created_at"`
	UpdatedAt     time.Time `bun:"updated_at" json:"updated_at"`

	Items []InspectionItemResult `bun:"-" json:"items,omitempty"`
}

// InspectionItemResult mirrors wam.inspection_item_results -- one answered
// checklist question. Prompt/ResponseType are snapshotted off the template item
// (templates evolve; a historical record must not). Passed is the pass/fail
// outcome (null = not applicable / not answered as pass-fail). DefectID links
// the defect a failing, severity-bearing item auto-raised.
type InspectionItemResult struct {
	bun.BaseModel `bun:"table:wam.inspection_item_results"`

	ID             string    `bun:"id,pk" json:"id"`
	InspectionID   string    `bun:"inspection_id" json:"inspection_id"`
	TemplateItemID *string   `bun:"template_item_id" json:"template_item_id"`
	Position       int       `bun:"position" json:"position"`
	Prompt         string    `bun:"prompt" json:"prompt"`
	ResponseType   string    `bun:"response_type" json:"response_type"`
	Passed         *bool     `bun:"passed" json:"passed"`
	ValueText      *string   `bun:"value_text" json:"value_text"`
	ValueNumeric   *float64  `bun:"value_numeric" json:"value_numeric"`
	DefectID       *string   `bun:"defect_id" json:"defect_id"`
	Notes          *string   `bun:"notes" json:"notes"`
	CreatedAt      time.Time `bun:"created_at" json:"created_at"`
}
