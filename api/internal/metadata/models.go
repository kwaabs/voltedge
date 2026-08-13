// Package metadata is the shared read layer over the metadata.* tables
// (see docs/metadata-core.md) -- feature classes, fields, subtypes,
// domains. Both the schema compiler (api/internal/compiler) and the
// generic CRUD engine (api/internal/crud) load the same shape of data
// through this package rather than each keeping their own copy of what a
// Field or a Domain looks like.
package metadata

import (
	"time"

	"github.com/uptrace/bun"
)

// FeatureClass mirrors metadata.feature_classes -- see migrations/0007.
type FeatureClass struct {
	bun.BaseModel `bun:"table:metadata.feature_classes"`

	ID                 string  `bun:"id,pk"`
	Code               string  `bun:"code"`
	Name               string  `bun:"name"`
	GeometryType       string  `bun:"geometry_type"`
	SRID               *int    `bun:"srid"`
	NetworkRole        string  `bun:"network_role"`
	PhysicalTable      *string `bun:"physical_table"`
	Status             string  `bun:"status"`
	IsDiagramContainer bool    `bun:"is_diagram_container"`
	MetadataVersionID  *string `bun:"metadata_version_id"`
}

// Field mirrors metadata.fields -- see migrations/0008. Only rows with
// status = 'active' are ever compiled or accepted on a write; deprecated
// fields keep their physical column (if one exists) but are invisible to
// both the compiler and the CRUD engine.
type Field struct {
	bun.BaseModel `bun:"table:metadata.fields"`

	ID                   string  `bun:"id,pk"`
	FeatureClassID       string  `bun:"feature_class_id"`
	Name                 string  `bun:"name"`
	DataType             string  `bun:"data_type"`
	DomainID             *string `bun:"domain_id"`
	Nullable             bool    `bun:"nullable"`
	IsComputed           bool    `bun:"is_computed"`
	Status               string  `bun:"status"`
	VisibleOnCreate      bool    `bun:"visible_on_create"`
	VisibleOnUpdate      bool    `bun:"visible_on_update"`
	EditableAfterPublish bool    `bun:"editable_after_publish"`
}

// Subtype mirrors metadata.subtypes -- see migrations/0008.
type Subtype struct {
	bun.BaseModel `bun:"table:metadata.subtypes"`

	ID             string `bun:"id,pk"`
	FeatureClassID string `bun:"feature_class_id"`
	Code           int    `bun:"code"`
	Name           string `bun:"name"`
}

// SubtypeFieldRule mirrors metadata.subtype_field_rules -- see
// migrations/0008. A field with no rule row for a given subtype is
// optional by default; the CRUD engine's required-field validation on
// create walks these per the feature's subtype.
type SubtypeFieldRule struct {
	bun.BaseModel `bun:"table:metadata.subtype_field_rules"`

	ID              string  `bun:"id,pk"`
	SubtypeID       string  `bun:"subtype_id"`
	FieldID         string  `bun:"field_id"`
	IsRequired      bool    `bun:"is_required"`
	DefaultOverride *string `bun:"default_override"`
}

// Domain mirrors metadata.domains -- see migrations/0004.
type Domain struct {
	bun.BaseModel `bun:"table:metadata.domains"`

	ID         string `bun:"id,pk"`
	Code       string `bun:"code"`
	DomainType string `bun:"domain_type"`
}

// DomainValue mirrors metadata.domain_values -- see migrations/0004.
type DomainValue struct {
	bun.BaseModel `bun:"table:metadata.domain_values"`

	ID       string `bun:"id,pk"`
	DomainID string `bun:"domain_id"`
	Code     string `bun:"code"`
}

// Terminal mirrors metadata.terminals -- see migrations/0009. A named,
// logical connection point on a junction feature class (e.g. DIST_XFMR's
// T1/T2/T3) -- not a separate geometry.
type Terminal struct {
	bun.BaseModel `bun:"table:metadata.terminals"`

	ID             string  `bun:"id,pk"`
	FeatureClassID string  `bun:"feature_class_id"`
	SubtypeID      *string `bun:"subtype_id"`
	Code           string  `bun:"code"`
	Role           string  `bun:"role"`
	PhaseCapable   bool    `bun:"phase_capable"`
}

