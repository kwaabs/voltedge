package trace

import (
	"sort"
	"testing"
)

// memGraph is a directed edge list with a neighbours function shaped like
// neighborsOf, for testing walkWithJunctions without a database.
type memGraph struct{ edges [][2]node } // from -> to

func (g memGraph) neighbors(n node, dir string) ([]node, error) {
	var out []node
	for _, e := range g.edges {
		switch dir {
		case "DOWNSTREAM":
			if e[0] == n {
				out = append(out, e[1])
			}
		case "UPSTREAM":
			if e[1] == n {
				out = append(out, e[0])
			}
		default:
			if e[0] == n {
				out = append(out, e[1])
			} else if e[1] == n {
				out = append(out, e[0])
			}
		}
	}
	return out, nil
}

func ids(r []nodeDepth) []string {
	var s []string
	for _, nd := range r {
		s = append(s, nd.n.featureID)
	}
	sort.Strings(s)
	return s
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// An RMU authored the way the assembly seed wires it: every way is way->bus, so
// the bus is a pure sink. A strict directed walk from the incoming way reaches
// the bus and stops; the junction pass-through must carry on out of the sibling
// way to the supply behind it (SP-230 on Feeder 7).
func rmuGraph() (memGraph, node, node, node, node, node) {
	feeder := node{classID: "cond", featureID: "feeder"}
	wayA := node{classID: "sw", featureID: "wayA"} // incoming way
	bus := node{classID: "bus", featureID: "bus"}
	wayB := node{classID: "sw", featureID: "wayB"} // outgoing way
	cable := node{classID: "cond", featureID: "cable"}
	sp := node{classID: "sp", featureID: "sp230"}
	g := memGraph{edges: [][2]node{
		{feeder, wayA}, {wayA, bus}, // way -> bus
		{wayB, bus},                // sibling way authored way -> bus too
		{wayB, cable}, {cable, sp}, // load side of the sibling way
	}}
	return g, feeder, wayA, bus, wayB, sp
}

func TestWalkDownstreamPassesThroughJunctionBus(t *testing.T) {
	g, feeder, _, _, _, _ := rmuGraph()
	junctions := map[string]bool{"bus": true}

	// Strictly directed (no junction set) misses the supply behind the bus.
	strict, _, _ := walkWithJunctions(feeder, "DOWNSTREAM", 1000, g.neighbors, nil, nil, nil)
	if got := ids(strict); !eq(got, []string{"bus", "feeder", "wayA"}) {
		t.Fatalf("strict directed walk = %v, expected it to dead-end at the bus", got)
	}

	got, truncated, err := walkWithJunctions(feeder, "DOWNSTREAM", 1000, g.neighbors, junctions, nil, nil)
	if err != nil || truncated {
		t.Fatalf("err=%v truncated=%v", err, truncated)
	}
	want := []string{"bus", "cable", "feeder", "sp230", "wayA", "wayB"}
	if ids(got) == nil || !eq(ids(got), want) {
		t.Fatalf("junction pass-through walk = %v, want %v", ids(got), want)
	}
}

func TestWalkDoesNotClimbBackPastTheBusWithTiers(t *testing.T) {
	g, _, wayA, _, _, _ := rmuGraph()
	// A source above the feeder, in a higher (lower number) tier than the bus.
	src := node{classID: "src", featureID: "src"}
	g.edges = append(g.edges, [2]node{src, {classID: "cond", featureID: "feeder"}})
	junctions := map[string]bool{"bus": true}
	tiers := map[string]int{"src": 0, "cond": 1, "sw": 1, "bus": 1, "sp": 2}

	// Start mid-feeder at wayA: an undirected expansion at the bus must not walk
	// back up the feeder to the source.
	got, _, _ := walkWithJunctions(wayA, "DOWNSTREAM", 1000, g.neighbors, junctions, tiers, nil)
	for _, id := range ids(got) {
		if id == "src" || id == "feeder" {
			t.Fatalf("walk climbed back toward the source: %v", ids(got))
		}
	}
	if !eq(ids(got), []string{"bus", "cable", "sp230", "wayA", "wayB"}) {
		t.Fatalf("walk = %v", ids(got))
	}
}

func TestWalkUpstreamArrivesAtSinkBus(t *testing.T) {
	g, feeder, _, _, _, sp := rmuGraph()
	junctions := map[string]bool{"bus": true}
	// Upstream from the supply behind wayB reaches wayB, then the bus (against the
	// edge, via the ARRIVE half), then the incoming way and the feeder.
	got, _, _ := walkWithJunctions(sp, "UPSTREAM", 1000, g.neighbors, junctions, nil, nil)
	has := map[string]bool{}
	for _, id := range ids(got) {
		has[id] = true
	}
	if !has[feeder.featureID] || !has["bus"] {
		t.Fatalf("upstream walk = %v, want it to reach the bus and the feeder", ids(got))
	}
}

func TestWalkWithoutJunctionsMatchesPlainDirectedBFS(t *testing.T) {
	a := node{classID: "c", featureID: "a"}
	b := node{classID: "c", featureID: "b"}
	c := node{classID: "c", featureID: "c"}
	g := memGraph{edges: [][2]node{{a, b}, {b, c}}}
	got, _, _ := walkWithJunctions(a, "DOWNSTREAM", 1000, g.neighbors, map[string]bool{}, nil, nil)
	if !eq(ids(got), []string{"a", "b", "c"}) {
		t.Fatalf("got %v", ids(got))
	}
	got, _, _ = walkWithJunctions(b, "DOWNSTREAM", 1000, g.neighbors, map[string]bool{}, nil, nil)
	if !eq(ids(got), []string{"b", "c"}) {
		t.Fatalf("got %v", ids(got))
	}
}

func TestIsClientError(t *testing.T) {
	if !IsClientError(notFound("feature %s not found", "x")) || !IsClientError(invalid("deleted")) {
		t.Fatal("not-found and validation errors are client errors")
	}
	if IsClientError(errBoom{}) || IsClientError(nil) {
		t.Fatal("other errors and nil are not client errors")
	}
}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }

