package powerflow

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/uptrace/bun"
)

// defaultVVORounds caps the coordinate-descent passes a Volt-VAR Optimization
// makes. VVO co-optimizes several controls that interact (a regulator boost
// changes which capacitor helps, a battery discharge changes the regulator's best
// tap), so it sweeps them round-robin until a full pass changes nothing
// (converged) or this many passes elapse. Eight is comfortably more than a
// distribution feeder's handful of controls needs to settle.
const defaultVVORounds = 8

// vvoRequest is POST /analysis/vvo's body. Source and NominalVoltageV are the
// feeder slack and its sending-end voltage, as every feeder study takes them.
// Where volt-VAR, tap-optimization and storage-dispatch each optimise ONE class
// of control in isolation, VVO co-optimises ALL of them together -- every
// switched capacitor bank, every step voltage regulator and every battery on the
// feeder -- against one objective: hold the whole feeder inside the voltage band,
// then minimise series loss. Min/MaxVoltagePu are the band (default ANSI C84.1
// Range A 0.95..1.05); MaxTapSteps bounds each regulator's tap sweep and
// DispatchSteps the per-side resolution of each battery's dispatch sweep, exactly
// as in the single-control studies. MaxRounds caps the coordinate-descent passes.
type vvoRequest struct {
	Source          featureRef `json:"source"`
	NominalVoltageV float64    `json:"nominal_voltage_v"`
	MinVoltagePu    *float64   `json:"min_voltage_pu"`
	MaxVoltagePu    *float64   `json:"max_voltage_pu"`
	MaxTapSteps     *int       `json:"max_tap_steps"`
	DispatchSteps   *int       `json:"dispatch_steps"`
	MaxRounds       *int       `json:"max_rounds"`
}

// vvoAction is one control the optimisation touched: its node and kind
// (capacitor / regulator / battery), the human-readable setting it held before
// and the one VVO chose, and whether that is a change. Reported for every control
// on the feeder, changed ones first.
type vvoAction struct {
	Node        featureRef `json:"node"`
	Kind        string     `json:"kind"`
	SettingFrom string     `json:"setting_from"`
	SettingTo   string     `json:"setting_to"`
	Changed     bool       `json:"changed"`
}

// vvoResponse is the full study: the assumed slack / band, how many
// coordinate-descent rounds ran and whether it converged, the base-case and
// optimised feeder metrics (both voltage bounds, loss, source draw, band
// violations), and one action per control.
type vvoResponse struct {
	Source          featureRef  `json:"source"`
	NominalVoltageV float64     `json:"nominal_voltage_v"`
	MinVoltagePu    float64     `json:"min_voltage_pu"`
	MaxVoltagePu    float64     `json:"max_voltage_pu"`
	Rounds          int         `json:"rounds"`
	Converged       bool        `json:"converged"`
	Base            tapMetrics  `json:"base"`
	Optimized       tapMetrics  `json:"optimized"`
	Actions         []vvoAction `json:"actions"`
}

// vvoActor is one controllable device abstracted to a single scalar knob so the
// coordinate-descent loop is control-agnostic: settings are the candidate values
// (tap number, 0/1 for a capacitor in/out, signed kW for a battery), set applies
// a value absolutely to the study state (so re-applying the chosen value persists
// it -- no delta bookkeeping), format renders a value for the read-out, liveVal
// is the base (as-found) setting and curVal the one currently applied.
type vvoActor struct {
	node     node
	ref      featureRef
	kind     string
	settings []float64
	set      func(float64)
	format   func(float64) string
	liveVal  float64
	curVal   float64
}

// vvoBetter is the optimisation objective, lexicographic: fewer band violations
// first (voltage compliance is never traded away), then lower series loss (the
// classic VVO loss-minimisation). Comparing the tuple directly avoids the
// precision loss a single weighted scalar would suffer on a large feeder.
func vvoBetter(a, b tapMetrics) bool {
	av := a.UnderCount + a.OverCount
	bv := b.UnderCount + b.OverCount
	if av != bv {
		return av < bv
	}
	return a.TotalLossKW < b.TotalLossKW
}

