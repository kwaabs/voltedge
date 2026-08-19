package connectivity

import "testing"

// TestPhasesIntersect covers the phase-set intersection at the heart of
// Phase 9 Slice 0's phase gate: two PHASE codes are compatible iff they
// share a live line conductor (L1/L2/L3). Neutral is never a shared
// phase, so codes that overlap only on N are incompatible.
func TestPhasesIntersect(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"single-phase into three-phase supply", "L1N", "L1L2L3N", true},
		{"same single phase", "L1N", "L1N", true},
		{"different single phases share only neutral", "L1N", "L2N", false},
		{"three-phase to three-phase", "L1L2L3N", "L1L2L3N", true},
		{"bare line phase into three-phase", "L2", "L1L2L3N", true},
		{"neutral only never intersects", "N", "L1L2L3N", false},
		{"disjoint bare phases", "L1", "L3", false},
		{"case-insensitive and space-tolerant", " l1n ", "L1L2L3N", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := phasesIntersect(c.a, c.b); got != c.want {
				t.Fatalf("phasesIntersect(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
			}
			// Intersection is symmetric -- the gate relies on this, since
			// it checks either connection direction.
			if got := phasesIntersect(c.b, c.a); got != c.want {
				t.Fatalf("phasesIntersect(%q, %q) [swapped] = %v, want %v", c.b, c.a, got, c.want)
			}
		})
	}
}
