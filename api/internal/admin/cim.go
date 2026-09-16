package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// This file is the write path for the three metadata.cim_* tables that drive
// CIM/XML export (Phase 55, docs/phase-55-cim-mapping-admin.md): cim_profiles
// (the export target/config), cim_class_mappings (feature class -> CIM class +
// kind), and cim_attribute_mappings (feature field -> CIM attribute + coercion),
// all defined by migrations/0034 (0035 widened the transform CHECK with
// 'phase'). Authored only by seed/SQL until now; the admin editor writes
// through these so a user never has to reach for psql. The read-side loader,
// metadata.LoadCIMMapping, is deliberately NOT reused here: it drops the row id
// (needed to edit/delete) and the unit column, so this file has its own
// admin-shaped SELECTs that keep both.
//
// A profile is keyed by code like a domain; class/attribute mappings have no
// natural key (they're global, and two attribute rows can share a field), so
// they're edited and deleted by their uuid id, the same as symbology rules.

// cimClassMappingModel is the bun insert/update model for
// metadata.cim_class_mappings -- metadata.cim.go only models the resolved
// (code-shaped) read shape, so the raw id-carrying row lives here where the
// write path needs it. created_at is unmapped, so it takes its DB default.
type cimClassMappingModel struct {
	bun.BaseModel `bun:"table:metadata.cim_class_mappings"`

	ID             string  `bun:"id,pk"`
	FeatureClassID string  `bun:"feature_class_id"`
	SubtypeID      *string `bun:"subtype_id"`
	CimClass       string  `bun:"cim_class"`
	CimKind        string  `bun:"cim_kind"`
}

// cimAttributeMappingModel is the bun insert/update model for
// metadata.cim_attribute_mappings -- same reasoning as cimClassMappingModel.
// value_map is a raw jsonb passthrough (marshalled from the request, parsed
// back for the response).
type cimAttributeMappingModel struct {
	bun.BaseModel `bun:"table:metadata.cim_attribute_mappings"`

	ID           string          `bun:"id,pk"`
	FieldID      string          `bun:"field_id"`
	CimAttribute string          `bun:"cim_attribute"`
	Transform    string          `bun:"transform"`
	ScaleFactor  *float64        `bun:"scale_factor"`
	Unit         *string         `bun:"unit"`
	ValueMap     json.RawMessage `bun:"value_map"`
}

// ---- CIM profiles ---------------------------------------------------------

// listCimProfiles returns every metadata.cim_profiles row, code-ordered --
// the editor's profile list. Reuses metadata.CimProfile (the one cim_* table
// metadata.go already models with a table tag).
func listCimProfiles(ctx context.Context, db *bun.DB) ([]cimProfileResponse, error) {
	var profiles []metadata.CimProfile
	if err := db.NewSelect().Model(&profiles).Order("code").Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]cimProfileResponse, len(profiles))
	for i, p := range profiles {
		out[i] = cimProfileResponse{
			Code: p.Code, Name: p.Name, CimNamespace: p.CimNamespace,
			ProfileURI: p.ProfileURI, ModelAuthority: p.ModelAuthority,
			CoordinateCRS: p.CoordinateCRS, UnmappedAttrPolicy: p.UnmappedAttrPolicy,
			IsDefault: p.IsDefault,
		}
	}
	return out, nil
}

