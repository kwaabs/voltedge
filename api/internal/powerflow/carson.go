package powerflow

import (
	"math"

	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// Carson's equations -- the multi-conductor series-impedance build the Tier 2
// coupled unbalanced load flow (unbalanced_coupled.go) rests on. Given where
// each wire sits on the structure and its own geometric mean radius, Carson
// gives the full asymmetric primitive impedance matrix WITH an earth-return
// path; folding the neutral away (Kron reduction) leaves the 3x3 phase-
// impedance matrix whose off-diagonal (mutual) terms are exactly what the
// decoupled Tier 1 sweep cannot see -- the reason a heavily loaded phase pulls
// its neighbours' voltages down in the field.
//
// Formulation is the standard modified-Carson approximation in metric units
// (ohm/km, positions/GMR/distances in metres), then scaled by the conductor's
// real length. Frequency and earth resistivity are run parameters (defaults
// 50 Hz / 100 ohm-m); every geometry input comes off the conductor's material
// spec (metadata.MaterialSpec, migrations/0117).

const (
	// defaultFrequencyHz / defaultEarthResistivityOhmM are the Carson run
	// defaults when the request leaves them unset: 50 Hz (VoltEdge's per-km
	// impedance columns are metric/50 Hz) and 100 ohm-m, the textbook average
	// soil resistivity Carson's De term is usually quoted at.
	defaultFrequencyHz          = 50.0
	defaultEarthResistivityOhmM = 100.0

	// gmrSolidFactor turns a solid round wire's physical radius into its
	// geometric mean radius (GMR = e^(-1/4) * r). Used only as the fallback
	// when a spec records a diameter but no explicit gmr_phase_mm.
	gmrSolidFactor = 0.7788
)

// carsonMatrix is one conductor's resolved 3x3 complex phase-impedance matrix
// in OHMS (already scaled by the conductor's real length), plus whether a
// neutral wire was Kron-reduced into it. resolved = false means the spec
// carried no usable line geometry, so the coupled sweep falls back to the
// decoupled diagonal impedance for that segment.
type carsonMatrix struct {
	z          [phaseCount][phaseCount]complex128
	hasNeutral bool
	resolved   bool
}

// carsonWire is one conductor in the primitive matrix: its position on the
// structure (m) and its own per-length series inputs (GMR in m, AC resistance
// in ohm/km).
type carsonWire struct {
	x, y   float64
	gmrM   float64
	rOhmKm float64
}

// buildCarsonMatrix builds a conductor's 3x3 phase-impedance matrix (ohm) from
// its material spec's line geometry, or returns resolved = false when the spec
// lacks the phase positions / GMR the build needs. All three phase positions
// and a phase GMR (explicit gmr_phase_mm, else derived from diameter_mm) are
// required; a neutral is optional -- present (position + GMR) it is Kron-reduced
// into the phase partition, absent the line is treated as 3-wire.
func buildCarsonMatrix(spec *metadata.MaterialSpec, lengthM, freqHz, earthRho float64) carsonMatrix {
	if spec == nil || lengthM <= 0 {
		return carsonMatrix{}
	}
	gmrPhaseM, ok := phaseGMRMeters(spec)
	if !ok {
		return carsonMatrix{}
	}
	if spec.PosAX == nil || spec.PosAY == nil ||
		spec.PosBX == nil || spec.PosBY == nil ||
		spec.PosCX == nil || spec.PosCY == nil {
		return carsonMatrix{}
	}
	rPhaseKm := 0.0
	if spec.ResistanceOhmPerKm != nil {
		rPhaseKm = *spec.ResistanceOhmPerKm
	}
	wires := []carsonWire{
		{*spec.PosAX, *spec.PosAY, gmrPhaseM, rPhaseKm},
		{*spec.PosBX, *spec.PosBY, gmrPhaseM, rPhaseKm},
		{*spec.PosCX, *spec.PosCY, gmrPhaseM, rPhaseKm},
	}
	hasNeutral := spec.PosNX != nil && spec.PosNY != nil &&
		spec.GmrNeutralMm != nil && *spec.GmrNeutralMm > 0
	if hasNeutral {
		rNeutralKm := 0.0
		if spec.ResistanceNeutralOhmPerKm != nil {
			rNeutralKm = *spec.ResistanceNeutralOhmPerKm
		}
		wires = append(wires, carsonWire{*spec.PosNX, *spec.PosNY, *spec.GmrNeutralMm / 1000, rNeutralKm})
	}

	// Modified-Carson constants (metric, ohm/km). rEarth = pi^2 * f * 1e-4;
	// kX = 4*pi*f * 1e-4 (the coefficient of the ln term); De = 658.5*sqrt(rho/f)
	// is the equivalent earth-return depth in metres.
	if freqHz <= 0 {
		freqHz = defaultFrequencyHz
	}
	if earthRho <= 0 {
		earthRho = defaultEarthResistivityOhmM
	}
	omega := 2 * math.Pi * freqHz
	rEarth := (math.Pi / 2) * omega * 1e-4
	kX := 2 * omega * 1e-4
	de := 658.5 * math.Sqrt(earthRho/freqHz)

	// Primitive impedance matrix over every wire (ohm/km).
	m := len(wires)
	prim := make([][]complex128, m)
	for i := range prim {
		prim[i] = make([]complex128, m)
	}
	for i := 0; i < m; i++ {
		for j := 0; j < m; j++ {
			if i == j {
				gmr := wires[i].gmrM
				if gmr <= 0 {
					gmr = 1e-6
				}
				prim[i][j] = complex(wires[i].rOhmKm+rEarth, kX*math.Log(de/gmr))
				continue
			}
			d := math.Hypot(wires[i].x-wires[j].x, wires[i].y-wires[j].y)
			if d <= 0 {
				d = 1e-6 // guard against two wires specified at the same point
			}
			prim[i][j] = complex(rEarth, kX*math.Log(de/d))
		}
	}

	// Kron-reduce the neutral (wire index 3) into the 3x3 phase partition:
	// Z_ij = z_ij - z_in * z_nj / z_nn. With no neutral the phase partition IS
	// the primitive matrix.
	var z [phaseCount][phaseCount]complex128
	if hasNeutral {
		znn := prim[3][3]
		for i := 0; i < phaseCount; i++ {
			for j := 0; j < phaseCount; j++ {
				z[i][j] = prim[i][j] - prim[i][3]*prim[3][j]/znn
			}
		}
	} else {
		for i := 0; i < phaseCount; i++ {
			for j := 0; j < phaseCount; j++ {
				z[i][j] = prim[i][j]
			}
		}
	}

	// Scale ohm/km -> ohm by the conductor's real length.
	lengthKm := complex(lengthM/1000, 0)
	for i := 0; i < phaseCount; i++ {
		for j := 0; j < phaseCount; j++ {
			z[i][j] *= lengthKm
		}
	}
	return carsonMatrix{z: z, hasNeutral: hasNeutral, resolved: true}
}

// phaseGMRMeters returns a spec's phase-conductor GMR in metres: the explicit
// gmr_phase_mm when set, else the solid-round-wire approximation
// GMR = 0.7788 * radius derived from diameter_mm. ok = false when neither is
// available, which disqualifies the spec from the Carson build.
func phaseGMRMeters(spec *metadata.MaterialSpec) (float64, bool) {
	if spec.GmrPhaseMm != nil && *spec.GmrPhaseMm > 0 {
		return *spec.GmrPhaseMm / 1000, true
	}
	if spec.DiameterMm != nil && *spec.DiameterMm > 0 {
		return gmrSolidFactor * (*spec.DiameterMm / 2) / 1000, true
	}
	return 0, false
}

// resolveCarsonMatrices builds every edge node's Carson 3x3 up front, once, so
// the iterated coupled forward sweep never rebuilds one. Only conductor nodes
// whose resolved material spec carries usable geometry get an entry; every
// other conductor is absent from the map and the sweep falls back to its
// decoupled diagonal impedance there.
func resolveCarsonMatrices(tr *tree, states map[node]*nodeState, freqHz, earthRho float64) map[node]carsonMatrix {
	out := make(map[node]carsonMatrix)
	for _, n := range tr.order {
		st := states[n]
		if st == nil || st.loaded.FeatureClass.NetworkRole != "edge" {
			continue
		}
		if !st.impedance.resolved || st.impedance.spec == nil {
			continue
		}
		m := buildCarsonMatrix(st.impedance.spec, st.impedance.lengthM, freqHz, earthRho)
		if m.resolved {
			out[n] = m
		}
	}
	return out
}
