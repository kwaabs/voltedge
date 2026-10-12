package trace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"sort"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// Exported, class-set graph walks for callers that need "the nearest / all
// features of a given kind up- or down-stream of a start feature" -- the two
// primitives the Phase 49 OMS prediction engine composes (api/internal/oms):
// walk UP from a calling service point to the protective device that would
// have operated, and walk DOWN from that device to the service points it
// feeds. Kept in the trace package (not oms) because the connectivity-graph
// traversal is this package's job; oms only supplies the class set and the
// outage-domain meaning.
//
// Both walks are BARRIER-FREE and topological -- they ignore live switch state
// (device_state / operational_state), matching feederTrace's upstream
// identity-walk reasoning: a fault's protection path and the electrical extent
// it de-energizes are topological facts, independent of which switches happen
// to be open right now. A later OMS slice can layer live switching state on top
// (partial restoration); v1 is deliberately the conservative topological
// answer. Neither ever includes the start node in its result.

// nodeDepth pairs a reached node with its hop distance from the walk's start,
// so UpstreamFeaturesOfClass can return protective devices nearest-first.
type nodeDepth struct {
	n     node
	depth int
}

// classIDsForCodes resolves a set of feature class codes to their ids, the
// form graph nodes carry. Unknown codes are skipped; an all-unknown set is a
// clear 400 rather than a walk that silently matches nothing.
func classIDsForCodes(ctx context.Context, db bun.IDB, codes []string) (map[string]bool, error) {
	if len(codes) == 0 {
		return nil, invalid("no feature class codes given")
	}
	var fcs []metadata.FeatureClass
	if err := db.NewSelect().Model(&fcs).Where("code IN (?)", bun.In(codes)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("trace: resolving class codes: %w", err)
	}
	out := make(map[string]bool, len(fcs))
	for _, fc := range fcs {
		out[fc.ID] = true
	}
	if len(out) == 0 {
		return nil, invalid("none of the feature classes %v exist", codes)
	}
	return out, nil
}

// classIDsForRole resolves every feature class carrying the given
// functional_role (migration 0091) to its id, the form graph nodes carry -- so a
// caller can stop a walk at "the nearest node of ANY class in this role" rather
// than one hardcoded class. role='controller', for instance, spans MV_SWITCH,
// MV_BREAKER, MV_RECLOSER, MV_ISOLATOR, MV_FUSE, LV_SWITCH and any future
// switching class, so section/isolate boundaries track the whole switching
// family without naming each. An empty result is returned as-is (not an error):
// a deployment that hasn't stamped functional_role yet simply has no role-based
// boundary, and the caller falls back to its default class.
func classIDsForRole(ctx context.Context, db bun.IDB, role string) (map[string]bool, error) {
	var fcs []metadata.FeatureClass
	if err := db.NewSelect().Model(&fcs).Where("functional_role = ?", role).Scan(ctx); err != nil {
		return nil, fmt.Errorf("trace: resolving classes for role %q: %w", role, err)
	}
	out := make(map[string]bool, len(fcs))
	for _, fc := range fcs {
		out[fc.ID] = true
	}
	return out, nil
}

// The three boundary_role sets a bounded walk stops at (migration 0129). The
// distinction is grounded in distribution practice -- the kind of boundary device
// defines the kind of span:
//   - isolationBoundaryRoles: interrupting/isolating devices only (protective +
//     switching). An ISOLATION walk reports its boundaries as the switches to
//     OPEN, and a transformer isn't openable, so it is excluded.
//   - switchingSectionRoles: the SWITCHING SECTION -- bounded by any device that
//     can isolate it (protective + switching) PLUS the transformer (a voltage
//     boundary ends a section; the LV below is a separate one). "What I can
//     isolate to work on it."
//   - protectionZoneRoles: the PROTECTION ZONE -- bounded only by devices that
//     interrupt fault current (protective) plus the transformer. A plain switch
//     does NOT bound a protection zone. "What de-energises on a fault."
var (
	isolationBoundaryRoles = []string{"protective", "switching"}
	switchingSectionRoles  = []string{"protective", "switching", "transformer", "source"}
	protectionZoneRoles    = []string{"protective", "transformer", "source"}
)

