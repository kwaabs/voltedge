package powerflow

// fusesaving.go is the RECLOSER / FUSE-SAVING scheme check -- POST
// /analysis/fuse-saving. Where coordination.go grades series protective pairs on
// operate-time margin, this evaluates the two-curve reclose scheme a feeder
// recloser runs against each downstream lateral fuse:
//
//   * FUSE SAVING (fast curve vs fuse minimum-melt): on a TRANSIENT fault the
//     recloser trips on its FAST curve before the fuse starts to melt, so the
//     fuse is "saved" -- the lateral is not lost for a fault that clears on its
//     own. Holds while t_fast(I) < t_fuse_min_melt(I).
//   * FUSE CLEARING (slow curve vs fuse total-clear): on a PERMANENT fault the
//     recloser switches to its SLOW (delayed) curve after reclosing, giving the
//     fuse time to clear the lateral before the recloser locks out the whole
//     feeder. Holds while t_fuse_total_clear(I) < t_slow(I).
//
// It walks the feeder below the source once, finds each fuse's nearest upstream
// recloser, and reports both verdicts at the prospective fault current at the
// fuse -- plus the fuse-saving CROSSOVER current (where the fast curve overtakes
// the fuse minimum-melt), the upper bound of the current range the scheme saves
// the fuse over. One report answers "does every lateral fuse coordinate with its
// recloser's reclose scheme, and up to what fault current?".

import (
	"context"
	"math"
	"sort"

	"github.com/uptrace/bun"
)

// maxFuseSavingPairs bounds a pathological feeder (one more curve read per pair).
// A real feeder has far fewer recloser/fuse laterals than this; hitting it sets
// Truncated so the report is never read as exhaustive.
const maxFuseSavingPairs = 60

// recloserCurveBands is the band preference for reading a recloser fast/slow
// curve's operate time: a recloser characteristic is tabulated under "operate"
// (a single-band curve), but older seeds carry it under the ampere-curve bands
// evalRecloser already grades reclosers on (total_clear, then minimum_melt), so
// fall back through those rather than failing to resolve the curve.
var recloserCurveBands = []string{bandOperate, bandTotalClear, bandMinMelt}

type fuseSavingRequest struct {
	Start           featureRef `json:"start"`
	NominalVoltageV float64    `json:"nominal_voltage_v"`
	VoltageBasis    string     `json:"voltage_basis"`
	VoltageFactorC  float64    `json:"voltage_factor_c,omitempty"`
}

// fuseSavingPair is one recloser -> downstream-fuse scheme evaluation. The four
// time fields are the operate times at FaultCurrentAmps (each read on its own
// device's base); FuseSaved / FuseClears are the two scheme verdicts, each nil
// when a curve it needs was unresolved. MaxSaveCurrentAmps is the fuse-saving
// crossover (fast == min-melt), on the fuse's base -- absent when the fast and
// min-melt curves never cross over their shared current range (the fuse is saved
// across it; the verdict at the fault current says which side).
type fuseSavingPair struct {
	Recloser featureRef `json:"recloser"`
	Fuse     featureRef `json:"fuse"`

	FaultCurrentAmps *float64 `json:"fault_current_amps,omitempty"`

	FastTimeS       *float64 `json:"fast_time_s,omitempty"`
	SlowTimeS       *float64 `json:"slow_time_s,omitempty"`
	FuseMinMeltS    *float64 `json:"fuse_min_melt_s,omitempty"`
	FuseTotalClearS *float64 `json:"fuse_total_clear_s,omitempty"`

	FuseSaved          *bool    `json:"fuse_saved,omitempty"`
	FuseClears         *bool    `json:"fuse_clears,omitempty"`
	MaxSaveCurrentAmps *float64 `json:"max_save_current_amps,omitempty"`

	Note string `json:"note,omitempty"`
}

