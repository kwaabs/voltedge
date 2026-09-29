package powerflow

import (
	"context"
	"math"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// sweepIterations mirrors the "2-3 iterations" this package's own doc
// comment promises -- each pass only refines which node's own voltage
// a branch's ΔV is measured against (the standard "improved" linear
// distribution load flow), since demand itself is constant-power and
// doesn't change between passes. Fixed rather than a convergence
// tolerance: at the drop magnitudes a real distribution feeder sees,
// three passes over-converges already: a tolerance loop would just be
// unobservable complexity for this pass.
const sweepIterations = 3

// nodeState is the sweep's own per-node working state, filled in
// during resolution, then the backward pass, then the forward pass.
// voltageBase is the nominal this node's voltageV is measured against
// -- the run's nominal on the source side of every transformer, a
// transformer's secondary_voltage below it -- set during the forward
// pass as the base propagates down the tree. xfmr is the node's
// two-winding behaviour (ok = false for every non-transformer node).
type nodeState struct {
	loaded      *metadata.Loaded
	attrs       map[string]any
	impedance   conductorImpedance
	xfmr        transformerModel
	reg         regulatorModel
	ownKW       float64
	ownKVAR     float64
	cumKW       float64
	cumKVAR     float64
	voltageV    float64
	voltageBase float64
	ratedKVA    float64
	hasRating   bool

	// genFault is the node's resolved embedded-generation fault-contribution
	// inputs (ok=false for every non-generator node). Read only by the
	// fault-current study (genfault.go), which turns it into a fault infeed
	// once the walk has referred a voltage base to the node; the load flow
	// ignores it.
	genFault genFaultSpec

	// three-winding transformer working state (nil/empty for every other node).
	// windingKW/windingKVAR are per-output-winding-role through-power, summed in
	// the backward pass so each arm's own drop uses only its own load. windingOut
	// is the forward pass's per-output voltage+base, which the transformer's LV-
	// vs TV-side children read instead of the node's single voltageV.
	windingKW   map[string]float64
	windingKVAR map[string]float64
	windingOut  map[string]windingVoltage
}

// windingVoltage is one output winding's resolved sending-end voltage and the
// nominal base it is measured against, handed down to the children on that
// winding.
type windingVoltage struct {
	voltageV    float64
	voltageBase float64
}

// terminal role strings (metadata.terminals.role) the three-winding sweep keys
// off: the secondary terminal is the LV output, the tertiary the TV output.
const (
	windingSecondary = "secondary"
	windingTertiary  = "tertiary"
)

// sweptTree is one fully-solved radial study: the tree, every node's
// swept electrical state, the resolved start node, class codes for
// formatting, and the barrier hits where the walk stopped. It is what
// runSweep hands back so more than one endpoint can format the same
// solve differently -- load-flow reports per-node voltage/loading, the
// voltage-profile report sums per-branch losses off the same states --
// without either re-running the (query-heavy) solve.
type sweptTree struct {
	tr          *tree
	states      map[node]*nodeState
	start       node
	classCodes  map[string]string
	barrierHits []node
	nominalV    float64
}

// runSweep is the shared engine core: resolve the start feature, walk it
// into a tree (rejecting a non-radial section outright), resolve every
// node's own electrical inputs, sweep backward to accumulate demand, and
// sweep forward to derive voltage. Both POST /analysis/load-flow and
// POST /analysis/voltage-profile run exactly this and then format its
// result their own way.
//
// closed is a set of nodes to treat as closed (never a barrier) for this
// sweep only, regardless of their live device_state -- the hook the N-1
// contingency study (POST /analysis/contingency) uses to hypothetically
// close a normally-open tie and re-solve the picked-up section without
// mutating runtime.feature_connectivity. Pass nil for a plain sweep.
//
// breaks, when non-nil, switches buildTree from "reject a mesh" to "record
// the loop-closing edges and build a radial spanning tree anyway" -- the hook
// the weakly-meshed load flow (POST /analysis/meshed-load-flow) uses to solve
// a radial base case it then closes by compensation (mesh.go). Every radial
// caller passes nil and keeps the strict radial-only behaviour.
func runSweep(ctx context.Context, db bun.IDB, startRef featureRef, nominalV float64, closed map[node]bool, breaks *[]meshBreak) (*sweptTree, error) {
	if startRef.FeatureClass == "" || startRef.FeatureID == "" {
		return nil, invalid("start.feature_class and start.feature_id are required")
	}
	if nominalV <= 0 {
		return nil, invalid("nominal_voltage_v must be a positive number")
	}

	start, err := resolveNode(ctx, db, startRef)
	if err != nil {
		return nil, err
	}

	barriers, err := loadFlowBarriers(ctx, db)
	if err != nil {
		return nil, err
	}
	loader := newFeatureClassLoader()

	tr, barrierHits, err := buildTree(ctx, db, start, barriers, loader, closed, breaks)
	if err != nil {
		return nil, err
	}

	states, err := resolveNodeStates(ctx, db, tr, loader)
	if err != nil {
		return nil, err
	}

	backwardSweep(tr, states)

	var sourceR, sourceX float64
	if startState := states[start]; startState != nil {
		sourceR, sourceX = resolveSourceImpedance(startState.attrs)
	}
	forwardSweep(tr, states, start, nominalV, sourceR, sourceX)

	allNodes := append(append([]node{}, tr.order...), barrierHits...)
	classCodes, err := nodeClassCodes(ctx, db, allNodes)
	if err != nil {
		return nil, err
	}

	return &sweptTree{
		tr: tr, states: states, start: start,
		classCodes: classCodes, barrierHits: barrierHits, nominalV: nominalV,
	}, nil
}

// runLoadFlow is POST /analysis/load-flow: run the shared sweep and
// format it into per-node voltage and per-branch loading.
func runLoadFlow(ctx context.Context, db bun.IDB, req loadFlowRequest) (*loadFlowResponse, error) {
	swept, err := runSweep(ctx, db, req.Start, req.NominalVoltageV, nil, nil)
	if err != nil {
		return nil, err
	}

	nodes, overloaded := formatResults(swept.tr, swept.states, swept.classCodes)

	return &loadFlowResponse{
		Start:           req.Start,
		NominalVoltageV: req.NominalVoltageV,
		Iterations:      sweepIterations,
		Nodes:           nodes,
		Barriers:        toFeatureRefs(swept.barrierHits, swept.classCodes),
		Overloaded:      overloaded,
	}, nil
}

// resolveNodeStates loads every visited node's live feature row once
// and resolves its own electrical inputs (branch impedance if it's a
// conductor, demand if it carries any) -- everything the backward and
// forward passes need, computed up front rather than re-queried each
// iteration.
func resolveNodeStates(ctx context.Context, db bun.IDB, tr *tree, loader *featureClassLoader) (map[node]*nodeState, error) {
	states := make(map[node]*nodeState, len(tr.order))
	for _, n := range tr.order {
		st, err := resolveNodeState(ctx, db, loader, n)
		if err != nil {
			return nil, err
		}
		if st == nil {
			// A dangling connectivity row pointing at a since-purged
			// feature -- skip it, same as trace's own checkBarrier
			// treats this as a dead end rather than a failed run.
			continue
		}
		states[n] = st
	}
	return states, nil
}

// resolveNodeState resolves one node's own electrical inputs (branch impedance
// if it's a conductor, netted demand/generation if it carries any, transformer
// model + rating if it's a rated junction, generator fault spec if it's a
// GENERATOR) -- everything the sweeps and the nodal solver (nodal.go) need,
// computed once from the live feature row. Returns (nil, nil) for a node whose
// feature row has since been purged (a dangling connectivity row), which the
// callers treat as a dead end rather than a failed run.
func resolveNodeState(ctx context.Context, db bun.IDB, loader *featureClassLoader, n node) (*nodeState, error) {
	loaded, err := loader.loadedFor(ctx, db, n.classID)
	if err != nil {
		return nil, err
	}
	row, err := crud.GetFeature(ctx, db, loaded, n.featureID)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return nil, nil
		}
		return nil, err
	}
	attrs, _ := row["attributes"].(map[string]any)

	var imp conductorImpedance
	if loaded.FeatureClass.NetworkRole == "edge" {
		imp, err = resolveConductorImpedance(ctx, db, loaded, n, attrs)
		if err != nil {
			return nil, err
		}
	}
	ownKW, ownKVAR := resolveDemand(attrs)
	// Net embedded generation in as negative demand (Tier A): a
	// GENERATOR injects (P, Q) at its node, so the node's net own
	// power is demand minus generation. A pure generator (no
	// demand_kw) ends up with a negative ownKW, which backwardSweep
	// carries up the tree as a reduction in upstream flow and
	// forwardSweep reads as a local voltage rise -- the exact
	// negative-demand model hosting.go uses, now asset-driven. A
	// node carrying both fields nets them (behind-the-meter PV under
	// a load), which is correct.
	genKW, genKVAR := resolveGeneration(attrs)
	ownKW -= genKW
	ownKVAR -= genKVAR
	// Net a shunt capacitor bank in as negative reactive demand: it
	// supplies leading VARs (P = 0), so only ownKVAR drops. A fixed bank
	// always injects; a switched bank injects only while CLOSED (see
	// resolveCapacitor). This is what makes every downstream sweep --
	// load-flow, profile, hosting, volt-VAR -- and the nodal sInjPU see
	// the compensation, with the sign owned here at the single netting
	// site rather than in the resolver.
	ownKVAR -= resolveCapacitor(attrs)
	// Net a battery-storage (BESS) unit in the same way, but SIGNED and on
	// both P and Q: resolveStorage returns a positive injection when the
	// battery is discharging (net own power drops, like a generator) and a
	// negative one when charging (net own power rises, an extra load). One
	// subtraction therefore drives both directions -- the storage-dispatch
	// study just varies storage_dispatch_kw and re-sweeps through this exact
	// site, so every sweep (load-flow, profile, dispatch) already sees it.
	stKW, stKVAR := resolveStorage(attrs)
	ownKW -= stKW
	ownKVAR -= stKVAR
	ratedKVA, hasRating := resolveTransformerRating(attrs)
	xfmr := resolveTransformerModel(attrs, ratedKVA, hasRating)
	// A series step voltage regulator (autotransformer, ratio ~ 1) resolved off
	// its own regulator_step_pct/tap fields. It keeps the same voltage base, so
	// unlike xfmr it never rebases -- the forward sweep applies its boost/buck
	// ratio to the parent output and passes the same base down.
	reg := resolveRegulator(attrs)

	// The generation subtype (generation_type slug) classifies inverter vs
	// synchronous for the fault-contribution fallback; resolved generically
	// off the row's subtype code so no class is hardcoded here (a non-
	// generator node simply has no fault fields, so the spec is inert).
	var subtypeName string
	if raw, ok := row["subtype"]; ok && raw != nil {
		if code, ok := readIntAttr(raw); ok {
			if s := loaded.SubtypeByCode(code); s != nil {
				subtypeName = s.Name
			}
		}
	}
	genFault := resolveGeneratorFaultSpec(attrs, subtypeName)

	return &nodeState{
		loaded: loaded, attrs: attrs, impedance: imp, xfmr: xfmr, reg: reg, ownKW: ownKW, ownKVAR: ownKVAR,
		ratedKVA: ratedKVA, hasRating: hasRating, genFault: genFault,
	}, nil
}

