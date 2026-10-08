package powerflow

// Load-growth FORECAST (docs/load-growth-forecast-design.md), Slice 1: engine core.
//
// A scenario projection of the radial load-flow along a TIME axis. Given a blanket
// load-growth rate and a horizon, it scales every load's demand year by year, re-runs
// the sweep, and reports each rated asset's loading trajectory + the first year it
// crosses its thermal limit -- the "time to overload" a planner sizes capex against.
//
// It is NOT a statistical forecast: the growth rate is an ASSUMPTION the caller
// supplies, so the result is a deterministic what-if (echoed in Assumptions, labelled
// Validation="derived"). See the design doc for the full scope; this slice is blanket
// %/yr growth + thermal (transformer loading + conductor ampacity) only. Per-feeder /
// per-class growth, hand-entered block loads, planned-asset pipeline, DER headroom,
// and voltage-band violations land in later slices.
//
// The engine reuse is the whole point: build the tree + resolve node states ONCE (the
// query-heavy part), then for each year scale the per-node own-demand and re-run only
// the in-memory backward/forward passes + formatResults. runSweep is untouched -- the
// existing endpoints keep their exact numerics -- this file just drives the same
// internal passes in a loop with scaled demand.

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/uptrace/bun"
)

const (
	// forecastMaxHorizon caps the projection length -- a horizon beyond a couple of
	// decades is meaningless for a deterministic growth assumption, and the cap keeps
	// the response bounded.
	forecastMaxHorizon = 60

	// forecastStressThreshold is the peak loading (%) at or above which an asset is
	// worth reporting. Below it the asset has comfortable headroom across the whole
	// horizon and would only be noise in the result.
	forecastStressThreshold = 80.0

	// forecastViolation is the loading (%) that counts as an overload -- the same
	// 100%-of-rating line formatResults flags.
	forecastViolation = 100.0

	// operationalStatePlanned is the OPERATIONAL_STATE code for a not-yet-built asset
	// (seed/operational_state_example.sql: PLANNED -> IN_SERVICE -> ISOLATED -> RETIRED).
	operationalStatePlanned = "PLANNED"

	// defaultPlannedDateField is the attribute a planned asset's in-service year is
	// read from when the request names none -- a conventional date field a utility opts
	// into (no schema change; absent on a class that doesn't maintain it).
	defaultPlannedDateField = "in_service_date"

	// defaultPlannedYear is the fallback in-service year for a planned asset with no
	// date field and no per-asset override.
	defaultPlannedYear = 1
)

// forecastRequest is POST /analysis/load-growth's body. Start + NominalVoltageV are
// the same radial-load-flow inputs every sweep takes (nominal may be 0 at a boundary-
// transformer start, where the secondary voltage is derived). HorizonYears is the
// projection length; BaseGrowthPct is the blanket compound growth applied to every
// load each year.
//
// PerFeeder / PerClass (Slice 2) are optional growth-rate overrides, keyed by circuit
// ref and feature-class code respectively. A load's rate is resolved most-specific-
// first: when both a feeder and a class rate match, their factors MULTIPLY; else the
// one that matches; else BaseGrowthPct. BlockLoads (Slice 2) are hand-entered new
// demand switched on at a given year at an existing feature.
type forecastRequest struct {
	Start           featureRef `json:"start"`
	NominalVoltageV float64    `json:"nominal_voltage_v"`
	HorizonYears    int        `json:"horizon_years"`
	BaseGrowthPct   float64    `json:"base_growth_pct"`

	PerFeeder  map[string]float64 `json:"per_feeder,omitempty"`
	PerClass   map[string]float64 `json:"per_class,omitempty"`
	BlockLoads []blockLoad        `json:"block_loads,omitempty"`

	// IncludePlanned (Slice 3) brings operational_state=PLANNED assets in as FUTURE
	// load, switched on at their in-service year. Off (default), planned assets carry
	// ZERO demand every year -- so the base year-0 case is always in-service-only and
	// the flag can never double-count. The in-service year per planned asset resolves
	// most-specific-first: PlannedDateField (an attribute date, default in_service_date,
	// interpreted as a year offset from today) -> PlannedYearOverrides[feature_id] ->
	// PlannedDefaultYear (nil = 1). An asset whose year lands beyond the horizon simply
	// never appears.
	IncludePlanned       bool           `json:"include_planned"`
	PlannedDateField     string         `json:"planned_date_field,omitempty"`
	PlannedYearOverrides map[string]int `json:"planned_year_overrides,omitempty"`
	PlannedDefaultYear   *int           `json:"planned_default_year,omitempty"`

	// DERHeadroom (Slice 4) adds the DER-headroom-over-time lens: at each year it runs
	// the hosting-capacity study under that year's grown load, so the result carries a
	// per-node curve of how much DER the feeder can still host as load rises. DERNodes
	// restricts the scan (empty = every apparatus node, like the hosting study). The
	// remaining fields are the hosting search's own knobs (same defaults). Committed
	// FUTURE DER is entered as negative block_loads (a known injection switched on at a
	// year); a per-class DER adoption curve is a later refinement.
	DERHeadroom       bool         `json:"der_headroom"`
	DERNodes          []featureRef `json:"der_nodes,omitempty"`
	DERPowerFactor    *float64     `json:"der_power_factor,omitempty"`
	DERMaxVoltagePu   *float64     `json:"der_max_voltage_pu,omitempty"`
	DERMaxInjectionKW *float64     `json:"der_max_injection_kw,omitempty"`
}

