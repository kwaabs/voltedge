package telemetry

// reconcile.go is the money feature of live-state (blocker #1): comparing what
// the FIELD reports (device_telemetry) against what the MODEL believes
// (device_state), and flagging where they disagree or where telemetry has gone
// STALE. "The model says CLOSED, SCADA says OPEN since 14:02" is exactly the blind
// spot a control room has without this -- it's how you find out your picture is
// out of sync with the copper before it bites you. Read-only; the operator
// decides whether to trust the field and apply it (slice 3).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// DefaultStaleSeconds is how old a reading may be before it's "stale". Telemetry
// polls every few seconds, so 5 minutes of silence from a device is a real signal
// (dead RTU, lost comms) -- overridable per request.
const DefaultStaleSeconds = 300

// ReconItem is one device whose telemetry needs attention: it disagrees with the
// model, or it's gone stale. agree/stale say which (both can be relevant).
type ReconItem struct {
	FeatureClassCode string    `json:"feature_class_code"`
	FeatureID        string    `json:"feature_id"`
	TelemetryState   *string   `json:"telemetry_state"`
	ModelState       *string   `json:"model_state"`
	Agree            bool      `json:"agree"`
	Stale            bool      `json:"stale"`
	AgeSeconds       int       `json:"age_seconds"`
	Quality          string    `json:"quality"`
	Source           *string   `json:"source"`
	MeasuredAt       time.Time `json:"measured_at"`
}

// Reconciliation is the field-vs-model summary plus the items needing attention.
type Reconciliation struct {
	StaleSeconds int         `json:"stale_seconds"`
	Total        int         `json:"total"`
	Agreeing     int         `json:"agreeing"`
	Disagreeing  int         `json:"disagreeing"`
	Stale        int         `json:"stale"`
	NoModelState int         `json:"no_model_state"`
	Items        []ReconItem `json:"items"`
}

// ReconcileTelemetry compares every telemetered device's reported state against
// its model device_state and its reading age against staleSeconds. Items are the
// ones that disagree or are stale (disagreements first). A device whose class has
// no device_state (a measurement-only sensor) counts as no_model_state, not an
// issue.
func ReconcileTelemetry(ctx context.Context, db bun.IDB, staleSeconds int) (*Reconciliation, error) {
	if staleSeconds <= 0 {
		staleSeconds = DefaultStaleSeconds
	}
	rows, err := ListTelemetry(ctx, db, "")
	if err != nil {
		return nil, err
	}
	models, err := modelStates(ctx, db, rows)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	out := &Reconciliation{StaleSeconds: staleSeconds, Total: len(rows), Items: []ReconItem{}}
	for _, r := range rows {
		age := int(now.Sub(r.MeasuredAt).Seconds())
		if age < 0 {
			age = 0
		}
		stale := age > staleSeconds
		model := models[r.FeatureClassCode+"|"+r.FeatureID]
		hasModel := model != nil && strings.TrimSpace(*model) != ""
		hasTelem := r.State != nil && strings.TrimSpace(*r.State) != ""
		agree := hasModel && hasTelem && strings.EqualFold(strings.TrimSpace(*model), strings.TrimSpace(*r.State))
		disagree := hasModel && hasTelem && !agree

		if !hasModel {
			out.NoModelState++
		}
		if stale {
			out.Stale++
		}
		if disagree {
			out.Disagreeing++
		}
		if agree && !stale {
			out.Agreeing++
		}

		if disagree || stale {
			out.Items = append(out.Items, ReconItem{
				FeatureClassCode: r.FeatureClassCode,
				FeatureID:        r.FeatureID,
				TelemetryState:   r.State,
				ModelState:       model,
				Agree:            agree,
				Stale:            stale,
				AgeSeconds:       age,
				Quality:          r.Quality,
				Source:           r.Source,
				MeasuredAt:       r.MeasuredAt,
			})
		}
	}

	// Disagreements first (a wrong model is worse than a quiet one), then oldest.
	sort.SliceStable(out.Items, func(i, j int) bool {
		di := !out.Items[i].Agree && out.Items[i].ModelState != nil && out.Items[i].TelemetryState != nil
		dj := !out.Items[j].Agree && out.Items[j].ModelState != nil && out.Items[j].TelemetryState != nil
		if di != dj {
			return di
		}
		return out.Items[i].AgeSeconds > out.Items[j].AgeSeconds
	})
	return out, nil
}

// modelStates reads each telemetered device's model device_state, keyed
// "class|id". A device whose class has no device_state field (a measurement-only
// class) is simply absent from the map. Table names come from server-controlled
// metadata (physical_table), the same trust basis deviceops/crud interpolate on;
// ids bind as parameters.
func modelStates(ctx context.Context, db bun.IDB, rows []DeviceTelemetry) (map[string]*string, error) {
	byClass := map[string][]string{}
	for _, r := range rows {
		byClass[r.FeatureClassCode] = append(byClass[r.FeatureClassCode], r.FeatureID)
	}
	out := make(map[string]*string, len(rows))
	for class, ids := range byClass {
		var table string
		err := db.NewSelect().
			TableExpr("metadata.feature_classes AS fc").
			ColumnExpr("fc.physical_table").
			Join("JOIN metadata.fields fld ON fld.feature_class_id = fc.id AND fld.name = 'device_state' AND fld.status = 'active'").
			Where("fc.code = ?", class).
			Where("fc.physical_table IS NOT NULL").
			Scan(ctx, &table)
		if errors.Is(err, sql.ErrNoRows) {
			continue // this class carries no device_state -> no model state to compare
		}
		if err != nil {
			return nil, fmt.Errorf("telemetry: resolving table for %s: %w", class, err)
		}

		var devs []struct {
			ID    string  `bun:"id"`
			State *string `bun:"device_state"`
		}
		if err := db.NewSelect().
			TableExpr(fmt.Sprintf("%s AS g", table)).
			ColumnExpr("g.id::text AS id").
			ColumnExpr("g.device_state::text AS device_state").
			Where("g.id IN (?)", bun.In(ids)).
			Scan(ctx, &devs); err != nil {
			return nil, fmt.Errorf("telemetry: reading device_state for %s: %w", class, err)
		}
		for _, d := range devs {
			state := d.State
			out[class+"|"+d.ID] = state
		}
	}
	return out, nil
}
