package powerflow

import (
	"context"
	"math"
	"strings"

	"github.com/uptrace/bun"
)

// Unbalanced (three-phase) load flow -- Tier 1, decoupled per-phase. This is the
// per-phase twin of the balanced forward-backward sweep in sweep.go: it reuses
// the same tree build and per-node resolution (buildTree, resolveNodeStates),
// but instead of one aggregate (P, Q) it tracks each phase A/B/C independently,
// solving three linearized ladders at once and reporting per-node Va/Vb/Vc, the
// voltage unbalance between them, and the neutral return current.
//
// WHERE THE PHASE DATA COMES FROM: the schema already carries a `phase` coded
// field on the conductor classes (LV_CONDUCTOR: L1/L2/L3/N and combinations;
// MV_CONDUCTOR: A/B/C/ABC), which nothing in the balanced engine reads. A
// downward pass turns those per-conductor phase designations into an energized
// phase set at every node (a single-phase lateral off a three-phase main narrows
// the set to one phase), and a load splits its own demand across the phases it is
// actually connected to. Loads that carry no phase designation of their own
// (SERVICE_POINT has none) inherit the phase set of the conductor feeding them,
// which is the physically correct default for an LV single-phase service.
//
// THE HONEST LIMIT (Tier 1): the three phases are solved DECOUPLED -- no mutual
// coupling between them (no Carson 3x3 impedance matrix), exactly the same class
// of planning-level simplification the balanced sweep already makes when it drops
// the branch current angle from ΔV. Mutual coupling and a sequence-frame solve
// are a Tier 2 slice that first needs conductor geometry/spacing data the model
// does not carry yet. The neutral current and voltage unbalance reported here are
// computed by assuming the three phase currents sit at their nominal 0/-120/+120
// angles -- a good estimate of the imbalance a real feeder sees, not an exact
// asymmetrical-angle solve.

// phaseCount is the number of phases this Tier-1 engine tracks (A, B, C). The
// neutral is not a solved conductor here -- its return current is derived from
// the three phase currents (see neutralCurrent), not swept as a fourth ladder.
const phaseCount = 3

// phaseLabels names the three tracked phases in index order, for the response.
var phaseLabels = [phaseCount]string{"A", "B", "C"}

// phaseAngles is each phase's nominal angle in radians (A at 0, B at -120, C at
// +120), used only to combine the three phase-current magnitudes into a neutral
// return current and a voltage-unbalance figure. The decoupled per-phase sweep
// itself works in magnitudes, like the balanced engine.
var phaseAngles = [phaseCount]float64{0, -2 * math.Pi / 3, 2 * math.Pi / 3}

// phaseMask is the set of phases present at a node -- indexed A=0, B=1, C=2.
type phaseMask [phaseCount]bool

func (m phaseMask) count() int {
	n := 0
	for _, on := range m {
		if on {
			n++
		}
	}
	return n
}

func (m phaseMask) any() bool { return m.count() > 0 }

// intersect returns the phases present in both masks. A single-phase conductor
// downstream of a three-phase main narrows the energized set this way.
func (m phaseMask) intersect(other phaseMask) phaseMask {
	var out phaseMask
	for i := range out {
		out[i] = m[i] && other[i]
	}
	return out
}

// parsePhases turns a `phase` field code into the set of phases it energizes,
// handling both the LV L-notation (L1/L2/L3/N, and combinations like L1N or
// L1L2L3N) and the MV letter notation (A/B/C/ABC, AB, ...). A pure-neutral code
// (N) energizes no phase and returns an empty mask, which the caller treats as
// "carries the incoming set through" rather than de-energizing the branch. The
// match is deliberately lenient: L-tokens take precedence, and only if none are
// present does it fall back to bare A/B/C letters, so "L1L2L3N" reads as ABC and
// never mis-fires on the 'N'.
func parsePhases(code string) phaseMask {
	up := strings.ToUpper(strings.TrimSpace(code))
	var m phaseMask
	if strings.Contains(up, "L1") {
		m[0] = true
	}
	if strings.Contains(up, "L2") {
		m[1] = true
	}
	if strings.Contains(up, "L3") {
		m[2] = true
	}
	if m.any() {
		return m
	}
	// No L-notation -- read bare phase letters (A/B/C/ABC/AB/...).
	if strings.Contains(up, "A") {
		m[0] = true
	}
	if strings.Contains(up, "B") {
		m[1] = true
	}
	if strings.Contains(up, "C") {
		m[2] = true
	}
	return m
}