// backwardSweep accumulates each node's own demand plus everything
// downstream of it. tr.order is BFS discovery order (non-decreasing
// depth); walking it in reverse guarantees every child is processed
// before its parent, since a child is always discovered strictly after
// its parent -- the standard backward pass of Kersting's method.
func backwardSweep(tr *tree, states map[node]*nodeState) {
	for i := len(tr.order) - 1; i >= 0; i-- {
		n := tr.order[i]
		st, ok := states[n]
		if !ok {
			continue
		}
		st.cumKW, st.cumKVAR = st.ownKW, st.ownKVAR
		for _, c := range tr.children[n] {
			if cs, ok := states[c]; ok {
				st.cumKW += cs.cumKW
				st.cumKVAR += cs.cumKVAR
			}
		}

		// A three-winding transformer also needs each output winding's own
		// through-power (not just the total) so its LV and TV arms drop against
		// their own load while the shared H arm still sees the total. Partition
		// the children by the winding they hang off (tr.childWinding). A child on
		// neither output winding (e.g. a ground connection) simply isn't counted
		// toward either arm.
		if st.xfmr.threeWinding {
			st.windingKW = map[string]float64{}
			st.windingKVAR = map[string]float64{}
			for _, c := range tr.children[n] {
				cs, ok := states[c]
				if !ok {
					continue
				}
				role := tr.childWinding[c]
				st.windingKW[role] += cs.cumKW
				st.windingKVAR[role] += cs.cumKVAR
			}
		}
	}
}