// classIDsForBoundaryRole resolves the classes carrying one of the given
// boundary_role values (migration 0129 -- the network-sectioning backbone) to
// their ids -- finer than functional_role='controller'. Empty when nothing is
// stamped yet, so the caller falls back.
func classIDsForBoundaryRole(ctx context.Context, db bun.IDB, roles []string) (map[string]bool, error) {
	var fcs []metadata.FeatureClass
	if err := db.NewSelect().Model(&fcs).Where("boundary_role IN (?)", bun.In(roles)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("trace: resolving boundary-role classes: %w", err)
	}
	out := make(map[string]bool, len(fcs))
	for _, fc := range fcs {
		out[fc.ID] = true
	}
	return out, nil
}

// boundaryClassSet resolves the class-set a section or isolation walk stops at,
// the one boundary rule both share. When requested is non-empty (the caller's
// boundary_class was set explicitly) it is honored as exactly that one named
// class. Otherwise the boundary is the CONTROLLER role -- every switching class
// at once (functional_role, migration 0091) -- so the walk stops at the nearest
// switch/breaker/recloser/isolator/fuse of any kind. fallbackClass is consulted
// only when no class carries the controller role yet (an unclassified
// deployment), degrading to the single named class each caller used before the
// role rewire.
func boundaryClassSet(ctx context.Context, db bun.IDB, requested, fallbackClass string, roles []string) (map[string]bool, error) {
	if requested != "" {
		return singleNamedClass(ctx, db, requested)
	}
	// Prefer the explicit sectioning backbone (boundary_role, migration 0129) with
	// the caller's role set (isolation / switching-section / protection-zone).
	// Falls back to the controller functional_role, then a single named class, for
	// a deployment that hasn't seeded boundary_role yet.
	if roleSet, err := classIDsForBoundaryRole(ctx, db, roles); err != nil {
		return nil, err
	} else if len(roleSet) > 0 {
		return roleSet, nil
	}
	set, err := classIDsForRole(ctx, db, roleController)
	if err != nil {
		return nil, err
	}
	if len(set) > 0 {
		return set, nil
	}
	return singleNamedClass(ctx, db, fallbackClass)
}

// singleNamedClass resolves one class code to a one-element class-set, turning
// an unknown code into the same 404 the boundary callers raised inline before.
func singleNamedClass(ctx context.Context, db bun.IDB, code string) (map[string]bool, error) {
	loaded, err := metadata.Load(ctx, db, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("boundary feature class %q does not exist", code)
		}
		return nil, err
	}
	return map[string]bool{loaded.FeatureClass.ID: true}, nil
}

// singleClassSet adapts a single, optional class id (the config-driven
// target_class_id, a *string) to bfs's class-set target parameter. nil or empty
// stays nil -- "no class target" -- so a shortest_path or unbounded config walks
// exactly as before the set generalization.
func singleClassSet(id *string) map[string]bool {
	if id == nil || *id == "" {
		return nil
	}
	return map[string]bool{*id: true}
}

// reachableWithDepth is a plain barrier-free BFS from start in one direction,
// returning every reached node (including start, at depth 0) with its hop
// distance. Bounded by maxTraceHops, the same defensive valve the config-driven
// bfs uses. Strictly directional: it never crosses a junction against the
// stored from/to of a connectivity row. The supply-path walk (supply.go) keeps
// this exact behaviour; the OMS class walks use reachableJunctionAware.
func reachableWithDepth(ctx context.Context, db bun.IDB, start node, direction string) ([]nodeDepth, bool, error) {
	return walkWithJunctions(start, direction, maxTraceHops, func(n node, dir string) ([]node, error) {
		return neighborsOf(ctx, db, n, dir)
	}, nil, nil, nil)
}

