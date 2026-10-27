package connectivity

// topology_audit.go is the loop (mesh) half of the validation framework. A
// distribution network is normally operated radially, so a CLOSED loop with no
// open point is worth flagging -- an accidental double-feed or a ring left closed.
// But two things keep this honest, matching how operators actually think:
//
//   - SWITCH STATE: detection respects device_state (trace.DetectLoopsInScope walks
//     with an OPEN device removed), so a normally-open ring -- radial in operation --
//     is NOT a loop. Only genuinely closed loops reach here.
//   - VOLTAGE POLICY: LV networks are routinely meshed by design, so a loop is only
//     reported where the network is expected radial -- at or above TopologyMeshBelowV
//     (default 1 kV). A ring is classified by its LOWEST-voltage member, so any LV in
//     it makes it an accepted mesh.
//
// Findings ride AuditAll into the SAME ops.validation_issues queue as everything
// else, keyed (fingerprintFor) by the ring's member-id set, so the same ring is one
// row, auto-resolves when broken, and a DELIBERATE mesh can be accepted by setting
// the issue status to "suppressed" (PATCH /connectivity/issues/{id}) -- it is then
// never re-flagged.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/trace"
)

// TopologyMeshBelowV is the voltage (volts) below which a closed loop is treated as
// an accepted mesh and not flagged. LV (< 1 kV) is routinely interconnected, so by
// default only MV-and-up loops are reported. Overridable -- a deployment with radial
// LV can set it to 0 to flag LV loops too (an admin-editable policy is a follow-up).
var TopologyMeshBelowV = 1000.0

// auditTopologyLoops detects closed loops (switch state respected) and reports the
// ones where the network is expected radial. Scoped to the given feature ids
// (resolved to class+id via the registry) or whole-network when empty. Capped at max.
func auditTopologyLoops(ctx context.Context, db bun.IDB, scope []string, max int) ([]AuditFinding, bool, error) {
	refs, err := loopScopeRefs(ctx, db, scope)
	if err != nil {
		return nil, false, err
	}
	rings, truncated, err := trace.DetectLoopsInScope(ctx, db, refs)
	if err != nil {
		return nil, false, err
	}

	out := make([]AuditFinding, 0, len(rings))
	for _, ring := range rings {
		if len(out) >= max {
			return out, true, nil
		}
		// No member voltage -> can't judge (not an electrical loop we understand).
		// Below the threshold -> an accepted LV mesh. Only an at/above-threshold ring
		// where the network is expected radial is a finding.
		if ring.MinVoltageV == nil || *ring.MinVoltageV < TopologyMeshBelowV || len(ring.Features) == 0 {
			continue
		}
		f := AuditFinding{
			Kind:     "topology_loop",
			Severity: "warning",
			Message: fmt.Sprintf("Closed loop of %d features where the network is expected radial -- open a point to break it, or suppress this issue if the ring is intentional.",
				len(ring.Features)),
			LoopKey: loopKeyOf(ring),
		}
		f.FromClass, f.FromID = ring.Features[0].FeatureClass, ring.Features[0].FeatureID
		if len(ring.Features) > 1 {
			f.ToClass, f.ToID = ring.Features[1].FeatureClass, ring.Features[1].FeatureID
		}
		out = append(out, f)
	}
	return out, truncated, nil
}

// loopKeyOf is the ring's stable identity -- its member feature ids sorted + joined
// (the same shape trace.ringKey uses), so the fingerprint maps one ring to one row.
func loopKeyOf(ring trace.LoopRing) string {
	ids := make([]string, len(ring.Features))
	for i, f := range ring.Features {
		ids[i] = f.FeatureID
	}
	sort.Strings(ids)
	return strings.Join(ids, "|")
}

// loopScopeRefs resolves the scoped feature ids to (class, id) refs via the registry
// so DetectLoopsInScope can seed from them. Empty scope -> nil (whole-network scan).
func loopScopeRefs(ctx context.Context, db bun.IDB, scope []string) ([]trace.FeatureRef, error) {
	if len(scope) == 0 {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT feature_class_code, feature_id FROM runtime.feature_registry WHERE feature_id::text IN (?) AND deleted_at IS NULL`,
		bun.In(scope))
	if err != nil {
		return nil, fmt.Errorf("connectivity: resolving loop scope: %w", err)
	}
	defer rows.Close()
	var out []trace.FeatureRef
	for rows.Next() {
		var ref trace.FeatureRef
		if err := rows.Scan(&ref.FeatureClass, &ref.FeatureID); err != nil {
			return nil, fmt.Errorf("connectivity: scanning loop scope ref: %w", err)
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}
