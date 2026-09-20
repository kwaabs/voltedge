package access

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// InvalidError is a client-fixable problem (an unknown user or territory, a
// manager assignment that would form a cycle) -- distinct from a plain error so
// an HTTP caller maps it to a 400 rather than a 500, the same split
// territory.InvalidError and crud's validationError draw. Callers test with
// IsInvalid.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalidf(format string, args ...any) error {
	return &InvalidError{msg: fmt.Sprintf(format, args...)}
}

// IsInvalid reports whether err is (or wraps) an InvalidError.
func IsInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

// EffectiveIDs returns the full set of territory ids a user is scoped to: the
// union of every territory GRANTED to the user or to anyone who reports up to
// them (down the manager tree), each expanded DOWN the territory tree to its
// whole subtree. It is the single spatial-scope primitive the enforcement
// layers (crud reads/writes, trace, export, tiles) build their
// `territory_id = ANY(...)` predicate from.
//
// One recursive query does all three steps:
//
//   - reports: the user plus every transitive report (walk down manager_id);
//   - granted: the territory nodes any of those users hold in user_territories;
//   - scope:   those nodes plus their whole subtrees (walk down parent_id).
//
// A user with no grants (and no reports who have any) yields an empty slice,
// which the callers treat as "match nothing" (fail closed) -- NOT "no filter".
// The territories:unscoped bypass is handled by the caller (package auth), which
// skips this call entirely for a bypass holder, so an empty result here is
// always a genuine "scoped to nothing".
func EffectiveIDs(ctx context.Context, db bun.IDB, userID string) ([]string, error) {
	const query = `
WITH RECURSIVE reports AS (
    SELECT id FROM iam.users WHERE id = ?
    UNION
    SELECT u.id FROM iam.users u JOIN reports r ON u.manager_id = r.id
),
granted AS (
    SELECT DISTINCT ut.territory_id AS id
    FROM iam.user_territories ut
    JOIN reports r ON ut.user_id = r.id
),
scope AS (
    SELECT id FROM granted
    UNION
    SELECT t.id FROM gis.territories t JOIN scope s ON t.parent_id = s.id
)
SELECT id::text FROM scope`

	rows, err := db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("access: resolving effective territories for %s: %w", userID, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("access: scanning effective territory id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Subordinates returns the ids of every user who reports up to userID
// (transitively, down the manager tree) -- NOT including userID itself. It is
// the draft-visibility primitive for Private-drafts (Phase 61): a private draft
// (one that has never been public) is visible to its author AND to everyone
// above the author in the org chart, so a viewer sees a private draft when its
// created_by is the viewer or ANY of the viewer's subordinates. The caller adds
// the viewer's own id to this set to form the full "authors whose private
// drafts I may see" list.
//
// This is the DOWN walk of the same manager tree EffectiveIDs walks (there the
// `reports` CTE seeds with the user and includes self, because a user's own
// grants are in scope; here we seed with the user's direct reports and exclude
// self, because "my subordinates" is exactly the people below me). An empty
// result (a leaf user with no reports) is normal -- such a user sees only their
// own private drafts.
func Subordinates(ctx context.Context, db bun.IDB, userID string) ([]string, error) {
	const query = `
WITH RECURSIVE reports AS (
    SELECT id FROM iam.users WHERE manager_id = ?
    UNION
    SELECT u.id FROM iam.users u JOIN reports r ON u.manager_id = r.id
)
SELECT id::text FROM reports`

	rows, err := db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("access: resolving subordinates for %s: %w", userID, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("access: scanning subordinate id: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListForUser returns the territory ids DIRECTLY granted to a user (not the
// expanded effective set, not inherited-from-reports) -- the raw grant rows, for
// the management UI to render and edit. Ordered by grant time so the newest
// grant is last.
func ListForUser(ctx context.Context, db bun.IDB, userID string) ([]UserTerritory, error) {
	var grants []UserTerritory
	if err := db.NewSelect().Model(&grants).
		Where("user_id = ?", userID).
		Order("granted_at").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("access: listing grants for %s: %w", userID, err)
	}
	return grants, nil
}

// Grant gives a user a direct grant on a territory node. Idempotent: a repeat
// grant is a no-op (ON CONFLICT on the composite PK), so callers needn't check
// first. grantedBy is the acting admin (nil for bootstrap). Both the user and
// the territory are validated to exist, so a typo is a 400, not a raw FK 500.
func Grant(ctx context.Context, db bun.IDB, userID, territoryID string, grantedBy *string) error {
	if err := requireUser(ctx, db, userID); err != nil {
		return err
	}
	if err := requireTerritory(ctx, db, territoryID); err != nil {
		return err
	}
	_, err := db.NewInsert().
		Model(&UserTerritory{UserID: userID, TerritoryID: territoryID, GrantedBy: grantedBy}).
		On("CONFLICT (user_id, territory_id) DO NOTHING").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("access: granting territory %s to %s: %w", territoryID, userID, err)
	}
	return nil
}

// Revoke removes a direct grant. Idempotent: revoking a grant that isn't held
// is a no-op (zero rows), not an error -- the caller's intent (this user should
// not hold this grant) is satisfied either way.
func Revoke(ctx context.Context, db bun.IDB, userID, territoryID string) error {
	_, err := db.NewDelete().
		Model((*UserTerritory)(nil)).
		Where("user_id = ?", userID).
		Where("territory_id = ?", territoryID).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("access: revoking territory %s from %s: %w", territoryID, userID, err)
	}
	return nil
}

// SetManager sets (or clears, with managerID == nil) a user's manager -- the
// edge in the org tree that access cascades up. It rejects a self-reference or
// any assignment that would close a cycle (a user transitively managing
// themselves), mirroring territory.wouldCreateCycle. Both users are validated
// to exist.
func SetManager(ctx context.Context, db bun.IDB, userID string, managerID *string) error {
	if err := requireUser(ctx, db, userID); err != nil {
		return err
	}
	if managerID != nil {
		if *managerID == userID {
			return invalidf("a user can't be their own manager")
		}
		if err := requireUser(ctx, db, *managerID); err != nil {
			return err
		}
		cycles, err := wouldManagerCycle(ctx, db, userID, *managerID)
		if err != nil {
			return err
		}
		if cycles {
			return invalidf("can't set that manager -- it would make the user manage themselves through the org tree")
		}
	}
	if _, err := db.NewUpdate().
		TableExpr("iam.users").
		Set("manager_id = ?", managerID).
		Set("updated_at = now()").
		Where("id = ?", userID).
		Exec(ctx); err != nil {
		return fmt.Errorf("access: setting manager of %s: %w", userID, err)
	}
	return nil
}

// wouldManagerCycle reports whether making managerID the manager of userID
// would form a cycle -- i.e. userID is already managerID itself or somewhere
// above managerID in the org tree. It walks managerID's manager chain upward
// and looks for userID: if the user being assigned a manager already sits above
// that manager, the edge would close a loop. Mirrors territory.wouldCreateCycle.
func wouldManagerCycle(ctx context.Context, db bun.IDB, userID, managerID string) (bool, error) {
	const query = `
WITH RECURSIVE ancestors AS (
    SELECT id, manager_id FROM iam.users WHERE id = ?
    UNION ALL
    SELECT u.id, u.manager_id FROM iam.users u
    JOIN ancestors a ON u.id = a.manager_id
)
SELECT EXISTS (SELECT 1 FROM ancestors WHERE id = ?)`
	var cycles bool
	if err := db.QueryRowContext(ctx, query, managerID, userID).Scan(&cycles); err != nil {
		return false, fmt.Errorf("access: checking for manager cycle: %w", err)
	}
	return cycles, nil
}

// requireUser returns an InvalidError when userID names no active/known user,
// so a bad id from an API caller becomes a 400 rather than a foreign-key 500.
func requireUser(ctx context.Context, db bun.IDB, userID string) error {
	exists, err := db.NewSelect().TableExpr("iam.users").Where("id = ?", userID).Exists(ctx)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return invalidf("user %q does not exist", userID)
		}
		return fmt.Errorf("access: checking user %s: %w", userID, err)
	}
	if !exists {
		return invalidf("user %q does not exist", userID)
	}
	return nil
}

// requireTerritory returns an InvalidError when territoryID names no territory.
func requireTerritory(ctx context.Context, db bun.IDB, territoryID string) error {
	exists, err := db.NewSelect().TableExpr("gis.territories").Where("id = ?", territoryID).Exists(ctx)
	if err != nil {
		return fmt.Errorf("access: checking territory %s: %w", territoryID, err)
	}
	if !exists {
		return invalidf("territory %q does not exist", territoryID)
	}
	return nil
}
