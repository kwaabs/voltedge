package powerflow

import (
	"context"
	"math"

	"github.com/uptrace/bun"
)

// DER hosting-capacity search parameters. defaultMaxInjectionKW caps the search
// (100 MW -- far past any distribution interconnection, so a feeder that never
// binds below it is reported "unbounded up to" the cap rather than looping
// forever). rampStartKW is the first trial injection the exponential ramp tries
// before doubling toward an infeasible upper bound; hostingBisectIters /
// hostingResolutionKW stop the bisection once it has pinned the capacity to
// within a fraction of a kW (whichever comes first).
const (
	defaultMaxInjectionKW = 100_000.0
	rampStartKW           = 50.0
	hostingBisectIters    = 40
	hostingResolutionKW   = 0.1
)

// Binding-constraint labels: which limit stops the DER from injecting more.
const (
	bindingOvervoltage = "overvoltage"
	bindingThermal     = "thermal"
	bindingNone        = "none"
	bindingPreExisting = "pre_existing"
)

// hostingRequest is POST /analysis/hosting-capacity's body. Source and
// NominalVoltageV are the feeder slack and its sending-end voltage, exactly as
// load-flow / voltage-profile take them: the study sweeps the feeder as it
// stands, then re-sweeps it with a generator injecting at a candidate node and
// grows that injection until the first limit binds.
//
// Node optionally names one interconnection point to size; left nil, every
// non-source apparatus node on the feeder is sized (the "where on this feeder
// hosts the most DER" scan). PowerFactor is the injecting generator's power
// factor, defaulting to unity (real power only, the fixed-PF PV model this slice
// commits to); at pf < 1 the generator also injects reactive power
// (kVAR = kW·tan(acos(pf))), which lifts voltage further. Min/MaxVoltagePu are
// the acceptable band (defaulting to ANSI C84.1 Range A 0.95..1.05); only the
// upper bound binds a generator, since injection raises voltage.
// MaxInjectionKW caps the search (default 100 MW).
type hostingRequest struct {
	Source          featureRef  `json:"source"`
	NominalVoltageV float64     `json:"nominal_voltage_v"`
	Node            *featureRef `json:"node,omitempty"`
	PowerFactor     *float64    `json:"power_factor,omitempty"`
	MinVoltagePu    *float64    `json:"min_voltage_pu"`
	MaxVoltagePu    *float64    `json:"max_voltage_pu"`
	MaxInjectionKW  *float64    `json:"max_injection_kw,omitempty"`
}

// hostingCandidate is one interconnection node's result. HostingCapacityKW is
// the largest real-power injection at Node that introduces no over-voltage and
// no thermal overload -- the DER nameplate the feeder can accept there.
// BindingConstraint names the limit that stops more (overvoltage / thermal, or
// pre_existing when the base case already violates so there's no headroom, or
// none when the search cap was reached without binding). BindingFeature is the
// node/branch that constraint acts on. WorstVoltagePu / WorstLoadingPct are the
// feeder's worst values AT the hosting capacity -- how close each limit sits.
// Unbounded is true when the cap was hit without a limit binding (capacity is at
// least HostingCapacityKW, possibly more).
type hostingCandidate struct {
	Node              featureRef  `json:"node"`
	HostingCapacityKW float64     `json:"hosting_capacity_kw"`
	BindingConstraint string      `json:"binding_constraint"`
	BindingFeature    *featureRef `json:"binding_feature,omitempty"`
	WorstVoltagePu    float64     `json:"worst_voltage_pu"`
	WorstLoadingPct   float64     `json:"worst_loading_pct"`
	Unbounded         bool        `json:"unbounded,omitempty"`
	Note              string      `json:"note,omitempty"`
}

// hostingResponse is the full study: the feeder slack / band / injection PF the
// search assumed, and one sized candidate per interconnection node evaluated.
type hostingResponse struct {
	Source          featureRef         `json:"source"`
	NominalVoltageV float64            `json:"nominal_voltage_v"`
	MinVoltagePu    float64            `json:"min_voltage_pu"`
	MaxVoltagePu    float64            `json:"max_voltage_pu"`
	PowerFactor     float64            `json:"power_factor"`
	Candidates      []hostingCandidate `json:"candidates"`
}