// runVVO is POST /analysis/vvo: a combined Volt-VAR Optimization. It resolves the
// feeder once, enumerates every switched capacitor, step regulator and battery on
// it, and runs coordinate descent -- repeatedly, for each control in turn, trying
// all its settings with the others held fixed and keeping the best -- until a full
// pass changes nothing or MaxRounds elapse. It adds NO new electrical model: each
// control is applied through the same state mutation its own single-control study
// uses (capacitor kVAr, regulator ratio, battery signed injection), and the
// objective is read from the same both-bound feeder sweep. Read-only.
func runVVO(ctx context.Context, db bun.IDB, req vvoRequest) (*vvoResponse, error) {
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
	maxTapSteps := defaultMaxTapSteps
	if req.MaxTapSteps != nil {
		maxTapSteps = *req.MaxTapSteps
	}
	if maxTapSteps <= 0 || maxTapSteps > 64 {
		return nil, invalid("max_tap_steps must be between 1 and 64")
	}
	dispatchSteps := defaultDispatchSteps
	if req.DispatchSteps != nil {
		dispatchSteps = *req.DispatchSteps
	}
	if dispatchSteps <= 0 || dispatchSteps > 200 {
		return nil, invalid("dispatch_steps must be between 1 and 200")
	}
	maxRounds := defaultVVORounds
	if req.MaxRounds != nil {
		maxRounds = *req.MaxRounds
	}
	if maxRounds <= 0 || maxRounds > 100 {
		return nil, invalid("max_rounds must be between 1 and 100")
	}

	// Resolve the feeder ONCE: every candidate setting below is a pure in-memory
	// re-sweep of these states, the same resolve-once pattern the single-control
	// studies use.
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

	actors := buildVVOActors(study, tr, states, classCodes, maxTapSteps, dispatchSteps)

	// Base case: sweep the feeder with every control at its as-found setting.
	base := study.sweepMetrics()

	// Coordinate descent. Each pass optimises one control at a time against the
	// current setting of all the others; a control's mutation persists (set is
	// absolute), so later controls in the pass see earlier moves. The pass repeats
	// until nothing changes (converged) or the round cap is hit.
	best := base
	rounds := 0
	converged := false
	for rounds < maxRounds {
		rounds++
		changedThisRound := false
		for i := range actors {
			a := &actors[i]
			localBest := best
			localVal := a.curVal
			for _, v := range a.settings {
				a.set(v)
				m := study.sweepMetrics()
				if vvoBetter(m, localBest) {
					localBest, localVal = m, v
				}
			}
			a.set(localVal) // leave the control at its chosen setting
			if localVal != a.curVal {
				a.curVal = localVal
				changedThisRound = true
			}
			best = localBest
		}
		if !changedThisRound {
			converged = true
			break
		}
	}

	actions := make([]vvoAction, 0, len(actors))
	for i := range actors {
		a := &actors[i]
		actions = append(actions, vvoAction{
			Node:        a.ref,
			Kind:        a.kind,
			SettingFrom: a.format(a.liveVal),
			SettingTo:   a.format(a.curVal),
			Changed:     a.curVal != a.liveVal,
		})
	}
	// Changed controls first, then by kind for a stable read-out.
	sortVVOActions(actions)

	return &vvoResponse{
		Source:          req.Source,
		NominalVoltageV: req.NominalVoltageV,
		MinVoltagePu:    minPu,
		MaxVoltagePu:    maxPu,
		Rounds:          rounds,
		Converged:       converged,
		Base:            base,
		Optimized:       best,
		Actions:         actions,
	}, nil
}

