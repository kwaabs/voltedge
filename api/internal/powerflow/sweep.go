package powerflow

import (
	"context"
	"math"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// sweepIterations mirrors the "2-3 iterations" this package's own doc
// comment promises -- each pass only refines which node's own voltage
// a branch's ΔV is measured against (the standard "improved" linear
// distribution load flow), since demand itself is constant-power and
// doesn't change between passes. Fixed rather than a convergence
// tolerance: at the drop magnitudes a real distribution feeder sees,
// three passes over-converges already: a tolerance loop would just be
// unobservable complexity for this pass.
const sweepIterations = 3

// nodeState is the sweep's own per-node working state, filled in
// during resolution, then the backward pass, then the forward pass.
type nodeState struct {
	loaded    *metadata.Loaded
	attrs     map[string]any
	impedance conductorImpedance
	ownKW     float64
	ownKVAR   float64
	cumKW     float64
	cumKVAR   float64
	voltageV  float64
	ratedKVA  float64
	hasRating bool
}

// runLoadFlow is this package's whole engine, POST /analysis/load-flow's
// single entry point: resolve the start feature, walk it into a tree
// (rejecting a non-radial section outright), resolve every node's own
// electrical inputs, sweep backward to accumulate demand, sweep
// forward to derive voltage, and format the result.
func runLoadFlow(ctx context.Context, db bun.IDB, req loadFlowRequest) (*loadFlowResponse, error) {
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

	backwardSweep(tr, states)

	var sourceR, sourceX float64
	if startState := states[start]; startState != nil {
		sourceR, sourceX = resolveSourceImpedance(startState.attrs)
	}
	forwardSweep(tr, states, start, req.NominalVoltageV, sourceR, sourceX)

	allNodes := append(append([]node{}, tr.order...), barrierHits...)
	classCodes, err := nodeClassCodes(ctx, db, allNodes)
	if err != nil {
		return nil, err
	}

	nodes, overloaded := formatResults(tr, states, classCodes, req.NominalVoltageV)

	return &loadFlowResponse{
		Start:           req.Start,
		NominalVoltageV: req.NominalVoltageV,
		Iterations:      sweepIterations,
		Nodes:           nodes,
		Barriers:        toFeatureRefs(barrierHits, classCodes),
		Overloaded:      overloaded,
	}, nil
}

// resolveNodeStates loads every visited node's live feature row once
// and resolves its own electrical inputs (branch impedance if it's a
// conductor, demand if it carries any) -- everything the backward and
// forward passes need, computed up front rather than re-queried each
// iteration.
func resolveNodeStates(ctx context.Context, db bun.IDB, tr *tree, loader *featureClassLoader) (map[node]*nodeState, error) {
	states := make(map[node]*nodeState, len(tr.order))
	for _, n := range tr.order {
		loaded, err := loader.loadedFor(ctx, db, n.classID)
		if err != nil {
			return nil, err
		}
		row, err := crud.GetFeature(ctx, db, loaded, n.featureID)
		if err != nil {
			if crud.IsNotFoundError(err) {
				// A dangling connectivity row pointing at a since-purged
				// feature -- skip it, same as trace's own checkBarrier
				// treats this as a dead end rather than a failed run.
				continue
			}
			return nil, err
		}
		attrs, _ := row["attributes"].(map[string]any)

		var imp conductorImpedance
		if loaded.FeatureClass.NetworkRole == "edge" {
			imp, err = resolveConductorImpedance(ctx, db, loaded, n, attrs)
			if err != nil {
				return nil, err
			}
		}
		ownKW, ownKVAR := resolveDemand(attrs)
		ratedKVA, hasRating := resolveTransformerRating(attrs)

		states[n] = &nodeState{
			loaded: loaded, attrs: attrs, impedance: imp, ownKW: ownKW, ownKVAR: ownKVAR,
			ratedKVA: ratedKVA, hasRating: hasRating,
		}
	}
	return states, nil
}

// backwardSweep accumulates each node's own demand plus everything
// downstream of it. tr.order is BFS discovery order (non-decreasing
// depth); walking it in reverse guarantees every child is processed
// before its parent, since a child is always discovered strictly after
// its parent -- the standard backward pass of Kersting's method.
func backwardSweep(tr *tree, states map[node]*nodeState) {
	for i := len(tr.order) - 1; i >= 0; i-- {
		st, ok := states[tr.order[i]]
		if !ok {
			continue
		}
		st.cumKW, st.cumKVAR = st.ownKW, st.ownKVAR
		for _, c := range tr.children[tr.order[i]] {
			if cs, ok := states[c]; ok {
				st.cumKW += cs.cumKW
				st.cumKVAR += cs.cumKVAR
			}
		}
	}
}

// forwardSweep derives each node's own voltage, sweepIterations times
// over (each pass re-measuring a branch's ΔV against the previous
// pass's own voltage at the sending end, refining nothing else --
// demand is fixed by backwardSweep already). The start node's own
// voltage absorbs the source's impedance (if any) against the whole
// network's total demand, once; every other node's voltage is its
// parent's voltage minus its own branch's drop (zero for a
// non-conductor node -- electrically pass-through) computed against
// its own cumulative downstream demand.
func forwardSweep(tr *tree, states map[node]*nodeState, start node, nominalVoltageV, sourceR, sourceX float64) {
	for iter := 0; iter < sweepIterations; iter++ {
		for _, n := range tr.order {
			st := states[n]
			if st == nil {
				continue
			}
			if n == start {
				st.voltageV = nominalVoltageV - approxDropV(st.cumKW, st.cumKVAR, sourceR, sourceX, nominalVoltageV)
				continue
			}

			parentState := states[tr.parent[n]]
			if parentState == nil {
				st.voltageV = nominalVoltageV
				continue
			}
			if st.impedance.resolved {
				st.voltageV = parentState.voltageV - approxDropV(st.cumKW, st.cumKVAR, st.impedance.rOhm, st.impedance.xOhm, parentState.voltageV)
			} else {
				st.voltageV = parentState.voltageV
			}
		}
	}
}

// approxDropV is the standard linearized distribution voltage-drop
// approximation, ΔV(volts) ≈ (P·R + Q·X) / V -- the industry-standard
// simplification for a planning-level radial feeder calc, accurate to
// a fraction of a percent at the drop magnitudes a well-run
// distribution feeder actually sees (exact would need the branch's own
// current angle, not just magnitude -- unnecessary precision for this
// slice). kW/kVAR are converted to watts/VAR before dividing.
func approxDropV(cumKW, cumKVAR, rOhm, xOhm, refVoltageV float64) float64 {
	if refVoltageV <= 0 {
		return 0
	}
	return (cumKW*1000*rOhm + cumKVAR*1000*xOhm) / refVoltageV
}

// approxCurrentAmps derives a branch's own current from its cumulative
// apparent power and the voltage at its sending end: I = S / V, where
// S = sqrt(P² + Q²) in VA.
func approxCurrentAmps(cumKW, cumKVAR, voltageV float64) float64 {
	if voltageV <= 0 {
		return 0
	}
	sVA := math.Hypot(cumKW*1000, cumKVAR*1000)
	return sVA / voltageV
}

// formatResults turns the swept nodeStates into the response shape,
// flagging (into Overloaded) any conductor node whose derived current
// exceeds its own resolved ampacity -- a client shouldn't have to
// re-scan Nodes itself to find what actually needs attention.
func formatResults(tr *tree, states map[node]*nodeState, classCodes map[string]string, nominalVoltageV float64) ([]nodeResult, []featureRef) {
	nodes := make([]nodeResult, 0, len(tr.order))
	overloaded := make([]featureRef, 0)

	for _, n := range tr.order {
		st := states[n]
		if st == nil {
			continue
		}
		res := nodeResult{
			FeatureClass:     classCodes[n.classID],
			FeatureID:        n.featureID,
			VoltageV:         st.voltageV,
			VoltageDropPct:   (nominalVoltageV - st.voltageV) / nominalVoltageV * 100,
			CumulativeKW:     st.cumKW,
			CumulativeKVAR:   st.cumKVAR,
			MaterialResolved: st.impedance.resolved,
		}

		if st.loaded.FeatureClass.NetworkRole == "edge" {
			currentAmps := approxCurrentAmps(st.cumKW, st.cumKVAR, st.voltageV)
			res.CurrentAmps = &currentAmps
			if st.impedance.ampacityAmps != nil {
				ampacity := *st.impedance.ampacityAmps
				res.AmpacityAmps = &ampacity
				loadingPct := currentAmps / ampacity * 100
				res.LoadingPct = &loadingPct
				if loadingPct > 100 {
					overloaded = append(overloaded, featureRef{FeatureClass: res.FeatureClass, FeatureID: res.FeatureID})
				}
			}
		} else if st.hasRating {
			sKVA := math.Hypot(st.cumKW, st.cumKVAR)
			ratedKVA := st.ratedKVA
			res.RatedKVA = &ratedKVA
			loadingPct := sKVA / ratedKVA * 100
			res.LoadingPct = &loadingPct
			if loadingPct > 100 {
				overloaded = append(overloaded, featureRef{FeatureClass: res.FeatureClass, FeatureID: res.FeatureID})
			}
		}
		nodes = append(nodes, res)
	}
	return nodes, overloaded
}
