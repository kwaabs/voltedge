package admin

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/importer"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// This file is the write path for the two metadata.import_* tables that drive
// the admin ETL / import engine (docs/etl-import-engine.md, migration 0102):
// import_profiles (a reusable, named import for a target class -- write policy,
// source format, geometry assembly) and import_field_mappings (one source
// column -> one target field, profile-scoped). It is the deliberate mirror of
// the CIM export mapping editor (cim.go): there feature fields are mapped OUT
// to a fixed external vocabulary; here external source columns are mapped IN to
// our fields.
//
// The bun row models (importer.Profile / importer.FieldMapping) live in the
// importer package so the later load worker shares one row-shape definition;
// this file owns the code-shaped wire DTOs and resolves between the two, reusing
// cim.go's resolvers (resolveFeatureClassCode / resolveSubtypeCode) and the
// package-wide invalid/notFound/codeExists helpers.
//
// A profile is keyed by code like a domain (and like a CIM profile). Its field
// mappings have no natural key, so they're edited and deleted by their uuid id,
// but -- unlike CIM's global mappings -- they're profile-scoped (created under a
// profile's code, cascade-deleted with it).

// The CHECK-constraint value sets from migration 0102, mirrored here so a bad
// value gets a friendly 400 from this package's own validation rather than a
// raw Postgres constraint-violation error surfacing from the insert. The
// source_format set is pre-widened for the v2 live-DB connectors, matching the
// column CHECK.
var (
	validImportSourceFormats = map[string]bool{
		"csv": true, "geojson": true, "shapefile": true, "kml": true, "dxf": true,
		"postgis": true, "mssql": true,
	}
	validImportWriteModes    = map[string]bool{"insert_only": true, "upsert": true}
	validImportGeometryModes = map[string]bool{"none": true, "native": true, "lonlat": true, "wkt": true}
	validImportStatuses      = map[string]bool{"active": true, "deprecated": true}
	validImportTransforms    = map[string]bool{
		"direct": true, "constant": true, "scale": true, "domain_map": true,
	}
)

// ---- import profiles ------------------------------------------------------

// importProfileResponse is one metadata.import_profiles row, code-shaped over
// the wire (feature_class_code + integer subtype_code, resolved from the stored
// ids) and keyed by its own code. MatchFields is always present (an empty array
// for insert_only) so the editor can bind it without a null check.
type importProfileResponse struct {
	Code              string   `json:"code"`
	Name              string   `json:"name"`
	FeatureClassCode  string   `json:"feature_class_code"`
	SubtypeCode       *int     `json:"subtype_code,omitempty"`
	SourceFormat      string   `json:"source_format"`
	WriteMode         string   `json:"write_mode"`
	MatchFields       []string `json:"match_fields"`
	GeometryMode      string   `json:"geometry_mode"`
	GeometryLonColumn *string  `json:"geometry_lon_column,omitempty"`
	GeometryLatColumn *string  `json:"geometry_lat_column,omitempty"`
	GeometryWktColumn *string  `json:"geometry_wkt_column,omitempty"`
	Status            string   `json:"status"`
}

// createImportProfileRequest is POST /admin/import-profiles' body. WriteMode,
// SourceFormat, GeometryMode, and Status are optional pointers only so an
// omitted one falls back to the column's DB default (insert_only / csv / native
// / active) rather than an empty string -- not clear-to-null sentinels, since
// none of those columns is nullable. A nil SubtypeCode leaves the profile
// unpinned (subtype_id NULL); the geometry columns are source column names, free
// text validated only for presence-per-mode.
type createImportProfileRequest struct {
	Code              string   `json:"code"`
	Name              string   `json:"name"`
	FeatureClassCode  string   `json:"feature_class_code"`
	SubtypeCode       *int     `json:"subtype_code"`
	SourceFormat      *string  `json:"source_format"`
	WriteMode         *string  `json:"write_mode"`
	MatchFields       []string `json:"match_fields"`
	GeometryMode      *string  `json:"geometry_mode"`
	GeometryLonColumn *string  `json:"geometry_lon_column"`
	GeometryLatColumn *string  `json:"geometry_lat_column"`
	GeometryWktColumn *string  `json:"geometry_wkt_column"`
	Status            *string  `json:"status"`
}

