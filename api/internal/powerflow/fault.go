package powerflow

import (
	"context"
	"math"

	"github.com/uptrace/bun"
)

// faultThreePhase / faultSLG are the two values faultCurrentRequest.FaultType
// accepts: the symmetrical three-phase bolted fault (the default, positive-
// sequence only) and the single-line-to-ground fault (which adds the zero-
// sequence network -- see groundfault.go).
const (
	faultThreePhase = "three_phase"
	faultSLG        = "slg"
)

// basisLineToLine / basisLineToNeutral are the two values faultCurrentRequest.
// VoltageBasis accepts. They decide the single factor that converts the driving
// voltage behind a fault from the supplied nominal into the line-to-neutral
// voltage the per-phase symmetrical fault current is actually driven by.
const (
	basisLineToLine    = "line_to_line"
	basisLineToNeutral = "line_to_neutral"
)

// phaseDivisor is the factor the supplied NominalVoltageV (and every transformer
// secondary base carried on it) is divided by to get the line-to-neutral driving
// voltage: √3 for a line-to-line nominal (the default), 1 for a line-to-neutral
// one. The accumulated path impedances are per-phase ohms either way, so the
// standard three-phase symmetrical fault current is I = (V_base / phaseDivisor) /
// |Z|, and the SLG and embedded-generation infeeds -- all on the same V²/S
// convention -- scale by the SAME factor, so grid, ground and generator figures
// stay mutually consistent whichever basis the caller declares.
func phaseDivisor(basis string) float64 {
	if basis == basisLineToNeutral {
		return 1
	}
	return math.Sqrt(3) // line_to_line (and the empty default)
}

// runFaultCurrent is POST /analysis/fault-current's single entry point:
// the symmetrical (3-phase bolted) fault-current counterpart of
// runLoadFlow, sharing the same radial-tree substrate. Where the load
// flow accumulates demand *backward* (leaves to source) and derives
// voltage forward, a fault-current walk accumulates series impedance
// *forward* (source to leaves) and derives prospective fault current at
// each node as I = V / |Z_total|. Everything up to the accumulation --
// resolving the start, loading barriers, building the radial tree
// (rejecting a mesh outright, since a series-impedance path is only
// well-defined on a tree), and resolving each node's own branch
// impedance -- is the exact same machinery the load flow already uses,
// reused rather than reimplemented.
func runFaultCurrent(ctx context.Context, db bun.IDB, req faultCurrentRequest) (*faultCurrentResponse, error) {
	if req.Start.FeatureClass == "" || req.Start.FeatureID == "" {
		return nil, invalid("start.feature_class and start.feature_id are required")
	}
	if req.NominalVoltageV <= 0 {
		return nil, invalid("nominal_voltage_v must be a positive number")
	}
	faultType := req.FaultType
	if faultType == "" {
		faultType = faultThreePhase
	}
	if faultType != faultThreePhase && faultType != faultSLG {
		return nil, invalid("fault_type must be %q or %q", faultThreePhase, faultSLG)
	}
	if req.FaultImpedanceOhm < 0 {
		return nil, invalid("fault_impedance_ohm must not be negative")
	}
	if req.VoltageBasis != "" && req.VoltageBasis != basisLineToLine && req.VoltageBasis != basisLineToNeutral {
		return nil, invalid("voltage_basis must be %q or %q", basisLineToLine, basisLineToNeutral)
	}
	phaseDiv := phaseDivisor(req.VoltageBasis)

	start, err := resolveNode(ctx, db, req.Start)
	if err != nil {
		return nil, err
	}

	barriers, err := loadFlowBarriers(ctx, db)
	if err != nil {
		return nil, err
	}
	loader := newFeatureClassLoader()

	tr, barrierHits, err := buildTree(ctx, db, start, barriers, loader, nil, nil)
	if err != nil {
		return nil, err
	}

	// resolveNodeStates already resolves each node's own branch impedance
	// (and demand, unused here) -- reused wholesale rather than duplicating
	// the per-node row load + impedance resolution.
	states, err := resolveNodeStates(ctx, db, tr, loader)
	if err != nil {
		return nil, err
	}

	var sourceR, sourceX float64
	if startState := states[start]; startState != nil {
		sourceR, sourceX = resolveSourceImpedance(startState.attrs)
	}
	paths := accumulateSeriesImpedance(tr, states, start, req.NominalVoltageV, sourceR, sourceX)

	// Embedded-generation fault infeed (Tier A): every GENERATOR in the tree is
	// a second fault source, contributing to the three-phase fault at each node
	// by superposition through its own path. Empty (all zero) on a network with
	// no generation, so the grid-only figures are unchanged there. SLG figures
	// stay grid-only -- see genfault.go.
	genContribs := generatorFaultContributions(tr, states, paths, start, sourceR, sourceX, phaseDiv)

	// Zero-sequence network only for an SLG run. The source Z0 is the start
	// node's own source_r0_ohm/source_x0_ohm when set, else the positive-sequence
	// source impedance (sourceR/sourceX) with the path flagged estimated. Nil for
	// a three-phase run, which formatFaultResults takes to mean "no SLG fields."
	var z0 map[node]zeroSeqPath
	if faultType == faultSLG {
		sourceR0, sourceX0, srcOK := sourceR, sourceX, false
		if startState := states[start]; startState != nil {
			if r0, x0, ok := resolveSourceZeroSeq(startState.attrs); ok {
				sourceR0, sourceX0, srcOK = r0, x0, true
			}
		}
		earthing := earthingShunts(tr, states)
		z0 = accumulateZeroSeqImpedance(tr, states, start, req.NominalVoltageV, sourceR0, sourceX0, !srcOK, earthing)
	}

	allNodes := append(append([]node{}, tr.order...), barrierHits...)
	classCodes, err := nodeClassCodes(ctx, db, allNodes)
	if err != nil {
		return nil, err
	}

	nodes := formatFaultResults(tr, states, paths, genContribs, z0, req.FaultImpedanceOhm, phaseDiv, classCodes)

	return &faultCurrentResponse{
		Start:           req.Start,
		NominalVoltageV: req.NominalVoltageV,
		FaultType:       faultType,
		Nodes:           nodes,
		Barriers:        toFeatureRefs(barrierHits, classCodes),
	}, nil
}

