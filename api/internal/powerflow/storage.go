package powerflow

import (
	"context"
	"math"

	"github.com/uptrace/bun"
)

// Informational storage fields the dispatch study reads but the sweep does not:
// energy_capacity_kwh sizes the battery and state_of_charge_pct says how full it
// is right now. The study uses SoC only to gate the feasible dispatch range (an
// empty battery cannot discharge, a full one cannot charge) -- for a single
// power-flow snapshot the energy itself is reported, not integrated.
const (
	storageFieldEnergyKWh = "energy_capacity_kwh"
	storageFieldSOCPct    = "state_of_charge_pct"
)

// defaultDispatchSteps is the number of trial set-points explored PER SIDE of
// zero when the request does not pin one, so the sweep tries 2N+1 dispatches from
// full charge (−rated) through idle to full discharge (+rated). Ten a side gives
// a 10%-of-rating resolution, fine enough to find a good peak-shave / voltage set
// point without an expensive sweep.
const defaultDispatchSteps = 10

// storageRequest is POST /analysis/storage-dispatch's body. Source and
// NominalVoltageV are the feeder slack and its sending-end voltage, as every
// feeder study takes them. The study sweeps the feeder as it stands (base case),
// then for each battery-storage unit on the feeder tries every dispatch set-point
// from full charge to full discharge and recommends the one that best holds the
// feeder inside the voltage band and shaves the source draw -- the "how should
// this battery dispatch right now?" question. Min/MaxVoltagePu are the band
// (default ANSI C84.1 Range A 0.95..1.05); BOTH bounds matter, since discharging
// can push voltage up as charging pulls it down. DispatchSteps is the per-side
// resolution of the set-point sweep.
type storageRequest struct {
	Source          featureRef `json:"source"`
	NominalVoltageV float64    `json:"nominal_voltage_v"`
	MinVoltagePu    *float64   `json:"min_voltage_pu"`
	MaxVoltagePu    *float64   `json:"max_voltage_pu"`
	DispatchSteps   *int       `json:"dispatch_steps"`
}

// storageUnit is one battery-storage unit found on the feeder: its node, the PCS
// power rating (the sweep bound), the dispatch it sits at now (signed: + is
// discharging, − is charging), its energy size and state of charge, and the
// SoC-feasible dispatch window the study actually swept (an empty battery is
// pinned to charge-only, a full one to discharge-only).
type storageUnit struct {
	Node              featureRef `json:"node"`
	RatedPowerKW      float64    `json:"rated_power_kw"`
	DispatchKWNow     float64    `json:"dispatch_kw_now"`
	EnergyCapacityKWh float64    `json:"energy_capacity_kwh"`
	StateOfChargePct  float64    `json:"state_of_charge_pct"`
	HasStateOfCharge  bool       `json:"has_state_of_charge"`
	MinDispatchKW     float64    `json:"min_dispatch_kw"`
	MaxDispatchKW     float64    `json:"max_dispatch_kw"`
}

// storageRecommendation is one battery evaluated across its dispatch range: the
// set-point it holds now versus the one the study picked (with the resulting
// operating mode), the worst-low and worst-high per-unit voltage before and
// after, the source real-power draw before/after and the peak-shave that
// represents, the loss and source power factor before/after, and the band-
// violation counts before/after with a flag for whether the move clears every
// violation. The recommended dispatch minimises band violations first, then the
// magnitude of the source draw (peak-shave toward net-zero import) -- a
// lexicographic objective, so it never trades a voltage excursion for a smaller
// import.
type storageRecommendation struct {
	Node               featureRef `json:"node"`
	RatedPowerKW       float64    `json:"rated_power_kw"`
	DispatchKWFrom     float64    `json:"dispatch_kw_from"`
	DispatchKWTo       float64    `json:"dispatch_kw_to"`
	ModeTo             string     `json:"mode_to"`
	MinVoltagePuFrom   float64    `json:"min_voltage_pu_from"`
	MinVoltagePuTo     float64    `json:"min_voltage_pu_to"`
	MaxVoltagePuFrom   float64    `json:"max_voltage_pu_from"`
	MaxVoltagePuTo     float64    `json:"max_voltage_pu_to"`
	SourceKWFrom       float64    `json:"source_kw_from"`
	SourceKWTo         float64    `json:"source_kw_to"`
	PeakShaveKW        float64    `json:"peak_shave_kw"`
	TotalLossKWFrom    float64    `json:"total_loss_kw_from"`
	TotalLossKWTo      float64    `json:"total_loss_kw_to"`
	SourcePFFrom       float64    `json:"source_pf_from"`
	SourcePFTo         float64    `json:"source_pf_to"`
	ViolationsFrom     int        `json:"violations_from"`
	ViolationsTo       int        `json:"violations_to"`
	ResolvesViolations bool       `json:"resolves_violations"`
}