// hostingEval is one trial injection's outcome: whether the feeder stays within
// limits, and its worst voltage / loading (with the feature each is worst at) so
// the binding constraint can be named at the capacity point.
type hostingEval struct {
	feasible        bool
	worstHighPu     float64
	ovFeature       featureRef
	worstLoadingPct float64
	loadFeature     featureRef
}

// hostingStudy holds the once-resolved feeder so each trial injection is a pure
// in-memory re-sweep (no queries): the binary search runs dozens of sweeps per
// candidate, so the query-heavy resolve (tree walk + per-node feature load)
// happens exactly once and only backward/forward sweep re-run per trial.
type hostingStudy struct {
	tr         *tree
	states     map[node]*nodeState
	start      node
	classCodes map[string]string
	nominalV   float64
	sourceR    float64
	sourceX    float64
	maxPu      float64
	maxInjKW   float64
	// injection reactive-power factor: kVAR injected per kW, = tan(acos(pf)).
	qPerKW float64
}

// runHosting is POST /analysis/hosting-capacity: a DER hosting-capacity study.
// It resolves the feeder once, then for each candidate node binary-searches the
// largest generator injection there that keeps every node at or below
// max_voltage_pu and every branch/transformer at or below 100 % loading. It adds
// no new electrical model -- a generator is negative demand (the sweep already
// carries signed P/Q), over-voltage reuses Slice 1's band, thermal reuses
// load-flow's own loading check (formatResults). Read-only.
func runHosting(ctx context.Context, db bun.IDB, req hostingRequest) (*hostingResponse, error) {
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

	pf := 1.0
	if req.PowerFactor != nil {
		pf = *req.PowerFactor
	}
	if pf <= 0 || pf > 1 {
		return nil, invalid("power_factor must be in (0, 1]")
	}
	// A generator at power factor pf injects kVAR = kW·tan(acos(pf)) alongside
	// its real power. At unity (the default) this is 0 -- real power only.
	qPerKW := math.Tan(math.Acos(pf))

	maxInjKW := defaultMaxInjectionKW
	if req.MaxInjectionKW != nil {
		maxInjKW = *req.MaxInjectionKW
	}
	if maxInjKW <= 0 {
		return nil, invalid("max_injection_kw must be a positive number")
	}

	if req.NominalVoltageV <= 0 {
		return nil, invalid("nominal_voltage_v must be a positive number")
	}

	// Resolve the feeder ONCE: walk the tree and load every node's electrical
	// inputs. Every trial injection below re-runs only the in-memory sweep.
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

	study := &hostingStudy{
		tr: tr, states: states, start: start, classCodes: classCodes,
		nominalV: req.NominalVoltageV, sourceR: sourceR, sourceX: sourceX,
		maxPu: maxPu, maxInjKW: maxInjKW, qPerKW: qPerKW,
	}

	// Candidate interconnection nodes: an explicit pick, or every non-source
	// apparatus node on the feeder (conductors are line segments, not
	// interconnection points, so they're skipped in the feeder-wide scan).
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
			// Skip a node with no resolved state (dangling connectivity) and a
			// conductor (line segments are not interconnection points).
			st := states[n]
			if st == nil || st.loaded.FeatureClass.NetworkRole == "edge" {
				continue
			}
			candidates = append(candidates, n)
		}
	}

	out := make([]hostingCandidate, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, study.size(c))
	}

	return &hostingResponse{
		Source:          req.Source,
		NominalVoltageV: req.NominalVoltageV,
		MinVoltagePu:    minPu,
		MaxVoltagePu:    maxPu,
		PowerFactor:     pf,
		Candidates:      out,
	}, nil
}

