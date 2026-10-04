package circuit

import (
	"context"
	"fmt"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/trace"
	"github.com/uptrace/bun"
)

// downstreamTraceCode is the stable metadata.trace_configurations code assign
// walks from a circuit's source feature (seed/trace_configurations_example.sql).
// A DOWNSTREAM trace stops at open devices. Note its cross_transformers=false
// is NOT enforced by the engine yet (the flag is "accepted but not read" --
// see the seed comment / docs/phase-4-trace-engine.md), so the walk CAN cross a
// DIST_XFMR into the LV network. Keeping an MV circuit off LV features is
// therefore done by the stamp step's same-network class filter (loadScopeClasses
// + the byCode filter below), not by the trace -- that filter is load-bearing.
const downstreamTraceCode = "DOWNSTREAM_TRACE"

// AssignResult reports what one assign run did: how many features the trace
// reached (within the circuit's network), how many rows were freshly stamped,
// and how many prior members were cleared by the full sync.
type AssignResult struct {
	CircuitID   string             `json:"circuit_id"`
	CircuitName string             `json:"circuit_name"`
	Network     string             `json:"network"`
	Reached     int                `json:"reached"`
	Stamped     int                `json:"stamped"`
	Cleared     int                `json:"cleared"`
	PerClass    []AssignClassCount `json:"per_class"`
}

// AssignClassCount is how many features of one class the run stamped.
type AssignClassCount struct {
	FeatureClass string `json:"feature_class"`
	Stamped      int    `json:"stamped"`
}

// scopeClass is one feature class that can hold a given circuit: it has an
// active circuit_id field, its network matches the circuit's, and it has been
// compiled (a physical table exists). hasRef records whether it additionally
// carries the denormalized circuit_ref column (only the two conductor classes
// do, from Phase 35) so the stamp writes the circuit name there too.
type scopeClass struct {
	code   string
	table  string
	hasRef bool
}