// yearFromDateAttr interprets a date-valued attribute as a year OFFSET from nowYear
// (the reference for turning absolute in-service dates into horizon years). It accepts
// a time.Time, a common date string, or a bare 4-digit year; a past date yields a
// negative offset the caller floors to 0. Returns ok=false when the value is absent or
// unparseable, so the caller falls through to the next precedence tier.
func yearFromDateAttr(v any, nowYear int) (int, bool) {
	switch t := v.(type) {
	case nil:
		return 0, false
	case time.Time:
		return t.Year() - nowYear, true
	case string:
		if t == "" {
			return 0, false
		}
		for _, layout := range []string{time.RFC3339, "2006-01-02", "2006-01-02T15:04:05", "2006/01/02"} {
			if parsed, err := time.Parse(layout, t); err == nil {
				return parsed.Year() - nowYear, true
			}
		}
		if y, err := strconv.Atoi(t); err == nil && y > 1900 && y < 3000 {
			return y - nowYear, true
		}
		return 0, false
	default:
		return 0, false
	}
}

// resolvePlannedYear applies the in-service-year precedence for one planned asset:
// date field -> per-asset override -> scenario default. A resolved year is floored at
// 0 (a past/already-due date is "on now"); it is NOT capped at the horizon -- a year
// beyond it means the asset never appears, which the loop handles naturally.
func resolvePlannedYear(req forecastRequest, attrs map[string]any, featureID, dateField string, nowYear int) int {
	if dateField != "" {
		if y, ok := yearFromDateAttr(attrs[dateField], nowYear); ok {
			return max(0, y)
		}
	}
	if y, ok := req.PlannedYearOverrides[featureID]; ok {
		return max(0, y)
	}
	d := defaultPlannedYear
	if req.PlannedDefaultYear != nil {
		d = *req.PlannedDefaultYear
	}
	return max(0, d)
}

// blockLoad is a known future demand (Slice 2): [kw]/[kvar] added at [attach_ref]'s
// node from [start_year] onward. Unlike grown load it is a fixed step (the planner
// states the mature size) -- e.g. a new estate or an EV depot energising in year 3.
type blockLoad struct {
	Attach    featureRef `json:"attach_ref"`
	KW        float64    `json:"kw"`
	KVAR      float64    `json:"kvar"`
	StartYear int        `json:"start_year"`
}

// growthRule is a node's resolved growth inputs (Slice 2): which of the per-feeder /
// per-class overrides matched it (and their rates). effectiveFactor turns it into the
// year-t multiplier.
type growthRule struct {
	hasFeeder bool
	feederPct float64
	hasClass  bool
	classPct  float64
}