// reachableJunctionAware is reachableWithDepth with the junction pass-through of
// bfs (see the note there): a bus / RMU common node (network_role='junction') is
// entered and left whichever way its ways were authored, so a supply fed through
// an RMU bus is found, and the cross-layer tier guard still stops a walk
// climbing back toward its source. It is still insensitive to LIVE switch
// state -- the affected set must not shrink because a tie happens to be closed --
// but it stops at a device whose NORMAL state is open (normal_state =
// NORMALLY_OPEN, e.g. a tie): that device is reached but not crossed, so the walk
// stays on the supplies normally fed through the start and cannot flood into a
// neighbouring feeder through a tie. Used by the OMS class walks (prediction,
// affected counts, isolation boundary) and the faults impact walk.
func reachableJunctionAware(ctx context.Context, db bun.IDB, start node, direction string) ([]nodeDepth, bool, error) {
	loader := newFeatureClassLoader()
	var junctions map[string]bool
	var tiers map[string]int
	if direction == "DOWNSTREAM" || direction == "UPSTREAM" {
		var err error
		if junctions, err = loader.junctions(ctx, db); err != nil {
			return nil, false, err
		}
		if tiers, err = loader.tiers(ctx, db); err != nil {
			return nil, false, err
		}
	}
	normalOpen := map[node]bool{}
	boundary := func(n node) (bool, error) {
		if v, ok := normalOpen[n]; ok {
			return v, nil
		}
		v, err := isNormallyOpen(ctx, db, n, loader)
		if err != nil {
			return false, err
		}
		normalOpen[n] = v
		return v, nil
	}
	return walkWithJunctions(start, direction, maxTraceHops, func(n node, dir string) ([]node, error) {
		return neighborsOf(ctx, db, n, dir)
	}, junctions, tiers, boundary)
}

// isNormallyOpen reports whether the feature's normal_state attribute is
// NORMALLY_OPEN. A class without the field, a missing feature (a dangling
// connectivity row) or an unset value is not a boundary.
func isNormallyOpen(ctx context.Context, db bun.IDB, n node, loader *featureClassLoader) (bool, error) {
	loaded, err := loader.loadedFor(ctx, db, n.classID)
	if err != nil {
		return false, err
	}
	row, err := crud.GetFeature(ctx, db, loaded, n.featureID)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return false, nil
		}
		return false, err
	}
	attrs, _ := row["attributes"].(map[string]any)
	v, _ := attrs["normal_state"].(string)
	return v == "NORMALLY_OPEN", nil
}

// walkWithJunctions is the pure core: a BFS over the neighbours function with
// the junction pass-through of bfs (LEAVE half for DOWNSTREAM and UPSTREAM,
// ARRIVE half for UPSTREAM), the tier guard judged on the original direction,
// and an optional boundary predicate (a boundary node is included in the result
// but never expanded). nil junctions/tiers/boundary give the plain directed
// BFS. Free of the database so it can be unit tested on an in-memory graph.
func walkWithJunctions(start node, direction string, maxHops int, neighbors func(node, string) ([]node, error), junctions map[string]bool, tiers map[string]int, boundary func(node) (bool, error)) ([]nodeDepth, bool, error) {
	passThrough := junctions != nil && (direction == "DOWNSTREAM" || direction == "UPSTREAM")
	visited := map[node]bool{start: true}
	unlocked := map[node]bool{}
	queue := []nodeDepth{{n: start, depth: 0}}
	out := make([]nodeDepth, 0, 16)

	for len(queue) > 0 && len(visited) < maxHops {
		cur := queue[0]
		queue = queue[1:]
		out = append(out, cur)

		if boundary != nil && cur.n != start {
			isB, err := boundary(cur.n)
			if err != nil {
				return nil, false, err
			}
			if isB {
				continue
			}
		}

		// The start is locked (strictly directional) even when its own class is a
		// junction (every switch, recloser and transformer is): a downstream walk from
		// a device must not step back up the feeder that supplies it.
		expandUndirected := passThrough && cur.n != start && (unlocked[cur.n] || junctions[cur.n.classID])
		dir := direction
		if expandUndirected {
			dir = "CONNECTED"
		}
		nbs, err := neighbors(cur.n, dir)
		if err != nil {
			return nil, false, err
		}
		// ARRIVE half (UPSTREAM only): pull in an incident junction even when the
		// edge points into it, so an upstream walk can land on a one-way-sink bus.
		if passThrough && direction == "UPSTREAM" && !expandUndirected {
			incident, err := neighbors(cur.n, "CONNECTED")
			if err != nil {
				return nil, false, err
			}
			for _, nb := range incident {
				if junctions[nb.classID] {
					nbs = append(nbs, nb)
				}
			}
		}
		for _, nb := range nbs {
			if visited[nb] {
				continue
			}
			if ct, ok := tiers[cur.n.classID]; ok {
				if nt, ok := tiers[nb.classID]; ok {
					if direction == "DOWNSTREAM" && nt < ct {
						continue
					}
					if direction == "UPSTREAM" && nt > ct {
						continue
					}
				}
			}
			visited[nb] = true
			if expandUndirected {
				unlocked[nb] = true
			}
			queue = append(queue, nodeDepth{n: nb, depth: cur.depth + 1})
		}
	}
	// Cap hit with work still queued -- the walk is incomplete.
	truncated := len(queue) > 0 && len(visited) >= maxHops
	return out, truncated, nil
}