// readPhaseAttr reads a node's own `phase` coded field, if it carries one. The
// physical column is always named "phase" (metadata.fields.name) on the classes
// that define it (LV_CONDUCTOR, MV_CONDUCTOR, STREET_LIGHT); a class without the
// field, or a feature that hasn't set it, returns ("", false).
func readPhaseAttr(attrs map[string]any) (string, bool) {
	v, ok := attrs["phase"]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return "", false
	}
	return s, true
}

// phaseState is the unbalanced sweep's per-node working state -- the per-phase
// analog of nodeState's scalar power/voltage fields. mask is the energized phase
// set; ownKW/ownKVAR are this node's own demand split across its phases; cumKW/
// cumKVAR are the backward-accumulated per-phase through-power; voltageV is the
// forward-swept per-phase voltage; voltageBase is the nominal those voltages are
// measured against (rebased below a transformer, exactly like the balanced sweep).
type phaseState struct {
	mask        phaseMask
	ownKW       [phaseCount]float64
	ownKVAR     [phaseCount]float64
	cumKW       [phaseCount]float64
	cumKVAR     [phaseCount]float64
	voltageV    [phaseCount]float64
	voltageBase float64
}

// runUnbalancedLoadFlow is POST /analysis/unbalanced-load-flow. It shares the
// balanced sweep's front half -- resolve the start, build the radial tree,
// resolve every node's electrical inputs -- then runs the decoupled per-phase
// passes instead of the scalar ones, and formats per-phase results.
func runUnbalancedLoadFlow(ctx context.Context, db bun.IDB, req loadFlowRequest) (*unbalancedResponse, error) {
	if req.Start.FeatureClass == "" || req.Start.FeatureID == "" {
		return nil, invalid("start.feature_class and start.feature_id are required")
	}
	if req.NominalVoltageV <= 0 {
		return nil, invalid("nominal_voltage_v must be a positive number")
	}

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
	states, err := resolveNodeStates(ctx, db, tr, loader)
	if err != nil {
		return nil, err
	}

	pstates := make(map[node]*phaseState, len(tr.order))
	for n := range states {
		pstates[n] = &phaseState{}
	}

	assignPhaseMasks(tr, states, pstates, start)
	splitDemand(tr, states, pstates)
	backwardSweepPhases(tr, pstates)

	var sourceR, sourceX float64
	if ss := states[start]; ss != nil {
		sourceR, sourceX = resolveSourceImpedance(ss.attrs)
	}
	forwardSweepPhases(tr, states, pstates, start, req.NominalVoltageV, sourceR, sourceX)

	allNodes := append(append([]node{}, tr.order...), barrierHits...)
	classCodes, err := nodeClassCodes(ctx, db, allNodes)
	if err != nil {
		return nil, err
	}

	nodes, unbalanced := formatUnbalanced(tr, states, pstates, classCodes)
	return &unbalancedResponse{
		Start:           req.Start,
		NominalVoltageV: req.NominalVoltageV,
		Iterations:      sweepIterations,
		Nodes:           nodes,
		Barriers:        toFeatureRefs(barrierHits, classCodes),
		Unbalanced:      unbalanced,
	}, nil
}

// assignPhaseMasks walks the tree top-down (tr.order is parent-before-child) and
// sets each node's energized phase set. The start is three-phase (or its own
// declared phase, if it carries one); every other node inherits its parent's set,
// narrowed by its own conductor `phase` designation if it is an edge that carries
// one. A transformer passes the set through unchanged (a three-phase transformer;
// Tier 1 does not model a single-phase transformer bank re-phasing its output).
func assignPhaseMasks(tr *tree, states map[node]*nodeState, pstates map[node]*phaseState, start node) {
	full := phaseMask{true, true, true}
	for _, n := range tr.order {
		ps := pstates[n]
		if ps == nil {
			continue
		}
		if n == start {
			ps.mask = startMask(states[n])
			continue
		}
		parent := pstates[tr.parent[n]]
		incoming := full
		if parent != nil && parent.mask.any() {
			incoming = parent.mask
		}
		ps.mask = incoming
		st := states[n]
		if st != nil && st.loaded.FeatureClass.NetworkRole == "edge" {
			if code, ok := readPhaseAttr(st.attrs); ok {
				if parsed := parsePhases(code); parsed.any() {
					narrowed := incoming.intersect(parsed)
					if narrowed.any() {
						ps.mask = narrowed
					}
				}
			}
		}
	}
}

