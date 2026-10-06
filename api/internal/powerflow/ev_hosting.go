package powerflow

import (
	"context"
	"encoding/json"
	"math"
	"net/http"

	"github.com/uptrace/bun"
)

// EV feeder-headroom study (Phase 54 Slice 3, docs/phase-54-ev-charging.md). This
// is the under-voltage mirror of the DER hosting-capacity study (hosting.go): it
// reuses the same resolve-once + in-memory re-sweep machinery, the same
// exponential-ramp-then-bisect search, and the same binding-constraint naming,
// with exactly two things flipped --
//
//   - hosting INJECTS negative demand (a generator) and binds on OVER-voltage;
//     EV headroom ADDS positive demand (charging load) and binds on UNDER-voltage
//     (the ANSI 0.95 pu floor).
//   - thermal binds identically in both (a branch/transformer over its own
//     rating, flagged by formatResults at 100 %).
//
// It answers: "how much more EV charging load can this feeder -- or this one
// connection point -- absorb before the first limit binds?" That headroom is the
// physical ceiling; managed / smart charging (Slice 2) is then what lets an
// operator install more nameplate than the headroom and stay within it by
// capping coincident draw. The study sizes the *uncapped* added load, so the
// headroom is reported against the operator's cap lever, not pre-limited by it.

// bindingUndervoltage is EV headroom's own binding label (hosting.go owns
// bindingOvervoltage / bindingThermal / bindingNone / bindingPreExisting, which
// this file reuses). Under-voltage is the limit a growing load crosses first.
const bindingUndervoltage = "undervoltage"

// evHostingRequest is POST /analysis/ev-hosting's body. Source and
// NominalVoltageV are the feeder slack and its sending-end voltage, exactly as
// load-flow / hosting take them. Node optionally names one connection point to
// size; left nil, every non-source apparatus node on the feeder is sized (the
// "where on this feeder absorbs the most EV load" scan). PowerFactor is the added
// charging load's power factor (default unity; at pf < 1 the load also draws
// reactive power kVAR = kW·tan(acos(pf)), which deepens the volt drop and lowers
// the headroom -- the conservative-at-unity default matches hosting). MinVoltagePu
// is the binding floor (default ANSI C84.1 Range A 0.95); only the lower bound
// binds a load, since added demand lowers voltage. MaxAddedKW caps the search
// (default 100 MW, far past any feeder).
type evHostingRequest struct {
	Source          featureRef  `json:"source"`
	NominalVoltageV float64     `json:"nominal_voltage_v"`
	Node            *featureRef `json:"node,omitempty"`
	PowerFactor     *float64    `json:"power_factor,omitempty"`
	MinVoltagePu    *float64    `json:"min_voltage_pu"`
	MaxAddedKW      *float64    `json:"max_added_kw,omitempty"`
}

// evHostingCandidate is one connection node's result. EvHostingCapacityKW is the
// largest additional charging load at Node that introduces no under-voltage and
// no thermal overload -- the EV nameplate the feeder can absorb there.
// BindingConstraint names the limit that stops more (undervoltage / thermal, or
// pre_existing when the base case already violates so there is no headroom, or
// none when the search cap was reached without binding). BindingFeature is the
// node/branch that constraint acts on. WorstVoltagePu / WorstLoadingPct are the
// feeder's worst values AT the hosting capacity -- how close each limit sits.
// Unbounded is true when the cap was hit without a limit binding.
type evHostingCandidate struct {
	Node                featureRef  `json:"node"`
	EvHostingCapacityKW float64     `json:"ev_hosting_capacity_kw"`
	BindingConstraint   string      `json:"binding_constraint"`
	BindingFeature      *featureRef `json:"binding_feature,omitempty"`
	WorstVoltagePu      float64     `json:"worst_voltage_pu"`
	WorstLoadingPct     float64     `json:"worst_loading_pct"`
	Unbounded           bool        `json:"unbounded,omitempty"`
	Note                string      `json:"note,omitempty"`
}

// evHostingResponse is the full study: the feeder slack / floor / load PF the
// search assumed, and one sized candidate per connection node evaluated.
type evHostingResponse struct {
	Source          featureRef           `json:"source"`
	NominalVoltageV float64              `json:"nominal_voltage_v"`
	MinVoltagePu    float64              `json:"min_voltage_pu"`
	PowerFactor     float64              `json:"power_factor"`
	Candidates      []evHostingCandidate `json:"candidates"`
}

// evHostingEval is one trial's outcome: whether the feeder stays within limits,
// and its worst (lowest) voltage / worst loading, with the feature each is worst
// at, so the binding constraint can be named at the capacity point.
type evHostingEval struct {
	feasible        bool
	worstLowPu      float64
	uvFeature       featureRef
	worstLoadingPct float64
	loadFeature     featureRef
}

