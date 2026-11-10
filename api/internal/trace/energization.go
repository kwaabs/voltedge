package trace

import (
	"context"
	"fmt"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// maxOpenDevices caps how many barrier-active (open) devices the network
// energisation scan will union cones from -- a backstop against a pathological
// network with thousands of open switches. Real networks hold few genuinely-open
// devices; this only bounds the work, it doesn't normally bite.
const maxOpenDevices = 2000

// NetworkDeEnergized returns the subset of refs that are currently de-energised:
// below some open device with no remaining supply. It is the whole-network
// counterpart to DeEnergizedCone (one device -> its dead cone) -- it unions the
// dead cones of EVERY currently barrier-active device (device_state=OPEN /
// operational_state ISOLATED|RETIRED, the same live barriers the downstream trace
// honours), then reports which of the caller's features fall in that union. A
// feature not in the union is energised. The input set (a viewport's features, a
// diagram's placements) bounds the output, so the result only ever describes
// features the caller already holds.
//
// Limitation (inherited from DeEnergizedCone): a cone actually back-fed through a
// closed normally-open tie is still reported de-energised -- correct for a radial
// network, conservative for a meshed one. A source-rooted walk would subtract the
// back-fed part; that's a later refinement.
func NetworkDeEnergized(ctx context.Context, db bun.IDB, refs []FeatureRef) ([]FeatureRef, error) {
	if len(refs) == 0 {
		return []FeatureRef{}, nil
	}

	// 1. Fresh cache -> cheap set membership, no walk.
	if out, fresh, err := cachedDeEnergized(ctx, db, refs); err == nil && fresh {
		return out, nil
	}
	// 2. Stale / never computed -> recolor the network and store it, then read the cache.
	if err := RecolorNetwork(ctx, db); err == nil {
		if out, fresh, err := cachedDeEnergized(ctx, db, refs); err == nil && fresh {
			return out, nil
		}
	}
	// 3. Last resort (a cache write failed) -> compute live without storing, so a read
	// never hard-fails on a cache problem.
	return computeDeEnergizedLive(ctx, db, refs)
}

// computeDeEnergizedLive classifies refs by a fresh coloring without touching the
// cache: source-rooted when sources are modelled (correct for meshed / back-fed
// networks), else the dead-cone fallback. The on-demand path behind the cache.
func computeDeEnergizedLive(ctx context.Context, db bun.IDB, refs []FeatureRef) ([]FeatureRef, error) {
	energized, ok, err := EnergizedSet(ctx, db)
	if err != nil {
		return nil, err
	}
	if ok {
		out := make([]FeatureRef, 0)
		for _, r := range refs {
			if !energized[r.FeatureID] {
				out = append(out, r)
			}
		}
		return out, nil
	}
	return deEnergizedByOpenCones(ctx, db, refs)
}

// deEnergizedByOpenCones is the original, radial energisation model: union the
// downstream dead cone of every currently-open device and report which of refs fall
// in it. Kept as the fallback for a network that declares no source (EnergizedSet
// can't root a flood without one). Conservative on a meshed network -- a cone back-fed
// through a closed tie is still reported dead -- which is exactly why the source-rooted
// path above supersedes it once sources are modelled.
func deEnergizedByOpenCones(ctx context.Context, db bun.IDB, refs []FeatureRef) ([]FeatureRef, error) {
	barriers, err := resolveDownstreamBarriers(ctx, db)
	if err != nil {
		return nil, err
	}
	opens, err := openDevices(ctx, db, barriers)
	if err != nil {
		return nil, err
	}

	dead := map[string]bool{}
	for _, dev := range opens {
		cone, err := DeEnergizedCone(ctx, db, dev)
		if err != nil {
			return nil, err
		}
		for _, c := range cone {
			dead[c.FeatureClass+"|"+c.FeatureID] = true
		}
	}

	out := make([]FeatureRef, 0)
	for _, r := range refs {
		if dead[r.FeatureClass+"|"+r.FeatureID] {
			out = append(out, r)
		}
	}
	return out, nil
}

// openDevices enumerates every feature currently matching a downstream barrier --
// the roots of the de-energised cones. For each class carrying barrier rules it
// queries the class's own table for rows whose barrier field holds a halt value
// (device_state=OPEN, operational_state=ISOLATED/RETIRED, ...). Field names come
// from trace_barrier_rules (trusted metadata, never request input); halt codes
// are bound.
func openDevices(ctx context.Context, db bun.IDB, barriers map[string][]barrierCheck) ([]FeatureRef, error) {
	if len(barriers) == 0 {
		return nil, nil
	}
	classes, err := metadata.ListFeatureClasses(ctx, db)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]metadata.FeatureClass, len(classes))
	for _, c := range classes {
		byID[c.ID] = c
	}

	var out []FeatureRef
	for classID, checks := range barriers {
		fc, ok := byID[classID]
		if !ok || fc.PhysicalTable == nil {
			continue
		}
		conds := make([]string, 0, len(checks))
		args := make([]any, 0, len(checks))
		for _, ck := range checks {
			if ck.fieldName == "" || ck.haltsOnCode == "" {
				continue
			}
			conds = append(conds, quoteIdent(ck.fieldName)+" = ?")
			args = append(args, ck.haltsOnCode)
		}
		if len(conds) == 0 {
			continue
		}
		query := fmt.Sprintf(
			"SELECT id::text FROM %s WHERE deleted_at IS NULL AND (%s) LIMIT %d",
			*fc.PhysicalTable, strings.Join(conds, " OR "), maxOpenDevices+1,
		)
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("trace: scanning open devices for %s: %w", fc.Code, err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, fmt.Errorf("trace: reading open devices for %s: %w", fc.Code, err)
			}
			out = append(out, FeatureRef{FeatureClass: fc.Code, FeatureID: id})
		}
		rowsErr := rows.Err()
		rows.Close()
		if rowsErr != nil {
			return nil, fmt.Errorf("trace: reading open devices for %s: %w", fc.Code, rowsErr)
		}
	}
	return out, nil
}