// updateImportProfileRequest is PATCH /admin/import-profiles/{code}'s body --
// every field optional, only what's present changes. FeatureClassCode and
// SubtypeCode are deliberately absent: re-targeting the class would orphan the
// field mappings (which reference that class's fields), so re-target by deleting
// and recreating -- the same reasoning cim.go gives for a class mapping's
// identity keys. The geometry source columns use the nil/""/value three-state
// (nil = leave, "" = clear to NULL, else set); MatchFields is a *[]string so a
// non-nil empty slice can clear the key back to '{}'.
type updateImportProfileRequest struct {
	Name              *string   `json:"name"`
	SourceFormat      *string   `json:"source_format"`
	WriteMode         *string   `json:"write_mode"`
	MatchFields       *[]string `json:"match_fields"`
	GeometryMode      *string   `json:"geometry_mode"`
	GeometryLonColumn *string   `json:"geometry_lon_column"`
	GeometryLatColumn *string   `json:"geometry_lat_column"`
	GeometryWktColumn *string   `json:"geometry_wkt_column"`
	Status            *string   `json:"status"`
}

// listImportProfiles returns every profile, code-shaped, code-ordered.
func listImportProfiles(ctx context.Context, db *bun.DB) ([]importProfileResponse, error) {
	return selectImportProfiles(ctx, db, "")
}

// selectImportProfiles is the shared code-shaped read: every profile, or the one
// with the given code when non-empty. A LEFT JOIN to subtypes keeps profiles
// with no pinned subtype. Ordered by code for a stable list.
func selectImportProfiles(ctx context.Context, db bun.IDB, code string) ([]importProfileResponse, error) {
	var rows []struct {
		Code              string   `bun:"code"`
		Name              string   `bun:"name"`
		FeatureClassCode  string   `bun:"fc_code"`
		SubtypeCode       *int     `bun:"subtype_code"`
		SourceFormat      string   `bun:"source_format"`
		WriteMode         string   `bun:"write_mode"`
		MatchFields       []string `bun:"match_fields,array"`
		GeometryMode      string   `bun:"geometry_mode"`
		GeometryLonColumn *string  `bun:"geometry_lon_column"`
		GeometryLatColumn *string  `bun:"geometry_lat_column"`
		GeometryWktColumn *string  `bun:"geometry_wkt_column"`
		Status            string   `bun:"status"`
	}
	q := db.NewSelect().
		TableExpr("metadata.import_profiles AS ip").
		ColumnExpr("ip.code AS code").
		ColumnExpr("ip.name AS name").
		ColumnExpr("fc.code AS fc_code").
		ColumnExpr("st.code AS subtype_code").
		ColumnExpr("ip.source_format AS source_format").
		ColumnExpr("ip.write_mode AS write_mode").
		ColumnExpr("ip.match_fields AS match_fields").
		ColumnExpr("ip.geometry_mode AS geometry_mode").
		ColumnExpr("ip.geometry_lon_column AS geometry_lon_column").
		ColumnExpr("ip.geometry_lat_column AS geometry_lat_column").
		ColumnExpr("ip.geometry_wkt_column AS geometry_wkt_column").
		ColumnExpr("ip.status AS status").
		Join("JOIN metadata.feature_classes AS fc ON fc.id = ip.feature_class_id").
		Join("LEFT JOIN metadata.subtypes AS st ON st.id = ip.subtype_id").
		OrderExpr("ip.code")
	if code != "" {
		q = q.Where("ip.code = ?", code)
	}
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make([]importProfileResponse, len(rows))
	for i, r := range rows {
		mf := r.MatchFields
		if mf == nil {
			mf = []string{}
		}
		out[i] = importProfileResponse{
			Code: r.Code, Name: r.Name, FeatureClassCode: r.FeatureClassCode,
			SubtypeCode: r.SubtypeCode, SourceFormat: r.SourceFormat, WriteMode: r.WriteMode,
			MatchFields: mf, GeometryMode: r.GeometryMode,
			GeometryLonColumn: r.GeometryLonColumn, GeometryLatColumn: r.GeometryLatColumn,
			GeometryWktColumn: r.GeometryWktColumn, Status: r.Status,
		}
	}
	return out, nil
}

