package trace

import (
	"sort"
	"testing"
)

// feeder7 is the manual's Feeder 7 estate as an in-memory graph: a grid infeed
// (T-ZS, tier 200) feeding bus B7; REC-7 and a fused tee FSW-12 on the trunk; an
// RMU (ways A, B and a tee way T, all authored way->bus) with the clinic behind
// way T; a ring tail ending at the normally-open tie T-79 to Feeder 9; and a
// Feeder 8 street off the same bus.
type f7 struct {
	nodes map[string]node
	adj   map[node][]node
	tiers map[string]int
}

func feeder7() f7 {
	cls := map[string]string{ // feature -> class (= tier key)
		"ZS": "POWER_XFMR", "ZS9": "POWER_XFMR",
		"c0": "MV_CONDUCTOR", "c1": "MV_CONDUCTOR", "c2": "MV_CONDUCTOR", "c3": "MV_CONDUCTOR", "c4": "MV_CONDUCTOR", "c4b": "MV_CONDUCTOR",
		"c5": "MV_CONDUCTOR", "c8": "MV_CONDUCTOR", "c9": "MV_CONDUCTOR", "c90": "MV_CONDUCTOR",
		"B7": "MV_BUSBAR", "B9": "MV_BUSBAR", "RB": "MV_BUSBAR",
		"CB7": "MV_BREAKER", "CB8": "MV_BREAKER", "CB9": "MV_BREAKER", "REC7": "MV_RECLOSER",
		"FSW12": "MV_SWITCH", "WA": "MV_SWITCH", "WB": "MV_SWITCH", "WT": "MV_SWITCH", "T79": "MV_SWITCH", "SEC4": "MV_SECTIONALIZER",
		"CT7": "MV_CT", "VT7": "MV_VT",
		"TX220": "DIST_XFMR", "TX230": "DIST_XFMR", "TX810": "DIST_XFMR", "TX910": "DIST_XFMR",
		"SP220": "SERVICE_POINT", "SP230": "SERVICE_POINT", "SP8": "SERVICE_POINT", "SP910": "SERVICE_POINT",
	}
	edges := [][2]string{
		{"ZS", "c0"}, {"c0", "B7"}, {"B7", "CB7"}, {"CB7", "c1"}, {"c1", "CT7"}, {"c1", "VT7"}, {"c1", "REC7"}, {"REC7", "c2"}, {"c2", "FSW12"}, {"FSW12", "c3"}, {"c3", "TX220"}, {"TX220", "SP220"},
		{"B7", "CB8"}, {"CB8", "c8"}, {"c8", "TX810"}, {"TX810", "SP8"},
		{"c2", "WA"}, {"WA", "RB"}, {"WB", "RB"}, {"WT", "RB"}, {"c4", "WB"}, {"c5", "WT"}, {"c5", "TX230"}, {"TX230", "SP230"},
		{"c4", "SEC4"}, {"SEC4", "c4b"}, {"c4b", "T79"}, {"T79", "c9"}, {"c9", "TX910"}, {"TX910", "SP910"}, {"CB9", "c9"}, {"B9", "CB9"}, {"c90", "B9"}, {"ZS9", "c90"},
	}
	g := f7{nodes: map[string]node{}, adj: map[node][]node{}, tiers: map[string]int{
		"POWER_XFMR": 200, "MV_CONDUCTOR": 300, "MV_BUSBAR": 300, "MV_BREAKER": 300, "MV_RECLOSER": 300, "MV_SWITCH": 300, "MV_SECTIONALIZER": 300,
		"MV_CT": 300, "MV_VT": 300, "DIST_XFMR": 400, "SERVICE_POINT": 600,
	}}
	for f, c := range cls {
		g.nodes[f] = node{classID: c, featureID: f}
	}
	for _, e := range edges {
		a, b := g.nodes[e[0]], g.nodes[e[1]]
		g.adj[a] = append(g.adj[a], b)
		g.adj[b] = append(g.adj[b], a)
	}
	return g
}

