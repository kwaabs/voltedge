package oms

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"
)

// worstserved.go adds the IEEE 1366 "worst-served customer" indices to the reliability
// report -- the lens the system averages (SAIDI/SAIFI) hide. Over the SAME frozen
// oms.customer_interruptions fact table and the same window / unplanned / circuit
// filters as indices.go, it groups by SERVICE POINT:
//
//	CEMI_n   Customers Experiencing Multiple Interruptions
//	         = customers with MORE THAN CEMIThreshold sustained interruptions / N
//	CELID    Customers Experiencing Long Interruption Durations
//	         = customers whose TOTAL interruption time exceeds CELIDHours / N
//
// plus WorstServed: the ranked service points (most interruptions, then most minutes)
// -- the "who is actually suffering" list behind the ratios. Everything is over the
// same customer denominator N as the system indices, so the numbers are comparable.

const (
	// DefaultCEMIThreshold is the IEEE 1366 CEMI count threshold n: CEMI_n counts a
	// customer once it has had MORE THAN n sustained interruptions in the window.
	DefaultCEMIThreshold = 4
	// DefaultCELIDHours is the total-duration threshold (hours) a customer must exceed
	// to count toward CELID.
	DefaultCELIDHours = 24.0
	// worstServedLimit caps the ranked worst-served list the report returns.
	worstServedLimit = 25
)

// WorstServedPoint is one service point's own interruption record over the window --
// how many times it went out and for how long -- so the operator can see the worst-
// served customers, not just the system average.
type WorstServedPoint struct {
	FeatureClass  string  `bun:"feature_class" json:"feature_class"`
	FeatureID     string  `bun:"service_point_id" json:"feature_id"`
	Customers     int     `bun:"customers" json:"customers"`
	Interruptions int     `bun:"interruptions" json:"interruptions"`
	TotalMinutes  float64 `bun:"total_minutes" json:"total_minutes"`
	MaxMinutes    float64 `bun:"max_minutes" json:"max_minutes"`
}

// perSPCTE is the shared per-service-point aggregate the worst-served reads build on:
// one row per interrupted service point with its interruption count, total and worst
// single clipped minutes, and its (frozen) customer weight. Uses the SAME window-clip,
// circuit-scope and unplanned/non-cancelled filters as the headline aggregate, so it
// is consistent with SAIDI/SAIFI. %s is the circuit join; its args follow [from, to].
func perSPCTE(circuitJoin string) string {
	return fmt.Sprintf(`WITH params AS (
    SELECT ?::timestamptz AS win_start, ?::timestamptz AS win_end
),
per_sp AS (
    SELECT
        ci.feature_class,
        ci.service_point_id,
        MAX(ci.customers) AS customers,
        COUNT(*) FILTER (WHERE ci.interrupted_at >= p.win_start AND ci.interrupted_at < p.win_end) AS interruptions,
        COALESCE(SUM(GREATEST(0, EXTRACT(EPOCH FROM (
            LEAST(COALESCE(ci.restored_at, p.win_end), p.win_end) - GREATEST(ci.interrupted_at, p.win_start)
        )) / 60.0)), 0) AS total_minutes,
        COALESCE(MAX(GREATEST(0, EXTRACT(EPOCH FROM (
            LEAST(COALESCE(ci.restored_at, p.win_end), p.win_end) - GREATEST(ci.interrupted_at, p.win_start)
        )) / 60.0)), 0) AS max_minutes
    FROM oms.customer_interruptions ci
    JOIN oms.incidents inc ON inc.id = ci.incident_id
    %s
    CROSS JOIN params p
    WHERE inc.status <> 'cancelled'
      AND inc.outage_type = 'unplanned'
      AND ci.interrupted_at < p.win_end
      AND COALESCE(ci.restored_at, p.win_end) > p.win_start
    GROUP BY ci.feature_class, ci.service_point_id
)`, circuitJoin)
}

// attachWorstServed computes CEMI / CELID and the ranked worst-served list and writes
// them onto out. args is the headline [from, to] (+ circuitID); cemiN is the CEMI count
// threshold, celidMinutes the CELID total-duration threshold (minutes). Two reads over
// the shared per-SP CTE: the counts (customer-weighted), then the top-N list.
func attachWorstServed(ctx context.Context, db bun.IDB, out *ReliabilityIndices, circuitJoin string, args []any, cemiN int, celidMinutes float64) error {
	// CEMI / CELID numerators: customers on service points over each threshold. The two
	// FILTER thresholds bind AFTER the window (and circuit) args, matching ? order.
	countQ := perSPCTE(circuitJoin) + `
SELECT
    COALESCE(SUM(customers) FILTER (WHERE interruptions > ?), 0) AS cemi_customers,
    COALESCE(SUM(customers) FILTER (WHERE total_minutes > ?), 0) AS celid_customers
FROM per_sp`
	var cnt struct {
		CEMICustomers  int `bun:"cemi_customers"`
		CELIDCustomers int `bun:"celid_customers"`
	}
	countArgs := append(append([]any{}, args...), cemiN, celidMinutes)
	if err := db.NewRaw(countQ, countArgs...).Scan(ctx, &cnt); err != nil {
		return fmt.Errorf("oms: computing CEMI/CELID: %w", err)
	}
	out.CEMICustomers = cnt.CEMICustomers
	out.CELIDCustomers = cnt.CELIDCustomers
	if out.TotalCustomers > 0 {
		out.CEMI = float64(cnt.CEMICustomers) / float64(out.TotalCustomers)
		out.CELID = float64(cnt.CELIDCustomers) / float64(out.TotalCustomers)
	}

	// The ranked worst-served list: most interruptions, then most minutes.
	listQ := perSPCTE(circuitJoin) + fmt.Sprintf(`
SELECT feature_class, service_point_id, customers, interruptions, total_minutes, max_minutes
FROM per_sp
WHERE interruptions > 0
ORDER BY interruptions DESC, total_minutes DESC
LIMIT %d`, worstServedLimit)
	out.WorstServed = []WorstServedPoint{}
	if err := db.NewRaw(listQ, args...).Scan(ctx, &out.WorstServed); err != nil {
		return fmt.Errorf("oms: computing the worst-served list: %w", err)
	}
	return nil
}