// createImportProfile inserts one import profile -- existence-checked on the
// code first (friendly 400 over the raw unique violation), the class/subtype
// targeting resolved to real ids, and the combined write/geometry state
// validated against the same shape rules the migration's CHECKs enforce.
func createImportProfile(ctx context.Context, db *bun.DB, req createImportProfileRequest) (*importProfileResponse, error) {
	if err := validateCreateImportProfile(req); err != nil {
		return nil, err
	}

	sourceFormat := valueOr(req.SourceFormat, "csv")
	writeMode := valueOr(req.WriteMode, "insert_only")
	geometryMode := valueOr(req.GeometryMode, "native")
	status := valueOr(req.Status, "active")
	matchFields := req.MatchFields
	if matchFields == nil {
		matchFields = []string{}
	}
	lon := blankToNil(req.GeometryLonColumn)
	lat := blankToNil(req.GeometryLatColumn)
	wkt := blankToNil(req.GeometryWktColumn)

	if err := validateImportProfileState(writeMode, geometryMode, matchFields, lon, lat, wkt); err != nil {
		return nil, err
	}

	var resp importProfileResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		exists, err := codeExists(ctx, tx, (*importer.Profile)(nil), req.Code)
		if err != nil {
			return err
		}
		if exists {
			return invalid("an import profile with code %q already exists", req.Code)
		}

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
		if err := ensureMatchFieldsExist(ctx, tx, fcID, req.FeatureClassCode, matchFields); err != nil {
			return err
		}

		actor := actorPtr(ctx)
		p := &importer.Profile{
			Code: req.Code, Name: req.Name, FeatureClassID: fcID, SubtypeID: subtypeID,
			SourceFormat: sourceFormat, WriteMode: writeMode, MatchFields: matchFields,
			GeometryMode: geometryMode, GeometryLonColumn: lon, GeometryLatColumn: lat,
			GeometryWktColumn: wkt, Status: status, CreatedBy: actor, UpdatedBy: actor,
		}
		if _, err := tx.NewInsert().Model(p).
			ExcludeColumn("id", "created_at", "updated_at").Exec(ctx); err != nil {
			return fmt.Errorf("insert import profile: %w", err)
		}

		out, err := selectImportProfiles(ctx, tx, req.Code)
		if err != nil {
			return err
		}
		resp = out[0]
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// updateImportProfile edits a profile in place, keyed by code -- only the fields
// present in the request. The final combined write/geometry state (after the
// patch) is re-validated so a partial update can't leave, say, upsert with no
// match_fields or lonlat with only one column.
func updateImportProfile(ctx context.Context, db *bun.DB, code string, req updateImportProfileRequest) (*importProfileResponse, error) {
	if err := validateUpdateImportProfile(req); err != nil {
		return nil, err
	}

	var resp importProfileResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		p := new(importer.Profile)
		if err := tx.NewSelect().Model(p).Where("code = ?", code).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("import profile %q does not exist", code)
			}
			return fmt.Errorf("loading import profile %q: %w", code, err)
		}

		cols := make([]string, 0, 10)
		if req.Name != nil {
			p.Name = *req.Name
			cols = append(cols, "name")
		}
		if req.SourceFormat != nil {
			p.SourceFormat = *req.SourceFormat
			cols = append(cols, "source_format")
		}
		if req.WriteMode != nil {
			p.WriteMode = *req.WriteMode
			cols = append(cols, "write_mode")
		}
		if req.MatchFields != nil {
			mf := *req.MatchFields
			if mf == nil {
				mf = []string{}
			}
			p.MatchFields = mf
			cols = append(cols, "match_fields")
		}
		if req.GeometryMode != nil {
			p.GeometryMode = *req.GeometryMode
			cols = append(cols, "geometry_mode")
		}
		if req.GeometryLonColumn != nil {
			p.GeometryLonColumn = blankToNil(req.GeometryLonColumn)
			cols = append(cols, "geometry_lon_column")
		}
		if req.GeometryLatColumn != nil {
			p.GeometryLatColumn = blankToNil(req.GeometryLatColumn)
			cols = append(cols, "geometry_lat_column")
		}
		if req.GeometryWktColumn != nil {
			p.GeometryWktColumn = blankToNil(req.GeometryWktColumn)
			cols = append(cols, "geometry_wkt_column")
		}
		if req.Status != nil {
			p.Status = *req.Status
			cols = append(cols, "status")
		}

		if err := validateImportProfileState(p.WriteMode, p.GeometryMode, p.MatchFields,
			p.GeometryLonColumn, p.GeometryLatColumn, p.GeometryWktColumn); err != nil {
			return err
		}
		if req.MatchFields != nil {
			if err := ensureMatchFieldsExist(ctx, tx, p.FeatureClassID, code, p.MatchFields); err != nil {
				return err
			}
		}

		p.UpdatedBy = actorPtr(ctx)
		p.UpdatedAt = time.Now()
		cols = append(cols, "updated_by", "updated_at")
		if _, err := tx.NewUpdate().Model(p).
			Column(cols...).Where("code = ?", code).Exec(ctx); err != nil {
			return fmt.Errorf("updating import profile %q: %w", code, err)
		}

		out, err := selectImportProfiles(ctx, tx, code)
		if err != nil {
			return err
		}
		resp = out[0]
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// deleteImportProfile removes a profile by code -- its field mappings cascade
// (import_field_mappings.profile_id ON DELETE CASCADE), and a job's profile_id
// is ON DELETE SET NULL, so past runs keep their audit record.
func deleteImportProfile(ctx context.Context, db *bun.DB, code string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewDelete().Model((*importer.Profile)(nil)).Where("code = ?", code).Exec(ctx)
		if err != nil {
			return fmt.Errorf("deleting import profile %q: %w", code, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return notFound("import profile %q does not exist", code)
		}
		return nil
	})
}

