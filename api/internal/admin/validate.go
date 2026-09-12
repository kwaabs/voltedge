package admin

import (
	"regexp"
	"strconv"
	"strings"
)

// codePattern is the same convention every hand-written seed file has
// followed so far (MV_CONDUCTOR, PROTECTIVE_RELAY, ANSI_DEVICE_NUMBER):
// upper-case letters, digits, underscores, must start with a letter.
// Enforced here so the admin UI can't silently drift from that
// convention just because nothing else stopped it.
var codePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// fieldNamePattern is the opposite case convention -- a field name
// becomes a real Postgres column, following the same lower_snake_case
// every hand-written seed file's fields have used (device_number,
// voltage_class, ...).
var fieldNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// iconRefPattern is the third case convention in this package -- unlike a
// domain/class code (UPPER_SNAKE) or a field name (lower_snake), an
// icon_ref is lower-kebab (circle, triangle-outline, iec-disconnector),
// the same shape every seeded row in seed/symbology_icons.sql and every
// style_def.icon_ref uses. Enforced so the icon library UI can't drift a
// name into a shape a rule's style_def could never spell.
var iconRefPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// reservedFieldNames are api/internal/compiler/ddl.go's own system
// columns (buildCreateTable): id/name/status/deleted_at/created_at/
// updated_at unconditionally, subtype/geom only when a class actually
// has subtypes/geometry. A brand-new class naming a field one of the
// unconditional five would fail loudly at CREATE TABLE time (Postgres
// rejects a duplicate column outright) -- but addField's own
// existingCols skip logic (compiler.go's ADD COLUMN loop) treats
// "already a column" as nothing to add, not an error, on an
// already-compiled class. Left unchecked, a field literally named
// "status" would silently ride on the real system status column: the
// CRUD engine would let a client overwrite it as a plain attribute, and
// a domain-linked "status" field would have syncCheckConstraints
// (ddl.go) overwrite its real draft/published CHECK with the domain's
// own values -- caught during this endpoint's own verification, not
// theoretical.
var reservedFieldNames = map[string]bool{
	"id": true, "name": true, "subtype": true, "status": true,
	"deleted_at": true, "geom": true, "created_at": true, "updated_at": true,
}

func validateCode(label, code string) error {
	if !codePattern.MatchString(code) {
		return invalid("%s %q must be upper-case letters, digits, and underscores, starting with a letter", label, code)
	}
	return nil
}

func validateCreateDomain(req createDomainRequest) error {
	if err := validateCode("domain code", req.Code); err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return invalid("domain name is required")
	}
	if !validDomainTypes[req.DomainType] {
		return invalid("domain_type must be one of coded, range")
	}
	if req.DomainType == "range" && len(req.Values) > 0 {
		return invalid("a range domain doesn't take values -- set range_min/range_max instead")
	}
	if req.DomainType == "coded" && (req.RangeMin != nil || req.RangeMax != nil) {
		return invalid("range_min/range_max only apply to a range domain")
	}
	seen := make(map[string]bool, len(req.Values))
	for _, v := range req.Values {
		if strings.TrimSpace(v.Code) == "" {
			return invalid("every domain value needs a code")
		}
		if strings.TrimSpace(v.Label) == "" {
			return invalid("domain value %q needs a label", v.Code)
		}
		if seen[v.Code] {
			return invalid("domain value code %q is repeated", v.Code)
		}
		seen[v.Code] = true
	}
	if req.DomainType == "coded" && len(req.Values) == 0 {
		return invalid("a coded domain needs at least one value")
	}
	if req.DomainType == "range" {
		if req.RangeMin == nil || req.RangeMax == nil {
			return invalid("a range domain needs both range_min and range_max")
		}
		if *req.RangeMin > *req.RangeMax {
			return invalid("range_min must be less than or equal to range_max")
		}
	}
	return nil
}

// validateUpdateDomain checks the structural rules updateDomainRequest
// needs before update.go looks at the existing row's domain_type: an
// empty Name is never valid, and range_min/range_max must be provided
// together (mirroring createDomainRequest's own rule) since one without
// the other leaves an incomplete bound. Whether the request is even
// *allowed* to touch range_min/range_max -- only a range domain has
// them -- is a database question, checked in updateDomain itself.
func validateUpdateDomain(req updateDomainRequest) error {
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		return invalid("name cannot be blank")
	}
	rangeTouched := req.RangeMin != nil || req.RangeMax != nil
	if req.Name == nil && !rangeTouched {
		return invalid("request must set at least one of name, range_min/range_max")
	}
	if rangeTouched {
		if req.RangeMin == nil || req.RangeMax == nil {
			return invalid("range_min and range_max must be provided together when changing either")
		}
		if *req.RangeMin > *req.RangeMax {
			return invalid("range_min must be less than or equal to range_max")
		}
	}
	return nil
}

