package admin

import "encoding/json"

// The metadata.* CHECK constraints, mirrored here so a bad value gets a
// friendly 400 from this package's own validation instead of a raw
// Postgres constraint-violation error surfacing from the insert.
var (
	validGeometryTypes = map[string]bool{"point": true, "line": true, "polygon": true, "none": true}
	validNetworkRoles  = map[string]bool{"edge": true, "junction": true, "container": true, "none": true}
	// validFunctionalRoles mirrors migration 0091's functional_role CHECK -- the
	// class's electrical function, orthogonal to network_role. An empty string
	// on a request clears it back to NULL (unclassified / non-electrical).
	validFunctionalRoles = map[string]bool{
		"source": true, "consumer": true, "passthrough": true, "transformer": true,
		"controller": true, "node": true, "sensor": true, "ground": true,
	}
	validDataTypes     = map[string]bool{
		"text": true, "numeric": true, "integer": true, "boolean": true,
		"date": true, "timestamp": true, "uuid": true, "jsonb": true,
	}
	validDomainTypes   = map[string]bool{"coded": true, "range": true}
	validTerminalRoles = map[string]bool{"primary": true, "secondary": true, "ground": true, "other": true}
	// validRenderContexts mirrors migrations/0012's symbology_rules
	// render_context CHECK -- a rule either styles the geographic map or
	// the schematic diagram, resolved from the same array client-side (see
	// api/cmd/server/main.go's symbologySchema doc comment).
	validRenderContexts = map[string]bool{"map": true, "diagram": true}
	// validLineDashes are the named stroke patterns a line rule's
	// style_def.dash may carry -- semantic names, not raw pixel arrays, so
	// a rule reads like the rest of style_def (dash names a pattern the way
	// icon_ref names an asset) and web/src/lib/line-dash.ts owns the single
	// name->array mapping the renderer and admin swatch both read. "solid"
	// is the default a rule simply omits; it's accepted here so a
	// raw-API/SQL author who writes it explicitly isn't rejected.
	validLineDashes = map[string]bool{"solid": true, "dashed": true, "dotted": true, "dashdot": true}
	// validCimKinds mirrors migrations/0034's cim_class_mappings.cim_kind
	// CHECK -- how the CIM encoder treats a mapped feature class (an edge
	// synthesizes two terminals, a container becomes an EquipmentContainer,
	// skip is intentionally excluded, ...). See api/internal/metadata/cim.go.
	validCimKinds = map[string]bool{
		"conducting_edge": true, "conducting_junction": true,
		"container": true, "asset": true, "protection": true, "skip": true,
	}
	// validCimTransforms mirrors cim_attribute_mappings.transform after
	// migration 0035 widened it with 'phase' -- the coercion bridging a
	// feature field and its CIM attribute (direct/scale/domain_map/boolean/
	// from_geometry/material_spec/phase).
	validCimTransforms = map[string]bool{
		"direct": true, "scale": true, "domain_map": true, "boolean": true,
		"from_geometry": true, "material_spec": true, "phase": true,
	}
	// validUnmappedAttrPolicies mirrors cim_profiles.unmapped_attr_policy's
	// CHECK -- what a profile does with a feature attribute that has no
	// mapping (drop it, v1 default, or carry it as a generic UserAttribute,
	// not yet implemented by the encoder).
	validUnmappedAttrPolicies = map[string]bool{"drop": true, "carry": true}
)

