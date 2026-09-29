package trace

import (
	"context"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

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
		visited, _, _, _, _, _, err := bfs(ctx, db, start, "DOWNSTREAM", barriers, loader, nil, nil, false, false, nil)
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
