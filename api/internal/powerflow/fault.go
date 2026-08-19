package powerflow

import (
	"context"
	"math"

	"github.com/uptrace/bun"
)

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

	start, err := resolveNode(ctx, db, req.Start)
	if err != nil {
		return nil, err
	}

	barriers, err := loadFlowBarriers(ctx, db)
	if err != nil {
		return nil, err
	}
	loader := newFeatureClassLoader()

	tr, barrierHits, err := buildTree(ctx, db, start, barriers, loader)
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
	paths := accumulateSeriesImpedance(tr, states, start, sourceR, sourceX)

	allNodes := append(append([]node{}, tr.order...), barrierHits...)
	classCodes, err := nodeClassCodes(ctx, db, allNodes)
	if err != nil {
		return nil, err
	}

	nodes := formatFaultResults(tr, states, paths, classCodes, req.NominalVoltageV)

	return &faultCurrentResponse{
		Start:           req.Start,
		NominalVoltageV: req.NominalVoltageV,
		Nodes:           nodes,
		Barriers:        toFeatureRefs(barrierHits, classCodes),
	}, nil
}

// pathImpedance is the accumulated series R+jX from the source down to one
// node, plus whether every conductor branch along that path had resolved
// material impedance -- the fault-current analog of load flow's per-node
// cumulative demand, except accumulated in the opposite direction.
type pathImpedance struct {
	rOhm     float64
	xOhm     float64
	resolved bool
}

// accumulateSeriesImpedance walks tr.order forward (root first, so a
// parent's accumulated impedance is always in hand before its child's is
// computed -- the mirror of backwardSweep's reverse walk) summing each
// edge node's own branch R+jX onto its parent's running total. The start
// node seeds the total with the source's own Thevenin impedance; a
// junction/container node adds nothing (electrically a bolted node, same
// pass-through it is for voltage drop). An edge node whose own impedance
// didn't resolve adds zero but flips the path's resolved flag false, so
// formatFaultResults can mark the resulting fault current as an optimistic
// upper bound rather than a trustworthy figure.
func accumulateSeriesImpedance(tr *tree, states map[node]*nodeState, start node, sourceR, sourceX float64) map[node]pathImpedance {
	paths := make(map[node]pathImpedance, len(tr.order))
	for _, n := range tr.order {
		if n == start {
			paths[n] = pathImpedance{rOhm: sourceR, xOhm: sourceX, resolved: true}
			continue
		}

		parent := paths[tr.parent[n]]
		acc := parent

		st := states[n]
		if st != nil && st.loaded.FeatureClass.NetworkRole == "edge" {
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

// formatFaultResults turns the accumulated per-node path impedances into
// the response shape: the series R/X/|Z| to each node and the prospective
// bolted-fault current there, I = V / |Z_total|. A node whose total path
// impedance is exactly zero (an ideal source with no conductor yet between
// it and the fault) reports no current rather than a division-by-zero or a
// bogus infinity -- the honest read of "unbounded behind an ideal source,
// set source_r_ohm/source_x_ohm on the start feature for a finite figure."
func formatFaultResults(tr *tree, states map[node]*nodeState, paths map[node]pathImpedance, classCodes map[string]string, nominalVoltageV float64) []faultNodeResult {
	nodes := make([]faultNodeResult, 0, len(tr.order))
	for _, n := range tr.order {
		if _, ok := states[n]; !ok {
			continue
		}
		p := paths[n]
		impedanceOhm := math.Hypot(p.rOhm, p.xOhm)

		res := faultNodeResult{
			FeatureClass:     classCodes[n.classID],
			FeatureID:        n.featureID,
			ResistanceOhm:    p.rOhm,
			ReactanceOhm:     p.xOhm,
			ImpedanceOhm:     impedanceOhm,
			MaterialResolved: p.resolved,
		}
		if impedanceOhm > 0 {
			faultCurrentAmps := nominalVoltageV / impedanceOhm
			res.FaultCurrentAmps = &faultCurrentAmps
		}
		nodes = append(nodes, res)
	}
	return nodes
}
