package admin

// The metadata.* CHECK constraints, mirrored here so a bad value gets a
// friendly 400 from this package's own validation instead of a raw
// Postgres constraint-violation error surfacing from the insert.
var (
	validGeometryTypes = map[string]bool{"point": true, "line": true, "polygon": true, "none": true}
	validNetworkRoles  = map[string]bool{"edge": true, "junction": true, "container": true, "none": true}
	validDataTypes     = map[string]bool{
		"text": true, "numeric": true, "integer": true, "boolean": true,
		"date": true, "timestamp": true, "uuid": true, "jsonb": true,
	}
	validDomainTypes   = map[string]bool{"coded": true, "range": true}
	validTerminalRoles = map[string]bool{"primary": true, "secondary": true, "ground": true, "other": true}
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
	GeometryType       *string `json:"geometry_type"`
	SRID               *int    `json:"srid"`
	NetworkRole        *string `json:"network_role"`
}

type updateFeatureClassResponse struct {
	Code               string `json:"code"`
	Name               string `json:"name"`
	GeometryType       string `json:"geometry_type"`
	SRID               *int   `json:"srid,omitempty"`
	NetworkRole        string `json:"network_role"`
	IsDiagramContainer bool   `json:"is_diagram_container"`
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
	Status             string  `json:"status"`
	IsDiagramContainer bool    `json:"is_diagram_container"`
	PhysicalTable      *string `json:"physical_table,omitempty"`
	SubtypeCount       int     `json:"subtype_count"`
	FieldCount         int     `json:"field_count"`
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
type updateFieldRequest struct {
	DomainCode           *string `json:"domain_code"`
	GroupCode            *string `json:"group_code"`
	Nullable             *bool   `json:"nullable"`
	VisibleOnCreate      *bool   `json:"visible_on_create"`
	VisibleOnUpdate      *bool   `json:"visible_on_update"`
	EditableAfterPublish *bool   `json:"editable_after_publish"`
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
}

type createFeatureClassRequest struct {
	Code               string            `json:"code"`
	Name               string            `json:"name"`
	GeometryType       string            `json:"geometry_type"`
	SRID               *int              `json:"srid"`
	NetworkRole        string            `json:"network_role"`
	IsDiagramContainer bool              `json:"is_diagram_container"`
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
}