func validateUpdateDomainValue(req updateDomainValueRequest) error {
	if req.Label != nil && strings.TrimSpace(*req.Label) == "" {
		return invalid("label cannot be blank")
	}
	if req.Label == nil && req.SortOrder == nil && req.MaterialSpecCode == nil {
		return invalid("request must set at least one of label, sort_order, material_spec_code")
	}
	return nil
}

// validateCreateMaterialSpec checks the structural rules a new material
// spec needs -- every engineering column is optional (see
// createMaterialSpecRequest's own doc comment), so there's nothing to
// check about them beyond what the database's own CHECK constraints
// (migrations/0021: each is NULL or strictly positive) already enforce,
// which surfaces as a Postgres error rather than a friendly 400 here --
// acceptable since a negative resistance/ampacity is a mistake, not a
// legitimate value ever worth a clearer message for.
func validateCreateMaterialSpec(req createMaterialSpecRequest) error {
	if err := validateCode("material spec code", req.Code); err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return invalid("material spec name is required")
	}
	if strings.TrimSpace(req.Category) == "" {
		return invalid("material spec category is required")
	}
	return nil
}

func validateUpdateMaterialSpec(req updateMaterialSpecRequest) error {
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		return invalid("name cannot be blank")
	}
	if req.Category != nil && strings.TrimSpace(*req.Category) == "" {
		return invalid("category cannot be blank")
	}
	if req.Name == nil && req.Category == nil && req.ResistanceOhmPerKm == nil && req.ReactanceOhmPerKm == nil &&
		req.AmpacityAmps == nil && req.DiameterMm == nil && req.WeightKgPerKm == nil &&
		req.MaxOperatingTempC == nil && req.Emissivity == nil && req.Absorptivity == nil &&
		req.ResistanceTempCoefficientPerC == nil && req.CostPerKm == nil && req.UnitCost == nil {
		return invalid("request must set at least one field")
	}
	return nil
}

// validateSymbologyIcon checks the two real content contracts an icon row
// must honor (see docs/phase-3-symbology-icon-table.md and
// migrations/0023's own column comments), the DB-authoring equivalent of
// the invariants the seed file's five hand-written shapes already satisfy.
// A {{COLOR}} placeholder is deliberately NOT required: its presence is
// what opts an icon into tinting, its absence renders full-color artwork
// as-is -- so requiring it would forbid exactly the full-color case this
// table was built to also support.
func validateSymbologyIcon(iconRef, svg string) error {
	if !iconRefPattern.MatchString(iconRef) {
		return invalid("icon_ref %q must be lower-case letters, digits, and hyphens, starting with a letter", iconRef)
	}
	if strings.TrimSpace(svg) == "" {
		return invalid("svg markup is required")
	}
	if !strings.Contains(svg, "<svg") {
		return invalid("svg must be a valid <svg> element")
	}
	// Explicit width/height, not just a viewBox: a detached Image() with
	// no intrinsic size resolves to Chromium's 150x150 default, which
	// silently broke icon rendering the one time it shipped without them
	// (docs/phase-3-symbology.md, bug #1).
	if !strings.Contains(svg, "width=") || !strings.Contains(svg, "height=") {
		return invalid("svg must carry explicit width and height attributes, not just a viewBox -- see migrations/0023")
	}
	return nil
}

// validateSymbologyRule checks the request-only structural rules a rule
// needs before symbology.go resolves its code-shaped targeting against
// real rows. The domain-value axis is all-or-nothing: a value code is
// meaningless without knowing which field's domain it belongs to, so
// domain_field and domain_value_code are provided together or not at all
// (the same pairing resolveSymbologySchema produces in reverse). Whether
// those codes actually resolve -- a real subtype on this class, a real
// value in that field's domain -- is a database question, checked in
// resolveRuleTargeting where the rows are available.
func validateSymbologyRule(req symbologyRuleRequest) error {
	if !validRenderContexts[req.RenderContext] {
		return invalid("render_context must be one of map, diagram")
	}
	if (req.DomainField == nil) != (req.DomainValueCode == nil) {
		return invalid("domain_field and domain_value_code must be provided together")
	}
	if req.DomainField != nil && strings.TrimSpace(*req.DomainField) == "" {
		return invalid("domain_field cannot be blank")
	}
	if req.DomainValueCode != nil && strings.TrimSpace(*req.DomainValueCode) == "" {
		return invalid("domain_value_code cannot be blank")
	}
	if req.MinZoom != nil && req.MaxZoom != nil && *req.MinZoom > *req.MaxZoom {
		return invalid("min_zoom must be less than or equal to max_zoom")
	}
	// dash names a line's stroke pattern; a value is validated whenever
	// present regardless of geometry (this request carries no geometry to
	// check against -- the renderer simply ignores dash on a non-line).
	if req.Style.Dash != nil && !validLineDashes[*req.Style.Dash] {
		return invalid("dash must be one of solid, dashed, dotted, dashdot")
	}
	return nil
}

