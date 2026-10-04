package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// Loaded bundles everything the compiler or the CRUD engine needs about
// one feature class, fetched in a handful of queries rather than each
// caller re-deriving it independently.
type Loaded struct {
	FeatureClass      *FeatureClass
	Fields            []Field
	Subtypes          []Subtype
	SubtypeFieldRules []SubtypeFieldRule
	Terminals         []Terminal
	Domains           map[string]*Domain  // by domain id
	DomainValues      map[string][]string // by domain id -> coded values
	// DomainTransitions holds a coded domain's optional state machine (migration
	// 0065): domain id -> fromCode -> allowed toCodes. Almost always empty -- a
	// domain only appears here when an admin has authored transition edges for
	// it (Phase 38 wires OPERATIONAL_STATE). Codes, not value ids, so the CRUD
	// update check can compare directly against a feature's stored coded value.
	DomainTransitions map[string]map[string][]string // by domain id -> from code -> to codes
}

// TransitionAllowed reports whether a coded field backed by domainID may change
// its value from `from` to `to`. Only edges explicitly authored in
// metadata.domain_transitions are legal -- but a domain with no edges loaded is
// unconstrained (returns true always), which is every coded domain except the
// few that opt into a lifecycle. Setting a previously-unset value (from == "")
// and clearing one (to == "") are always allowed: those are first-declaration
// and un-declaration, not transitions between two declared states. A no-op
// (from == to) is allowed too.
func (l *Loaded) TransitionAllowed(domainID, from, to string) bool {
	edges := l.DomainTransitions[domainID]
	if len(edges) == 0 {
		return true // no state machine defined for this domain
	}
	if from == "" || to == "" || from == to {
		return true // first-declaration, clearing, or no change
	}
	for _, allowed := range edges[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// Load fetches a feature class by code, its active fields, its subtypes
// and their field rules, and every domain (and coded values, for coded
// domains) referenced by those fields. Only fields with status = 'active'
// are included -- deprecated fields are invisible to both the compiler
// and the CRUD engine, per docs/metadata-core.md's field lifecycle.
func Load(ctx context.Context, db bun.IDB, code string) (*Loaded, error) {
	fc := new(FeatureClass)
	if err := db.NewSelect().Model(fc).Where("code = ?", code).Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: feature class %q not found: %w", code, err)
	}

	var fields []Field
	if err := db.NewSelect().Model(&fields).
		Where("feature_class_id = ?", fc.ID).
		Where("status = ?", "active").
		Order("name").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: loading fields for %q: %w", code, err)
	}

	var subtypes []Subtype
	if err := db.NewSelect().Model(&subtypes).
		Where("feature_class_id = ?", fc.ID).
		Order("code").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: loading subtypes for %q: %w", code, err)
	}

	var rules []SubtypeFieldRule
	if len(subtypes) > 0 {
		subtypeIDs := make([]string, len(subtypes))
		for i, s := range subtypes {
			subtypeIDs[i] = s.ID
		}
		if err := db.NewSelect().Model(&rules).
			Where("subtype_id IN (?)", bun.In(subtypeIDs)).
			Scan(ctx); err != nil {
			return nil, fmt.Errorf("metadata: loading subtype_field_rules for %q: %w", code, err)
		}
	}

	var terminals []Terminal
	if err := db.NewSelect().Model(&terminals).
		Where("feature_class_id = ?", fc.ID).
		Order("code").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: loading terminals for %q: %w", code, err)
	}

	domains, domainValues, transitions, err := loadDomains(ctx, db, fields)
	if err != nil {
		return nil, fmt.Errorf("metadata: %q: %w", code, err)
	}

	return &Loaded{
		FeatureClass:      fc,
		Fields:            fields,
		Subtypes:          subtypes,
		SubtypeFieldRules: rules,
		Terminals:         terminals,
		Domains:           domains,
		DomainValues:      domainValues,
		DomainTransitions: transitions,
	}, nil
}

// LoadNetworkRules returns every network_rules row for a specific
// from/to feature-class pair, valid or not -- unlike Load, which only
// ever surfaces active data. api/internal/connectivity needs both: a
// disabled rule (is_valid = false) that would otherwise match is a
// different rejection reason than no matching rule existing at all, and
// that distinction only exists in the full set.
func LoadNetworkRules(ctx context.Context, db bun.IDB, fromFeatureClassID, toFeatureClassID string) ([]NetworkRule, error) {
	var rules []NetworkRule
	if err := db.NewSelect().Model(&rules).
		Where("from_feature_class_id = ?", fromFeatureClassID).
		Where("to_feature_class_id = ?", toFeatureClassID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: loading network_rules: %w", err)
	}
	return rules, nil
}