// updateFeatureClassRequest is PATCH /admin/feature-classes/{code}'s
// body -- every field optional, only what's present gets changed.
// Name/IsDiagramContainer are pure metadata, safe to change on any
// class regardless of status. GeometryType/SRID/NetworkRole are the
// three columns the compiler's own DDL is built from -- see
// updateFeatureClass's own doc comment for why touching any of them is
// only accepted while the class is still draft (never compiled).
// Touching one of the three requires all three together, same
// srid-required-unless-none rule createFeatureClassRequest already
// enforces -- there's no meaningful "just change network_role, leave
// geometry_type as whatever it already was" partial update here, since
// SRID's own validity depends on GeometryType.
type updateFeatureClassRequest struct {
	Name               *string `json:"name"`
	IsDiagramContainer *bool   `json:"is_diagram_container"`
	IsDiagramPlaceable *bool   `json:"is_diagram_placeable"`
	GeometryOptional   *bool   `json:"geometry_optional"`
	// DiagramGeometryType is three-state: nil = not touched, "" = clear
	// back to inherit (NULL), "point"/"line" = set the override (migration
	// 0053). Empty-string-means-clear because a JSON null unmarshals to a
	// nil pointer, indistinguishable from omission.
	DiagramGeometryType *string `json:"diagram_geometry_type"`
	GeometryType        *string `json:"geometry_type"`
	SRID                *int    `json:"srid"`
	NetworkRole         *string `json:"network_role"`
	// FunctionalRole (migration 0091) is the class's electrical function -- pure
	// metadata, editable at any status (unlike network_role, which shapes the
	// DDL and is draft-only). Three-state: nil = not touched, "" clears it back
	// to NULL (unclassified), any taxonomy value sets it.
	FunctionalRole *string `json:"functional_role"`
	// Phase 30 map label config, raw jsonb passthrough. nil = not touched;
	// a JSON object sets it; JSON null (RawMessage "null") clears labels.
	LabelDef *json.RawMessage `json:"label_def"`
	// Category is the admin-defined "New feature" menu grouping (migration
	// 0074). Three-state like DiagramGeometryType: nil = not touched, ""
	// clears it back to uncategorised (NULL), any other string sets it.
	Category *string `json:"category"`
	// MinDisplayZoom / MaxDisplayZoom set the map display-zoom band (migration
	// 0090). Three-state, sentinel-encoded because a JSON null is
	// indistinguishable from omission on an *int: nil = not touched, a
	// negative value clears the bound to NULL (no bound), a value in 0..24
	// sets it. The display-zoom editor always sends both together (empty
	// input -> -1), mirroring how the label editor sends the whole label_def.
	MinDisplayZoom *int `json:"min_display_zoom"`
	MaxDisplayZoom *int `json:"max_display_zoom"`
	// TraceTier (migration 0126) is the class's network tier -- pure metadata,
	// editable at any status. Three-state, sentinel-encoded like the display-zoom
	// band: nil = not touched, a negative value clears it back to NULL (untiered),
	// a non-negative value sets it.
	TraceTier *int `json:"trace_tier"`
}

type updateFeatureClassResponse struct {
	Code                string  `json:"code"`
	Name                string  `json:"name"`
	GeometryType        string  `json:"geometry_type"`
	SRID                *int    `json:"srid,omitempty"`
	NetworkRole         string  `json:"network_role"`
	FunctionalRole      *string `json:"functional_role,omitempty"`
	IsDiagramContainer  bool    `json:"is_diagram_container"`
	IsDiagramPlaceable  bool    `json:"is_diagram_placeable"`
	GeometryOptional    bool    `json:"geometry_optional"`
	DiagramGeometryType *string `json:"diagram_geometry_type,omitempty"`
	Category            *string `json:"category,omitempty"`
	TraceTier           *int    `json:"trace_tier,omitempty"`
}

// featureClassSummary is one row of the admin catalog view -- enough to
// browse and decide what to open, not the full definition
// createFeatureClassRequest would need to recreate it (that's
// updateFeatureClassRequest's job, above, for what's actually safe to
// change).
type featureClassSummary struct {
	Code               string  `json:"code"`
	Name               string  `json:"name"`
	GeometryType       string  `json:"geometry_type"`
	SRID               *int    `json:"srid,omitempty"`
	NetworkRole        string  `json:"network_role"`
	FunctionalRole      *string `json:"functional_role,omitempty"`
	Status              string  `json:"status"`
	IsDiagramContainer  bool    `json:"is_diagram_container"`
	IsDiagramPlaceable  bool    `json:"is_diagram_placeable"`
	GeometryOptional    bool    `json:"geometry_optional"`
	DiagramGeometryType *string `json:"diagram_geometry_type,omitempty"`
	Category            *string `json:"category,omitempty"`
	TraceTier           *int    `json:"trace_tier,omitempty"`
	PhysicalTable       *string `json:"physical_table,omitempty"`
	SubtypeCount        int     `json:"subtype_count"`
	FieldCount          int     `json:"field_count"`
}

// domainValueRequest is one value of a coded domain, provided inline
// when creating the domain -- adding a value to an already-existing
// domain later is a separate, smaller endpoint (see handler.go).
// MaterialSpecCode, if set, must name an existing metadata.material_specs
// row (see metadata.MaterialSpec's own doc comment) -- optional, since
// most domain values (OPEN/CLOSED, phase codes, ...) never carry one.
type domainValueRequest struct {
	Code             string  `json:"code"`
	Label            string  `json:"label"`
	SortOrder        int     `json:"sort_order"`
	MaterialSpecCode *string `json:"material_spec_code"`
}

