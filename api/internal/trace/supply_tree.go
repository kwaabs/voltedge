package trace

import (
	"context"
	"sort"
	"sync"

	"github.com/uptrace/bun"
)

// The supply tree: who is normally fed through whom.
//
// A strictly directed walk can only guess "below the device" from how each
// connectivity row happened to be authored, and an RMU authors its ways
// way->bus, so the guess fails there; patching it with junction pass-through
// without live-state barriers then floods through ties and back up the feeder.
// The supply tree stops guessing. It roots the component at its grid infeed
// (the minimum-tier transformer/source nodes, e.g. POWER_XFMR), grows a
// breadth-first spanning tree outward over the UNDIRECTED connectivity, steps only
// to an equal-or-higher trace tier (never back toward the source), and does not
// cross a device whose NORMAL state is open (normal_state = NORMALLY_OPEN, e.g. a
// tie): it is reached, not crossed. A node's parent is the neighbour it is
// normally fed from, so:
//
//   - "below device d" is d's subtree, whatever way the ways were wired;
//   - "the protective device upstream of x" is the first one on x's parent chain,
//     so a call is blamed on a device that actually feeds it (never on a sibling
//     feeder the walk happened to leak into);
//   - a ring way's load side is the side away from the source.
//
// Where the component has no usable root (no tiers seeded, or its top tier is
// not a transformer/source) the tree is nil and callers fall back to the
// junction-aware class walk. A ring closed in normal state is a mesh: a node
// takes the first parent breadth-first order reaches (ties by class and id), so
// its subtree is deterministic but arbitrary between the two feeds.

// supplyTree is the spanning tree of one component.
type supplyTree struct {
	parent   map[node]node
	depth    map[node]int
	children map[node][]node
	roots    map[node]bool
	members  map[node]bool
}

// buildSupplyTree floods the component containing start (stopping expansion at
// boundary nodes), picks the roots, and grows the tree. nb returns a node's
// undirected neighbours; tier reports a class's trace_tier (ok=false when
// untiered); isRoot says whether a class may root a tree (transformer/source
// role); boundary reports a normally-open device. Returns nil when no root can be
// found. Pure of the database so it can be unit tested on an in-memory graph.
func buildSupplyTree(start node, maxHops int, nb func(node) ([]node, error), tier func(classID string) (int, bool), isRoot func(classID string) bool, isSourceClass func(classID string) bool, boundary func(node) (bool, error)) (*supplyTree, error) {
	adj := map[node][]node{}
	neighbors := func(n node) ([]node, error) {
		if a, ok := adj[n]; ok {
			return a, nil
		}
		raw, err := nb(n)
		if err != nil {
			return nil, err
		}
		seen := map[node]bool{}
		out := make([]node, 0, len(raw))
		for _, x := range raw {
			if x != n && !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].classID != out[j].classID {
				return out[i].classID < out[j].classID
			}
			return out[i].featureID < out[j].featureID
		})
		adj[n] = out
		return out, nil
	}
	bnd := map[node]bool{}
	isBoundary := func(n node) (bool, error) {
		if v, ok := bnd[n]; ok {
			return v, nil
		}
		v := false
		if boundary != nil {
			var err error
			if v, err = boundary(n); err != nil {
				return false, err
			}
		}
		bnd[n] = v
		return v, nil
	}

	// 1. the component: flood undirected from start; a boundary node is a member
	//    but is not expanded.
	members := map[node]bool{start: true}
	order := []node{start}
	for i := 0; i < len(order) && len(members) < maxHops; i++ {
		cur := order[i]
		if cur != start {
			if b, err := isBoundary(cur); err != nil {
				return nil, err
			} else if b {
				continue
			}
		}
		nbs, err := neighbors(cur)
		if err != nil {
			return nil, err
		}
		for _, x := range nbs {
			if !members[x] {
				members[x] = true
				order = append(order, x)
			}
		}
	}

	// 2. roots: the minimum-tier nodes of the component, provided they are all
	//    of a root-capable (transformer / source) class and not generators.
	minTier, haveTier := 0, false
	for _, n := range order {
		if isSourceClass(n.classID) {
			continue
		}
		if t, ok := tier(n.classID); ok && (!haveTier || t < minTier) {
			minTier, haveTier = t, true
		}
	}
	if !haveTier {
		return nil, nil
	}
	roots := map[node]bool{}
	var rootList []node
	for _, n := range order {
		if isSourceClass(n.classID) {
			continue
		}
		if t, ok := tier(n.classID); ok && t == minTier {
			if !isRoot(n.classID) {
				return nil, nil
			}
			roots[n] = true
			rootList = append(rootList, n)
		}
	}
	if len(rootList) == 0 {
		return nil, nil
	}
	sort.Slice(rootList, func(i, j int) bool {
		if rootList[i].classID != rootList[j].classID {
			return rootList[i].classID < rootList[j].classID
		}
		return rootList[i].featureID < rootList[j].featureID
	})

	// 3. grow the tree breadth-first from every root: step only to an equal-or-
	//    higher tier (untiered neighbours are transparent) and do not expand a
	//    normally-open device.
	t := &supplyTree{parent: map[node]node{}, depth: map[node]int{}, children: map[node][]node{}, roots: roots, members: members}
	queue := make([]node, 0, len(order))
	for _, r := range rootList {
		t.depth[r] = 0
		queue = append(queue, r)
	}
	for i := 0; i < len(queue); i++ {
		cur := queue[i]
		if !roots[cur] {
			if b, err := isBoundary(cur); err != nil {
				return nil, err
			} else if b {
				continue
			}
		}
		nbs, err := neighbors(cur)
		if err != nil {
			return nil, err
		}
		ct, cok := tier(cur.classID)
		for _, x := range nbs {
			if !members[x] {
				continue
			}
			if _, done := t.depth[x]; done {
				continue
			}
			if isSourceClass(x.classID) {
				continue
			}
			if nt, ok := tier(x.classID); ok && cok && nt < ct {
				continue // never step back toward the source
			}
			t.depth[x] = t.depth[cur] + 1
			t.parent[x] = cur
			t.children[cur] = append(t.children[cur], x)
			queue = append(queue, x)
		}
	}
	return t, nil
}

