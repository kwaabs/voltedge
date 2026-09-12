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
	TotalCustomers       int     `json:"total_customers"`        // N: customers served on the live system
	CustomersInterrupted int     `json:"customers_interrupted"`  // Σ customers over interruptions that began in-window (SAIFI numerator)
	InterruptionEvents   int     `json:"interruption_events"`    // count of interruptions that began in-window
	CustomerMinutes      float64 `json:"customer_minutes"`       // Σ customers × in-window minutes (SAIDI numerator)
	OngoingInterruptions int     `json:"ongoing_interruptions"`  // interruptions still open at To

	// The four indices.
	SAIFI float64 `json:"saifi"` // interruptions per customer
	SAIDI float64 `json:"saidi"` // customer-minutes per customer
	CAIDI float64 `json:"caidi"` // minutes per interruption (0 when no interruptions)
	ENS   float64 `json:"ens_kwh"`

	// ENS assumption, echoed so the estimate is auditable.
	ENSEstimated bool    `json:"ens_estimated"`
	AvgLoadKW    float64 `json:"avg_load_kw"`
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
	var totalCustomers int
	denomQuery := fmt.Sprintf(
		`SELECT COALESCE(SUM(COALESCE(customers_served, 1)), 0) FROM gis.service_point WHERE deleted_at IS NULL %s`,
		denomCircuitFilter,
	)
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

	return out, nil
}
