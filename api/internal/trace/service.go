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
// load leaves among them (every consumer-role class -- see isConsumerLeaf;
// SERVICE_POINT/PRIMARY-METER count as customers, the rest as load-only)
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
	// The two CUSTOMER leaf classes (isCustomerLeaf) -- the load leaves that count
	// as customers, distinct from the wider consumer-role set that counts as load.
	// Phase 62 started with SERVICE_POINT alone; Phase 63 ("the network ends at the
	// meter") made the METER a load-bearing leaf in its own right -- a metered
	// supply's load records live on its PRIMARY meter, not on a SERVICE_POINT
	// behind it. SERVICE_POINT now covers the UNMETERED customer leaf only. The
	// per-class meter rule (a meter only counts when PRIMARY) lives in
	// meterCountsAsLoad below; the load-leaf membership test is isConsumerLeaf.
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

// consumerRole is the functional_role (migration 0091, seed/functional_role_
// backfill.sql) that marks a class as a LOAD LEAF -- a sink where power is
// delivered out of the network. Phase 62/63 recognised a leaf by code
// (SERVICE_POINT + METER only); the rollup now recognises one by this metadata
// signal, so every consumer-role class (STREET_LIGHT, EV_CHARGING_STATION, the
// MV metering leaves, ...) counts the moment it is tagged a consumer -- no
// per-class code change. The CUSTOMER subset stays SERVICE_POINT + METER
// (isCustomerLeaf); the rest are load, not customers.
const consumerRole = "consumer"

// isConsumerLeaf reports whether a class's functional_role marks it a load leaf
// -- the metadata-driven replacement for the old hardcoded two-class check.
// Being a load leaf is necessary but not sufficient: meterCountsAsLoad then vets
// the loaded feature (a meter counts only when PRIMARY).
func isConsumerLeaf(role *string) bool {
	return role != nil && *role == consumerRole
}

// isCustomerLeaf reports whether a load leaf is an actual CUSTOMER supply -- so
// it contributes to the customer count -- rather than pure load. Only
// SERVICE_POINT and the (PRIMARY) METER are customers; STREET_LIGHT,
// EV_CHARGING_STATION and the MV metering leaves are load-only (0 customers),
// keeping "customers affected" honest. OMS never passes these other classes to
// SummarizeService (it pre-filters its cone to SERVICE_POINT/METER), so its
// customer and SAIDI figures are unaffected by the wider leaf set.
func isCustomerLeaf(code string) bool {
	return code == servicePointClass || code == meterClass
}