// ---- import field mappings ------------------------------------------------

// importFieldMappingResponse is one metadata.import_field_mappings row --
// code-shaped (profile_code + target field_name) with the row id, plus the
// coercion. value_map is returned parsed (or omitted when NULL).
type importFieldMappingResponse struct {
	ID            string         `json:"id"`
	ProfileCode   string         `json:"profile_code"`
	FieldName     string         `json:"field_name"`
	SourceColumn  *string        `json:"source_column,omitempty"`
	Transform     string         `json:"transform"`
	ConstantValue *string        `json:"constant_value,omitempty"`
	ScaleFactor   *float64       `json:"scale_factor,omitempty"`
	ValueMap      map[string]any `json:"value_map,omitempty"`
}

// createImportFieldMappingRequest is POST
// /admin/import-profiles/{code}/field-mappings' body. FieldName resolves against
// the profile's own feature class. SourceColumn is required for every transform
// except constant; the per-transform parameter (constant_value / scale_factor /
// value_map) is required for its transform, matching the migration's CHECKs.
type createImportFieldMappingRequest struct {
	FieldName     string         `json:"field_name"`
	SourceColumn  *string        `json:"source_column"`
	Transform     string         `json:"transform"`
	ConstantValue *string        `json:"constant_value"`
	ScaleFactor   *float64       `json:"scale_factor"`
	ValueMap      map[string]any `json:"value_map"`
}

// updateImportFieldMappingRequest is PATCH /admin/import-field-mappings/{id}'s
// body -- every field optional. ValueMap (*json.RawMessage) mirrors
// updateCimAttributeMappingRequest's (nil = leave, JSON null/empty clears to
// NULL, a JSON object sets it); SourceColumn/ConstantValue use nil/""/value
// (nil = leave, "" = clear to NULL, else set); ScaleFactor is set-only.
type updateImportFieldMappingRequest struct {
	SourceColumn  *string          `json:"source_column"`
	Transform     *string          `json:"transform"`
	ConstantValue *string          `json:"constant_value"`
	ScaleFactor   *float64         `json:"scale_factor"`
	ValueMap      *json.RawMessage `json:"value_map"`
}