type fuseSavingResponse struct {
	Start           featureRef       `json:"start"`
	PairCount       int              `json:"pair_count"`
	FuseSavedCount  int              `json:"fuse_saved_count"`
	FuseClearsCount int              `json:"fuse_clears_count"`
	IssueCount      int              `json:"issue_count"`
	Pairs           []fuseSavingPair `json:"pairs"`
	Truncated       bool             `json:"truncated"`
}

func runFuseSaving(ctx context.Context, db bun.IDB, req fuseSavingRequest) (*fuseSavingResponse, error) {
	if req.Start.FeatureClass == "" || req.Start.FeatureID == "" {
		return nil, invalid("start.feature_class and start.feature_id are required")
	}
	if req.NominalVoltageV < 0 {
		return nil, invalid("nominal_voltage_v must not be negative")
	}
	if req.VoltageBasis != "" && req.VoltageBasis != basisLineToLine && req.VoltageBasis != basisLineToNeutral {
		return nil, invalid("voltage_basis must be %q or %q", basisLineToLine, basisLineToNeutral)
	}
	if req.VoltageFactorC < 0 {
		return nil, invalid("voltage_factor_c must not be negative")
	}
	phaseDiv := phaseDivisor(req.VoltageBasis)

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

	sourceR, sourceX, startBaseV, _ := sourceFromStart(states[start], req.NominalVoltageV)
	if startBaseV <= 0 {
		return nil, errNominalRequired()
	}
	paths := accumulateSeriesImpedance(tr, states, start, startBaseV, sourceR, sourceX)

	allNodes := append(append([]node{}, tr.order...), barrierHits...)
	classCodes, err := nodeClassCodes(ctx, db, allNodes)
	if err != nil {
		return nil, err
	}

	curves := newCurveCache()
	pairs := make([]fuseSavingPair, 0)
	truncated := false
	for _, n := range tr.order {
		code := classCodes[n.classID]
		fuseRef, isFuse := fuseCurveRefFor(states[n], code)
		if !isFuse {
			continue
		}
		rec, ok := nearestUpstreamRecloser(tr, n, start, classCodes)
		if !ok {
			continue // a fuse with no recloser above it is not part of any reclose scheme
		}
		if len(pairs) >= maxFuseSavingPairs {
			truncated = true
			break
		}
		pairs = append(pairs, evalFuseSavingPair(ctx, db, rec, n, fuseRef, states, paths, classCodes, curves, phaseDiv, req.VoltageFactorC))
	}

	saved, clears, issues := 0, 0, 0
	for _, p := range pairs {
		if p.FuseSaved != nil && *p.FuseSaved {
			saved++
		}
		if p.FuseClears != nil && *p.FuseClears {
			clears++
		}
		if (p.FuseSaved != nil && !*p.FuseSaved) || (p.FuseClears != nil && !*p.FuseClears) {
			issues++
		}
	}
	// Problem pairs (either verdict failed) first, then by fuse id for stability.
	sort.SliceStable(pairs, func(i, j int) bool { return fuseSavingLess(pairs[i], pairs[j]) })

	return &fuseSavingResponse{
		Start:           req.Start,
		PairCount:       len(pairs),
		FuseSavedCount:  saved,
		FuseClearsCount: clears,
		IssueCount:      issues,
		Pairs:           pairs,
		Truncated:       truncated,
	}, nil
}

// fuseCurveRefFor reports whether a node is a fuse-like protective element and,
// if so, its ampere-quantity curve_ref. A plain MV_FUSE carries curve_ref
// directly; a generic MV_SWITCH / LV_SWITCH only when its is_fused flag is set
// (the same rule evalFusedSwitch applies). Any other class, a nil state, or a
// missing curve_ref is not a fuse for this check.
func fuseCurveRefFor(st *nodeState, code string) (string, bool) {
	if st == nil || st.attrs == nil {
		return "", false
	}
	switch code {
	case classMVFuse:
	case classMVSwitch, classLVSwitch:
		if fused, _ := st.attrs[switchIsFusedField].(bool); !fused {
			return "", false
		}
	default:
		return "", false
	}
	ref, _ := st.attrs[fuseCurveRefField].(string)
	if ref == "" {
		return "", false
	}
	return ref, true
}

