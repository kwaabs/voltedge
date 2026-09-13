package powerflow

// dispatch.go is Phase 52 Tier 2, Slice S4: the load-flow DISPATCH ROUTER.
//
// Three distinct load-flow engines now live in this package, each with its own
// endpoint and its own valid domain:
//
//   - runLoadFlow (POST /analysis/load-flow) -- the radial forward-backward
//     ladder (Kersting). Fast, exact for a tree, but buildTree REJECTS a mesh
//     outright (it passes breaks = nil; see graph.go): a single loop is a hard
//     error.
//   - runMeshLoadFlow (POST /analysis/meshed-load-flow) -- Tier 1 weak-mesh
//     compensation (mesh.go): solves a radial spanning tree, then closes each
//     recorded loop by injecting circulating power. Right for a handful of
//     independent loops (a ring main, a closed link box); degrades to the plain
//     ladder on a radial network.
//   - runNodalLoadFlow (POST /analysis/nodal-load-flow) -- Tier 2 nodal Y-bus
//     (nodal.go), solved by Gauss-Seidel over the general graph. Solves an
//     arbitrarily dense mesh directly, and is the ONLY engine that supports
//     multiple slack sources and grid-forming (PV) buses (S3).
//
// Until S4 the CALLER had to know the network's topology to pick the right
// endpoint -- ask the radial ladder to solve a ring and it fails; hand a simple
// feeder to the Y-bus solver and you pay Gauss-Seidel for nothing. This router
// turns buildTree's own radial/non-radial verdict -- the exact classifier the
// three engines already disagree over -- into an automatic engine selection, so
// one entry (POST /analysis/load-flow-auto) solves any of the three network
// shapes and REPORTS which engine actually ran.
//
// Classification is topology-first and reuses the existing machinery verbatim:
// buildTree with a breaks accumulator counts the independent loop-closing edges
// (0 = radial tree; a small number = weak mesh; many = dense mesh), exactly the
// signal mesh.go was built around. Selection:
//
//   - an explicit engine override (request "engine") always wins;
//   - else any requested extra sources[] or regulated[] PV buses => nodal, the
//     only engine that models them;
//   - else 0 loops        => radial ladder (fastest, exact for a tree);
//   - else 1..maxWeakLoops => Tier 1 mesh compensation;
//   - else (> maxWeakLoops) => Tier 2 nodal Y-bus (dense mesh).
//
// The router then calls the chosen run* function UNCHANGED and wraps its native
// response in an envelope that names the engine and the reason. It deliberately
// re-walks the graph inside the chosen engine rather than threading the
// classification tree through: classification is one cheap barrier-aware walk,
// the run* entry points already own their full setup, and keeping them untouched
// keeps the three engines' numerics byte-for-byte identical to their own
// endpoints. (A later slice could thread the pre-built tree in to skip the
// double walk.)

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// routerMaxWeakLoops is the number of independent loop-closing edges at or below
// which the router still trusts Tier 1 weak-mesh compensation (mesh.go) rather
// than the Tier 2 nodal Y-bus solver. Compensation superposes one circulating-
// current correction per loop and reconverges; it is accurate and cheap for a
// few genuinely weak loops (a ring main, a closed link box) but is not meant to
// stack many strongly-interacting loops -- past this count the network is
// "dense" and the Gauss-Seidel Y-bus solver, which handles arbitrary mesh
// directly, is the honest choice. A deliberately conservative threshold: raise
// it only with evidence the compensation stays accurate.
const routerMaxWeakLoops = 3

// Engine identifiers reported in dispatchLoadFlowResponse.Engine and accepted as
// the request's engine override.
const (
	engineRadial = "radial"
	engineMeshed = "meshed"
	engineNodal  = "nodal"
	engineAuto   = "auto"
)

// dispatchLoadFlowRequest is POST /analysis/load-flow-auto's body: the union of
// what the three engines need, so one request shape reaches any of them. Start
// and NominalVoltageV are required exactly as every engine requires them.
//
// Engine, when set to "radial" | "meshed" | "nodal", forces that engine and
// skips classification (a caller who already knows the topology, or wants to
// compare engines on the same network); "" or "auto" auto-selects. Sources and
// Regulated are the nodal engine's multi-slack / PV inputs (S3) -- supplying
// either forces the nodal engine under auto-selection, since it is the only
// engine that models them, and both are ignored by the radial and meshed
// engines. MaxIterations is the meshed engine's compensation-pass cap (ignored
// by the others).
type dispatchLoadFlowRequest struct {
	Start           featureRef     `json:"start"`
	NominalVoltageV float64        `json:"nominal_voltage_v"`
	Engine          string         `json:"engine,omitempty"`
	Sources         []featureRef   `json:"sources,omitempty"`
	Regulated       []regulatedGen `json:"regulated,omitempty"`
	MaxIterations   *int           `json:"max_iterations,omitempty"`
}

// dispatchLoadFlowResponse wraps whichever engine ran. Engine names it
// ("radial" | "meshed" | "nodal"); EngineReason is a one-line human-readable
// justification (why this engine, given the topology and request); LoopCount is
// the independent loop count the classifier found (0 for a tree; omitted-as-zero
// when an explicit override skipped classification). Exactly one of Radial /
// Meshed / Nodal is populated -- the chosen engine's own native response,
// unchanged from its dedicated endpoint -- so a client renders it with the same
// view it already uses for that endpoint and only has to read Engine to know
// which.
type dispatchLoadFlowResponse struct {
	Engine       string `json:"engine"`
	EngineReason string `json:"engine_reason"`
	LoopCount    int    `json:"loop_count"`

	Radial *loadFlowResponse       `json:"radial,omitempty"`
	Meshed *meshedLoadFlowResponse `json:"meshed,omitempty"`
	Nodal  *nodalLoadFlowResponse  `json:"nodal,omitempty"`
}