// startMask is the source node's energized phase set: its own declared phase if
// it carries the field, else all three (a substation / feeder head is three-phase).
func startMask(st *nodeState) phaseMask {
	full := phaseMask{true, true, true}
	if st == nil {
		return full
	}
	if code, ok := readPhaseAttr(st.attrs); ok {
		if parsed := parsePhases(code); parsed.any() {
			return parsed
		}
	}
	return full
}

// splitDemand assigns each node's own (net) demand across the phases it is
// connected to. A single-phase load puts all its demand on its one phase; a
// three-phase load is split evenly. The scalar ownKW/ownKVAR already netted
// embedded generation in (resolveNodeState), so a generator node splits its
// negative demand the same way -- a per-phase injection.
func splitDemand(tr *tree, states map[node]*nodeState, pstates map[node]*phaseState) {
	for _, n := range tr.order {
		st, ps := states[n], pstates[n]
		if st == nil || ps == nil {
			continue
		}
		if st.ownKW == 0 && st.ownKVAR == 0 {
			continue
		}
		// A load with its own phase field (e.g. STREET_LIGHT) uses it; otherwise
		// it inherits the phase set already assigned to the node (the feeding
		// conductor's phases), which is the correct default for a SERVICE_POINT.
		conn := ps.mask
		if code, ok := readPhaseAttr(st.attrs); ok {
			if parsed := parsePhases(code); parsed.any() {
				if narrowed := ps.mask.intersect(parsed); narrowed.any() {
					conn = narrowed
				} else {
					conn = parsed
				}
			}
		}
		cnt := conn.count()
		if cnt == 0 {
			continue
		}
		perKW := st.ownKW / float64(cnt)
		perKVAR := st.ownKVAR / float64(cnt)
		for i := 0; i < phaseCount; i++ {
			if conn[i] {
				ps.ownKW[i] = perKW
				ps.ownKVAR[i] = perKVAR
			}
		}
	}
}

// backwardSweepPhases is the per-phase backward pass: each node's per-phase
// through-power is its own per-phase demand plus every child's, walking tr.order
// in reverse so a child is always summed before its parent (Kersting, per phase).
func backwardSweepPhases(tr *tree, pstates map[node]*phaseState) {
	for i := len(tr.order) - 1; i >= 0; i-- {
		n := tr.order[i]
		ps := pstates[n]
		if ps == nil {
			continue
		}
		ps.cumKW = ps.ownKW
		ps.cumKVAR = ps.ownKVAR
		for _, c := range tr.children[n] {
			cs := pstates[c]
			if cs == nil {
				continue
			}
			for j := 0; j < phaseCount; j++ {
				ps.cumKW[j] += cs.cumKW[j]
				ps.cumKVAR[j] += cs.cumKVAR[j]
			}
		}
	}
}

// forwardSweepPhases is the per-phase forward pass: derive each node's per-phase
// voltage, sweepIterations times, exactly like the balanced forwardSweep but with
// one ladder per phase. A conductor drops each phase against its own per-phase
// through-power over the SAME per-phase impedance (decoupled -- no mutual term);
// a transformer rebases all three phases by its turns ratio and secondary base; a
// plain junction is pass-through. The source absorbs its own impedance per phase.
func forwardSweepPhases(tr *tree, states map[node]*nodeState, pstates map[node]*phaseState, start node, nominalV, sourceR, sourceX float64) {
	for iter := 0; iter < sweepIterations; iter++ {
		for _, n := range tr.order {
			st, ps := states[n], pstates[n]
			if ps == nil {
				continue
			}
			if n == start {
				ps.voltageBase = nominalV
				for j := 0; j < phaseCount; j++ {
					ps.voltageV[j] = nominalV - approxDropV(ps.cumKW[j], ps.cumKVAR[j], sourceR, sourceX, nominalV)
				}
				continue
			}
			parent := pstates[tr.parent[n]]
			if parent == nil {
				ps.voltageBase = nominalV
				for j := 0; j < phaseCount; j++ {
					ps.voltageV[j] = nominalV
				}
				continue
			}

			switch {
			case st != nil && st.xfmr.ok:
				// Three-phase transformer: apply the turns ratio to each phase and
				// subtract that phase's own series drop on the secondary base. A
				// three-winding transformer is approximated here by its main
				// (secondary) winding -- the unbalanced Tier-1 limit noted up top.
				ps.voltageBase = st.xfmr.secondaryV
				for j := 0; j < phaseCount; j++ {
					vIdeal := parent.voltageV[j] * st.xfmr.ratio
					ps.voltageV[j] = vIdeal - approxDropV(ps.cumKW[j], ps.cumKVAR[j], st.xfmr.rOhm, st.xfmr.xOhm, vIdeal)
				}
			case st != nil && st.impedance.resolved:
				ps.voltageBase = parent.voltageBase
				for j := 0; j < phaseCount; j++ {
					ps.voltageV[j] = parent.voltageV[j] - approxDropV(ps.cumKW[j], ps.cumKVAR[j], st.impedance.rOhm, st.impedance.xOhm, parent.voltageV[j])
				}
			default:
				ps.voltageBase = parent.voltageBase
				ps.voltageV = parent.voltageV
			}
		}
	}
}