// LoadNetworkRulesForClass returns every network rule where
// featureClassID appears on either side -- for browsing "what can this
// class connect to, or be connected from" as a whole (api/internal/rules),
// unlike LoadNetworkRules's own from/to-pair-scoped query for validating
// one specific proposed connection (api/internal/connectivity).
func LoadNetworkRulesForClass(ctx context.Context, db bun.IDB, featureClassID string) ([]NetworkRule, error) {
	var rules []NetworkRule
	if err := db.NewSelect().Model(&rules).
		Where("from_feature_class_id = ? OR to_feature_class_id = ?", featureClassID, featureClassID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: loading network_rules for class: %w", err)
	}
	return rules, nil
}

// LoadSnappingRulesForClass returns every snapping rule where
// featureClassID appears on either side -- as the class being digitized
// (from) or as an eligible snap target for some other class (to).
func LoadSnappingRulesForClass(ctx context.Context, db bun.IDB, featureClassID string) ([]SnappingRule, error) {
	var rules []SnappingRule
	if err := db.NewSelect().Model(&rules).
		Where("from_feature_class_id = ? OR to_feature_class_id = ?", featureClassID, featureClassID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: loading snapping_rules for class: %w", err)
	}
	return rules, nil
}

// LoadPanelTemplate resolves the device fan-out recipe for one (panel feature
// class, panel role) pairing (migration 0072) -- the ordered list of devices to
// create when a panel of that class and role is placed. panelClassCode is the
// panel's own feature class (e.g. MV_SWITCHGEAR_PANEL); roleCode is the panel's
// role value on the shared BAY_ROLE domain (e.g. FEEDER). Each returned device
// carries its class code (ready to metadata.Load), subtype, decoded default
// attributes, and mimic offset, in position order. An empty result means no
// template is defined for that pairing -- the caller (api/internal/panel) turns
// that into a clear client error rather than creating an empty panel. Device
// attributes are stored as jsonb and decoded here so the composite create can
// merge them straight into a CreateFeatureInput.
func LoadPanelTemplate(ctx context.Context, db bun.IDB, panelClassCode, roleCode string) ([]PanelTemplateDevice, error) {
	const query = `
SELECT dfc.code, pti.subtype_code, pti.attributes, pti.offset_x, pti.offset_y, pti.rotation
FROM metadata.panel_template_items pti
JOIN metadata.panel_templates pt ON pt.id = pti.template_id
JOIN metadata.feature_classes pfc ON pfc.id = pt.panel_feature_class_id
JOIN metadata.domain_values rv ON rv.id = pt.role_value_id
JOIN metadata.feature_classes dfc ON dfc.id = pti.device_feature_class_id
WHERE pfc.code = ? AND rv.code = ?
ORDER BY pti.position`

	rows, err := db.QueryContext(ctx, query, panelClassCode, roleCode)
	if err != nil {
		return nil, fmt.Errorf("metadata: loading panel template for %s/%s: %w", panelClassCode, roleCode, err)
	}
	defer rows.Close()

	var devices []PanelTemplateDevice
	for rows.Next() {
		var (
			d        PanelTemplateDevice
			rawAttrs []byte
		)
		if err := rows.Scan(&d.DeviceFeatureClass, &d.SubtypeCode, &rawAttrs, &d.OffsetX, &d.OffsetY, &d.Rotation); err != nil {
			return nil, fmt.Errorf("metadata: scanning panel template item: %w", err)
		}
		if len(rawAttrs) > 0 {
			if err := json.Unmarshal(rawAttrs, &d.Attributes); err != nil {
				return nil, fmt.Errorf("metadata: decoding panel template item attributes for %s: %w", d.DeviceFeatureClass, err)
			}
		}
		devices = append(devices, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metadata: reading panel template for %s/%s: %w", panelClassCode, roleCode, err)
	}
	return devices, nil
}

// LoadRMUTemplate resolves the fan-out recipe for one RMU (class, config): the
// single internal bus class to create, plus every switched way in position
// order (class code, subtype, decoded default attributes, the terminal wired to
// the bus, and mimic offset). Returns a nil result with no error when the RMU
// class/config pairing has no template row -- the caller treats that as "no
// fan-out defined" and rejects it, the same way the panel endpoint does. This
// is the RMU analogue of LoadPanelTemplate, with the added busbar class + per-
// way bus_terminal the RMU's internal connectivity needs.
func LoadRMUTemplate(ctx context.Context, db bun.IDB, rmuClassCode, configCode string) (*RMUTemplateResolved, error) {
	const busQuery = `
SELECT bfc.code
FROM metadata.rmu_templates rt
JOIN metadata.feature_classes rfc ON rfc.id = rt.rmu_feature_class_id
JOIN metadata.domain_values cv ON cv.id = rt.config_value_id
JOIN metadata.feature_classes bfc ON bfc.id = rt.busbar_feature_class_id
WHERE rfc.code = ? AND cv.code = ?`

	var busbarClass string
	switch err := db.QueryRowContext(ctx, busQuery, rmuClassCode, configCode).Scan(&busbarClass); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("metadata: loading rmu template for %s/%s: %w", rmuClassCode, configCode, err)
	}

	const wayQuery = `
SELECT wfc.code, rti.subtype_code, rti.attributes, rti.bus_terminal, rti.offset_x, rti.offset_y, rti.rotation,
       cfc.code, rti.cable_subtype_code, rti.cable_attributes, rti.cable_terminal
FROM metadata.rmu_template_items rti
JOIN metadata.rmu_templates rt ON rt.id = rti.template_id
JOIN metadata.feature_classes rfc ON rfc.id = rt.rmu_feature_class_id
JOIN metadata.domain_values cv ON cv.id = rt.config_value_id
JOIN metadata.feature_classes wfc ON wfc.id = rti.way_feature_class_id
LEFT JOIN metadata.feature_classes cfc ON cfc.id = rti.cable_feature_class_id
WHERE rfc.code = ? AND cv.code = ?
ORDER BY rti.position`

	rows, err := db.QueryContext(ctx, wayQuery, rmuClassCode, configCode)
	if err != nil {
		return nil, fmt.Errorf("metadata: loading rmu template ways for %s/%s: %w", rmuClassCode, configCode, err)
	}
	defer rows.Close()

	resolved := &RMUTemplateResolved{BusbarFeatureClass: busbarClass}
	for rows.Next() {
		var (
			w             RMUTemplateWay
			rawAttrs      []byte
			rawCableAttrs []byte
		)
		// CableFeatureClass / CableSubtypeCode / CableTerminal are nullable
		// columns scanned straight into their *string / *int fields -- a NULL
		// lands as a nil pointer (the same way SubtypeCode already scans a
		// nullable subtype_code), which the fan-out reads as "no cable".
		if err := rows.Scan(&w.WayFeatureClass, &w.SubtypeCode, &rawAttrs, &w.BusTerminal, &w.OffsetX, &w.OffsetY, &w.Rotation,
			&w.CableFeatureClass, &w.CableSubtypeCode, &rawCableAttrs, &w.CableTerminal); err != nil {
			return nil, fmt.Errorf("metadata: scanning rmu template way: %w", err)
		}
		if len(rawAttrs) > 0 {
			if err := json.Unmarshal(rawAttrs, &w.Attributes); err != nil {
				return nil, fmt.Errorf("metadata: decoding rmu template way attributes for %s: %w", w.WayFeatureClass, err)
			}
		}
		if len(rawCableAttrs) > 0 {
			if err := json.Unmarshal(rawCableAttrs, &w.CableAttributes); err != nil {
				return nil, fmt.Errorf("metadata: decoding rmu template cable attributes for %s: %w", w.WayFeatureClass, err)
			}
		}
		resolved.Ways = append(resolved.Ways, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metadata: reading rmu template ways for %s/%s: %w", rmuClassCode, configCode, err)
	}
	return resolved, nil
}

// LoadPackagedSubTemplate resolves the fan-out recipe for one packaged
// substation (class, config): every internal component in chain (position)
// order -- class code, subtype, decoded default attributes, the two terminals
// wiring it to the previous component, and mimic offset. Returns a nil result
// with no error when the class/config pairing has no template row -- the caller
// treats that as "no fan-out defined" and rejects it, the same way the RMU and
// panel endpoints do. This is the chain analogue of LoadRMUTemplate: instead of
// one shared-bus terminal per way, each item carries own_terminal +
// upstream_terminal so the fan-out can wire it to the PRECEDING component.
func LoadPackagedSubTemplate(ctx context.Context, db bun.IDB, packagedClassCode, configCode string) (*PackagedSubTemplateResolved, error) {
	const headerQuery = `
SELECT 1
FROM metadata.packaged_substation_templates pt
JOIN metadata.feature_classes pfc ON pfc.id = pt.packaged_feature_class_id
JOIN metadata.domain_values cv ON cv.id = pt.config_value_id
WHERE pfc.code = ? AND cv.code = ?`

	var exists int
	switch err := db.QueryRowContext(ctx, headerQuery, packagedClassCode, configCode).Scan(&exists); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("metadata: loading packaged substation template for %s/%s: %w", packagedClassCode, configCode, err)
	}

	const itemQuery = `
SELECT cfc.code, psi.subtype_code, psi.attributes, psi.own_terminal, psi.upstream_terminal, psi.offset_x, psi.offset_y, psi.rotation
FROM metadata.packaged_substation_items psi
JOIN metadata.packaged_substation_templates pt ON pt.id = psi.template_id
JOIN metadata.feature_classes pfc ON pfc.id = pt.packaged_feature_class_id
JOIN metadata.domain_values cv ON cv.id = pt.config_value_id
JOIN metadata.feature_classes cfc ON cfc.id = psi.component_feature_class_id
WHERE pfc.code = ? AND cv.code = ?
ORDER BY psi.position`

	rows, err := db.QueryContext(ctx, itemQuery, packagedClassCode, configCode)
	if err != nil {
		return nil, fmt.Errorf("metadata: loading packaged substation items for %s/%s: %w", packagedClassCode, configCode, err)
	}
	defer rows.Close()

	resolved := &PackagedSubTemplateResolved{}
	for rows.Next() {
		var (
			it       PackagedSubItem
			rawAttrs []byte
		)
		if err := rows.Scan(&it.ComponentFeatureClass, &it.SubtypeCode, &rawAttrs, &it.OwnTerminal, &it.UpstreamTerminal, &it.OffsetX, &it.OffsetY, &it.Rotation); err != nil {
			return nil, fmt.Errorf("metadata: scanning packaged substation item: %w", err)
		}
		if len(rawAttrs) > 0 {
			if err := json.Unmarshal(rawAttrs, &it.Attributes); err != nil {
				return nil, fmt.Errorf("metadata: decoding packaged substation item attributes for %s: %w", it.ComponentFeatureClass, err)
			}
		}
		resolved.Items = append(resolved.Items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metadata: reading packaged substation items for %s/%s: %w", packagedClassCode, configCode, err)
	}
	return resolved, nil
}

// LoadAssemblyTemplate resolves one general kit-assembly recipe (Phase 57) for the
// (anchor class, code) pair: the anchor subtype override plus every component in
// position order, each with its structural link (support_*) and electrical target
// (wire_to_position + terminals). Returns (nil, nil) when no template exists for
// the pair so the caller can answer "no kit defined" with a clean 400. Read by
// api/internal/assembly.
func LoadAssemblyTemplate(ctx context.Context, db bun.IDB, anchorClassCode, code string) (*AssemblyTemplateResolved, error) {
	const headerQuery = `
SELECT at.anchor_subtype_code
FROM metadata.assembly_templates at
JOIN metadata.feature_classes afc ON afc.id = at.anchor_feature_class_id
WHERE afc.code = ? AND at.code = ?`

	resolved := &AssemblyTemplateResolved{}
	switch err := db.QueryRowContext(ctx, headerQuery, anchorClassCode, code).Scan(&resolved.AnchorSubtypeCode); {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("metadata: loading assembly template for %s/%s: %w", anchorClassCode, code, err)
	}

	const itemQuery = `
SELECT cfc.code, ai.subtype_code, ai.attributes, ai.support_kind, ai.support_parent_position,
       ai.wire_to_position, ai.own_terminal, ai.upstream_terminal, ai.position,
       ai.offset_x, ai.offset_y, ai.rotation
FROM metadata.assembly_items ai
JOIN metadata.assembly_templates at ON at.id = ai.template_id
JOIN metadata.feature_classes afc ON afc.id = at.anchor_feature_class_id
JOIN metadata.feature_classes cfc ON cfc.id = ai.component_feature_class_id
WHERE afc.code = ? AND at.code = ?
ORDER BY ai.position`

	rows, err := db.QueryContext(ctx, itemQuery, anchorClassCode, code)
	if err != nil {
		return nil, fmt.Errorf("metadata: loading assembly items for %s/%s: %w", anchorClassCode, code, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			it       AssemblyItem
			rawAttrs []byte
		)
		if err := rows.Scan(&it.ComponentFeatureClass, &it.SubtypeCode, &rawAttrs, &it.SupportKind, &it.SupportParentPosition,
			&it.WireToPosition, &it.OwnTerminal, &it.UpstreamTerminal, &it.Position,
			&it.OffsetX, &it.OffsetY, &it.Rotation); err != nil {
			return nil, fmt.Errorf("metadata: scanning assembly item: %w", err)
		}
		if len(rawAttrs) > 0 {
			if err := json.Unmarshal(rawAttrs, &it.Attributes); err != nil {
				return nil, fmt.Errorf("metadata: decoding assembly item attributes for %s: %w", it.ComponentFeatureClass, err)
			}
		}
		resolved.Items = append(resolved.Items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("metadata: reading assembly items for %s/%s: %w", anchorClassCode, code, err)
	}
	return resolved, nil
}

func loadDomains(ctx context.Context, db bun.IDB, fields []Field) (map[string]*Domain, map[string][]string, map[string]map[string][]string, error) {
	domains := map[string]*Domain{}
	domainValues := map[string][]string{}
	transitions := map[string]map[string][]string{}

	for _, f := range fields {
		if f.DomainID == nil {
			continue
		}
		if _, ok := domains[*f.DomainID]; ok {
			continue
		}

		d := new(Domain)
		if err := db.NewSelect().Model(d).Where("id = ?", *f.DomainID).Scan(ctx); err != nil {
			return nil, nil, nil, fmt.Errorf("field %s references missing domain %s: %w", f.Name, *f.DomainID, err)
		}
		domains[d.ID] = d

		if d.DomainType != "coded" {
			continue
		}

		var values []DomainValue
		if err := db.NewSelect().Model(&values).
			Where("domain_id = ?", d.ID).
			Order("sort_order").
			Scan(ctx); err != nil {
			return nil, nil, nil, fmt.Errorf("loading values for domain %s: %w", d.Code, err)
		}
		codes := make([]string, len(values))
		byID := make(map[string]string, len(values))
		for i, v := range values {
			codes[i] = v.Code
			byID[v.ID] = v.Code
		}
		domainValues[d.ID] = codes

		// Load any state-machine edges authored for this coded domain (migration
		// 0065). Almost always none -- only a domain with a real lifecycle
		// (OPERATIONAL_STATE) has rows, and then the CRUD update path enforces
		// them. Resolve the value-id FKs back to codes here so the check can
		// compare directly against a feature's stored coded value.
		var edges []DomainTransition
		if err := db.NewSelect().Model(&edges).
			Where("domain_id = ?", d.ID).
			Scan(ctx); err != nil {
			return nil, nil, nil, fmt.Errorf("loading transitions for domain %s: %w", d.Code, err)
		}
		if len(edges) > 0 {
			adj := map[string][]string{}
			for _, e := range edges {
				from, okFrom := byID[e.FromValueID]
				to, okTo := byID[e.ToValueID]
				if okFrom && okTo {
					adj[from] = append(adj[from], to)
				}
			}
			transitions[d.ID] = adj
		}
	}

	return domains, domainValues, transitions, nil
}

// FieldByName looks up a loaded field by its physical column name.
func (l *Loaded) FieldByName(name string) *Field {
	for i := range l.Fields {
		if l.Fields[i].Name == name {
			return &l.Fields[i]
		}
	}
	return nil
}

// SubtypeByCode looks up a loaded subtype by its integer code.
func (l *Loaded) SubtypeByCode(code int) *Subtype {
	for i := range l.Subtypes {
		if l.Subtypes[i].Code == code {
			return &l.Subtypes[i]
		}
	}
	return nil
}

// RequiredFieldNames returns the names of fields required for the given
// subtype id (per subtype_field_rules.is_required).
func (l *Loaded) RequiredFieldNames(subtypeID string) []string {
	var names []string
	for _, r := range l.SubtypeFieldRules {
		if r.SubtypeID != subtypeID || !r.IsRequired {
			continue
		}
		if f := l.fieldByID(r.FieldID); f != nil {
			names = append(names, f.Name)
		}
	}
	return names
}

// RequiredSubtypeCodes returns the integer subtype codes for which the given
// field is required (per subtype_field_rules.is_required) -- the inverse view of
// RequiredFieldNames, keyed by field instead of subtype. This is what the web
// schema exposes as required_for_subtypes, and what the field pack ships per
// field so the offline editor can mark a field required for the feature's
// actual subtype rather than guessing from column nullability.
func (l *Loaded) RequiredSubtypeCodes(fieldID string) []int {
	var codes []int
	for _, r := range l.SubtypeFieldRules {
		if r.FieldID != fieldID || !r.IsRequired {
			continue
		}
		if s := l.subtypeByID(r.SubtypeID); s != nil {
			codes = append(codes, s.Code)
		}
	}
	return codes
}

// ListFeatureClasses returns every active feature class -- the code
// (Martin's tile source ids are always its lowercased form) alongside
// the human-readable name an admin actually set, plus enough shape info
// for a client to know what it's looking at (geometry_type,
// network_role) without a second round trip. Active only: a draft class
// has no physical table, so it can't appear on a map or in Martin's
// catalog regardless.
func ListFeatureClasses(ctx context.Context, db bun.IDB) ([]FeatureClass, error) {
	var classes []FeatureClass
	if err := db.NewSelect().Model(&classes).
		Where("status = ?", "active").
		Order("code").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: listing feature classes: %w", err)
	}
	return classes, nil
}

// ListAllFeatureClasses is ListFeatureClasses without the active-only
// filter -- what api/internal/admin's catalog view needs (a draft class
// not yet compiled is exactly what an admin browsing this list wants to
// see, not something to hide), unlike every other consumer of this
// package, which only ever cares about classes real features can
// already be written against.
func ListAllFeatureClasses(ctx context.Context, db bun.IDB) ([]FeatureClass, error) {
	var classes []FeatureClass
	if err := db.NewSelect().Model(&classes).
		Order("code").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: listing all feature classes: %w", err)
	}
	return classes, nil
}