// reachForOMS is the walk behind the OMS class walks (prediction, affected counts,
// the isolation boundary) and the faults impact walk. When the start's component
// has a usable root it answers from the SUPPLY TREE (supply_tree.go): "below" is
// the start's subtree and "above" its parent chain, which needs no guess about how
// the connectivity rows were authored. Otherwise (no tiers seeded, or no
// transformer/source at the top of the component) it falls back to the
// junction-aware walk (reachableJunctionAware). The start is included at depth 0,
// like reachableWithDepth.
func reachForOMS(ctx context.Context, db bun.IDB, start node, direction string) ([]nodeDepth, error) {
	if direction == "DOWNSTREAM" || direction == "UPSTREAM" {
		t, err := supplyTreeFor(ctx, db, start)
		if err != nil {
			return nil, err
		}
		if t != nil && t.contains(start) {
			var rest []nodeDepth
			if direction == "DOWNSTREAM" {
				rest = t.below(start)
			} else {
				rest = t.above(start)
			}
			return append([]nodeDepth{{n: start, depth: 0}}, rest...), nil
		}
	}
	reached, _, err := reachableJunctionAware(ctx, db, start, direction)
	return reached, err
}

// featuresOfClass walks in one direction from start and returns every reached
// feature whose class is in classIDs, ordered nearest-first (by hop distance,
// then by id for a stable tiebreak). The start node is never included.
func featuresOfClass(ctx context.Context, db bun.IDB, start FeatureRef, direction string, classCodes []string) ([]FeatureRef, error) {
	return featuresOfClassWhere(ctx, db, start, direction, classCodes, nil)
}

// ClassAttrFilter makes a class count as a hit only when a boolean attribute is
// true -- e.g. an MV_SWITCH counts as protective only when is_fused is set.
type ClassAttrFilter struct {
	Class string // feature class code
	Field string // boolean attribute that must be true
}