// neutralCurrent estimates the neutral return current from the three phase
// currents by combining them at their nominal 0/-120/+120 angles -- zero for a
// balanced set, growing with imbalance. This is the Tier-1 estimate (the sweep
// itself carries magnitudes only, so the true asymmetrical angles are not solved).
func neutralCurrent(iA, iB, iC float64) float64 {
	var re, im float64
	for idx, mag := range [phaseCount]float64{iA, iB, iC} {
		re += mag * math.Cos(phaseAngles[idx])
		im += mag * math.Sin(phaseAngles[idx])
	}
	return math.Hypot(re, im)
}

// voltageUnbalancePct is the NEMA voltage-unbalance figure -- the maximum
// deviation of any phase voltage from the three-phase average, as a percent of
// that average. Reported only when all three phases are present at the node (an
// unbalance figure over a single- or two-phase node is meaningless).
func voltageUnbalancePct(vA, vB, vC float64) (float64, bool) {
	mean := (vA + vB + vC) / 3
	if mean <= 0 {
		return 0, false
	}
	maxDev := math.Max(math.Abs(vA-mean), math.Max(math.Abs(vB-mean), math.Abs(vC-mean)))
	return maxDev / mean * 100, true
}

// formatUnbalanced turns the swept phase states into the response, flagging into
// Unbalanced any node whose voltage unbalance exceeds the common 2% planning
// limit (NEMA MG-1 / EN 50160 order of magnitude) so a client need not re-scan.
func formatUnbalanced(tr *tree, states map[node]*nodeState, pstates map[node]*phaseState, classCodes map[string]string) ([]unbalancedNodeResult, []featureRef) {
	nodes := make([]unbalancedNodeResult, 0, len(tr.order))
	unbalanced := make([]featureRef, 0)

	for _, n := range tr.order {
		st, ps := states[n], pstates[n]
		if st == nil || ps == nil {
			continue
		}
		circuitID, circuitRef := circuitIdentity(st)
		res := unbalancedNodeResult{
			FeatureClass: classCodes[n.classID],
			FeatureID:    n.featureID,
			CircuitID:    circuitID,
			CircuitRef:   circuitRef,
			NominalBaseV: ps.voltageBase,
			Phases:       make([]phaseResult, 0, phaseCount),
		}

		isEdge := st.loaded.FeatureClass.NetworkRole == "edge"
		var iByPhase [phaseCount]float64
		for j := 0; j < phaseCount; j++ {
			if !ps.mask[j] {
				continue
			}
			v := ps.voltageV[j]
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
				amps := approxCurrentAmps(ps.cumKW[j], ps.cumKVAR[j], v)
				iByPhase[j] = amps
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

		// Neutral return + voltage unbalance are only meaningful once all three
		// phases are present at the node.
		if ps.mask.count() == phaseCount {
			if isEdge {
				in := neutralCurrent(iByPhase[0], iByPhase[1], iByPhase[2])
				res.NeutralCurrentAmps = &in
			}
			if uv, ok := voltageUnbalancePct(ps.voltageV[0], ps.voltageV[1], ps.voltageV[2]); ok {
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

// unbalanceLimitPct is the voltage-unbalance threshold (percent) above which a
// node is surfaced in the response's Unbalanced convenience list -- the common
// 2% distribution-planning limit.
const unbalanceLimitPct = 2.0