// evHostingStudy holds the once-resolved feeder so each trial is a pure
// in-memory re-sweep (no queries), the same discipline hostingStudy keeps.
type evHostingStudy struct {
	tr         *tree
	states     map[node]*nodeState
	start      node
	classCodes map[string]string
	nominalV   float64
	sourceR    float64
	sourceX    float64
	minPu      float64
	maxAddKW   float64
	// added-load reactive-power factor: kVAR drawn per kW, = tan(acos(pf)).
	qPerKW float64
}

// runEvHosting is POST /analysis/ev-hosting. It resolves the feeder once, then
// for each candidate node binary-searches the largest added charging load there
// that keeps every node at or above min_voltage_pu and every branch/transformer
// at or below 100 % loading. It adds no new electrical model -- added EV load is
// positive demand (the sweep already carries signed P/Q), under-voltage reuses
// the profile band floor, thermal reuses load-flow's own loading check
// (formatResults). Read-only.
func runEvHosting(ctx context.Context, db bun.IDB, req evHostingRequest) (*evHostingResponse, error) {
	minPu := defaultMinVoltagePu
	if req.MinVoltagePu != nil {
		minPu = *req.MinVoltagePu
	}
	if minPu <= 0 {
		return nil, invalid("min_voltage_pu must be positive")
	}

	pf := 1.0
	if req.PowerFactor != nil {
		pf = *req.PowerFactor
	}
	if pf <= 0 || pf > 1 {
		return nil, invalid("power_factor must be in (0, 1]")
	}
	// A charging load at power factor pf draws kVAR = kW·tan(acos(pf)) alongside
	// its real power. At unity (the default) this is 0 -- real power only.
	qPerKW := math.Tan(math.Acos(pf))

	maxAddKW := defaultMaxInjectionKW
	if req.MaxAddedKW != nil {
		maxAddKW = *req.MaxAddedKW
	}
	if maxAddKW <= 0 {
		return nil, invalid("max_added_kw must be a positive number")
	}

	if req.NominalVoltageV <= 0 {
		return nil, invalid("nominal_voltage_v must be a positive number")
	}

	// Resolve the feeder ONCE: walk the tree and load every node's electrical
	// inputs. Every trial below re-runs only the in-memory sweep.
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

	study := &evHostingStudy{
		tr: tr, states: states, start: start, classCodes: classCodes,
		nominalV: req.NominalVoltageV, sourceR: sourceR, sourceX: sourceX,
		minPu: minPu, maxAddKW: maxAddKW, qPerKW: qPerKW,
	}

	// Candidate connection nodes: an explicit pick, or every non-source
	// apparatus node on the feeder (conductors are line segments, not
	// connection points, so they're skipped in the feeder-wide scan).
	var candidates []node
	if req.Node != nil {
		n, err := resolveNode(ctx, db, *req.Node)
		if err != nil {
			return nil, err
		}
		if _, ok := states[n]; !ok {
			return nil, invalid("node is not reachable from source (not on the swept feeder)")
		}
		candidates = []node{n}
	} else {
		for _, n := range tr.order {
			if n == start {
				continue
			}
			st := states[n]
			if st == nil || st.loaded.FeatureClass.NetworkRole == "edge" {
				continue
			}
			candidates = append(candidates, n)
		}
	}

	out := make([]evHostingCandidate, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, study.size(c))
	}

	return &evHostingResponse{
		Source:          req.Source,
		NominalVoltageV: req.NominalVoltageV,
		MinVoltagePu:    minPu,
		PowerFactor:     pf,
		Candidates:      out,
	}, nil
}

