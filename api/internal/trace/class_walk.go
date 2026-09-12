package trace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// boundaryClassSet resolves the class-set a section or isolation walk stops at,
// the one boundary rule both share. When requested is non-empty (the caller's
// boundary_class was set explicitly) it is honored as exactly that one named
// class. Otherwise the boundary is the CONTROLLER role -- every switching class
// at once (functional_role, migration 0091) -- so the walk stops at the nearest
// switch/breaker/recloser/isolator/fuse of any kind. fallbackClass is consulted
// only when no class carries the controller role yet (an unclassified
// deployment), degrading to the single named class each caller used before the
// role rewire.
func boundaryClassSet(ctx context.Context, db bun.IDB, requested, fallbackClass string) (map[string]bool, error) {
	if requested != "" {
		return singleNamedClass(ctx, db, requested)
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
// bfs uses.
func reachableWithDepth(ctx context.Context, db bun.IDB, start node, direction string) ([]nodeDepth, error) {
	visited := map[node]bool{start: true}
	queue := []nodeDepth{{n: start, depth: 0}}
	out := make([]nodeDepth, 0, 16)

	for len(queue) > 0 && len(visited) < maxTraceHops {
		cur := queue[0]
		queue = queue[1:]
		out = append(out, cur)

		neighbors, err := neighborsOf(ctx, db, cur.n, direction)
		if err != nil {
			return nil, err
		}
		for _, nb := range neighbors {
			if visited[nb] {
				continue
			}
			visited[nb] = true
			queue = append(queue, nodeDepth{n: nb, depth: cur.depth + 1})
		}
	}
	return out, nil
}

// featuresOfClass walks in one direction from start and returns every reached
// feature whose class is in classIDs, ordered nearest-first (by hop distance,
// then by id for a stable tiebreak). The start node is never included.
func featuresOfClass(ctx context.Context, db bun.IDB, start FeatureRef, direction string, classCodes []string) ([]FeatureRef, error) {
	s, err := resolveNode(ctx, db, featureRef{FeatureClass: start.FeatureClass, FeatureID: start.FeatureID})
	if err != nil {
		return nil, err
	}
	classIDs, err := classIDsForCodes(ctx, db, classCodes)
	if err != nil {
		return nil, err
	}

	reached, err := reachableWithDepth(ctx, db, s, direction)
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