// RangeMin/RangeMax only apply when DomainType is "range" -- see
// validateCreateDomain for the rule that a range domain requires both
// and a coded domain rejects either being set.
type createDomainRequest struct {
	Code       string               `json:"code"`
	Name       string               `json:"name"`
	DomainType string               `json:"domain_type"`
	Values     []domainValueRequest `json:"values"`
	RangeMin   *float64             `json:"range_min"`
	RangeMax   *float64             `json:"range_max"`
}

// updateDomainRequest is PATCH /admin/domains/{code}'s body -- Name is
// always editable (pure metadata, no physical footprint at all).
// RangeMin/RangeMax only apply to a range domain (checked against the
// actual row in update.go, same split validateUpdateFeatureClass/
// updateFeatureClass already use) -- editing them is safe the same way
// widening a coded domain's values already is: a recompile of any class
// using this domain rebuilds the field's BETWEEN CHECK from whatever's
// here now, and if that narrows past real data, Postgres's own ADD
// CONSTRAINT rejects it outright (see compiler/ddl.go's
// syncCheckConstraints doc comment) rather than silently applying it.
type updateDomainRequest struct {
	Name     *string  `json:"name"`
	RangeMin *float64 `json:"range_min"`
	RangeMax *float64 `json:"range_max"`
}

// updateDomainValueRequest is PATCH /admin/domains/{code}/values/{value_code}'s
// body -- label, sort_order, and material_spec_code, all optional. The
// code itself is the path segment and isn't renameable here: a field's
// CHECK constraint and any real feature data both reference a domain
// value by its code, so renaming it out from under them is a different,
// harder operation than this endpoint means to be (delete the old code,
// add the new one, if that's really what's wanted). MaterialSpecCode
// uses the same nil/""/value three-state sentinel updateFieldRequest's
// own DomainCode already established: nil leaves the link alone, ""
// clears it, any other string resolves against an existing material
// spec.
type updateDomainValueRequest struct {
	Label            *string `json:"label"`
	SortOrder        *int    `json:"sort_order"`
	MaterialSpecCode *string `json:"material_spec_code"`
}

type domainValueResponse struct {
	Code             string  `json:"code"`
	Label            string  `json:"label"`
	SortOrder        int     `json:"sort_order"`
	MaterialSpecCode *string `json:"material_spec_code,omitempty"`
}

type domainResponse struct {
	Code       string                `json:"code"`
	Name       string                `json:"name"`
	DomainType string                `json:"domain_type"`
	Values     []domainValueResponse `json:"values,omitempty"`
	RangeMin   *float64              `json:"range_min,omitempty"`
	RangeMax   *float64              `json:"range_max,omitempty"`
}

type createFieldGroupRequest struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	SortOrder int     `json:"sort_order"`
	Icon      *string `json:"icon"`
}

type fieldGroupResponse struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	SortOrder int     `json:"sort_order"`
	Icon      *string `json:"icon,omitempty"`
}

// subtypeRequest is one subtype defined as part of creating a brand-new
// feature class -- Code must be unique within the request, and is what
// fieldRequest.RequiredForSubtypes and terminalRequest.SubtypeCode refer
// back to below, before any of these rows have real ids yet. Reused
// as-is for addSubtype's POST .../subtypes body (add.go), which adds a
// single subtype to an already-existing class instead.
type subtypeRequest struct {
	Code int    `json:"code"`
	Name string `json:"name"`
}

type subtypeResponse struct {
	Code int    `json:"code"`
	Name string `json:"name"`
}

// fieldRequest is one attribute of a brand-new feature class.
// DomainCode, if set, must name a domain that already exists -- this
// wizard doesn't create domains inline (see docs/phase-6-admin-metadata.md
// on why that's a deliberate scope cut, not an oversight).
// RequiredForSubtypes references subtypeRequest.Code values from the
// same request.
type fieldRequest struct {
	Name                 string  `json:"name"`
	DataType             string  `json:"data_type"`
	DomainCode           *string `json:"domain_code"`
	GroupCode            *string `json:"group_code"`
	Nullable             bool    `json:"nullable"`
	VisibleOnCreate      bool    `json:"visible_on_create"`
	VisibleOnUpdate      bool    `json:"visible_on_update"`
	EditableAfterPublish bool    `json:"editable_after_publish"`
	RequiredForSubtypes  []int   `json:"required_for_subtypes"`
}