// classifyTopology runs the one barrier-aware downstream walk the three engines
// disagree over -- buildTree with a breaks accumulator -- and returns the number
// of independent loop-closing edges: 0 for a radial tree, N for an N-loop mesh.
// This is the same walk buildTree does for every engine; here it is used purely
// to count loops, discarding the tree. It honours the live DOWNSTREAM_TRACE
// barriers exactly as an actual solve would, so a loop that only closes through
// an open device is still counted (mesh.go then reports it as an open,
// uncompensated loop -- the honest outcome, and one the radial ladder could not
// produce because it would reject the loop outright).
func classifyTopology(ctx context.Context, db bun.IDB, startRef featureRef, nominalV float64) (int, error) {
	if startRef.FeatureClass == "" || startRef.FeatureID == "" {
		return 0, invalid("start.feature_class and start.feature_id are required")
	}
	if nominalV <= 0 {
		return 0, invalid("nominal_voltage_v must be a positive number")
	}

	start, err := resolveNode(ctx, db, startRef)
	if err != nil {
		return 0, err
	}
	barriers, err := loadFlowBarriers(ctx, db)
	if err != nil {
		return 0, err
	}
	loader := newFeatureClassLoader()

	var breaks []meshBreak
	if _, _, err := buildTree(ctx, db, start, barriers, loader, nil, &breaks); err != nil {
		return 0, err
	}
	return len(breaks), nil
}

// runDispatchLoadFlow is POST /analysis/load-flow-auto: classify the network
// once, pick the engine, run it unchanged, and wrap the result with the engine
// that ran and why.
func runDispatchLoadFlow(ctx context.Context, db bun.IDB, req dispatchLoadFlowRequest) (*dispatchLoadFlowResponse, error) {
	// An explicit override skips classification entirely -- the caller has taken
	// responsibility for the choice (and may be comparing engines on one network).
	switch req.Engine {
	case engineRadial:
		return dispatchRun(ctx, db, req, engineRadial, "engine forced to radial ladder by request", 0)
	case engineMeshed:
		return dispatchRun(ctx, db, req, engineMeshed, "engine forced to Tier 1 mesh compensation by request", 0)
	case engineNodal:
		return dispatchRun(ctx, db, req, engineNodal, "engine forced to Tier 2 nodal Y-bus by request", 0)
	case "", engineAuto:
		// fall through to auto-selection
	default:
		return nil, invalid("engine must be one of \"\"/\"auto\", %q, %q, %q", engineRadial, engineMeshed, engineNodal)
	}

	// Multi-slack or PV is a nodal-only capability: if the caller asked for extra
	// sources or regulated generators, only the nodal engine can honour them, so
	// route there regardless of loop count (a two-source radial network is still
	// beyond the single-slack ladder and the single-slack compensation).
	if len(req.Sources) > 0 || len(req.Regulated) > 0 {
		reason := fmt.Sprintf("request carries %d extra source(s) and %d regulated (PV) bus(es) -- only the nodal Y-bus engine models multi-slack / PV", len(req.Sources), len(req.Regulated))
		return dispatchRun(ctx, db, req, engineNodal, reason, 0)
	}

	loops, err := classifyTopology(ctx, db, req.Start, req.NominalVoltageV)
	if err != nil {
		return nil, err
	}

	switch {
	case loops == 0:
		return dispatchRun(ctx, db, req, engineRadial, "network is radial (no loops) -- fast forward-backward ladder", loops)
	case loops <= routerMaxWeakLoops:
		reason := fmt.Sprintf("network is weakly meshed (%d loop(s) <= %d) -- Tier 1 mesh compensation", loops, routerMaxWeakLoops)
		return dispatchRun(ctx, db, req, engineMeshed, reason, loops)
	default:
		reason := fmt.Sprintf("network is densely meshed (%d loops > %d) -- Tier 2 nodal Y-bus (Gauss-Seidel)", loops, routerMaxWeakLoops)
		return dispatchRun(ctx, db, req, engineNodal, reason, loops)
	}
}

// dispatchRun invokes one engine (unchanged) and wraps its native response.
func dispatchRun(ctx context.Context, db bun.IDB, req dispatchLoadFlowRequest, engine, reason string, loops int) (*dispatchLoadFlowResponse, error) {
	out := &dispatchLoadFlowResponse{Engine: engine, EngineReason: reason, LoopCount: loops}
	switch engine {
	case engineRadial:
		res, err := runLoadFlow(ctx, db, loadFlowRequest{
			Start:           req.Start,
			NominalVoltageV: req.NominalVoltageV,
		})
		if err != nil {
			return nil, err
		}
		out.Radial = res
	case engineMeshed:
		res, err := runMeshLoadFlow(ctx, db, meshedLoadFlowRequest{
			Start:           req.Start,
			NominalVoltageV: req.NominalVoltageV,
			MaxIterations:   req.MaxIterations,
		})
		if err != nil {
			return nil, err
		}
		out.Meshed = res
	case engineNodal:
		res, err := runNodalLoadFlow(ctx, db, nodalLoadFlowRequest{
			Start:           req.Start,
			NominalVoltageV: req.NominalVoltageV,
			Sources:         req.Sources,
			Regulated:       req.Regulated,
		})
		if err != nil {
			return nil, err
		}
		out.Nodal = res
	default:
		return nil, invalid("unknown engine %q", engine)
	}
	return out, nil
}
