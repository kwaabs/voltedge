package powerflow

// Single-line-to-ground (SLG) fault support layered onto the symmetrical fault
// engine in fault.go. A balanced three-phase fault sees only the positive-
// sequence network (Z1), which accumulateSeriesImpedance already builds. An SLG
// fault current is I = 3·E / (Z1 + Z2 + Z0 + 3·Zf), where Z2 (negative sequence)
// is taken equal to Z1 for the static plant a distribution network is made of,
// and Z0 (zero sequence) is a genuinely different network: zero-sequence current
// is in-phase in all three phases and returns through earth, so it only flows
// where a winding provides a ground path, and a transformer's winding
// connections decide whether it passes through, is blocked, or is grounded
// locally. accumulateZeroSeqImpedance builds that Z0 network on the same radial
// tree; fault.go assembles the two into the SLG current.

// zeroSeqLineFactor is the default ratio of a line's zero-sequence impedance to
// its positive-sequence impedance, applied when no measured r0/x0 is available
// (a later slice adds real per-spec zero-sequence data). ~3× is the standard
// planning value for an overhead line's earth-return path (the ground return
// adds resistance and inductance a phase-only loop doesn't see); a conservative
// default a real figure only refines.
const zeroSeqLineFactor = 3.0

// z0OhmField / neutralResistanceField are the earthing-transformer fields
// (EARTHING_XFMR, seed/earthing_transformer_example.sql) the ground-fault calc
// reads. z0_ohm is the transformer's own zero-sequence impedance per phase (a
// shunt path to earth at its connection point); neutral_resistance_ohm is an
// integral neutral earthing resistor, which zero-sequence current sees at 3× its
// physical value (all three phases' zero-sequence current returns through the
// one neutral). Identified by the field name, not the class, the same
// generic-field-name approach the rest of this package uses.
const (
	z0OhmField             = "z0_ohm"
	neutralResistanceField = "neutral_resistance_ohm"
)

// earthShunt is one bus's earthing-transformer shunt to ground: the equivalent
// zero-sequence R+jX from that bus to earth through the earthing transformer(s)
// connected there (parallel-combined when a bus carries more than one).
type earthShunt struct {
	r0, x0 float64
}

// resolveEarthingShunt reads an earthing transformer's shunt-to-earth zero-
// sequence impedance from its own fields. z0_ohm (the transformer's own Z0) is
// taken as reactance -- an earthing transformer's impedance is predominantly
// reactive, and z0_ohm is a single magnitude field; the resistive part is the
// neutral earthing resistor at 3×, added when present. ok = false for a node
// with no positive z0_ohm (i.e. not an earthing transformer, or one whose Z0
// wasn't surveyed -- which then provides no modellable ground path).
func resolveEarthingShunt(attrs map[string]any) (r0, x0 float64, ok bool) {
	z0, okZ := readFloatAttr(attrs, z0OhmField)
	if !okZ || z0 <= 0 {
		return 0, 0, false
	}
	x0 = z0
	if rn, okR := readFloatAttr(attrs, neutralResistanceField); okR && rn > 0 {
		r0 = 3 * rn
	}
	return r0, x0, true
}

// parallelZ combines two complex impedances in parallel, Z1·Z2/(Z1+Z2) -- how
// two independent zero-sequence ground paths from the same bus (an upstream
// transformer-neutral path and a local earthing-transformer shunt) present a
// single equivalent to a fault below them. A zero sum (both paths zero, an
// impossible-but-guarded degenerate) returns zero rather than dividing.
func parallelZ(r1, x1, r2, x2 float64) (float64, float64) {
	z1 := complex(r1, x1)
	z2 := complex(r2, x2)
	sum := z1 + z2
	if sum == 0 {
		return 0, 0
	}
	zp := z1 * z2 / sum
	return real(zp), imag(zp)
}

