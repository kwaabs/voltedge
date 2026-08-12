// Package compiler reads published metadata (metadata.feature_classes and
// everything hanging off it) and compiles it into real physical schema in
// the gis schema -- CREATE TABLE for a class compiled for the first time,
// ALTER TABLE ADD COLUMN for one that already has a table and gained a
// field. It never drops or alters an existing column: disabling a field
// is a metadata-level status flip (fields.status = deprecated), never a
// physical DROP COLUMN, matching docs/metadata-core.md's field lifecycle
// rules.
package compiler

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
// status = 'active' are ever compiled; deprecated fields keep their
// physical column (if one exists) but never gain a new one.
type Field struct {
	bun.BaseModel `bun:"table:metadata.fields"`

	ID             string  `bun:"id,pk"`
	FeatureClassID string  `bun:"feature_class_id"`
	Name           string  `bun:"name"`
	DataType       string  `bun:"data_type"`
	DomainID       *string `bun:"domain_id"`
	Nullable       bool    `bun:"nullable"`
	IsComputed     bool    `bun:"is_computed"`
	Status         string  `bun:"status"`
}

// Subtype mirrors metadata.subtypes -- see migrations/0008. A feature
// class with one or more subtypes gets a physical `subtype` column,
// constrained to the codes that exist at compile time.
type Subtype struct {
	bun.BaseModel `bun:"table:metadata.subtypes"`

	ID             string `bun:"id,pk"`
	FeatureClassID string `bun:"feature_class_id"`
	Code           int    `bun:"code"`
	Name           string `bun:"name"`
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

// MetadataVersion mirrors metadata.metadata_versions -- see
// migrations/0006. Compile creates one per call and links the compiled
// feature class to it. True multi-class atomic publishing (one version
// covering several feature classes compiled together) is future work --
// see docs/phase-1-metadata-core.md.
type MetadataVersion struct {
	bun.BaseModel `bun:"table:metadata.metadata_versions"`

	// nullzero: without it, bun inserts Go's zero value "" literally
	// instead of omitting the column, which overrides the table's
	// gen_random_uuid() default with an empty string.
	ID          string    `bun:"id,pk,nullzero"`
	PublishedAt time.Time `bun:"published_at"`
	PublishedBy string    `bun:"published_by"`
	Status      string    `bun:"status"`
}
