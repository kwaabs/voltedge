package metadata

// CIM mapping catalog (Phase 16, docs/phase-16-cim-export.md). These mirror the
// three metadata.cim_* tables (migrations/0034) that drive CIM/XML export: the
// profile/config, the feature-class -> CIM-class mapping, and the field -> CIM-
// attribute mapping. This is the DECLARATIVE half of the CIM feature; the
// behavioral half (ConnectivityNode synthesis, RDF/XML serialization) lives in
// api/internal/export. The mappings are loaded resolved -- joined to
// feature_classes/subtypes/fields -- so the encoder can look them up by the
// (class code, subtype code, field name) it already has on each Feature.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// CimProfile mirrors metadata.cim_profiles -- one CIM/XML export target (e.g.
// CDPSM_CIM16): the namespace/authority URIs the RDF/XML header needs, the
// coordinate CRS for the GL profile, and the unmapped-attribute policy.
type CimProfile struct {
	bun.BaseModel `bun:"table:metadata.cim_profiles"`

	ID                 string `bun:"id,pk"`
	Code               string `bun:"code"`
	Name               string `bun:"name"`
	CimNamespace       string `bun:"cim_namespace"`
	ProfileURI         string `bun:"profile_uri"`
	ModelAuthority     string `bun:"model_authority"`
	CoordinateCRS      string `bun:"coordinate_crs"`
	UnmappedAttrPolicy string `bun:"unmapped_attr_policy"`
	IsDefault          bool   `bun:"is_default"`
}

// CimClassMapping is a resolved metadata.cim_class_mappings row: the feature
// class code and optional subtype code the encoder keys on (joined from the
// feature_class_id/subtype_id foreign keys), plus the target CIM class and the
// cim_kind that tells the encoder how to treat the feature.
type CimClassMapping struct {
	FeatureClassCode string
	SubtypeCode      *int
	CimClass         string
	CimKind          string
}

// CimAttributeMapping is a resolved metadata.cim_attribute_mappings row: the
// feature class code + field name the encoder keys on, the target CIM attribute
// (dotted ClassName.attribute), and the coercion (transform + its parameters).
type CimAttributeMapping struct {
	FeatureClassCode string
	FieldName        string
	CimAttribute     string
	Transform        string
	ScaleFactor      *float64
	ValueMap         map[string]any
}

// CIMMapping is the whole declarative mapping for one export: the chosen
// profile plus every class/attribute mapping, indexed for O(1) lookup by the
// keys the encoder walks features with.
type CIMMapping struct {
	Profile *CimProfile

	classDefault   map[string]CimClassMapping         // class code -> default (subtype NULL) row
	classBySubtype map[string]map[int]CimClassMapping // class code -> subtype code -> override row
	attrsByClass   map[string][]CimAttributeMapping   // class code -> field mappings
}

// ClassFor resolves the CIM class mapping for a feature: a subtype-scoped
// override wins over the class default. Returns ok == false when the feature
// class has no mapping at all (the encoder then treats the class as unmapped
// and skips it).
func (m *CIMMapping) ClassFor(classCode string, subtype *int) (CimClassMapping, bool) {
	if subtype != nil {
		if bySub, ok := m.classBySubtype[classCode]; ok {
			if cm, ok := bySub[*subtype]; ok {
				return cm, true
			}
		}
	}
	cm, ok := m.classDefault[classCode]
	return cm, ok
}

// AttributesFor returns the field -> CIM-attribute mappings for a feature class,
// in no particular order (the encoder emits them deterministically itself).
func (m *CIMMapping) AttributesFor(classCode string) []CimAttributeMapping {
	return m.attrsByClass[classCode]
}

