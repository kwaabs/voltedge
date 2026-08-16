// Package metadata is the shared read layer over the metadata.* tables
// (see docs/metadata-core.md) -- feature classes, fields, subtypes,
// domains. Both the schema compiler (api/internal/compiler) and the
// generic CRUD engine (api/internal/crud) load the same shape of data
// through this package rather than each keeping their own copy of what a
// Field or a Domain looks like.
package metadata

import (
	"encoding/json"
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

	ID             string  `bun:"id,pk"`
	FeatureClassID string  `bun:"feature_class_id"`
	Name           string  `bun:"name"`
	DataType       string  `bun:"data_type"`
	DomainID       *string `bun:"domain_id"`
	// GroupID was left off this struct originally -- nothing before
	// api/internal/admin needed to read or write which form section a
	// field belongs in, only whether it has one at all indirectly via
	// featureClassSchemaResponse's own separate rendering (cmd/server's
	// main.go doesn't source this from the Field model).
	GroupID              *string `bun:"group_id"`
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

// Domain mirrors metadata.domains -- see migrations/0004. Name was left
// off this struct originally (nothing before api/internal/admin needed
// it -- every resolution here has only ever gone from a field to its
// domain's *values*, never displayed the domain's own label), even
// though the column is real and NOT NULL.
type Domain struct {
	bun.BaseModel `bun:"table:metadata.domains"`

	ID         string   `bun:"id,pk"`
	Code       string   `bun:"code"`
	Name       string   `bun:"name"`
	DomainType string   `bun:"domain_type"`
	RangeMin   *float64 `bun:"range_min"`
	RangeMax   *float64 `bun:"range_max"`
}

// DomainValue mirrors metadata.domain_values -- see migrations/0004.
// Label and SortOrder were left off this struct originally since nothing
// before api/internal/admin needed them (every existing consumer only
// ever needed the code); both are real, always-populated columns.
type DomainValue struct {
	bun.BaseModel `bun:"table:metadata.domain_values"`

	ID             string  `bun:"id,pk"`
	DomainID       string  `bun:"domain_id"`
	Code           string  `bun:"code"`
	Label          string  `bun:"label"`
	SortOrder      int     `bun:"sort_order"`
	MaterialSpecID *string `bun:"material_spec_id"`
}

// MaterialSpec mirrors metadata.material_specs -- see migrations/0021.
// A catalog of real physical/electrical properties (resistance,
// ampacity, ...) for one material+size combination, e.g. ACSR_50.
// Attaches to a coded domain value via DomainValue.MaterialSpecID
// rather than to a feature class directly -- the domain value a field
// already selects (e.g. MV_CONDUCTOR's conductor_spec = "ACSR 50mm^2")
// is what carries the real numbers, so nothing about fields, the
// compiler, or the CRUD engine needed to change to introduce this.
type MaterialSpec struct {
	bun.BaseModel `bun:"table:metadata.material_specs"`

	ID                 string   `bun:"id,pk"`
	Code               string   `bun:"code"`
	Name               string   `bun:"name"`
	Category           string   `bun:"category"`
	ResistanceOhmPerKm *float64 `bun:"resistance_ohm_per_km"`
	ReactanceOhmPerKm  *float64 `bun:"reactance_ohm_per_km"`
	AmpacityAmps       *float64 `bun:"ampacity_amps"`
	DiameterMm         *float64 `bun:"diameter_mm"`
	WeightKgPerKm      *float64 `bun:"weight_kg_per_km"`

	// The four columns below (migrations/0022) are conductor-intrinsic
	// inputs to a first-principles IEEE 738 ampacity calc, on top of the
	// static ampacity_amps catalog figure above. Environmental
	// conditions the same calc needs -- ambient temp, wind speed/angle,
	// solar gain -- are deliberately NOT modeled here: those vary per
	// calculation run, not per conductor, so they're request-time
	// parameters to the ampacity endpoint instead.
	MaxOperatingTempC             *float64 `bun:"max_operating_temp_c"`
	Emissivity                    *float64 `bun:"emissivity"`
	Absorptivity                  *float64 `bun:"absorptivity"`
	ResistanceTempCoefficientPerC *float64 `bun:"resistance_temp_coefficient_per_c"`
}

// FieldGroup mirrors metadata.field_groups -- see migrations/0005. A
// small, reusable, global set of attribute categories (general,
// electrical, maintenance, asset) that metadata.fields.group_id points
// at, not redefined per feature class.
type FieldGroup struct {
	bun.BaseModel `bun:"table:metadata.field_groups"`

	ID        string  `bun:"id,pk"`
	Code      string  `bun:"code"`
	Name      string  `bun:"name"`
	SortOrder int     `bun:"sort_order"`
	Icon      *string `bun:"icon"`
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

// SnappingRule mirrors metadata.snapping_rules -- see migrations/0013.
// Purely geometric (feeds OpenLayers' own Snap interaction, see
// snap-store.ts): proves two vertices are coincident while digitizing
// FromFeatureClassID, nothing more -- never implies connectivity
// (NetworkRule, above) or structural support (RelationshipClass) on its
// own. This table has existed since Phase 1 but was never read from Go
// until api/internal/rules -- the frontend's own snap toggle
// (snap-store.ts/snap-toggle-button.tsx) is a client-side on/off switch,
// not driven by this table's own per-class-pair tolerances.
type SnappingRule struct {
	bun.BaseModel `bun:"table:metadata.snapping_rules"`

	ID                 string  `bun:"id,pk"`
	FromFeatureClassID string  `bun:"from_feature_class_id"`
	FromSubtypeID      *string `bun:"from_subtype_id"`
	ToFeatureClassID   string  `bun:"to_feature_class_id"`
	ToSubtypeID        *string `bun:"to_subtype_id"`
	ToTerminalID       *string `bun:"to_terminal_id"`
	TargetTypeValueID  string  `bun:"target_type_value_id"`
	ToleranceM         float64 `bun:"tolerance_m"`
	Priority           int     `bun:"priority"`
	IsRequired         bool    `bun:"is_required"`
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

	ID                string  `bun:"id,pk"`
	Code              string  `bun:"code"`
	Name              string  `bun:"name"`
	DirectionValueID  string  `bun:"direction_value_id"`
	StopAtOpenDevices bool    `bun:"stop_at_open_devices"`
	CrossTransformers bool    `bun:"cross_transformers"`
	RespectPhase      bool    `bun:"respect_phase"`
	TargetClassID     *string `bun:"target_class_id"`
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

// SymbologyRule mirrors metadata.symbology_rules -- see migrations/0012.
// Never read by Martin and never baked into a tile -- resolved into a
// feature's actual style entirely client-side (or by a thin styling
// service later), most-specific-match-wins: (feature_class + subtype +
// domain_value) > (feature_class + subtype) > (feature_class +
// domain_value) > (feature_class alone, the mandatory default).
type SymbologyRule struct {
	bun.BaseModel `bun:"table:metadata.symbology_rules"`

	ID             string          `bun:"id,pk"`
	FeatureClassID string          `bun:"feature_class_id"`
	SubtypeID      *string         `bun:"subtype_id"`
	DomainValueID  *string         `bun:"domain_value_id"`
	RenderContext  string          `bun:"render_context"`
	MinZoom        *int            `bun:"min_zoom"`
	MaxZoom        *int            `bun:"max_zoom"`
	StyleDef       json.RawMessage `bun:"style_def"`
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
