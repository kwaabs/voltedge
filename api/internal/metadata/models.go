// Package metadata is the shared read layer over the metadata.* tables
// (see docs/metadata-core.md) -- feature classes, fields, subtypes,
// domains. Both the schema compiler (api/internal/compiler) and the
// generic CRUD engine (api/internal/crud) load the same shape of data
// through this package rather than each keeping their own copy of what a
// Field or a Domain looks like.
package metadata

import (
	"time"

	"github.com/uptrace/bun"
)

// FeatureClass mirrors metadata.feature_classes -- see migrations/0007.
type FeatureClass struct {
	bun.BaseModel `bun:"table:metadata.feature_classes"`

	ID                 string  `bun:"id,pk"`
	Code               string  `bun:"code"`
	Name               string  `bun:"name"`
	GeometryType       string  `bun:"geometry_type"`
	SRID               *int    `bun:"srid"`
	NetworkRole        string  `bun:"network_role"`
	PhysicalTable      *string `bun:"physical_table"`
	Status             string  `bun:"status"`
	IsDiagramContainer bool    `bun:"is_diagram_container"`
	MetadataVersionID  *string `bun:"metadata_version_id"`
}

// Field mirrors metadata.fields -- see migrations/0008. Only rows with
// status = 'active' are ever compiled or accepted on a write; deprecated
// fields keep their physical column (if one exists) but are invisible to
// both the compiler and the CRUD engine.
type Field struct {
	bun.BaseModel `bun:"table:metadata.fields"`

	ID                   string  `bun:"id,pk"`
	FeatureClassID       string  `bun:"feature_class_id"`
	Name                 string  `bun:"name"`
	DataType             string  `bun:"data_type"`
	DomainID             *string `bun:"domain_id"`
	Nullable             bool    `bun:"nullable"`
	IsComputed           bool    `bun:"is_computed"`
	Status               string  `bun:"status"`
	VisibleOnCreate      bool    `bun:"visible_on_create"`
	VisibleOnUpdate      bool    `bun:"visible_on_update"`
	EditableAfterPublish bool    `bun:"editable_after_publish"`
}

// Subtype mirrors metadata.subtypes -- see migrations/0008.
type Subtype struct {
	bun.BaseModel `bun:"table:metadata.subtypes"`

	ID             string `bun:"id,pk"`
	FeatureClassID string `bun:"feature_class_id"`
	Code           int    `bun:"code"`
	Name           string `bun:"name"`
}

// SubtypeFieldRule mirrors metadata.subtype_field_rules -- see
// migrations/0008. A field with no rule row for a given subtype is
// optional by default; the CRUD engine's required-field validation on
// create walks these per the feature's subtype.
type SubtypeFieldRule struct {
	bun.BaseModel `bun:"table:metadata.subtype_field_rules"`

	ID              string  `bun:"id,pk"`
	SubtypeID       string  `bun:"subtype_id"`
	FieldID         string  `bun:"field_id"`
	IsRequired      bool    `bun:"is_required"`
	DefaultOverride *string `bun:"default_override"`
}

// Domain mirrors metadata.domains -- see migrations/0004.
type Domain struct {
	bun.BaseModel `bun:"table:metadata.domains"`

	ID         string `bun:"id,pk"`
	Code       string `bun:"code"`
	DomainType string `bun:"domain_type"`
}

// DomainValue mirrors metadata.domain_values -- see migrations/0004.
type DomainValue struct {
	bun.BaseModel `bun:"table:metadata.domain_values"`

	ID       string `bun:"id,pk"`
	DomainID string `bun:"domain_id"`
	Code     string `bun:"code"`
}

// Version mirrors metadata.metadata_versions -- see migrations/0006.
type Version struct {
	bun.BaseModel `bun:"table:metadata.metadata_versions"`

	// nullzero: without it, bun inserts Go's zero value "" literally
	// instead of omitting the column, which overrides the table's
	// gen_random_uuid() default with an empty string.
	ID          string    `bun:"id,pk,nullzero"`
	PublishedAt time.Time `bun:"published_at"`
	PublishedBy string    `bun:"published_by"`
	Status      string    `bun:"status"`
}