// size finds one candidate node's hosting capacity: an exponential ramp to
// bracket the first infeasible injection, then a bisection to pin the largest
// feasible one, then a name for whichever limit binds at that capacity.
func (s *hostingStudy) size(c node) hostingCandidate {
	ref := featureRef{FeatureClass: s.classCodes[c.classID], FeatureID: c.featureID}
	res := hostingCandidate{Node: ref}

	// Base case: no injection. If the feeder already violates a limit, there is
	// no headroom for DER here at all -- report 0 and say why.
	base := s.evaluate(c, 0)
	if !base.feasible {
		res.HostingCapacityKW = 0
		res.WorstVoltagePu = base.worstHighPu
		res.WorstLoadingPct = base.worstLoadingPct
		res.BindingConstraint = bindingPreExisting
		if base.worstHighPu > s.maxPu {
			bf := base.ovFeature
			res.BindingFeature = &bf
			res.Note = "the feeder is already over-voltage before any DER -- no hosting headroom at this node"
		} else {
			bf := base.loadFeature
			res.BindingFeature = &bf
			res.Note = "the feeder already has a thermal overload before any DER -- no hosting headroom at this node"
		}
		return res
	}

	// Exponential ramp: double the injection until it becomes infeasible or hits
	// the search cap. lo stays the largest known-feasible injection, hi the
	// smallest known-infeasible one.
	lo := 0.0
	hi := rampStartKW
	var hiEval hostingEval
	feasibleAtCap := false
	for {
		if hi >= s.maxInjKW {
			hi = s.maxInjKW
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
		res.HostingCapacityKW = s.maxInjKW
		res.WorstVoltagePu = hiEval.worstHighPu
		res.WorstLoadingPct = hiEval.worstLoadingPct
		res.BindingConstraint = bindingNone
		res.Unbounded = true
		res.Note = "no limit binds up to max_injection_kw -- hosting capacity is at least this"
		return res
	}

	// Bisect [lo feasible, hi infeasible] toward the largest feasible injection.
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

	res.HostingCapacityKW = lo
	res.WorstVoltagePu = loEval.worstHighPu
	res.WorstLoadingPct = loEval.worstLoadingPct
	// At the capacity both limits are satisfied; the binding one is whichever
	// sits closest to its ceiling (smallest remaining margin) -- the limit the
	// next kW of injection would cross.
	ovMargin := s.maxPu - loEval.worstHighPu
	thMargin := 1.0 - loEval.worstLoadingPct/100
	if ovMargin <= thMargin {
		res.BindingConstraint = bindingOvervoltage
		bf := loEval.ovFeature
		res.BindingFeature = &bf
	} else {
		res.BindingConstraint = bindingThermal
		bf := loEval.loadFeature
		res.BindingFeature = &bf
	}
	return res
}

// evaluate re-sweeps the feeder with injKW (and its power-factor kVAR) injected
// at node c -- modelled as negative demand at that node -- and reports whether
// every node stays at or below max_voltage_pu and every branch/transformer at or
// below 100 % loading, plus the worst voltage/loading and where. It mutates the
// candidate's own demand for the sweep and restores it before returning, so
// candidates don't contaminate each other.
func (s *hostingStudy) evaluate(c node, injKW float64) hostingEval {
	st := s.states[c]
	if st == nil {
		return hostingEval{feasible: true}
	}
	savedKW, savedKVAR := st.ownKW, st.ownKVAR
	// A generator is negative demand: subtract the injection from the node's own
	// load so the backward sweep nets it against everything downstream.
	st.ownKW = savedKW - injKW
	st.ownKVAR = savedKVAR - injKW*s.qPerKW

	backwardSweep(s.tr, s.states)
	forwardSweep(s.tr, s.states, s.start, s.nominalV, s.sourceR, s.sourceX)

	nodes, overloaded := formatResults(s.tr, s.states, s.classCodes)

	eval := hostingEval{}
	for _, r := range nodes {
		if r.NominalBaseV > 0 {
			pu := r.VoltageV / r.NominalBaseV
			if pu > eval.worstHighPu {
				eval.worstHighPu = pu
				eval.ovFeature = featureRef{FeatureClass: r.FeatureClass, FeatureID: r.FeatureID}
			}
		}
		if r.LoadingPct != nil && *r.LoadingPct > eval.worstLoadingPct {
			eval.worstLoadingPct = *r.LoadingPct
			eval.loadFeature = featureRef{FeatureClass: r.FeatureClass, FeatureID: r.FeatureID}
		}
	}
	eval.feasible = eval.worstHighPu <= s.maxPu && len(overloaded) == 0

	st.ownKW, st.ownKVAR = savedKW, savedKVAR
	return eval
}
