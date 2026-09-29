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
	PhysicalTable       *string `bun:"physical_table"`
	Status              string  `bun:"status"`
	IsDiagramContainer  bool    `bun:"is_diagram_container"`
	IsDiagramPlaceable  bool    `bun:"is_diagram_placeable"`
	GeometryOptional    bool    `bun:"geometry_optional"`
	DiagramGeometryType *string `bun:"diagram_geometry_type"`
	MetadataVersionID   *string `bun:"metadata_version_id"`
	// Phase 30 map label config (free-form jsonb, nullable) -- passed
	// through untouched to the schema endpoint, like symbology style_def.
	// nullzero so a cleared config (nil RawMessage) writes SQL NULL rather
	// than a JSONB 'null' literal; a real object is non-zero and written
	// verbatim.
	LabelDef json.RawMessage `bun:"label_def,nullzero"`
	// Network is the class's nominal voltage namespace, "MV" or "LV", or nil
	// for structural / cross-network / unclassified classes (migration 0064,
	// Phase 37 Slice 2). Distinct from NetworkRole, which is the class's
	// connectivity role (edge/junction/container/none) -- orthogonal to
	// voltage. Scopes the circuit_id picker to same-network circuits. Like
	// label_def it's pure metadata (the compiler's DDL never reads it) that
	// just rides along on this struct-model select.
	Network *string `bun:"network"`
	// FunctionalRole is the class's ELECTRICAL FUNCTION (migration 0091): one of
	// source/consumer/passthrough/transformer/controller/node/sensor/ground, or
	// nil for a class with no electrical function (containers, civil assets,
	// furniture). Orthogonal to NetworkRole, which is the class's connectivity-
	// graph role (edge/junction/container/none) -- a DIST_XFMR and an
	// MV_CONDUCTOR are both network_role='edge' but functionally a transformer
	// versus a wire. Read by the supply-path trace (api/internal/trace/supply.go)
	// to identify serving transformers, switching devices and the source by role
	// rather than a hardcoded class-code list. Like label_def/network/category
	// it's pure metadata the compiler's DDL never reads; it rides along on this
	// struct-model select and passes straight through to the schema endpoint.
	FunctionalRole *string `bun:"functional_role"`
	// MinDisplayZoom / MaxDisplayZoom are the class's map display-zoom band
	// (migration 0090): the geographic map hides the whole feature (glyph +
	// label) below MinDisplayZoom or above MaxDisplayZoom. nil on either side
	// means no bound; both nil (the default) is "draw at every zoom". Pure
	// map-render metadata like label_def/network/category -- the compiler's
	// DDL never reads it, it just rides along on this struct-model select and
	// passes straight through to the schema endpoint. Distinct from a symbology
	// rule's own min_zoom/max_zoom (which pick a style, not visibility).
	MinDisplayZoom *int `bun:"min_display_zoom"`
	MaxDisplayZoom *int `bun:"max_display_zoom"`
	// Category is the admin-defined display grouping used to section the
	// map's "New feature" menu (migration 0074) -- free text, nil for an
	// uncategorised class (shown under "Other"). Pure presentation metadata:
	// unrelated to NetworkRole (topology) or Network (voltage), and the
	// compiler's DDL never reads it -- like label_def/network it just rides
	// along on this struct-model select.
	Category *string `bun:"category"`
	// TraceTier is the class's NETWORK TIER (migration 0126): its rank in the
	// vertical source-to-load hierarchy -- lower = closer to the source (grid
	// infeed = 0), higher = closer to the load, nil for an untiered class. Read
	// by api/internal/trace's bfs to keep a directional walk on-layer: a
	// DOWNSTREAM step may go to an equal-or-higher tier but never a strictly-lower
	// one (walking back toward the source), UPSTREAM mirrors it. nil is
	// transparent -- a step to or from an untiered class is never tier-blocked --
	// so the guard is a no-op until tiers are seeded. Orthogonal to FunctionalRole
	// (what a class DOES) and NetworkRole (its connectivity-graph role); like them
	// it's pure metadata the compiler's DDL never reads, riding along on this
	// struct-model select straight through to the schema endpoint.
	TraceTier *int `bun:"trace_tier"`
}

