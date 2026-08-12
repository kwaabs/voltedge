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

	domains, domainValues, err := loadDomains(ctx, db, fields)
	if err != nil {
		return nil, fmt.Errorf("metadata: %q: %w", code, err)
	}

	return &Loaded{
		FeatureClass:      fc,
		Fields:            fields,
		Subtypes:          subtypes,
		SubtypeFieldRules: rules,
		Domains:           domains,
		DomainValues:      domainValues,
	}, nil
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

func (l *Loaded) fieldByID(id string) *Field {
	for i := range l.Fields {
		if l.Fields[i].ID == id {
			return &l.Fields[i]
		}
	}
	return nil
}
