package powerflow

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/trace"
)

// RunLoadAllocationJSON is the agent/MCP adapter over the load-allocation study --
// a thin exported wrapper that unmarshals the tool args and runs it, gated by the
// caller on analysis:run (the same permission the REST /analysis mount requires).
func RunLoadAllocationJSON(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var req loadAllocationRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return runLoadAllocation(ctx, db, req)
}

// allocation.go is the ADMS LOAD-ALLOCATION study (POST /analysis/load-allocation).
// The rest of the engine draws per-node demand from the model's as-designed
// `demand_kw` attribute (resolve.go) -- a planner's estimate that rarely sums to the
// load a feeder is actually carrying. Load allocation closes that gap: given a
// MEASURED total at a feeder head (from SCADA, or an operator's reading), it
// distributes that load across the downstream consumer leaves in proportion to a
// weight basis, so the per-transformer loads reflect reality.
//
// It is READ-ONLY and standalone -- it reuses trace.DownstreamLoadLeaves to enumerate
// the leaves (and their as-designed kW, apparent kVA and customer counts) and does not
// touch the solve. Feeding the allocated loads INTO the load-flow (and the reliability
// ENS) is a separate, later step; this produces the allocation table + the model-vs-
// measured calibration factor on its own.
//
// Weight basis (default `kva`): each leaf's share is its weight / Σ weights.
//   - `kva`        — the leaf's as-designed apparent power (demand_kva); the classic
//                    "allocate by connected transformer capacity". Falls back to
//                    `customers` when the feeder carries no kVA data (Σ kva = 0).
//   - `customers`  — customers_served per leaf (robust when kVA/demand is sparse).
//   - `as_designed`— the leaf's demand_kw; preserves the as-designed shape and only
//                    corrects the magnitude (a leaf with no demand_kw gets nothing).

// loadAllocationRequest asks to distribute a measured feeder-head load across the
// consumer leaves downstream of Start.
type loadAllocationRequest struct {
	Start featureRef `json:"start"`
	// MeasuredKW is the measured real power at the feeder head (kW). MeasuredKVAR, if
	// given, is used directly; else PowerFactor derives it (default unity -> 0 kvar).
	MeasuredKW   float64  `json:"measured_kw"`
	MeasuredKVAR *float64 `json:"measured_kvar"`
	PowerFactor  *float64 `json:"power_factor"`
	// Basis selects the weight: "kva" (default), "customers", or "as_designed".
	Basis string `json:"basis"`
}

// leafAllocation is one consumer leaf's allocated load.
type leafAllocation struct {
	FeatureClass  string  `json:"feature_class"`
	FeatureID     string  `json:"feature_id"`
	Label         string  `json:"label,omitempty"`
	Customers     int     `json:"customers"`
	Weight        float64 `json:"weight"`
	AsDesignedKW  float64 `json:"as_designed_kw"`
	AllocatedKW   float64 `json:"allocated_kw"`
	AllocatedKVAR float64 `json:"allocated_kvar"`
}

// loadAllocationResult is the allocation table plus the model-vs-measured calibration.
type loadAllocationResult struct {
	Start         featureRef `json:"start"`
	Basis         string     `json:"basis"`
	BasisFallback string     `json:"basis_fallback,omitempty"`

	MeasuredKW   float64 `json:"measured_kw"`
	MeasuredKVAR float64 `json:"measured_kvar"`

	// AsDesignedKW is Σ leaf demand_kw; ScaleFactor = measured_kw / as_designed_kw --
	// the diversity/calibration factor ("the model is at 1/ScaleFactor of measured").
	AsDesignedKW float64 `json:"as_designed_kw"`
	ScaleFactor  float64 `json:"scale_factor"`

	LeafCount      int `json:"leaf_count"`
	CustomersTotal int `json:"customers_total"`

	Allocations []leafAllocation `json:"allocations"`
	Note        string           `json:"note,omitempty"`
}

