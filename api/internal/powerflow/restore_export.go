package powerflow

import (
	"context"

	"github.com/uptrace/bun"
)

// Exported entrypoint over the contingency (back-feed restoration) engine, for a
// caller outside this package that needs a LOAD-FLOW-validated answer to "can this
// donor feeder pick up the section that closing this tie would re-energize?" --
// specifically the FLISR restoration suggester in api/internal/oms, which uses it
// to replace its nameplate summed-demand estimate with a real sweep. No powerflow
// logic lives here; it only maps exported DTOs onto the engine's internal ones,
// the same thin-wrapper pattern api/internal/trace/export.go uses.

// FeatureRef is the exported form of featureRef (feature class code + id).
type FeatureRef struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
}

// RestorationCheck is the load-flow verdict for closing one tie to back-feed a
// de-energized section from a donor feeder: Feasible is true when the transfer
// restores load with NO post-transfer voltage violation and NO thermal overload --
// a move an operator can make as-is. RestoredLoadKW is the extra demand the donor
// picks up, MinVoltagePu the worst per-unit voltage reached (at WorstNode), and
// Violations/Overloaded the offending nodes and branches. Note explains an
// infeasible/trivial result (a loop/mesh, no load gained, or a tie not on the
// donor feeder). It is the authoritative capacity check behind FLISR's nameplate
// estimate -- an actual sweep, not summed demand.
type RestorationCheck struct {
	Feasible       bool         `json:"feasible"`
	RestoredLoadKW float64      `json:"restored_load_kw"`
	MinVoltagePu   float64      `json:"min_voltage_pu"`
	WorstNode      *FeatureRef  `json:"worst_node,omitempty"`
	Violations     []FeatureRef `json:"violations"`
	Overloaded     []FeatureRef `json:"overloaded"`
	Note           string       `json:"note,omitempty"`
}

// EvaluateRestorationTie runs the contingency engine for ONE explicit tie: it
// sweeps the donor feeder from source (at nominalV sending-end voltage), re-sweeps
// it with the tie hypothetically closed (no connectivity is mutated), and reports
// whether the donor can pick up the section that closing the tie re-energizes
// without a voltage violation or overload. A loop/mesh, or a tie that isn't an open
// boundary on the donor feeder, comes back as an infeasible check with an
// explanatory Note rather than an error. Read-only.
func EvaluateRestorationTie(ctx context.Context, db bun.IDB, source, tie FeatureRef, nominalV float64) (*RestorationCheck, error) {
	resp, err := runContingency(ctx, db, contingencyRequest{
		Source:          featureRef{FeatureClass: source.FeatureClass, FeatureID: source.FeatureID},
		NominalVoltageV: nominalV,
		Tie:             &featureRef{FeatureClass: tie.FeatureClass, FeatureID: tie.FeatureID},
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Options) == 0 {
		return &RestorationCheck{
			Violations: []FeatureRef{},
			Overloaded: []FeatureRef{},
			Note:       "tie could not be evaluated against this feeder",
		}, nil
	}
	opt := resp.Options[0]
	return &RestorationCheck{
		Feasible:       opt.Feasible,
		RestoredLoadKW: opt.RestoredLoadKW,
		MinVoltagePu:   opt.MinVoltagePu,
		WorstNode:      restorationExportedRefPtr(opt.WorstNode),
		Violations:     restorationExportedRefs(opt.Violations),
		Overloaded:     restorationExportedRefs(opt.Overloaded),
		Note:           opt.Note,
	}, nil
}

func restorationExportedRefs(rs []featureRef) []FeatureRef {
	out := make([]FeatureRef, len(rs))
	for i, r := range rs {
		out[i] = FeatureRef{FeatureClass: r.FeatureClass, FeatureID: r.FeatureID}
	}
	return out
}

func restorationExportedRefPtr(r *featureRef) *FeatureRef {
	if r == nil {
		return nil
	}
	return &FeatureRef{FeatureClass: r.FeatureClass, FeatureID: r.FeatureID}
}
