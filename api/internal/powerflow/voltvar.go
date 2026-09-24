package powerflow

import (
	"context"
	"math"

	"github.com/uptrace/bun"
)

// voltVarRequest is POST /analysis/volt-var's body. Source and
// NominalVoltageV are the feeder slack and its sending-end voltage, exactly
// as load-flow / voltage-profile / hosting take them. The study sweeps the
// feeder as it stands (base case), then for each SWITCHABLE capacitor bank
// that is currently OUT of circuit re-sweeps it back IN and reports the
// voltage lift and loss reduction closing it would buy -- the "should I
// switch this bank on?" volt-VAR question. Fixed banks (and switched banks
// already CLOSED) are already reflected in the base sweep and are reported
// as active context, not recommendations.
//
// Min/MaxVoltagePu are the acceptable band (defaulting to ANSI C84.1 Range A
// 0.95..1.05); only the lower bound matters here, since shunt capacitors
// raise voltage and the point is relieving under-voltage.
type voltVarRequest struct {
	Source          featureRef `json:"source"`
	NominalVoltageV float64    `json:"nominal_voltage_v"`
	MinVoltagePu    *float64   `json:"min_voltage_pu"`
	MaxVoltagePu    *float64   `json:"max_voltage_pu"`
}

// voltVarMetrics is the feeder-level state after one sweep: the worst
// (lowest) per-unit voltage anywhere and where, the total series I²R loss,
// the source real/reactive draw and the power factor that implies, and how
// many nodes sit below the lower voltage band. LossesComplete is false when
// some branch had no resolved R/X, so the loss total is a lower bound (same
// caveat as voltage-profile).
type voltVarMetrics struct {
	MinVoltagePu   float64     `json:"min_voltage_pu"`
	WorstNode      *featureRef `json:"worst_node,omitempty"`
	TotalLossKW    float64     `json:"total_loss_kw"`
	SourceKW       float64     `json:"source_kw"`
	SourceKVAR     float64     `json:"source_kvar"`
	SourcePF       float64     `json:"source_pf"`
	ViolationCount int         `json:"violation_count"`
	LossesComplete bool        `json:"losses_complete"`
}

// capBank is one capacitor bank found on the feeder. RatedKVAR is its
// nameplate reactive output. Switchable is true for a switched bank (carries
// a device_state); a fixed bank is not switchable. Active is whether it is
// currently in circuit and injecting (a fixed bank always, a switched bank
// only while CLOSED) -- an active bank is already reflected in the base
// sweep, an inactive switched bank is a switching candidate.
type capBank struct {
	Node       featureRef `json:"node"`
	RatedKVAR  float64    `json:"rated_kvar"`
	Switchable bool       `json:"switchable"`
	Active     bool       `json:"active"`
}

// voltVarRecommendation is one switchable, currently-out bank evaluated as a
// candidate to switch IN: the feeder's worst voltage and total loss before
// and after closing it, the voltage lift (per unit) and loss reduction (kW)
// that buys, the source power factor before/after, and whether closing it
// clears every under-voltage violation the base case had. A negative
// LossReductionKW (loss went up) or VoltageRisePu is possible where a bank is
// badly oversized for the feeder -- reported honestly rather than clamped, so
// an over-compensating bank shows as the disbenefit it is.
type voltVarRecommendation struct {
	Node                 featureRef `json:"node"`
	RatedKVAR            float64    `json:"rated_kvar"`
	MinVoltagePuFrom     float64    `json:"min_voltage_pu_from"`
	MinVoltagePuTo       float64    `json:"min_voltage_pu_to"`
	VoltageRisePu        float64    `json:"voltage_rise_pu"`
	TotalLossKWFrom      float64    `json:"total_loss_kw_from"`
	TotalLossKWTo        float64    `json:"total_loss_kw_to"`
	LossReductionKW      float64    `json:"loss_reduction_kw"`
	SourcePFFrom         float64    `json:"source_pf_from"`
	SourcePFTo           float64    `json:"source_pf_to"`
	ResolvesUnderVoltage bool       `json:"resolves_under_voltage"`
}

