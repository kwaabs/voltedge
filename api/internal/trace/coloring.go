package trace

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// EnergizedSet performs source-rooted NETWORK COLORING: it floods outward from every
// live source (functional_role='source') through conducting elements and returns the
// set of reached features, keyed by feature id. A feature is energized iff a path of
// conducting elements -- closed switchgear, in-service plant -- joins it to a source;
// anything not reached is de-energised.
//
// This is the correct, non-radial model. Because the flood simply follows closed
// connectivity in BOTH directions, a section BACK-FED through a closed normally-open
// tie IS reached (reported live) -- the meshed-network case the dead-cone model got
// wrong. An open device is REACHED (it has voltage on its live terminal) but does not
// conduct ONWARD, so the flood stops AT it, not through it; a source always conducts.
//
// Transformers need no special handling: the undirected flood passes through an
// intact transformer, so the LV side lights up iff the MV side is reached -- exactly
// the physical behaviour.
//
// ok=false (no error) when the network declares NO source: the caller then falls back
// to the dead-cone model, so a network without sources modelled isn't painted dead.
func EnergizedSet(ctx context.Context, db bun.IDB) (reached map[string]bool, ok bool, err error) {
	sources, err := sourceNodes(ctx, db)
	if err != nil {
		return nil, false, err
	}
	if len(sources) == 0 {
		return nil, false, nil
	}

	barriers, err := resolveOpenDeviceBarriers(ctx, db)
	if err != nil {
		return nil, false, err
	}
	loader := newFeatureClassLoader()

	started := time.Now()
	reached, barriersHit, err := colorFlood(ctx, db, sources, barriers, loader, true)
	if err != nil {
		return nil, false, err
	}
	slog.InfoContext(ctx, "energization coloring",
		"sources", len(sources), "energized", len(reached), "barriers_hit", barriersHit,
		"ms", time.Since(started).Milliseconds())
	return reached, true, nil
}

// colorFlood floods outward from sources over energized (lifecycle-permissive)
// connectivity and returns the reached feature-id set plus how many open devices the
// flood stopped at. When respectBarriers is false the open-device check is skipped
// (the as-wired reach, for diagnostics).
func colorFlood(ctx context.Context, db bun.IDB, sources []node, barriers map[string][]barrierCheck, loader *featureClassLoader, respectBarriers bool) (map[string]bool, int, error) {
	sourceSet := make(map[node]bool, len(sources))
	for _, s := range sources {
		sourceSet[s] = true
	}
	reached := make(map[string]bool)
	seen := make(map[node]bool, len(sources))
	queue := make([]node, 0, len(sources))
	enqueue := func(n node) {
		if seen[n] {
			return
		}
		seen[n] = true
		reached[n.featureID] = true
		queue = append(queue, n)
	}
	for _, s := range sources {
		enqueue(s)
	}

	barriersHit := 0
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		// An open device has voltage on its live terminal (so it IS reached) but does not
		// pass power to its far side -- don't expand it. Only classes that carry a barrier
		// rule can be barriers, so skip the per-node check for everything else. A source
		// always conducts.
		if respectBarriers && !sourceSet[n] {
			if _, hasRule := barriers[n.classID]; hasRule {
				isBarrier, berr := checkBarrier(ctx, db, n, barriers, loader)
				if berr != nil {
					return nil, 0, berr
				}
				if isBarrier {
					barriersHit++
					continue
				}
			}
		}
		neighbors, nerr := energizedNeighbors(ctx, db, n)
		if nerr != nil {
			return nil, 0, nerr
		}
		for _, m := range neighbors {
			enqueue(m)
		}
	}
	return reached, barriersHit, nil
}

// energizedNeighbors returns the undirected connectivity neighbours of n that CONDUCT
// for energisation purposes. Unlike the trace engine's neighborsOf (strict AS-OPERATED:
// an endpoint must be lifecycle_status IN ('IN_SERVICE','DERATED')), this treats a NULL
// / unset lifecycle and an absent registry row as in-service -- the column's own default
// intent -- and only EXCLUDES an endpoint that is explicitly out of the operating
// network: deleted, or PLANNED / UNDER_CONSTRUCTION / RETIRED / REMOVED. That stops a
// network with incomplete lifecycle data from colouring itself a mass outage, while a
// genuinely planned or retired asset still doesn't conduct.
func energizedNeighbors(ctx context.Context, db bun.IDB, n node) ([]node, error) {
	var rows []edgeRow
	const outOfNetwork = `NOT EXISTS (SELECT 1 FROM runtime.feature_registry r WHERE r.feature_id = %s AND (r.deleted_at IS NOT NULL OR r.lifecycle_status IN ('PLANNED','UNDER_CONSTRUCTION','RETIRED','REMOVED')))`
	if err := db.NewSelect().Model(&rows).
		Where("(from_feature_id = ? AND from_feature_class_id = ?) OR (to_feature_id = ? AND to_feature_class_id = ?)",
			n.featureID, n.classID, n.featureID, n.classID).
		Where(fmt.Sprintf(outOfNetwork, "feature_connectivity.from_feature_id")).
		Where(fmt.Sprintf(outOfNetwork, "feature_connectivity.to_feature_id")).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("trace: loading energized neighbors: %w", err)
	}
	out := make([]node, 0, len(rows))
	for _, r := range rows {
		from := node{classID: r.FromFeatureClassID, featureID: r.FromFeatureID}
		to := node{classID: r.ToFeatureClassID, featureID: r.ToFeatureID}
		if from == n {
			out = append(out, to)
		} else {
			out = append(out, from)
		}
	}
	return out, nil
}