// forwardSweep derives each node's own voltage and voltage base,
// sweepIterations times over (each pass re-measuring a branch's ΔV
// against the previous pass's own voltage at the sending end, refining
// nothing else -- demand is fixed by backwardSweep already). The start
// node's own voltage absorbs the source's impedance (if any) against
// the whole network's total demand, once, on the run's nominal base.
// Every other node inherits its parent's base and voltage, less its own
// branch's drop -- with two special cases: a conductor node drops
// (P·R+Q·X)/V across its resolved impedance, and a transformer node
// crossed mid-tree rebases to its secondary winding (the parent's
// primary voltage stepped by the turns ratio, less the transformer's
// own series drop on the secondary base) and hands that secondary base
// down to everything below it. A plain junction is electrically
// pass-through -- same voltage and base as its parent. tr.order is BFS
// order (parent before child), so a parent's base is always set before
// its child reads it, in the same pass.
func forwardSweep(tr *tree, states map[node]*nodeState, start node, nominalVoltageV, sourceR, sourceX float64) {
	for iter := 0; iter < sweepIterations; iter++ {
		for _, n := range tr.order {
			st := states[n]
			if st == nil {
				continue
			}
			if n == start {
				// The start is the source at the supplied nominal -- even if it
				// is itself a transformer (an LV-feeder study started at the
				// DIST_XFMR), its ratio is not applied here: the caller has
				// already supplied the sending-end voltage for this study.
				st.voltageBase = nominalVoltageV
				st.voltageV = nominalVoltageV - approxDropV(st.cumKW, st.cumKVAR, sourceR, sourceX, nominalVoltageV)
				continue
			}

			parentState := states[tr.parent[n]]
			if parentState == nil {
				st.voltageBase = nominalVoltageV
				st.voltageV = nominalVoltageV
				continue
			}

			// The sending-end voltage/base this node actually hangs off -- normally
			// the parent's single output, but the winding-specific output when the
			// parent is a three-winding transformer and this child is on its LV or
			// TV side.
			pV, pBase := parentOutput(n, parentState, tr)

			switch {
			case st.xfmr.ok && st.xfmr.threeWinding:
				// Two outputs. Each output's ideal open-circuit voltage is the
				// parent voltage times that winding's ratio; from it we subtract the
				// shared H-arm drop (carrying the whole transformer's through-power,
				// st.cumKW/KVAR, referred to that output's base) and the output arm's
				// own drop (only that winding's own through-power). The node's own
				// reported voltage is the LV (secondary) output.
				st.windingOut = map[string]windingVoltage{}

				vIdealLV := pV * st.xfmr.ratio
				lvKW, lvKVAR := st.windingKW[windingSecondary], st.windingKVAR[windingSecondary]
				vLV := vIdealLV -
					approxDropV(st.cumKW, st.cumKVAR, st.xfmr.rHtoL, st.xfmr.xHtoL, vIdealLV) -
					approxDropV(lvKW, lvKVAR, st.xfmr.rL, st.xfmr.xL, vIdealLV)
				st.windingOut[windingSecondary] = windingVoltage{voltageV: vLV, voltageBase: st.xfmr.secondaryV}

				vIdealTV := pV * st.xfmr.tertiaryRatio
				tvKW, tvKVAR := st.windingKW[windingTertiary], st.windingKVAR[windingTertiary]
				vTV := vIdealTV -
					approxDropV(st.cumKW, st.cumKVAR, st.xfmr.rHtoT, st.xfmr.xHtoT, vIdealTV) -
					approxDropV(tvKW, tvKVAR, st.xfmr.rT, st.xfmr.xT, vIdealTV)
				st.windingOut[windingTertiary] = windingVoltage{voltageV: vTV, voltageBase: st.xfmr.tertiaryV}

				st.voltageBase = st.xfmr.secondaryV
				st.voltageV = vLV
			case st.xfmr.ok:
				vSecondaryIdeal := pV * st.xfmr.ratio
				st.voltageBase = st.xfmr.secondaryV
				st.voltageV = vSecondaryIdeal - approxDropV(st.cumKW, st.cumKVAR, st.xfmr.rOhm, st.xfmr.xOhm, vSecondaryIdeal)
			case st.reg.ok:
				// Series step voltage regulator: an autotransformer with a ratio
				// near unity that boosts/bucks the parent voltage while KEEPING the
				// same voltage base (a regulator does not change voltage class). The
				// tap ratio scales the sending-end voltage; the unit is modelled as
				// ideal (a regulator's own series impedance is negligible against a
				// feeder's), so no drop is subtracted here.
				st.voltageBase = pBase
				st.voltageV = pV * st.reg.ratio
			case st.impedance.resolved:
				st.voltageBase = pBase
				st.voltageV = pV - approxDropV(st.cumKW, st.cumKVAR, st.impedance.rOhm, st.impedance.xOhm, pV)
			default:
				st.voltageBase = pBase
				st.voltageV = pV
			}
		}
	}
}