// earthingShunts pre-scans the tree for earthing transformers and returns, per
// bus, the equivalent earthing shunt connected there. An earthing transformer is
// a leaf that grounds the bus it taps -- its PARENT in the radial tree -- so the
// shunt is recorded against tr.parent[E], grounding that bus and everything in
// its downstream cone (correct for the standard placement: an earthing
// transformer on the substation busbar that feeds the delta feeders). A bus with
// two earthing transformers gets their parallel combination. Limitation: because
// the ground propagates only downstream in this radial pass, a fault on a
// sibling branch that taps the SAME bus through a separate stub, or upstream of
// the bus within the same ungrounded island, is not seen as grounded -- connect
// an earthing transformer directly to the bus whose feeders it protects.
func earthingShunts(tr *tree, states map[node]*nodeState) map[node]earthShunt {
	out := map[node]earthShunt{}
	for _, n := range tr.order {
		st := states[n]
		if st == nil {
			continue
		}
		r0, x0, ok := resolveEarthingShunt(st.attrs)
		if !ok {
			continue
		}
		bus, hasParent := tr.parent[n]
		if !hasParent {
			bus = n // an earthing transformer at the study's start grounds itself
		}
		if existing, seen := out[bus]; seen {
			pr, px := parallelZ(existing.r0, existing.x0, r0, x0)
			out[bus] = earthShunt{pr, px}
		} else {
			out[bus] = earthShunt{r0, x0}
		}
	}
	return out
}

// zeroSeqPath is the accumulated zero-sequence R+jX from the nearest upstream
// zero-sequence ground source down to one node, referred to that node's own
// voltage base, plus whether a ground source was found at all (grounded) and
// whether every element on the path resolved. grounded = false means no
// zero-sequence return path reaches this node -- an SLG fault here draws
// essentially no current (an ungrounded section, until an earthing transformer
// provides a path -- a later slice). baseV matches the positive-sequence
// pathImpedance.baseV for the same node, so the two sequence impedances sum
// directly.
type zeroSeqPath struct {
	r0Ohm    float64
	x0Ohm    float64
	baseV    float64
	grounded bool
	resolved bool

	// estimated is true when any element on this path fell back to a default
	// rather than measured zero-sequence data (ground-fault Slice 3): a line
	// defaulting to 3× its positive-sequence impedance, a transformer taking Z0 =
	// Z1, or the source taking Z0 = its positive-sequence impedance. resolved
	// (does every element have *some* impedance?) and estimated (is any of it a
	// zero-sequence guess?) are independent: a fully-resolved path can still be
	// estimated. An earthing-transformer shunt is measured data (z0_ohm), so it
	// never sets estimated -- and when it establishes the ground on an otherwise
	// ungrounded bus, the path becomes non-estimated from that bus down.
	estimated bool
}