// ListDomains returns every domain, and ListDomainValues returns every
// value for every domain in one query (not one query per domain) --
// api/internal/admin's own domain picker and management view both want
// the full set at once, unlike Loaded's own per-feature-class scoping.
func ListDomains(ctx context.Context, db bun.IDB) ([]Domain, error) {
	var domains []Domain
	if err := db.NewSelect().Model(&domains).Order("code").Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: listing domains: %w", err)
	}
	return domains, nil
}

func ListDomainValues(ctx context.Context, db bun.IDB) ([]DomainValue, error) {
	var values []DomainValue
	if err := db.NewSelect().Model(&values).Order("domain_id", "sort_order", "code").Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: listing domain values: %w", err)
	}
	return values, nil
}

// ListFieldGroups returns every field group -- a small, global,
// reusable set, cheap enough to always fetch in full for a picker.
func ListFieldGroups(ctx context.Context, db bun.IDB) ([]FieldGroup, error) {
	var groups []FieldGroup
	if err := db.NewSelect().Model(&groups).Order("sort_order", "code").Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: listing field groups: %w", err)
	}
	return groups, nil
}

// ListMaterialSpecs returns every material spec -- a small, global
// catalog (mirrors ListFieldGroups above), cheap enough to always fetch
// in full for a picker (the domains admin page's own "attach a material
// spec to this value" affordance) rather than a lookup-by-code endpoint.
func ListMaterialSpecs(ctx context.Context, db bun.IDB) ([]MaterialSpec, error) {
	var specs []MaterialSpec
	if err := db.NewSelect().Model(&specs).Order("category", "code").Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: listing material specs: %w", err)
	}
	return specs, nil
}