// listImportFieldMappings returns every field mapping of one profile (by code),
// code-shaped with its id, ordered by target field name.
func listImportFieldMappings(ctx context.Context, db *bun.DB, profileCode string) ([]importFieldMappingResponse, error) {
	var out []importFieldMappingResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		profileID, err := resolveImportProfileCode(ctx, tx, profileCode)
		if err != nil {
			return err
		}
		var rows []struct {
			ID            string          `bun:"id"`
			FieldName     string          `bun:"field_name"`
			SourceColumn  *string         `bun:"source_column"`
			Transform     string          `bun:"transform"`
			ConstantValue *string         `bun:"constant_value"`
			ScaleFactor   *float64        `bun:"scale_factor"`
			ValueMap      json.RawMessage `bun:"value_map"`
		}
		if err := tx.NewSelect().
			TableExpr("metadata.import_field_mappings AS fm").
			ColumnExpr("fm.id AS id").
			ColumnExpr("f.name AS field_name").
			ColumnExpr("fm.source_column AS source_column").
			ColumnExpr("fm.transform AS transform").
			ColumnExpr("fm.constant_value AS constant_value").
			ColumnExpr("fm.scale_factor AS scale_factor").
			ColumnExpr("fm.value_map AS value_map").
			Join("JOIN metadata.fields AS f ON f.id = fm.field_id").
			Where("fm.profile_id = ?", profileID).
			OrderExpr("f.name").
			Scan(ctx, &rows); err != nil {
			return err
		}
		out = make([]importFieldMappingResponse, len(rows))
		for i, r := range rows {
			resp := importFieldMappingResponse{
				ID: r.ID, ProfileCode: profileCode, FieldName: r.FieldName,
				SourceColumn: r.SourceColumn, Transform: r.Transform,
				ConstantValue: r.ConstantValue, ScaleFactor: r.ScaleFactor,
			}
			if len(r.ValueMap) > 0 {
				if err := json.Unmarshal(r.ValueMap, &resp.ValueMap); err != nil {
					return fmt.Errorf("import_field_mappings.value_map for %s.%s is not valid JSON: %w", profileCode, r.FieldName, err)
				}
			}
			out[i] = resp
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// createImportFieldMapping resolves the profile (by code) and the target field
// (by name, against the profile's class), guards the (profile, field) uniqueness
// with a friendly 400, validates the transform's required parameter, marshals
// value_map to jsonb, and inserts.
func createImportFieldMapping(ctx context.Context, db *bun.DB, profileCode string, req createImportFieldMappingRequest) (*importFieldMappingResponse, error) {
	if err := validateCreateImportFieldMapping(req); err != nil {
		return nil, err
	}

	sourceColumn := blankToNil(req.SourceColumn)
	constantValue := blankToNil(req.ConstantValue)
	if err := validateImportFieldMappingState(req.Transform, sourceColumn, constantValue, req.ScaleFactor, req.ValueMap != nil); err != nil {
		return nil, err
	}

	var resp importFieldMappingResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		p := new(importer.Profile)
		if err := tx.NewSelect().Model(p).Where("code = ?", profileCode).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("import profile %q does not exist", profileCode)
			}
			return fmt.Errorf("loading import profile %q: %w", profileCode, err)
		}
		fieldID, err := resolveFieldOnClass(ctx, tx, p.FeatureClassID, profileCode, req.FieldName)
		if err != nil {
			return err
		}

		exists, err := tx.NewSelect().Model((*importer.FieldMapping)(nil)).
			Where("profile_id = ? AND field_id = ?", p.ID, fieldID).Exists(ctx)
		if err != nil {
			return fmt.Errorf("checking import field mapping for %q.%q: %w", profileCode, req.FieldName, err)
		}
		if exists {
			return invalid("import profile %q already maps a source column to field %q", profileCode, req.FieldName)
		}

		var raw json.RawMessage
		if req.ValueMap != nil {
			b, err := json.Marshal(req.ValueMap)
			if err != nil {
				return fmt.Errorf("marshal value_map: %w", err)
			}
			raw = b
		}

		m := &importer.FieldMapping{
			ProfileID: p.ID, FieldID: fieldID, SourceColumn: sourceColumn,
			Transform: req.Transform, ConstantValue: constantValue,
			ScaleFactor: req.ScaleFactor, ValueMap: raw,
		}
		if _, err := tx.NewInsert().Model(m).
			ExcludeColumn("id", "created_at").Returning("id").Exec(ctx); err != nil {
			return fmt.Errorf("insert import field mapping: %w", err)
		}

		resp = importFieldMappingResponse{
			ID: m.ID, ProfileCode: profileCode, FieldName: req.FieldName,
			SourceColumn: sourceColumn, Transform: req.Transform,
			ConstantValue: constantValue, ScaleFactor: req.ScaleFactor, ValueMap: req.ValueMap,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// updateImportFieldMapping edits a field mapping's coercion by id. The final
// combined state (after the patch) is re-validated so a partial update can't
// leave a transform without its required parameter. The code-shaped response
// resolves the stored ids back to codes.
func updateImportFieldMapping(ctx context.Context, db *bun.DB, id string, req updateImportFieldMappingRequest) (*importFieldMappingResponse, error) {
	if err := validateUpdateImportFieldMapping(req); err != nil {
		return nil, err
	}

	var resp importFieldMappingResponse
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		m := new(importer.FieldMapping)
		if err := tx.NewSelect().Model(m).Where("id = ?", id).Scan(ctx); err != nil {
			if err == sql.ErrNoRows {
				return notFound("import field mapping %q does not exist", id)
			}
			return fmt.Errorf("loading import field mapping %q: %w", id, err)
		}

		cols := make([]string, 0, 5)
		if req.SourceColumn != nil {
			m.SourceColumn = blankToNil(req.SourceColumn)
			cols = append(cols, "source_column")
		}
		if req.Transform != nil {
			m.Transform = *req.Transform
			cols = append(cols, "transform")
		}
		if req.ConstantValue != nil {
			m.ConstantValue = blankToNil(req.ConstantValue)
			cols = append(cols, "constant_value")
		}
		if req.ScaleFactor != nil {
			m.ScaleFactor = req.ScaleFactor
			cols = append(cols, "scale_factor")
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

		if err := validateImportFieldMappingState(m.Transform, m.SourceColumn, m.ConstantValue, m.ScaleFactor, len(m.ValueMap) > 0); err != nil {
			return err
		}

		if _, err := tx.NewUpdate().Model(m).Column(cols...).Where("id = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("updating import field mapping %q: %w", id, err)
		}

		// Resolve the code-shaped response (profile code + field name).
		var meta struct {
			ProfileCode string `bun:"profile_code"`
			FieldName   string `bun:"field_name"`
		}
		if err := tx.NewSelect().
			TableExpr("metadata.import_field_mappings AS fm").
			ColumnExpr("ip.code AS profile_code").
			ColumnExpr("f.name AS field_name").
			Join("JOIN metadata.import_profiles AS ip ON ip.id = fm.profile_id").
			Join("JOIN metadata.fields AS f ON f.id = fm.field_id").
			Where("fm.id = ?", id).Scan(ctx, &meta); err != nil {
			return fmt.Errorf("loading codes for response: %w", err)
		}
		resp = importFieldMappingResponse{
			ID: m.ID, ProfileCode: meta.ProfileCode, FieldName: meta.FieldName,
			SourceColumn: m.SourceColumn, Transform: m.Transform,
			ConstantValue: m.ConstantValue, ScaleFactor: m.ScaleFactor,
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

// deleteImportFieldMapping removes a field mapping by id -- no dependents, just a
// notFound when the id is unknown.
func deleteImportFieldMapping(ctx context.Context, db *bun.DB, id string) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewDelete().Model((*importer.FieldMapping)(nil)).Where("id = ?", id).Exec(ctx)
		if err != nil {
			return fmt.Errorf("deleting import field mapping %q: %w", id, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return notFound("import field mapping %q does not exist", id)
		}
		return nil
	})
}

// ---- validation -----------------------------------------------------------

func validateCreateImportProfile(req createImportProfileRequest) error {
	if err := validateCode("import profile code", req.Code); err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return invalid("import profile name is required")
	}
	if err := validateCode("feature_class_code", req.FeatureClassCode); err != nil {
		return err
	}
	if req.SourceFormat != nil && !validImportSourceFormats[*req.SourceFormat] {
		return invalid("source_format must be one of csv, geojson, shapefile, kml, dxf, postgis, mssql")
	}
	if req.WriteMode != nil && !validImportWriteModes[*req.WriteMode] {
		return invalid("write_mode must be one of insert_only, upsert")
	}
	if req.GeometryMode != nil && !validImportGeometryModes[*req.GeometryMode] {
		return invalid("geometry_mode must be one of none, native, lonlat, wkt")
	}
	if req.Status != nil && !validImportStatuses[*req.Status] {
		return invalid("status must be one of active, deprecated")
	}
	return nil
}

func validateUpdateImportProfile(req updateImportProfileRequest) error {
	if req.Name != nil && strings.TrimSpace(*req.Name) == "" {
		return invalid("name cannot be blank")
	}
	if req.SourceFormat != nil && !validImportSourceFormats[*req.SourceFormat] {
		return invalid("source_format must be one of csv, geojson, shapefile, kml, dxf, postgis, mssql")
	}
	if req.WriteMode != nil && !validImportWriteModes[*req.WriteMode] {
		return invalid("write_mode must be one of insert_only, upsert")
	}
	if req.GeometryMode != nil && !validImportGeometryModes[*req.GeometryMode] {
		return invalid("geometry_mode must be one of none, native, lonlat, wkt")
	}
	if req.Status != nil && !validImportStatuses[*req.Status] {
		return invalid("status must be one of active, deprecated")
	}
	if req.Name == nil && req.SourceFormat == nil && req.WriteMode == nil && req.MatchFields == nil &&
		req.GeometryMode == nil && req.GeometryLonColumn == nil && req.GeometryLatColumn == nil &&
		req.GeometryWktColumn == nil && req.Status == nil {
		return invalid("request must set at least one field")
	}
	return nil
}

// validateImportProfileState mirrors migration 0102's cross-field CHECKs on the
// resolved final state: upsert needs a match key, lonlat needs both coordinate
// columns, wkt needs its column.
func validateImportProfileState(writeMode, geometryMode string, matchFields []string, lon, lat, wkt *string) error {
	if writeMode == "upsert" && len(matchFields) == 0 {
		return invalid("write_mode upsert requires at least one match field")
	}
	switch geometryMode {
	case "lonlat":
		if lon == nil || lat == nil {
			return invalid("geometry_mode lonlat requires both geometry_lon_column and geometry_lat_column")
		}
	case "wkt":
		if wkt == nil {
			return invalid("geometry_mode wkt requires geometry_wkt_column")
		}
	}
	return nil
}

func validateCreateImportFieldMapping(req createImportFieldMappingRequest) error {
	if !fieldNamePattern.MatchString(req.FieldName) {
		return invalid("field_name %q must be lower-case letters, digits, and underscores, starting with a letter", req.FieldName)
	}
	if !validImportTransforms[req.Transform] {
		return invalid("transform must be one of direct, constant, scale, domain_map")
	}
	return nil
}

func validateUpdateImportFieldMapping(req updateImportFieldMappingRequest) error {
	if req.Transform != nil && !validImportTransforms[*req.Transform] {
		return invalid("transform must be one of direct, constant, scale, domain_map")
	}
	if req.SourceColumn == nil && req.Transform == nil && req.ConstantValue == nil &&
		req.ScaleFactor == nil && req.ValueMap == nil {
		return invalid("request must set at least one of source_column, transform, constant_value, scale_factor, value_map")
	}
	return nil
}

// validateImportFieldMappingState mirrors migration 0102's per-transform CHECKs
// on the resolved final state: every transform except constant needs a source
// column; each parameterized transform needs its parameter.
func validateImportFieldMappingState(transform string, sourceColumn, constantValue *string, scaleFactor *float64, hasValueMap bool) error {
	if transform != "constant" && sourceColumn == nil {
		return invalid("transform %q requires a source_column", transform)
	}
	switch transform {
	case "constant":
		if constantValue == nil {
			return invalid("transform constant requires constant_value")
		}
	case "scale":
		if scaleFactor == nil {
			return invalid("transform scale requires scale_factor")
		}
	case "domain_map":
		if !hasValueMap {
			return invalid("transform domain_map requires value_map")
		}
	}
	return nil
}

// ---- resolvers + small helpers --------------------------------------------

// resolveImportProfileCode looks up one profile id by code -- an unknown code is
// a 404 (the profile is a real, addressable resource, unlike a bare foreign-key
// reference).
func resolveImportProfileCode(ctx context.Context, tx bun.Tx, code string) (string, error) {
	p := new(importer.Profile)
	if err := tx.NewSelect().Model(p).Column("id").Where("code = ?", code).Scan(ctx); err != nil {
		if err == sql.ErrNoRows {
			return "", notFound("import profile %q does not exist", code)
		}
		return "", fmt.Errorf("resolving import profile %q: %w", code, err)
	}
	return p.ID, nil
}

// resolveFieldOnClass looks up a field id by name scoped to an already-resolved
// feature class id -- an unknown field is a 400 naming the class it isn't on.
func resolveFieldOnClass(ctx context.Context, tx bun.Tx, featureClassID, featureClassCode, fieldName string) (string, error) {
	var f metadata.Field
	if err := tx.NewSelect().Model(&f).Where("feature_class_id = ? AND name = ?", featureClassID, fieldName).Scan(ctx); err != nil {
		if err == sql.ErrNoRows {
			return "", invalid("field %q is not a field on feature class %q", fieldName, featureClassCode)
		}
		return "", fmt.Errorf("resolving field %q: %w", fieldName, err)
	}
	return f.ID, nil
}

// ensureMatchFieldsExist checks every match field names a real field on the
// target class -- the migration comment defers this cross-table check to the
// handler (it can't be expressed as a column CHECK).
func ensureMatchFieldsExist(ctx context.Context, tx bun.Tx, featureClassID, featureClassCode string, matchFields []string) error {
	for _, name := range matchFields {
		if strings.TrimSpace(name) == "" {
			return invalid("match_fields must not contain a blank field name")
		}
		if _, err := resolveFieldOnClass(ctx, tx, featureClassID, featureClassCode, name); err != nil {
			return err
		}
	}
	return nil
}

// valueOr returns *p when non-nil and non-empty, else the fallback -- for
// optional-with-DB-default request pointers.
func valueOr(p *string, fallback string) string {
	if p != nil && *p != "" {
		return *p
	}
	return fallback
}

// blankToNil collapses a nil or empty-string pointer to nil, so an omitted or
// explicitly-cleared optional column stores SQL NULL rather than an empty string.
func blankToNil(p *string) *string {
	if p == nil || strings.TrimSpace(*p) == "" {
		return nil
	}
	return p
}

// actorPtr returns the acting user's id as a pointer for the audit columns, or
// nil when no actor is on the context (never expected behind the /admin gate,
// but the columns are nullable so this stays safe).
func actorPtr(ctx context.Context) *string {
	id := auth.ActorID(ctx)
	if id == "" {
		return nil
	}
	return &id
}

// ---- handlers -------------------------------------------------------------

func listImportProfilesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profiles, err := listImportProfiles(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"import_profiles": profiles})
	}
}

func createImportProfileHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createImportProfileRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createImportProfile(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func updateImportProfileHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		var req updateImportProfileRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := updateImportProfile(r.Context(), db, code, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func deleteImportProfileHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		if err := deleteImportProfile(r.Context(), db, code); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func listImportFieldMappingsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		mappings, err := listImportFieldMappings(r.Context(), db, code)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"import_field_mappings": mappings})
	}
}

func createImportFieldMappingHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		var req createImportFieldMappingRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createImportFieldMapping(r.Context(), db, code, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func updateImportFieldMappingHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req updateImportFieldMappingRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := updateImportFieldMapping(r.Context(), db, id, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func deleteImportFieldMappingHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := deleteImportFieldMapping(r.Context(), db, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
