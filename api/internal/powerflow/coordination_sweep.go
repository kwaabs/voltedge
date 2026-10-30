package powerflow

// coordination_sweep.go is the NETWORK-WIDE miscoordination sweep -- POST
// /analysis/coordination-sweep. The single coordination study (coordination.go)
// grades one source->fault path; this grades EVERY series protective pair on the
// feeder below the source and reports the ones that fail.
//
// It walks the radial tree once (the start-dependent, expensive part), then
// postulates a fault AT each protective device on the tree and reuses
// runCoordination to grade the chain up to it. Each run re-grades cheaply off the
// same topology; the per-pair verdicts are merged keeping the WORST (smallest)
// margin seen across the faults a pair was graded at, so one report answers "is the
// whole feeder coordinated, and exactly which pairs are not?".

import (
	"context"
	"sort"

	"github.com/uptrace/bun"
)

// maxSweepDevices bounds a pathological feeder (each device is one more coordination
// run). A real feeder has far fewer protective devices than this; hitting it sets
// Truncated so the report is never read as exhaustive.
const maxSweepDevices = 40

type coordinationSweepRequest struct {
	Start           featureRef `json:"start"`
	NominalVoltageV float64    `json:"nominal_voltage_v"`
	CTISeconds      float64    `json:"cti_seconds"`
	VoltageBasis    string     `json:"voltage_basis"`
	VoltageFactorC  float64    `json:"voltage_factor_c,omitempty"`
}

type coordinationSweepResponse struct {
	Start               featureRef         `json:"start"`
	CTISeconds          float64            `json:"cti_seconds"`
	DevicesFaulted      int                `json:"devices_faulted"`
	PairCount           int                `json:"pair_count"`
	MiscoordinatedCount int                `json:"miscoordinated_count"`
	UngradedCount       int                `json:"ungraded_count"`
	Pairs               []coordinationPair `json:"pairs"`
	Truncated           bool               `json:"truncated"`
}

func runCoordinationSweep(ctx context.Context, db bun.IDB, req coordinationSweepRequest) (*coordinationSweepResponse, error) {
	if req.Start.FeatureClass == "" || req.Start.FeatureID == "" {
		return nil, invalid("start.feature_class and start.feature_id are required")
	}
	cti := req.CTISeconds
	if cti <= 0 {
		cti = defaultCTISeconds
	}

	start, err := resolveNode(ctx, db, req.Start)
	if err != nil {
		return nil, err
	}
	barriers, err := loadFlowBarriers(ctx, db)
	if err != nil {
		return nil, err
	}
	loader := newFeatureClassLoader()
	tr, _, err := buildTree(ctx, db, start, barriers, loader, nil, nil)
	if err != nil {
		return nil, err
	}

	classCodes, err := nodeClassCodes(ctx, db, tr.order)
	if err != nil {
		return nil, err
	}

	// Fault points = the downstream protective devices; a fault at each grades its
	// pair with the next protective device upstream.
	var faultNodes []node
	for _, n := range tr.order {
		if n == start {
			continue
		}
		if isProtectiveClass(classCodes[n.classID]) {
			faultNodes = append(faultNodes, n)
		}
	}
	truncated := false
	if len(faultNodes) > maxSweepDevices {
		faultNodes = faultNodes[:maxSweepDevices]
		truncated = true
	}
	refs := toFeatureRefs(faultNodes, classCodes)

	// Merge pairs across every fault point, keeping the worst (smallest) margin.
	byKey := map[string]coordinationPair{}
	devicesFaulted := 0
	for _, ref := range refs {
		res, rerr := runCoordination(ctx, db, coordinationRequest{
			Start:           req.Start,
			Fault:           ref,
			NominalVoltageV: req.NominalVoltageV,
			CTISeconds:      cti,
			VoltageBasis:    req.VoltageBasis,
			VoltageFactorC:  req.VoltageFactorC,
		})
		if rerr != nil {
			continue // a device that can't be graded (unreached, no source) is skipped
		}
		devicesFaulted++
		for _, p := range res.Pairs {
			key := p.Downstream.FeatureClass + ":" + p.Downstream.FeatureID + "|" + p.Upstream.FeatureClass + ":" + p.Upstream.FeatureID
			if cur, ok := byKey[key]; !ok || pairWorse(p, cur) {
				byKey[key] = p
			}
		}
	}

	pairs := make([]coordinationPair, 0, len(byKey))
	mis, ungraded := 0, 0
	for _, p := range byKey {
		pairs = append(pairs, p)
		switch {
		case p.Coordinated == nil:
			ungraded++
		case !*p.Coordinated:
			mis++
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairLess(pairs[i], pairs[j]) })

	return &coordinationSweepResponse{
		Start:               req.Start,
		CTISeconds:          cti,
		DevicesFaulted:      devicesFaulted,
		PairCount:           len(pairs),
		MiscoordinatedCount: mis,
		UngradedCount:       ungraded,
		Pairs:               pairs,
		Truncated:           truncated,
	}, nil
}

func isProtectiveClass(code string) bool {
	switch code {
	case classMVBreaker, classMVFuse, classMVSwitch, classLVSwitch, classMVRecloser:
		return true
	}
	return false
}

// pairWorse reports whether a is a worse coordination outcome than b (so it should
// replace b in the merge): a graded pair beats an ungraded one, and among graded
// pairs the smaller margin is worse.
func pairWorse(a, b coordinationPair) bool {
	if a.MarginS == nil {
		return false
	}
	if b.MarginS == nil {
		return true
	}
	return *a.MarginS < *b.MarginS
}

// pairLess orders the report: miscoordinated pairs first, then by ascending margin,
// ungraded pairs last.
func pairLess(a, b coordinationPair) bool {
	aMis := a.Coordinated != nil && !*a.Coordinated
	bMis := b.Coordinated != nil && !*b.Coordinated
	if aMis != bMis {
		return aMis
	}
	if a.MarginS == nil {
		return false
	}
	if b.MarginS == nil {
		return true
	}
	return *a.MarginS < *b.MarginS
}