// ListCostRates returns every cost_rates row -- the whole unit-rate price book
// (migration 0127), a small global catalog fetched in full and indexed by the
// BoQ engine per (feature class, subtype).
func ListCostRates(ctx context.Context, db bun.IDB) ([]CostRate, error) {
	var rates []CostRate
	if err := db.NewSelect().Model(&rates).Order("feature_class_code", "subtype_code").Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: listing cost rates: %w", err)
	}
	return rates, nil
}

// ListCurrencies returns every currencies row (migration 0127) -- a small global
// set, base row plus any presentation currencies, fetched whole for the BoQ
// currency picker and base-amount conversion.
func ListCurrencies(ctx context.Context, db bun.IDB) ([]Currency, error) {
	var currencies []Currency
	if err := db.NewSelect().Model(&currencies).Order("is_base DESC", "code").Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: listing currencies: %w", err)
	}
	return currencies, nil
}

// ListFeatureCostOverrides returns every feature_cost_overrides row (migration
// 0128) -- the whole per-feature override set, a small global table fetched in
// full and indexed by the BoQ engine per (feature class, feature id). Optionally
// narrowed to one class when classCode is non-empty (the feature panel's
// single-feature lookup still filters in Go, but a class scan prefers this).
func ListFeatureCostOverrides(ctx context.Context, db bun.IDB, classCode string) ([]FeatureCostOverride, error) {
	var overrides []FeatureCostOverride
	q := db.NewSelect().Model(&overrides)
	if classCode != "" {
		q = q.Where("feature_class_code = ?", classCode)
	}
	if err := q.Order("feature_class_code", "feature_id").Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: listing feature cost overrides: %w", err)
	}
	return overrides, nil
}