// effectiveFactor is the compound load multiplier for a node at a given year, applying
// the most-specific growth rule: feeder ∧ class factors multiply when both match; else
// the single matching factor; else the blanket basePct. A pure kernel (unit-tested).
func effectiveFactor(rule growthRule, basePct float64, year int) float64 {
	switch {
	case rule.hasFeeder && rule.hasClass:
		return growthFactor(rule.feederPct, year) * growthFactor(rule.classPct, year)
	case rule.hasFeeder:
		return growthFactor(rule.feederPct, year)
	case rule.hasClass:
		return growthFactor(rule.classPct, year)
	default:
		return growthFactor(basePct, year)
	}
}

// forecastPoint is one year's loading for an asset.
type forecastPoint struct {
	Year       int     `json:"year"`
	LoadingPct float64 `json:"loading_pct"`
}

// forecastAsset is one stressed asset's projection: its limit (transformer rating or
// conductor ampacity), the first year it exceeds that limit (null if never within the
// horizon), its peak loading, and the full year-by-year trajectory.
type forecastAsset struct {
	FeatureClass       string          `json:"feature_class"`
	FeatureID          string          `json:"feature_id"`
	CircuitRef         string          `json:"circuit_ref,omitempty"`
	LimitKind          string          `json:"limit_kind"` // transformer_loading | ampacity
	Rating             float64         `json:"rating"`
	Unit               string          `json:"unit"` // kVA | A
	FirstViolationYear *int            `json:"first_violation_year"`
	PeakLoadingPct     float64         `json:"peak_loading_pct"`
	Trajectory         []forecastPoint `json:"trajectory"`
}

// derHeadroomPoint is one year's DER hosting capacity at a node.
type derHeadroomPoint struct {
	Year              int     `json:"year"`
	HostingCapacityKW float64 `json:"hosting_capacity_kw"`
	BindingConstraint string  `json:"binding_constraint"` // overvoltage | thermal | none | pre_existing
}

// derHeadroomAsset is one candidate interconnection node's DER-headroom trajectory --
// how much more DER it can host each year as load grows (Slice 4).
type derHeadroomAsset struct {
	FeatureClass string             `json:"feature_class"`
	FeatureID    string             `json:"feature_id"`
	CircuitRef   string             `json:"circuit_ref,omitempty"`
	Trajectory   []derHeadroomPoint `json:"trajectory"`
}

// forecastResponse is the load-growth study result: the stressed assets with their
// trajectories, the reinforcement order (violating assets, soonest + most severe
// first), the optional DER-headroom curves, the echoed assumptions, and the Derived
// validation label.
type forecastResponse struct {
	Start              featureRef         `json:"start"`
	NominalVoltageV    float64            `json:"nominal_voltage_v"`
	HorizonYears       int                `json:"horizon_years"`
	BaseGrowthPct      float64            `json:"base_growth_pct"`
	Assets             []forecastAsset    `json:"assets"`
	ReinforcementOrder []featureRef       `json:"reinforcement_order"`
	DERHeadroom        []derHeadroomAsset `json:"der_headroom,omitempty"`
	Assumptions        []string           `json:"assumptions"`
	Validation         string             `json:"validation"`
}

// growthFactor is the compound load multiplier at a given year: (1 + pct/100)^year.
// A pure kernel (unit-tested): year 0 is always 1, a negative pct models decline.
func growthFactor(pct float64, year int) float64 {
	return math.Pow(1+pct/100, float64(year))
}

// firstViolationYear returns the earliest year in traj whose loading exceeds
// threshold, or nil if none does. A pure kernel (unit-tested); traj is in year order.
func firstViolationYear(traj []forecastPoint, threshold float64) *int {
	for _, p := range traj {
		if p.LoadingPct > threshold {
			y := p.Year
			return &y
		}
	}
	return nil
}

// peakLoading returns the maximum loading across a trajectory.
func peakLoading(traj []forecastPoint) float64 {
	peak := 0.0
	for _, p := range traj {
		if p.LoadingPct > peak {
			peak = p.LoadingPct
		}
	}
	return peak
}