// fieldResponse mirrors fieldRequest, echoed back for addField's POST
// .../fields body (add.go), which adds a single field to an
// already-existing class instead.
type fieldResponse struct {
	Name                 string  `json:"name"`
	DataType             string  `json:"data_type"`
	DomainCode           *string `json:"domain_code,omitempty"`
	GroupCode            *string `json:"group_code,omitempty"`
	Nullable             bool    `json:"nullable"`
	VisibleOnCreate      bool    `json:"visible_on_create"`
	VisibleOnUpdate      bool    `json:"visible_on_update"`
	EditableAfterPublish bool    `json:"editable_after_publish"`
	RequiredForSubtypes  []int   `json:"required_for_subtypes,omitempty"`
}

// updateFieldRequest is PATCH /admin/feature-classes/{code}/fields/{name}'s
// body -- every field optional, only what's present gets changed. Name and
// data_type are deliberately absent: the compiler has no rename/retype
// path (see handler.go's package doc), so those stay fixed for a field's
// whole life. DomainCode/GroupCode use the same *string a bit differently
// than fieldRequest's own: nil means "leave alone", a pointer to "" means
// "clear the assignment back to none", and a pointer to any other string
// resolves against an existing domain/group the same way addField already
// does -- JSON's null-vs-omitted can't tell "clear" from "don't touch"
// through a plain *string alone, and no domain/group code is ever
// legitimately blank, so "" is a safe sentinel for the missing state.
//
// RequiredForSubtypes is the one slice here: nil means "leave the field's
// subtype_field_rules alone", a non-nil (even empty) slice means "reconcile
// the field's is_required rules to exactly these subtype codes" -- so an
// empty slice clears every required mark. Unlike nullable (a physical NOT
// NULL baked at compile), is_required is read live by the CRUD engine
// (api/internal/crud validateRequired), so editing it takes effect with no
// recompile.
type updateFieldRequest struct {
	DomainCode           *string `json:"domain_code"`
	GroupCode            *string `json:"group_code"`
	Nullable             *bool   `json:"nullable"`
	VisibleOnCreate      *bool   `json:"visible_on_create"`
	VisibleOnUpdate      *bool   `json:"visible_on_update"`
	EditableAfterPublish *bool   `json:"editable_after_publish"`
	RequiredForSubtypes  *[]int  `json:"required_for_subtypes"`
}

// terminalRequest is one named connection point, only meaningful (and
// only accepted, see validate.go) when the class's network_role is edge
// or junction. SubtypeCode nil means it applies to every subtype.
type terminalRequest struct {
	Code         string `json:"code"`
	Role         string `json:"role"`
	PhaseCapable bool   `json:"phase_capable"`
	SubtypeCode  *int   `json:"subtype_code"`
}

// symbologyRequest is the one, mandatory-default map-context rule a
// brand-new class gets, if given -- no subtype/domain-value targeting,
// no zoom bounds. A finer-grained rule editor (per-subtype, per-domain-
// value, diagram-context) is real, deliberately deferred scope -- see
// docs/phase-6-admin-metadata.md.
type symbologyRequest struct {
	IconRef string   `json:"icon_ref"`
	Color   string   `json:"color"`
	Width   *float64 `json:"width"`
	Dash    *string  `json:"dash"`
}

type createFeatureClassRequest struct {
	Code               string            `json:"code"`
	Name               string            `json:"name"`
	GeometryType       string            `json:"geometry_type"`
	SRID               *int              `json:"srid"`
	NetworkRole        string            `json:"network_role"`
	// FunctionalRole (migration 0091) is optional on create -- omit or "" for an
	// unclassified / non-electrical class; else a taxonomy value.
	FunctionalRole     *string           `json:"functional_role"`
	// TraceTier (migration 0126) is optional on create -- omit/null for an
	// untiered (transparent) class; else a non-negative rank in the hundreds
	// scale (0/200/300/400/500/600). See seed/trace_tiers.sql.
	TraceTier          *int              `json:"trace_tier"`
	IsDiagramContainer bool              `json:"is_diagram_container"`
	IsDiagramPlaceable bool              `json:"is_diagram_placeable"`
	GeometryOptional   bool              `json:"geometry_optional"`
	Subtypes           []subtypeRequest  `json:"subtypes"`
	Fields             []fieldRequest    `json:"fields"`
	Terminals          []terminalRequest `json:"terminals"`
	Symbology          *symbologyRequest `json:"symbology"`
}

// createFeatureClassResponse confirms what was created -- deliberately
// not the full schema shape (that's GET /feature-classes/{code}/schema's
// job, already built for every other consumer); just enough for the
// wizard to offer "compile now."
type createFeatureClassResponse struct {
	Code           string `json:"code"`
	SubtypesAdded  int    `json:"subtypes_added"`
	FieldsAdded    int    `json:"fields_added"`
	TerminalsAdded int    `json:"terminals_added"`
}

