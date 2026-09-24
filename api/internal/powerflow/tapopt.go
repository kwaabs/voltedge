package powerflow

import (
	"context"
	"math"
	"strings"

	"github.com/uptrace/bun"
)

// defaultMaxTapSteps is the ± tap range a step voltage regulator is swept over
// when the request does not pin one. Sixteen steps at the common 0.625% step is
// the classic ±10% ANSI regulator range, so a plain request explores a real
// regulator's full authority.
const defaultMaxTapSteps = 16

// tapOptRequest is POST /analysis/tap-optimization's body. Source and
// NominalVoltageV are the feeder slack and its sending-end voltage, exactly as
// the other feeder studies take them. The study sweeps the feeder as it stands
// (base case), then for each series step voltage regulator on the feeder tries
// every tap position in ±MaxTapSteps and recommends the one that best holds the
// whole feeder inside the voltage band -- the "where should this regulator sit?"
// question. Min/MaxVoltagePu are the band (default ANSI C84.1 Range A
// 0.95..1.05); unlike volt-VAR, BOTH bounds matter, since a regulator can buck
// to relieve over-voltage as well as boost to relieve under-voltage.
type tapOptRequest struct {
	Source          featureRef `json:"source"`
	NominalVoltageV float64    `json:"nominal_voltage_v"`
	MinVoltagePu    *float64   `json:"min_voltage_pu"`
	MaxVoltagePu    *float64   `json:"max_voltage_pu"`
	MaxTapSteps     *int       `json:"max_tap_steps"`
}

// tapMetrics is the feeder-level state after one sweep, read for a regulator
// study: the worst (lowest) and highest per-unit voltage and where each is, the
// total series I²R loss, the source draw / power factor, and how many nodes sit
// below (Under) or above (Over) the band. It differs from voltVarMetrics in
// tracking BOTH voltage extremes, because a regulator acts on the whole band.
type tapMetrics struct {
	MinVoltagePu   float64     `json:"min_voltage_pu"`
	WorstLowNode   *featureRef `json:"worst_low_node,omitempty"`
	MaxVoltagePu   float64     `json:"max_voltage_pu"`
	WorstHighNode  *featureRef `json:"worst_high_node,omitempty"`
	TotalLossKW    float64     `json:"total_loss_kw"`
	SourceKW       float64     `json:"source_kw"`
	SourceKVAR     float64     `json:"source_kvar"`
	SourcePF       float64     `json:"source_pf"`
	UnderCount     int         `json:"under_count"`
	OverCount      int         `json:"over_count"`
	LossesComplete bool        `json:"losses_complete"`
}

// regulatorInfo is one step voltage regulator found on the feeder: its node,
// per-step boost percentage, live tap position and the ratio that implies, and
// whether it is currently bypassed (device_state other than CLOSED -- in bypass
// it passes voltage straight through until it is put back in service).
type regulatorInfo struct {
	Node       featureRef `json:"node"`
	StepPct    float64    `json:"step_pct"`
	CurrentTap float64    `json:"current_tap"`
	RatioNow   float64    `json:"ratio_now"`
	Bypassed   bool       `json:"bypassed"`
}

// tapRecommendation is one regulator evaluated across its tap range: the tap
// (and ratio) it sits at now versus the tap the study picked, the worst-low and
// worst-high per-unit voltage before and after, the net worst-voltage movement,
// the loss and source power factor before/after, and the band-violation counts
// before/after with a flag for whether the move clears every violation. The
// recommended tap is the one minimising total band violations, tie-broken by the
// smallest worst-case deviation from nominal (the most centred profile).
type tapRecommendation struct {
	Node               featureRef `json:"node"`
	StepPct            float64    `json:"step_pct"`
	TapFrom            float64    `json:"tap_from"`
	TapTo              float64    `json:"tap_to"`
	RatioFrom          float64    `json:"ratio_from"`
	RatioTo            float64    `json:"ratio_to"`
	MinVoltagePuFrom   float64    `json:"min_voltage_pu_from"`
	MinVoltagePuTo     float64    `json:"min_voltage_pu_to"`
	MaxVoltagePuFrom   float64    `json:"max_voltage_pu_from"`
	MaxVoltagePuTo     float64    `json:"max_voltage_pu_to"`
	TotalLossKWFrom    float64    `json:"total_loss_kw_from"`
	TotalLossKWTo      float64    `json:"total_loss_kw_to"`
	SourcePFFrom       float64    `json:"source_pf_from"`
	SourcePFTo         float64    `json:"source_pf_to"`
	ViolationsFrom     int        `json:"violations_from"`
	ViolationsTo       int        `json:"violations_to"`
	ResolvesViolations bool       `json:"resolves_violations"`
}

