// Package access is the spatial access-control layer (Phase 58): it links a
// user to the territories they may see and edit, and resolves the effective
// territory set a request is scoped to.
//
// Two hierarchies feed that set:
//
//   - the territory tree (gis.territories) cascades DOWN -- a grant on a node
//     covers its whole subtree (via territory.DescendantIDs);
//   - the org/people tree (iam.users.manager_id) cascades UP -- a manager
//     inherits the union of every grant held by their transitive reports.
//
// The effective set is `EffectiveIDs`; enforcement of it (reads, writes, tiles)
// lives in the crud/trace/export/tile layers, all gated on the existing
// territory_enforcement_enabled flag. A holder of the territories:unscoped
// permission bypasses scoping entirely -- that check stays in package auth
// (which already loads the permission set), so this package never imports auth
// and auth can depend on it without a cycle.
package access

import (
	"time"

	"github.com/uptrace/bun"
)

// PermUnscoped is the permission key whose holders bypass territory scoping
// (see and edit every feature regardless of grants). Seeded by migration 0094
// and granted to admin / break-glass-admin. Defined here so package auth can
// reference the one canonical string when computing SessionUser.Unscoped.
const PermUnscoped = "territories:unscoped"

// UserTerritory mirrors iam.user_territories (migration 0094) -- one direct
// user->territory grant. The composite PK (UserID, TerritoryID) makes a
// re-grant a no-op; a revoke is a DELETE of the row. TerritoryID names a node
// in the tree; its subtree is expanded at resolve time, never stored here.
type UserTerritory struct {
	bun.BaseModel `bun:"table:iam.user_territories"`

	UserID      string    `bun:"user_id,pk"`
	TerritoryID string    `bun:"territory_id,pk"`
	GrantedAt   time.Time `bun:"granted_at,nullzero"`
	GrantedBy   *string   `bun:"granted_by"`
}