// size finds one candidate node's EV hosting capacity: an exponential ramp to
// bracket the first infeasible added load, then a bisection to pin the largest
// feasible one, then a name for whichever limit binds at that capacity.
func (s *evHostingStudy) size(c node) evHostingCandidate {
	ref := featureRef{FeatureClass: s.classCodes[c.classID], FeatureID: c.featureID}
	res := evHostingCandidate{Node: ref}

	// Base case: no added load. If the feeder already violates a limit, there is
	// no headroom for EV load here at all -- report 0 and say why.
	base := s.evaluate(c, 0)
	if !base.feasible {
		res.EvHostingCapacityKW = 0
		res.WorstVoltagePu = base.worstLowPu
		res.WorstLoadingPct = base.worstLoadingPct
		res.BindingConstraint = bindingPreExisting
		if base.worstLowPu < s.minPu {
			bf := base.uvFeature
			res.BindingFeature = &bf
			res.Note = "the feeder is already below the voltage floor before any EV load -- no headroom at this node"
		} else {
			bf := base.loadFeature
			res.BindingFeature = &bf
			res.Note = "the feeder already has a thermal overload before any EV load -- no headroom at this node"
		}
		return res
	}

	// Exponential ramp: double the added load until it becomes infeasible or
	// hits the search cap. lo stays the largest known-feasible load, hi the
	// smallest known-infeasible one.
	lo := 0.0
	hi := rampStartKW
	var hiEval evHostingEval
	feasibleAtCap := false
	for {
		if hi >= s.maxAddKW {
			hi = s.maxAddKW
			hiEval = s.evaluate(c, hi)
			if hiEval.feasible {
				feasibleAtCap = true
			}
			break
		}
		e := s.evaluate(c, hi)
		if !e.feasible {
			hiEval = e
			break
		}
		lo = hi
		hi *= 2
	}

	// The feeder never bound below the cap -- capacity is at least the cap.
	if feasibleAtCap {
		res.EvHostingCapacityKW = s.maxAddKW
		res.WorstVoltagePu = hiEval.worstLowPu
		res.WorstLoadingPct = hiEval.worstLoadingPct
		res.BindingConstraint = bindingNone
		res.Unbounded = true
		res.Note = "no limit binds up to max_added_kw -- EV headroom is at least this"
		return res
	}

	// Bisect [lo feasible, hi infeasible] toward the largest feasible load.
	loEval := s.evaluate(c, lo)
	for i := 0; i < hostingBisectIters && hi-lo > hostingResolutionKW; i++ {
		mid := (lo + hi) / 2
		e := s.evaluate(c, mid)
		if e.feasible {
			lo = mid
			loEval = e
		} else {
			hi = mid
		}
	}

	res.EvHostingCapacityKW = lo
	res.WorstVoltagePu = loEval.worstLowPu
	res.WorstLoadingPct = loEval.worstLoadingPct
	// At the capacity both limits are satisfied; the binding one is whichever
	// sits closest to its ceiling (smallest remaining margin) -- the limit the
	// next kW of load would cross.
	uvMargin := loEval.worstLowPu - s.minPu
	thMargin := 1.0 - loEval.worstLoadingPct/100
	if uvMargin <= thMargin {
		res.BindingConstraint = bindingUndervoltage
		bf := loEval.uvFeature
		res.BindingFeature = &bf
	} else {
		res.BindingConstraint = bindingThermal
		bf := loEval.loadFeature
		res.BindingFeature = &bf
	}
	return res
}

// evaluate re-sweeps the feeder with addKW (and its power-factor kVAR) ADDED as
// demand at node c and reports whether every node stays at or above
// min_voltage_pu and every branch/transformer at or below 100 % loading, plus
// the worst (lowest) voltage / worst loading and where. It mutates the
// candidate's own demand for the sweep and restores it before returning, so
// candidates don't contaminate each other.
func (s *evHostingStudy) evaluate(c node, addKW float64) evHostingEval {
	st := s.states[c]
	if st == nil {
		return evHostingEval{feasible: true, worstLowPu: 1}
	}
	savedKW, savedKVAR := st.ownKW, st.ownKVAR
	// Added charging load is positive demand: add it to the node's own load so
	// the backward sweep carries it up to the source.
	st.ownKW = savedKW + addKW
	st.ownKVAR = savedKVAR + addKW*s.qPerKW

	backwardSweep(s.tr, s.states)
	forwardSweep(s.tr, s.states, s.start, s.nominalV, s.sourceR, s.sourceX)

	nodes, overloaded := formatResults(s.tr, s.states, s.classCodes)

	eval := evHostingEval{worstLowPu: math.Inf(1)}
	for _, r := range nodes {
		if r.NominalBaseV > 0 {
			pu := r.VoltageV / r.NominalBaseV
			if pu < eval.worstLowPu {
				eval.worstLowPu = pu
				eval.uvFeature = featureRef{FeatureClass: r.FeatureClass, FeatureID: r.FeatureID}
			}
		}
		if r.LoadingPct != nil && *r.LoadingPct > eval.worstLoadingPct {
			eval.worstLoadingPct = *r.LoadingPct
			eval.loadFeature = featureRef{FeatureClass: r.FeatureClass, FeatureID: r.FeatureID}
		}
	}
	// No rated node on the feeder -> no voltage to constrain; report nominal so
	// the result JSON stays finite (json.Marshal rejects Inf) and the voltage
	// limit reads as satisfied.
	if math.IsInf(eval.worstLowPu, 1) {
		eval.worstLowPu = 1
	}
	eval.feasible = eval.worstLowPu >= s.minPu && len(overloaded) == 0

	st.ownKW, st.ownKVAR = savedKW, savedKVAR
	return eval
}

func evHostingHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req evHostingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runEvHosting(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}