// ListSymbologyIcons returns every symbology_icons row -- a small,
// global set (mirrors ListMaterialSpecs/ListFieldGroups), served whole to
// the frontend at GET /symbology/icons and cached there, since a rule can
// reference any icon regardless of feature class. Ordered by icon_ref for
// a stable admin-picker list.
func ListSymbologyIcons(ctx context.Context, db bun.IDB) ([]SymbologyIcon, error) {
	var icons []SymbologyIcon
	if err := db.NewSelect().Model(&icons).Order("icon_ref").Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: listing symbology icons: %w", err)
	}
	return icons, nil
}

// ListDiagramStencils returns every diagram_stencils row -- the whole symbol
// stencil palette (Phase 24 Slice 2, migration 0048), served whole at GET
// /symbology/stencils and cached client-side, same as ListSymbologyIcons. The
// glyph markup itself comes from symbology_icons; this is only the catalog
// (which icons are stencils, plus label/category/order). Ordered by category
// then sort_order for a stable, grouped palette, icon_ref as the final tiebreak.
func ListDiagramStencils(ctx context.Context, db bun.IDB) ([]DiagramStencil, error) {
	var stencils []DiagramStencil
	if err := db.NewSelect().Model(&stencils).Order("category", "sort_order", "icon_ref").Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: listing diagram stencils: %w", err)
	}
	return stencils, nil
}