const (
	allocBasisKVA        = "kva"
	allocBasisCustomers  = "customers"
	allocBasisAsDesigned = "as_designed"
)

// allocationInput is a load-flow request's optional load-allocation override: a
// MEASURED feeder-head load to spread over the downstream leaves (by the same basis the
// standalone /analysis/load-allocation study uses) so the sweep runs against the real
// load instead of the model's as-designed demand_kw.
type allocationInput struct {
	MeasuredKW   float64  `json:"measured_kw"`
	MeasuredKVAR *float64 `json:"measured_kvar"`
	PowerFactor  *float64 `json:"power_factor"`
	Basis        string   `json:"basis"`
}

// allocationSummary is echoed in the load-flow response when an allocation was applied:
// what was spread, by which basis, the model-vs-measured scale factor, and how many of
// the allocated leaves actually matched a node in the swept tree (AppliedNodes) vs.
// didn't (UnmatchedLeaves) -- an honest coverage figure, since the allocation's leaf
// walk and the sweep's tree can differ at a barrier.
type allocationSummary struct {
	MeasuredKW      float64 `json:"measured_kw"`
	MeasuredKVAR    float64 `json:"measured_kvar"`
	Basis           string  `json:"basis"`
	BasisFallback   string  `json:"basis_fallback,omitempty"`
	ScaleFactor     float64 `json:"scale_factor"`
	LeafCount       int     `json:"leaf_count"`
	AppliedNodes    int     `json:"applied_nodes"`
	UnmatchedLeaves int     `json:"unmatched_leaves"`
	Note            string  `json:"note,omitempty"`
}

// demandOverride is one node's (P,Q) to force in place of its as-designed demand.
type demandOverride struct {
	kw   float64
	kvar float64
}

// buildAllocationOverrides runs the load-allocation study for start and returns the
// per-leaf (P,Q) overrides keyed by FEATURE ID (a uuid, so a sweep node matches on id
// alone, no class lookup) plus the full result for the response summary.
func buildAllocationOverrides(ctx context.Context, db bun.IDB, start featureRef, in allocationInput) (map[string]demandOverride, *loadAllocationResult, error) {
	res, err := runLoadAllocation(ctx, db, loadAllocationRequest{
		Start:        start,
		MeasuredKW:   in.MeasuredKW,
		MeasuredKVAR: in.MeasuredKVAR,
		PowerFactor:  in.PowerFactor,
		Basis:        in.Basis,
	})
	if err != nil {
		return nil, nil, err
	}
	overrides := make(map[string]demandOverride, len(res.Allocations))
	for _, a := range res.Allocations {
		overrides[a.FeatureID] = demandOverride{kw: a.AllocatedKW, kvar: a.AllocatedKVAR}
	}
	return overrides, res, nil
}

// applyDemandOverrides forces each overridden node's own (P,Q) in the resolved state
// map, in place of its as-designed demand -- the load-allocation seam in the sweep.
// A consumer leaf carries no generation, so overwriting its ownKW/ownKVAR is correct
// (generation/storage nodes are never consumer leaves, so they are never overridden).
func applyDemandOverrides(states map[node]*nodeState, overrides map[string]demandOverride) {
	for n, st := range states {
		if o, ok := overrides[n.featureID]; ok {
			st.ownKW, st.ownKVAR = o.kw, o.kvar
		}
	}
}