// compileResponse mirrors compiler.Result -- its own fields aren't
// json-tagged (nothing needed that before this package), so this is a
// thin, wire-shaped copy rather than exposing that struct directly.
type compileResponse struct {
	FeatureClassCode  string   `json:"feature_class_code"`
	PhysicalTable     string   `json:"physical_table"`
	Created           bool     `json:"created"`
	ColumnsAdded      []string `json:"columns_added,omitempty"`
	MetadataVersionID string   `json:"metadata_version_id"`
}

// uncompileResponse mirrors compiler.UncompileResult (same not-json-tagged
// reason as compileResponse). RowsDropped is the count discarded by the
// DROP TABLE, so the UI can report exactly how much data went.
type uncompileResponse struct {
	FeatureClassCode string `json:"feature_class_code"`
	PhysicalTable    string `json:"physical_table"`
	TableExisted     bool   `json:"table_existed"`
	RowsDropped      int    `json:"rows_dropped"`
}

// createMaterialSpecRequest is POST /admin/material-specs' body -- a new
// catalog entry (e.g. ACSR_50, see metadata.MaterialSpec's own doc
// comment). Every engineering column is optional -- a spec can be
// created with just a code/name and filled in later via PATCH, the same
// "author now, refine later" flow the rest of this package already
// allows for a domain value's own label/sort_order.
type createMaterialSpecRequest struct {
	Code               string   `json:"code"`
	Name               string   `json:"name"`
	Category           string   `json:"category"`
	ResistanceOhmPerKm *float64 `json:"resistance_ohm_per_km"`
	ReactanceOhmPerKm  *float64 `json:"reactance_ohm_per_km"`
	AmpacityAmps       *float64 `json:"ampacity_amps"`
	DiameterMm         *float64 `json:"diameter_mm"`
	WeightKgPerKm      *float64 `json:"weight_kg_per_km"`

	// migrations/0022 -- conductor-intrinsic inputs to a first-principles
	// IEEE 738 ampacity calc, see metadata.MaterialSpec's own doc comment.
	MaxOperatingTempC             *float64 `json:"max_operating_temp_c"`
	Emissivity                    *float64 `json:"emissivity"`
	Absorptivity                  *float64 `json:"absorptivity"`
	ResistanceTempCoefficientPerC *float64 `json:"resistance_temp_coefficient_per_c"`

	// migrations/0060 -- optional cost, bare numeric in the house currency.
	CostPerKm *float64 `json:"cost_per_km"`
	UnitCost  *float64 `json:"unit_cost"`
}

// updateMaterialSpecRequest is PATCH /admin/material-specs/{code}'s
// body -- every field optional, only what's present gets changed.
// Unlike updateDomainValueRequest's material_spec_code, there's no
// clear-to-null sentinel needed here: every column already tolerates
// NULL at the database level, but nothing in this package's own
// discipline requires clearing one back out once set, and every
// consumer (a calc reading these numbers) already has to handle NULL
// either way, so a numeric field simply isn't touched unless a new
// value is provided.
type updateMaterialSpecRequest struct {
	Name               *string  `json:"name"`
	Category           *string  `json:"category"`
	ResistanceOhmPerKm *float64 `json:"resistance_ohm_per_km"`
	ReactanceOhmPerKm  *float64 `json:"reactance_ohm_per_km"`
	AmpacityAmps       *float64 `json:"ampacity_amps"`
	DiameterMm         *float64 `json:"diameter_mm"`
	WeightKgPerKm      *float64 `json:"weight_kg_per_km"`

	MaxOperatingTempC             *float64 `json:"max_operating_temp_c"`
	Emissivity                    *float64 `json:"emissivity"`
	Absorptivity                  *float64 `json:"absorptivity"`
	ResistanceTempCoefficientPerC *float64 `json:"resistance_temp_coefficient_per_c"`

	CostPerKm *float64 `json:"cost_per_km"`
	UnitCost  *float64 `json:"unit_cost"`
}