// voltVarResponse is the full study: the assumed slack / band, the base-case
// feeder metrics, every capacitor bank found (with its active/switchable
// state), and one recommendation per switchable out-of-circuit bank.
type voltVarResponse struct {
	Source          featureRef              `json:"source"`
	NominalVoltageV float64                 `json:"nominal_voltage_v"`
	MinVoltagePu    float64                 `json:"min_voltage_pu"`
	MaxVoltagePu    float64                 `json:"max_voltage_pu"`
	Base            voltVarMetrics          `json:"base"`
	Banks           []capBank               `json:"banks"`
	Recommendations []voltVarRecommendation `json:"recommendations"`
}

// voltVarStudy holds the once-resolved feeder so each candidate switching is a
// pure in-memory re-sweep -- the same resolve-once pattern hosting.go uses.
type voltVarStudy struct {
	tr         *tree
	states     map[node]*nodeState
	start      node
	classCodes map[string]string
	nominalV   float64
	sourceR    float64
	sourceX    float64
	minPu      float64
}

// runVoltVar is POST /analysis/volt-var: a shunt-capacitor volt-VAR study. It
// resolves the feeder once, sweeps the base case, then for each switched
// capacitor bank that is currently out of circuit re-sweeps it in and reports
// the voltage lift and loss reduction switching it on would buy. It adds no
// new electrical model -- a capacitor is negative reactive demand (the sweep
// already carries signed Q via resolveCapacitor at the netting site), and the
// worst-voltage / loss / power-factor read-outs are the same ones
// voltage-profile computes off the swept states. Read-only.
func runVoltVar(ctx context.Context, db bun.IDB, req voltVarRequest) (*voltVarResponse, error) {
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
	if req.NominalVoltageV <= 0 {
		return nil, invalid("nominal_voltage_v must be a positive number")
	}

	// Resolve the feeder ONCE: walk the tree and load every node's electrical
	// inputs. Every candidate switching below re-runs only the in-memory sweep.
	start, err := resolveNode(ctx, db, req.Source)
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
	var sourceR, sourceX float64
	if startState := states[start]; startState != nil {
		sourceR, sourceX = resolveSourceImpedance(startState.attrs)
	}
	classCodes, err := nodeClassCodes(ctx, db, append(append([]node{}, tr.order...), barrierHits...))
	if err != nil {
		return nil, err
	}

	study := &voltVarStudy{
		tr: tr, states: states, start: start, classCodes: classCodes,
		nominalV: req.NominalVoltageV, sourceR: sourceR, sourceX: sourceX, minPu: minPu,
	}

	// Base case: sweep the feeder as it stands (fixed banks and any already-
	// CLOSED switched banks are already netted in by resolveCapacitor).
	base := study.sweepMetrics()

	// Enumerate every capacitor bank on the feeder (a node carrying rated_kvar)
	// and split into active context vs switching candidates.
	banks := make([]capBank, 0)
	type candidate struct {
		n     node
		rated float64
	}
	var candidates []candidate
	for _, n := range tr.order {
		st := states[n]
		if st == nil {
			continue
		}
		rated, ok := readFloatAttr(st.attrs, capFieldRatedKVAR)
		if !ok || rated <= 0 {
			continue
		}
		_, switchable := readStringAttr(st.attrs, capFieldDeviceState)
		// resolveCapacitor returns the in-circuit injection (>0) only for a
		// bank that is actually supplying VARs right now.
		active := resolveCapacitor(st.attrs) > 0
		ref := featureRef{FeatureClass: classCodes[n.classID], FeatureID: n.featureID}
		banks = append(banks, capBank{Node: ref, RatedKVAR: rated, Switchable: switchable, Active: active})
		// A switchable bank that is currently out of circuit is a candidate to
		// switch on. (A fixed bank can't be switched; an already-active bank is
		// already in the base case.)
		if switchable && !active {
			candidates = append(candidates, candidate{n: n, rated: rated})
		}
	}

	recs := make([]voltVarRecommendation, 0, len(candidates))
	for _, c := range candidates {
		after := study.evaluateClose(c.n, c.rated)
		ref := featureRef{FeatureClass: classCodes[c.n.classID], FeatureID: c.n.featureID}
		recs = append(recs, voltVarRecommendation{
			Node:                 ref,
			RatedKVAR:            c.rated,
			MinVoltagePuFrom:     base.MinVoltagePu,
			MinVoltagePuTo:       after.MinVoltagePu,
			VoltageRisePu:        after.MinVoltagePu - base.MinVoltagePu,
			TotalLossKWFrom:      base.TotalLossKW,
			TotalLossKWTo:        after.TotalLossKW,
			LossReductionKW:      base.TotalLossKW - after.TotalLossKW,
			SourcePFFrom:         base.SourcePF,
			SourcePFTo:           after.SourcePF,
			ResolvesUnderVoltage: base.ViolationCount > 0 && after.ViolationCount == 0,
		})
	}

	// The base sweep is left in the states from the last candidate's restore +
	// re-sweep in evaluateClose; re-run it so any downstream reader sees the
	// true base case. (Not strictly needed for the response, which is already
	// built, but keeps the study reusable and matches hosting's contract that
	// evaluate restores.)
	study.sweepMetrics()

	return &voltVarResponse{
		Source:          req.Source,
		NominalVoltageV: req.NominalVoltageV,
		MinVoltagePu:    minPu,
		MaxVoltagePu:    maxPu,
		Base:            base,
		Banks:           banks,
		Recommendations: recs,
	}, nil
}