// sourceNodes enumerates where power ENTERS the network -- the seeds of the
// energisation flood. The platform marks an infeed three ways (matching supply-path /
// powerflow, not just a class role), so all three are rooted here:
//
//  1. functional_role='source' classes (GENERATOR, migration 0091) -- embedded / DG.
//  2. A circuit's DESIGNATED source feature (gis.circuits.source_feature_*, migration
//     0062) -- the station/feeder-head breaker. "Which breaker is the source" is an
//     INSTANCE fact carried by the circuit, not a class role (a breaker is 'protective'
//     by class), so functional_role alone misses it -- which is exactly why a grid-fed
//     network coloured itself mostly dead.
//  3. A transformer carrying source_r_ohm (the Thevenin grid-infeed stand-in,
//     seed/load_flow_fields.sql) -- a SUB-SOURCE / GRID-SRC boundary.
//
// A source's own live/lifecycle state isn't checked yet (every modelled infeed is
// treated as live -- a later refinement can drop a retired/de-energised one).
func sourceNodes(ctx context.Context, db bun.IDB) ([]node, error) {
	classes, err := metadata.ListFeatureClasses(ctx, db)
	if err != nil {
		return nil, err
	}
	idByCode := make(map[string]string, len(classes))
	for _, fc := range classes {
		idByCode[fc.Code] = fc.ID
	}

	var out []node
	seen := make(map[node]bool)
	add := func(classID, id string) {
		if classID == "" || id == "" {
			return
		}
		n := node{classID: classID, featureID: id}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	// scanIDs runs an id-only query against a class table and adds each row as a source.
	scanIDs := func(classID, query string) error {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			add(classID, id)
		}
		return rows.Err()
	}

	for _, fc := range classes {
		if fc.PhysicalTable == nil {
			continue
		}
		// (1) functional_role='source' classes.
		if fc.FunctionalRole != nil && *fc.FunctionalRole == roleSource {
			if err := scanIDs(fc.ID, fmt.Sprintf("SELECT id::text FROM %s WHERE deleted_at IS NULL", *fc.PhysicalTable)); err != nil {
				return nil, fmt.Errorf("trace: scanning sources for %s: %w", fc.Code, err)
			}
		}
		// (3) grid-infeed transformers carrying source_r_ohm (only the transformer
		// classes have the column; probe information_schema so a class without it skips).
		if fc.FunctionalRole != nil && *fc.FunctionalRole == roleTransformer {
			has, herr := tableHasColumn(ctx, db, *fc.PhysicalTable, "source_r_ohm")
			if herr != nil {
				return nil, herr
			}
			if has {
				if err := scanIDs(fc.ID, fmt.Sprintf("SELECT id::text FROM %s WHERE deleted_at IS NULL AND source_r_ohm IS NOT NULL", *fc.PhysicalTable)); err != nil {
					return nil, fmt.Errorf("trace: scanning grid-infeed transformers for %s: %w", fc.Code, err)
				}
			}
		}
	}

	// (2) circuit-designated feeder heads. source_feature_class is the class CODE.
	var crows []struct {
		Class *string `bun:"source_feature_class"`
		ID    *string `bun:"source_feature_id"`
	}
	if err := db.NewRaw("SELECT source_feature_class, source_feature_id FROM gis.circuits WHERE source_feature_id IS NOT NULL").Scan(ctx, &crows); err != nil {
		return nil, fmt.Errorf("trace: reading circuit source features: %w", err)
	}
	for _, r := range crows {
		if r.Class != nil && r.ID != nil {
			add(idByCode[*r.Class], *r.ID)
		}
	}

	return out, nil
}

// tableHasColumn reports whether a qualified (schema.table) physical table has a given
// column, via information_schema -- used to probe optional columns (e.g. source_r_ohm,
// present only on transformer tables) before selecting them.
func tableHasColumn(ctx context.Context, db bun.IDB, qualifiedTable, column string) (bool, error) {
	schema, table := "public", qualifiedTable
	if i := strings.IndexByte(qualifiedTable, '.'); i >= 0 {
		schema, table = qualifiedTable[:i], qualifiedTable[i+1:]
	}
	var exists bool
	if err := db.NewRaw(
		"SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = ?)",
		schema, table, column,
	).Scan(ctx, &exists); err != nil {
		return false, fmt.Errorf("trace: probing %s.%s: %w", qualifiedTable, column, err)
	}
	return exists, nil
}