type materialSpecResponse struct {
	Code               string   `json:"code"`
	Name               string   `json:"name"`
	Category           string   `json:"category"`
	ResistanceOhmPerKm *float64 `json:"resistance_ohm_per_km,omitempty"`
	ReactanceOhmPerKm  *float64 `json:"reactance_ohm_per_km,omitempty"`
	AmpacityAmps       *float64 `json:"ampacity_amps,omitempty"`
	DiameterMm         *float64 `json:"diameter_mm,omitempty"`
	WeightKgPerKm      *float64 `json:"weight_kg_per_km,omitempty"`

	MaxOperatingTempC             *float64 `json:"max_operating_temp_c,omitempty"`
	Emissivity                    *float64 `json:"emissivity,omitempty"`
	Absorptivity                  *float64 `json:"absorptivity,omitempty"`
	ResistanceTempCoefficientPerC *float64 `json:"resistance_temp_coefficient_per_c,omitempty"`

	CostPerKm *float64 `json:"cost_per_km,omitempty"`
	UnitCost  *float64 `json:"unit_cost,omitempty"`
}

// createSymbologyIconRequest is POST /admin/symbology-icons' body -- one
// icon_ref -> raw <svg> markup row of metadata.symbology_icons
// (migrations/0023). IconRef is the name a symbology rule's
// style_def.icon_ref points at (lowercase-kebab, e.g. "triangle-outline");
// SVG is the markup that name resolves to. See validateSymbologyIcon for
// the two real content rules (a {{COLOR}} placeholder is optional and opts
// the icon into tinting; explicit width/height is mandatory).
type createSymbologyIconRequest struct {
	IconRef string `json:"icon_ref"`
	SVG     string `json:"svg"`
}

// updateSymbologyIconRequest is PATCH /admin/symbology-icons/{icon_ref}'s
// body -- only the markup is editable (icon_ref is the identity a rule
// references, renaming it out from under a rule is a delete-and-recreate,
// same reasoning updateDomainValueRequest's own doc comment gives for a
// value code).
type updateSymbologyIconRequest struct {
	SVG *string `json:"svg"`
}

type symbologyIconResponse struct {
	IconRef string `json:"icon_ref"`
	SVG     string `json:"svg"`
}

// updateDiagramStencilRequest is PATCH /admin/symbology-stencils/{icon_ref}'s
// body. PromoteClass uses the same nil/""/value three-state sentinel
// updateFieldRequest.DomainCode established: nil means "leave the mapping
// alone", a pointer to "" clears it back to decoration-only (NULL), and a
// pointer to any other string names the feature class a stamped symbol of
// this stencil is promoted into (Phase 25 -- see updateDiagramStencil for
// the geometry_type='none' rule enforced on that class). icon_ref (the
// stencil's identity, the PK) is the path segment, never changed here.
type updateDiagramStencilRequest struct {
	PromoteClass *string `json:"promote_class"`
}

type diagramStencilResponse struct {
	IconRef      string  `json:"icon_ref"`
	Label        string  `json:"label"`
	Category     string  `json:"category"`
	SortOrder    int     `json:"sort_order"`
	PromoteClass *string `json:"promote_class,omitempty"`
}

// styleDefRequest is the style half of one symbology rule -- the same
// shape as web/src/lib/symbology.ts's StyleDef and migrations/0012's
// style_def JSON. Every field optional: a point rule carries
// icon_ref/color/scale, a line color/width/dash, a polygon fill_color/
// stroke_color -- buildStyleDef writes only the ones actually set, so a
// rule's style_def never carries irrelevant keys for its geometry.
type styleDefRequest struct {
	IconRef     *string  `json:"icon_ref"`
	Color       *string  `json:"color"`
	Scale       *float64 `json:"scale"`
	Width       *float64 `json:"width"`
	Dash        *string  `json:"dash"`
	FillColor   *string  `json:"fill_color"`
	StrokeColor *string  `json:"stroke_color"`
}

// symbologyRuleRequest is the body for both POST
// /admin/feature-classes/{code}/symbology-rules and PATCH
// /admin/symbology-rules/{id} -- a full rule each time (a rule is small
// and its targeting axes interlock, so a wholesale replace is clearer
// than a partial patch). Targeting is expressed in the same *codes* the
// schema endpoint speaks, not raw ids: SubtypeCode resolves against this
// class's own subtypes, and (DomainField, DomainValueCode) against the
// field's domain -- exactly the inverse of resolveSymbologySchema's
// id->code flattening (see api/cmd/server/main.go). A nil axis means
// "unconstrained" (matches any subtype / any value), the same
// most-specific-wins semantics migrations/0012 documents.
type symbologyRuleRequest struct {
	RenderContext   string          `json:"render_context"`
	SubtypeCode     *int            `json:"subtype_code"`
	DomainField     *string         `json:"domain_field"`
	DomainValueCode *string         `json:"domain_value_code"`
	MinZoom         *int            `json:"min_zoom"`
	MaxZoom         *int            `json:"max_zoom"`
	Style           styleDefRequest `json:"style"`
}

