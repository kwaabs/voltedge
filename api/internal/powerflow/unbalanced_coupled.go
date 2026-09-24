package powerflow

import (
	"math"
	"math/cmplx"
)

// Unbalanced (three-phase) load flow -- Tier 2, coupled. Where the Tier 1 path
// (unbalanced.go) solves phases A/B/C as three INDEPENDENT scalar ladders, this
// path runs a single COMPLEX forward-backward sweep in which every conductor's
// voltage drop is the full matrix product ΔV = Z·I over its 3x3 Carson phase-
// impedance matrix (carson.go). The off-diagonal (mutual) terms of that matrix
// are exactly what Tier 1 omits: with them, current on one phase drops the OTHER
// phases' voltages too, so a real single-phase-heavy feeder shows the cross-phase
// interaction the decoupled model cannot.
//
// The backward pass is shared with Tier 1 unchanged -- per-phase demand is
// constant-power, so the accumulated per-phase (P,Q) does not depend on voltage
// and needs no re-summing between iterations. Only the forward pass differs:
// it carries per-phase COMPLEX voltages (phasors, seeded at the balanced nominal
// set) and, each iteration, derives each branch's per-phase complex current from
// the accumulated power and the latest voltage, I = conj(S/V), then applies the
// coupled drop. Three passes, like every other sweep here.
//
// HONEST LIMITS (Tier 2): the neutral is Kron-reduced into the phase matrix
// (its own current is recovered from the phase currents, not solved as a fourth
// state), transformers are still modelled per-phase by their turns ratio plus a
// DIAGONAL series impedance (no transformer inter-phase coupling or vector-group
// phase shift -- the same three-phase-transformer simplification Tier 1 makes),
// and a conductor whose spec lacks line geometry degrades to its decoupled
// diagonal impedance rather than failing the run. What Tier 2 adds over Tier 1
// is real line mutual coupling and a true complex (angle-exact) neutral current
// and voltage unbalance.

// couplingDecoupled / couplingCarson are the two coupling_model labels the
// response carries.
const (
	couplingDecoupled = "decoupled"
	couplingCarson    = "carson"
)

// coupledForwardSweep is the Tier 2 forward pass: per-phase COMPLEX voltages,
// derived sweepIterations times over the tree top-down. The start node sits at
// the balanced nominal set behind its own (diagonal) source impedance; a
// transformer rebases each phase by its turns ratio and drops a diagonal series
// impedance; a conductor with a resolved Carson matrix drops the full coupled
// Z·I; a conductor with only a scalar impedance drops a decoupled diagonal Z·I;
// a plain junction passes voltage through. voltageBase is set exactly as the
// decoupled pass sets it, so the two paths report drop % on the same basis.
func coupledForwardSweep(tr *tree, states map[node]*nodeState, pstates map[node]*phaseState, carsons map[node]carsonMatrix, start node, nominalV, sourceR, sourceX float64) {
	source := nominalPhasors(nominalV)

	// Flat start: seed every node at the balanced nominal set so the first
	// iteration's current calc never divides by a zero voltage. The top-down
	// pass overwrites these in order (parent before child), so a node below a
	// transformer is corrected to its own base within the same iteration.
	for _, n := range tr.order {
		if ps := pstates[n]; ps != nil {
			ps.voltageC = source
		}
	}

	for iter := 0; iter < sweepIterations; iter++ {
		for _, n := range tr.order {
			st, ps := states[n], pstates[n]
			if ps == nil {
				continue
			}
			if n == start {
				ps.voltageBase = nominalV
				zs := complex(sourceR, sourceX)
				iBranch := branchCurrents(ps, source)
				for j := 0; j < phaseCount; j++ {
					ps.voltageC[j] = source[j] - zs*iBranch[j]
				}
				continue
			}
			parent := pstates[tr.parent[n]]
			if parent == nil {
				ps.voltageBase = nominalV
				ps.voltageC = source
				continue
			}

			switch {
			case st != nil && st.xfmr.ok:
				// Three-phase transformer: turns ratio per phase, then a DIAGONAL
				// series drop on the secondary base (no inter-phase coupling).
				ps.voltageBase = st.xfmr.secondaryV
				ratio := complex(st.xfmr.ratio, 0)
				zt := complex(st.xfmr.rOhm, st.xfmr.xOhm)
				var vIdeal [phaseCount]complex128
				for j := 0; j < phaseCount; j++ {
					vIdeal[j] = parent.voltageC[j] * ratio
				}
				iBranch := branchCurrents(ps, vIdeal)
				for j := 0; j < phaseCount; j++ {
					ps.voltageC[j] = vIdeal[j] - zt*iBranch[j]
				}
			case hasCarson(carsons, n):
				// Coupled conductor: full 3x3 Z·I drop off the parent voltage.
				ps.voltageBase = parent.voltageBase
				z := carsons[n].z
				iBranch := branchCurrents(ps, parent.voltageC)
				for p := 0; p < phaseCount; p++ {
					var drop complex128
					for q := 0; q < phaseCount; q++ {
						drop += z[p][q] * iBranch[q]
					}
					ps.voltageC[p] = parent.voltageC[p] - drop
				}
			case st != nil && st.impedance.resolved:
				// Conductor with no geometry: decoupled diagonal complex drop.
				ps.voltageBase = parent.voltageBase
				zd := complex(st.impedance.rOhm, st.impedance.xOhm)
				iBranch := branchCurrents(ps, parent.voltageC)
				for j := 0; j < phaseCount; j++ {
					ps.voltageC[j] = parent.voltageC[j] - zd*iBranch[j]
				}
			default:
				ps.voltageBase = parent.voltageBase
				ps.voltageC = parent.voltageC
			}
		}
	}
}

