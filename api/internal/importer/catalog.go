package importer

// The runnable-profile catalog (S5, docs/etl-import-engine.md). The import
// wizard is a features:write surface, but to pick which saved profile to run it
// needs to LIST the profiles -- and the authoring list (GET /admin/import-
// profiles) is metadata:read-gated, a permission a run-only operator need not
// hold. So this package exposes its own lean, features-gated read of the same
// metadata.import_profiles rows, scoped to the active ones (a deprecated profile
// is kept for its history but never offered for a new run). It is a read of the
// admin's config, not a second copy of it: the shape mirrors the admin editor's
// importProfileResponse minus the audit/status plumbing the wizard has no use
// for.

import (
	"context"

	"github.com/uptrace/bun"
)

// ProfileSummary is one active import profile as the run wizard needs it: the
// code to run, the human name, the target class/subtype it writes, and the
// policy knobs (source_format, write_mode + match_fields, geometry_mode + its
// source columns) the wizard reads to hint the operator which file to pick and
// what a run will do. Code-shaped: the stored feature_class_id/subtype_id are
// resolved to their codes here, exactly as the admin editor resolves them.
type ProfileSummary struct {
	Code              string   `json:"code"`
	Name              string   `json:"name"`
	FeatureClassCode  string   `json:"feature_class_code"`
	SubtypeCode       *int     `json:"subtype_code,omitempty"`
	SourceFormat      string   `json:"source_format"`
	SourceLayer       *string  `json:"source_layer,omitempty"`
	WriteMode         string   `json:"write_mode"`
	MatchFields       []string `json:"match_fields"`
	GeometryMode      string   `json:"geometry_mode"`
	GeometryLonColumn *string  `json:"geometry_lon_column,omitempty"`
	GeometryLatColumn *string  `json:"geometry_lat_column,omitempty"`
	GeometryWktColumn *string  `json:"geometry_wkt_column,omitempty"`
}

// listActiveProfiles returns every active import profile, code-shaped and ordered
// by name for the wizard's picker. A LEFT JOIN to subtypes keeps profiles with no
// pinned subtype; deprecated profiles are excluded (they exist only for the
// history of past runs).
func listActiveProfiles(ctx context.Context, db *bun.DB) ([]ProfileSummary, error) {
	var rows []struct {
		Code              string   `bun:"code"`
		Name              string   `bun:"name"`
		FeatureClassCode  string   `bun:"fc_code"`
		SubtypeCode       *int     `bun:"subtype_code"`
		SourceFormat      string   `bun:"source_format"`
		SourceLayer       *string  `bun:"source_layer"`
		WriteMode         string   `bun:"write_mode"`
		MatchFields       []string `bun:"match_fields,array"`
		GeometryMode      string   `bun:"geometry_mode"`
		GeometryLonColumn *string  `bun:"geometry_lon_column"`
		GeometryLatColumn *string  `bun:"geometry_lat_column"`
		GeometryWktColumn *string  `bun:"geometry_wkt_column"`
	}
	err := db.NewSelect().
		TableExpr("metadata.import_profiles AS ip").
		ColumnExpr("ip.code AS code").
		ColumnExpr("ip.name AS name").
		ColumnExpr("fc.code AS fc_code").
		ColumnExpr("st.code AS subtype_code").
		ColumnExpr("ip.source_format AS source_format").
		ColumnExpr("ip.source_layer AS source_layer").
		ColumnExpr("ip.write_mode AS write_mode").
		ColumnExpr("ip.match_fields AS match_fields").
		ColumnExpr("ip.geometry_mode AS geometry_mode").
		ColumnExpr("ip.geometry_lon_column AS geometry_lon_column").
		ColumnExpr("ip.geometry_lat_column AS geometry_lat_column").
		ColumnExpr("ip.geometry_wkt_column AS geometry_wkt_column").
		Join("JOIN metadata.feature_classes AS fc ON fc.id = ip.feature_class_id").
		Join("LEFT JOIN metadata.subtypes AS st ON st.id = ip.subtype_id").
		Where("ip.status = ?", "active").
		OrderExpr("ip.name").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}

	out := make([]ProfileSummary, len(rows))
	for i, r := range rows {
		mf := r.MatchFields
		if mf == nil {
			mf = []string{}
		}
		out[i] = ProfileSummary{
			Code: r.Code, Name: r.Name, FeatureClassCode: r.FeatureClassCode,
			SubtypeCode: r.SubtypeCode, SourceFormat: r.SourceFormat, SourceLayer: r.SourceLayer,
			WriteMode:   r.WriteMode,
			MatchFields: mf, GeometryMode: r.GeometryMode,
			GeometryLonColumn: r.GeometryLonColumn, GeometryLatColumn: r.GeometryLatColumn,
			GeometryWktColumn: r.GeometryWktColumn,
		}
	}
	return out, nil
}