// symbologyRuleResponse is one raw symbology_rules row with its id -- what
// the rule editor lists and edits by identity. Deliberately distinct from
// api/cmd/server/main.go's symbologySchema (which is id-less, pre-resolved
// for the map/legend's read-only matcher): editing a rule needs its id and
// the same code-shaped targeting the request speaks back.
type symbologyRuleResponse struct {
	ID              string          `json:"id"`
	RenderContext   string          `json:"render_context"`
	SubtypeCode     *int            `json:"subtype_code,omitempty"`
	DomainField     *string         `json:"domain_field,omitempty"`
	DomainValueCode *string         `json:"domain_value_code,omitempty"`
	MinZoom         *int            `json:"min_zoom,omitempty"`
	MaxZoom         *int            `json:"max_zoom,omitempty"`
	StyleDef        json.RawMessage `json:"style_def"`
}

// ---- CIM export mapping (Phase 55, migrations/0034 + 0035) -----------------
// The write path for the three metadata.cim_* tables that drive CIM/XML
// export (api/internal/metadata/cim.go, api/internal/export). Authored only
// by seed/SQL until now; these DTOs are the admin editor's wire shape. See
// cim.go for the resolvers and the per-table CRUD.

// cimProfileResponse is one metadata.cim_profiles row, keyed by code (the
// natural key the profile URL uses, like domains). All columns are NOT NULL,
// so nothing here is a nullable-clearable *pointer -- coordinate_crs and
// unmapped_attr_policy simply carry their DB defaults when a create omits them.
type cimProfileResponse struct {
	Code               string `json:"code"`
	Name               string `json:"name"`
	CimNamespace       string `json:"cim_namespace"`
	ProfileURI         string `json:"profile_uri"`
	ModelAuthority     string `json:"model_authority"`
	CoordinateCRS      string `json:"coordinate_crs"`
	UnmappedAttrPolicy string `json:"unmapped_attr_policy"`
	IsDefault          bool   `json:"is_default"`
}

// createCimProfileRequest is POST /admin/cim-profiles' body. CoordinateCRS
// and UnmappedAttrPolicy are optional pointers only so an omitted one falls
// back to the column's DB default (EPSG:4326 URN / 'drop') rather than an
// empty string -- not a clear-to-null sentinel, since neither column is
// nullable.
type createCimProfileRequest struct {
	Code               string  `json:"code"`
	Name               string  `json:"name"`
	CimNamespace       string  `json:"cim_namespace"`
	ProfileURI         string  `json:"profile_uri"`
	ModelAuthority     string  `json:"model_authority"`
	CoordinateCRS      *string `json:"coordinate_crs"`
	UnmappedAttrPolicy *string `json:"unmapped_attr_policy"`
	IsDefault          *bool   `json:"is_default"`
}

// updateCimProfileRequest is PATCH /admin/cim-profiles/{code}'s body -- every
// field optional, only what's present changes. Every cim_profiles column is
// NOT NULL, so these are plain set-only pointers (nil = leave alone), never
// the nil/""/value three-state a nullable column would use.
type updateCimProfileRequest struct {
	Name               *string `json:"name"`
	CimNamespace       *string `json:"cim_namespace"`
	ProfileURI         *string `json:"profile_uri"`
	ModelAuthority     *string `json:"model_authority"`
	CoordinateCRS      *string `json:"coordinate_crs"`
	UnmappedAttrPolicy *string `json:"unmapped_attr_policy"`
	IsDefault          *bool   `json:"is_default"`
}

// cimClassMappingResponse is one metadata.cim_class_mappings row -- code-shaped
// over the wire (feature_class_code + integer subtype_code, the same keys
// metadata.LoadCIMMapping resolves) but carrying the row id, since these are
// global (no natural key) and edited/deleted by id.
type cimClassMappingResponse struct {
	ID               string `json:"id"`
	FeatureClassCode string `json:"feature_class_code"`
	SubtypeCode      *int   `json:"subtype_code,omitempty"`
	CimClass         string `json:"cim_class"`
	CimKind          string `json:"cim_kind"`
}

// createCimClassMappingRequest is POST /admin/cim-class-mappings' body. A nil
// SubtypeCode is the class default (subtype_id NULL); a value scopes the
// mapping to that subtype (resolved against feature_class_code's own subtypes).
type createCimClassMappingRequest struct {
	FeatureClassCode string `json:"feature_class_code"`
	SubtypeCode      *int   `json:"subtype_code"`
	CimClass         string `json:"cim_class"`
	CimKind          string `json:"cim_kind"`
}