// pathImpedance is the accumulated series R+jX from the source down to one
// node, referred to that node's own voltage base, plus whether every
// impedance-bearing element along the path resolved -- the fault-current
// analog of load flow's per-node cumulative demand, except accumulated in
// the opposite direction. baseV is the voltage the accumulated R/X are
// referred to: the run's nominal on the source side of every transformer,
// a transformer's secondary below it -- so the prospective current at a
// node is V_base / |Z| measured on that node's own side, and impedances
// crossing a transformer are scaled to the local base before summing.
type pathImpedance struct {
	rOhm     float64
	xOhm     float64
	baseV    float64
	resolved bool
}

// accumulateSeriesImpedance walks tr.order forward (root first, so a
// parent's accumulated impedance is always in hand before its child's is
// computed -- the mirror of backwardSweep's reverse walk) summing each
// edge node's own branch R+jX onto its parent's running total. The start
// node seeds the total with the source's own Thevenin impedance on the
// run's nominal base. A transformer node crossed mid-tree refers the
// parent's accumulated impedance across the turns ratio (ohms scale by
// ratio^2 from primary base to secondary base), adds the transformer's own
// series impedance (already referred to the secondary), and rebases every
// node below it to the secondary voltage -- so an LV fault level reflects
// the transformer's own %Z, which dominates it. A junction/container node
// adds nothing (electrically a bolted node, same pass-through it is for
// voltage drop). An edge node whose own impedance didn't resolve, or a
// transformer contributing zero series impedance (no nameplate %Z or
// rating to compute it from -- the impedance that actually limits an LV
// fault), adds zero but flips the path's resolved flag false, so
// formatFaultResults can mark the resulting fault current as an optimistic
// upper bound rather than a trustworthy figure.
func accumulateSeriesImpedance(tr *tree, states map[node]*nodeState, start node, nominalVoltageV, sourceR, sourceX float64) map[node]pathImpedance {
	paths := make(map[node]pathImpedance, len(tr.order))
	for _, n := range tr.order {
		if n == start {
			paths[n] = pathImpedance{rOhm: sourceR, xOhm: sourceX, baseV: nominalVoltageV, resolved: true}
			continue
		}

		parent := paths[tr.parent[n]]
		acc := parent

		st := states[n]
		switch {
		case st != nil && st.xfmr.ok:
			// Refer the upstream (primary-base) impedance onto the secondary
			// base, then add the transformer's own series impedance (which
			// resolveTransformerModel already referred to the secondary).
			ratioSq := st.xfmr.ratio * st.xfmr.ratio
			acc.rOhm = parent.rOhm*ratioSq + st.xfmr.rOhm
			acc.xOhm = parent.xOhm*ratioSq + st.xfmr.xOhm
			acc.baseV = st.xfmr.secondaryV
			if st.xfmr.rOhm == 0 && st.xfmr.xOhm == 0 {
				acc.resolved = false
			}
		case st != nil && st.loaded.FeatureClass.NetworkRole == "edge":
			if st.impedance.resolved {
				acc.rOhm += st.impedance.rOhm
				acc.xOhm += st.impedance.xOhm
			} else {
				acc.resolved = false
			}
		}
		paths[n] = acc
	}
	return paths
}