// hasCarson reports whether a node has a resolved Carson matrix in the map.
func hasCarson(carsons map[node]carsonMatrix, n node) bool {
	m, ok := carsons[n]
	return ok && m.resolved
}

// nominalPhasors is the balanced three-phase voltage set at magnitude v: phase
// A at 0, B at -120, C at +120 degrees (the same nominal angles Tier 1 uses to
// estimate its neutral current, here the actual sweep seed).
func nominalPhasors(v float64) [phaseCount]complex128 {
	var out [phaseCount]complex128
	for p := 0; p < phaseCount; p++ {
		out[p] = complex(v*math.Cos(phaseAngles[p]), v*math.Sin(phaseAngles[p]))
	}
	return out
}

// branchCurrents is each energized phase's complex current from the node's
// accumulated per-phase power and a reference voltage: I = conj(S / V), with
// S = (cumKW + j·cumKVAR)·1000 in VA. A phase not present at the node, or one
// whose reference voltage has collapsed toward zero, carries no current (rather
// than a divide-by-zero blow-up). Constant-power load, so this is the exact
// current-injection Kersting's backward-forward sweep uses.
func branchCurrents(ps *phaseState, vref [phaseCount]complex128) [phaseCount]complex128 {
	var i [phaseCount]complex128
	for p := 0; p < phaseCount; p++ {
		if !ps.mask[p] {
			continue
		}
		v := vref[p]
		if cmplx.Abs(v) < 1e-6 {
			continue
		}
		s := complex(ps.cumKW[p]*1000, ps.cumKVAR[p]*1000)
		i[p] = cmplx.Conj(s / v)
	}
	return i
}

// formatUnbalancedCoupled turns the complex-swept states into the response --
// the Tier 2 twin of formatUnbalanced. Per-phase voltage is the phasor
// magnitude; per-phase current is the branch current magnitude (from the node's
// OWN final voltage); the neutral current is the true complex sum |Ia+Ib+Ic|
// (angle-exact here, not the nominal-angle estimate Tier 1 reports); voltage
// unbalance is NEMA over the three magnitudes. A node whose voltage unbalance
// exceeds the planning limit is surfaced in the Unbalanced convenience list.
func formatUnbalancedCoupled(tr *tree, states map[node]*nodeState, pstates map[node]*phaseState, carsons map[node]carsonMatrix, classCodes map[string]string) ([]unbalancedNodeResult, []featureRef) {
	nodes := make([]unbalancedNodeResult, 0, len(tr.order))
	unbalanced := make([]featureRef, 0)

	for _, n := range tr.order {
		st, ps := states[n], pstates[n]
		if st == nil || ps == nil {
			continue
		}
		circuitID, circuitRef := circuitIdentity(st)
		res := unbalancedNodeResult{
			FeatureClass:  classCodes[n.classID],
			FeatureID:     n.featureID,
			CircuitID:     circuitID,
			CircuitRef:    circuitRef,
			NominalBaseV:  ps.voltageBase,
			Phases:        make([]phaseResult, 0, phaseCount),
			MutualModeled: hasCarson(carsons, n),
		}

		isEdge := st.loaded.FeatureClass.NetworkRole == "edge"
		iBranch := branchCurrents(ps, ps.voltageC)
		var vMag [phaseCount]float64
		for j := 0; j < phaseCount; j++ {
			if !ps.mask[j] {
				continue
			}
			v := cmplx.Abs(ps.voltageC[j])
			vMag[j] = v
			var dropPct float64
			if ps.voltageBase > 0 {
				dropPct = (ps.voltageBase - v) / ps.voltageBase * 100
			}
			pr := phaseResult{
				Phase:          phaseLabels[j],
				VoltageV:       v,
				VoltageDropPct: dropPct,
				CumulativeKW:   ps.cumKW[j],
				CumulativeKVAR: ps.cumKVAR[j],
			}
			if isEdge {
				amps := cmplx.Abs(iBranch[j])
				pr.CurrentAmps = &amps
				if st.impedance.ampacityAmps != nil {
					amp := *st.impedance.ampacityAmps
					loadingPct := amps / amp * 100
					pr.AmpacityAmps = &amp
					pr.LoadingPct = &loadingPct
				}
			}
			res.Phases = append(res.Phases, pr)
		}

		// Neutral return + voltage unbalance are meaningful only once all three
		// phases are present. The neutral current is the exact complex phasor sum.
		if ps.mask.count() == phaseCount {
			if isEdge {
				in := cmplx.Abs(iBranch[0] + iBranch[1] + iBranch[2])
				res.NeutralCurrentAmps = &in
			}
			if uv, ok := voltageUnbalancePct(vMag[0], vMag[1], vMag[2]); ok {
				res.VoltageUnbalancePct = &uv
				if uv > unbalanceLimitPct {
					unbalanced = append(unbalanced, featureRef{FeatureClass: res.FeatureClass, FeatureID: res.FeatureID})
				}
			}
		}
		res.MaterialResolved = st.impedance.resolved
		nodes = append(nodes, res)
	}
	return nodes, unbalanced
}