// updateCimClassMappingRequest is PATCH /admin/cim-class-mappings/{id}'s body.
// Only cim_class/cim_kind are editable -- the identity keys (feature_class_id,
// subtype_id) are not re-keyed here; re-target by deleting and recreating, the
// same reasoning updateDomainValueRequest's own doc comment gives for a code.
type updateCimClassMappingRequest struct {
	CimClass *string `json:"cim_class"`
	CimKind  *string `json:"cim_kind"`
}

// cimAttributeMappingResponse is one metadata.cim_attribute_mappings row --
// code-shaped (feature_class_code + field_name) with the row id, plus the
// coercion (transform + scale_factor/unit/value_map). value_map is returned
// parsed (or omitted when NULL).
type cimAttributeMappingResponse struct {
	ID               string         `json:"id"`
	FeatureClassCode string         `json:"feature_class_code"`
	FieldName        string         `json:"field_name"`
	CimAttribute     string         `json:"cim_attribute"`
	Transform        string         `json:"transform"`
	ScaleFactor      *float64       `json:"scale_factor,omitempty"`
	Unit             *string        `json:"unit,omitempty"`
	ValueMap         map[string]any `json:"value_map,omitempty"`
}

// createCimAttributeMappingRequest is POST /admin/cim-attribute-mappings' body.
// ValueMap is accepted as a JSON object and marshalled to the value_map jsonb
// column; an omitted one stores NULL.
type createCimAttributeMappingRequest struct {
	FeatureClassCode string         `json:"feature_class_code"`
	FieldName        string         `json:"field_name"`
	CimAttribute     string         `json:"cim_attribute"`
	Transform        string         `json:"transform"`
	ScaleFactor      *float64       `json:"scale_factor"`
	Unit             *string        `json:"unit"`
	ValueMap         map[string]any `json:"value_map"`
}

// updateCimAttributeMappingRequest is PATCH /admin/cim-attribute-mappings/{id}'s
// body -- every field optional. The nullable columns use the sentinels JSON can
// actually express: Unit (*string) is nil/""/value like updateFieldRequest's
// DomainCode (nil = leave, "" = clear to NULL, else set); ValueMap
// (*json.RawMessage) mirrors updateFeatureClassRequest.LabelDef (nil = leave, a
// JSON null or empty clears the column to SQL NULL, a JSON object sets it) --
// the one shape that keeps clear-to-NULL distinguishable from omission for a
// jsonb value. ScaleFactor stays a plain set-only *float64 (nil = leave, a
// value sets it): a JSON null and an omission both unmarshal to nil, so there's
// no clear sentinel, matching updateMaterialSpecRequest's own numeric fields --
// a stale scale_factor on a non-'scale' transform is simply ignored by the
// encoder.
type updateCimAttributeMappingRequest struct {
	CimAttribute *string          `json:"cim_attribute"`
	Transform    *string          `json:"transform"`
	ScaleFactor  *float64         `json:"scale_factor"`
	Unit         *string          `json:"unit"`
	ValueMap     *json.RawMessage `json:"value_map"`
}

// cimCoverageResponse is one feature class with NO cim_class_mappings row of any
// kind -- the Phase 55 Slice 2 warn-on-unmapped signal. Such a class is silently
// dropped from every CIM export (metadata.CIMMapping.ClassFor returns ok=false),
// so the editor lists these so an operator can either map the class (Slice 3) or
// mark it cim_kind='skip' to make the exclusion deliberate. geometry_type and
// network_role are carried so the UI can hint whether a class plausibly SHOULD
// export (a none-geometry annotation class rarely does; a conducting one usually
// does). A class that already has any mapping row -- including a skip -- is not
// listed here (it's a decided class, covered or deliberately excluded).
type cimCoverageResponse struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	GeometryType string `json:"geometry_type"`
	NetworkRole  string `json:"network_role"`
}

// cimPreviewResponse is one real feature of a class rendered through the actual
// CIM encoder (export.PreviewFeature) -- the byte-for-byte CIM/XML a full export
// would produce for it, so an operator can see the shape a mapping yields instead
// of trusting the rule table. Sampled is false (Xml empty) when there was nothing
// to render -- no instance, or a geometry-less class the CIM export never emits --
// with Note explaining which. FeatureID is the sampled feature's id (blank when
// not sampled).
type cimPreviewResponse struct {
	ClassCode string `json:"class_code"`
	FeatureID string `json:"feature_id"`
	Xml       string `json:"xml"`
	Sampled   bool   `json:"sampled"`
	Note      string `json:"note,omitempty"`
}