// accumulateZeroSeqImpedance walks tr.order forward (root first, like
// accumulateSeriesImpedance) building each node's zero-sequence path back to the
// nearest ground source. The start node is modelled as a solidly grounded
// zero-sequence source on the run's nominal base -- correct for a fault upstream
// of every transformer, on a grounded utility feed. Its Z0 is the caller-resolved
// sourceR0/sourceX0 (a real source_r0_ohm/source_x0_ohm when set, else the
// positive-sequence source impedance); sourceEstimated flags which, seeding the
// path's estimated bit. A conductor extends the path (if grounded) by its own
// zero-sequence impedance: the spec's measured r0/x0 when present, else its
// positive-sequence R/X scaled by zeroSeqLineFactor (which marks the path
// estimated). A transformer connects the upstream (HV) zero-sequence network to
// its LV side by its winding connections:
//
//   - YNyn (grounded wye both sides): zero-sequence passes through -- refer the
//     upstream Z0 across the ratio² and add the transformer's own Z0.
//   - delta/ungrounded-wye HV with a grounded-wye LV (Dyn / YNd-type): the LV
//     neutral grounds this side and the HV winding BLOCKS the upstream network,
//     so Z0 restarts at the transformer's own Z0, independent of anything above.
//   - LV winding delta or ungrounded wye: no zero-sequence ground path onto this
//     side through this transformer (grounded = false below it).
//
// The transformer's own zero-sequence impedance is its nameplate z0_pct when
// surveyed (xfmr.r0Ohm/x0Ohm), else taken equal to its positive-sequence series
// impedance (xfmr.rOhm/xOhm, Z0 = Z1, which marks the path estimated).
//
// earthing (from earthingShunts) supplies, per bus, an earthing-transformer shunt
// to ground: at a bus that already has an upstream ground path the shunt parallels
// it (a lower combined Z0, a stiffer earth); at an otherwise ungrounded bus the
// shunt IS the ground source, turning grounded false→true -- which is the whole
// point of an earthing transformer on a delta section. Because the shunt is
// applied as each node is finalised, it flows into every downstream node's path
// through the normal parent-inheritance.
func accumulateZeroSeqImpedance(tr *tree, states map[node]*nodeState, start node, nominalVoltageV, sourceR0, sourceX0 float64, sourceEstimated bool, earthing map[node]earthShunt) map[node]zeroSeqPath {
	z0 := make(map[node]zeroSeqPath, len(tr.order))
	for _, n := range tr.order {
		var acc zeroSeqPath
		if n == start {
			acc = zeroSeqPath{r0Ohm: sourceR0, x0Ohm: sourceX0, baseV: nominalVoltageV, grounded: true, resolved: true, estimated: sourceEstimated}
		} else {
			parent := z0[tr.parent[n]]
			acc = parent

			st := states[n]
			switch {
			case st != nil && st.xfmr.ok:
				acc.baseV = st.xfmr.secondaryV
				// Real z0_pct when surveyed, else Z0 ≈ Z1 (estimated) -- both already
				// referred to the secondary.
				z0r, z0x := st.xfmr.r0Ohm, st.xfmr.x0Ohm
				z0Estimated := false
				if !st.xfmr.zeroSeqResolved {
					z0r, z0x = st.xfmr.rOhm, st.xfmr.xOhm
					z0Estimated = true
				}
				zResolved := !(z0r == 0 && z0x == 0)
				switch {
				case st.xfmr.hvConn == connWyeGrounded && st.xfmr.lvConn == connWyeGrounded:
					// YNyn -- the ground path extends from the source through.
					ratioSq := st.xfmr.ratio * st.xfmr.ratio
					acc.r0Ohm = parent.r0Ohm*ratioSq + z0r
					acc.x0Ohm = parent.x0Ohm*ratioSq + z0x
					acc.grounded = parent.grounded
					acc.resolved = parent.resolved && zResolved
					acc.estimated = parent.estimated || z0Estimated
				case st.xfmr.lvConn == connWyeGrounded:
					// Grounded-wye LV behind a blocking HV winding -- this transformer
					// is the ground source for its LV side; upstream Z0 is discarded
					// (and with it the upstream estimated bit -- a fresh ground path).
					acc.r0Ohm, acc.x0Ohm = z0r, z0x
					acc.grounded = true
					acc.resolved = zResolved
					acc.estimated = z0Estimated
				default:
					// LV delta or ungrounded wye -- no earth path onto this side.
					acc.r0Ohm, acc.x0Ohm = 0, 0
					acc.grounded = false
					acc.resolved = true
					acc.estimated = false
				}
			case st != nil && st.loaded.FeatureClass.NetworkRole == "edge":
				// A conductor extends the current ground path (if any) by its own
				// zero-sequence impedance. On an ungrounded section acc == parent
				// (still ungrounded, nothing to add).
				if parent.grounded {
					switch {
					case !st.impedance.resolved:
						acc.resolved = false
					case st.impedance.zeroSeqResolved:
						// Real measured r0/x0 -- no estimate introduced.
						acc.r0Ohm += st.impedance.r0Ohm
						acc.x0Ohm += st.impedance.x0Ohm
					default:
						// No zero-sequence data: default to zeroSeqLineFactor × Z1.
						acc.r0Ohm += st.impedance.rOhm * zeroSeqLineFactor
						acc.x0Ohm += st.impedance.xOhm * zeroSeqLineFactor
						acc.estimated = true
					}
				}
			}
		}

		// An earthing transformer tapped at this bus adds its shunt to earth. On a
		// grounded bus it parallels the existing path (keeping that path's estimated
		// bit); on an ungrounded one it establishes the ground from measured z0_ohm,
		// so the path becomes grounded and non-estimated from here down. The shunt
		// is referred to this bus's own base (z0_ohm is specified at the earthing
		// transformer's connection voltage).
		if sh, ok := earthing[n]; ok {
			if acc.grounded {
				acc.r0Ohm, acc.x0Ohm = parallelZ(acc.r0Ohm, acc.x0Ohm, sh.r0, sh.x0)
			} else {
				acc.r0Ohm, acc.x0Ohm = sh.r0, sh.x0
				acc.grounded = true
				acc.resolved = true
				acc.estimated = false
			}
		}

		z0[n] = acc
	}
	return z0
}
