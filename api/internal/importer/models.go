package importer

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// This file holds the bun row models for the two metadata.import_* config
// tables (migration 0102). They live in this package rather than in
// api/internal/admin so both sides of the engine share one definition of the
// row shape: the admin editor (api/internal/admin/imports.go) writes them, and
// the load worker (a later slice) reads them to snapshot a run. Keeping them
// here also keeps the dependency edge one-way -- admin imports importer, never
// the reverse -- so the standalone worker binary never pulls in the admin HTTP
// surface.
//
// These are the raw, id-carrying rows. The admin editor maps them to/from its
// own code-shaped wire DTOs (feature_class_code, field_name, subtype_code); the
// worker consumes the resolved ids directly.

// Profile is one metadata.import_profiles row: a reusable, named import for a
// target feature class (optionally pinned to a subtype), holding the write
// policy (insert_only|upsert + the match_fields natural key) and how geometry
// is assembled for each row (geometry_mode + its source columns).
type Profile struct {
	bun.BaseModel `bun:"table:metadata.import_profiles"`

	ID                string    `bun:"id,pk"`
	Code              string    `bun:"code"`
	Name              string    `bun:"name"`
	FeatureClassID    string    `bun:"feature_class_id"`
	SubtypeID         *string   `bun:"subtype_id"`
	SourceFormat      string    `bun:"source_format"`
	WriteMode         string    `bun:"write_mode"`
	MatchFields       []string  `bun:"match_fields,array"`
	GeometryMode      string    `bun:"geometry_mode"`
	GeometryLonColumn *string   `bun:"geometry_lon_column"`
	GeometryLatColumn *string   `bun:"geometry_lat_column"`
	GeometryWktColumn *string   `bun:"geometry_wkt_column"`
	Status            string    `bun:"status"`
	CreatedAt         time.Time `bun:"created_at,nullzero"`
	UpdatedAt         time.Time `bun:"updated_at,nullzero"`
	CreatedBy         *string   `bun:"created_by"`
	UpdatedBy         *string   `bun:"updated_by"`
}

// FieldMapping is one metadata.import_field_mappings row: one source column ->
// one target field, with the coercion (transform + its parameter) bridging the
// source value to the field's data type. value_map is a raw jsonb passthrough
// (marshalled from the request, parsed back for the response / consumed as a
// lookup by the load).
type FieldMapping struct {
	bun.BaseModel `bun:"table:metadata.import_field_mappings"`

	ID            string          `bun:"id,pk"`
	ProfileID     string          `bun:"profile_id"`
	FieldID       string          `bun:"field_id"`
	SourceColumn  *string         `bun:"source_column"`
	Transform     string          `bun:"transform"`
	ConstantValue *string         `bun:"constant_value"`
	ScaleFactor   *float64        `bun:"scale_factor"`
	ValueMap      json.RawMessage `bun:"value_map"`
	CreatedAt     time.Time       `bun:"created_at,nullzero"`
}
