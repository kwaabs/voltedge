package crud

// lifecycle.go is the asset-lifecycle axis -- where a physical asset sits in its
// life, as a system column (lifecycle_status + commissioned_at/retired_at) the
// compiler puts on EVERY feature table (buildCreateTable / the compiler's
// ensure-columns path). It is a distinct axis from the editorial `status`
// (draft/published) and the network `boundary_role`: an asset can be a published
// draft that is still only PLANNED, or IN_SERVICE and privately staged.
//
// Lifecycle changes flow through SetLifecycle (a dedicated transition path),
// NOT the generic attribute editor -- a status change is a deliberate, auditable
// operational act, and keeping it separate lets the network gate (trace & power
// flow) and the aging report read one authoritative column. New rows default to
// IN_SERVICE via the column DEFAULT, so create needn't thread it.

import (
	"context"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// The coded lifecycle states, in life order. Kept in lockstep with the CHECK
// constraint buildCreateTable / the compiler's ensure path write onto the column.
const (
	LifecyclePlanned           = "PLANNED"
	LifecycleUnderConstruction = "UNDER_CONSTRUCTION"
	LifecycleInService         = "IN_SERVICE"
	LifecycleDerated           = "DERATED"
	LifecycleRetired           = "RETIRED"
	LifecycleRemoved           = "REMOVED"
)

// lifecycleStatuses is the full set, for input validation and the FE value list.
var lifecycleStatuses = []string{
	LifecyclePlanned, LifecycleUnderConstruction, LifecycleInService,
	LifecycleDerated, LifecycleRetired, LifecycleRemoved,
}

// lifecycleOperationalStatuses are the states in which an asset CONDUCTS -- it
// carries current and participates in trace & power flow. A DERATED asset is
// still energized (it has a reduced rating, not an open point), so it conducts;
// a PLANNED / UNDER_CONSTRUCTION asset isn't built yet and a RETIRED / REMOVED
// one is out of service, so none of those conduct. S2's network gate ANDs
// LifecycleOperationalClause into its neighbour queries.
var lifecycleOperationalStatuses = []string{LifecycleInService, LifecycleDerated}

// lifecycleDesignedStatuses are the AS-DESIGNED network: the operational states
// PLUS the not-yet-built PLANNED / UNDER_CONSTRUCTION. RETIRED / REMOVED stay
// excluded -- they're gone, not planned. The as-designed trace/power-flow scenario
// uses this set so an engineer can analyse the network as it WILL be, not only as
// it is operated today.
var lifecycleDesignedStatuses = []string{
	LifecyclePlanned, LifecycleUnderConstruction, LifecycleInService, LifecycleDerated,
}

// scenarioKey carries the trace/power-flow scenario on a request context.
type scenarioKey struct{}

// WithAsDesigned marks ctx for the AS-DESIGNED scenario (include PLANNED /
// UNDER_CONSTRUCTION in the conducting network). An unmarked ctx is AS-OPERATED --
// only the currently energised network. The trace/power-flow handlers set this from
// the request; LifecycleClauseCtx reads it.
func WithAsDesigned(ctx context.Context, asDesigned bool) context.Context {
	return context.WithValue(ctx, scenarioKey{}, asDesigned)
}

func asDesignedFrom(ctx context.Context) bool {
	v, _ := ctx.Value(scenarioKey{}).(bool)
	return v
}

// LifecycleStatuses returns the coded lifecycle value set (life order), for the
// schema/catalog surfaces that need to offer the choices.
func LifecycleStatuses() []string { return append([]string(nil), lifecycleStatuses...) }

func isLifecycleStatus(s string) bool {
	for _, v := range lifecycleStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// lifecycleClause builds a SQL boolean predicate for an aliased feature table
// (alias "" = unqualified): lifecycle_status IN (<statuses>). The value list comes
// from fixed internal constants, so there is no injection surface.
func lifecycleClause(alias string, statuses []string) string {
	col := "lifecycle_status"
	if alias != "" {
		col = alias + ".lifecycle_status"
	}
	quoted := make([]string, len(statuses))
	for i, s := range statuses {
		quoted[i] = "'" + s + "'"
	}
	return fmt.Sprintf("%s IN (%s)", col, strings.Join(quoted, ", "))
}

// LifecycleOperationalClause is the AS-OPERATED network predicate (IN_SERVICE /
// DERATED only), for callers that always mean the operated network regardless of
// scenario.
func LifecycleOperationalClause(alias string) string {
	return lifecycleClause(alias, lifecycleOperationalStatuses)
}

// LifecycleClauseCtx is the scenario-aware network predicate the trace & power-flow
// neighbour queries use: AS-OPERATED (IN_SERVICE/DERATED) by default, or AS-DESIGNED
// (also PLANNED/UNDER_CONSTRUCTION) when ctx is marked WithAsDesigned. RETIRED /
// REMOVED are excluded either way.
func LifecycleClauseCtx(ctx context.Context, alias string) string {
	statuses := lifecycleOperationalStatuses
	if asDesignedFrom(ctx) {
		statuses = lifecycleDesignedStatuses
	}
	return lifecycleClause(alias, statuses)
}

// LifecycleInput is the transition request: any field left nil is unchanged.
// A date can be cleared with the matching Clear* flag (distinct from "leave as
// is", which a nil pointer means). Dates are ISO "YYYY-MM-DD".
type LifecycleInput struct {
	Status            *string `json:"lifecycle_status,omitempty"`
	CommissionedAt    *string `json:"commissioned_at,omitempty"`
	RetiredAt         *string `json:"retired_at,omitempty"`
	ClearCommissioned bool    `json:"clear_commissioned_at,omitempty"`
	ClearRetired      bool    `json:"clear_retired_at,omitempty"`
}

// SetLifecycle moves a feature along its lifecycle: set the status and/or the
// commission/retire dates in one write. It mirrors setDeletedAt's shape -- same
// read/draft scope gates, same audit-history + event emission -- because a
// lifecycle change is an edit of the same kind. Ergonomic auto-stamps: moving
// INTO service stamps commissioned_at if still unset (and not explicitly given),
// and moving to retired/removed stamps retired_at likewise, so the common path
// is a single status pick.
func SetLifecycle(ctx context.Context, db bun.IDB, loaded *metadata.Loaded, id string, in LifecycleInput) (map[string]any, error) {
	current, err := getFeature(ctx, db, loaded, id)
	if err != nil {
		return nil, err
	}
	// Same gates a delete uses: a scoped user can't touch a feature outside their
	// territories (surfaced as not-found), and a peer can't touch a private draft.
	if err := enforceReadScope(ctx, db, loaded, current); err != nil {
		return nil, err
	}
	if err := enforceDraftScope(ctx, db, current); err != nil {
		return nil, err
	}
	if current["deleted_at"] != nil {
		return nil, invalid("feature %s is deleted -- restore it before changing its lifecycle", id)
	}

	var setParts []string
	var args []any

	if in.Status != nil {
		s := strings.ToUpper(strings.TrimSpace(*in.Status))
		if !isLifecycleStatus(s) {
			return nil, invalid("lifecycle_status must be one of %s", strings.Join(lifecycleStatuses, ", "))
		}
		setParts = append(setParts, "lifecycle_status = ?")
		args = append(args, s)

		// Auto-stamp the matching date on a transition, only when the caller
		// didn't set/clear it themselves and the row has none yet.
		if s == LifecycleInService && current["commissioned_at"] == nil && in.CommissionedAt == nil && !in.ClearCommissioned {
			setParts = append(setParts, "commissioned_at = now()::date")
		}
		if (s == LifecycleRetired || s == LifecycleRemoved) && current["retired_at"] == nil && in.RetiredAt == nil && !in.ClearRetired {
			setParts = append(setParts, "retired_at = now()::date")
		}
	}

	switch {
	case in.ClearCommissioned:
		setParts = append(setParts, "commissioned_at = NULL")
	case in.CommissionedAt != nil:
		setParts = append(setParts, "commissioned_at = ?::date")
		args = append(args, *in.CommissionedAt)
	}
	switch {
	case in.ClearRetired:
		setParts = append(setParts, "retired_at = NULL")
	case in.RetiredAt != nil:
		setParts = append(setParts, "retired_at = ?::date")
		args = append(args, *in.RetiredAt)
	}

	if len(setParts) == 0 {
		return current, nil // nothing asked to change
	}

	table, err := physicalTable(loaded.FeatureClass)
	if err != nil {
		return nil, err
	}

	// A lifecycle change is an edit -- stamp the actor, same as setDeletedAt.
	setParts = append(setParts, "updated_at = now()", "updated_by = ?")
	args = append(args, actorArg(ctx))
	args = append(args, id) // WHERE id = ?

	query := fmt.Sprintf(
		"UPDATE %s SET %s WHERE id = ? RETURNING %s",
		table, strings.Join(setParts, ", "), strings.Join(selectColumns(loaded), ", "),
	)
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("crud: set lifecycle: %w", err)
	}
	results, err := scanRows(rows)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, notFound("feature %s not found", id)
	}
	out, err := formatRow(loaded, results[0])
	if err != nil {
		return nil, err
	}
	if err := recordFeatureHistory(ctx, db, loaded, historyUpdate, out); err != nil {
		return nil, err
	}
	if err := emitFeatureEvent(ctx, db, loaded, eventFeatureUpdated, out); err != nil {
		return nil, err
	}
	// Append to the lifecycle-events ledger when the STATUS actually moved (a
	// date-only edit isn't a transition). from = the pre-update value read above.
	if in.Status != nil {
		to := strings.ToUpper(strings.TrimSpace(*in.Status))
		from, _ := current["lifecycle_status"].(string)
		if to != from {
			if err := recordLifecycleEvent(ctx, db, loaded.FeatureClass.Code, id, from, to); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// recordLifecycleEvent appends one transition to the ops.lifecycle_events ledger
// (migration 0184), on the same bun.IDB as the feature write so it commits with it.
// A blank from (the feature had no prior value read) is stored NULL.
func recordLifecycleEvent(ctx context.Context, db bun.IDB, classCode, featureID, fromStatus, toStatus string) error {
	var from any
	if fromStatus != "" {
		from = fromStatus
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO ops.lifecycle_events (feature_class_code, feature_id, from_status, to_status, actor_id)
		 VALUES (?, ?, ?, ?, ?)`,
		classCode, featureID, from, toStatus, actorArg(ctx),
	); err != nil {
		return fmt.Errorf("crud: recording lifecycle event: %w", err)
	}
	return nil
}
