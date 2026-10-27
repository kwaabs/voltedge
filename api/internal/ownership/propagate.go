// Package ownership is the asset-ownership propagation engine (Slice 2 of the
// ownership axis). The axis itself -- the coded `ownership` field (utility /
// private) on the transformer + LV classes -- is seeded metadata
// (seed/ownership_axis.sql). This package makes the agreed rule real: declaring
// a boundary asset (a transformer) PRIVATE stamps `ownership = private` onto
// every feature electrically downstream of it, and a re-sync recomputes the
// whole map from the current set of private boundaries.
//
// It is the direct twin of circuit.AssignCircuit: trace from a source, then
// bulk-write one field across every reached class in one transaction. The one
// difference is the trace -- ownership uses OWNERSHIP_DOWNSTREAM (a downstream,
// NO-barrier, cross-transformer config), because ownership is a connectivity
// property: an asset beyond a normally-open switch, or across the private
// transformer, is still privately owned.
package ownership

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/trace"
)

const (
	// ownershipTraceCode is the trace_configurations code the propagation walks
	// from a private boundary (seed/ownership_axis.sql): DOWNSTREAM, no barriers,
	// cross transformers. We also pass CrossTransformers=true per-run as
	// belt-and-braces, so the walk crosses the (private) transformer into the LV
	// regardless of the stored default.
	ownershipTraceCode = "OWNERSHIP_DOWNSTREAM"

	ownerPrivate = "private"
)

// boundaryClasses are the classes a re-sync treats as declared ownership
// boundaries: a transformer carrying ownership=private is the point you set by
// hand, and everything below it derives private. (Hand-marking a non-boundary
// feature private is not the propagation workflow and is not preserved by a
// re-sync -- documented behaviour.)
var boundaryClasses = []string{"DIST_XFMR", "POWER_XFMR"}

// FeatureRef identifies one feature.
type FeatureRef struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
}

// ClassCount is how many features of one class a run stamped.
type ClassCount struct {
	FeatureClass string `json:"feature_class"`
	Stamped      int    `json:"stamped"`
}

// PropagateResult reports one "mark downstream private" run.
type PropagateResult struct {
	Source   FeatureRef   `json:"source"`
	Reached  int          `json:"reached"`
	Stamped  int          `json:"stamped"`
	PerClass []ClassCount `json:"per_class"`
}

// ResyncResult reports a global re-sync.
type ResyncResult struct {
	Boundaries int          `json:"boundaries"`
	Cleared    int          `json:"cleared"`
	Stamped    int          `json:"stamped"`
	PerClass   []ClassCount `json:"per_class"`
}

// ownClass is one compiled feature class carrying the ownership column.
type ownClass struct {
	code  string
	table string
}