// asymmetryKappa is the IEC 60909 peak-asymmetry factor κ from a path's X/R:
//
//	κ = 1.02 + 0.98 · e^(−3·R/X)
//
// It is the DC-offset multiplier that turns the symmetrical RMS fault current
// into the first-half-cycle peak. A purely reactive path (R→0, X/R→∞) gives the
// maximum κ → 2.0; a resistive one (X→0, R/X→∞) gives κ → 1.02 (almost no
// offset). Returns 0 (caller omits the peak) when the path has no reactance OR no
// resistance, where X/R is undefined.
func asymmetryKappa(rOhm, xOhm float64) float64 {
	if rOhm <= 0 || xOhm <= 0 {
		return 0
	}
	return 1.02 + 0.98*math.Exp(-3*rOhm/xOhm)
}

// peakFaultCurrentAmps is the crest asymmetrical current i_p = κ·√2·I_sym -- the
// making/peak-withstand duty a device sees, from the symmetrical RMS current and
// the asymmetry factor κ (asymmetryKappa).
func peakFaultCurrentAmps(iSymAmps, kappa float64) float64 {
	return kappa * math.Sqrt2 * iSymAmps
}

// formatFaultResults turns the accumulated per-node path impedances into
// the response shape: the series R/X/|Z| to each node (referred to that
// node's own voltage base) and the prospective bolted-fault current there,
// I = V_base / |Z_total|, measured on the node's own side of any
// transformer. NominalBaseV reports that base so a client can tell an MV
// fault level from an LV one in a single result set. A node whose total
// path impedance is exactly zero (an ideal source with no conductor yet
// between it and the fault) reports no current rather than a
// division-by-zero or a bogus infinity -- the honest read of "unbounded
// behind an ideal source, set source_r_ohm/source_x_ohm on the start
// feature for a finite figure."
// z0 is nil on a three-phase run (no SLG fields emitted) and the per-node
// zero-sequence path on an SLG run; faultImpedanceOhm is the fault's own Zf,
// entering the SLG denominator as the standard 3·Zf term.
func formatFaultResults(tr *tree, states map[node]*nodeState, paths map[node]pathImpedance, genContribs map[node]float64, z0 map[node]zeroSeqPath, faultImpedanceOhm, phaseDiv float64, classCodes map[string]string) []faultNodeResult {
	nodes := make([]faultNodeResult, 0, len(tr.order))
	for _, n := range tr.order {
		if _, ok := states[n]; !ok {
			continue
		}
		p := paths[n]
		impedanceOhm := math.Hypot(p.rOhm, p.xOhm)

		circuitID, circuitRef := circuitIdentity(states[n])
		res := faultNodeResult{
			FeatureClass:     classCodes[n.classID],
			FeatureID:        n.featureID,
			CircuitID:        circuitID,
			CircuitRef:       circuitRef,
			ResistanceOhm:    p.rOhm,
			ReactanceOhm:     p.xOhm,
			ImpedanceOhm:     impedanceOhm,
			NominalBaseV:     p.baseV,
			MaterialResolved: p.resolved,
		}
		if impedanceOhm > 0 {
			// Grid infeed (the figure this endpoint has always reported), plus any
			// embedded-generation contribution to this node's fault by
			// superposition. FaultCurrentAmps is the TOTAL -- the prospective duty
			// switchgear here must interrupt. The split is surfaced only when a
			// generator actually contributes, so a network with no generation reads
			// exactly as before.
			// Driving voltage is line-to-neutral (baseV / phaseDiv), so this is the
			// STANDARD per-phase symmetrical fault current, directly comparable to a
			// device's kA interrupting rating.
			gridAmps := (p.baseV / phaseDiv) / impedanceOhm
			genAmps := genContribs[n]
			totalAmps := gridAmps + genAmps
			res.FaultCurrentAmps = &totalAmps
			if genAmps > 0 {
				grid := gridAmps
				gen := genAmps
				res.GridContributionAmps = &grid
				res.GeneratorContributionAmps = &gen
			}

			// Peak (crest) asymmetrical current from the path's X/R: the making/
			// peak-withstand duty, which the symmetrical RMS alone understates.
			// Uses the Thévenin X/R at the fault point applied to the TOTAL
			// symmetrical current (a documented simplification when a generator
			// also feeds the fault). Omitted when the path has no resistance, where
			// X/R -- and so κ -- is undefined.
			if kappa := asymmetryKappa(p.rOhm, p.xOhm); kappa > 0 {
				xr := p.xOhm / p.rOhm
				ip := peakFaultCurrentAmps(totalAmps, kappa)
				res.XrRatio = &xr
				res.PeakAsymmetryKappa = &kappa
				res.PeakFaultCurrentAmps = &ip
			}
		}

		if z0 != nil {
			addGroundFaultFields(&res, p, z0[n], faultImpedanceOhm, phaseDiv)
		}
		nodes = append(nodes, res)
	}
	return nodes
}

