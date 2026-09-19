// Package featureregistry serves the read side of runtime.feature_registry
// (migration 0108): a single cross-class index of every feature row, keyed by
// its globally-unique id, kept in sync by a per-class DB trigger the compiler
// attaches. It exists so a caller can resolve an asset id -- or offer a
// typeahead over live assets of a class -- without knowing which gis.<code>
// table holds the row. The bare (class, id) references faults / oms / wam carry,
// and the asset-id pickers on their forms, are the first consumers.
//
// This package is read-only: nothing writes the registry through Go. The
// trigger owns every write (INSERT/UPDATE/DELETE of a gis row), so an app-level
// writer here would be a second, drift-prone source of truth -- exactly what the
// trigger design set out to avoid.
package featureregistry

import (
	"time"

	"github.com/uptrace/bun"
)

// Entry is one registry row joined to its class's human name. It is the shape
// both the lookup and the search return.
//
// There is deliberately no per-feature label (a "name" for the individual
// asset). Labels are computed per class from a LabelDef expression at read time
// in the CRUD layer; a plpgsql trigger can't cheaply reproduce that, so the
// registry indexes ids, not names. A caller that needs the label resolves it
// lazily against the per-class endpoint once a candidate id is chosen -- the
// registry's job is to find the id and its class, fast.
type Entry struct {
	bun.BaseModel `bun:"table:runtime.feature_registry,alias:fr"`

	FeatureID        string     `json:"feature_id" bun:"feature_id"`
	FeatureClassCode string     `json:"feature_class_code" bun:"feature_class_code"`
	ClassName        *string    `json:"class_name" bun:"class_name"`
	Status           *string    `json:"status" bun:"status"`
	DeletedAt        *time.Time `json:"deleted_at" bun:"deleted_at"`
	CreatedAt        *time.Time `json:"created_at" bun:"created_at"`
	TerritoryID      *string    `json:"territory_id" bun:"territory_id"`
}