// storageResponse is the full study: the assumed slack / band / swept resolution,
// the base-case feeder metrics, every battery found (with its live dispatch and
// feasible window), and one recommendation per battery.
type storageResponse struct {
	Source          featureRef              `json:"source"`
	NominalVoltageV float64                 `json:"nominal_voltage_v"`
	MinVoltagePu    float64                 `json:"min_voltage_pu"`
	MaxVoltagePu    float64                 `json:"max_voltage_pu"`
	DispatchSteps   int                     `json:"dispatch_steps"`
	Base            tapMetrics              `json:"base"`
	Units           []storageUnit           `json:"units"`
	Recommendations []storageRecommendation `json:"recommendations"`
}

// runStorageDispatch is POST /analysis/storage-dispatch: a battery-storage
// dispatch study. It resolves the feeder once, sweeps the base case, then for
// each battery tries every set-point from full charge to full discharge and
// recommends the one that best holds the feeder in band and shaves the source
// draw. It adds no new electrical model -- a battery is a signed injection the
// resolve step already nets (resolveStorage), and the worst-voltage / loss /
// source-draw read-outs are the same tapMetrics the regulator study reads, so
// this study is the same resolve-once + in-memory re-sweep as tap-optimization,
// varying the node's own power instead of a series ratio. Read-only.
func runStorageDispatch(ctx context.Context, db bun.IDB, req storageRequest) (*storageResponse, error) {
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
	steps := defaultDispatchSteps
	if req.DispatchSteps != nil {
		steps = *req.DispatchSteps
	}
	if steps <= 0 || steps > 200 {
		return nil, invalid("dispatch_steps must be between 1 and 200")
	}

	// Resolve the feeder ONCE: walk the tree and load every node's electrical
	// inputs. Every candidate dispatch below re-runs only the in-memory sweep.
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

	// Reuse the generic feeder study (resolve-once + both-bound sweepMetrics) --
	// the dispatch study varies node power, not a regulator ratio, but the sweep,
	// band bookkeeping and source-draw read-out are identical.
	study := &tapOptStudy{
		tr: tr, states: states, start: start, classCodes: classCodes,
		nominalV: req.NominalVoltageV, sourceR: sourceR, sourceX: sourceX,
		minPu: minPu, maxPu: maxPu,
	}

	// Base case: sweep the feeder with every battery at its live dispatch.
	base := study.sweepMetrics()

	// Enumerate every battery on the feeder (a node carrying rated_power_kw, the
	// PCS rating that bounds its dispatch), recording its live set-point and the
	// SoC-feasible window the sweep will explore.
	units := make([]storageUnit, 0)
	type candidate struct {
		n               node
		rated           float64
		pf              float64
		storageFreeKW   float64
		storageFreeKVAR float64
		loKW            float64
		hiKW            float64
		nowKW           float64
	}
	var candidates []candidate
	for _, n := range tr.order {
		st := states[n]
		if st == nil {
			continue
		}
		rated, ok := readFloatAttr(st.attrs, storageFieldRatedPowerKW)
		if !ok || rated <= 0 {
			continue
		}
		// The node's power with its battery backed out: resolveNodeStates already
		// netted the live dispatch into ownKW/ownKVAR, so add it back to get the
		// stationary (demand/generation) part each trial dispatch is applied over.
		nowKW, nowKVAR := resolveStorage(st.attrs)
		storageFreeKW := st.ownKW + nowKW
		storageFreeKVAR := st.ownKVAR + nowKVAR

		pf, okPF := readFloatAttr(st.attrs, storageFieldPF)
		if !okPF || pf <= 0 || pf > 1 {
			pf = 1
		}

		// SoC gates the feasible window: an empty battery can only charge
		// (dispatch <= 0), a full one can only discharge (dispatch >= 0).
		loKW, hiKW := -rated, rated
		soc, hasSOC := readFloatAttr(st.attrs, storageFieldSOCPct)
		if hasSOC {
			if soc <= 0 {
				hiKW = 0
			}
			if soc >= 100 {
				loKW = 0
			}
		}

		energy, _ := readFloatAttr(st.attrs, storageFieldEnergyKWh)
		ref := featureRef{FeatureClass: classCodes[n.classID], FeatureID: n.featureID}
		units = append(units, storageUnit{
			Node: ref, RatedPowerKW: rated, DispatchKWNow: nowKW,
			EnergyCapacityKWh: energy, StateOfChargePct: soc, HasStateOfCharge: hasSOC,
			MinDispatchKW: loKW, MaxDispatchKW: hiKW,
		})
		candidates = append(candidates, candidate{
			n: n, rated: rated, pf: pf,
			storageFreeKW: storageFreeKW, storageFreeKVAR: storageFreeKVAR,
			loKW: loKW, hiKW: hiKW, nowKW: nowKW,
		})
	}

	baseViol := base.UnderCount + base.OverCount
	recs := make([]storageRecommendation, 0, len(candidates))
	for _, c := range candidates {
		ref := featureRef{FeatureClass: classCodes[c.n.classID], FeatureID: c.n.featureID}

		bestKW := c.nowKW
		best := evaluateStorageDispatch(study, c.n, c.nowKW, c.storageFreeKW, c.storageFreeKVAR, c.pf)
		bestViol := best.UnderCount + best.OverCount
		bestDraw := math.Abs(best.SourceKW)
		for k := -steps; k <= steps; k++ {
			d := c.rated * float64(k) / float64(steps)
			if d < c.loKW || d > c.hiKW {
				continue
			}
			m := evaluateStorageDispatch(study, c.n, d, c.storageFreeKW, c.storageFreeKVAR, c.pf)
			viol := m.UnderCount + m.OverCount
			draw := math.Abs(m.SourceKW)
			// Lexicographic: fewest band violations first, then the smallest source
			// draw (peak-shave toward net-zero import) as the tie-break.
			if viol < bestViol || (viol == bestViol && draw < bestDraw) {
				best, bestViol, bestDraw = m, viol, draw
				bestKW = d
			}
		}

		recs = append(recs, storageRecommendation{
			Node:               ref,
			RatedPowerKW:       c.rated,
			DispatchKWFrom:     c.nowKW,
			DispatchKWTo:       bestKW,
			ModeTo:             dispatchMode(bestKW),
			MinVoltagePuFrom:   base.MinVoltagePu,
			MinVoltagePuTo:     best.MinVoltagePu,
			MaxVoltagePuFrom:   base.MaxVoltagePu,
			MaxVoltagePuTo:     best.MaxVoltagePu,
			SourceKWFrom:       base.SourceKW,
			SourceKWTo:         best.SourceKW,
			PeakShaveKW:        math.Abs(base.SourceKW) - math.Abs(best.SourceKW),
			TotalLossKWFrom:    base.TotalLossKW,
			TotalLossKWTo:      best.TotalLossKW,
			SourcePFFrom:       base.SourcePF,
			SourcePFTo:         best.SourcePF,
			ViolationsFrom:     baseViol,
			ViolationsTo:       bestViol,
			ResolvesViolations: baseViol > 0 && bestViol == 0,
		})
	}

	// Restore the true base sweep for any downstream reuse of the study, matching
	// the contract that evaluation leaves the states as it found them.
	study.sweepMetrics()

	return &storageResponse{
		Source:          req.Source,
		NominalVoltageV: req.NominalVoltageV,
		MinVoltagePu:    minPu,
		MaxVoltagePu:    maxPu,
		DispatchSteps:   steps,
		Base:            base,
		Units:           units,
		Recommendations: recs,
	}, nil
}

