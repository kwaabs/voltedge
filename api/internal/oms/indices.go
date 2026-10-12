package oms

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// Phase 49 Slice 2 -- reliability indices. The regulator-facing summary of how
// reliably the network kept customers supplied, computed over the frozen
// oms.customer_interruptions fact table (materialised on confirm; see
// store.ConfirmIncident). Everything here is READ-ONLY analytics over that
// table -- it never touches the live network.
//
// The four standard indices (IEEE 1366), all per-customer over an analysis
// window [From, To):
//
//	SAIFI  System Average Interruption Frequency  = customer interruptions / N
//	SAIDI  System Average Interruption Duration   = customer-minutes / N   (minutes)
//	CAIDI  Customer Average Interruption Duration = SAIDI / SAIFI          (minutes)
//	ENS    Energy Not Supplied                    = customer-hours * assumed kW  (kWh)
//
// where N is the total customers served on the system (Σ customers_served over
// live service points, a NULL counted as 1). ENS is flagged estimated: without
// a per-customer load model we multiply interrupted customer-hours by a single
// assumed average demand (AvgLoadKW, overridable) -- a later slice can swap in
// measured load. MAIFI (momentary interruptions) is deferred: this phase only
// records sustained outages.
//
// Window handling clips each interruption to [From, To): an outage that began
// before the window but is still open contributes only its in-window minutes to
// SAIDI, and a still-open interruption is counted up to To. An interruption
// counts toward SAIFI (frequency) only if it BEGAN inside the window, so a
// carried-over outage inflates duration but not frequency. Cancelled incidents'
// interruptions are excluded, and so are PLANNED outages -- SAIDI/SAIFI/CAIDI
// are unplanned-only by IEEE 1366 convention (a planned outage is recorded in
// the fact table but never counted here).

// DefaultAvgLoadKW is the assumed average demand per customer used to estimate
// ENS when no measured load is available. 1 kW keeps ENS numerically equal to
// interrupted customer-hours, so the assumption is transparent; override via the
// avg_load_kw query param.
const DefaultAvgLoadKW = 1.0

