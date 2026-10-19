// Package safety is the operational-safety layer (migration 0151): the tags,
// grounds and clearances a control operator applies to protect a crew working on
// a de-energised section, and the interlock they feed. A BLOCKING tag
// (danger/out_of_service) on a device bars it from being operated -- the gate the
// OMS switching path consults before it flips a device's live device_state (wired
// in a later slice). It is the Oracle-NMS-style "safety document" VoltEdge lacked:
// isolation and switching existed, but nothing stopped a dispatcher from
// re-closing a device a crew is working behind.
//
// Records use bare feature/device refs (class + id, no FK), the same convention
// oms/faults/ops use, because a feature lives in its own per-class gis.<code>
// table. A clearance OWNS the tags and grounds that protect its work area (they
// carry its clearance_id); surrendering the clearance releases them.
package safety

import (
	"time"

	"github.com/uptrace/bun"
)

// Valid coded values, mirroring the CHECK constraints in migration 0151 --
// validated in the store so a bad value is a clean 400, not a raw constraint error.
var (
	tagTypes = map[string]bool{
		"danger": true, "out_of_service": true, "caution": true, "information": true,
	}
	// blockingTagTypes default blocks_operation=true: operating the device is barred
	// while such a tag is active. caution/information are advisory (warn only).
	blockingTagTypes = map[string]bool{"danger": true, "out_of_service": true}
)

// Status values shared across the records.
const (
	StatusActive    = "active"
	StatusRemoved   = "removed"
	ClearanceIssued      = "issued"
	ClearanceSurrendered = "surrendered"
	ClearanceCancelled   = "cancelled"
)

// Tag is one row of safety.tags -- a restriction placed on a device. BlocksOperation
// is true for danger/out_of_service (the gate refuses to operate the device) and
// false for caution/information (advisory). ClearanceID is set when the tag is part
// of a permit-to-work, null for an ad-hoc tag.
type Tag struct {
	bun.BaseModel `bun:"table:safety.tags,alias:t"`

	ID              string     `bun:"id,pk" json:"id"`
	DeviceClass     string     `bun:"device_class" json:"device_class"`
	DeviceID        string     `bun:"device_id" json:"device_id"`
	TagType         string     `bun:"tag_type" json:"tag_type"`
	BlocksOperation bool       `bun:"blocks_operation" json:"blocks_operation"`
	Reason          *string    `bun:"reason" json:"reason,omitempty"`
	ClearanceID     *string    `bun:"clearance_id" json:"clearance_id,omitempty"`
	Status          string     `bun:"status" json:"status"`
	PlacedBy        *string    `bun:"placed_by" json:"placed_by,omitempty"`
	PlacedAt        time.Time  `bun:"placed_at" json:"placed_at"`
	RemovedBy       *string    `bun:"removed_by" json:"removed_by,omitempty"`
	RemovedAt       *time.Time `bun:"removed_at" json:"removed_at,omitempty"`
	Notes           *string    `bun:"notes" json:"notes,omitempty"`
	CreatedAt       time.Time  `bun:"created_at,scanonly" json:"created_at"`
}

// Ground is one row of safety.grounds -- an applied safety earth at a de-energised
// network point. Must be removed before that section is re-energised.
type Ground struct {
	bun.BaseModel `bun:"table:safety.grounds,alias:g"`

	ID           string     `bun:"id,pk" json:"id"`
	FeatureClass string     `bun:"feature_class" json:"feature_class"`
	FeatureID    string     `bun:"feature_id" json:"feature_id"`
	Reason       *string    `bun:"reason" json:"reason,omitempty"`
	ClearanceID  *string    `bun:"clearance_id" json:"clearance_id,omitempty"`
	Status       string     `bun:"status" json:"status"`
	AppliedBy    *string    `bun:"applied_by" json:"applied_by,omitempty"`
	AppliedAt    time.Time  `bun:"applied_at" json:"applied_at"`
	RemovedBy    *string    `bun:"removed_by" json:"removed_by,omitempty"`
	RemovedAt    *time.Time `bun:"removed_at" json:"removed_at,omitempty"`
	Notes        *string    `bun:"notes" json:"notes,omitempty"`
	CreatedAt    time.Time  `bun:"created_at,scanonly" json:"created_at"`
}

// Clearance is one row of safety.clearances -- a permit-to-work over an isolated
// work area. Tags/Grounds are loaded together when a single clearance is fetched
// (bun has-many on clearance_id), never stored columns.
type Clearance struct {
	bun.BaseModel `bun:"table:safety.clearances,alias:c"`

	ID               string     `bun:"id,pk" json:"id"`
	Reference        string     `bun:"reference" json:"reference"`
	IncidentID       *string    `bun:"incident_id" json:"incident_id,omitempty"`
	SwitchingOrderID *string    `bun:"switching_order_id" json:"switching_order_id,omitempty"`
	WorkDescription  *string    `bun:"work_description" json:"work_description,omitempty"`
	IssuedTo         *string    `bun:"issued_to" json:"issued_to,omitempty"`
	Status           string     `bun:"status" json:"status"`
	RequestedBy      *string    `bun:"requested_by" json:"requested_by,omitempty"`
	RequestedAt      time.Time  `bun:"requested_at" json:"requested_at"`
	IssuedBy         *string    `bun:"issued_by" json:"issued_by,omitempty"`
	IssuedAt         *time.Time `bun:"issued_at" json:"issued_at,omitempty"`
	SurrenderedBy    *string    `bun:"surrendered_by" json:"surrendered_by,omitempty"`
	SurrenderedAt    *time.Time `bun:"surrendered_at" json:"surrendered_at,omitempty"`
	Notes            *string    `bun:"notes" json:"notes,omitempty"`
	CreatedAt        time.Time  `bun:"created_at,scanonly" json:"created_at"`
	UpdatedAt        time.Time  `bun:"updated_at,scanonly" json:"updated_at"`

	Tags    []Tag    `bun:"rel:has-many,join:id=clearance_id" json:"tags,omitempty"`
	Grounds []Ground `bun:"rel:has-many,join:id=clearance_id" json:"grounds,omitempty"`
}
