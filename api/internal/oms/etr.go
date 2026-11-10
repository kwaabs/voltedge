package oms

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// etr.go derives a DATA-DRIVEN estimated restoration time (ETR) suggestion from
// the history of how long similar outages actually took to restore -- the ADMS
// "predicted restoration" idea. Setting the ETR itself (SetIncidentETR, crew.go)
// stays a manual operator action; this only computes a GROUNDED default to offer,
// replacing the hand-rolled "+2 hours" guess. It is READ-ONLY analytics over the
// restored-incident history and never touches the live network.
//
// The estimate is the median restoration DURATION (restored_at - started_at) over
// restored unplanned outages in the trailing window, tried CAUSE-SPECIFIC first
// (same cause_code as this incident) and falling back to system-wide when the
// cause has too little history. The suggested ETR is the incident's start plus that
// duration, floored so it is never in the past: an outage already running longer
// than typical is flagged and offered a short floor from now instead.

const (
	// etrHistoryLookback bounds the restored-outage history the median is taken over.
	etrHistoryLookback = 365 * 24 * time.Hour
	// etrMinSamples is the fewest restored outages needed to trust a median; below
	// it the cause-specific set falls back to system-wide, and a system-wide set
	// below it returns insufficient-history (the operator sets the ETR by hand).
	etrMinSamples = 3
	// etrFloor is the soonest a suggested ETR may be from now -- an outage already
	// past its typical duration still gets a sensible "a bit longer" estimate.
	etrFloor = 30 * time.Minute
)

// ETRSuggestion is a grounded ETR proposal plus the evidence behind it, so the
// operator sees exactly what the figure rests on before accepting it.
type ETRSuggestion struct {
	IncidentID string `json:"incident_id"`

	// SuggestedETR is the proposed restoration timestamp; nil when not applicable
	// (restored/cancelled incident, or too little history to derive one).
	SuggestedETR *time.Time `json:"suggested_etr"`

	// Basis is how it was derived: "cause:<code>", "system", "planned_end",
	// "insufficient_history", or "not_applicable".
	Basis     string  `json:"basis"`
	CauseCode *string `json:"cause_code,omitempty"`

	SampleSize        int     `json:"sample_size"`
	MedianDurationMin float64 `json:"median_duration_minutes"`

	// LongerThanTypical is true when the outage has already run past the median
	// duration, so the suggestion was floored to etrFloor from now.
	LongerThanTypical bool   `json:"longer_than_typical"`
	Applicable        bool   `json:"applicable"`
	Note              string `json:"note,omitempty"`
}

// SuggestETR computes a data-driven ETR suggestion for one incident. It does not
// write anything; the caller (or operator) applies it via SetIncidentETR.
func SuggestETR(ctx context.Context, db bun.IDB, incidentID string) (*ETRSuggestion, error) {
	inc, err := GetIncident(ctx, db, incidentID)
	if err != nil {
		return nil, err
	}
	out := &ETRSuggestion{IncidentID: inc.ID}

	if inc.Status == StatusRestored || inc.Status == StatusCancelled {
		out.Basis = "not_applicable"
		out.Note = fmt.Sprintf("incident is %s -- no ETR needed", inc.Status)
		return out, nil
	}

	// A planned outage's restoration is simply its booked end.
	if inc.OutageType == OutageTypePlanned && inc.PlannedEnd != nil {
		end := inc.PlannedEnd.UTC()
		out.SuggestedETR = &end
		out.Basis = "planned_end"
		out.Applicable = true
		out.Note = "planned outage -- the suggested ETR is the booked end time"
		return out, nil
	}

	// Cause-specific history first, system-wide as the fallback.
	var median float64
	var n int
	basis := "system"
	if inc.CauseCode != nil && *inc.CauseCode != "" {
		m, c, err := restorationMedianMinutes(ctx, db, inc.CauseCode)
		if err != nil {
			return nil, err
		}
		if c >= etrMinSamples {
			median, n, basis = m, c, "cause:"+*inc.CauseCode
			out.CauseCode = inc.CauseCode
		}
	}
	if n < etrMinSamples {
		m, c, err := restorationMedianMinutes(ctx, db, nil)
		if err != nil {
			return nil, err
		}
		median, n, basis = m, c, "system"
		out.CauseCode = nil
	}

	if n < etrMinSamples {
		out.Basis = "insufficient_history"
		out.SampleSize = n
		out.Note = fmt.Sprintf("only %d restored outage(s) in the last 12 months -- not enough to derive an ETR; set one manually", n)
		return out, nil
	}

	out.SampleSize = n
	out.MedianDurationMin = median
	out.Basis = basis
	out.Applicable = true

	now := time.Now()
	suggested := inc.StartedAt.Add(time.Duration(median * float64(time.Minute)))
	if floor := now.Add(etrFloor); suggested.Before(floor) {
		suggested = floor
		out.LongerThanTypical = true
		out.Note = fmt.Sprintf("already running longer than the typical %s (median of %d similar outages) -- suggesting %s from now",
			humanizeMinutes(median), n, humanizeMinutes(etrFloor.Minutes()))
	} else {
		out.Note = fmt.Sprintf("typical restoration is %s (median of %d similar outages)", humanizeMinutes(median), n)
	}
	su := suggested.UTC()
	out.SuggestedETR = &su
	return out, nil
}

// restorationMedianMinutes is the median restoration duration (minutes) over
// restored unplanned outages in the trailing window, optionally filtered to one
// cause_code, with the sample size. A zero median with n>0 is a legitimate (near-
// instant) history; callers gate on n, not the median.
func restorationMedianMinutes(ctx context.Context, db bun.IDB, cause *string) (float64, int, error) {
	q := db.NewSelect().
		ColumnExpr("COALESCE(percentile_cont(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (restored_at - started_at)) / 60.0), 0) AS median_min").
		ColumnExpr("COUNT(*) AS n").
		TableExpr("oms.incidents").
		Where("outage_type = ?", OutageTypeUnplanned).
		Where("status = ?", StatusRestored).
		Where("restored_at IS NOT NULL").
		Where("restored_at > started_at").
		Where("restored_at >= ?", time.Now().Add(-etrHistoryLookback))
	if cause != nil && *cause != "" {
		q = q.Where("cause_code = ?", *cause)
	}

	var row struct {
		MedianMin float64 `bun:"median_min"`
		N         int     `bun:"n"`
	}
	if err := q.Scan(ctx, &row); err != nil {
		return 0, 0, fmt.Errorf("oms: computing restoration median for ETR: %w", err)
	}
	return row.MedianMin, row.N, nil
}

// humanizeMinutes renders a minute count as a compact "2h14m" / "45m" / "3d2h"
// for the suggestion note.
func humanizeMinutes(min float64) string {
	total := int(min + 0.5)
	if total <= 0 {
		return "0m"
	}
	d := total / (24 * 60)
	h := (total % (24 * 60)) / 60
	m := total % 60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd%dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	default:
		return fmt.Sprintf("%dm", m)
	}
}
