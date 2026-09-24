package trace

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// Phase 62/63 -- the service / consumption dimension of a trace. Every
// trace-family walk already reaches a set of features; this file rolls up the
// load leaves among them (SERVICE_POINTs and PRIMARY METERs -- see isLoadLeaf)
// into "how much load, and how many customers, of what restoration priority,
// sits below here." It is the load-side counterpart to the topology a
// trace already returns: a downstream/feeder/section/isolation walk answers
// "what does this reach," summarizeService answers "what does that reaching
// COST if it goes dark."
//
// Deliberately attribute-only and query-light: one live GetFeature per reached
// SERVICE_POINT, through the same class-loader cache enrichNode already uses, so
// attaching a rollup to a trace costs nothing a barrier-respecting walk didn't
// already pay. No power-flow solve -- these are the planner's stored nameplate
// figures (demand_kw / power_factor / customers_served), the same ones OMS
// counts and powerflow seeds its injections from, NOT a computed per-node
// result. A client that wants solved load overlays it from a load-flow run.

const (
	// The two consumer-facing leaf classes this app models load on. Phase 62
	// started with SERVICE_POINT alone; Phase 63 ("the network ends at the
	// meter") made the METER a load-bearing leaf in its own right -- a metered
	// supply's load records live on its PRIMARY meter, not on a SERVICE_POINT
	// behind it. SERVICE_POINT now covers the UNMETERED leaf only. isLoadLeaf
	// tests membership; the per-class rules (a meter only counts when PRIMARY)
	// live in meterCountsAsLoad below.
	servicePointClass = "SERVICE_POINT"
	meterClass        = "METER"

	// meterRoleField / meterRolePrimary -- the METER_ROLE domain from
	// seed/meter_load_leaf.sql. A meter carries the supply's load ONLY when it
	// is PRIMARY; an unset role (every legacy/device meter) or CHECK contributes
	// nothing, which is what keeps a not-yet-migrated meter->SP chain (load on
	// the SP, meter empty) counting exactly once -- via the SP. See the seed
	// header's PRIMARY CONTRACT.
	meterRoleField   = "meter_role"
	meterRolePrimary = "PRIMARY"

	// The stored load attributes, identical on SERVICE_POINT and METER (Phase 63
	// gives the meter the same names, so these readers stay class-agnostic).
	// demand_kw/power_factor are the Phase 8 load-flow inputs
	// (seed/load_flow_fields.sql); customers_served is the OMS per-point customer
	// count (null counted as 1, matching OMS); load_priority is the Phase 62
	// restoration tier, is_critical the legacy SERVICE_POINT boolean it
	// supersedes (still read as a fallback during the transition).
	loadPriorityField = "load_priority"
	isCriticalField   = "is_critical"
	demandKWField     = "demand_kw"
	powerFactorField  = "power_factor"
	customersField    = "customers_served"

	// defaultPriority is where an unset load_priority lands -- Standard. Absence
	// reads as "ordinary load," never as critical, the same honest default the
	// old is_critical NULL carried.
	defaultPriority = "P4"

	// criticalMaxRank: a tier at or below this rank (P1, P2) counts as critical.
	// This is the derived replacement for the old is_critical boolean.
	criticalMaxRank = 2
)

// orderedPriorities is every tier best-first, so a rollup's ByPriority list is
// always the same four rows in the same order regardless of what the walk hit
// (a tier with nothing below reports zeroes rather than vanishing) -- a stable
// shape for the frontend's per-tier bars.
var orderedPriorities = []string{"P1", "P2", "P3", "P4"}

var priorityRank = map[string]int{"P1": 1, "P2": 2, "P3": 3, "P4": 4}

var priorityLabel = map[string]string{
	"P1": "Life support",
	"P2": "Essential",
	"P3": "Priority",
	"P4": "Standard",
}

// ServiceTotals is the additive core of a rollup -- a bag of running sums usable
// for the whole set, for the critical subset, and per tier. kVA is derived from
// each SP's own kW and power factor as it is added (kVA = kW / pf, pf defaulting
// to unity), so the total is the sum of per-SP apparent powers, not a re-derive
// off an averaged pf.
type ServiceTotals struct {
	ServicePoints int     `json:"service_points"`
	Customers     int     `json:"customers"`
	DemandKW      float64 `json:"demand_kw"`
	DemandKVA     float64 `json:"demand_kva"`
}

// ServiceTier is one restoration tier's totals plus its identity, for the
// ByPriority breakdown.
type ServiceTier struct {
	Priority string `json:"priority"`
	Label    string `json:"label"`
	ServiceTotals
}

