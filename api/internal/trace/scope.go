package trace

import (
	"context"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/uptrace/bun"
)

// This file is the Phase 58 spatial gate for the trace engine. A scoped user
// may not trace from -- or see in a trace result -- a feature outside their
// territory set. The gate enforces that at two points in runTrace: the start
// (and shortest-path target) must be in scope, and every node in the response
// (features, barriers, targets) is filtered to the in-scope set. The BFS itself
// is left untouched; traversal may pass THROUGH an out-of-scope feature, but
// none is ever surfaced, so no feature outside the user's territories is
// disclosed.
//
// Like the CRUD read enforcement, the whole gate is a no-op unless enforcement
// is switched on and the user is genuinely scoped (see resolveTraceScope) -- so
// with the flag off the trace engine behaves exactly as before this phase.

// traceScope is the resolved spatial gate for one trace run.
type traceScope struct {
	apply   bool                // false => no enforcement; permits always true
	allowed map[string]struct{} // the user's effective territory ids (empty => nothing)
}

// resolveTraceScope reads the acting request's spatial scope once per trace.
// apply is false -- meaning permit everything -- when enforcement is off, the
// user bypasses scoping (territories:unscoped), or there is no session (an
// internal caller). Otherwise apply is true and allowed is the user's effective
// territory set; an EMPTY set is the fail-closed case (the user sees nothing).
func resolveTraceScope(ctx context.Context, db bun.IDB) (traceScope, error) {
	enforced, err := territory.EnforcementEnabled(ctx, db)
	if err != nil {
		return traceScope{}, err
	}
	if !enforced {
		return traceScope{}, nil
	}
	user, ok := auth.UserFrom(ctx)
	if !ok {
		return traceScope{}, nil
	}
	ids, unscoped := user.TerritoryFilter()
	if unscoped {
		return traceScope{}, nil
	}
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return traceScope{apply: true, allowed: set}, nil
}

// enforceScopeStart resolves the spatial scope for a trace run and verifies the
// start node is in scope, returning the ready traceScope for the caller to then
// filter its result sets with. It is the synthetic traces' (feeder / section /
// supply-path / service / isolate) shared equivalent of the start-gate runTrace
// applies inline: all of them must refuse an out-of-scope start the same way --
// a notFound, identical to what GET gives for a feature outside the user's
// territories -- or they would be a Phase 58 bypass that discloses features the
// plain /trace endpoint hides. A no-op (scope.apply false) unless enforcement is
// on and the caller is genuinely scoped.
func enforceScopeStart(ctx context.Context, db bun.IDB, loader *featureClassLoader, start node, startRef featureRef) (traceScope, error) {
	scope, err := resolveTraceScope(ctx, db)
	if err != nil {
		return traceScope{}, err
	}
	if scope.apply {
		ok, err := scope.permits(ctx, db, start, loader)
		if err != nil {
			return traceScope{}, err
		}
		if !ok {
			return traceScope{}, notFound("feature %s (%s) not found", startRef.FeatureID, startRef.FeatureClass)
		}
	}
	return scope, nil
}

// permits reports whether one node is visible to the scoped user. A
// geometry-less class (a relay, a panel: no territory_id column) carries no
// territory and is always permitted -- territories scope geographic features
// only, and blocking the non-geographic devices a trace naturally passes
// through would break every trace. A geometric feature is permitted only when
// its territory_id is in the allowed set; an unassigned (NULL territory_id)
// feature is not visible to a scoped user under fail-closed. A since-purged
// feature (dangling connectivity row) is treated as not permitted -- it's a
// dead end either way. Reuses the shared class-loader so the read is cached.
func (s traceScope) permits(ctx context.Context, db bun.IDB, n node, loader *featureClassLoader) (bool, error) {
	if !s.apply {
		return true, nil
	}
	loaded, err := loader.loadedFor(ctx, db, n.classID)
	if err != nil {
		return false, err
	}
	if loaded.FeatureClass.GeometryType == "none" {
		return true, nil
	}
	row, err := crud.GetFeature(ctx, db, loaded, n.featureID)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return false, nil
		}
		return false, err
	}
	// formatRow surfaces territory_id at the row's top level for a geometric
	// class; scanRows normalizes the uuid to a string.
	tid, _ := row["territory_id"].(string)
	if tid == "" {
		return false, nil
	}
	_, ok := s.allowed[tid]
	return ok, nil
}

// filter keeps only the in-scope nodes of a set, preserving order. Used on the
// response node lists (features, barriers, targets) so an out-of-scope feature
// a traversal passed through is never returned.
func (s traceScope) filter(ctx context.Context, db bun.IDB, nodes []node, loader *featureClassLoader) ([]node, error) {
	if !s.apply {
		return nodes, nil
	}
	out := make([]node, 0, len(nodes))
	for _, n := range nodes {
		ok, err := s.permits(ctx, db, n, loader)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, n)
		}
	}
	return out, nil
}
