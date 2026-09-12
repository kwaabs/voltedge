package powerflow

import (
	"context"

	"github.com/uptrace/bun"
)

// Default voltage-limit band, per-unit of each node's own nominal base:
// ANSI C84.1 Range A service voltage (±5%). A caller can override either
// bound in the request; these apply only when a bound is left unset. The
// band is what the report flags "within limits" against and what drives
// the violation list -- it does NOT change the solve, only how the same
// swept voltages are judged.
const (
	defaultMinVoltagePu = 0.95
	defaultMaxVoltagePu = 1.05
)

// voltageProfileRequest is POST /analysis/voltage-profile's body. Start
// and NominalVoltageV mean exactly what they do for load-flow (this
// report runs the same sweep). MinVoltagePu/MaxVoltagePu are the optional
// acceptable voltage band each node's per-unit voltage is judged against;
// left nil they default to ANSI C84.1 Range A (0.95..1.05). Pointers so
// "not supplied" is distinct from a deliberate 0.
type voltageProfileRequest struct {
	Start           featureRef `json:"start"`
	NominalVoltageV float64    `json:"nominal_voltage_v"`
	MinVoltagePu    *float64   `json:"min_voltage_pu"`
	MaxVoltagePu    *float64   `json:"max_voltage_pu"`
}

// profileNode is one node's entry in the voltage-profile report.
// VoltagePu is VoltageV / NominalBaseV -- the normalized figure a planner
// compares across MV and LV without the transformer step confusing the
// picture (an LV node reads ~1.0 pu of its own LV base, not ~0.004 pu of
// the MV nominal). WithinLimits is VoltagePu inside the request band.
// BranchLossKW / LengthM / CurrentAmps are set only for a conductor
// (network_role = 'edge') node: the series I²R loss this one branch
// dissipates, its geometry length, and the current through it. They are
// the same single-voltage-basis figures the rest of the engine reports
// (see the package doc): if the caller supplied three-phase P on a
// line-to-line base, the losses are the matching single-phase-equivalent
// figure, consistent with load-flow's own currents.
type profileNode struct {
	FeatureClass   string   `json:"feature_class"`
	FeatureID      string   `json:"feature_id"`
	CircuitID      string   `json:"circuit_id,omitempty"`
	CircuitRef     string   `json:"circuit_ref,omitempty"`
	VoltageV       float64  `json:"voltage_v"`
	NominalBaseV   float64  `json:"nominal_base_v"`
	VoltagePu      float64  `json:"voltage_pu"`
	VoltageDropPct float64  `json:"voltage_drop_pct"`
	WithinLimits   bool     `json:"within_limits"`
	BranchLossKW   *float64 `json:"branch_loss_kw,omitempty"`
	LengthM        *float64 `json:"length_m,omitempty"`
	CurrentAmps    *float64 `json:"current_amps,omitempty"`
}

// profileSummary is the feeder-level roll-up. TotalLoadKW/KVAR is the
// whole tree's demand (the start node's cumulative). TotalLossKW is the
// sum of every branch's I²R series loss; LossPct is that as a percentage
// of TotalLoadKW (0 when there is no load). MinVoltagePu is the worst
// (lowest) per-unit voltage anywhere in the tree, at WorstNode. Losses
// are a lower bound when LossesComplete is false -- some branch on the
// tree had no resolved R/X so its (non-zero) loss couldn't be counted,
// exactly the MaterialResolved caveat load-flow already reports per node.
type profileSummary struct {
	TotalLoadKW    float64     `json:"total_load_kw"`
	TotalLoadKVAR  float64     `json:"total_load_kvar"`
	TotalLossKW    float64     `json:"total_loss_kw"`
	LossPct        float64     `json:"loss_pct"`
	MinVoltagePu   float64     `json:"min_voltage_pu"`
	WorstNode      *featureRef `json:"worst_node,omitempty"`
	ViolationCount int         `json:"violation_count"`
	LossesComplete bool        `json:"losses_complete"`
}

// voltageProfileResponse is the full report: the per-node profile, the
// subset outside the voltage band, the feeder summary, and the barrier
// hits (same meaning as load-flow's).
type voltageProfileResponse struct {
	Start           featureRef    `json:"start"`
	NominalVoltageV float64       `json:"nominal_voltage_v"`
	MinVoltagePu    float64       `json:"min_voltage_pu"`
	MaxVoltagePu    float64       `json:"max_voltage_pu"`
	Nodes           []profileNode `json:"nodes"`
	Violations      []featureRef  `json:"violations"`
	Summary         profileSummary `json:"summary"`
	Barriers        []featureRef  `json:"barriers"`
}