// nearestUpstreamRecloser walks the parent chain up from a fuse toward the start
// and returns the first MV_RECLOSER it meets (the start itself included). That
// recloser is the device whose reclose scheme protects this fuse. ok is false
// when the chain reaches the start without passing a recloser.
func nearestUpstreamRecloser(tr *tree, fuse, start node, classCodes map[string]string) (node, bool) {
	for cur := fuse; cur != start; {
		p, ok := tr.parent[cur]
		if !ok {
			return node{}, false
		}
		if classCodes[p.classID] == classMVRecloser {
			return p, true
		}
		cur = p
	}
	return node{}, false
}

// evalFuseSavingPair computes both scheme verdicts for one recloser/fuse pair at
// the prospective fault current at the fuse. The recloser's fast/slow curves are
// read at the fault current referred to the recloser's base (power through a
// series path is conserved across an ideal transformer); the fuse's min-melt /
// total-clear at the fuse's own base. It never mutates shared state beyond the
// (memoising) curve cache.
func evalFuseSavingPair(ctx context.Context, db bun.IDB, rec, fuse node, fuseRef string, states map[node]*nodeState, paths map[node]pathImpedance, classCodes map[string]string, curves *curveCache, phaseDiv, reqC float64) fuseSavingPair {
	p := fuseSavingPair{
		Recloser: featureRef{FeatureClass: classCodes[rec.classID], FeatureID: rec.featureID},
		Fuse:     featureRef{FeatureClass: classCodes[fuse.classID], FeatureID: fuse.featureID},
	}

	fp := paths[fuse]
	rp := paths[rec]
	faultZ := math.Hypot(fp.rOhm, fp.xOhm)
	faultC := effectiveFaultC(reqC, fp.baseV)
	if faultZ <= 0 || fp.baseV <= 0 {
		p.Note = "indeterminate fault current at the fuse (zero source impedance or base)"
		return p
	}
	iFault := (faultC * fp.baseV / phaseDiv) / faultZ
	p.FaultCurrentAmps = &iFault

	throughFuse := iFault
	throughRec := iFault
	if rp.baseV > 0 {
		throughRec = iFault * fp.baseV / rp.baseV
	}

	// Recloser fast/slow operate times at the recloser-base current.
	recSt := states[rec]
	var fastRef, slowRef string
	if recSt != nil && recSt.attrs != nil {
		fastRef, _ = recSt.attrs[recloserFastCurveField].(string)
		slowRef, _ = recSt.attrs[recloserSlowCurveField].(string)
	}
	var fastPts []curvePoint
	if fastRef != "" {
		if pts, ok := firstBandPoints(ctx, db, curves, fastRef, recloserCurveBands); ok {
			fastPts = pts
			t, _ := interpLogLog(pts, throughRec)
			p.FastTimeS = &t
		}
	}
	if slowRef != "" {
		if pts, ok := firstBandPoints(ctx, db, curves, slowRef, recloserCurveBands); ok {
			t, _ := interpLogLog(pts, throughRec)
			p.SlowTimeS = &t
		}
	}

	// Fuse bounding bands at the fuse-base current.
	var mmPts []curvePoint
	if pts, err := curves.points(ctx, db, fuseRef, bandMinMelt); err == nil && len(pts) > 0 {
		mmPts = pts
		t, _ := interpLogLog(pts, throughFuse)
		p.FuseMinMeltS = &t
	}
	if pts, err := curves.points(ctx, db, fuseRef, bandTotalClear); err == nil && len(pts) > 0 {
		t, _ := interpLogLog(pts, throughFuse)
		p.FuseTotalClearS = &t
	}

	// Verdicts. Fuse saved: fast clears before the fuse begins to melt. Fuse
	// clears: the fuse totally clears before the recloser's delayed (slow) trip.
	if p.FastTimeS != nil && p.FuseMinMeltS != nil {
		b := *p.FastTimeS < *p.FuseMinMeltS
		p.FuseSaved = &b
	}
	if p.SlowTimeS != nil && p.FuseTotalClearS != nil {
		b := *p.FuseTotalClearS < *p.SlowTimeS
		p.FuseClears = &b
	}

	// Fuse-saving crossover (fast == min-melt), on the fuse base. ratio converts a
	// fuse-base current to the recloser-base current the fast curve is read at.
	if len(fastPts) > 0 && len(mmPts) > 0 {
		ratio := 1.0
		if rp.baseV > 0 && fp.baseV > 0 {
			ratio = fp.baseV / rp.baseV
		}
		p.MaxSaveCurrentAmps = fuseSaveCrossover(fastPts, mmPts, ratio)
	}

	if p.FuseSaved == nil && p.FuseClears == nil && p.Note == "" {
		p.Note = "recloser fast/slow curves or fuse bands unresolved -- nothing to grade"
	}
	return p
}