// addGroundFaultFields fills a node's single-line-to-ground result from its
// positive-sequence path p and zero-sequence path zp (both on the same base).
// The SLG current is I = 3·V / |Z1 + Z2 + Z0 + 3·Zf| with Z2 = Z1, so the
// denominator is the complex sum (2·R1 + R0 + 3·Zf) + j(2·X1 + X0). An
// ungrounded node (no zero-sequence return path) reports grounded = false and no
// current -- an SLG fault there draws essentially nothing. A zero denominator
// (an ideal source with no impedance yet to the fault) reports no current, the
// same honest read as the three-phase case.
func addGroundFaultFields(res *faultNodeResult, p pathImpedance, zp zeroSeqPath, faultImpedanceOhm, phaseDiv float64) {
	grounded := zp.grounded
	res.Grounded = &grounded
	if !grounded {
		return
	}

	r0, x0 := zp.r0Ohm, zp.x0Ohm
	res.ZeroSeqResistanceOhm = &r0
	res.ZeroSeqReactanceOhm = &x0
	groundResolved := p.resolved && zp.resolved
	res.GroundPathResolved = &groundResolved
	estimated := zp.estimated
	res.ZeroSeqEstimated = &estimated

	totalR := 2*p.rOhm + r0 + 3*faultImpedanceOhm
	totalX := 2*p.xOhm + x0
	totalZ := math.Hypot(totalR, totalX)
	if totalZ > 0 {
		// 3·V_LN / |Z1+Z2+Z0+3Zf|: the driving voltage is line-to-neutral
		// (baseV / phaseDiv), matching the three-phase figure's basis.
		slgAmps := 3 * (p.baseV / phaseDiv) / totalZ
		res.GroundFaultCurrentAmps = &slgAmps
	}
}
