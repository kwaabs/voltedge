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
	}

	return nil
}