// validateAddField checks a new field being added to an already-existing
// feature class -- the same per-field structural rules
// validateCreateFeatureClass applies within a create request, just for
// one field added on its own. Cross-references (domain_code/group_code
// resolving to a real row, required_for_subtypes codes existing on this
// class) are DB questions, checked in add.go where the actual rows are
// available.
func validateAddField(req fieldRequest) error {
	if !fieldNamePattern.MatchString(req.Name) {
		return invalid("field name %q must be lower-case letters, digits, and underscores, starting with a letter", req.Name)
	}
	if reservedFieldNames[req.Name] {
		return invalid("field name %q is reserved by the compiler's own system columns", req.Name)
	}
	if !validDataTypes[req.DataType] {
		return invalid("field %q: data_type must be one of text, numeric, integer, boolean, date, timestamp, uuid, jsonb", req.Name)
	}
	return nil
}

// validateCreateFieldGroup uses fieldNamePattern, not codePattern -- unlike
// domain/feature-class codes, every hand-written seed file's field_groups
// rows (general/electrical/maintenance/asset/...) are lower_snake_case, the
// same convention a field's own name follows. The admin-domains page's own
// Code input correctly preserves case for that reason; admin-field-groups
// lower-cases as typed to match.
func validateCreateFieldGroup(req createFieldGroupRequest) error {
	if !fieldNamePattern.MatchString(req.Code) {
		return invalid("field group code %q must be lower-case letters, digits, and underscores, starting with a letter", req.Code)
	}
	if strings.TrimSpace(req.Name) == "" {
		return invalid("field group name is required")
	}
	return nil
}

// validateUpdateFeatureClass checks the structural rules
// updateFeatureClassRequest needs before update.go even looks at the
// existing row's status: an empty Name is never valid, and touching
// GeometryType/SRID/NetworkRole requires all three together (mirroring
// createFeatureClassRequest's own geometry_type/srid/network_role
// rules) since SRID's own validity depends on GeometryType. Whether
// the request is even *allowed* to touch those three -- only while the
// class is still draft -- is a database question, checked in
// updateFeatureClass itself.
func validateUpdateFeatureClass(req updateFeatureClassRequest) error {
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		return invalid("name cannot be blank")
	}

	// functional_role (migration 0091) is pure metadata, editable at any status;
	// "" clears it to NULL, else it must be a taxonomy value.
	if req.FunctionalRole != nil && *req.FunctionalRole != "" && !validFunctionalRoles[*req.FunctionalRole] {
		return invalid("functional_role must be one of source, consumer, passthrough, transformer, controller, node, sensor, ground (or empty to clear)")
	}

	// diagram_geometry_type override (migration 0053): "" clears it, and
	// only 'point'/'line' are meaningful diagram shapes (a diagram has no
	// polygon or 'none' placement).
	if req.DiagramGeometryType != nil && *req.DiagramGeometryType != "" &&
		*req.DiagramGeometryType != "point" && *req.DiagramGeometryType != "line" {
		return invalid("diagram_geometry_type must be point, line, or empty to inherit geometry_type")
	}

	geometryTouched := req.GeometryType != nil || req.SRID != nil || req.NetworkRole != nil
	if !geometryTouched {
		return nil
	}
	if req.GeometryType == nil || req.NetworkRole == nil {
		return invalid("geometry_type, srid, and network_role must be provided together when changing any of them")
	}
	if !validGeometryTypes[*req.GeometryType] {
		return invalid("geometry_type must be one of point, line, polygon, none")
	}
	if *req.GeometryType != "none" && req.SRID == nil {
		return invalid("srid is required unless geometry_type is none")
	}
	if *req.GeometryType == "none" && req.SRID != nil {
		return invalid("srid must be omitted when geometry_type is none")
	}
	if !validNetworkRoles[*req.NetworkRole] {
		return invalid("network_role must be one of edge, junction, container, none")
	}
	return nil
}

