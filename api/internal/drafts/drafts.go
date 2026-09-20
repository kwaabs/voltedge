// Package drafts is the cross-cutting Private-drafts (Phase 61) primitive: the
// single place every feature-read surface (crud list/get, tiles, export, field
// packs, analysis) turns the acting user into a draft-visibility filter.
//
// A feature that has never been public is private to its author and the
// author's management chain: a viewer may see it only when its created_by is the
// viewer or one of the viewer's subordinates. "Never been public" is
// status = 'draft' AND published_at IS NULL, so the PUBLIC test -- a row anyone
// in scope may see -- is `status = 'published' OR published_at IS NOT NULL`.
// Including the live status as well as the sticky published_at marker is
// belt-and-suspenders: any path that creates a row already-published is still
// treated as public even if it never stamped published_at.
//
// This composes with -- never replaces -- the Phase 58 territory predicate: both
// are AND-ed into the same read, so a private draft is visible only to an
// authorized author AND within the viewer's territory scope. It is an
// independent, additive gate with its own rollout switch, so an install that
// hasn't turned it on reads exactly as before.
//
// Kept in its own package (not crud) so tiles/export/etc. can share it without
// importing crud. It imports auth (for the session user) but nothing imports
// drafts that auth depends on, so there is no cycle.
package drafts

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// SettingEnabled is the iam.system_settings key that gates the draft read
// filter. Off by default -- turning it on retroactively privatizes every
// never-published feature to its author chain, so it's a deliberate admin action
// (mirrors territory.SettingEnforcementEnabled), not a deploy-time surprise.
const SettingEnabled = "private_drafts_enabled"

// Enabled reports whether the draft read filter is on. A missing or unparseable
// row is OFF -- the safe, behavior-preserving default during rollout. Only an
// explicit "true" turns it on.
func Enabled(ctx context.Context, db bun.IDB) (bool, error) {
	var value string
	err := db.NewSelect().
		ColumnExpr("value").
		TableExpr("iam.system_settings").
		Where("key = ?", SettingEnabled).
		Scan(ctx, &value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	enabled, parseErr := strconv.ParseBool(value)
	if parseErr != nil {
		return false, nil
	}
	return enabled, nil
}

// SetEnabled flips the private_drafts_enabled system setting -- the rollout
// switch that turns the draft read filter on for the whole install. Upserts the
// iam.system_settings row so it works whether or not the key exists yet,
// mirroring territory.SetEnforcement.
func SetEnabled(ctx context.Context, db bun.IDB, enabled bool) error {
	value := "false"
	if enabled {
		value = "true"
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO iam.system_settings (key, value)
		VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		SettingEnabled, value)
	return err
}

// VisibleAuthors resolves the acting request's draft-visibility filter. It
// returns the set of author ids whose private drafts the viewer may see (the
// viewer's own id plus their subordinates) and whether the filter applies.
//
// apply is false -- "read every draft, add no predicate" -- when any of:
//
//   - the rollout gate is off (private_drafts_enabled): the feature is dark
//     until an admin flips it, so behavior is unchanged;
//   - there is no session on the context: an internal, non-HTTP caller is
//     trusted;
//   - the user is Unscoped OR BreakGlass: an admin / the omnipotent break-glass
//     account sees every draft regardless. BreakGlass is checked explicitly (not
//     only via Unscoped) so the account sees all private drafts even if it were
//     ever provisioned without the territories:unscoped grant -- it overrides
//     every permission and spatial bound by definition.
//
// When apply is true, authors is non-empty (it always contains the viewer's own
// id), so a user always sees at least their own private drafts.
func VisibleAuthors(ctx context.Context, db bun.IDB) (authors []string, apply bool, err error) {
	enabled, err := Enabled(ctx, db)
	if err != nil {
		return nil, false, err
	}
	if !enabled {
		return nil, false, nil
	}
	user, ok := auth.UserFrom(ctx)
	if !ok {
		return nil, false, nil
	}
	if user.Unscoped || user.BreakGlass {
		return nil, false, nil
	}
	return append([]string{user.ID}, user.Subordinates...), true, nil
}

// WhereClause turns VisibleAuthors' (authors, apply) into a SQL fragment plus
// its bind args, for AND-ing into a feature read. Returns ("", nil) when the
// filter doesn't apply. alias is the table alias prefix for the columns (e.g.
// "t." for the tile query, "" for a bare single-table read). A row survives if
// it is public (currently published, or ever published via the sticky marker)
// OR authored by someone in the visible set.
func WhereClause(authors []string, apply bool, alias string) (clause string, args []any) {
	if !apply {
		return "", nil
	}
	placeholders := make([]string, len(authors))
	args = make([]any, len(authors))
	for i := range authors {
		placeholders[i] = "?"
		args[i] = authors[i]
	}
	return "(" + alias + "status = 'published' OR " + alias + "published_at IS NOT NULL OR " +
		alias + "created_by::text IN (" + strings.Join(placeholders, ", ") + "))", args
}

// IsVisible is the single-row counterpart of WhereClause: given a row's status,
// published_at and created_by, and the (authors, apply) from VisibleAuthors, it
// reports whether the acting user may see the row. Callers that already hold a
// scanned row (a GET post-filter, an export/analysis loop) use this instead of
// re-querying. When apply is false every row is visible.
func IsVisible(status string, publishedAt any, createdBy string, authors []string, apply bool) bool {
	if !apply {
		return true
	}
	if status == "published" || publishedAt != nil {
		return true
	}
	for _, a := range authors {
		if a == createdBy {
			return true
		}
	}
	return false
}