// LoadRelationshipClasses returns every relationship_classes row for a
// specific origin/destination feature-class pair. Unlike NetworkRule
// there's no valid/invalid split to preserve here -- a row either exists
// (legal) or it doesn't (rejected) -- but there can still legitimately be
// more than one row for the same pairing (different kind), which is why
// this returns a slice rather than assuming a single match:
// api/internal/relationship disambiguates by kind when more than one
// applies.
func LoadRelationshipClasses(ctx context.Context, db bun.IDB, originFeatureClassID, destinationFeatureClassID string) ([]RelationshipClass, error) {
	var classes []RelationshipClass
	if err := db.NewSelect().Model(&classes).
		Where("origin_feature_class_id = ?", originFeatureClassID).
		Where("destination_feature_class_id = ?", destinationFeatureClassID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: loading relationship_classes: %w", err)
	}
	return classes, nil
}

// LoadRelationshipClassesForClass returns every relationship class where
// featureClassID appears on either side -- for browsing "what can this
// class relate to" as a whole (api/internal/rules), unlike
// LoadRelationshipClasses's own origin/destination-pair-scoped query for
// validating one specific proposed relationship (api/internal/relationship).
func LoadRelationshipClassesForClass(ctx context.Context, db bun.IDB, featureClassID string) ([]RelationshipClass, error) {
	var classes []RelationshipClass
	if err := db.NewSelect().Model(&classes).
		Where("origin_feature_class_id = ? OR destination_feature_class_id = ?", featureClassID, featureClassID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: loading relationship_classes for class: %w", err)
	}
	return classes, nil
}