// LoadCIMMapping loads the CIM mapping for an export. profileCode selects the
// metadata.cim_profiles row by code; an empty profileCode selects the
// is_default row. It returns sql.ErrNoRows (wrapped) when no matching profile
// exists so the caller can turn that into a clean "CIM mapping not configured"
// error rather than a 500.
func LoadCIMMapping(ctx context.Context, db bun.IDB, profileCode string) (*CIMMapping, error) {
	profile := new(CimProfile)
	q := db.NewSelect().Model(profile)
	if profileCode == "" {
		q = q.Where("is_default = true")
	} else {
		q = q.Where("code = ?", profileCode)
	}
	if err := q.Limit(1).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if profileCode == "" {
				return nil, fmt.Errorf("metadata: no default CIM profile configured (seed metadata.cim_profiles): %w", err)
			}
			return nil, fmt.Errorf("metadata: no CIM profile %q: %w", profileCode, err)
		}
		return nil, err
	}

	mapping := &CIMMapping{
		Profile:        profile,
		classDefault:   map[string]CimClassMapping{},
		classBySubtype: map[string]map[int]CimClassMapping{},
		attrsByClass:   map[string][]CimAttributeMapping{},
	}

	// Class mappings, joined to their feature class (code) and, for the
	// subtype-scoped rows, their subtype (integer code). A LEFT JOIN keeps the
	// class-default rows (subtype_id NULL -> subtype_code NULL).
	var classRows []struct {
		FeatureClassCode string `bun:"fc_code"`
		SubtypeCode      *int   `bun:"subtype_code"`
		CimClass         string `bun:"cim_class"`
		CimKind          string `bun:"cim_kind"`
	}
	if err := db.NewSelect().
		TableExpr("metadata.cim_class_mappings AS cm").
		ColumnExpr("fc.code AS fc_code").
		ColumnExpr("st.code AS subtype_code").
		ColumnExpr("cm.cim_class AS cim_class").
		ColumnExpr("cm.cim_kind AS cim_kind").
		Join("JOIN metadata.feature_classes AS fc ON fc.id = cm.feature_class_id").
		Join("LEFT JOIN metadata.subtypes AS st ON st.id = cm.subtype_id").
		Scan(ctx, &classRows); err != nil {
		return nil, err
	}
	for _, r := range classRows {
		cm := CimClassMapping{
			FeatureClassCode: r.FeatureClassCode,
			SubtypeCode:      r.SubtypeCode,
			CimClass:         r.CimClass,
			CimKind:          r.CimKind,
		}
		if r.SubtypeCode == nil {
			mapping.classDefault[r.FeatureClassCode] = cm
			continue
		}
		bySub := mapping.classBySubtype[r.FeatureClassCode]
		if bySub == nil {
			bySub = map[int]CimClassMapping{}
			mapping.classBySubtype[r.FeatureClassCode] = bySub
		}
		bySub[*r.SubtypeCode] = cm
	}

	// Attribute mappings, joined to their field (name) and feature class (code).
	var attrRows []struct {
		FeatureClassCode string          `bun:"fc_code"`
		FieldName        string          `bun:"field_name"`
		CimAttribute     string          `bun:"cim_attribute"`
		Transform        string          `bun:"transform"`
		ScaleFactor      *float64        `bun:"scale_factor"`
		ValueMap         json.RawMessage `bun:"value_map"`
	}
	if err := db.NewSelect().
		TableExpr("metadata.cim_attribute_mappings AS am").
		ColumnExpr("fc.code AS fc_code").
		ColumnExpr("f.name AS field_name").
		ColumnExpr("am.cim_attribute AS cim_attribute").
		ColumnExpr("am.transform AS transform").
		ColumnExpr("am.scale_factor AS scale_factor").
		ColumnExpr("am.value_map AS value_map").
		Join("JOIN metadata.fields AS f ON f.id = am.field_id").
		Join("JOIN metadata.feature_classes AS fc ON fc.id = f.feature_class_id").
		Scan(ctx, &attrRows); err != nil {
		return nil, err
	}
	for _, r := range attrRows {
		am := CimAttributeMapping{
			FeatureClassCode: r.FeatureClassCode,
			FieldName:        r.FieldName,
			CimAttribute:     r.CimAttribute,
			Transform:        r.Transform,
			ScaleFactor:      r.ScaleFactor,
		}
		if len(r.ValueMap) > 0 {
			if err := json.Unmarshal(r.ValueMap, &am.ValueMap); err != nil {
				return nil, fmt.Errorf("metadata: cim_attribute_mappings.value_map for %s.%s is not valid JSON: %w", r.FeatureClassCode, r.FieldName, err)
			}
		}
		mapping.attrsByClass[r.FeatureClassCode] = append(mapping.attrsByClass[r.FeatureClassCode], am)
	}

	return mapping, nil
}

// NewCIMMapping assembles a CIMMapping from already-resolved rows, indexed
// exactly as LoadCIMMapping indexes them. It lets callers that don't have a
// database -- unit tests of the CIM encoder, documentation sample generators --
// build a mapping from literals. A class row with a nil SubtypeCode is the class
// default; one with a SubtypeCode is the subtype override.
func NewCIMMapping(profile *CimProfile, classes []CimClassMapping, attrs []CimAttributeMapping) *CIMMapping {
	m := &CIMMapping{
		Profile:        profile,
		classDefault:   map[string]CimClassMapping{},
		classBySubtype: map[string]map[int]CimClassMapping{},
		attrsByClass:   map[string][]CimAttributeMapping{},
	}
	for _, cm := range classes {
		if cm.SubtypeCode == nil {
			m.classDefault[cm.FeatureClassCode] = cm
			continue
		}
		bySub := m.classBySubtype[cm.FeatureClassCode]
		if bySub == nil {
			bySub = map[int]CimClassMapping{}
			m.classBySubtype[cm.FeatureClassCode] = bySub
		}
		bySub[*cm.SubtypeCode] = cm
	}
	for _, am := range attrs {
		m.attrsByClass[am.FeatureClassCode] = append(m.attrsByClass[am.FeatureClassCode], am)
	}
	return m
}