// quoteIdent double-quotes a SQL identifier (only the trusted barrier field names
// pass through here, never request input).
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// DeEnergizedCone returns every feature currently cut off from supply below
// `device`, reading LIVE device_state / operational_state as barriers -- the
// network's real de-energized extent as it stands right now.
//
// It is the live counterpart to Isolate. Isolate answers "what WOULD
// de-energize if I opened this device," deliberately starting past the device
// so it never touches the device's own live state; DeEnergizedCone answers
// "what IS de-energized," and is meant to be called after a switching step has
// actually opened the device (oms.SetStepExecuted flips device_state=OPEN).
// Both walk a barrier-respecting downstream trace from each of the device's
// downstream neighbours, so any OTHER open device below -- a restoration or
// back-feed step already executed -- terminates the cone. That is the whole
// point of tying switching to network state: executing the restore steps
// visibly shrinks this set until it is empty.
//
// Barriers are resolved config-free (see resolveDownstreamBarriers), matching
// the OMS engine's existing DownstreamFeaturesOfClass in taking no
// trace_configuration -- an incident names a device, not a trace profile. The
// device itself is never included. Like the isolate primitive it composes,
// this reports the conservative directed-downstream extent; a normally-open
// tie that is actually closed and back-feeding part of the cone is not
// subtracted here (Isolate surfaces those as PossibleBackfeedPaths).
func DeEnergizedCone(ctx context.Context, db bun.IDB, device FeatureRef) ([]FeatureRef, error) {
	dev, err := resolveNode(ctx, db, featureRef{FeatureClass: device.FeatureClass, FeatureID: device.FeatureID})
	if err != nil {
		return nil, err
	}

	barriers, err := resolveDownstreamBarriers(ctx, db)
	if err != nil {
		return nil, err
	}
	loader := newFeatureClassLoader()

	downstreamNeighbors, err := neighborsOf(ctx, db, dev, "DOWNSTREAM")
	if err != nil {
		return nil, err
	}

	seen := map[node]bool{}
	ordered := make([]node, 0, 16)
	for _, start := range downstreamNeighbors {
		// DeEnergizedCone returns a bare []FeatureRef to OMS with no result struct,
		// so truncation has nowhere to surface -- discard it (truncates at
		// maxTraceHops as before).
		visited, _, _, _, _, _, _, err := bfs(ctx, db, start, "DOWNSTREAM", barriers, loader, nil, nil, false, false, nil)
		if err != nil {
			return nil, err
		}
		for _, v := range visited {
			if seen[v] {
				continue
			}
			seen[v] = true
			ordered = append(ordered, v)
		}
	}

	classCodes, err := nodeClassCodes(ctx, db, ordered)
	if err != nil {
		return nil, err
	}
	return toExportedRefs(toFeatureRefs(ordered, classCodes)), nil
}