// LoadTraceConfiguration fetches a trace_configurations row by its
// stable Code (e.g. "DOWNSTREAM_TRACE") -- api/internal/trace's entry
// point for resolving which profile a trace request named.
func LoadTraceConfiguration(ctx context.Context, db bun.IDB, code string) (*TraceConfiguration, error) {
	tc := new(TraceConfiguration)
	if err := db.NewSelect().Model(tc).Where("code = ?", code).Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: trace configuration %q not found: %w", code, err)
	}
	return tc, nil
}

// ListTraceConfigurations returns every trace_configurations row --
// backs a real listing endpoint (api/internal/trace) so a client (or a
// future admin UI authoring these freely) can discover what's available
// instead of a fixed set of hardcoded codes.
func ListTraceConfigurations(ctx context.Context, db bun.IDB) ([]TraceConfiguration, error) {
	var configs []TraceConfiguration
	if err := db.NewSelect().Model(&configs).Order("code").Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: listing trace configurations: %w", err)
	}
	return configs, nil
}

// LoadTraceBarrierRules returns every trace_barrier_rules row for one
// trace_configurations id -- empty when no barrier condition was
// authored for this profile (or, at the caller's discretion, when
// StopAtOpenDevices is false and barriers shouldn't be consulted at
// all regardless of what's authored here).
func LoadTraceBarrierRules(ctx context.Context, db bun.IDB, traceConfigurationID string) ([]TraceBarrierRule, error) {
	var rules []TraceBarrierRule
	if err := db.NewSelect().Model(&rules).
		Where("trace_configuration_id = ?", traceConfigurationID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: loading trace_barrier_rules: %w", err)
	}
	return rules, nil
}

