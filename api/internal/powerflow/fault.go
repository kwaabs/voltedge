package powerflow

import (
	"context"
	"math"

	"github.com/uptrace/bun"
)

// runFaultCurrent computes the available symmetrical (3-phase bolted)
// fault current at every node downstream of Start -- reusing
// runLoadFlow's own tree-walk, barrier handling, and per-node branch-
// impedance resolution directly (resolveNode, loadFlowBarriers,
// buildTree, resolveNodeStates, resolveSourceImpedance), but not its
// forward-backward sweep: fault current depends only on the network's
// own topology and impedance, never on what demand is drawing through
// it, so there's no demand-voltage circular dependency to iterate over
// -- one forward pass summing each node's own R+jX onto its parent's
// already-accumulated total is exact, not an approximation refined
// over sweepIterations. See docs/phase-8-fault-current.md.
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

	states, err := resolveNodeStates(ctx, db, tr, loader)
	if err != nil {
		return nil, err
	}

	var sourceR, sourceX float64
	if startState := states[start]; startState != nil {
		sourceR, sourceX = resolveSourceImpedance(startState.attrs)
	}

	allNodes := append(append([]node{}, tr.order...), barrierHits...)
	classCodes, err := nodeClassCodes(ctx, db, allNodes)
	if err != nil {
		return nil, err
	}

	nodes := formatFaultResults(tr, states, classCodes, start, sourceR, sourceX, req.NominalVoltageV)

	return &faultCurrentResponse{
		Start:           req.Start,
		NominalVoltageV: req.NominalVoltageV,
		Nodes:           nodes,
		Barriers:        toFeatureRefs(barrierHits, classCodes),
	}, nil
}

// formatFaultResults walks tr.order (BFS discovery order -- a parent
// is always discovered before its children, the same guarantee
// backwardSweep relies on in reverse) accumulating each node's own
// series R+jX onto its parent's already-accumulated total, starting
// from the source's own impedance at Start. cumR/cumX are kept local
// to this pass -- a genuinely different accumulation (series
// impedance to this point) from nodeState's own cumKW/cumKVAR
// (downstream demand), so there's no reason to fold it into that
// struct.
func formatFaultResults(tr *tree, states map[node]*nodeState, classCodes map[string]string, start node, sourceR, sourceX float64, nominalVoltageV float64) []faultNodeResult {
	cumR := make(map[node]float64, len(tr.order))
	cumX := make(map[node]float64, len(tr.order))
	nodes := make([]faultNodeResult, 0, len(tr.order))

	for _, n := range tr.order {
		st := states[n]
		if st == nil {
			continue
		}

		var rOhm, xOhm float64
		if n == start {
			rOhm, xOhm = sourceR, sourceX
		} else {
			rOhm, xOhm = cumR[tr.parent[n]], cumX[tr.parent[n]]
			if st.impedance.resolved {
				rOhm += st.impedance.rOhm
				xOhm += st.impedance.xOhm
			}
		}
		cumR[n], cumX[n] = rOhm, xOhm

		zOhm := math.Hypot(rOhm, xOhm)
		var faultCurrentAmps *float64
		if zOhm > 0 {
			amps := nominalVoltageV / zOhm
			faultCurrentAmps = &amps
		}

		nodes = append(nodes, faultNodeResult{
			FeatureClass:     classCodes[n.classID],
			FeatureID:        n.featureID,
			ROhmTotal:        rOhm,
			XOhmTotal:        xOhm,
			ZOhmTotal:        zOhm,
			FaultCurrentAmps: faultCurrentAmps,
			MaterialResolved: st.impedance.resolved,
		})
	}
	return nodes
}