// ReliabilityIndices is the computed summary plus the raw aggregates and the
// assumptions behind it, so the caller can see exactly what went into each
// index (and recompute with a different load figure client-side if wanted).
type ReliabilityIndices struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`

	// CircuitID echoes the circuit the report was scoped to (both the customer
	// denominator and the interrupted set are restricted to service points in
	// this circuit); nil for a whole-system report.
	CircuitID *string `json:"circuit_id,omitempty"`

	// Denominator and raw numerators.
	TotalCustomers       int     `json:"total_customers"`       // N: customers served on the live system
	CustomersInterrupted int     `json:"customers_interrupted"` // Σ customers over interruptions that began in-window (SAIFI numerator)
	InterruptionEvents   int     `json:"interruption_events"`   // count of interruptions that began in-window
	CustomerMinutes      float64 `json:"customer_minutes"`      // Σ customers × in-window minutes (SAIDI numerator)
	OngoingInterruptions int     `json:"ongoing_interruptions"` // interruptions still open at To

	// The four indices.
	SAIFI float64 `json:"saifi"` // interruptions per customer
	SAIDI float64 `json:"saidi"` // customer-minutes per customer
	CAIDI float64 `json:"caidi"` // minutes per interruption (0 when no interruptions)
	ENS   float64 `json:"ens_kwh"`

	// ENS assumption, echoed so the estimate is auditable.
	ENSEstimated bool    `json:"ens_estimated"`
	AvgLoadKW    float64 `json:"avg_load_kw"`

	// ByFaultCause decomposes SAIDI/SAIFI into per-cause contributions using the
	// fault event linked to each interruption's incident -- so the operator can
	// see WHAT drove the reliability (equipment failure, lightning, vegetation,
	// ...), the "fault-caused reliability" rollup. Every in-window interruption
	// lands in exactly one bucket (its incident's fault cause, an uncategorised
	// fault, or "no fault linked"), so the buckets' SAIDI/SAIFI sum back to the
	// system figures above -- a built-in consistency check. Nil when the faults
	// module is not installed (faults.fault_events absent); an empty slice means
	// no interruptions in the window. See FaultBreakdownAvailable.
	ByFaultCause            []FaultCauseContribution `json:"by_fault_cause,omitempty"`
	FaultBreakdownAvailable bool                     `json:"fault_breakdown_available"`

	// IEEE 1366 major-event-day (MED) normalisation (the 2.5-beta method, med.go).
	// SAIDI/SAIFI above are RAW -- a single storm can dominate them. These report the
	// threshold T_MED (in daily-SAIDI units), the count of days above it, and the
	// SAIDI/SAIFI with those major event days excluded -- the day-to-day reliability
	// a regulator compares year-on-year. MEDApplicable is false when the window holds
	// too few outage days to define the threshold; the normalised figures then equal
	// the raw ones (nothing excluded). See med.go's HONEST LIMITS for the reference-
	// period and multi-day-attribution simplifications.
	MEDThresholdSAIDI float64 `json:"med_threshold_saidi"`
	MajorEventDays    int     `json:"major_event_days"`
	SAIDINormalized   float64 `json:"saidi_normalized"`
	SAIFINormalized   float64 `json:"saifi_normalized"`
	MEDApplicable     bool    `json:"med_applicable"`
	MEDNote           string  `json:"med_note,omitempty"`
}

// FaultCauseContribution is one fault-cause bucket's share of the window's
// unreliability, over the SAME customer denominator N as the system indices so
// the buckets sum to the totals. Cause is the linked fault's recorded cause;
// nil when the fault carries no cause OR (FaultLinked=false) when the incident
// had no fault event at all -- the two are distinguished by FaultLinked, not by
// Cause, so a genuinely uncategorised fault and an unattributed outage stay
// separate.
type FaultCauseContribution struct {
	Cause       *string `json:"cause"`
	FaultLinked bool    `json:"fault_linked"`

	CustomersInterrupted int     `json:"customers_interrupted"`
	InterruptionEvents   int     `json:"interruption_events"`
	CustomerMinutes      float64 `json:"customer_minutes"`

	SAIFI float64 `json:"saifi"`
	SAIDI float64 `json:"saidi"`
}

// IndicesParams selects the analysis window and the ENS load assumption. Zero
// values are filled with defaults by ReliabilityIndicesReport (To = now, From =
// To - 12 months, AvgLoadKW = DefaultAvgLoadKW).
type IndicesParams struct {
	From      *time.Time
	To        *time.Time
	AvgLoadKW *float64
	// CircuitID, when set to a non-empty uuid, scopes the whole report to the
	// service points in that circuit -- both the interrupted set (numerators)
	// and the customer denominator -- so per-feeder reliability is consistent
	// on both sides of each index. Requires SERVICE_POINT to carry circuit_id
	// (Phase 37 Slice 2 circuit-membership seed + a recompile).
	CircuitID *string
}

// ReliabilityIndicesReport computes the system reliability indices over the
// window in p. It runs two reads -- the interruption aggregates (window-clipped)
// and the system customer denominator -- then derives the four indices in Go so
// the division-by-zero guards are explicit.
func ReliabilityIndicesReport(ctx context.Context, db bun.IDB, p IndicesParams) (*ReliabilityIndices, error) {
	to := time.Now()
	if p.To != nil {
		to = *p.To
	}
	from := to.AddDate(-1, 0, 0)
	if p.From != nil {
		from = *p.From
	}
	if !from.Before(to) {
		return nil, invalidf("indices window is empty: from (%s) must be before to (%s)", from.Format(time.RFC3339), to.Format(time.RFC3339))
	}

	avgLoadKW := DefaultAvgLoadKW
	if p.AvgLoadKW != nil {
		avgLoadKW = *p.AvgLoadKW
	}
	if avgLoadKW < 0 {
		return nil, invalidf("avg_load_kw must not be negative, got %v", avgLoadKW)
	}

	// Optional per-circuit scope. When a circuit is given, both the interrupted
	// set (via the SP the interruption is on) and the customer denominator are
	// restricted to service points in that circuit, so the two sides of each
	// index share the same membership definition. SERVICE_POINT carries
	// circuit_id only after the Phase 37 Slice 2 circuit-membership seed +
	// recompile; if the column is absent we fail with a clean 400 rather than a
	// raw 42703, since that is an apply-order gap, not a caller error in intent.
	var circuitID string
	scoped := false
	if p.CircuitID != nil {
		circuitID = strings.TrimSpace(*p.CircuitID)
		scoped = circuitID != ""
	}
	if scoped {
		var hasCol bool
		const colQuery = `SELECT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = 'gis' AND table_name = 'service_point' AND column_name = 'circuit_id'
)`
		if err := db.NewRaw(colQuery).Scan(ctx, &hasCol); err != nil {
			return nil, fmt.Errorf("oms: checking service_point circuit_id column: %w", err)
		}
		if !hasCol {
			return nil, invalidf("per-circuit indices need gis.service_point.circuit_id -- apply the Phase 37 Slice 2 circuit-membership seed and recompile SERVICE_POINT first")
		}
	}

	// Circuit-scoping fragments, empty for a whole-system report. The SP join in
	// the aggregate ties each interruption to its service point's circuit; its
	// placeholder appears AFTER the params CTE's two, so the args are ordered
	// [from, to, circuitID].
	aggCircuitJoin := ""
	denomCircuitFilter := ""
	aggArgs := []any{from, to}
	denomArgs := []any{}
	if scoped {
		aggCircuitJoin = "JOIN gis.service_point sp ON sp.id = ci.service_point_id AND sp.deleted_at IS NULL AND sp.circuit_id = ?::uuid"
		denomCircuitFilter = "AND circuit_id = ?::uuid"
		aggArgs = append(aggArgs, circuitID)
		denomArgs = append(denomArgs, circuitID)
	}

	// Window-clipped interruption aggregates. A params CTE names the window once
	// so the clip/overlap/frequency expressions stay readable.
	aggQuery := fmt.Sprintf(`
