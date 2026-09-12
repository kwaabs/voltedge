package powerflow

import (
	"context"
	"sort"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/uptrace/bun"
)

// normalStateField / nopValue are Phase 48's declared-open-point field
// (seed/switch_normal_state_fields.sql). This engine reads normal_state ONLY to
// rank candidate ties -- a NORMALLY_OPEN point is a purpose-built tie and so is
// offered first -- never to decide flow: the live device_state is what actually
// bars a trace, and this study overrides exactly that (per candidate) to model
// closing the tie. This is normal_state's first engine consumer.
const (
	normalStateField = "normal_state"
	nopValue         = "NORMALLY_OPEN"
)

// contingencyRequest is POST /analysis/contingency's body. Source +
// NominalVoltageV are the donor feeder's slack and sending-end voltage, exactly
// as load-flow/voltage-profile take them: the study sweeps the donor feeder as
// it stands, then re-sweeps it with one normally-open tie hypothetically closed
// to see what outaged section the donor could pick up and at what cost.
//
// Tie optionally names one specific open point to close; left nil, the study
// evaluates EVERY open boundary switch the base sweep halted at (the feeder's
// candidate restoration ties), ranked normally-open-first -- the "consider every
// open boundary switch, rank NORMALLY_OPEN first" posture. MinVoltagePu/
// MaxVoltagePu are the acceptable post-transfer voltage band (defaulting to ANSI
// C84.1 Range A, 0.95..1.05), the same judging Slice 1 uses.
type contingencyRequest struct {
	Source          featureRef  `json:"source"`
	NominalVoltageV float64     `json:"nominal_voltage_v"`
	Tie             *featureRef `json:"tie,omitempty"`
	MinVoltagePu    *float64    `json:"min_voltage_pu"`
	MaxVoltagePu    *float64    `json:"max_voltage_pu"`
}

// restorationOption is one candidate tie's result: close this tie and the donor
// feeder picks up RestoredNodes (RestoredLoadKW/KVAR of new demand), reaching a
// worst post-transfer per-unit voltage of MinVoltagePu at WorstNode. Violations
// are the newly-swept nodes that fall outside the voltage band; Overloaded are
// the branches/transformers whose loading exceeds 100 % once the load transfers.
// Feasible is true when the transfer restores load AND introduces no voltage
// violation or overload -- the switching move an operator can make as-is. Note
// carries the reason an option is infeasible or trivial (a loop, or no load
// gained). NormalState is the tie's declared posture (why it ranked where it
// did): NORMALLY_OPEN ties -- purpose-built standby paths -- sort first.
type restorationOption struct {
	Tie               featureRef   `json:"tie"`
	NormalState       string       `json:"normal_state,omitempty"`
	RestoredLoadKW    float64      `json:"restored_load_kw"`
	RestoredLoadKVAR  float64      `json:"restored_load_kvar"`
	RestoredNodeCount int          `json:"restored_node_count"`
	RestoredNodes     []featureRef `json:"restored_nodes"`
	MinVoltagePu      float64      `json:"min_voltage_pu"`
	WorstNode         *featureRef  `json:"worst_node,omitempty"`
	Violations        []featureRef `json:"violations"`
	Overloaded        []featureRef `json:"overloaded"`
	Feasible          bool         `json:"feasible"`
	Note              string       `json:"note,omitempty"`
}

// contingencyResponse is the full study: the donor feeder's base load and reach,
// the judging band, and one ranked restoration option per candidate tie.
type contingencyResponse struct {
	Source          featureRef          `json:"source"`
	NominalVoltageV float64             `json:"nominal_voltage_v"`
	MinVoltagePu    float64             `json:"min_voltage_pu"`
	MaxVoltagePu    float64             `json:"max_voltage_pu"`
	BaseLoadKW      float64             `json:"base_load_kw"`
	BaseLoadKVAR    float64             `json:"base_load_kvar"`
	BaseNodeCount   int                 `json:"base_node_count"`
	Options         []restorationOption `json:"options"`
}