// LoadSymbologyRules returns every symbology_rules row for one feature
// class, both render_context values included -- callers filter to
// "map" or "diagram" themselves (diagram rules have nothing to render
// against yet, no diagram_instance/diagram_placements table exists,
// but there's no reason to hide them from a caller that might want to
// know they're there).
func LoadSymbologyRules(ctx context.Context, db bun.IDB, featureClassID string) ([]SymbologyRule, error) {
	var rules []SymbologyRule
	if err := db.NewSelect().Model(&rules).
		Where("feature_class_id = ?", featureClassID).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: loading symbology_rules: %w", err)
	}
	return rules, nil
}

// DomainValueCode resolves a domain_values id to its stable Code (e.g.
// "DOWNSTREAM", "OPEN") -- trace_configurations.direction_value_id and
// trace_barrier_rules.halts_on_value_id are both FKs a caller needs
// turned into a comparable string, the same way a field's own coded
// value already is by the time it reaches a feature's attributes.
func DomainValueCode(ctx context.Context, db bun.IDB, id string) (string, error) {
	dv := new(DomainValue)
	if err := db.NewSelect().Model(dv).Where("id = ?", id).Scan(ctx); err != nil {
		return "", fmt.Errorf("metadata: domain value %s not found: %w", id, err)
	}
	return dv.Code, nil
}

// ResolveDomainValueByCode is DomainValueCode's inverse -- a caller
// accepting a human-typed domain value (e.g. api/internal/diagram's
// "diagram_type": "ONE_LINE" in a create request) needs the id to store
// as a FK, the same direction api/internal/crud already resolves a
// field's own coded value in. Scoped by domain code, not just value
// code, since a bare value code alone isn't guaranteed unique across
// every domain in the system.
func ResolveDomainValueByCode(ctx context.Context, db bun.IDB, domainCode, valueCode string) (*DomainValue, error) {
	domain := new(Domain)
	if err := db.NewSelect().Model(domain).Where("code = ?", domainCode).Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: domain %q not found: %w", domainCode, err)
	}
	dv := new(DomainValue)
	if err := db.NewSelect().Model(dv).Where("domain_id = ? AND code = ?", domain.ID, valueCode).Scan(ctx); err != nil {
		return nil, fmt.Errorf("metadata: domain value %q not found in domain %q: %w", valueCode, domainCode, err)
	}
	return dv, nil
}

// ResolveTerminal finds the terminal matching a code for a given
// subtype (or no subtype, if the feature class doesn't have any). A
// terminal scoped to the specific subtype wins over one that applies to
// every subtype of the feature class (subtype_id null) -- same
// most-specific-wins principle as symbology resolution.
func (l *Loaded) ResolveTerminal(subtypeID *string, code string) *Terminal {
	var generic *Terminal
	for i := range l.Terminals {
		t := &l.Terminals[i]
		if t.Code != code {
			continue
		}
		if t.SubtypeID == nil {
			generic = t
			continue
		}
		if subtypeID != nil && *t.SubtypeID == *subtypeID {
			return t
		}
	}
	return generic
}

// TerminalByID looks up a loaded terminal by id, for formatting a
// resolved terminal id back into its human-readable code.
func (l *Loaded) TerminalByID(id string) *Terminal {
	for i := range l.Terminals {
		if l.Terminals[i].ID == id {
			return &l.Terminals[i]
		}
	}
	return nil
}

func (l *Loaded) fieldByID(id string) *Field {
	for i := range l.Fields {
		if l.Fields[i].ID == id {
			return &l.Fields[i]
		}
	}
	return nil
}

func (l *Loaded) subtypeByID(id string) *Subtype {
	for i := range l.Subtypes {
		if l.Subtypes[i].ID == id {
			return &l.Subtypes[i]
		}
	}
	return nil
}