// meterCountsAsLoad applies the per-class rule once a load-leaf feature is
// loaded. A METER counts only when meter_role = PRIMARY -- an unset role
// (legacy/device meters) or a CHECK meter contributes nothing, so the supply's
// load is counted exactly once and a not-yet-migrated meter->SP chain never
// double-counts. Every other consumer leaf (SERVICE_POINT, STREET_LIGHT,
// EV_CHARGING_STATION, ...) always counts -- the PRIMARY gate is specific to the
// meter/service-point double-count, not load leaves in general.
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
// load leaf once and folds it into the totals AND emits its own line. Skips
// non-load nodes, opted-out meters, and since-purged features (a dangling
// connectivity row), the same dead-end treatment enrichNode/checkBarrier give.
// One live GetFeature per load leaf, through the shared loader cache. Always
// returns a rollup with all four tiers present (zeroed if nothing hit them).
// points is nil-safe to ignore for callers that only want the rollup
// (summarizeService).
//
// wide selects the LEAF SET. wide=true counts every consumer-role class
// (isConsumerLeaf) -- street lights, EV stations and the MV metering leaves join
// SERVICE_POINT/METER -- so the service/consumption trace shows unmetered load.
// wide=false is the CUSTOMER-only set (isCustomerLeaf: SERVICE_POINT + PRIMARY
// METER), the historic behaviour OMS relies on -- its DownstreamLoadLeaves
// (frozen supplies, affected customers) must not pick up non-customer load. In
// BOTH modes a non-customer leaf would count 0 customers anyway; wide is about
// whether such a leaf is counted (as load) at all.
func collectService(ctx context.Context, db bun.IDB, nodes []node, classCodes map[string]string, loader *featureClassLoader, wide bool) (*ServiceRollup, []ServicePoint, error) {
	byTier := make(map[string]*ServiceTotals, len(orderedPriorities))
	for _, p := range orderedPriorities {
		byTier[p] = &ServiceTotals{}
	}
	rollup := &ServiceRollup{}
	points := make([]ServicePoint, 0, 8)

	for _, n := range nodes {
		code := classCodes[n.classID]
		// Narrow (customer-only) mode keeps the cheap code pre-filter: skip a
		// non-customer class before any DB read, exactly as the old isLoadLeaf did.
		if !wide && !isCustomerLeaf(code) {
			continue
		}
		loaded, err := loader.loadedFor(ctx, db, n.classID)
		if err != nil {
			return nil, nil, err
		}
		// Wide mode counts any consumer-role class (migration 0091) -- the metadata
		// signal, so a newly-tagged consumer class counts with no code change. The
		// class loader is cached, and the expensive per-feature GetFeature runs only
		// past this point.
		if wide && !isConsumerLeaf(loaded.FeatureClass.FunctionalRole) {
			continue
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
		// Only SERVICE_POINT / PRIMARY METER are customer supplies; the other
		// consumer leaves (street lights, EV stations, MV metering) are load-only,
		// so they count 0 customers -- their load and restoration tier still roll up.
		customers := 0
		if isCustomerLeaf(code) {
			customers = serviceCustomers(attrs)
		}
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

// summarizeService rolls the load leaves among a set of already-walked nodes
// into a ServiceRollup, discarding the per-point breakdown -- the seam every
// ordinary trace (run/feeder/section/isolation/supply) uses to attach its
// consumption summary. Those traces pass wide=true (count every consumer-role
// leaf, so unmetered load shows); OMS's SummarizeService passes wide=false (the
// customer-only set). See collectService.
func summarizeService(ctx context.Context, db bun.IDB, nodes []node, classCodes map[string]string, loader *featureClassLoader, wide bool) (*ServiceRollup, error) {
	rollup, _, err := collectService(ctx, db, nodes, classCodes, loader, wide)
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
	// Truncated is true when the downstream walk hit the maxTraceHops cap -- the
	// consumption rollup is incomplete (under-counts), so the client should warn.
	Truncated bool `json:"truncated"`
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

	// Phase 58 spatial gate: refuse an out-of-scope start and roll up only
	// in-scope load leaves, exactly as runTrace does (see scope.go). A no-op
	// unless enforcement is on and the caller is scoped.
	scope, err := enforceScopeStart(ctx, db, loader, start, req.Start)
	if err != nil {
		return nil, err
	}

	visited, _, _, _, _, _, truncated, err := bfs(ctx, db, start, "DOWNSTREAM", barriers, loader, nil, nil, false, false, nil)
	if err != nil {
		return nil, err
	}

	// Drop out-of-scope features the walk passed through, so the rollup and its
	// per-point breakdown count only load within the user's territories.
	if scope.apply {
		if visited, err = scope.filter(ctx, db, visited, loader); err != nil {
			return nil, err
		}
	}

	classCodes, err := nodeClassCodes(ctx, db, visited)
	if err != nil {
		return nil, err
	}

	rollup, points, err := collectService(ctx, db, visited, classCodes, loader, true)
	if err != nil {
		return nil, err
	}

	return &serviceResponse{
		Start:         req.Start,
		Service:       rollup,
		ServicePoints: points,
		Truncated:     truncated,
	}, nil
}

// DownstreamLoadLeaves returns each COUNTING CUSTOMER leaf (a SERVICE_POINT, or a
// PRIMARY METER -- isCustomerLeaf/meterCountsAsLoad; it passes wide=false)
// topologically downstream of device, as its own consumption line (class+id,
// customers, tier, load), plus the rollup over them. OMS consumes this for its
// frozen supplies and affected-customer counts, so it stays the customer-only
// set -- street lights / EV / MV-metering load (counted by the service trace's
// wide rollup) must never inflate an outage's customer count.
// Barrier-free and topological -- it ignores live switch
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
	// OMS consumes a bare ([]ServicePoint, *ServiceRollup) with no result struct,
	// so there is nowhere to surface truncation -- discard it (truncates at
	// maxTraceHops as before).
	reached, _, err := reachableWithDepth(ctx, db, s, "DOWNSTREAM")
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
	rollup, points, err := collectService(ctx, db, nodes, classCodes, newFeatureClassLoader(), false)
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
	return summarizeService(ctx, db, nodes, classCodes, loader, false)
}