// A normally-open tie must stop the walk: it is reached, not crossed, so the
// affected set stays on the supplies normally fed through the start instead of
// flooding into the neighbouring feeder (Feeder 9 through T-79).
func TestWalkStopsAtNormallyOpenBoundary(t *testing.T) {
	g, feeder, _, _, wayB, _ := rmuGraph()
	tie := node{classID: "sw", featureID: "tie"}
	f9 := node{classID: "cond", featureID: "f9trunk"}
	sp9 := node{classID: "sp", featureID: "sp910"}
	g.edges = append(g.edges, [2]node{wayB, tie}, [2]node{tie, f9}, [2]node{f9, sp9})
	junctions := map[string]bool{"bus": true}
	isTie := func(n node) (bool, error) { return n == tie, nil }

	flood, _, _ := walkWithJunctions(feeder, "DOWNSTREAM", 1000, g.neighbors, junctions, nil, nil)
	hasF9 := false
	for _, id := range ids(flood) {
		if id == "sp910" {
			hasF9 = true
		}
	}
	if !hasF9 {
		t.Fatalf("without a boundary the walk should flood through the tie, got %v", ids(flood))
	}

	got, _, _ := walkWithJunctions(feeder, "DOWNSTREAM", 1000, g.neighbors, junctions, nil, isTie)
	want := []string{"bus", "cable", "feeder", "sp230", "tie", "wayA", "wayB"}
	if !eq(ids(got), want) {
		t.Fatalf("with the tie as a boundary got %v, want %v", ids(got), want)
	}
}

// The start is locked: a downstream walk from a device whose own class is a
// junction (a recloser, a switch) must not step back up the feeder that feeds
// it, which would drag in every other supply on the same source bus.
func TestWalkStartIsLockedEvenWhenItsClassIsAJunction(t *testing.T) {
	bus7 := node{classID: "bus", featureID: "b7"}
	rec := node{classID: "rec", featureID: "rec7"}
	up := node{classID: "cond", featureID: "c1"}
	down := node{classID: "cond", featureID: "c2"}
	sp := node{classID: "sp", featureID: "sp220"}
	other := node{classID: "sp", featureID: "sp810"} // another feeder off the same source bus
	cb8 := node{classID: "sw", featureID: "cb8"}
	g := memGraph{edges: [][2]node{{bus7, up}, {up, rec}, {rec, down}, {down, sp}, {bus7, cb8}, {cb8, other}}}
	junctions := map[string]bool{"bus": true, "rec": true, "sw": true, "sp": true}
	got, _, _ := walkWithJunctions(rec, "DOWNSTREAM", 1000, g.neighbors, junctions, nil, nil)
	if !eq(ids(got), []string{"c2", "rec7", "sp220"}) {
		t.Fatalf("walk from a junction-class start = %v, want only what is below it", ids(got))
	}
}
