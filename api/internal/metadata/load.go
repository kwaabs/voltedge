package metadata

import (
	"context"
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

	domains, domainValues, err := loadDomains(ctx, db, fields)
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

func loadDomains(ctx context.Context, db bun.IDB, fields []Field) (map[string]*Domain, map[string][]string, error) {
	domains := map[string]*Domain{}
	domainValues := map[string][]string{}

	for _, f := range fields {
		if f.DomainID == nil {
			continue
		}
		if _, ok := domains[*f.DomainID]; ok {
			continue
		}

		d := new(Domain)
		if err := db.NewSelect().Model(d).Where("id = ?", *f.DomainID).Scan(ctx); err != nil {
			return nil, nil, fmt.Errorf("field %s references missing domain %s: %w", f.Name, *f.DomainID, err)
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
			return nil, nil, fmt.Errorf("loading values for domain %s: %w", d.Code, err)
		}
		codes := make([]string, len(values))
		for i, v := range values {
			codes[i] = v.Code
		}
		domainValues[d.ID] = codes
	}

	return domains, domainValues, nil
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