// runLoadAllocation computes the per-leaf allocation for one feeder head.
func runLoadAllocation(ctx context.Context, db bun.IDB, req loadAllocationRequest) (*loadAllocationResult, error) {
	if req.Start.FeatureClass == "" || req.Start.FeatureID == "" {
		return nil, invalid("start (feature_class + feature_id) is required")
	}
	if req.MeasuredKW < 0 {
		return nil, invalid("measured_kw must not be negative, got %v", req.MeasuredKW)
	}
	basis := req.Basis
	if basis == "" {
		basis = allocBasisKVA
	}
	if basis != allocBasisKVA && basis != allocBasisCustomers && basis != allocBasisAsDesigned {
		return nil, invalid("basis must be one of %q, %q or %q", allocBasisKVA, allocBasisCustomers, allocBasisAsDesigned)
	}

	// Measured reactive power: explicit kVAR wins; else derive from the power factor
	// (default unity -> 0 kvar), the same pf convention resolve.go uses.
	measuredKVAR := 0.0
	switch {
	case req.MeasuredKVAR != nil:
		measuredKVAR = *req.MeasuredKVAR
	case req.PowerFactor != nil:
		pf := *req.PowerFactor
		if pf <= 0 || pf > 1 {
			return nil, invalid("power_factor must be in (0, 1], got %v", pf)
		}
		measuredKVAR = req.MeasuredKW * math.Tan(math.Acos(pf))
	}

	leaves, _, err := trace.DownstreamLoadLeaves(ctx, db, trace.FeatureRef{
		FeatureClass: req.Start.FeatureClass,
		FeatureID:    req.Start.FeatureID,
	})
	if err != nil {
		return nil, err
	}
	if len(leaves) == 0 {
		return nil, invalid("no consumer load points are downstream of %s %s -- nothing to allocate to", req.Start.FeatureClass, req.Start.FeatureID)
	}

	weightOf := func(sp trace.ServicePoint, b string) float64 {
		switch b {
		case allocBasisKVA:
			return sp.DemandKVA
		case allocBasisCustomers:
			return float64(sp.Customers)
		default: // as_designed
			return sp.DemandKW
		}
	}

	// Sum weights for the requested basis; fall back from kva -> customers when the
	// feeder carries no kVA data at all.
	fallback := ""
	effBasis := basis
	var sumW float64
	for _, sp := range leaves {
		sumW += weightOf(sp, effBasis)
	}
	if sumW == 0 && basis == allocBasisKVA {
		effBasis, fallback = allocBasisCustomers, allocBasisCustomers
		sumW = 0
		for _, sp := range leaves {
			sumW += weightOf(sp, effBasis)
		}
	}
	if sumW == 0 {
		return nil, invalid("cannot allocate: every downstream leaf has zero weight for basis %q (no kVA, customers or demand data) -- populate the leaf attributes first", basis)
	}

	out := &loadAllocationResult{
		Start:         req.Start,
		Basis:         basis,
		BasisFallback: fallback,
		MeasuredKW:    req.MeasuredKW,
		MeasuredKVAR:  measuredKVAR,
		Allocations:   make([]leafAllocation, 0, len(leaves)),
	}
	for _, sp := range leaves {
		w := weightOf(sp, effBasis)
		share := w / sumW
		out.Allocations = append(out.Allocations, leafAllocation{
			FeatureClass:  sp.FeatureClass,
			FeatureID:     sp.FeatureID,
			Label:         sp.Label,
			Customers:     sp.Customers,
			Weight:        w,
			AsDesignedKW:  sp.DemandKW,
			AllocatedKW:   req.MeasuredKW * share,
			AllocatedKVAR: measuredKVAR * share,
		})
		out.AsDesignedKW += sp.DemandKW
		out.CustomersTotal += sp.Customers
	}
	out.LeafCount = len(leaves)

	// Heaviest allocations first -- the operator reads the biggest loads at the top.
	sort.SliceStable(out.Allocations, func(i, j int) bool {
		return out.Allocations[i].AllocatedKW > out.Allocations[j].AllocatedKW
	})

	if out.AsDesignedKW > 0 {
		out.ScaleFactor = req.MeasuredKW / out.AsDesignedKW
	} else {
		out.Note = "the model carries no as-designed demand_kw for these leaves, so the scale factor is undefined; the measured load was still allocated by " + effBasis
	}
	return out, nil
}
