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
)

// forecastRequest is POST /analysis/load-growth's body (Slice 1). Start +
// NominalVoltageV are the same radial-load-flow inputs every sweep takes (nominal may
// be 0 at a boundary-transformer start, where the secondary voltage is derived).
// HorizonYears is the projection length; BaseGrowthPct is the blanket compound growth
// applied to every load each year.
type forecastRequest struct {
	Start           featureRef `json:"start"`
	NominalVoltageV float64    `json:"nominal_voltage_v"`
	HorizonYears    int        `json:"horizon_years"`
	BaseGrowthPct   float64    `json:"base_growth_pct"`
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

// forecastResponse is the load-growth study result: the stressed assets with their
// trajectories, the reinforcement order (violating assets, soonest + most severe
// first), the echoed assumptions, and the Derived validation label.
type forecastResponse struct {
	Start              featureRef      `json:"start"`
	NominalVoltageV    float64         `json:"nominal_voltage_v"`
	HorizonYears       int             `json:"horizon_years"`
	BaseGrowthPct      float64         `json:"base_growth_pct"`
	Assets             []forecastAsset `json:"assets"`
	ReinforcementOrder []featureRef    `json:"reinforcement_order"`
	Assumptions        []string        `json:"assumptions"`
	Validation         string          `json:"validation"`
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
		f := growthFactor(req.BaseGrowthPct, t)
		for n, st := range states {
			if st == nil {
				continue
			}
			b := base[n]
			st.ownKW = b.kw * f
			st.ownKVAR = b.kvar * f
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

	return &forecastResponse{
		Start:              req.Start,
		NominalVoltageV:    usedNominalV,
		HorizonYears:       req.HorizonYears,
		BaseGrowthPct:      req.BaseGrowthPct,
		Assets:             assets,
		ReinforcementOrder: reinforcement,
		Assumptions: []string{
			fmt.Sprintf("%.2f%%/yr blanket load growth over %d year(s)", req.BaseGrowthPct, req.HorizonYears),
			"a scenario projection under your stated growth assumption -- not a statistical prediction of actual future demand",
			"thermal loading only (transformer rating + conductor ampacity); voltage-band, per-feeder/class growth, planned assets and DER headroom are later slices",
		},
		Validation: "derived",
	}, nil
}