// runVoltageProfile is POST /analysis/voltage-profile: run the shared
// radial sweep, then read a voltage-profile and a series-loss report off
// the swept states. It computes nothing electrical the sweep didn't
// already -- per-branch current and impedance are both in the swept
// state; this only sums I²R and judges each node's per-unit voltage
// against the requested band. Pure add-on: the sweep is byte-for-byte the
// load-flow solve.
func runVoltageProfile(ctx context.Context, db bun.IDB, req voltageProfileRequest) (*voltageProfileResponse, error) {
	minPu := defaultMinVoltagePu
	if req.MinVoltagePu != nil {
		minPu = *req.MinVoltagePu
	}
	maxPu := defaultMaxVoltagePu
	if req.MaxVoltagePu != nil {
		maxPu = *req.MaxVoltagePu
	}
	if minPu <= 0 || maxPu <= 0 {
		return nil, invalid("min_voltage_pu and max_voltage_pu must be positive")
	}
	if minPu > maxPu {
		return nil, invalid("min_voltage_pu must not exceed max_voltage_pu")
	}

	swept, err := runSweep(ctx, db, req.Start, req.NominalVoltageV, nil, nil)
	if err != nil {
		return nil, err
	}

	nodes := make([]profileNode, 0, len(swept.tr.order))
	violations := make([]featureRef, 0)

	var totalLoss float64
	lossesComplete := true
	worstPu := 0.0
	var worst *featureRef

	for _, n := range swept.tr.order {
		st := swept.states[n]
		if st == nil {
			continue
		}

		var pu, dropPct float64
		if st.voltageBase > 0 {
			pu = st.voltageV / st.voltageBase
			dropPct = (st.voltageBase - st.voltageV) / st.voltageBase * 100
		}

		circuitID, circuitRef := circuitIdentity(st)
		ref := featureRef{FeatureClass: swept.classCodes[n.classID], FeatureID: n.featureID}
		pn := profileNode{
			FeatureClass:   ref.FeatureClass,
			FeatureID:      ref.FeatureID,
			CircuitID:      circuitID,
			CircuitRef:     circuitRef,
			VoltageV:       st.voltageV,
			NominalBaseV:   st.voltageBase,
			VoltagePu:      pu,
			VoltageDropPct: dropPct,
			WithinLimits:   true,
		}

		// A voltage of exactly 0 means this node was never reached by the
		// forward pass (no base) -- don't flag it as an under-voltage
		// violation, that would be a false positive from a missing solve, not
		// a real out-of-band condition.
		if st.voltageBase > 0 {
			pn.WithinLimits = pu >= minPu && pu <= maxPu
			if !pn.WithinLimits {
				violations = append(violations, ref)
			}
			if worst == nil || pu < worstPu {
				worstPu = pu
				w := ref
				worst = &w
			}
		}

		// Series loss on a conductor branch: I²R, with the same current the
		// load-flow report shows (I = S/V at the branch's sending end) and the
		// branch's own resolved series resistance. A branch with no resolved
		// R/X drops nothing in the sweep and so contributes no loss here --
		// flagged via losses_complete so the total reads as the lower bound it
		// is, not a spuriously clean figure.
		if st.loaded.FeatureClass.NetworkRole == "edge" {
			current := approxCurrentAmps(st.cumKW, st.cumKVAR, st.voltageV)
			amps := current
			pn.CurrentAmps = &amps
			if st.impedance.resolved {
				lossKW := current * current * st.impedance.rOhm / 1000
				pn.BranchLossKW = &lossKW
				totalLoss += lossKW
				if st.impedance.lengthM > 0 {
					length := st.impedance.lengthM
					pn.LengthM = &length
				}
			} else {
				lossesComplete = false
			}
		}

		nodes = append(nodes, pn)
	}

	// Whole-tree demand is what the start node accumulated in the backward
	// sweep -- everything downstream of (and including) it.
	var totalLoadKW, totalLoadKVAR float64
	if startState := swept.states[swept.start]; startState != nil {
		totalLoadKW = startState.cumKW
		totalLoadKVAR = startState.cumKVAR
	}
	var lossPct float64
	if totalLoadKW > 0 {
		lossPct = totalLoss / totalLoadKW * 100
	}

	return &voltageProfileResponse{
		Start:           req.Start,
		NominalVoltageV: req.NominalVoltageV,
		MinVoltagePu:    minPu,
		MaxVoltagePu:    maxPu,
		Nodes:           nodes,
		Violations:      violations,
		Summary: profileSummary{
			TotalLoadKW:    totalLoadKW,
			TotalLoadKVAR:  totalLoadKVAR,
			TotalLossKW:    totalLoss,
			LossPct:        lossPct,
			MinVoltagePu:   worstPu,
			WorstNode:      worst,
			ViolationCount: len(violations),
			LossesComplete: lossesComplete,
		},
		Barriers: toFeatureRefs(swept.barrierHits, swept.classCodes),
	}, nil
}