// NetworkRule mirrors metadata.network_rules -- see migrations/0011. One
// row = one legal electrical connectivity pattern. Absence of a matching
// row -- or a matching row with IsValid = false -- is itself the
// rejection; see api/internal/connectivity.
type NetworkRule struct {
	bun.BaseModel `bun:"table:metadata.network_rules"`

	ID                 string  `bun:"id,pk"`
	Name               string  `bun:"name"`
	FromFeatureClassID string  `bun:"from_feature_class_id"`
	FromSubtypeID      *string `bun:"from_subtype_id"`
	FromTerminalID     *string `bun:"from_terminal_id"`
	ToFeatureClassID   string  `bun:"to_feature_class_id"`
	ToSubtypeID        *string `bun:"to_subtype_id"`
	ToTerminalID       *string `bun:"to_terminal_id"`
	PhaseRule          *string `bun:"phase_rule"`
	IsValid            bool    `bun:"is_valid"`
}

// RelationshipClass mirrors metadata.relationship_classes -- see
// migrations/0010. Defines a legal structural/spatial relationship
// between two feature classes (e.g. POLE supports MV_CONDUCTOR). Unlike
// NetworkRule, there's no IsValid flag -- legality is simply whether a
// row exists for the origin/destination pairing at all; see
// api/internal/relationship.
type RelationshipClass struct {
	bun.BaseModel `bun:"table:metadata.relationship_classes"`

	ID                        string `bun:"id,pk"`
	Name                      string `bun:"name"`
	OriginFeatureClassID      string `bun:"origin_feature_class_id"`
	DestinationFeatureClassID string `bun:"destination_feature_class_id"`
	Kind                      string `bun:"kind"`
	Cardinality               string `bun:"cardinality"`
}

// TraceConfiguration mirrors metadata.trace_configurations -- see
// migrations/0014. A named, reusable trace profile (e.g. "Downstream
// trace"): api/internal/trace loads one by its stable Code, resolves
// DirectionValueID to a domain_values.code (CONNECTED/DOWNSTREAM/
// UPSTREAM/SHORTEST_PATH), then walks runtime.feature_connectivity
// according to it. Isolation and loop-detection traces aren't separate
// rows here -- the Go service composes two primitive traces and
// post-processes the result.
type TraceConfiguration struct {
	bun.BaseModel `bun:"table:metadata.trace_configurations"`

	ID                string `bun:"id,pk"`
	Code              string `bun:"code"`
	Name              string `bun:"name"`
	DirectionValueID  string `bun:"direction_value_id"`
	StopAtOpenDevices bool   `bun:"stop_at_open_devices"`
	CrossTransformers bool   `bun:"cross_transformers"`
	RespectPhase      bool   `bun:"respect_phase"`
}

// TraceBarrierRule mirrors metadata.trace_barrier_rules -- see
// migrations/0014. One row = one condition that halts traversal for a
// given TraceConfiguration, e.g. "on DIST_SWITCH, if device_state
// equals OPEN, stop." Checked live against each hop's actual attribute
// value -- never baked into feature_connectivity itself.
type TraceBarrierRule struct {
	bun.BaseModel `bun:"table:metadata.trace_barrier_rules"`

	ID                   string `bun:"id,pk"`
	TraceConfigurationID string `bun:"trace_configuration_id"`
	FeatureClassID       string `bun:"feature_class_id"`
	FieldID              string `bun:"field_id"`
	HaltsOnValueID       string `bun:"halts_on_value_id"`
}

// Version mirrors metadata.metadata_versions -- see migrations/0006.
type Version struct {
	bun.BaseModel `bun:"table:metadata.metadata_versions"`

	// nullzero: without it, bun inserts Go's zero value "" literally
	// instead of omitting the column, which overrides the table's
	// gen_random_uuid() default with an empty string.
	ID          string    `bun:"id,pk,nullzero"`
	PublishedAt time.Time `bun:"published_at"`
	PublishedBy string    `bun:"published_by"`
	Status      string    `bun:"status"`
}