// evaluateStorageDispatch re-sweeps the feeder with battery n dispatching
// dispatchKW (signed: + discharge, − charge) and reads the resulting metrics. It
// sets the node's own power to its stationary part minus the trial injection
// (Q following P at the unit's power factor, the same rule resolveStorage uses),
// then restores the live values before returning, so candidate set-points don't
// contaminate each other -- the restore contract evaluateTap keeps.
func evaluateStorageDispatch(s *tapOptStudy, n node, dispatchKW, storageFreeKW, storageFreeKVAR, pf float64) tapMetrics {
	st := s.states[n]
	if st == nil {
		return s.sweepMetrics()
	}
	savedKW, savedKVAR := st.ownKW, st.ownKVAR
	kvar := dispatchKW * math.Tan(math.Acos(pf))
	st.ownKW = storageFreeKW - dispatchKW
	st.ownKVAR = storageFreeKVAR - kvar
	m := s.sweepMetrics()
	st.ownKW, st.ownKVAR = savedKW, savedKVAR
	return m
}

// dispatchMode labels a signed dispatch set-point for the read-out: a positive
// value discharges (injects), a negative one charges (absorbs), and zero is idle.
func dispatchMode(kw float64) string {
	switch {
	case kw > 0:
		return "discharge"
	case kw < 0:
		return "charge"
	default:
		return "idle"
	}
}