func (g f7) tree(t *testing.T, start string, mutate func(*f7)) *supplyTree {
	t.Helper()
	if mutate != nil {
		mutate(&g)
	}
	tr, err := buildSupplyTree(g.nodes[start], 10000,
		func(n node) ([]node, error) { return g.adj[n], nil },
		func(c string) (int, bool) { v, ok := g.tiers[c]; return v, ok },
		func(c string) bool { return c == "POWER_XFMR" || c == "GENERATOR" },
		func(c string) bool { return c == "GENERATOR" },
		func(n node) (bool, error) { return n.featureID == "T79", nil })
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func sps(g f7, ds []nodeDepth) []string {
	var out []string
	for _, d := range ds {
		if d.n.classID == "SERVICE_POINT" {
			out = append(out, d.n.featureID)
		}
	}
	sort.Strings(out)
	return out
}

func TestSupplyTreeSubtrees(t *testing.T) {
	g := feeder7()
	tr := g.tree(t, "REC7", nil)
	if tr == nil {
		t.Fatal("no tree")
	}
	for _, c := range []struct {
		dev  string
		want []string
	}{
		{"REC7", []string{"SP220", "SP230"}}, // the clinic behind the RMU bus is counted
		{"CB7", []string{"SP220", "SP230"}},  // Feeder 8 hangs off the bus, not below CB-7
		{"FSW12", []string{"SP220"}},
		{"WT", []string{"SP230"}}, // a tee way's load side is away from the source
		{"WA", []string{"SP230"}}, // the incoming ring way carries the bus
		{"WB", nil},               // the ring-out way feeds only the open tail
		{"CB8", []string{"SP8"}},
	} {
		got := sps(g, tr.below(g.nodes[c.dev]))
		if !eq(got, c.want) {
			t.Errorf("below(%s) service points = %v, want %v", c.dev, got, c.want)
		}
	}
}

func TestSupplyTreeStopsAtTheNormallyOpenTie(t *testing.T) {
	g := feeder7()
	tr := g.tree(t, "REC7", nil)
	if tr.contains(g.nodes["SP910"]) && tr.depth[g.nodes["SP910"]] > 0 {
		// SP910 may be in the component's tree only if rooted at ZS9, never below REC7
		for _, d := range tr.below(g.nodes["REC7"]) {
			if d.n.featureID == "SP910" {
				t.Fatal("Feeder 9 was dragged below REC-7 through the normally-open tie")
			}
		}
	}
	// Feeder 9 is its own tree, rooted at ZS9.
	tr9 := g.tree(t, "CB9", nil)
	if got := sps(g, tr9.below(g.nodes["CB9"])); !eq(got, []string{"SP910"}) {
		t.Fatalf("below(CB9) = %v", got)
	}
}

func TestSupplyTreeUpstreamChainBlamesTheFeedingDevice(t *testing.T) {
	g := feeder7()
	tr := g.tree(t, "SP230", nil)
	first := func(from string, classes ...string) string {
		for _, a := range tr.above(g.nodes[from]) {
			for _, c := range classes {
				if a.n.classID == c {
					return a.n.featureID
				}
			}
		}
		return ""
	}
	prot := []string{"MV_FUSE", "MV_RECLOSER", "MV_BREAKER"}
	if got := first("SP230", prot...); got != "REC7" {
		t.Fatalf("nearest protective device above SP230 = %q, want REC7", got)
	}
	if got := first("SP220", prot...); got != "REC7" {
		t.Fatalf("nearest protective device above SP220 = %q, want REC7", got)
	}
	if got := first("SP8", prot...); got != "CB8" {
		t.Fatalf("nearest protective device above SP8 = %q, want CB8", got)
	}
	// FSW-12 is on SP220's chain but not SP230's.
	onChain := func(from, dev string) bool {
		for _, a := range tr.above(g.nodes[from]) {
			if a.n.featureID == dev {
				return true
			}
		}
		return false
	}
	if !onChain("SP220", "FSW12") || onChain("SP230", "FSW12") {
		t.Fatal("FSW-12 should be above SP220 only")
	}
	// the chain ends at the grid infeed and never includes the start
	chain := tr.above(g.nodes["SP230"])
	if last := chain[len(chain)-1].n.featureID; last != "ZS" {
		t.Fatalf("chain ends at %q, want the infeed ZS", last)
	}
}

func TestSupplyTreeIgnoresAGeneratorAndNeverClimbsTiers(t *testing.T) {
	g := feeder7()
	gen := node{classID: "GENERATOR", featureID: "gen1"}
	g.nodes["gen1"] = gen
	g.tiers["GENERATOR"] = 0
	g.adj[gen] = []node{g.nodes["B7"]}
	g.adj[g.nodes["B7"]] = append(g.adj[g.nodes["B7"]], gen)
	// a second distribution transformer feeding SP230's LV side from Feeder 8
	// (MV -> LV -> back up to MV) must not create a path that climbs a tier
	lvb := node{classID: "SERVICE_POINT", featureID: "SP-X"}
	g.nodes["SP-X"] = lvb
	g.adj[lvb] = []node{g.nodes["TX810"], g.nodes["TX230"]}
	g.adj[g.nodes["TX810"]] = append(g.adj[g.nodes["TX810"]], lvb)
	g.adj[g.nodes["TX230"]] = append(g.adj[g.nodes["TX230"]], lvb)
	tr, err := buildSupplyTree(g.nodes["REC7"], 10000,
		func(n node) ([]node, error) { return g.adj[n], nil },
		func(c string) (int, bool) { v, ok := g.tiers[c]; return v, ok },
		func(c string) bool { return c == "POWER_XFMR" || c == "GENERATOR" },
		func(c string) bool { return c == "GENERATOR" },
		func(n node) (bool, error) { return n.featureID == "T79", nil })
	if err != nil || tr == nil {
		t.Fatalf("tree=%v err=%v", tr, err)
	}
	if tr.contains(gen) {
		t.Fatal("a generator must not be placed in (or root) the supply tree")
	}
	if got := tr.parent[g.nodes["TX230"]]; got.featureID != "c5" {
		t.Fatalf("TX230 should be fed from c5, got %q (the walk climbed through the LV side)", got.featureID)
	}
}

func TestSupplyTreeFallsBackWithoutRoots(t *testing.T) {
	g := feeder7()
	if tr := g.tree(t, "REC7", func(x *f7) { x.tiers = map[string]int{} }); tr != nil {
		t.Fatal("with no tiers seeded there is no usable root; the caller must fall back")
	}
	// the top tier is not a transformer/source class: no root
	tr, err := buildSupplyTree(g.nodes["REC7"], 10000,
		func(n node) ([]node, error) { return g.adj[n], nil },
		func(c string) (int, bool) { v, ok := g.tiers[c]; return v, ok },
		func(c string) bool { return false },
		func(c string) bool { return false },
		nil)
	if err != nil || tr != nil {
		t.Fatalf("tree=%v err=%v, want nil", tr, err)
	}
}