// ServiceRollup is the consumption summary attached to a trace result: the whole
// set's totals, the critical subset (rank <= P2), and the per-tier breakdown.
// Exported because OMS reaches it directly (SummarizeService) to split its
// affected/de-energized counts by the same tiers a trace uses -- one definition
// of "critical load" across the whole app.
type ServiceRollup struct {
	ServiceTotals
	Critical   ServiceTotals `json:"critical"`
	ByPriority []ServiceTier `json:"by_priority"`
}

// add folds one SERVICE_POINT's figures into a totals bag.
func (t *ServiceTotals) add(customers int, kw, kva float64) {
	t.ServicePoints++
	t.Customers += customers
	t.DemandKW += kw
	t.DemandKVA += kva
}

// isLoadLeaf reports whether a class code is one this app models consumption on
// at all -- the cheap pre-filter (by class code, before any DB read) that lets
// collectService skip the poles/cables/switches a walk mostly reaches. Being a
// load-leaf CLASS is necessary but not sufficient: meterCountsAsLoad then vets
// the loaded feature (a meter must be PRIMARY).
func isLoadLeaf(code string) bool {
	return code == servicePointClass || code == meterClass
}

// meterCountsAsLoad applies the per-class rule once a load-leaf feature is
// loaded. A SERVICE_POINT always counts (the unmetered leaf). A METER counts
// only when meter_role = PRIMARY -- an unset role (legacy/device meters) or a
// CHECK meter contributes nothing, so the supply's load is counted exactly once
// and a not-yet-migrated meter->SP chain never double-counts. Any other class
// reaching here (it shouldn't, isLoadLeaf gates it) counts, matching the old
// SERVICE_POINT-only behaviour for safety.
func meterCountsAsLoad(code string, attrs map[string]any) bool {
	if code != meterClass {
		return true
	}
	role, _ := attrs[meterRoleField].(string)
	return role == meterRolePrimary
}

// resolveServicePriority reads a SERVICE_POINT's restoration tier, superseding
// the legacy is_critical: the load_priority field if set and valid, else the
// old boolean (true -> P2, the faithful lift the backfill uses), else Standard.
func resolveServicePriority(attrs map[string]any) string {
	if raw, ok := attrs[loadPriorityField].(string); ok {
		if _, valid := priorityRank[raw]; valid {
			return raw
		}
	}
	if crit, _ := attrs[isCriticalField].(bool); crit {
		return "P2"
	}
	return defaultPriority
}

// serviceCustomers reads customers_served, a null/absent value counting as one
// customer -- the same coalesce(customers_served, 1) OMS applies everywhere.
func serviceCustomers(attrs map[string]any) int {
	if n, ok := readServiceFloat(attrs, customersField); ok && n > 0 {
		return int(n)
	}
	return 1
}

// serviceApparentKVA turns a real-power demand into apparent power using the
// SP's own power factor (kVA = kW / pf). An unset or non-positive pf defaults to
// unity, so kVA == kW rather than dividing by zero -- the same unity fallback
// powerflow's resolveDemand uses for a demand with no power_factor.
func serviceApparentKVA(kw float64, attrs map[string]any) float64 {
	pf, ok := readServiceFloat(attrs, powerFactorField)
	if !ok || pf <= 0 || pf > 1 {
		return kw
	}
	return kw / pf
}