// validateCreateFeatureClass checks everything about a new-feature-class
// request that doesn't require a database round trip (existing-domain/
// existing-group lookups happen in create.go, where the actual rows are
// available) -- structural correctness of the request itself: valid
// enums, no duplicate codes within the request, and every cross-reference
// (a field's required_for_subtypes, a terminal's subtype_code) pointing
// at a subtype actually defined in this same request.
func validateCreateFeatureClass(req createFeatureClassRequest) error {
	if err := validateCode("feature class code", req.Code); err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return invalid("feature class name is required")
	}
	if !validGeometryTypes[req.GeometryType] {
		return invalid("geometry_type must be one of point, line, polygon, none")
	}
	if req.GeometryType != "none" && req.SRID == nil {
		return invalid("srid is required unless geometry_type is none")
	}
	if req.GeometryType == "none" && req.SRID != nil {
		return invalid("srid must be omitted when geometry_type is none")
	}
	if !validNetworkRoles[req.NetworkRole] {
		return invalid("network_role must be one of edge, junction, container, none")
	}
	if req.FunctionalRole != nil && *req.FunctionalRole != "" && !validFunctionalRoles[*req.FunctionalRole] {
		return invalid("functional_role must be one of source, consumer, passthrough, transformer, controller, node, sensor, ground (or omitted)")
	}

	subtypeCodes := make(map[int]bool, len(req.Subtypes))
	for _, s := range req.Subtypes {
		if strings.TrimSpace(s.Name) == "" {
			return invalid("subtype %d needs a name", s.Code)
		}
		if subtypeCodes[s.Code] {
			return invalid("subtype code %d is repeated", s.Code)
		}
		subtypeCodes[s.Code] = true
	}

	fieldNames := make(map[string]bool, len(req.Fields))
	for _, f := range req.Fields {
		if !fieldNamePattern.MatchString(f.Name) {
			return invalid("field name %q must be lower-case letters, digits, and underscores, starting with a letter", f.Name)
		}
		if reservedFieldNames[f.Name] {
			return invalid("field name %q is reserved by the compiler's own system columns", f.Name)
		}
		if fieldNames[f.Name] {
			return invalid("field name %q is repeated", f.Name)
		}
		fieldNames[f.Name] = true
		if !validDataTypes[f.DataType] {
			return invalid("field %q: data_type must be one of text, numeric, integer, boolean, date, timestamp, uuid, jsonb", f.Name)
		}
		for _, sc := range f.RequiredForSubtypes {
			if !subtypeCodes[sc] {
				return invalid("field %q: required_for_subtypes references subtype code %d, which isn't defined in this request", f.Name, sc)
			}
		}
	}

	if len(req.Terminals) > 0 && req.NetworkRole != "edge" && req.NetworkRole != "junction" {
		return invalid("terminals are only meaningful for network_role edge or junction")
	}
	terminalCodes := make(map[string]bool, len(req.Terminals))
	for _, t := range req.Terminals {
		if strings.TrimSpace(t.Code) == "" {
			return invalid("every terminal needs a code")
		}
		key := t.Code
		if t.SubtypeCode != nil {
			key = t.Code + ":" + strconv.Itoa(*t.SubtypeCode)
		}
		if terminalCodes[key] {
			return invalid("terminal code %q is repeated for the same subtype scope", t.Code)
		}
		terminalCodes[key] = true
		if !validTerminalRoles[t.Role] {
			return invalid("terminal %q: role must be one of primary, secondary, ground, other", t.Code)
		}
		if t.SubtypeCode != nil && !subtypeCodes[*t.SubtypeCode] {
			return invalid("terminal %q: subtype_code %d isn't defined in this request", t.Code, *t.SubtypeCode)
		}
	}

	if req.Symbology != nil {
		if strings.TrimSpace(req.Symbology.IconRef) == "" {
			return invalid("symbology.icon_ref is required when symbology is provided")
		}
		if strings.TrimSpace(req.Symbology.Color) == "" {
			return invalid("symbology.color is required when symbology is provided")
		}
		if req.Symbology.Dash != nil && !validLineDashes[*req.Symbology.Dash] {
			return invalid("symbology.dash must be one of solid, dashed, dotted, dashdot")
		}
	}

	return nil
}
