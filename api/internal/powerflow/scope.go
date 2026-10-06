package powerflow

import (
	"context"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/uptrace/bun"
)

// scope.go is the Phase 58 START gate for the analysis engine. By product
// decision it is deliberately NARROWER than api/internal/trace's gate: an
// analysis may not be STARTED from a feature outside a scoped user's territories
// -- that would be a direct out-of-scope read of the feature the user named --
// but the per-node RESULTS of a legitimately in-scope start are NOT filtered.
// An engineering study (load flow, fault, coordination, ...) is meant to see the
// whole electrically-connected feeder, which can cross territory boundaries by
// design; filtering the far nodes would silently corrupt the physics (a sweep
// over a filtered subtree understates demand) rather than protect anything the
// start gate hasn't already. So we gate the entry feature and let the walk run.
//
// Like the CRUD read enforcement and the trace gate, the whole thing is a no-op
// unless territory enforcement is switched on AND the caller is genuinely scoped
// -- a session-less internal caller (OMS, the assistant) is never gated, and a
// territories:unscoped user bypasses it.

// enforceScope refuses an out-of-scope START feature for a scoped user, given the
// feature's ALREADY-LOADED class and row -- so it adds no extra feature read on
// top of the resolveNode / ampacity read the caller already did. It returns a
// notFound (the same answer GET gives for a feature outside the user's
// territories) when the feature is geometric, carries a territory_id, and that id
// is not in the user's allowed set; nil in every bypass case:
//   - enforcement off, no session, or a territories:unscoped user;
//   - a geometry-less class (relay, panel, geomless MV device) -- it carries no
//     territory_id column, so there is nothing to scope, exactly as the trace
//     gate treats such a class.
//
// An unassigned (NULL/"" territory_id) geometric feature is NOT visible to a
// scoped user under Phase 58's fail-closed rule -- same as traceScope.permits.
func enforceScope(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, row map[string]any, ref featureRef) error {
	enforced, err := territory.EnforcementEnabled(ctx, db)
	if err != nil {
		return err
	}
	if !enforced {
		return nil
	}
	user, ok := auth.UserFrom(ctx)
	if !ok {
		return nil
	}
	allowedIDs, unscoped := user.TerritoryFilter()
	if unscoped {
		return nil
	}
	// A geometry-less class carries no territory -- territories scope geographic
	// features only, and blocking the non-geographic devices an analysis naturally
	// starts near or passes would break legitimate studies.
	if loaded.FeatureClass.GeometryType == "none" {
		return nil
	}
	// formatRow surfaces territory_id at the row's top level for a geometric class;
	// scanRows normalizes the uuid to a string.
	tid, _ := row["territory_id"].(string)
	if tid != "" {
		for _, id := range allowedIDs {
			if id == tid {
				return nil
			}
		}
	}
	return notFound("feature %s (%s) not found", ref.FeatureID, ref.FeatureClass)
}