// tapOptResponse is the full study: the assumed slack / band / swept tap range,
// the base-case feeder metrics, every regulator found (with its live tap), and
// one recommendation per regulator.
type tapOptResponse struct {
	Source          featureRef          `json:"source"`
	NominalVoltageV float64             `json:"nominal_voltage_v"`
	MinVoltagePu    float64             `json:"min_voltage_pu"`
	MaxVoltagePu    float64             `json:"max_voltage_pu"`
	MaxTapSteps     int                 `json:"max_tap_steps"`
	Base            tapMetrics          `json:"base"`
	Regulators      []regulatorInfo     `json:"regulators"`
	Recommendations []tapRecommendation `json:"recommendations"`
}

// tapOptStudy holds the once-resolved feeder so each candidate tap is a pure
// in-memory re-sweep -- the same resolve-once pattern hosting.go / voltvar.go
// use. minPu/maxPu are the acceptable band both regulator directions work to.
type tapOptStudy struct {
	tr         *tree
	states     map[node]*nodeState
	start      node
	classCodes map[string]string
	nominalV   float64
	sourceR    float64
	sourceX    float64
	minPu      float64
	maxPu      float64
}

// runTapOptimization is POST /analysis/tap-optimization: a step-voltage-
// regulator tap study. It resolves the feeder once, sweeps the base case, then
// for each series regulator tries every tap in ±maxSteps and recommends the one
// that best holds the feeder inside the band. It adds no new electrical model --
// a regulator is a series ratio the forward sweep already applies (st.reg), and
// the worst-voltage / loss / power-factor read-outs are the same voltage-profile
// computes off the swept states. Read-only.
func runTapOptimization(ctx context.Context, db bun.IDB, req tapOptRequest) (*tapOptResponse, error) {
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
	maxSteps := defaultMaxTapSteps
	if req.MaxTapSteps != nil {
		maxSteps = *req.MaxTapSteps
	}
	if maxSteps <= 0 || maxSteps > 64 {
		return nil, invalid("max_tap_steps must be between 1 and 64")
	}

	// Resolve the feeder ONCE: walk the tree and load every node's electrical
	// inputs. Every candidate tap below re-runs only the in-memory sweep.
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

	study := &tapOptStudy{
		tr: tr, states: states, start: start, classCodes: classCodes,
		nominalV: req.NominalVoltageV, sourceR: sourceR, sourceX: sourceX,
		minPu: minPu, maxPu: maxPu,
	}

	// Base case: sweep the feeder with every regulator at its live tap.
	base := study.sweepMetrics()

	// Enumerate every regulator on the feeder (a node the sweep resolved a
	// regulator model for -- i.e. carrying regulator_step_pct).
	regs := make([]regulatorInfo, 0)
	type candidate struct {
		n       node
		stepPct float64
	}
	var candidates []candidate
	for _, n := range tr.order {
		st := states[n]
		if st == nil || !st.reg.ok {
			continue
		}
		stepPct, _ := readFloatAttr(st.attrs, regFieldStepPct)
		curTap, _ := readFloatAttr(st.attrs, regFieldTapPosition)
		// A regulator is bypassed when it carries a device_state that is anything
		// other than CLOSED -- resolveRegulator has already forced its live ratio
		// to 1 in that case, so the base sweep reflects the bypass.
		bypassed := false
		if state, ok := readStringAttr(st.attrs, regFieldDeviceState); ok {
			bypassed = !strings.EqualFold(strings.TrimSpace(state), regStateClosed)
		}
		ref := featureRef{FeatureClass: classCodes[n.classID], FeatureID: n.featureID}
		regs = append(regs, regulatorInfo{
			Node: ref, StepPct: stepPct, CurrentTap: curTap,
			RatioNow: st.reg.ratio, Bypassed: bypassed,
		})
		if stepPct != 0 {
			candidates = append(candidates, candidate{n: n, stepPct: stepPct})
		}
	}

	recs := make([]tapRecommendation, 0, len(candidates))
	for _, c := range candidates {
		st := states[c.n]
		ref := featureRef{FeatureClass: classCodes[c.n.classID], FeatureID: c.n.featureID}
		fromRatio := st.reg.ratio
		fromTap, _ := readFloatAttr(st.attrs, regFieldTapPosition)

		bestTap := fromTap
		bestRatio := fromRatio
		best := study.evaluateTap(c.n, fromRatio)
		bestViol := best.UnderCount + best.OverCount
		bestDev := worstDeviation(best)
		for tap := -maxSteps; tap <= maxSteps; tap++ {
			ratio := 1 + float64(tap)*c.stepPct/100
			m := study.evaluateTap(c.n, ratio)
			viol := m.UnderCount + m.OverCount
			dev := worstDeviation(m)
			if viol < bestViol || (viol == bestViol && dev < bestDev) {
				best, bestViol, bestDev = m, viol, dev
				bestTap = float64(tap)
				bestRatio = ratio
			}
		}

		recs = append(recs, tapRecommendation{
			Node:               ref,
			StepPct:            c.stepPct,
			TapFrom:            fromTap,
			TapTo:              bestTap,
			RatioFrom:          fromRatio,
			RatioTo:            bestRatio,
			MinVoltagePuFrom:   base.MinVoltagePu,
			MinVoltagePuTo:     best.MinVoltagePu,
			MaxVoltagePuFrom:   base.MaxVoltagePu,
			MaxVoltagePuTo:     best.MaxVoltagePu,
			TotalLossKWFrom:    base.TotalLossKW,
			TotalLossKWTo:      best.TotalLossKW,
			SourcePFFrom:       base.SourcePF,
			SourcePFTo:         best.SourcePF,
			ViolationsFrom:     base.UnderCount + base.OverCount,
			ViolationsTo:       bestViol,
			ResolvesViolations: (base.UnderCount+base.OverCount) > 0 && bestViol == 0,
		})
	}

	// Restore the true base sweep for any downstream reuse of the study, matching
	// volt-VAR's contract that evaluation leaves the states as it found them.
	study.sweepMetrics()

	return &tapOptResponse{
		Source:          req.Source,
		NominalVoltageV: req.NominalVoltageV,
		MinVoltagePu:    minPu,
		MaxVoltagePu:    maxPu,
		MaxTapSteps:     maxSteps,
		Base:            base,
		Regulators:      regs,
		Recommendations: recs,
	}, nil
}

