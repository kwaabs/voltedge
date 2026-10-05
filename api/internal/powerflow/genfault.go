package powerflow

import "math"

// genfault.go adds embedded-generation fault infeed to the three-phase
// fault-current study (fault.go). A GENERATOR is a SECOND source of fault
// current: the base engine walks impedance from the single grid Thevenin source
// and reports I = V/|Z| at each node, but an embedded generator feeds the fault
// too, through its own internal sub-transient impedance plus the network between
// it and the fault. This file computes that per-node contribution by
// superposition over every generator in the tree and hands fault.go a
// per-node total to add onto the grid figure.
//
// SCOPE (Tier A): three-phase symmetrical fault only. Generator infeed into a
// single-line-to-ground fault depends on the machine's grounding and its
// interface transformer's zero-sequence connection -- data the model does not
// yet collect -- so the SLG figures (groundfault.go) stay grid-only, a
// documented Tier A limit.
//
// EXACTNESS: the per-source superposition (grid V/Z plus each generator's V/Z
// through its own path, summed as current magnitudes) is exact when the sources
// feed the fault through disjoint impedance -- the dominant case of an embedded
// generator downstream of, or on a sibling branch to, a fault nearer the grid
// (the switchgear-duty concern). When grid and generator share series impedance
// to the fault (a fault downstream of the generator, fed by both through the
// same conductors) it is a conservative approximation that slightly overstates
// the duty -- the safe direction to err for a rating check, and the same
// upper-bound spirit the base engine's MaterialResolved flag already embraces.

// rootImpedance is one node's accumulated series R+jX from the source, referred
// to a SINGLE common base (the run's nominal at the source) instead of rebased
// to each node's own base the way pathImpedance is. ratioFromRoot is the product
// of transformer turns ratios from the root down to this node (v_local/v_root),
// the factor that refers an impedance between this node's own base and the
// common root base (ohms scale by ratio^2). Keeping every node on one base is
// what lets the impedance BETWEEN any two nodes be read off as
// Z(a)+Z(b)-2*Z(lca) -- the standard radial two-node impedance -- which the
// multi-source superposition needs and the per-node local-base accumulation
// (rebased across every transformer) cannot give.
type rootImpedance struct {
	rOhm          float64
	xOhm          float64
	ratioFromRoot float64
}

// accumulateRootBaseImpedance is accumulateSeriesImpedance's single-base twin:
// the same forward walk summing each edge/transformer node's own branch
// impedance onto its parent's running total, but every element referred to the
// common root base rather than the accumulation being rebased down across each
// transformer. The root seeds the total with the source's own Thevenin
// impedance (already on the root base, ratioFromRoot = 1). A transformer node
// multiplies the running turns ratio and adds its own series impedance -- which
// resolveTransformerModel referred to its secondary (local) base -- brought onto
// the root base by /ratioFromRoot^2. An edge node adds its own impedance, on its
// parent's base, likewise referred by the parent's ratioFromRoot^2. A junction,
// an unresolved edge, or a transformer with no series impedance passes the
// parent's accumulation and ratio straight through (adding zero) -- the same
// pass-through accumulateSeriesImpedance makes.
func accumulateRootBaseImpedance(tr *tree, states map[node]*nodeState, start node, sourceR, sourceX float64) map[node]rootImpedance {
	m := make(map[node]rootImpedance, len(tr.order))
	for _, n := range tr.order {
		if n == start {
			m[n] = rootImpedance{rOhm: sourceR, xOhm: sourceX, ratioFromRoot: 1}
			continue
		}
		parent := m[tr.parent[n]]
		acc := parent // pass-through by default (junction / unresolved edge)

		st := states[n]
		switch {
		case st != nil && st.xfmr.ok:
			ratio := parent.ratioFromRoot * st.xfmr.ratio
			rr := ratio * ratio
			acc.rOhm = parent.rOhm + st.xfmr.rOhm/rr
			acc.xOhm = parent.xOhm + st.xfmr.xOhm/rr
			acc.ratioFromRoot = ratio
		case st != nil && st.loaded.FeatureClass.NetworkRole == "edge" && st.impedance.resolved:
			rr := parent.ratioFromRoot * parent.ratioFromRoot
			acc.rOhm = parent.rOhm + st.impedance.rOhm/rr
			acc.xOhm = parent.xOhm + st.impedance.xOhm/rr
		}
		m[n] = acc
	}
	return m
}

