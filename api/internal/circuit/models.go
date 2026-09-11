// Package circuit is the read/write layer over gis.circuits (migration 0062)
// -- the catalog of named, network-wide electrical circuits a utility runs
// (e.g. the MV feeder "Old Age 2"). A circuit is the persistent parent
// identity that many features across many classes belong to: its fuses,
// conductors, isolators, transformers, and so on. This package owns the
// catalog and its CRUD; feature membership (a later slice's circuit_id field)
// is derived by a downstream trace from the circuit's source with manual
// override, and lives in the crud write path, not here.
//
// It deliberately mirrors api/internal/territory in shape (a gis-schema lookup
// entity, curated in-app through a CRUD API + admin UI) but is FLAT: no level
// catalog, no parent tree, no geometry of its own. A circuit's extent is the
// union of the features stamped onto it, not a drawn boundary. MV and LV are
// separate namespaces, so uniqueness is per (network, name).
package circuit

import (
	"time"

	"github.com/uptrace/bun"
)

// Networks are the two voltage namespaces a circuit lives in. An MV "Old Age
// 2" is a different circuit from any LV one of the same name, so the name is
// only unique within a network. Matches the CHECK constraint in migration
// 0062.
const (
	NetworkMV = "MV"
	NetworkLV = "LV"
)

// Circuit mirrors gis.circuits -- one named circuit. VoltageKV is a property
// of the circuit (an MV circuit "could be 11 or 33"), nullable until known.
// SourceFeatureClass/SourceFeatureID point at the feature a later slice's
// assign-by-trace starts from (the substation breaker / transformer LV
// terminal); both nullable and not a DB foreign key -- the referenced row
// lives in a per-class physical table this catalog can't FK to, the same
// no-enforced-join convention territory_id follows.
type Circuit struct {
	bun.BaseModel `bun:"table:gis.circuits"`

	ID                 string    `bun:"id,pk" json:"id"`
	Name               string    `bun:"name" json:"name"`
	Network            string    `bun:"network" json:"network"`
	VoltageKV          *float64  `bun:"voltage_kv" json:"voltage_kv"`
	SourceFeatureClass *string   `bun:"source_feature_class" json:"source_feature_class"`
	SourceFeatureID    *string   `bun:"source_feature_id" json:"source_feature_id"`
	CreatedAt          time.Time `bun:"created_at" json:"created_at"`
	UpdatedAt          time.Time `bun:"updated_at" json:"updated_at"`
}