// featuresOfClassWhere is featuresOfClass plus attribute-qualified classes: the
// classes named in extra are hits only for features whose Field is true.
func featuresOfClassWhere(ctx context.Context, db bun.IDB, start FeatureRef, direction string, classCodes []string, extra []ClassAttrFilter) ([]FeatureRef, error) {
	s, err := resolveNode(ctx, db, featureRef{FeatureClass: start.FeatureClass, FeatureID: start.FeatureID})
	if err != nil {
		return nil, err
	}
	classIDs, err := classIDsForCodes(ctx, db, classCodes)
	if err != nil {
		return nil, err
	}
	extraIDs := map[string]string{} // class id -> required boolean field
	if len(extra) > 0 {
		codes := make([]string, len(extra))
		for i, e := range extra {
			codes[i] = e.Class
		}
		ids, err := classIDsForCodes(ctx, db, codes)
		if err != nil {
			return nil, err
		}
		byCode := map[string]string{}
		for _, e := range extra {
			byCode[e.Class] = e.Field
		}
		fcs, err := classCodesForIDs(ctx, db, ids)
		if err != nil {
			return nil, err
		}
		for id, code := range fcs {
			extraIDs[id] = byCode[code]
		}
	}

	// OMS class walks (UpstreamFeaturesOfClass/DownstreamFeaturesOfClass) return a
	// bare []FeatureRef with no result struct, so there is nowhere to surface a
	// truncation flag -- discard it (they truncate at maxTraceHops as before).
	reached, err := reachForOMS(ctx, db, s, direction)
	if err != nil {
		return nil, err
	}

	hits := make([]nodeDepth, 0, 8)
	for _, nd := range reached {
		if nd.n == s {
			continue
		}
		if classIDs[nd.n.classID] {
			hits = append(hits, nd)
		} else if field, ok := extraIDs[nd.n.classID]; ok {
			on, err := boolAttr(ctx, db, nd.n, field)
			if err != nil {
				return nil, err
			}
			if on {
				hits = append(hits, nd)
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].depth != hits[j].depth {
			return hits[i].depth < hits[j].depth
		}
		return hits[i].n.featureID < hits[j].n.featureID
	})

	nodes := make([]node, len(hits))
	for i, h := range hits {
		nodes[i] = h.n
	}
	classCodesByID, err := nodeClassCodes(ctx, db, nodes)
	if err != nil {
		return nil, err
	}
	return toExportedRefs(toFeatureRefs(nodes, classCodesByID)), nil
}

// UpstreamFeaturesOfClass returns every feature whose class is in classCodes
// on the path from start toward the network source, nearest-first. The OMS
// prediction engine calls it with the protective-device classes to get a
// calling service point's ordered protection path -- element 0 is the device
// that would operate for a fault at or below that point.
func UpstreamFeaturesOfClass(ctx context.Context, db bun.IDB, start FeatureRef, classCodes []string) ([]FeatureRef, error) {
	return featuresOfClass(ctx, db, start, "UPSTREAM", classCodes)
}

// DownstreamFeaturesOfClass returns every feature whose class is in classCodes
// electrically downstream of start, nearest-first. The OMS impact analysis
// calls it with the service-point class to enumerate the supplies a tripped
// protective device de-energizes.
func DownstreamFeaturesOfClass(ctx context.Context, db bun.IDB, start FeatureRef, classCodes []string) ([]FeatureRef, error) {
	return featuresOfClass(ctx, db, start, "DOWNSTREAM", classCodes)
}

// UpstreamFeaturesWhere is UpstreamFeaturesOfClass with attribute-qualified
// classes: besides every feature of classCodes, a feature of one of the extra
// classes counts when its boolean Field is true. The OMS prediction engine uses
// it to treat an is_fused MV_SWITCH as a protective device.
func UpstreamFeaturesWhere(ctx context.Context, db bun.IDB, start FeatureRef, classCodes []string, extra []ClassAttrFilter) ([]FeatureRef, error) {
	return featuresOfClassWhere(ctx, db, start, "UPSTREAM", classCodes, extra)
}

// classCodesForIDs resolves feature_class ids (as returned by classIDsForCodes)
// back to their codes.
func classCodesForIDs(ctx context.Context, db bun.IDB, ids map[string]bool) (map[string]string, error) {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	var fcs []metadata.FeatureClass
	if err := db.NewSelect().Model(&fcs).Where("id IN (?)", bun.In(list)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("trace: resolving class codes: %w", err)
	}
	for _, fc := range fcs {
		out[fc.ID] = fc.Code
	}
	return out, nil
}

// boolAttr reports whether the feature's boolean attribute is true; a class
// without the field, a missing feature or an unset value is false.
func boolAttr(ctx context.Context, db bun.IDB, n node, field string) (bool, error) {
	loader := newFeatureClassLoader()
	loaded, err := loader.loadedFor(ctx, db, n.classID)
	if err != nil {
		return false, err
	}
	row, err := crud.GetFeature(ctx, db, loaded, n.featureID)
	if err != nil {
		if crud.IsNotFoundError(err) {
			return false, nil
		}
		return false, err
	}
	attrs, _ := row["attributes"].(map[string]any)
	switch v := attrs[field].(type) {
	case bool:
		return v, nil
	case string:
		return v == "true", nil
	}
	return false, nil
}