// parentOutput is the sending-end voltage and base a child inherits from its
// parent. Normally that is the parent's single output (voltageV/voltageBase),
// but a three-winding transformer presents two different outputs -- so a child
// on its LV side reads the LV output and one on its TV side reads the TV output,
// selected by the winding role recorded on the discovering edge. A child on a
// winding the parent didn't model (or any non-transformer parent) falls back to
// the parent's single output, i.e. the pre-Phase-2 behaviour.
func parentOutput(child node, parent *nodeState, tr *tree) (float64, float64) {
	if parent.windingOut != nil {
		if w, ok := parent.windingOut[tr.childWinding[child]]; ok {
			return w.voltageV, w.voltageBase
		}
	}
	return parent.voltageV, parent.voltageBase
}

// approxDropV is the standard linearized distribution voltage-drop
// approximation, ΔV(volts) ≈ (P·R + Q·X) / V -- the industry-standard
// simplification for a planning-level radial feeder calc, accurate to
// a fraction of a percent at the drop magnitudes a well-run
// distribution feeder actually sees (exact would need the branch's own
// current angle, not just magnitude -- unnecessary precision for this
// slice). kW/kVAR are converted to watts/VAR before dividing.
func approxDropV(cumKW, cumKVAR, rOhm, xOhm, refVoltageV float64) float64 {
	if refVoltageV <= 0 {
		return 0
	}
	return (cumKW*1000*rOhm + cumKVAR*1000*xOhm) / refVoltageV
}