// AssignCircuit stamps circuit membership by tracing downstream from the
// circuit's source feature and bulk-writing circuit_id (and, where the column
// exists, circuit_ref = the circuit name) onto every feature reached. This is
// the first bulk feature write in the codebase; every other feature write is
// single-row.
//
// Membership is a full sync (the user's decision): each run first clears this
// circuit off every in-scope class, then stamps the freshly-traced set -- so a
// feature that no longer traces from the source (e.g. a switch was opened) is
// dropped, and membership always equals "what traces from the source right
// now." A manual override on a dropped feature is therefore also cleared; hand
// edits are for one-off corrections, not a durable pin against a re-run.
//
// The whole clear+stamp runs in one transaction: a mid-run failure leaves
// membership exactly as it was rather than half-synced.
func AssignCircuit(ctx context.Context, db *bun.DB, circuitID string) (*AssignResult, error) {
	c, err := GetCircuit(ctx, db, circuitID)
	if err != nil {
		return nil, err
	}
	if c.SourceFeatureClass == nil || c.SourceFeatureID == nil {
		return nil, invalidf("circuit %q has no source feature to trace from -- set its source first", c.Name)
	}

	// Everything downstream of the source, across every class it reaches. The
	// engine is read-only; the write is entirely below.
	traced, err := trace.RunTrace(ctx, db, trace.RunTraceInput{
		TraceConfiguration: downstreamTraceCode,
		Start: trace.FeatureRef{
			FeatureClass: *c.SourceFeatureClass,
			FeatureID:    *c.SourceFeatureID,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("circuit: tracing from source of %q: %w", c.Name, err)
	}

	// The classes that may carry this circuit: same network, has circuit_id,
	// compiled. Ordered for deterministic per-class output; the map is the
	// lookup used to filter the traced set.
	scope, byCode, err := loadScopeClasses(ctx, db, c.Network)
	if err != nil {
		return nil, err
	}

	// Group reached feature ids by class, dropping anything outside the
	// circuit's network -- an MV trace that somehow crossed into LV must never
	// stamp an MV circuit onto an LV feature.
	idsByClass := map[string][]string{}
	reached := 0
	for _, f := range traced.Features {
		if _, ok := byCode[f.FeatureClass]; !ok {
			continue
		}
		idsByClass[f.FeatureClass] = append(idsByClass[f.FeatureClass], f.FeatureID)
		reached++
	}

	actor := actorParam(auth.ActorID(ctx))
	result := &AssignResult{
		CircuitID:   c.ID,
		CircuitName: c.Name,
		Network:     c.Network,
		Reached:     reached,
	}

	if err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Full sync: clear this circuit off every in-scope class first (drops
		// orphans in any class), then stamp the reached set. A feature present
		// in both is cleared then re-stamped -- net unchanged.
		for _, sc := range scope {
			n, err := clearCircuit(ctx, tx, sc, c.ID, actor)
			if err != nil {
				return err
			}
			result.Cleared += n
		}
		for _, sc := range scope {
			ids := idsByClass[sc.code]
			if len(ids) == 0 {
				continue
			}
			n, err := stampCircuit(ctx, tx, sc, c, ids, actor)
			if err != nil {
				return err
			}
			result.Stamped += n
			result.PerClass = append(result.PerClass, AssignClassCount{FeatureClass: sc.code, Stamped: n})
		}
		// Record the sync point (migration 0130). now() is the transaction
		// timestamp, the SAME value stampCircuit just wrote to every member's
		// updated_at -- so the freshness check (member.updated_at > this) is false
		// for a just-assigned circuit and only a LATER edit trips it.
		if _, err := tx.ExecContext(ctx,
			"UPDATE gis.circuits SET members_synced_at = now(), updated_at = now() WHERE id = ?", c.ID); err != nil {
			return fmt.Errorf("circuit: recording sync point for %q: %w", c.Name, err)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

// applyStaleness populates each circuit's computed Stale flag (migration 0130): a
// circuit is stale when any member feature has been edited since members_synced_at.
// One grouped MAX(updated_at)-per-circuit query per circuit-bearing class
// (O(classes), NOT O(circuits)); the newest member edit per circuit across every
// class is compared to its sync point. A never-assigned circuit (nil
// members_synced_at) is never stale. Best-effort: it catches member edits / moves
// / soft-deletes; a pure connect/disconnect that touches no member row is the
// known blind spot (documented) -- re-assigning is always safe regardless.
func applyStaleness(ctx context.Context, db bun.IDB, circuits []Circuit) error {
	if len(circuits) == 0 {
		return nil
	}
	latest := map[string]time.Time{}
	for _, network := range []string{NetworkMV, NetworkLV} {
		scope, _, err := loadScopeClasses(ctx, db, network)
		if err != nil {
			if IsInvalid(err) {
				continue // no circuit-bearing classes compiled for this network yet
			}
			return err
		}
		for _, sc := range scope {
			query := fmt.Sprintf(
				"SELECT circuit_id, MAX(updated_at) FROM %s WHERE circuit_id IS NOT NULL AND deleted_at IS NULL GROUP BY circuit_id",
				sc.table,
			)
			rows, err := db.QueryContext(ctx, query)
			if err != nil {
				return fmt.Errorf("circuit: computing staleness on %s: %w", sc.code, err)
			}
			for rows.Next() {
				var cid string
				var mx time.Time
				if err := rows.Scan(&cid, &mx); err != nil {
					rows.Close()
					return fmt.Errorf("circuit: scanning staleness on %s: %w", sc.code, err)
				}
				if cur, ok := latest[cid]; !ok || mx.After(cur) {
					latest[cid] = mx
				}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return fmt.Errorf("circuit: reading staleness on %s: %w", sc.code, err)
			}
			rows.Close()
		}
	}
	for i := range circuits {
		c := &circuits[i]
		if c.MembersSyncedAt == nil {
			continue
		}
		if mx, ok := latest[c.ID]; ok && mx.After(*c.MembersSyncedAt) {
			c.Stale = true
		}
	}
	return nil
}

// loadScopeClasses returns every compiled feature class in the given network
// that carries an active circuit_id field, plus (per class) whether it also
// carries circuit_ref. Returned both as an ordered slice (deterministic output)
// and a by-code map (filtering the traced set). An empty result is a
// misconfiguration -- the Phase 37 Slice 2 seed/recompile hasn't run -- so it's
// a client-fixable 400 rather than a silent no-op.
func loadScopeClasses(ctx context.Context, db bun.IDB, network string) ([]scopeClass, map[string]scopeClass, error) {
	const query = `
SELECT fc.code, fc.physical_table,
       EXISTS (
           SELECT 1 FROM metadata.fields rf
           WHERE rf.feature_class_id = fc.id AND rf.name = 'circuit_ref' AND rf.status = 'active'
       ) AS has_ref
FROM metadata.feature_classes fc
WHERE fc.network = ?
  AND fc.physical_table IS NOT NULL
  AND EXISTS (
      SELECT 1 FROM metadata.fields f
      WHERE f.feature_class_id = fc.id AND f.name = 'circuit_id' AND f.status = 'active'
  )
ORDER BY fc.code`

	rows, err := db.QueryContext(ctx, query, network)
	if err != nil {
		return nil, nil, fmt.Errorf("circuit: loading assignable %s classes: %w", network, err)
	}
	defer rows.Close()

	var list []scopeClass
	byCode := map[string]scopeClass{}
	for rows.Next() {
		var sc scopeClass
		if err := rows.Scan(&sc.code, &sc.table, &sc.hasRef); err != nil {
			return nil, nil, fmt.Errorf("circuit: scanning assignable class: %w", err)
		}
		list = append(list, sc)
		byCode[sc.code] = sc
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("circuit: reading assignable classes: %w", err)
	}
	if len(list) == 0 {
		return nil, nil, invalidf("no compiled %s classes carry a circuit_id field -- apply the Phase 37 Slice 2 seed and recompile those classes", network)
	}
	return list, byCode, nil
}

// clearCircuit removes this circuit from every live feature of one class that
// currently carries it, clearing circuit_ref too where that column exists so
// the denormalized name never outlives the id. Returns the row count.
func clearCircuit(ctx context.Context, tx bun.IDB, sc scopeClass, circuitID string, actor any) (int, error) {
	set := "circuit_id = NULL"
	if sc.hasRef {
		set += ", circuit_ref = NULL"
	}
	query := fmt.Sprintf(
		"UPDATE %s SET %s, updated_at = now(), updated_by = ? WHERE circuit_id = ? AND deleted_at IS NULL",
		sc.table, set,
	)
	res, err := tx.ExecContext(ctx, query, actor, circuitID)
	if err != nil {
		return 0, fmt.Errorf("circuit: clearing membership on %s: %w", sc.code, err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// stampCircuit writes this circuit onto the given live features of one class,
// setting circuit_id and (where the column exists) the denormalized
// circuit_ref = the circuit name, so the map label works without resolving the
// uuid per tile. Returns the row count. ids come from the trace, so they are
// this class's members; bun.In expands the set into one UPDATE.
func stampCircuit(ctx context.Context, tx bun.IDB, sc scopeClass, c *Circuit, ids []string, actor any) (int, error) {
	set := "circuit_id = ?"
	args := []any{c.ID}
	if sc.hasRef {
		set += ", circuit_ref = ?"
		args = append(args, c.Name)
	}
	query := fmt.Sprintf(
		"UPDATE %s SET %s, updated_at = now(), updated_by = ? WHERE id IN (?) AND deleted_at IS NULL",
		sc.table, set,
	)
	args = append(args, actor, bun.In(ids))
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("circuit: stamping membership on %s: %w", sc.code, err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// actorParam turns the acting user's id into a bind value, or nil when there is
// no session so updated_by is written NULL rather than an empty string (which
// would fail the uuid cast) -- the same rule crud's actorArg follows.
func actorParam(id string) any {
	if id == "" {
		return nil
	}
	return id
}