// MarkDownstreamPrivate declares src private and stamps ownership=private onto
// src plus every feature connectivity-reachable downstream of it. It's the
// one-click "make this and everything below it private" action.
func MarkDownstreamPrivate(ctx context.Context, db bun.IDB, src FeatureRef) (*PropagateResult, error) {
	classes, byCode, err := loadOwnershipClasses(ctx, db)
	if err != nil {
		return nil, err
	}
	if _, ok := byCode[src.FeatureClass]; !ok {
		return nil, invalidf("class %q has no ownership field -- apply seed/ownership_axis.sql and recompile it", src.FeatureClass)
	}

	idsByClass, reached, err := downstreamSet(ctx, db, src, byCode)
	if err != nil {
		return nil, err
	}

	actor := actorParam(auth.ActorID(ctx))
	result := &PropagateResult{Source: src, Reached: reached}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, c := range classes {
			ids := idsByClass[c.code]
			if len(ids) == 0 {
				continue
			}
			n, err := stampPrivate(ctx, tx, c, ids, actor)
			if err != nil {
				return err
			}
			result.Stamped += n
			result.PerClass = append(result.PerClass, ClassCount{FeatureClass: c.code, Stamped: n})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

// MarkDownstreamUtility is the exact inverse of MarkDownstreamPrivate: it clears
// ownership (back to NULL = utility) on src plus every feature connectivity-
// reachable downstream of it. The one-click "this transformer and its network are
// the utility's after all" action, so a private boundary set by mistake -- or a
// customer network taken back over -- can be reverted. (For the rare case where
// some of that downstream is ALSO fed from a DIFFERENT still-private transformer,
// a follow-up Resync re-stamps it correctly from the remaining boundaries.)
func MarkDownstreamUtility(ctx context.Context, db bun.IDB, src FeatureRef) (*PropagateResult, error) {
	classes, byCode, err := loadOwnershipClasses(ctx, db)
	if err != nil {
		return nil, err
	}
	if _, ok := byCode[src.FeatureClass]; !ok {
		return nil, invalidf("class %q has no ownership field -- apply seed/ownership_axis.sql and recompile it", src.FeatureClass)
	}

	idsByClass, reached, err := downstreamSet(ctx, db, src, byCode)
	if err != nil {
		return nil, err
	}

	actor := actorParam(auth.ActorID(ctx))
	result := &PropagateResult{Source: src, Reached: reached}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, c := range classes {
			ids := idsByClass[c.code]
			if len(ids) == 0 {
				continue
			}
			n, err := clearPrivateIDs(ctx, tx, c, ids, actor)
			if err != nil {
				return err
			}
			result.Stamped += n
			result.PerClass = append(result.PerClass, ClassCount{FeatureClass: c.code, Stamped: n})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

// Resync recomputes the whole ownership map from scratch: it reads every
// declared-private boundary (a transformer currently private), clears ownership
// everywhere, then re-stamps each boundary and its downstream set private. This
// is the drift fix for after a re-wire -- membership always equals "what traces
// from a private transformer right now." The clear+stamp is one transaction, so
// a mid-run failure leaves the map exactly as it was.
func Resync(ctx context.Context, db bun.IDB) (*ResyncResult, error) {
	classes, byCode, err := loadOwnershipClasses(ctx, db)
	if err != nil {
		return nil, err
	}

	// 1. The declared boundaries (private transformers), read BEFORE the reset
	//    wipes the information.
	boundaries, err := privateBoundaries(ctx, db, byCode)
	if err != nil {
		return nil, err
	}

	// 2. The full private set = each boundary plus its downstream reach. Traced
	//    outside the tx (connectivity is unaffected by the ownership reset).
	stampByClass := map[string]map[string]struct{}{}
	for _, b := range boundaries {
		idsByClass, _, err := downstreamSet(ctx, db, b, byCode)
		if err != nil {
			return nil, err
		}
		for cls, ids := range idsByClass {
			set := stampByClass[cls]
			if set == nil {
				set = map[string]struct{}{}
				stampByClass[cls] = set
			}
			for _, id := range ids {
				set[id] = struct{}{}
			}
		}
	}

	actor := actorParam(auth.ActorID(ctx))
	result := &ResyncResult{Boundaries: len(boundaries)}
	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, c := range classes {
			n, err := clearPrivate(ctx, tx, c, actor)
			if err != nil {
				return err
			}
			result.Cleared += n
		}
		for _, c := range classes {
			set := stampByClass[c.code]
			if len(set) == 0 {
				continue
			}
			ids := make([]string, 0, len(set))
			for id := range set {
				ids = append(ids, id)
			}
			n, err := stampPrivate(ctx, tx, c, ids, actor)
			if err != nil {
				return err
			}
			result.Stamped += n
			result.PerClass = append(result.PerClass, ClassCount{FeatureClass: c.code, Stamped: n})
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

// downstreamSet traces downstream of src and returns the reached feature ids
// grouped by class (including src itself), plus a reached count (excluding src).
// Only classes that carry the ownership column are kept.
func downstreamSet(ctx context.Context, db bun.IDB, src FeatureRef, byCode map[string]ownClass) (map[string][]string, int, error) {
	cross := true
	traced, err := trace.RunTrace(ctx, db, trace.RunTraceInput{
		TraceConfiguration: ownershipTraceCode,
		Start:              trace.FeatureRef{FeatureClass: src.FeatureClass, FeatureID: src.FeatureID},
		CrossTransformers:  &cross,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("ownership: tracing downstream of %s/%s: %w", src.FeatureClass, src.FeatureID, err)
	}

	idsByClass := map[string][]string{src.FeatureClass: {src.FeatureID}}
	seen := map[string]struct{}{src.FeatureClass + "/" + src.FeatureID: {}}
	reached := 0
	for _, f := range traced.Features {
		if _, ok := byCode[f.FeatureClass]; !ok {
			continue
		}
		key := f.FeatureClass + "/" + f.FeatureID
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		idsByClass[f.FeatureClass] = append(idsByClass[f.FeatureClass], f.FeatureID)
		reached++
	}
	return idsByClass, reached, nil
}

// loadOwnershipClasses returns every compiled feature class that carries an
// active `ownership` field -- both an ordered slice (deterministic output) and a
// by-code map (filtering the traced set). An empty result means the Slice 1 seed
// hasn't been applied/compiled, so it's a client-fixable 400.
func loadOwnershipClasses(ctx context.Context, db bun.IDB) ([]ownClass, map[string]ownClass, error) {
	const query = `
SELECT fc.code, fc.physical_table
FROM metadata.feature_classes fc
WHERE fc.physical_table IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM metadata.fields f
      WHERE f.feature_class_id = fc.id AND f.name = 'ownership' AND f.status = 'active'
  )
ORDER BY fc.code`

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, nil, fmt.Errorf("ownership: loading ownership-bearing classes: %w", err)
	}
	defer rows.Close()

	var list []ownClass
	byCode := map[string]ownClass{}
	for rows.Next() {
		var c ownClass
		if err := rows.Scan(&c.code, &c.table); err != nil {
			return nil, nil, fmt.Errorf("ownership: scanning class: %w", err)
		}
		list = append(list, c)
		byCode[c.code] = c
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("ownership: reading classes: %w", err)
	}
	if len(list) == 0 {
		return nil, nil, invalidf("no compiled class carries an ownership field -- apply seed/ownership_axis.sql and recompile those classes")
	}
	return list, byCode, nil
}

// privateBoundaries lists every transformer feature currently marked private --
// the declared boundaries a re-sync propagates from.
func privateBoundaries(ctx context.Context, db bun.IDB, byCode map[string]ownClass) ([]FeatureRef, error) {
	var out []FeatureRef
	for _, code := range boundaryClasses {
		c, ok := byCode[code]
		if !ok {
			continue // that class isn't seeded/compiled with ownership -- skip
		}
		query := fmt.Sprintf("SELECT id FROM %s WHERE ownership = ? AND deleted_at IS NULL", c.table)
		rows, err := db.QueryContext(ctx, query, ownerPrivate)
		if err != nil {
			return nil, fmt.Errorf("ownership: listing private %s: %w", code, err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, fmt.Errorf("ownership: scanning private %s: %w", code, err)
			}
			out = append(out, FeatureRef{FeatureClass: code, FeatureID: id})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("ownership: reading private %s: %w", code, err)
		}
		rows.Close()
	}
	return out, nil
}

// stampPrivate writes ownership=private onto the given live features of one
// class. Idempotent -- a feature already private is a harmless no-op counted in
// RowsAffected.
func stampPrivate(ctx context.Context, tx bun.IDB, c ownClass, ids []string, actor any) (int, error) {
	query := fmt.Sprintf(
		"UPDATE %s SET ownership = ?, updated_at = now(), updated_by = ? WHERE id IN (?) AND deleted_at IS NULL",
		c.table,
	)
	res, err := tx.ExecContext(ctx, query, ownerPrivate, actor, bun.In(ids))
	if err != nil {
		return 0, fmt.Errorf("ownership: stamping private on %s: %w", c.code, err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// clearPrivateIDs resets ownership to NULL (= utility) on the given live features
// of one class -- the targeted inverse of stampPrivate (MarkDownstreamUtility).
// Idempotent: a feature already utility is a harmless no-op.
func clearPrivateIDs(ctx context.Context, tx bun.IDB, c ownClass, ids []string, actor any) (int, error) {
	query := fmt.Sprintf(
		"UPDATE %s SET ownership = NULL, updated_at = now(), updated_by = ? WHERE id IN (?) AND deleted_at IS NULL",
		c.table,
	)
	res, err := tx.ExecContext(ctx, query, actor, bun.In(ids))
	if err != nil {
		return 0, fmt.Errorf("ownership: clearing ownership on %s: %w", c.code, err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// clearPrivate resets ownership to NULL (= utility) on every live feature of one
// class that currently carries a value. Returns the row count.
func clearPrivate(ctx context.Context, tx bun.IDB, c ownClass, actor any) (int, error) {
	query := fmt.Sprintf(
		"UPDATE %s SET ownership = NULL, updated_at = now(), updated_by = ? WHERE ownership IS NOT NULL AND deleted_at IS NULL",
		c.table,
	)
	res, err := tx.ExecContext(ctx, query, actor)
	if err != nil {
		return 0, fmt.Errorf("ownership: clearing ownership on %s: %w", c.code, err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// actorParam turns the acting user's id into a bind value, or nil when there is
// no session so updated_by is written NULL rather than an empty string (which
// would fail the uuid cast) -- the same rule crud's actorArg and circuit follow.
func actorParam(id string) any {
	if id == "" {
		return nil
	}
	return id
}