WITH params AS (
    SELECT ?::timestamptz AS win_start, ?::timestamptz AS win_end
),
clipped AS (
    SELECT
        ci.customers,
        ci.interrupted_at,
        GREATEST(ci.interrupted_at, p.win_start) AS eff_start,
        LEAST(COALESCE(ci.restored_at, p.win_end), p.win_end) AS eff_end,
        (ci.restored_at IS NULL) AS ongoing,
        p.win_start,
        p.win_end
    FROM oms.customer_interruptions ci
    JOIN oms.incidents inc ON inc.id = ci.incident_id
    %s
    CROSS JOIN params p
    WHERE inc.status <> 'cancelled'
      -- SAIDI/SAIFI/CAIDI are UNPLANNED-only by IEEE 1366 convention: a planned
      -- outage's interruptions are recorded but excluded from these indices.
      AND inc.outage_type = 'unplanned'
      AND ci.interrupted_at < p.win_end
      AND COALESCE(ci.restored_at, p.win_end) > p.win_start
)
SELECT
    COALESCE(SUM(customers) FILTER (WHERE interrupted_at >= win_start AND interrupted_at < win_end), 0) AS customers_interrupted,
    COALESCE(COUNT(*)       FILTER (WHERE interrupted_at >= win_start AND interrupted_at < win_end), 0) AS interruption_events,
    COALESCE(SUM(customers * GREATEST(0, EXTRACT(EPOCH FROM (eff_end - eff_start)) / 60.0)) FILTER (WHERE eff_end > eff_start), 0) AS customer_minutes,
    COALESCE(COUNT(*) FILTER (WHERE ongoing AND eff_end > eff_start), 0) AS ongoing_interruptions