// runLoadGrowthForecast is POST /analysis/load-growth: project the radial load-flow
// across HorizonYears under a blanket growth rate and report each asset's loading
// trajectory + time-to-overload.
func runLoadGrowthForecast(ctx context.Context, db bun.IDB, req forecastRequest) (*forecastResponse, error) {
	if req.Start.FeatureClass == "" || req.Start.FeatureID == "" {
		return nil, invalid("start.feature_class and start.feature_id are required")
	}
	if req.NominalVoltageV < 0 {
		return nil, invalid("nominal_voltage_v must not be negative")
	}
	if req.HorizonYears <= 0 {
		return nil, invalid("horizon_years must be a positive number")
	}
	if req.HorizonYears > forecastMaxHorizon {
		return nil, invalid("horizon_years must be %d or fewer", forecastMaxHorizon)
	}
	if req.BaseGrowthPct < -50 || req.BaseGrowthPct > 100 {
		return nil, invalid("base_growth_pct must be between -50 and 100")
	}
	for ref, pct := range req.PerFeeder {
		if pct < -50 || pct > 100 {
			return nil, invalid("per_feeder[%q] must be between -50 and 100", ref)
		}
	}
	for code, pct := range req.PerClass {
		if pct < -50 || pct > 100 {
			return nil, invalid("per_class[%q] must be between -50 and 100", code)
		}
	}
	for i, bl := range req.BlockLoads {
		if bl.Attach.FeatureClass == "" || bl.Attach.FeatureID == "" {
			return nil, invalid("block_loads[%d].attach_ref.feature_class and feature_id are required", i)
		}
		if bl.StartYear < 0 || bl.StartYear > req.HorizonYears {
			return nil, invalid("block_loads[%d].start_year must be between 0 and horizon_years (%d)", i, req.HorizonYears)
		}
	}
	if req.PlannedDefaultYear != nil && *req.PlannedDefaultYear < 0 {
		return nil, invalid("planned_default_year must not be negative")
	}
	for id, y := range req.PlannedYearOverrides {
		if y < 0 {
			return nil, invalid("planned_year_overrides[%q] must not be negative", id)
		}
	}
	if req.DERPowerFactor != nil && (*req.DERPowerFactor <= 0 || *req.DERPowerFactor > 1) {
		return nil, invalid("der_power_factor must be in (0, 1]")
	}
	if req.DERMaxVoltagePu != nil && *req.DERMaxVoltagePu <= 0 {
		return nil, invalid("der_max_voltage_pu must be a positive number")
	}
	if req.DERMaxInjectionKW != nil && *req.DERMaxInjectionKW <= 0 {
		return nil, invalid("der_max_injection_kw must be a positive number")
	}

	// Build the tree + resolve node states ONCE -- the query-heavy work. Radial only
	// in Slice 1 (breaks = nil, so buildTree rejects a mesh exactly as the plain
	// load-flow does).
	start, err := resolveNode(ctx, db, req.Start)
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
	allNodes := append(append([]node{}, tr.order...), barrierHits...)
	classCodes, err := nodeClassCodes(ctx, db, allNodes)
	if err != nil {
		return nil, err
	}

	// Snapshot each node's base own-demand before we start scaling it in place. Only
	// true load nodes carry a non-zero demand; transformers/conductors are 0, so
	// scaling uniformly grows exactly the loads and the sweep re-accumulates the rest.
	type demand struct{ kw, kvar float64 }
	base := make(map[node]demand, len(states))
	for n, st := range states {
		if st != nil {
			base[n] = demand{st.ownKW, st.ownKVAR}
		}
	}

	// Resolve each node's growth rule once (Slice 2): which per-feeder / per-class
	// override matches it (feeder by the node's circuit ref, class by its class code).
	rules := make(map[node]growthRule, len(states))
	for n, st := range states {
		if st == nil {
			continue
		}
		var rule growthRule
		if len(req.PerClass) > 0 {
			if pct, ok := req.PerClass[classCodes[n.classID]]; ok {
				rule.hasClass, rule.classPct = true, pct
			}
		}
		if len(req.PerFeeder) > 0 {
			if _, ref := circuitIdentity(st); ref != "" {
				if pct, ok := req.PerFeeder[ref]; ok {
					rule.hasFeeder, rule.feederPct = true, pct
				}
			}
		}
		rules[n] = rule
	}

	// Resolve each block load's attach feature to a node in the swept network. A block
	// load attached to a feature not downstream of the start can't affect the result,
	// so reject it rather than silently drop it. Several block loads on one node sum.
	type blockDemand struct {
		kw, kvar  float64
		startYear int
	}
	nodeByRef := make(map[featureRef]node, len(tr.order))
	for _, n := range tr.order {
		nodeByRef[featureRef{FeatureClass: classCodes[n.classID], FeatureID: n.featureID}] = n
	}
	blocks := make(map[node][]blockDemand)
	for i, bl := range req.BlockLoads {
		n, ok := nodeByRef[featureRef{FeatureClass: bl.Attach.FeatureClass, FeatureID: bl.Attach.FeatureID}]
		if !ok {
			return nil, invalid("block_loads[%d]: attach feature %s/%s is not in the analysed network (downstream of the start)", i, bl.Attach.FeatureClass, bl.Attach.FeatureID)
		}
		blocks[n] = append(blocks[n], blockDemand{kw: bl.KW, kvar: bl.KVAR, startYear: bl.StartYear})
	}

	// Identify the PLANNED nodes and resolve each one's in-service year (Slice 3). This
	// runs regardless of IncludePlanned: when the flag is OFF these nodes are forced to
	// zero demand every year (so the base is in-service-only and can never double-count
	// a planned asset); when ON, their demand switches on at the resolved year. Only
	// their DEMAND is gated -- a planned node's topology, if it is in the tree, is not
	// pruned, which is correct for the common case (planned = new leaf loads + their
	// service lines, carrying no flow once their demand is zero); a planned asset that
	// re-routes existing load is a reinforcement study, not a growth forecast.
	nowYear := time.Now().Year()
	dateField := req.PlannedDateField
	if dateField == "" {
		dateField = defaultPlannedDateField
	}
	plannedISY := make(map[node]int)
	for n, st := range states {
		if st == nil {
			continue
		}
		if s, _ := st.attrs["operational_state"].(string); s == operationalStatePlanned {
			plannedISY[n] = resolvePlannedYear(req, st.attrs, n.featureID, dateField, nowYear)
		}
	}

	// DER-headroom setup (Slice 4): resolve the candidate interconnection nodes + the
	// hosting search's parameters ONCE, so each year is a pure in-memory hosting run
	// over that year's grown load. Candidates default to every non-source apparatus
	// node (conductors aren't interconnection points), exactly like the hosting study.
	type derAcc struct {
		class, id, circuitRef string
		traj                  []derHeadroomPoint
	}
	var derCandidates []node
	var derMaxPu, derMaxInjKW, derQPerKW float64
	derAccs := make(map[node]*derAcc)
	if req.DERHeadroom {
		derMaxPu = defaultMaxVoltagePu
		if req.DERMaxVoltagePu != nil {
			derMaxPu = *req.DERMaxVoltagePu
		}
		derMaxInjKW = defaultMaxInjectionKW
		if req.DERMaxInjectionKW != nil {
			derMaxInjKW = *req.DERMaxInjectionKW
		}
		pf := 1.0
		if req.DERPowerFactor != nil {
			pf = *req.DERPowerFactor
		}
		derQPerKW = math.Tan(math.Acos(pf))

		if len(req.DERNodes) > 0 {
			for i, ref := range req.DERNodes {
				n, ok := nodeByRef[featureRef{FeatureClass: ref.FeatureClass, FeatureID: ref.FeatureID}]
				if !ok {
					return nil, invalid("der_nodes[%d]: %s/%s is not in the analysed network (downstream of the start)", i, ref.FeatureClass, ref.FeatureID)
				}
				derCandidates = append(derCandidates, n)
			}
		} else {
			for _, n := range tr.order {
				if n == start {
					continue
				}
				st := states[n]
				if st == nil || st.loaded.FeatureClass.NetworkRole == "edge" {
					continue
				}
				derCandidates = append(derCandidates, n)
			}
		}
		for _, c := range derCandidates {
			_, ref := circuitIdentity(states[c])
			derAccs[c] = &derAcc{class: classCodes[c.classID], id: c.featureID, circuitRef: ref}
		}
	}

	// Per-asset trajectory, keyed by class|id, built up over the year loop. order
	// preserves first-seen (tree) order so the output is stable.
	type assetAcc struct {
		class, id, circuitRef, limitKind, unit string
		rating                                 float64
		traj                                   []forecastPoint
	}
	accs := make(map[string]*assetAcc)
	var order []string
	var usedNominalV float64

	for t := 0; t <= req.HorizonYears; t++ {
		for n, st := range states {
			if st == nil {
				continue
			}
			b := base[n]
			if isy, planned := plannedISY[n]; planned {
				// Planned asset: zero until it is both included and in service, then its
				// demand grows from its in-service year (exponent t-isy, so it comes on at
				// its stated size and grows thereafter at its resolved rate).
				if !req.IncludePlanned || t < isy {
					st.ownKW, st.ownKVAR = 0, 0
					continue
				}
				f := effectiveFactor(rules[n], req.BaseGrowthPct, t-isy)
				st.ownKW, st.ownKVAR = b.kw*f, b.kvar*f
				continue
			}
			f := effectiveFactor(rules[n], req.BaseGrowthPct, t)
			st.ownKW, st.ownKVAR = b.kw*f, b.kvar*f
		}
		// Add any block loads active by this year, on top of the grown base demand.
		for n, bls := range blocks {
			st := states[n]
			if st == nil {
				continue
			}
			for _, bl := range bls {
				if t >= bl.startYear {
					st.ownKW += bl.kw
					st.ownKVAR += bl.kvar
				}
			}
		}

		backwardSweep(tr, states)
		sourceR, sourceX, startBaseV, _ := sourceFromStart(states[start], req.NominalVoltageV)
		if startBaseV <= 0 {
			return nil, errNominalRequired()
		}
		usedNominalV = startBaseV
		forwardSweep(tr, states, start, startBaseV, sourceR, sourceX)

		nodes, _ := formatResults(tr, states, classCodes)
		for i := range nodes {
			nr := &nodes[i]
			if nr.LoadingPct == nil {
				continue // only rated nodes + ampacity-resolved conductors have a loading
			}
			key := nr.FeatureClass + "|" + nr.FeatureID
			a := accs[key]
			if a == nil {
				a = &assetAcc{class: nr.FeatureClass, id: nr.FeatureID, circuitRef: nr.CircuitRef}
				switch {
				case nr.RatedKVA != nil:
					a.limitKind, a.unit, a.rating = "transformer_loading", "kVA", *nr.RatedKVA
				case nr.AmpacityAmps != nil:
					a.limitKind, a.unit, a.rating = "ampacity", "A", *nr.AmpacityAmps
				}
				accs[key] = a
				order = append(order, key)
			}
			a.traj = append(a.traj, forecastPoint{Year: t, LoadingPct: *nr.LoadingPct})
		}

		// DER headroom under this year's load (Slice 4): a hosting study over the SAME
		// year-t states. size() mutates states during its bisection but restores each
		// candidate's demand; the load trajectory above is already captured, and next
		// year re-scales + re-sweeps fresh, so running it last is safe.
		if req.DERHeadroom {
			study := &hostingStudy{
				tr: tr, states: states, start: start, classCodes: classCodes,
				nominalV: startBaseV, sourceR: sourceR, sourceX: sourceX,
				maxPu: derMaxPu, maxInjKW: derMaxInjKW, qPerKW: derQPerKW,
			}
			for _, c := range derCandidates {
				hc := study.size(c)
				derAccs[c].traj = append(derAccs[c].traj, derHeadroomPoint{
					Year:              t,
					HostingCapacityKW: hc.HostingCapacityKW,
					BindingConstraint: hc.BindingConstraint,
				})
			}
		}
	}

	// Keep only the assets that actually get stressed, compute each one's first
	// overload year + peak.
	assets := make([]forecastAsset, 0, len(order))
	for _, key := range order {
		a := accs[key]
		peak := peakLoading(a.traj)
		if peak < forecastStressThreshold {
			continue
		}
		assets = append(assets, forecastAsset{
			FeatureClass:       a.class,
			FeatureID:          a.id,
			CircuitRef:         a.circuitRef,
			LimitKind:          a.limitKind,
			Rating:             a.rating,
			Unit:               a.unit,
			FirstViolationYear: firstViolationYear(a.traj, forecastViolation),
			PeakLoadingPct:     peak,
			Trajectory:         a.traj,
		})
	}

	// Reinforcement order: the assets that DO overload within the horizon, soonest
	// first, ties broken by the more severe peak.
	violating := make([]forecastAsset, 0, len(assets))
	for _, a := range assets {
		if a.FirstViolationYear != nil {
			violating = append(violating, a)
		}
	}
	sort.SliceStable(violating, func(i, j int) bool {
		if *violating[i].FirstViolationYear != *violating[j].FirstViolationYear {
			return *violating[i].FirstViolationYear < *violating[j].FirstViolationYear
		}
		return violating[i].PeakLoadingPct > violating[j].PeakLoadingPct
	})
	reinforcement := make([]featureRef, 0, len(violating))
	for _, a := range violating {
		reinforcement = append(reinforcement, featureRef{FeatureClass: a.FeatureClass, FeatureID: a.FeatureID})
	}

	// DER-headroom curves (Slice 4), in tree order.
	var derHeadroom []derHeadroomAsset
	if req.DERHeadroom {
		derHeadroom = make([]derHeadroomAsset, 0, len(derCandidates))
		for _, c := range derCandidates {
			a := derAccs[c]
			derHeadroom = append(derHeadroom, derHeadroomAsset{
				FeatureClass: a.class,
				FeatureID:    a.id,
				CircuitRef:   a.circuitRef,
				Trajectory:   a.traj,
			})
		}
	}

	assumptions := []string{
		fmt.Sprintf("%.2f%%/yr blanket load growth over %d year(s)", req.BaseGrowthPct, req.HorizonYears),
	}
	if len(req.PerFeeder) > 0 || len(req.PerClass) > 0 {
		assumptions = append(assumptions, fmt.Sprintf(
			"growth overrides: %d per-feeder, %d per-class (feeder and class rates multiply where both match a load)",
			len(req.PerFeeder), len(req.PerClass)))
	}
	if len(req.BlockLoads) > 0 {
		assumptions = append(assumptions, fmt.Sprintf(
			"%d hand-entered block load(s) switched on at their stated year", len(req.BlockLoads)))
	}
	if req.IncludePlanned {
		assumptions = append(assumptions, fmt.Sprintf(
			"%d planned asset(s) included as future load (in-service year from the %q field, else a per-asset override, else year %d)",
			len(plannedISY), dateField, func() int {
				if req.PlannedDefaultYear != nil {
					return *req.PlannedDefaultYear
				}
				return defaultPlannedYear
			}()))
	} else if len(plannedISY) > 0 {
		assumptions = append(assumptions, fmt.Sprintf(
			"%d planned asset(s) excluded (include_planned off) -- base is in-service only", len(plannedISY)))
	}
	if req.DERHeadroom {
		assumptions = append(assumptions, fmt.Sprintf(
			"DER headroom (hosting capacity) sized at %d candidate node(s) each year under the grown load; committed future DER can be entered as negative block loads", len(derCandidates)))
	}
	assumptions = append(assumptions,
		"a scenario projection under your stated assumptions -- not a statistical prediction of actual future demand",
		"thermal loading only for the overload timeline (transformer rating + conductor ampacity); voltage-band violations are a later slice",
	)

	return &forecastResponse{
		Start:              req.Start,
		NominalVoltageV:    usedNominalV,
		HorizonYears:       req.HorizonYears,
		BaseGrowthPct:      req.BaseGrowthPct,
		Assets:             assets,
		ReinforcementOrder: reinforcement,
		DERHeadroom:        derHeadroom,
		Assumptions:        assumptions,
		Validation:         "derived",
	}, nil
}