// DiagramGeom returns how this class is drawn on a diagram: its
// diagram_geometry_type override when set, else its geographic
// GeometryType (migration 0053). The busbar case sets the override to
// "line" so a point junction reads as a spanning bar on the one-line
// without changing the map shape.
func (fc *FeatureClass) DiagramGeom() string {
	if fc.DiagramGeometryType != nil && *fc.DiagramGeometryType != "" {
		return *fc.DiagramGeometryType
	}
	return fc.GeometryType
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

// DomainTransition mirrors metadata.domain_transitions -- see migration 0065.
// One row = one legal edge in a coded domain's state machine: a field backed by
// DomainID may change its value from FromValueID to ToValueID, and no other move
// between two set values is allowed. A domain with no rows is unconstrained --
// the default for every coded domain (device_state, phase, ...), so the update
// path enforces edges only when some exist. Phase 38 Slice 2 wires
// OPERATIONAL_STATE's lifecycle this way; the mechanism is generic. Value ids
// (not codes) are stored, the same FK shape trace_barrier_rules.halts_on_value_id
// uses; metadata.Load resolves them back to codes for the CRUD check.
type DomainTransition struct {
	bun.BaseModel `bun:"table:metadata.domain_transitions"`

	ID          string `bun:"id,pk"`
	DomainID    string `bun:"domain_id"`
	FromValueID string `bun:"from_value_id"`
	ToValueID   string `bun:"to_value_id"`
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

	// Zero-sequence series impedance per km (migrations/0071), the earth-return
	// counterparts of the two positive-sequence columns above -- read by the
	// single-line-to-ground calc (api/internal/powerflow/groundfault.go). NULL on
	// either falls back to the engine's 3x-positive-sequence default.
	ResistanceZeroOhmPerKm *float64 `bun:"resistance_zero_ohm_per_km"`
	ReactanceZeroOhmPerKm  *float64 `bun:"reactance_zero_ohm_per_km"`
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

	// migrations/0060 -- optional cost, bare numeric in the house currency.
	// CostPerKm suits the per-length line materials this table holds today;
	// UnitCost is a flat per-item price for a material catalogued per piece.
	// Nothing consumes these yet -- catalog data ready for a future estimate.
	CostPerKm *float64 `bun:"cost_per_km"`
	UnitCost  *float64 `bun:"unit_cost"`

	// Line geometry (migrations/0117) -- the per-conductor-type input the
	// Tier 2 coupled unbalanced load flow (api/internal/powerflow/carson.go)
	// needs to build the asymmetric 3x3 phase-impedance matrix from Carson's
	// equations. GmrPhaseMm is the phase wire's geometric mean radius; the
	// neutral trio (GmrNeutralMm/ResistanceNeutralOhmPerKm + PosN) drives the
	// Kron reduction when present, else the line is treated as 3-wire. Pos*
	// are wire positions on the structure (m) from any consistent origin --
	// only pairwise distances enter Carson. All NULL => the coupled solver
	// falls back to the decoupled per-phase path for that segment; no other
	// analysis reads these.
	GmrPhaseMm                 *float64 `bun:"gmr_phase_mm"`
	GmrNeutralMm               *float64 `bun:"gmr_neutral_mm"`
	ResistanceNeutralOhmPerKm  *float64 `bun:"resistance_neutral_ohm_per_km"`
	PosAX                      *float64 `bun:"pos_a_x_m"`
	PosAY                      *float64 `bun:"pos_a_y_m"`
	PosBX                      *float64 `bun:"pos_b_x_m"`
	PosBY                      *float64 `bun:"pos_b_y_m"`
	PosCX                      *float64 `bun:"pos_c_x_m"`
	PosCY                      *float64 `bun:"pos_c_y_m"`
	PosNX                      *float64 `bun:"pos_n_x_m"`
	PosNY                      *float64 `bun:"pos_n_y_m"`
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
	// ScopeToCircuit (migration 0069) confines the walk to the start feature's
	// stored circuit membership: when true, api/internal/trace admits a node
	// only when its live circuit_id equals the start's. Orthogonal to direction
	// and barriers, the same way TargetClassID is -- a "trace the circuit"
	// primitive layered on a CONNECTED walk.
	ScopeToCircuit bool `bun:"scope_to_circuit"`
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

// SymbologyIcon mirrors metadata.symbology_icons (migrations/0023) -- the
// SVG markup behind a SymbologyRule's style_def.icon_ref. Resolved
// client-side, never by Martin; see web/src/lib/symbology-icons.ts.
type SymbologyIcon struct {
	bun.BaseModel `bun:"table:metadata.symbology_icons"`

	IconRef string `bun:"icon_ref,pk"`
	SVG     string `bun:"svg"`
}

// DiagramStencil mirrors metadata.diagram_stencils (migration 0048) -- one
// entry in the diagram symbol stencil palette (Phase 24 Slice 2). IconRef is
// an FK into metadata.symbology_icons: the SVG artwork is shared with feature
// symbology rules, this row only marks the glyph as a palette stencil and
// gives it a label/category/order. Served whole (like SymbologyIcon) and
// resolved client-side.
type DiagramStencil struct {
	bun.BaseModel `bun:"table:metadata.diagram_stencils"`

	IconRef   string `bun:"icon_ref,pk"`
	Label     string `bun:"label"`
	Category  string `bun:"category"`
	SortOrder int    `bun:"sort_order"`
	// PromoteClass is the feature class code a stamped symbol of this stencil
	// can be promoted into (Phase 25, migration 0051), or nil when the stencil
	// is decoration only. A code, not an FK -- resolved at promote time.
	PromoteClass *string `bun:"promote_class"`
}

// PanelTemplate mirrors metadata.panel_templates -- see migration 0072. One
// device-fan-out recipe per (panel feature class × panel role): placing a panel
// of that class and role creates the device set its items list. Phase 42 Slice
// 2 wires MV_SWITCHGEAR_PANEL's per-role recipes; the mechanism is generic.
type PanelTemplate struct {
	bun.BaseModel `bun:"table:metadata.panel_templates"`

	ID                  string `bun:"id,pk"`
	PanelFeatureClassID string `bun:"panel_feature_class_id"`
	RoleValueID         string `bun:"role_value_id"`
	Name                string `bun:"name"`
}

// PanelTemplateItem mirrors metadata.panel_template_items -- see migration 0072.
// One device a template fans out: the class to create, its subtype, the default
// attribute values its required fields need (so the created feature passes the
// same validation a manual create does), and where it sits on the board mimic
// relative to the panel's own column (drawn by Phase 42 Slice 3).
type PanelTemplateItem struct {
	bun.BaseModel `bun:"table:metadata.panel_template_items"`

	ID                   string          `bun:"id,pk"`
	TemplateID           string          `bun:"template_id"`
	DeviceFeatureClassID string          `bun:"device_feature_class_id"`
	SubtypeCode          *int            `bun:"subtype_code"`
	Attributes           json.RawMessage `bun:"attributes"`
	Position             int             `bun:"position"`
	OffsetX              float64         `bun:"offset_x"`
	OffsetY              float64         `bun:"offset_y"`
	Rotation             float64         `bun:"rotation"`
}

// PanelTemplateDevice is one template item resolved for the fan-out: the device
// class Code (not id, so the caller can metadata.Load it), its subtype and
// decoded default attributes, and its mimic offset. LoadPanelTemplate returns
// these in position order -- the ready-to-create device list for one panel role.
type PanelTemplateDevice struct {
	DeviceFeatureClass string
	SubtypeCode        *int
	Attributes         map[string]any
	OffsetX            float64
	OffsetY            float64
	Rotation           float64
}

// RMUTemplateWay is one switched way resolved for the RMU fan-out: the way
// class Code (not id, so the caller can metadata.Load it), its subtype and
// decoded default attributes, the terminal wired to the internal bus, and its
// mimic offset. LoadRMUTemplate returns these in position order.
type RMUTemplateWay struct {
	WayFeatureClass string
	SubtypeCode     *int
	Attributes      map[string]any
	BusTerminal     string
	OffsetX         float64
	OffsetY         float64
	Rotation        float64

	// Cable describes the external conductor the fan-out creates on this way's
	// cable side and wires to CableTerminal (migration 0123). CableFeatureClass
	// is nil when the template item defines no cable -- the fan-out then skips
	// the cable step for this way, preserving the pre-0123 behaviour.
	// CableAttributes supplies the conductor's required-field defaults (e.g.
	// voltage_class, condition_rating) so the geometry-less create validates.
	CableFeatureClass *string
	CableSubtypeCode  *int
	CableAttributes   map[string]any
	CableTerminal     *string
}

// RMUTemplateResolved is one RMU configuration recipe ready for the fan-out:
// the single internal bus class to create once, plus the ways in position
// order. Unlike a panel template (containment only), it also names each way's
// bus-side terminal so the fan-out can wire the internal connectivity.
type RMUTemplateResolved struct {
	BusbarFeatureClass string
	Ways               []RMUTemplateWay
}

// PackagedSubItem is one internal component resolved for the packaged-substation
// fan-out: the component class Code (not id, so the caller can metadata.Load it),
// its subtype and decoded default attributes, the two terminals wiring it to the
// PREVIOUS component in the chain, and its mimic offset. OwnTerminal is the
// terminal on THIS component; UpstreamTerminal the terminal on the previous one;
// either may be nil (a terminal-less, bus-style leg), and both are unused on the
// first item (the MV incomer has no upstream to wire). LoadPackagedSubTemplate
// returns these in position (chain) order.
type PackagedSubItem struct {
	ComponentFeatureClass string
	SubtypeCode           *int
	Attributes            map[string]any
	OwnTerminal           *string
	UpstreamTerminal      *string
	OffsetX               float64
	OffsetY               float64
	Rotation              float64
}

// PackagedSubTemplateResolved is one packaged-substation configuration recipe
// ready for the fan-out: the internal components in chain (position) order. It
// carries no shared-bus header (unlike RMUTemplateResolved) because a packaged
// substation is a SERIES chain -- each component past the first wires to the
// previous one, not to a common bus.
type PackagedSubTemplateResolved struct {
	Items []PackagedSubItem
}

// AssemblyItem is one component resolved for the general kit-assembly fan-out
// (Phase 57, api/internal/assembly). Unlike PackagedSubItem it names BOTH a
// structural link and an electrical target independently, so the same recipe shape
// describes a star, a chain OR a branch. Position is the component's creation order
// (1..N; the anchor is conceptual position 0). SupportKind, when non-nil, is the
// relationship kind (e.g. "spatial_support") linking this item to
// SupportParentPosition (nil = the anchor); nil SupportKind = no structural link.
// WireToPosition, when non-nil, is which already-created feature this item connects
// to (0 = the anchor, >=1 = an earlier item); nil = not wired. UpstreamTerminal is
// the terminal on that target, OwnTerminal the terminal on THIS item; either may be
// nil for a terminal-less leg. LoadAssemblyTemplate returns these in position order.
type AssemblyItem struct {
	ComponentFeatureClass string
	SubtypeCode           *int
	Attributes            map[string]any
	SupportKind           *string
	SupportParentPosition *int
	WireToPosition        *int
	OwnTerminal           *string
	UpstreamTerminal      *string
	Position              int
	OffsetX               float64
	OffsetY               float64
	Rotation              float64
}

// AssemblyTemplateResolved is one kit recipe ready for the fan-out: the anchor
// subtype override (nil = the placing user picks) plus the components in creation
// (position) order.
type AssemblyTemplateResolved struct {
	AnchorSubtypeCode *int
	Items             []AssemblyItem
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