FROM clipped`, aggCircuitJoin)

	var agg struct {
		CustomersInterrupted int     `bun:"customers_interrupted"`
		InterruptionEvents   int     `bun:"interruption_events"`
		CustomerMinutes      float64 `bun:"customer_minutes"`
		OngoingInterruptions int     `bun:"ongoing_interruptions"`
	}
	if err := db.NewRaw(aggQuery, aggArgs...).Scan(ctx, &agg); err != nil {
		return nil, fmt.Errorf("oms: computing interruption aggregates: %w", err)
	}

	// Denominator: total customers served on the live system (or in the circuit).
	// Phase 63: metered customers now live on PRIMARY meters, so a WHOLE-SYSTEM
	// denominator sums gis.service_point AND gis.meter(role=PRIMARY) -- matching
	// the numerator, whose frozen ci.customers already includes meter
	// interruptions. A PER-CIRCUIT report stays SP-only on BOTH sides: meters
	// carry no circuit_id yet, so the scoped numerator's SP-join drops meter rows
	// and the denominator must exclude them too, keeping the scope internally
	// consistent (a documented limitation until meters carry circuit membership).
	// The meter term is probed fail-soft, like the circuit_id guard above, so a
	// deployment that has not applied seed/meter_load_leaf.sql still reports on
	// service points alone rather than 500-ing on a missing gis.meter.meter_role.
	var totalCustomers int
	denomQuery := fmt.Sprintf(
		`SELECT COALESCE(SUM(COALESCE(customers_served, 1)), 0) FROM gis.service_point WHERE deleted_at IS NULL %s`,
		denomCircuitFilter,
	)
	if !scoped {
		hasMeterLoad, err := meterLoadAvailable(ctx, db)
		if err != nil {
			return nil, err
		}
		if hasMeterLoad {
			denomQuery = `SELECT
    (SELECT COALESCE(SUM(COALESCE(customers_served, 1)), 0) FROM gis.service_point WHERE deleted_at IS NULL)
  + (SELECT COALESCE(SUM(COALESCE(customers_served, 1)), 0) FROM gis.meter WHERE deleted_at IS NULL AND meter_role = 'PRIMARY')`
		}
	}
	if err := db.NewRaw(denomQuery, denomArgs...).Scan(ctx, &totalCustomers); err != nil {
		return nil, fmt.Errorf("oms: computing customer denominator: %w", err)
	}

	out := &ReliabilityIndices{
		From:                 from,
		To:                   to,
		TotalCustomers:       totalCustomers,
		CustomersInterrupted: agg.CustomersInterrupted,
		InterruptionEvents:   agg.InterruptionEvents,
		CustomerMinutes:      agg.CustomerMinutes,
		OngoingInterruptions: agg.OngoingInterruptions,
		ENSEstimated:         true,
		AvgLoadKW:            avgLoadKW,
	}
	if scoped {
		out.CircuitID = &circuitID
	}
	if totalCustomers > 0 {
		out.SAIFI = float64(agg.CustomersInterrupted) / float64(totalCustomers)
		out.SAIDI = agg.CustomerMinutes / float64(totalCustomers)
	}
	if agg.CustomersInterrupted > 0 {
		out.CAIDI = agg.CustomerMinutes / float64(agg.CustomersInterrupted)
	}
	// ENS = interrupted customer-hours × assumed average demand.
	out.ENS = (agg.CustomerMinutes / 60.0) * avgLoadKW

	// IEEE 1366 major-event-day normalisation. One grouped read gives the per-day
	// interruption totals (same clip/scope/unplanned filters as the headline agg, so
	// the daily rows sum back to it); the 2.5-beta classification is pure Go (med.go).
	daily, err := dailyReliabilityRows(ctx, db, aggCircuitJoin, aggArgs)
	if err != nil {
		return nil, err
	}
	med := computeMED(daily, totalCustomers)
	out.MEDThresholdSAIDI = med.ThresholdSAIDI
	out.MajorEventDays = med.MajorEventDays
	out.SAIDINormalized = med.SAIDINorm
	out.SAIFINormalized = med.SAIFINorm
	out.MEDApplicable = med.Applicable
	out.MEDNote = med.Note

	// Fault-cause decomposition. The faults module is a separate schema/migration
	// (0106); a deployment may run OMS without it, so we probe for the table and
	// simply skip the breakdown when it is absent rather than 500 the whole
	// report -- the same fail-soft posture as the circuit_id column guard above.
	var hasFaultTable bool
	if err := db.NewRaw(`SELECT to_regclass('faults.fault_events') IS NOT NULL`).Scan(ctx, &hasFaultTable); err != nil {
		return nil, fmt.Errorf("oms: checking for faults.fault_events: %w", err)
	}
	out.FaultBreakdownAvailable = hasFaultTable
	if hasFaultTable {
		if err := attachFaultCauseBreakdown(ctx, db, out, aggCircuitJoin, aggArgs); err != nil {
			return nil, err
		}
	}

	return out, nil
}

// meterLoadAvailable reports whether gis.meter carries the meter_role column --
// i.e. whether seed/meter_load_leaf.sql (Phase 63) has been applied and METER
// recompiled. Used fail-soft to decide whether the whole-system customer
// denominator should include PRIMARY meters, so the reliability report keeps
// working on a deployment still on the SERVICE_POINT-only model. Same
// information_schema probe posture as the circuit_id guard.
func meterLoadAvailable(ctx context.Context, db bun.IDB) (bool, error) {
	var ok bool
	const q = `SELECT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_schema = 'gis' AND table_name = 'meter' AND column_name = 'meter_role'
)`
	if err := db.NewRaw(q).Scan(ctx, &ok); err != nil {
		return false, fmt.Errorf("oms: checking gis.meter.meter_role column: %w", err)
	}
	return ok, nil
}

// dailyReliabilityRows returns one row per outage day in the window -- the
// customer-minutes and the customers whose outage began that day -- grouped by the
// (clipped) start date. It uses the SAME window-clip, circuit-scope and
// unplanned/non-cancelled filters as the headline aggregate, so the rows sum back
// to CustomerMinutes / CustomersInterrupted exactly; only the per-day split is new.
// A multi-day outage's clipped minutes are attributed to its start day (med.go's
// documented simplification). args is the headline [from, to] (+ circuitID).
func dailyReliabilityRows(ctx context.Context, db bun.IDB, circuitJoin string, args []any) ([]dailyReliability, error) {
	query := fmt.Sprintf(`