// approxCurrentAmps derives a branch's own current from its cumulative
// apparent power and the voltage at its sending end: I = S / V, where
// S = sqrt(P² + Q²) in VA.
func approxCurrentAmps(cumKW, cumKVAR, voltageV float64) float64 {
	if voltageV <= 0 {
		return 0
	}
	sVA := math.Hypot(cumKW*1000, cumKVAR*1000)
	return sVA / voltageV
}

// formatResults turns the swept nodeStates into the response shape,
// flagging (into Overloaded) any conductor node whose derived current
// exceeds its own resolved ampacity -- a client shouldn't have to
// re-scan Nodes itself to find what actually needs attention.
func formatResults(tr *tree, states map[node]*nodeState, classCodes map[string]string) ([]nodeResult, []featureRef) {
	nodes := make([]nodeResult, 0, len(tr.order))
	overloaded := make([]featureRef, 0)

	// A node is in the response iff it has a resolved state; a parent pointer is
	// only stamped when the parent is itself in the response, so a client can
	// always resolve it (mirrors trace's inResponse guard).
	inResponse := make(map[node]bool, len(tr.order))
	for _, n := range tr.order {
		if states[n] != nil {
			inResponse[n] = true
		}
	}

	for _, n := range tr.order {
		st := states[n]
		if st == nil {
			continue
		}
		// VoltageDropPct is measured against this node's own base, not the
		// run's single nominal -- so an LV node below a transformer reports
		// its drop from the LV secondary, not a spurious ~95% drop from the
		// MV nominal. base is zero only if a node was never reached by the
		// forward pass (parentless, non-start), in which case the drop is
		// reported as zero rather than dividing by zero.
		var dropPct float64
		if st.voltageBase > 0 {
			dropPct = (st.voltageBase - st.voltageV) / st.voltageBase * 100
		}
		circuitID, circuitRef := circuitIdentity(st)
		res := nodeResult{
			FeatureClass:     classCodes[n.classID],
			FeatureID:        n.featureID,
			CircuitID:        circuitID,
			CircuitRef:       circuitRef,
			VoltageV:         st.voltageV,
			VoltageDropPct:   dropPct,
			NominalBaseV:     st.voltageBase,
			CumulativeKW:     st.cumKW,
			CumulativeKVAR:   st.cumKVAR,
			MaterialResolved: st.impedance.resolved,
		}
		if p, ok := tr.parent[n]; ok && inResponse[p] {
			pc := classCodes[p.classID]
			pid := p.featureID
			res.ParentFeatureClass = &pc
			res.ParentFeatureID = &pid
		}

		if st.loaded.FeatureClass.NetworkRole == "edge" {
			currentAmps := approxCurrentAmps(st.cumKW, st.cumKVAR, st.voltageV)
			res.CurrentAmps = &currentAmps
			if st.impedance.ampacityAmps != nil {
				ampacity := *st.impedance.ampacityAmps
				res.AmpacityAmps = &ampacity
				loadingPct := currentAmps / ampacity * 100
				res.LoadingPct = &loadingPct
				if loadingPct > 100 {
					overloaded = append(overloaded, featureRef{FeatureClass: res.FeatureClass, FeatureID: res.FeatureID})
				}
			}
		} else if st.hasRating {
			sKVA := math.Hypot(st.cumKW, st.cumKVAR)
			ratedKVA := st.ratedKVA
			res.RatedKVA = &ratedKVA
			loadingPct := sKVA / ratedKVA * 100
			res.LoadingPct = &loadingPct
			if loadingPct > 100 {
				overloaded = append(overloaded, featureRef{FeatureClass: res.FeatureClass, FeatureID: res.FeatureID})
			}
		}
		nodes = append(nodes, res)
	}
	return nodes, overloaded
}
