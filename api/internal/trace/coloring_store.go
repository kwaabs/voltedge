package trace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// energizationTTL bounds how stale the cached coloring may be before a read triggers a
// recompute. Operational changes (operate / switching / topology) invalidate the cache
// explicitly (InvalidateEnergization), so this is only the backstop for changes that
// don't go through those paths -- it self-heals within a minute without any wiring.
const energizationTTL = 60 * time.Second

// energizationRow is one currently-de-energised feature in the cache
// (runtime.energization, migration 0201). Presence = de-energised.
type energizationRow struct {
	bun.BaseModel `bun:"table:runtime.energization"`

	FeatureClass string `bun:"feature_class"`
	FeatureID    string `bun:"feature_id"`
}

// RecolorNetwork recomputes the whole-network coloring and REPLACES the cached
// de-energised set (runtime.energization) + stamps the freshness marker. It is the
// single writer of the energization read-model; nothing else sets it. Source-rooted
// when the model declares sources, else the dead-cone fallback -- either way it stores
// the de-energised set over the connected network.
//
// The dead set is computed fully in memory first, so the DELETE+INSERT replace is a
// sub-millisecond window; the cache is intentionally not transactional (a stale read
// during that window is harmless and self-corrects on the next read).
func RecolorNetwork(ctx context.Context, db bun.IDB) error {
	universe, err := connectedUniverse(ctx, db)
	if err != nil {
		return err
	}

	energized, ok, err := EnergizedSet(ctx, db)
	if err != nil {
		return err
	}
	var dead []FeatureRef
	if ok {
		for _, r := range universe {
			if !energized[r.FeatureID] {
				dead = append(dead, r)
			}
		}
	} else {
		dead, err = deEnergizedByOpenCones(ctx, db, universe)
		if err != nil {
			return err
		}
	}

	if _, err := db.NewRaw("DELETE FROM runtime.energization").Exec(ctx); err != nil {
		return fmt.Errorf("trace: clearing energization cache: %w", err)
	}
	if len(dead) > 0 {
		rows := make([]energizationRow, len(dead))
		for i, r := range dead {
			rows[i] = energizationRow{FeatureClass: r.FeatureClass, FeatureID: r.FeatureID}
		}
		if _, err := db.NewInsert().Model(&rows).Exec(ctx); err != nil {
			return fmt.Errorf("trace: writing energization cache: %w", err)
		}
	}
	if _, err := db.NewRaw(
		"INSERT INTO runtime.energization_state (id, computed_at, source_rooted) VALUES (true, now(), ?) "+
			"ON CONFLICT (id) DO UPDATE SET computed_at = now(), source_rooted = EXCLUDED.source_rooted", ok,
	).Exec(ctx); err != nil {
		return fmt.Errorf("trace: stamping energization state: %w", err)
	}
	slog.InfoContext(ctx, "energization recolor", "universe", len(universe), "de_energized", len(dead), "source_rooted", ok)
	return nil
}

// InvalidateEnergization marks the cached coloring stale, so the next read recomputes
// it. Called right where the operating picture changes (a device operates, a switching
// step executes) -- cheaper than recomputing synchronously in the operation's request,
// and the SSE the same sites fire makes the client refetch immediately anyway.
func InvalidateEnergization(ctx context.Context, db bun.IDB) error {
	if _, err := db.NewRaw("DELETE FROM runtime.energization_state").Exec(ctx); err != nil {
		return fmt.Errorf("trace: invalidating energization cache: %w", err)
	}
	return nil
}

// cachedDeEnergized reads the de-energised cache for refs, reporting fresh=false when
// the cache was never computed or is older than the TTL (the caller then recomputes).
// The de-energised set is small, so it's read whole and filtered in Go.
func cachedDeEnergized(ctx context.Context, db bun.IDB, refs []FeatureRef) (out []FeatureRef, fresh bool, err error) {
	var computedAt time.Time
	err = db.NewRaw("SELECT computed_at FROM runtime.energization_state WHERE id = true").Scan(ctx, &computedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if time.Since(computedAt) > energizationTTL {
		return nil, false, nil
	}

	var rows []energizationRow
	if err := db.NewRaw("SELECT feature_class, feature_id FROM runtime.energization").Scan(ctx, &rows); err != nil {
		return nil, false, err
	}
	deadKey := make(map[string]bool, len(rows))
	for _, r := range rows {
		deadKey[r.FeatureClass+"|"+r.FeatureID] = true
	}
	out = make([]FeatureRef, 0)
	for _, r := range refs {
		if deadKey[r.FeatureClass+"|"+r.FeatureID] {
			out = append(out, r)
		}
	}
	return out, true, nil
}

// connectedUniverse is every live feature that appears in the connectivity graph --
// the set the coloring stores a verdict over. A feature with no connectivity isn't in
// the operating picture, so it's absent from the cache (treated energised by absence,
// as before). Deleted features are excluded.
func connectedUniverse(ctx context.Context, db bun.IDB) ([]FeatureRef, error) {
	var rows []struct {
		ClassID   string `bun:"class_id"`
		FeatureID string `bun:"feature_id"`
	}
	const q = `
SELECT DISTINCT class_id, feature_id FROM (
    SELECT from_feature_class_id AS class_id, from_feature_id AS feature_id FROM runtime.feature_connectivity
    UNION
    SELECT to_feature_class_id, to_feature_id FROM runtime.feature_connectivity
) e
WHERE EXISTS (SELECT 1 FROM runtime.feature_registry r WHERE r.feature_id = e.feature_id AND r.deleted_at IS NULL)`
	if err := db.NewRaw(q).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("trace: reading connected universe: %w", err)
	}

	classes, err := metadata.ListFeatureClasses(ctx, db)
	if err != nil {
		return nil, err
	}
	codeByID := make(map[string]string, len(classes))
	for _, c := range classes {
		codeByID[c.ID] = c.Code
	}

	out := make([]FeatureRef, 0, len(rows))
	for _, r := range rows {
		code, ok := codeByID[r.ClassID]
		if !ok {
			continue
		}
		out = append(out, FeatureRef{FeatureClass: code, FeatureID: r.FeatureID})
	}
	return out, nil
}