WITH params AS (
    SELECT ?::timestamptz AS win_start, ?::timestamptz AS win_end
)
SELECT
    COALESCE(SUM(ci.customers * GREATEST(0, EXTRACT(EPOCH FROM (
        LEAST(COALESCE(ci.restored_at, p.win_end), p.win_end) - GREATEST(ci.interrupted_at, p.win_start)
    )) / 60.0)), 0) AS customer_minutes,
    COALESCE(SUM(ci.customers) FILTER (WHERE ci.interrupted_at >= p.win_start AND ci.interrupted_at < p.win_end), 0) AS customers_started
FROM oms.customer_interruptions ci
JOIN oms.incidents inc ON inc.id = ci.incident_id
%s
CROSS JOIN params p
WHERE inc.status <> 'cancelled'
  AND inc.outage_type = 'unplanned'
  AND ci.interrupted_at < p.win_end
  AND COALESCE(ci.restored_at, p.win_end) > p.win_start
GROUP BY (GREATEST(ci.interrupted_at, p.win_start))::date`, circuitJoin)

	var rows []struct {
		CustomerMinutes  float64 `bun:"customer_minutes"`
		CustomersStarted int     `bun:"customers_started"`
	}
	if err := db.NewRaw(query, args...).Scan(ctx, &rows); err != nil {
		return nil, fmt.Errorf("oms: computing daily reliability rows: %w", err)
	}

	daily := make([]dailyReliability, len(rows))
	for i, r := range rows {
		daily[i] = dailyReliability{customerMinutes: r.CustomerMinutes, customersStarted: r.CustomersStarted}
	}
	return daily, nil
}

// attachFaultCauseBreakdown computes out.ByFaultCause: the same window's
// interruptions grouped by the cause of the fault linked to each one's incident,
// with each bucket's SAIFI/SAIDI over out.TotalCustomers so the buckets sum to
// the system indices. It reuses the caller's window/circuit fragments and args
// (ordered [from, to] or [from, to, circuitID]) so the scope matches the headline
// figures exactly. rep_fault picks ONE representative fault per incident
// (earliest detected, id tie-break) so an incident with several fault events is
// not double-counted.
func attachFaultCauseBreakdown(ctx context.Context, db bun.IDB, out *ReliabilityIndices, circuitJoin string, args []any) error {
	query := fmt.Sprintf(`