// sweepMetrics runs the radial sweep over the current states and reads the
// feeder-level metrics for a regulator study off it -- worst-low and worst-high
// per-unit voltage, total series loss, source draw / power factor, and the
// under- and over-voltage counts. It is the voltage-profile summary extended to
// both band bounds.
func (s *tapOptStudy) sweepMetrics() tapMetrics {
	backwardSweep(s.tr, s.states)
	forwardSweep(s.tr, s.states, s.start, s.nominalV, s.sourceR, s.sourceX)

	m := tapMetrics{MinVoltagePu: math.Inf(1), MaxVoltagePu: math.Inf(-1), LossesComplete: true}
	for _, n := range s.tr.order {
		st := s.states[n]
		if st == nil {
			continue
		}
		if st.voltageBase > 0 {
			pu := st.voltageV / st.voltageBase
			ref := featureRef{FeatureClass: s.classCodes[n.classID], FeatureID: n.featureID}
			if pu < m.MinVoltagePu {
				m.MinVoltagePu = pu
				lo := ref
				m.WorstLowNode = &lo
			}
			if pu > m.MaxVoltagePu {
				m.MaxVoltagePu = pu
				hi := ref
				m.WorstHighNode = &hi
			}
			if pu < s.minPu {
				m.UnderCount++
			}
			if pu > s.maxPu {
				m.OverCount++
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
	if math.IsInf(m.MinVoltagePu, 1) {
		m.MinVoltagePu = 0
	}
	if math.IsInf(m.MaxVoltagePu, -1) {
		m.MaxVoltagePu = 0
	}
	if startState := s.states[s.start]; startState != nil {
		m.SourceKW = startState.cumKW
		m.SourceKVAR = startState.cumKVAR
	}
	m.SourcePF = powerFactorOf(m.SourceKW, m.SourceKVAR)
	return m
}

// evaluateTap re-sweeps the feeder with regulator c set to the trial ratio and
// reads the resulting metrics. It mutates the regulator's ratio for the sweep
// and restores it before returning, so candidate taps don't contaminate each
// other -- the same restore contract voltvar.evaluateClose keeps.
func (s *tapOptStudy) evaluateTap(c node, ratio float64) tapMetrics {
	st := s.states[c]
	if st == nil {
		return s.sweepMetrics()
	}
	savedRatio := st.reg.ratio
	st.reg.ratio = ratio
	m := s.sweepMetrics()
	st.reg.ratio = savedRatio
	return m
}

// worstDeviation is a feeder's largest per-unit distance from nominal across
// both band bounds -- the tie-breaker the tap search minimises once two taps
// leave the same number of band violations (prefer the more centred profile).
func worstDeviation(m tapMetrics) float64 {
	return math.Max(math.Abs(1-m.MinVoltagePu), math.Abs(m.MaxVoltagePu-1))
}