// nodeDepths is each node's hop distance from the root, off tr.parent. tr.order
// is BFS order (a parent always precedes its child), so one forward pass fills
// it -- the depth a symmetric LCA walk needs.
func nodeDepths(tr *tree, start node) map[node]int {
	depth := make(map[node]int, len(tr.order))
	for _, n := range tr.order {
		if n == start {
			depth[n] = 0
			continue
		}
		depth[n] = depth[tr.parent[n]] + 1
	}
	return depth
}

// lca is the lowest common ancestor of two in-tree nodes: lift the deeper one to
// the other's depth, then walk both up in lockstep until they meet. On a radial
// tree the unique path between a and b runs a -> lca -> b, so the series
// impedance between them is Z(a)+Z(b)-2*Z(lca) on a common base.
func lca(tr *tree, depth map[node]int, a, b node) node {
	for depth[a] > depth[b] {
		a = tr.parent[a]
	}
	for depth[b] > depth[a] {
		b = tr.parent[b]
	}
	for a != b {
		a = tr.parent[a]
		b = tr.parent[b]
	}
	return a
}

// generatorFaultContributions returns each node's total embedded-generation
// infeed (in the same standard per-phase amperes the base engine reports, driven
// by the line-to-neutral voltage baseV/phaseDiv) to a bolted three-phase fault at
// that node, by superposition over every GENERATOR
// in the tree with a resolvable fault spec. The result is empty (all zero) for a
// network with no embedded generation, leaving fault.go's grid figures
// untouched.
//
// For a generator g and a fault node f: the loop impedance is g's internal
// (reactive) fault impedance plus the series path g->f. That path is read off
// the common-base accumulation as Z(g)+Z(f)-2*Z(lca) and referred to g's own
// terminal base by ratioFromRoot(g)^2, so the driving EMF (g's terminal voltage)
// and the impedance are on one base. The current leaving g is V_g/|Z_loop|;
// referred to the fault's own side (a transformer between g and f scales current
// inversely to voltage) it is that times V_g/V_f. Contributions from all
// generators sum.
func generatorFaultContributions(tr *tree, states map[node]*nodeState, paths map[node]pathImpedance, start node, sourceR, sourceX, phaseDiv float64) map[node]float64 {
	// Each generator's internal fault impedance (a reactance magnitude on its
	// own terminal base), resolved now that paths gives every node its base V.
	type genSource struct {
		n    node
		zInt float64
	}
	var gens []genSource
	for _, n := range tr.order {
		st := states[n]
		if st == nil || !st.genFault.ok {
			continue
		}
		z := st.genFault.genFaultImpedanceOhm(paths[n].baseV)
		if z <= 0 {
			continue
		}
		gens = append(gens, genSource{n: n, zInt: z})
	}

	contrib := make(map[node]float64, len(tr.order))
	if len(gens) == 0 {
		return contrib
	}

	root := accumulateRootBaseImpedance(tr, states, start, sourceR, sourceX)
	depth := nodeDepths(tr, start)

	for _, f := range tr.order {
		vf := paths[f].baseV
		if vf <= 0 {
			continue
		}
		var total float64
		for _, g := range gens {
			vg := paths[g.n].baseV
			if vg <= 0 {
				continue
			}
			anc := lca(tr, depth, g.n, f)
			// Series path g->f on the common root base, then referred to g's
			// terminal base (ohms scale by ratioFromRoot(g)^2).
			k := root[g.n].ratioFromRoot * root[g.n].ratioFromRoot
			pr := (root[g.n].rOhm + root[f].rOhm - 2*root[anc].rOhm) * k
			px := (root[g.n].xOhm + root[f].xOhm - 2*root[anc].xOhm) * k
			// Loop impedance from the generator EMF to the fault: its internal
			// (reactive) impedance in series with the path.
			zLoop := math.Hypot(pr, px+g.zInt)
			if zLoop <= 0 {
				continue
			}
			// Driving voltage is line-to-neutral (vg / phaseDiv), the same basis as
			// the grid infeed, so the two combine in standard per-phase amperes. The
			// vg/vf referral ratio is basis-independent (same-basis voltages).
			iAtGen := (vg / phaseDiv) / zLoop // current leaving the generator (g's side)
			total += iAtGen * (vg / vf)       // referred to the fault's own side
		}
		contrib[f] = total
	}
	return contrib
}