WITH params AS (
    SELECT ?::timestamptz AS win_start, ?::timestamptz AS win_end
),
rep_fault AS (
    SELECT DISTINCT ON (incident_id) incident_id, cause
    FROM faults.fault_events
    WHERE incident_id IS NOT NULL
    ORDER BY incident_id, detected_at, id
),
clipped AS (
    SELECT
        ci.customers,
        ci.interrupted_at,
        GREATEST(ci.interrupted_at, p.win_start) AS eff_start,
        LEAST(COALESCE(ci.restored_at, p.win_end), p.win_end) AS eff_end,
        (rf.incident_id IS NOT NULL) AS fault_linked,
        rf.cause,
        p.win_start,
        p.win_end
    FROM oms.customer_interruptions ci
    JOIN oms.incidents inc ON inc.id = ci.incident_id
    %s
    LEFT JOIN rep_fault rf ON rf.incident_id = inc.id
    CROSS JOIN params p
    WHERE inc.status <> 'cancelled'
      AND inc.outage_type = 'unplanned'
      AND ci.interrupted_at < p.win_end
      AND COALESCE(ci.restored_at, p.win_end) > p.win_start
)
SELECT
    fault_linked,
    cause,
    COALESCE(SUM(customers) FILTER (WHERE interrupted_at >= win_start AND interrupted_at < win_end), 0) AS customers_interrupted,
    COALESCE(COUNT(*)       FILTER (WHERE interrupted_at >= win_start AND interrupted_at < win_end), 0) AS interruption_events,
    COALESCE(SUM(customers * GREATEST(0, EXTRACT(EPOCH FROM (eff_end - eff_start)) / 60.0)) FILTER (WHERE eff_end > eff_start), 0) AS customer_minutes
FROM clipped
GROUP BY fault_linked, cause
ORDER BY customer_minutes DESC, customers_interrupted DESC`, circuitJoin)

	var rows []struct {
		FaultLinked          bool    `bun:"fault_linked"`
		Cause                *string `bun:"cause"`
		CustomersInterrupted int     `bun:"customers_interrupted"`
		InterruptionEvents   int     `bun:"interruption_events"`
		CustomerMinutes      float64 `bun:"customer_minutes"`
	}
	if err := db.NewRaw(query, args...).Scan(ctx, &rows); err != nil {
		return fmt.Errorf("oms: computing fault-cause breakdown: %w", err)
	}

	out.ByFaultCause = make([]FaultCauseContribution, 0, len(rows))
	for _, r := range rows {
		c := FaultCauseContribution{
			Cause:                r.Cause,
			FaultLinked:          r.FaultLinked,
			CustomersInterrupted: r.CustomersInterrupted,
			InterruptionEvents:   r.InterruptionEvents,
			CustomerMinutes:      r.CustomerMinutes,
		}
		if out.TotalCustomers > 0 {
			c.SAIFI = float64(r.CustomersInterrupted) / float64(out.TotalCustomers)
			c.SAIDI = r.CustomerMinutes / float64(out.TotalCustomers)
		}
		out.ByFaultCause = append(out.ByFaultCause, c)
	}
	return nil
}