// contains reports whether n is placed in the tree (reached from a root).
func (t *supplyTree) contains(n node) bool {
	_, ok := t.depth[n]
	return ok
}

// below returns every descendant of n (n excluded), nearest-first, ties by
// class and id.
func (t *supplyTree) below(n node) []nodeDepth {
	out := []nodeDepth{}
	queue := []node{n}
	for i := 0; i < len(queue); i++ {
		for _, c := range t.children[queue[i]] {
			out = append(out, nodeDepth{n: c, depth: t.depth[c] - t.depth[n]})
			queue = append(queue, c)
		}
	}
	return out
}

// above returns n's ancestors, nearest-first (n excluded), ending at a root.
func (t *supplyTree) above(n node) []nodeDepth {
	out := []nodeDepth{}
	for cur, d := n, 0; ; {
		p, ok := t.parent[cur]
		if !ok {
			return out
		}
		d++
		out = append(out, nodeDepth{n: p, depth: d})
		cur = p
	}
}

// supplyCache memoises supply trees for the lifetime of one OMS operation
// (a Predict over many calls asks the same feeder's tree repeatedly). A tree is
// stored under every member of its component, so a lookup by any node finds it.
type supplyCache struct {
	mu    sync.Mutex
	trees map[node]*supplyTree
	miss  map[node]bool // components that have no usable root (fallback)
}

type supplyCacheKey struct{}

// WithSupplyCache returns a context under which the class walks reuse one
// supply tree per component instead of rebuilding it for every call.
func WithSupplyCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, supplyCacheKey{}, &supplyCache{trees: map[node]*supplyTree{}, miss: map[node]bool{}})
}

// supplyTreeFor returns the supply tree containing start, or nil when the
// component has no usable root (the caller then falls back to the
// junction-aware walk).
func supplyTreeFor(ctx context.Context, db bun.IDB, start node) (*supplyTree, error) {
	cache, _ := ctx.Value(supplyCacheKey{}).(*supplyCache)
	if cache != nil {
		cache.mu.Lock()
		if t, ok := cache.trees[start]; ok {
			cache.mu.Unlock()
			return t, nil
		}
		if cache.miss[start] {
			cache.mu.Unlock()
			return nil, nil
		}
		cache.mu.Unlock()
	}

	loader := newFeatureClassLoader()
	tiers, err := loader.tiers(ctx, db)
	if err != nil {
		return nil, err
	}
	transformers, err := loader.transformerRoles(ctx, db)
	if err != nil {
		return nil, err
	}
	sources, err := classIDsForRole(ctx, db, "source")
	if err != nil {
		return nil, err
	}
	normalOpen := map[node]bool{}
	t, err := buildSupplyTree(start, maxTraceHops,
		func(n node) ([]node, error) { return neighborsOf(ctx, db, n, "CONNECTED") },
		func(classID string) (int, bool) { v, ok := tiers[classID]; return v, ok },
		func(classID string) bool { return transformers[classID] || sources[classID] },
		func(classID string) bool { return sources[classID] },
		func(n node) (bool, error) {
			if v, ok := normalOpen[n]; ok {
				return v, nil
			}
			v, err := isNormallyOpen(ctx, db, n, loader)
			if err != nil {
				return false, err
			}
			normalOpen[n] = v
			return v, nil
		})
	if err != nil {
		return nil, err
	}
	if cache != nil {
		cache.mu.Lock()
		if t == nil {
			cache.miss[start] = true
		} else {
			for m := range t.members {
				cache.trees[m] = t
			}
		}
		cache.mu.Unlock()
	}
	return t, nil
}
