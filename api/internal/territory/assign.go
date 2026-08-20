package territory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/uptrace/bun"
)

// SettingEnforcementEnabled is the iam.system_settings key that gates the
// "every geographic feature must belong to at least a district" rule.
// Reusing the existing global key/value settings table (migrations/0024)
// means the invariant can be switched on at runtime -- after territories
// are authored and existing rows are backfilled -- with no redeploy, the
// same pattern as the local-auth break-glass switch.
const SettingEnforcementEnabled = "territory_enforcement_enabled"

// EnforcementEnabled reports whether write-time enforcement is on. A
// missing or unparseable row is treated as OFF -- during rollout the
// invariant must not reject writes before coverage exists, so the safe
// default is permissive (features still save, just possibly unassigned).
// Only an explicit "true" turns rejection on.
func EnforcementEnabled(ctx context.Context, db bun.IDB) (bool, error) {
	var value string
	err := db.NewSelect().
		ColumnExpr("value").
		TableExpr("iam.system_settings").
		Where("key = ?", SettingEnforcementEnabled).
		Scan(ctx, &value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("territory: reading enforcement setting: %w", err)
	}
	enabled, parseErr := strconv.ParseBool(value)
	if parseErr != nil {
		return false, nil
	}
	return enabled, nil
}

// Assign decides which territory a geographic feature belongs to at write
// time. It is the single entry point the CRUD engine calls; it returns the
// territory_id to store (nil = leave unassigned) or an InvalidError to
// reject the write with a 400.
//
// Two paths:
//
//   - Explicit override (override != nil): the caller named a territory.
//     It's validated as real and at least the minimum level, ALWAYS --
//     independent of the enforcement flag -- because it's a deliberate
//     assertion of membership; a too-shallow override is a user error worth
//     rejecting regardless of rollout state. It is NOT required to
//     spatially contain the feature (that's the whole point of an override:
//     the boundary may be missing or imperfect).
//
//   - Automatic (override == nil): resolve the deepest territory whose
//     boundary contains the feature's centroid (ResolveContaining). A
//     qualifying match (at least the minimum level) is always stored, even
//     when enforcement is off -- that's how coverage gets populated. The
//     enforcement flag only governs the failure case: with it ON, "no
//     territory covers this" or "only a too-shallow one does" rejects the
//     write; with it OFF, both simply leave the feature unassigned (nil).
func Assign(ctx context.Context, db bun.IDB, geomGeoJSON string, srid int, override *string) (*string, error) {
	min, err := MinFeatureLevel(ctx, db)
	if err != nil {
		return nil, err
	}

	if override != nil {
		code, rank, err := levelOfTerritory(ctx, db, *override)
		if err != nil {
			return nil, err
		}
		if rank < min.Rank {
			return nil, invalidf("assigned territory is a %q, but a feature must belong to at least a %q", code, min.Code)
		}
		return override, nil
	}

	enforced, err := EnforcementEnabled(ctx, db)
	if err != nil {
		return nil, err
	}

	match, err := ResolveContaining(ctx, db, geomGeoJSON, srid)
	if err != nil {
		return nil, err
	}
	if match == nil {
		if enforced {
			return nil, invalidf("this location isn't within any %q -- no territory covers it", min.Code)
		}
		return nil, nil
	}
	if match.LevelRank < min.Rank {
		if enforced {
			return nil, invalidf("this location is only within %q (a %q), but a feature must belong to at least a %q", match.TerritoryName, match.LevelCode, min.Code)
		}
		return nil, nil
	}
	return &match.TerritoryID, nil
}

// levelOfTerritory returns the level code and rank a territory sits at --
// what the override path needs to check depth, in one join. An unknown id
// is an InvalidError so a bad override surfaces as a 400, not a 500.
func levelOfTerritory(ctx context.Context, db bun.IDB, territoryID string) (code string, rank int, err error) {
	query := `SELECT l.code, l.rank
	          FROM gis.territories t
	          JOIN gis.territory_levels l ON l.id = t.level_id
	          WHERE t.id = ?`
	err = db.QueryRowContext(ctx, query, territoryID).Scan(&code, &rank)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, invalidf("territory %q does not exist", territoryID)
	}
	if err != nil {
		return "", 0, fmt.Errorf("territory: loading level for %s: %w", territoryID, err)
	}
	return code, rank, nil
}