// readServiceFloat is this file's tolerant numeric reader for a nested
// attribute, mirroring powerflow's readFloatAttr (kept local for the same
// each-package-owns-its-own reason the trace engine stays decoupled from
// powerflow) but also accepting the integer shapes crud may hand back for a
// count column like customers_served.
func readServiceFloat(attrs map[string]any, name string) (float64, bool) {
	v, ok := attrs[name]
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

// ServicePoint is one SERVICE_POINT's own consumption line, for the dedicated
// service trace's per-point breakdown (the summary rollup answers "how much in
// total," this answers "which points, each how much"). Embeds featureRef so its
// JSON carries the same feature_class/feature_id a plain ref does, plus the
// point's tier, load, and derived critical flag.
type ServicePoint struct {
	featureRef
	Priority  string  `json:"priority"`
	Label     string  `json:"label"`
	Customers int     `json:"customers"`
	DemandKW  float64 `json:"demand_kw"`
	DemandKVA float64 `json:"demand_kva"`
	Critical  bool    `json:"critical"`
}

// collectService is the single pass behind both the rollup a trace attaches and
// the dedicated service trace's per-point breakdown: it reads each reached
// load leaf (a SERVICE_POINT, or a PRIMARY METER -- see isLoadLeaf /
// meterCountsAsLoad) once and folds it into the totals AND emits its own line.
// Skips non-load nodes, opted-out meters, and since-purged features (a dangling
// connectivity row), the same dead-end treatment enrichNode/checkBarrier give.
// One live GetFeature per load leaf, through the shared loader cache. Always
// returns a rollup with all four tiers present (zeroed if nothing hit them).
// points is nil-safe to ignore for callers that only want the rollup
// (summarizeService).
func collectService(ctx context.Context, db bun.IDB, nodes []node, classCodes map[string]string, loader *featureClassLoader) (*ServiceRollup, []ServicePoint, error) {
	byTier := make(map[string]*ServiceTotals, len(orderedPriorities))
	for _, p := range orderedPriorities {
		byTier[p] = &ServiceTotals{}
	}
	rollup := &ServiceRollup{}
	points := make([]ServicePoint, 0, 8)

	for _, n := range nodes {
		code := classCodes[n.classID]
		if !isLoadLeaf(code) {
			continue
		}
		loaded, err := loader.loadedFor(ctx, db, n.classID)
		if err != nil {
			return nil, nil, err
		}
		row, err := crud.GetFeature(ctx, db, loaded, n.featureID)
		if err != nil {
			if crud.IsNotFoundError(err) {
				continue
			}
			return nil, nil, err
		}
		attrs, _ := row["attributes"].(map[string]any)
		// A load-leaf CLASS that this particular feature opts out of (a CHECK or
		// role-unset meter) contributes nothing -- skip it AFTER the load, since
		// the role lives in the feature's attributes, not its class.
		if !meterCountsAsLoad(code, attrs) {
			continue
		}

		priority := resolveServicePriority(attrs)
		customers := serviceCustomers(attrs)
		kw, _ := readServiceFloat(attrs, demandKWField)
		kva := serviceApparentKVA(kw, attrs)
		critical := priorityRank[priority] <= criticalMaxRank

		rollup.ServiceTotals.add(customers, kw, kva)
		byTier[priority].add(customers, kw, kva)
		if critical {
			rollup.Critical.add(customers, kw, kva)
		}
		points = append(points, ServicePoint{
			featureRef: featureRef{FeatureClass: code, FeatureID: n.featureID},
			Priority:   priority,
			Label:      priorityLabel[priority],
			Customers:  customers,
			DemandKW:   kw,
			DemandKVA:  kva,
			Critical:   critical,
		})
	}

	rollup.ByPriority = make([]ServiceTier, 0, len(orderedPriorities))
	for _, p := range orderedPriorities {
		rollup.ByPriority = append(rollup.ByPriority, ServiceTier{
			Priority:      p,
			Label:         priorityLabel[p],
			ServiceTotals: *byTier[p],
		})
	}
	return rollup, points, nil
}

// summarizeService rolls the SERVICE_POINTs among a set of already-walked nodes
// into a ServiceRollup, discarding the per-point breakdown -- the seam every
// ordinary trace (run/feeder/section/isolation/supply) uses to attach its
// consumption summary. See collectService.
func summarizeService(ctx context.Context, db bun.IDB, nodes []node, classCodes map[string]string, loader *featureClassLoader) (*ServiceRollup, error) {
	rollup, _, err := collectService(ctx, db, nodes, classCodes, loader)
	return rollup, err
}

// serviceRequest is the POST /trace/service body. TraceConfiguration must name a
// DOWNSTREAM-direction row -- its barrier settings govern the walk toward load,
// same reasoning feederRequest/isolationRequest use.
type serviceRequest struct {
	TraceConfiguration string     `json:"trace_configuration"`
	Start              featureRef `json:"start"`
}

// serviceResponse is the whole result of a dedicated service trace: the total
// consumption rollup below Start plus the per-SERVICE_POINT breakdown behind it.
// Distinct from the Service rollup that rides along on every other trace in that
// this trace's PURPOSE is the load answer -- it walks downstream to the load and
// returns the individual points, where an ordinary trace returns topology with
// the rollup as a summary extra.
type serviceResponse struct {
	Start         featureRef     `json:"start"`
	Service       *ServiceRollup `json:"service"`
	ServicePoints []ServicePoint `json:"service_points"`
}

// serviceTrace answers "what consumption sits below this feature, and of what
// restoration priority" -- a DOWNSTREAM barrier-respecting walk from Start whose
// SERVICE_POINTs are rolled up by load, customers, and tier, and also returned
// individually. Composes the ordinary downstream walk (same as feederTrace's
// downstream leg) rather than a new BFS mode; the load summary is the point, not
// a side extra.
func serviceTrace(ctx context.Context, db bun.IDB, req serviceRequest) (*serviceResponse, error) {
	if req.TraceConfiguration == "" {
		return nil, invalid("trace_configuration is required")
	}
	if req.Start.FeatureClass == "" || req.Start.FeatureID == "" {
		return nil, invalid("start.feature_class and start.feature_id are required")
	}

	tc, err := metadata.LoadTraceConfiguration(ctx, db, req.TraceConfiguration)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("trace configuration %q does not exist", req.TraceConfiguration)
		}
		return nil, err
	}
	direction, err := metadata.DomainValueCode(ctx, db, tc.DirectionValueID)
	if err != nil {
		return nil, err
	}
	if direction != "DOWNSTREAM" {
		return nil, invalid("service trace requires a DOWNSTREAM trace_configuration, %q is %s", req.TraceConfiguration, direction)
	}

	start, err := resolveNode(ctx, db, req.Start)
	if err != nil {
		return nil, err
	}

	barriers, err := resolveBarriers(ctx, db, tc.ID, tc.StopAtOpenDevices)
	if err != nil {
		return nil, err
	}
	loader := newFeatureClassLoader()

	visited, _, _, _, _, err := bfs(ctx, db, start, "DOWNSTREAM", barriers, loader, nil, nil, false, nil)
	if err != nil {
		return nil, err
	}

	classCodes, err := nodeClassCodes(ctx, db, visited)
	if err != nil {
		return nil, err
	}

	rollup, points, err := collectService(ctx, db, visited, classCodes, loader)
	if err != nil {
		return nil, err
	}

	return &serviceResponse{
		Start:         req.Start,
		Service:       rollup,
		ServicePoints: points,
	}, nil
}