// firstBandPoints returns a curve's points for the first band (in preference
// order) that has any, or ok=false when none do. Used to read a recloser's
// single operate characteristic whichever band a seed tabulated it under.
func firstBandPoints(ctx context.Context, db bun.IDB, curves *curveCache, ref string, bands []string) ([]curvePoint, bool) {
	for _, b := range bands {
		if pts, err := curves.points(ctx, db, ref, b); err == nil && len(pts) > 0 {
			return pts, true
		}
	}
	return nil, false
}

// fuseSaveCrossover finds the fault current (on the fuse base) at which the
// recloser FAST curve overtakes the fuse MINIMUM-MELT curve -- the upper bound of
// the range the fuse is saved over. g(I) = t_fast(I·ratio) - t_mm(I) is negative
// while the fuse is saved and crosses zero at the crossover; the search samples
// the shared current range log-spaced for the first negative->non-negative flip,
// then bisects geometrically. Returns nil when the curves never cross over their
// shared range (the per-fault verdict already says which side the scheme is on).
func fuseSaveCrossover(fastPts, mmPts []curvePoint, ratio float64) *float64 {
	if len(fastPts) == 0 || len(mmPts) == 0 || ratio <= 0 {
		return nil
	}
	// Shared current domain, expressed on the fuse base (the fast curve's own
	// amps divided by ratio bring it onto the fuse base).
	lo := math.Max(mmPts[0].current, fastPts[0].current/ratio)
	hi := math.Min(mmPts[len(mmPts)-1].current, fastPts[len(fastPts)-1].current/ratio)
	if lo <= 0 || hi <= lo {
		return nil
	}
	g := func(i float64) float64 {
		tf, _ := interpLogLog(fastPts, i*ratio)
		tm, _ := interpLogLog(mmPts, i)
		return tf - tm
	}
	const steps = 64
	prevI, prevG := lo, g(lo)
	for k := 1; k <= steps; k++ {
		ci := lo * math.Pow(hi/lo, float64(k)/steps)
		cg := g(ci)
		if prevG < 0 && cg >= 0 {
			a, b := prevI, ci
			for j := 0; j < 40; j++ {
				m := math.Sqrt(a * b)
				if g(m) < 0 {
					a = m
				} else {
					b = m
				}
			}
			r := math.Sqrt(a * b)
			return &r
		}
		prevI, prevG = ci, cg
	}
	return nil
}

// fuseSavingLess orders the report: pairs with a failed verdict (fuse not saved,
// or fuse does not clear before lockout) first, then the rest, each group stable
// by fuse id.
func fuseSavingLess(a, b fuseSavingPair) bool {
	aIssue := (a.FuseSaved != nil && !*a.FuseSaved) || (a.FuseClears != nil && !*a.FuseClears)
	bIssue := (b.FuseSaved != nil && !*b.FuseSaved) || (b.FuseClears != nil && !*b.FuseClears)
	if aIssue != bIssue {
		return aIssue
	}
	return a.Fuse.FeatureID < b.Fuse.FeatureID
}
