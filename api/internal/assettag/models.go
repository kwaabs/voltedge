// Package assettag is the asset-tagging module: every feature carries one or
// more short, scannable tag codes (rendered as a QR + Code128 + human-readable
// label and affixed on the physical asset in the field), and a scan resolves a
// code back to the GIS record.
//
// The store is runtime.asset_tags (migration 0114). Codes are MINTED by a DB
// trigger on runtime.feature_registry, NOT by this package: the registry already
// sees every feature the instant it is written by any path (REST, MCP, field
// sync, raw-SQL seeds), so one trigger there inherits total coverage and this Go
// layer never has to mint on the happy path. What this package owns is the read
// and lifecycle side -- resolve a scanned code (scope- and draft-aware, so a
// code you may not see answers 404), list a feature's tags, register a legacy /
// external number, set the primary, void, and reissue.
//
// A feature may hold MANY codes: the system-minted one plus any pre-existing
// asset numbers a utility already paints on its plant. Exactly one is primary --
// the one that gets printed. The generated code is primary by default, but an
// editor can promote a legacy number so an already-labelled brownfield asset
// keeps its number of record; every code still resolves.
package assettag

import (
	"time"

	"github.com/uptrace/bun"
)

// Tag is one row of runtime.asset_tags.
type Tag struct {
	bun.BaseModel `bun:"table:runtime.asset_tags,alias:t"`

	TagCode          string  `json:"tag_code" bun:"tag_code,pk"`
	FeatureClassCode string  `json:"feature_class_code" bun:"feature_class_code"`
	FeatureID        string  `json:"feature_id" bun:"feature_id"`
	Source           string  `json:"source" bun:"source"` // generated | external
	IsPrimary        bool    `json:"is_primary" bun:"is_primary"`
	Status           string  `json:"status" bun:"status"` // active | void | superseded
	CreatedBy        *string `json:"created_by" bun:"created_by"`
	// DB-managed: never written on insert (the store sets them via SQL defaults /
	// explicit Set(...) clauses), only scanned back. scanonly keeps bun from
	// sending a zero time / count that would clobber the column default.
	PrintCount int       `json:"print_count" bun:"print_count,scanonly"`
	CreatedAt  time.Time `json:"created_at" bun:"created_at,scanonly"`
	UpdatedAt  time.Time `json:"updated_at" bun:"updated_at,scanonly"`
}