// buildVVOActors enumerates every controllable device on the feeder and wraps
// each as a vvoActor whose scalar knob the descent loop drives. A capacitor is
// controllable when it is switched (carries device_state) with a non-zero rating;
// a regulator when the sweep resolved a regulator model with a non-zero step; a
// battery when it carries a positive PCS rating. Each actor's set closure mutates
// exactly the state its own single-control study mutates.
func buildVVOActors(
	study *tapOptStudy,
	tr *tree,
	states map[node]*nodeState,
	classCodes map[string]string,
	maxTapSteps, dispatchSteps int,
) []vvoActor {
	var actors []vvoActor
	for _, n := range tr.order {
		st := states[n]
		if st == nil {
			continue
		}
		ref := featureRef{FeatureClass: classCodes[n.classID], FeatureID: n.featureID}

		// Switched capacitor bank -- knob is 0 (out) / 1 (in). kvarFree is the
		// node's reactive demand with the bank backed out, so set(v) puts exactly
		// v*rated of leading VAr back in.
		if rated, ok := readFloatAttr(st.attrs, capFieldRatedKVAR); ok && rated != 0 {
			if _, switchable := readStringAttr(st.attrs, capFieldDeviceState); switchable {
				stNode := st
				kvarFree := st.ownKVAR + resolveCapacitor(st.attrs)
				live := 0.0
				if state, ok := readStringAttr(st.attrs, capFieldDeviceState); ok &&
					strings.EqualFold(strings.TrimSpace(state), capStateClosed) {
					live = 1
				}
				actors = append(actors, vvoActor{
					node: n, ref: ref, kind: "capacitor",
					settings: []float64{0, 1},
					set:      func(v float64) { stNode.ownKVAR = kvarFree - v*rated },
					format: func(v float64) string {
						if v >= 1 {
							return fmt.Sprintf("in / closed (%.0f kVAr)", rated)
						}
						return "out / open"
					},
					liveVal: live, curVal: live,
				})
				continue
			}
		}

		// Step voltage regulator -- knob is the tap position; set applies the ratio.
		if st.reg.ok {
			if step, ok := readFloatAttr(st.attrs, regFieldStepPct); ok && step != 0 {
				stNode := st
				live, _ := readFloatAttr(st.attrs, regFieldTapPosition)
				settings := make([]float64, 0, 2*maxTapSteps+1)
				for tap := -maxTapSteps; tap <= maxTapSteps; tap++ {
					settings = append(settings, float64(tap))
				}
				actors = append(actors, vvoActor{
					node: n, ref: ref, kind: "regulator",
					settings: settings,
					set:      func(v float64) { stNode.reg.ratio = 1 + v*step/100 },
					format:   func(v float64) string { return fmt.Sprintf("tap %+d", int(v)) },
					liveVal:  live, curVal: live,
				})
				continue
			}
		}

		// Battery -- knob is signed dispatch kW; set applies it as a signed
		// injection (P and Q), gated by state of charge.
		if rated, ok := readFloatAttr(st.attrs, storageFieldRatedPowerKW); ok && rated > 0 {
			stNode := st
			nowKW, nowKVAR := resolveStorage(st.attrs)
			freeKW := st.ownKW + nowKW
			freeKVAR := st.ownKVAR + nowKVAR
			pf, okPF := readFloatAttr(st.attrs, storageFieldPF)
			if !okPF || pf <= 0 || pf > 1 {
				pf = 1
			}
			tanPhi := math.Tan(math.Acos(pf))
			loKW, hiKW := -rated, rated
			if soc, hasSOC := readFloatAttr(st.attrs, storageFieldSOCPct); hasSOC {
				if soc <= 0 {
					hiKW = 0
				}
				if soc >= 100 {
					loKW = 0
				}
			}
			settings := make([]float64, 0, 2*dispatchSteps+1)
			for k := -dispatchSteps; k <= dispatchSteps; k++ {
				d := rated * float64(k) / float64(dispatchSteps)
				if d < loKW || d > hiKW {
					continue
				}
				settings = append(settings, d)
			}
			actors = append(actors, vvoActor{
				node: n, ref: ref, kind: "battery",
				settings: settings,
				set: func(v float64) {
					stNode.ownKW = freeKW - v
					stNode.ownKVAR = freeKVAR - v*tanPhi
				},
				format:  func(v float64) string { return fmt.Sprintf("%+.0f kW (%s)", v, dispatchMode(v)) },
				liveVal: nowKW, curVal: nowKW,
			})
			continue
		}
	}
	return actors
}

// sortVVOActions orders the read-out changed controls first, then by kind
// (battery, capacitor, regulator) for a stable, deterministic listing.
func sortVVOActions(actions []vvoAction) {
	kindRank := map[string]int{"battery": 0, "capacitor": 1, "regulator": 2}
	for i := 1; i < len(actions); i++ {
		for j := i; j > 0; j-- {
			a, b := actions[j-1], actions[j]
			less := false
			if a.Changed != b.Changed {
				less = b.Changed // a changed=false, b changed=true -> b should come first
			} else if kindRank[a.Kind] != kindRank[b.Kind] {
				less = kindRank[b.Kind] < kindRank[a.Kind]
			}
			if !less {
				break
			}
			actions[j-1], actions[j] = actions[j], actions[j-1]
		}
	}
}