// createCimProfile inserts one export-target profile -- existence-checked
// first on the code, the same friendly-400-over-raw-unique-violation
// discipline createDomain applies. CoordinateCRS/UnmappedAttrPolicy fall back
// to the column defaults when omitted (they're excluded from the request only
// as optional pointers, so an omitted one isn't written as an empty string).
func createCimProfile(ctx context.Context, db *bun.DB, req createCimProfileRequest) (*cimProfileResponse, error) {
	if err := validateCreateCimProfile(req); err != nil {
		return nil, err
	}

	coordinateCRS := "urn:ogc:def:crs:EPSG::4326"
	if req.CoordinateCRS != nil && *req.CoordinateCRS != "" {
		coordinateCRS = *req.CoordinateCRS
	}
	policy := "drop"
	if req.UnmappedAttrPolicy != nil && *req.UnmappedAttrPolicy != "" {
		policy = *req.UnmappedAttrPolicy
	}
	isDefault := req.IsDefault != nil && *req.IsDefault

	var resp cimProfileResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		exists, err := codeExists(ctx, tx, (*metadata.CimProfile)(nil), req.Code)
		if err != nil {
			return err
		}
		if exists {
			return invalid("a CIM profile with code %q already exists", req.Code)
		}

		p := &metadata.CimProfile{
			Code: req.Code, Name: req.Name, CimNamespace: req.CimNamespace,
			ProfileURI: req.ProfileURI, ModelAuthority: req.ModelAuthority,
			CoordinateCRS: coordinateCRS, UnmappedAttrPolicy: policy, IsDefault: isDefault,
		}
		if _, err := tx.NewInsert().Model(p).ExcludeColumn("id").Exec(ctx); err != nil {
			return fmt.Errorf("insert CIM profile: %w", err)
		}

		resp = cimProfileResponse{
			Code: p.Code, Name: p.Name, CimNamespace: p.CimNamespace,
			ProfileURI: p.ProfileURI, ModelAuthority: p.ModelAuthority,
			CoordinateCRS: p.CoordinateCRS, UnmappedAttrPolicy: p.UnmappedAttrPolicy,
			IsDefault: p.IsDefault,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// updateCimProfile edits an existing profile in place -- keyed by code, only
// the fields present in the request. Every column is NOT NULL, so there's no
// clear-to-null path (see updateCimProfileRequest's own doc comment).
func updateCimProfile(ctx context.Context, db *bun.DB, code string, req updateCimProfileRequest) (*cimProfileResponse, error) {
	if err := validateUpdateCimProfile(req); err != nil {
		return nil, err
	}

	var resp cimProfileResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		p := new(metadata.CimProfile)
		if err := tx.NewSelect().Model(p).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("CIM profile %q does not exist", code)
			}
			return fmt.Errorf("loading CIM profile %q: %w", code, err)
		}

		cols := make([]string, 0, 7)
		if req.Name != nil {
			p.Name = *req.Name
			cols = append(cols, "name")
		}
		if req.CimNamespace != nil {
			p.CimNamespace = *req.CimNamespace
			cols = append(cols, "cim_namespace")
		}
		if req.ProfileURI != nil {
			p.ProfileURI = *req.ProfileURI
			cols = append(cols, "profile_uri")
		}
		if req.ModelAuthority != nil {
			p.ModelAuthority = *req.ModelAuthority
			cols = append(cols, "model_authority")
		}
		if req.CoordinateCRS != nil {
			p.CoordinateCRS = *req.CoordinateCRS
			cols = append(cols, "coordinate_crs")
		}
		if req.UnmappedAttrPolicy != nil {
			p.UnmappedAttrPolicy = *req.UnmappedAttrPolicy
			cols = append(cols, "unmapped_attr_policy")
		}
		if req.IsDefault != nil {
			p.IsDefault = *req.IsDefault
			cols = append(cols, "is_default")
		}

		if _, err := tx.NewUpdate().Model(p).Column(cols...).Where("code = ?", code).Exec(ctx); err != nil {
			return fmt.Errorf("updating CIM profile %q: %w", code, err)
		}

		resp = cimProfileResponse{
			Code: p.Code, Name: p.Name, CimNamespace: p.CimNamespace,
			ProfileURI: p.ProfileURI, ModelAuthority: p.ModelAuthority,
			CoordinateCRS: p.CoordinateCRS, UnmappedAttrPolicy: p.UnmappedAttrPolicy,
			IsDefault: p.IsDefault,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// deleteCimProfile removes a profile by code -- no referential guard, since
// class/attribute mappings are global (not profile-scoped) and nothing else
// references a profile row.
func deleteCimProfile(ctx context.Context, db *bun.DB, code string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewDelete().Model((*metadata.CimProfile)(nil)).Where("code = ?", code).Exec(ctx)
		if err != nil {
			return fmt.Errorf("deleting CIM profile %q: %w", code, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return notFound("CIM profile %q does not exist", code)
		}
		return nil
	})
}

// ---- CIM class mappings ---------------------------------------------------

// listCimClassMappings returns every class mapping, code-shaped with its id --
// ordered by feature_class_code then subtype_code NULLS FIRST so a class's
// default row (subtype NULL) sorts above its subtype overrides. A LEFT JOIN
// keeps the default rows. Deliberately not metadata.LoadCIMMapping (which
// drops the id the editor needs).
func listCimClassMappings(ctx context.Context, db *bun.DB) ([]cimClassMappingResponse, error) {
	var rows []struct {
		ID               string `bun:"id"`
		FeatureClassCode string `bun:"fc_code"`
		SubtypeCode      *int   `bun:"subtype_code"`
		CimClass         string `bun:"cim_class"`
		CimKind          string `bun:"cim_kind"`
	}
	if err := db.NewSelect().
		TableExpr("metadata.cim_class_mappings AS cm").
		ColumnExpr("cm.id AS id").
		ColumnExpr("fc.code AS fc_code").
		ColumnExpr("st.code AS subtype_code").
		ColumnExpr("cm.cim_class AS cim_class").
		ColumnExpr("cm.cim_kind AS cim_kind").
		Join("JOIN metadata.feature_classes AS fc ON fc.id = cm.feature_class_id").
		Join("LEFT JOIN metadata.subtypes AS st ON st.id = cm.subtype_id").
		OrderExpr("fc.code").
		OrderExpr("st.code NULLS FIRST").
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make([]cimClassMappingResponse, len(rows))
	for i, r := range rows {
		out[i] = cimClassMappingResponse{
			ID: r.ID, FeatureClassCode: r.FeatureClassCode, SubtypeCode: r.SubtypeCode,
			CimClass: r.CimClass, CimKind: r.CimKind,
		}
	}
	return out, nil
}

// createCimClassMapping resolves the code-shaped targeting to real ids, guards
// the (feature_class_id, subtype_id) uniqueness with a friendly 400 (rather
// than letting the UNIQUE constraint bubble up as a raw 500), and inserts.
func createCimClassMapping(ctx context.Context, db *bun.DB, req createCimClassMappingRequest) (*cimClassMappingResponse, error) {
	if err := validateCreateCimClassMapping(req); err != nil {
		return nil, err
	}

	var resp cimClassMappingResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		fcID, err := resolveFeatureClassCode(ctx, tx, req.FeatureClassCode)
		if err != nil {
			return err
		}
		var subtypeID *string
		if req.SubtypeCode != nil {
			id, err := resolveSubtypeCode(ctx, tx, fcID, req.FeatureClassCode, *req.SubtypeCode)
			if err != nil {
				return err
			}
			subtypeID = &id
		}

		dup := tx.NewSelect().Model((*cimClassMappingModel)(nil)).Where("feature_class_id = ?", fcID)
		if subtypeID == nil {
			dup = dup.Where("subtype_id IS NULL")
		} else {
			dup = dup.Where("subtype_id = ?", *subtypeID)
		}
		exists, err := dup.Exists(ctx)
		if err != nil {
			return fmt.Errorf("checking CIM class mapping for %q: %w", req.FeatureClassCode, err)
		}
		if exists {
			if subtypeID == nil {
				return invalid("a CIM class mapping for feature class %q (class default) already exists", req.FeatureClassCode)
			}
			return invalid("a CIM class mapping for feature class %q subtype %d already exists", req.FeatureClassCode, *req.SubtypeCode)
		}

		m := &cimClassMappingModel{FeatureClassID: fcID, SubtypeID: subtypeID, CimClass: req.CimClass, CimKind: req.CimKind}
		if _, err := tx.NewInsert().Model(m).ExcludeColumn("id").Returning("id").Exec(ctx); err != nil {
			return fmt.Errorf("insert CIM class mapping: %w", err)
		}

		resp = cimClassMappingResponse{
			ID: m.ID, FeatureClassCode: req.FeatureClassCode, SubtypeCode: req.SubtypeCode,
			CimClass: req.CimClass, CimKind: req.CimKind,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// updateCimClassMapping edits a class mapping's cim_class/cim_kind by id -- the
// identity keys (feature_class_id, subtype_id) aren't re-keyed here (see
// updateCimClassMappingRequest's own doc comment). The code-shaped response
// resolves the stored ids back to codes.
func updateCimClassMapping(ctx context.Context, db *bun.DB, id string, req updateCimClassMappingRequest) (*cimClassMappingResponse, error) {
	if err := validateUpdateCimClassMapping(req); err != nil {
		return nil, err
	}

	var resp cimClassMappingResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		m := new(cimClassMappingModel)
		if err := tx.NewSelect().Model(m).Where("id = ?", id).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("CIM class mapping %q does not exist", id)
			}
			return fmt.Errorf("loading CIM class mapping %q: %w", id, err)
		}

		cols := make([]string, 0, 2)
		if req.CimClass != nil {
			m.CimClass = *req.CimClass
			cols = append(cols, "cim_class")
		}
		if req.CimKind != nil {
			m.CimKind = *req.CimKind
			cols = append(cols, "cim_kind")
		}

		if _, err := tx.NewUpdate().Model(m).Column(cols...).Where("id = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("updating CIM class mapping %q: %w", id, err)
		}

		fc := new(metadata.FeatureClass)
		if err := tx.NewSelect().Model(fc).Where("id = ?", m.FeatureClassID).Scan(ctx); err != nil {
			return fmt.Errorf("loading feature class for response: %w", err)
		}
		resp = cimClassMappingResponse{ID: m.ID, FeatureClassCode: fc.Code, CimClass: m.CimClass, CimKind: m.CimKind}
		if m.SubtypeID != nil {
			st := new(metadata.Subtype)
			if err := tx.NewSelect().Model(st).Where("id = ?", *m.SubtypeID).Scan(ctx); err != nil {
				return fmt.Errorf("loading subtype for response: %w", err)
			}
			resp.SubtypeCode = &st.Code
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// deleteCimClassMapping removes a class mapping by id -- no dependents, so no
// referential guard, just a notFound when the id doesn't exist.
func deleteCimClassMapping(ctx context.Context, db *bun.DB, id string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewDelete().Model((*cimClassMappingModel)(nil)).Where("id = ?", id).Exec(ctx)
		if err != nil {
			return fmt.Errorf("deleting CIM class mapping %q: %w", id, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return notFound("CIM class mapping %q does not exist", id)
		}
		return nil
	})
}

// ---- CIM attribute mappings -----------------------------------------------

// listCimAttributeMappings returns every attribute mapping, code-shaped with
// its id, ordered by feature_class_code, field_name, cim_attribute. value_map
// is parsed from its stored jsonb (or left nil when NULL). Deliberately not
// metadata.LoadCIMMapping (which drops the id and the unit column).
func listCimAttributeMappings(ctx context.Context, db *bun.DB) ([]cimAttributeMappingResponse, error) {
	var rows []struct {
		ID               string          `bun:"id"`
		FeatureClassCode string          `bun:"fc_code"`
		FieldName        string          `bun:"field_name"`
		CimAttribute     string          `bun:"cim_attribute"`
		Transform        string          `bun:"transform"`
		ScaleFactor      *float64        `bun:"scale_factor"`
		Unit             *string         `bun:"unit"`
		ValueMap         json.RawMessage `bun:"value_map"`
	}
	if err := db.NewSelect().
		TableExpr("metadata.cim_attribute_mappings AS am").
		ColumnExpr("am.id AS id").
		ColumnExpr("fc.code AS fc_code").
		ColumnExpr("f.name AS field_name").
		ColumnExpr("am.cim_attribute AS cim_attribute").
		ColumnExpr("am.transform AS transform").
		ColumnExpr("am.scale_factor AS scale_factor").
		ColumnExpr("am.unit AS unit").
		ColumnExpr("am.value_map AS value_map").
		Join("JOIN metadata.fields AS f ON f.id = am.field_id").
		Join("JOIN metadata.feature_classes AS fc ON fc.id = f.feature_class_id").
		OrderExpr("fc.code").
		OrderExpr("f.name").
		OrderExpr("am.cim_attribute").
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make([]cimAttributeMappingResponse, len(rows))
	for i, r := range rows {
		resp := cimAttributeMappingResponse{
			ID: r.ID, FeatureClassCode: r.FeatureClassCode, FieldName: r.FieldName,
			CimAttribute: r.CimAttribute, Transform: r.Transform,
			ScaleFactor: r.ScaleFactor, Unit: r.Unit,
		}
		if len(r.ValueMap) > 0 {
			if err := json.Unmarshal(r.ValueMap, &resp.ValueMap); err != nil {
				return nil, fmt.Errorf("cim_attribute_mappings.value_map for %s.%s is not valid JSON: %w", r.FeatureClassCode, r.FieldName, err)
			}
		}
		out[i] = resp
	}
	return out, nil
}

// createCimAttributeMapping resolves (feature_class_code, field_name) to a
// field id, guards the (field_id, cim_attribute) uniqueness with a friendly
// 400, marshals value_map to jsonb, and inserts.
func createCimAttributeMapping(ctx context.Context, db *bun.DB, req createCimAttributeMappingRequest) (*cimAttributeMappingResponse, error) {
	if err := validateCreateCimAttributeMapping(req); err != nil {
		return nil, err
	}

	var resp cimAttributeMappingResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		fieldID, err := resolveFieldRef(ctx, tx, req.FeatureClassCode, req.FieldName)
		if err != nil {
			return err
		}

		exists, err := tx.NewSelect().Model((*cimAttributeMappingModel)(nil)).
			Where("field_id = ? AND cim_attribute = ?", fieldID, req.CimAttribute).Exists(ctx)
		if err != nil {
			return fmt.Errorf("checking CIM attribute mapping for %q.%q: %w", req.FeatureClassCode, req.FieldName, err)
		}
		if exists {
			return invalid("a CIM attribute mapping of field %q.%q to %q already exists", req.FeatureClassCode, req.FieldName, req.CimAttribute)
		}

		var raw json.RawMessage
		if req.ValueMap != nil {
			b, err := json.Marshal(req.ValueMap)
			if err != nil {
				return fmt.Errorf("marshal value_map: %w", err)
			}
			raw = b
		}

		m := &cimAttributeMappingModel{
			FieldID: fieldID, CimAttribute: req.CimAttribute, Transform: req.Transform,
			ScaleFactor: req.ScaleFactor, Unit: req.Unit, ValueMap: raw,
		}
		if _, err := tx.NewInsert().Model(m).ExcludeColumn("id").Returning("id").Exec(ctx); err != nil {
			return fmt.Errorf("insert CIM attribute mapping: %w", err)
		}

		resp = cimAttributeMappingResponse{
			ID: m.ID, FeatureClassCode: req.FeatureClassCode, FieldName: req.FieldName,
			CimAttribute: req.CimAttribute, Transform: req.Transform,
			ScaleFactor: req.ScaleFactor, Unit: req.Unit, ValueMap: req.ValueMap,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// updateCimAttributeMapping edits an attribute mapping's coercion by id. The
// nullable columns use the sentinels JSON can express (see
// updateCimAttributeMappingRequest's own doc comment): Unit "" clears to NULL,
// ValueMap JSON null/empty clears to NULL, ScaleFactor is set-only. The
// code-shaped response resolves the stored field id back to codes.
func updateCimAttributeMapping(ctx context.Context, db *bun.DB, id string, req updateCimAttributeMappingRequest) (*cimAttributeMappingResponse, error) {
	if err := validateUpdateCimAttributeMapping(req); err != nil {
		return nil, err
	}

	var resp cimAttributeMappingResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		m := new(cimAttributeMappingModel)
		if err := tx.NewSelect().Model(m).Where("id = ?", id).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("CIM attribute mapping %q does not exist", id)
			}
			return fmt.Errorf("loading CIM attribute mapping %q: %w", id, err)
		}

		cols := make([]string, 0, 5)
		if req.CimAttribute != nil {
			m.CimAttribute = *req.CimAttribute
			cols = append(cols, "cim_attribute")
		}
		if req.Transform != nil {
			m.Transform = *req.Transform
			cols = append(cols, "transform")
		}
		if req.ScaleFactor != nil {
			m.ScaleFactor = req.ScaleFactor
			cols = append(cols, "scale_factor")
		}
		if req.Unit != nil {
			if *req.Unit == "" {
				m.Unit = nil
			} else {
				u := *req.Unit
				m.Unit = &u
			}
			cols = append(cols, "unit")
		}
		if req.ValueMap != nil {
			raw := *req.ValueMap
			if len(raw) == 0 || string(raw) == "null" {
				m.ValueMap = nil
			} else {
				m.ValueMap = raw
			}
			cols = append(cols, "value_map")
		}

		if _, err := tx.NewUpdate().Model(m).Column(cols...).Where("id = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("updating CIM attribute mapping %q: %w", id, err)
		}

		f := new(metadata.Field)
		if err := tx.NewSelect().Model(f).Where("id = ?", m.FieldID).Scan(ctx); err != nil {
			return fmt.Errorf("loading field for response: %w", err)
		}
		fc := new(metadata.FeatureClass)
		if err := tx.NewSelect().Model(fc).Where("id = ?", f.FeatureClassID).Scan(ctx); err != nil {
			return fmt.Errorf("loading feature class for response: %w", err)
		}
		resp = cimAttributeMappingResponse{
			ID: m.ID, FeatureClassCode: fc.Code, FieldName: f.Name,
			CimAttribute: m.CimAttribute, Transform: m.Transform,
			ScaleFactor: m.ScaleFactor, Unit: m.Unit,
		}
		if len(m.ValueMap) > 0 {
			if err := json.Unmarshal(m.ValueMap, &resp.ValueMap); err != nil {
				return fmt.Errorf("value_map is not valid JSON: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// deleteCimAttributeMapping removes an attribute mapping by id -- no
// dependents, so no referential guard, just a notFound when the id is unknown.
func deleteCimAttributeMapping(ctx context.Context, db *bun.DB, id string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewDelete().Model((*cimAttributeMappingModel)(nil)).Where("id = ?", id).Exec(ctx)
		if err != nil {
			return fmt.Errorf("deleting CIM attribute mapping %q: %w", id, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return notFound("CIM attribute mapping %q does not exist", id)
		}
		return nil
	})
}

// ---- coverage (Phase 55 Slice 2 warn-on-unmapped) -------------------------

// listCimCoverage returns every feature class with NO cim_class_mappings row of
// any kind -- the classes silently dropped from CIM export today (ClassFor
// returns ok=false, cim.go skips the feature). A NOT EXISTS anti-join against
// cim_class_mappings; a class with even one row (a class default, a subtype
// override, or an explicit cim_kind='skip') is considered decided and omitted.
// Ordered by code for a stable list. This is read-only and additive -- the
// interactive counterpart to runExport's warn-on-drop log line.
func listCimCoverage(ctx context.Context, db *bun.DB) ([]cimCoverageResponse, error) {
	var rows []cimCoverageResponse
	if err := db.NewSelect().
		TableExpr("metadata.feature_classes AS fc").
		ColumnExpr("fc.code AS code").
		ColumnExpr("fc.name AS name").
		ColumnExpr("fc.geometry_type AS geometry_type").
		ColumnExpr("fc.network_role AS network_role").
		Where("NOT EXISTS (SELECT 1 FROM metadata.cim_class_mappings cm WHERE cm.feature_class_id = fc.id)").
		OrderExpr("fc.code").
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// ---- code -> id resolvers -------------------------------------------------

// resolveFeatureClassCode looks up one feature_class_code against real,
// already-existing metadata.feature_classes rows -- an unresolvable code is a
// 400 naming the code, not a foreign-key violation surfacing from the insert
// (the same discipline resolveDomainCodes/resolveMaterialSpecCode apply).
func resolveFeatureClassCode(ctx context.Context, tx bun.Tx, code string) (string, error) {
	var fc metadata.FeatureClass
	if err := tx.NewSelect().Model(&fc).Where("code = ?", code).Scan(ctx); err != nil {
		if err == sql.ErrNoRows {
			return "", invalid("feature_class_code %q does not exist", code)
		}
		return "", fmt.Errorf("resolving feature class %q: %w", code, err)
	}
	return fc.ID, nil
}

// resolveSubtypeCode looks up one integer subtype code scoped to a feature
// class -- an unresolvable code is a 400 naming the class it isn't on.
func resolveSubtypeCode(ctx context.Context, tx bun.Tx, featureClassID, featureClassCode string, code int) (string, error) {
	var st metadata.Subtype
	if err := tx.NewSelect().Model(&st).Where("feature_class_id = ? AND code = ?", featureClassID, code).Scan(ctx); err != nil {
		if err == sql.ErrNoRows {
			return "", invalid("subtype_code %d is not defined on feature class %q", code, featureClassCode)
		}
		return "", fmt.Errorf("resolving subtype_code %d: %w", code, err)
	}
	return st.ID, nil
}

// resolveFieldRef looks up a field by (feature_class_code, field_name),
// resolving the class first so an unknown class and an unknown field each get
// their own clear 400.
func resolveFieldRef(ctx context.Context, tx bun.Tx, featureClassCode, fieldName string) (string, error) {
	fcID, err := resolveFeatureClassCode(ctx, tx, featureClassCode)
	if err != nil {
		return "", err
	}
	var f metadata.Field
	if err := tx.NewSelect().Model(&f).Where("feature_class_id = ? AND name = ?", fcID, fieldName).Scan(ctx); err != nil {
		if err == sql.ErrNoRows {
			return "", invalid("field %q is not a field on feature class %q", fieldName, featureClassCode)
		}
		return "", fmt.Errorf("resolving field %q: %w", fieldName, err)
	}
	return f.ID, nil
}