// DownstreamLoadLeaves returns each COUNTING load leaf (a SERVICE_POINT, or a
// PRIMARY METER -- isLoadLeaf/meterCountsAsLoad) topologically downstream of
// device, as its own consumption line (class+id, customers, tier, load), plus
// the rollup over them. Barrier-free and topological -- it ignores live switch
// state, the same basis DownstreamFeaturesOfClass uses -- so it answers "every
// supply electrically below this device," the conservative set OMS freezes as
// interruptions and counts as affected. device itself is never included; a
// non-load leaf (or an opted-out CHECK/role-unset meter) contributes nothing.
//
// One place for OMS to get all three things it needs from a de-energized cone --
// the per-supply freeze rows, the map geometry refs, and the affected counts --
// through the exact same predicate a trace rollup uses, so an incident's
// "affected customers" can never drift from a downstream trace's.
func DownstreamLoadLeaves(ctx context.Context, db bun.IDB, device FeatureRef) ([]ServicePoint, *ServiceRollup, error) {
	s, err := resolveNode(ctx, db, featureRef{FeatureClass: device.FeatureClass, FeatureID: device.FeatureID})
	if err != nil {
		return nil, nil, err
	}
	reached, err := reachableWithDepth(ctx, db, s, "DOWNSTREAM")
	if err != nil {
		return nil, nil, err
	}
	nodes := make([]node, 0, len(reached))
	for _, nd := range reached {
		if nd.n == s {
			continue
		}
		nodes = append(nodes, nd.n)
	}
	classCodes, err := nodeClassCodes(ctx, db, nodes)
	if err != nil {
		return nil, nil, err
	}
	rollup, points, err := collectService(ctx, db, nodes, classCodes, newFeatureClassLoader())
	if err != nil {
		return nil, nil, err
	}
	return points, rollup, nil
}

// SummarizeService is the exported entry point OMS uses to roll a set of feature
// refs (e.g. a DeEnergizedCone or an affected-area list) up by the same
// restoration tiers a trace uses. It resolves each ref to a node, skipping any
// that no longer exist (a purged feature counts as nothing, not an error), then
// runs the same summary. Non-load-leaf refs (and opted-out meters) are ignored,
// so a caller can pass a mixed set without pre-filtering.
func SummarizeService(ctx context.Context, db bun.IDB, refs []FeatureRef) (*ServiceRollup, error) {
	loader := newFeatureClassLoader()
	nodes := make([]node, 0, len(refs))
	for _, r := range refs {
		n, err := resolveNode(ctx, db, featureRef{FeatureClass: r.FeatureClass, FeatureID: r.FeatureID})
		if err != nil {
			// A ref to a since-purged/not-found or soft-deleted feature
			// contributes nothing, rather than failing the whole rollup -- the
			// same tolerance the internal walk gives a dangling connectivity row
			// (resolveNode reports the deleted case as a validation error).
			if isNotFoundError(err) || isValidationError(err) {
				continue
			}
			return nil, err
		}
		nodes = append(nodes, n)
	}
	classCodes, err := nodeClassCodes(ctx, db, nodes)
	if err != nil {
		return nil, err
	}
	return summarizeService(ctx, db, nodes, classCodes, loader)
}