// runContingency is POST /analysis/contingency: an N-1 / backfeed restoration
// study. It sweeps the donor feeder as it stands (the base case), then for each
// candidate normally-open tie re-sweeps the donor with that tie hypothetically
// closed (via runSweep's closed override -- no connectivity is mutated) and
// reports the load the donor would pick up and any post-transfer voltage
// violation or thermal overload. It computes nothing electrical the sweep
// doesn't already: restored load is the delta in the source's accumulated
// demand, violations reuse Slice 1's band judging, overloads reuse load-flow's
// own loading check (formatResults). Read-only.
func runContingency(ctx context.Context, db bun.IDB, req contingencyRequest) (*contingencyResponse, error) {
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

	// Base case: the donor feeder as it stands (every tie live/open). Its barrier
	// hits are exactly the open boundary switches that a transfer could close.
	base, err := runSweep(ctx, db, req.Source, req.NominalVoltageV, nil, nil)
	if err != nil {
		return nil, err
	}
	baseReached := nodeSet(base.tr.order)
	var baseLoadKW, baseLoadKVAR float64
	if st := base.states[base.start]; st != nil {
		baseLoadKW, baseLoadKVAR = st.cumKW, st.cumKVAR
	}

	loader := newFeatureClassLoader()

	// Candidate ties. An explicit tie is evaluated on its own; otherwise every
	// open boundary switch the base sweep halted at is a candidate.
	var candidates []node
	if req.Tie != nil {
		n, err := resolveNode(ctx, db, *req.Tie)
		if err != nil {
			return nil, err
		}
		candidates = []node{n}
	} else {
		candidates = append(candidates, base.barrierHits...)
	}

	// Read each candidate's declared normal_state and rank NORMALLY_OPEN first
	// (a purpose-built standby path), then by class/id for a stable order.
	states := make(map[node]string, len(candidates))
	for _, c := range candidates {
		states[c] = readNormalState(ctx, db, c, loader)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		ai := states[candidates[i]] == nopValue
		aj := states[candidates[j]] == nopValue
		if ai != aj {
			return ai // NORMALLY_OPEN before anything else
		}
		if candidates[i].classID != candidates[j].classID {
			return candidates[i].classID < candidates[j].classID
		}
		return candidates[i].featureID < candidates[j].featureID
	})

	options := make([]restorationOption, 0, len(candidates))
	for _, c := range candidates {
		opt := restorationOption{
			Tie:         featureRef{FeatureClass: base.classCodes[c.classID], FeatureID: c.featureID},
			NormalState: states[c],
			Violations:  []featureRef{},
			Overloaded:  []featureRef{},
		}
		// A tie the base sweep never halted at (an explicit pick that is already
		// closed, or not reachable from this source) can't restore anything by
		// closing it -- say so rather than silently reporting a no-op.
		if req.Tie != nil && !isBarrierHit(base.barrierHits, c) {
			opt.Note = "this feature is not an open boundary point reachable from the source -- closing it changes nothing from this feeder"
			options = append(options, opt)
			continue
		}

		swept, err := runSweep(ctx, db, req.Source, req.NominalVoltageV, map[node]bool{c: true}, nil)
		if err != nil {
			// Closing the tie may join the feeder to itself through a second path
			// -- a loop/mesh buildTree rejects. That is a real (and useful) result:
			// this tie is a parallel path, not a radial transfer. Report it as an
			// infeasible option rather than failing the whole study; any other
			// error is a genuine failure.
			if isValidationError(err) {
				opt.Note = "closing this tie forms a loop/mesh with the existing network (a parallel path, not a radial transfer) -- run LOOP_CHECK_TRACE"
				options = append(options, opt)
				continue
			}
			return nil, err
		}

		// Restored load = the extra demand the source now accumulates with the tie
		// closed; restored nodes = everything newly reached past the tie.
		if st := swept.states[swept.start]; st != nil {
			opt.RestoredLoadKW = st.cumKW - baseLoadKW
			opt.RestoredLoadKVAR = st.cumKVAR - baseLoadKVAR
		}
		restored := make([]featureRef, 0)
		for _, n := range swept.tr.order {
			if !baseReached[n] {
				restored = append(restored, featureRef{FeatureClass: swept.classCodes[n.classID], FeatureID: n.featureID})
			}
		}
		opt.RestoredNodes = restored
		opt.RestoredNodeCount = len(restored)

		// Post-transfer overloads reuse load-flow's own loading check; undervoltage
		// / overvoltage reuse Slice 1's band judging over this swept state.
		_, overloaded := formatResults(swept.tr, swept.states, swept.classCodes)
		if overloaded == nil {
			overloaded = []featureRef{}
		}
		opt.Overloaded = overloaded

		worstPu := 0.0
		var worst *featureRef
		violations := []featureRef{}
		for _, n := range swept.tr.order {
			st := swept.states[n]
			if st == nil || st.voltageBase <= 0 {
				continue
			}
			pu := st.voltageV / st.voltageBase
			ref := featureRef{FeatureClass: swept.classCodes[n.classID], FeatureID: n.featureID}
			if pu < minPu || pu > maxPu {
				violations = append(violations, ref)
			}
			if worst == nil || pu < worstPu {
				worstPu = pu
				w := ref
				worst = &w
			}
		}
		opt.Violations = violations
		opt.MinVoltagePu = worstPu
		opt.WorstNode = worst

		switch {
		case opt.RestoredNodeCount == 0:
			opt.Note = "closing this tie energizes no additional load (it dead-ends immediately or the section beyond is already reachable)"
		case len(violations) == 0 && len(overloaded) == 0:
			opt.Feasible = true
		}
		options = append(options, opt)
	}

	return &contingencyResponse{
		Source:          req.Source,
		NominalVoltageV: req.NominalVoltageV,
		MinVoltagePu:    minPu,
		MaxVoltagePu:    maxPu,
		BaseLoadKW:      baseLoadKW,
		BaseLoadKVAR:    baseLoadKVAR,
		BaseNodeCount:   len(base.tr.order),
		Options:         options,
	}, nil
}

// nodeSet turns a node slice into a membership set.
func nodeSet(nodes []node) map[node]bool {
	out := make(map[node]bool, len(nodes))
	for _, n := range nodes {
		out[n] = true
	}
	return out
}

// isBarrierHit reports whether n is one of the base sweep's barrier hits.
func isBarrierHit(hits []node, n node) bool {
	for _, h := range hits {
		if h == n {
			return true
		}
	}
	return false
}

// readNormalState reads a node's declared normal_state ("" when the class has no
// such field or it's unset), used only to rank candidate ties. A not-found /
// unreadable row simply reads as "" -- ranking degrades to id order, never an
// error, since this is a display/ordering hint, not a solve input.
func readNormalState(ctx context.Context, db bun.IDB, n node, loader *featureClassLoader) string {
	loaded, err := loader.loadedFor(ctx, db, n.classID)
	if err != nil {
		return ""
	}
	row, err := crud.GetFeature(ctx, db, loaded, n.featureID)
	if err != nil {
		return ""
	}
	attrs, _ := row["attributes"].(map[string]any)
	v, _ := attrs[normalStateField].(string)
	return v
}
