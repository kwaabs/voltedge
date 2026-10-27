package oms

// analytics.go -- the "source & cause" payoff metrics (migration 0168): HOW the
// utility learned of its outages and WHY they happened, over a window. Read-only
// over oms.incidents (+ the cause catalog); unplanned-only and scoped by
// started_at, matching the reliability-index window convention (default trailing
// 12 months). These are the numbers that justify logging source + cause: the
// detection-source mix, the situational-awareness share ("knew without a call"),
// the cause-family breakdown, and the mean detection->restore time.

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// DetectionSourceStat is one detection-source bucket's count + customer impact.
type DetectionSourceStat struct {
	Source    string `json:"source" bun:"source"`
	Incidents int    `json:"incidents" bun:"incidents"`
	Customers int    `json:"customers_affected" bun:"customers"`
}

// CauseFamilyStat is one cause-family bucket's count + customer impact.
type CauseFamilyStat struct {
	Family    string `json:"family" bun:"family"`
	Incidents int    `json:"incidents" bun:"incidents"`
	Customers int    `json:"customers_affected" bun:"customers"`
}

// DetectionMetrics is the source/cause analytics summary for a window.
type DetectionMetrics struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`

	TotalIncidents    int `json:"total_incidents"`
	RestoredIncidents int `json:"restored_incidents"`

	// BySource is the detection-source mix (how we learned); 'unknown' buckets a
	// pre-0168 null source.
	BySource []DetectionSourceStat `json:"by_source"`

	// MachineDetected* count the incidents we knew about WITHOUT a human calling --
	// SCADA / AMI / analytics -- the single best situational-awareness measure.
	MachineDetectedIncidents int     `json:"machine_detected_incidents"`
	MachineDetectedShare     float64 `json:"machine_detected_share"`

	// ByCause is the cause-family mix (why), via the coded catalog; 'unknown' when
	// the cause is unset or not in the catalog.
	ByCause []CauseFamilyStat `json:"by_cause"`

	// AvgDetectionToRestoreMinutes is the mean minutes from the first signal
	// (detected_at) to restoration, over restored incidents. Null when none.
	AvgDetectionToRestoreMinutes *float64 `json:"avg_detection_to_restore_minutes"`
}

// machineSources are the detection sources that mean "we knew without a human
// call" (field telemetry + automated analytics).
var machineSources = map[string]bool{"scada": true, "ami": true, "analytics": true}

// ComputeDetectionMetrics summarises the window's unplanned incidents by detection
// source and cause family, plus the awareness share and detection->restore time.
// to defaults to now, from to a year before to; from must precede to.
func ComputeDetectionMetrics(ctx context.Context, db bun.IDB, from, to *time.Time) (*DetectionMetrics, error) {
	t := time.Now()
	if to != nil {
		t = *to
	}
	f := t.AddDate(-1, 0, 0)
	if from != nil {
		f = *from
	}
	if !f.Before(t) {
		return nil, invalidf("from must be before to")
	}

	m := &DetectionMetrics{From: f, To: t, BySource: []DetectionSourceStat{}, ByCause: []CauseFamilyStat{}}

	// Detection-source mix (how we learned).
	if err := db.NewSelect().
		ColumnExpr("COALESCE(detection_source, 'unknown') AS source").
		ColumnExpr("COUNT(*) AS incidents").
		ColumnExpr("COALESCE(SUM(affected_customers), 0) AS customers").
		TableExpr("oms.incidents").
		Where("outage_type = ?", "unplanned").
		Where("started_at >= ?", f).
		Where("started_at < ?", t).
		GroupExpr("COALESCE(detection_source, 'unknown')").
		OrderExpr("incidents DESC").
		Scan(ctx, &m.BySource); err != nil {
		return nil, fmt.Errorf("oms: detection source mix: %w", err)
	}
	for _, s := range m.BySource {
		m.TotalIncidents += s.Incidents
		if machineSources[s.Source] {
			m.MachineDetectedIncidents += s.Incidents
		}
	}
	if m.TotalIncidents > 0 {
		m.MachineDetectedShare = float64(m.MachineDetectedIncidents) / float64(m.TotalIncidents)
	}

	// Cause-family mix (why), via the coded catalog.
	if err := db.NewSelect().
		ColumnExpr("COALESCE(oc.family, 'unknown') AS family").
		ColumnExpr("COUNT(*) AS incidents").
		ColumnExpr("COALESCE(SUM(i.affected_customers), 0) AS customers").
		TableExpr("oms.incidents AS i").
		Join("LEFT JOIN oms.outage_causes AS oc ON oc.code = i.cause_code").
		Where("i.outage_type = ?", "unplanned").
		Where("i.started_at >= ?", f).
		Where("i.started_at < ?", t).
		GroupExpr("COALESCE(oc.family, 'unknown')").
		OrderExpr("incidents DESC").
		Scan(ctx, &m.ByCause); err != nil {
		return nil, fmt.Errorf("oms: cause family mix: %w", err)
	}

	// Restored count + mean detection->restore latency (minutes).
	var lat struct {
		Restored int      `bun:"restored"`
		AvgMin   *float64 `bun:"avg_min"`
	}
	if err := db.NewSelect().
		ColumnExpr("COUNT(*) FILTER (WHERE restored_at IS NOT NULL) AS restored").
		ColumnExpr("AVG(EXTRACT(EPOCH FROM (restored_at - detected_at)) / 60.0) "+
			"FILTER (WHERE restored_at IS NOT NULL AND detected_at IS NOT NULL) AS avg_min").
		TableExpr("oms.incidents").
		Where("outage_type = ?", "unplanned").
		Where("started_at >= ?", f).
		Where("started_at < ?", t).
		Scan(ctx, &lat); err != nil {
		return nil, fmt.Errorf("oms: detection latency: %w", err)
	}
	m.RestoredIncidents = lat.Restored
	m.AvgDetectionToRestoreMinutes = lat.AvgMin

	return m, nil
}