// sweepMetrics runs the radial sweep over the current states and reads the
// feeder-level metrics off it -- worst per-unit voltage, total series loss,
// source draw / power factor, and the under-voltage violation count. It is the
// voltage-profile summary computation, factored so the base case and every
// candidate switching share one code path.
func (s *voltVarStudy) sweepMetrics() voltVarMetrics {
	backwardSweep(s.tr, s.states)
	forwardSweep(s.tr, s.states, s.start, s.nominalV, s.sourceR, s.sourceX)

	m := voltVarMetrics{MinVoltagePu: math.Inf(1), LossesComplete: true}
	for _, n := range s.tr.order {
		st := s.states[n]
		if st == nil {
			continue
		}
		if st.voltageBase > 0 {
			pu := st.voltageV / st.voltageBase
			if pu < m.MinVoltagePu {
				m.MinVoltagePu = pu
				ref := featureRef{FeatureClass: s.classCodes[n.classID], FeatureID: n.featureID}
				m.WorstNode = &ref
			}
			if pu < s.minPu {
				m.ViolationCount++
			}
		}
		if st.loaded.FeatureClass.NetworkRole == "edge" {
			if st.impedance.resolved {
				current := approxCurrentAmps(st.cumKW, st.cumKVAR, st.voltageV)
				m.TotalLossKW += current * current * st.impedance.rOhm / 1000
			} else {
				m.LossesComplete = false
			}
		}
	}
	// A feeder that never forward-swept a single based node has no meaningful
	// worst voltage -- report 0 rather than +Inf.
	if math.IsInf(m.MinVoltagePu, 1) {
		m.MinVoltagePu = 0
	}
	if startState := s.states[s.start]; startState != nil {
		m.SourceKW = startState.cumKW
		m.SourceKVAR = startState.cumKVAR
	}
	m.SourcePF = powerFactorOf(m.SourceKW, m.SourceKVAR)
	return m
}

// evaluateClose re-sweeps the feeder with bank c switched IN -- its rated_kvar
// netted as leading (negative) reactive demand at its node -- and reads the
// resulting metrics. It mutates the bank's own reactive demand for the sweep
// and restores it before returning, so candidates don't contaminate each
// other, exactly as hosting.evaluate does for its trial injections.
func (s *voltVarStudy) evaluateClose(c node, ratedKVAR float64) voltVarMetrics {
	st := s.states[c]
	if st == nil {
		return s.sweepMetrics()
	}
	savedKVAR := st.ownKVAR
	// A capacitor supplies leading VARs: subtract its output from the node's own
	// reactive demand so the backward sweep nets it against everything upstream.
	st.ownKVAR = savedKVAR - ratedKVAR
	m := s.sweepMetrics()
	st.ownKVAR = savedKVAR
	return m
}

// powerFactorOf returns |P| / |S| for a P + jQ draw, the (unsigned) power
// factor at a point. A zero apparent power is reported as unity (a de-energized
// or no-load point has no meaningful pf; unity is the harmless default).
func powerFactorOf(kw, kvar float64) float64 {
	s := math.Hypot(kw, kvar)
	if s == 0 {
		return 1
	}
	return math.Abs(kw) / s
}