// resolveDownstreamBarriers unions the barrier conditions of every DOWNSTREAM
// trace configuration that stops at open devices, giving the "standard"
// live-state barrier set (device_state=OPEN, operational_state ISOLATED/
// RETIRED, ...) without a caller having to name one specific configuration.
// The barrier rules are the same across downstream configs in practice, and
// checkBarrier already OR-combines every rule on a class, so the union is
// exactly the effective barrier a config-driven downstream walk would apply.
// An empty result (no configs, or none stopping at open devices) degrades to a
// barrier-free downstream walk -- the same conservative answer the OMS engine
// gave before live state was wired in.
func resolveDownstreamBarriers(ctx context.Context, db bun.IDB) (map[string][]barrierCheck, error) {
	configs, err := metadata.ListTraceConfigurations(ctx, db)
	if err != nil {
		return nil, err
	}

	out := map[string][]barrierCheck{}
	seen := map[string]bool{}
	for _, c := range configs {
		if !c.StopAtOpenDevices {
			continue
		}
		dir, err := metadata.DomainValueCode(ctx, db, c.DirectionValueID)
		if err != nil {
			return nil, err
		}
		if dir != "DOWNSTREAM" {
			continue
		}
		byClass, err := resolveBarriers(ctx, db, c.ID, true)
		if err != nil {
			return nil, err
		}
		for classID, checks := range byClass {
			for _, ck := range checks {
				key := classID + "|" + ck.fieldName + "|" + ck.haltsOnCode
				if seen[key] {
					continue
				}
				seen[key] = true
				out[classID] = append(out[classID], ck)
			}
		}
	}
	return out, nil
}

// resolveOpenDeviceBarriers is resolveDownstreamBarriers without the DOWNSTREAM
// filter: it unions the "stop at an open device" barrier rules across EVERY trace
// configuration that stops at open devices, regardless of the config's direction --
// an open switch is open no matter which way a walk crosses it. For an UPSTREAM walk
// that wants the live/energized view (the supply-path RespectSwitchState mode), so a
// ring's normally-open point halts the trace to the one energized direction. Empty
// (a barrier-free walk) when no configuration stops at open devices.
func resolveOpenDeviceBarriers(ctx context.Context, db bun.IDB) (map[string][]barrierCheck, error) {
	configs, err := metadata.ListTraceConfigurations(ctx, db)
	if err != nil {
		return nil, err
	}

	out := map[string][]barrierCheck{}
	seen := map[string]bool{}
	for _, c := range configs {
		if !c.StopAtOpenDevices {
			continue
		}
		byClass, err := resolveBarriers(ctx, db, c.ID, true)
		if err != nil {
			return nil, err
		}
		for classID, checks := range byClass {
			for _, ck := range checks {
				key := classID + "|" + ck.fieldName + "|" + ck.haltsOnCode
				if seen[key] {
					continue
				}
				seen[key] = true
				out[classID] = append(out[classID], ck)
			}
		}
	}
	return out, nil
}
