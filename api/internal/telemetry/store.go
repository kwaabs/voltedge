package telemetry

// store.go is the live-state / telemetry ingestion engine (blocker #1): a
// latest-value upsert into ops.device_telemetry, keyed by the device. It is
// deliberately NOT the durable business-event webhook engine -- telemetry is a
// high-rate, latest-value, loss-tolerant stream, so this fire-and-forget upsert
// (newest measured_at wins, out-of-order older samples dropped, a bad report
// rejected without failing the batch) is the right model. Device correlation
// reuses the same external_ref reverse index the webhook adapters use.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

var validQuality = map[string]bool{"good": true, "suspect": true, "stale": true, "bad": true}

// DeviceTelemetry is the latest-value telemetry for one device (ops.device_telemetry).
type DeviceTelemetry struct {
	bun.BaseModel `bun:"table:ops.device_telemetry,alias:dt"`

	FeatureClassCode string          `bun:"feature_class_code,pk" json:"feature_class_code"`
	FeatureID        string          `bun:"feature_id,pk" json:"feature_id"`
	State            *string         `bun:"state" json:"state"`
	Measurements     json.RawMessage `bun:"measurements" json:"measurements,omitempty"`
	Quality          string          `bun:"quality" json:"quality"`
	Source           *string         `bun:"source" json:"source"`
	MeasuredAt       time.Time       `bun:"measured_at" json:"measured_at"`
	IngestedAt       time.Time       `bun:"ingested_at" json:"ingested_at"`
}

// Report is one incoming telemetry sample. The device is named directly
// (feature_class + feature_id) or by its external id (external_id + system_code,
// resolved through the external_ref reverse index). state is the reported switch
// position (nullable -- a measurement-only feed); measurements is an optional
// jsonb bag. measured_at defaults to now when absent; quality defaults to 'good'.
type Report struct {
	FeatureClass string          `json:"feature_class"`
	FeatureID    string          `json:"feature_id"`
	ExternalID   string          `json:"external_id"`
	SystemCode   string          `json:"system_code"`
	State        *string         `json:"state"`
	Measurements json.RawMessage `json:"measurements"`
	Quality      string          `json:"quality"`
	Source       *string         `json:"source"`
	MeasuredAt   *time.Time      `json:"measured_at"`
}

// RejectedReport is one report that couldn't be ingested, with why -- the batch
// goes on (telemetry is tolerant), so a caller sees exactly which samples failed.
type RejectedReport struct {
	Index  int    `json:"index"`
	Reason string `json:"reason"`
}

// IngestResult is a batch's outcome: how many landed and which were rejected.
type IngestResult struct {
	Accepted int              `json:"accepted"`
	Rejected []RejectedReport `json:"rejected"`
}

// IngestBatch upserts a batch of telemetry, latest-value per device. A report
// with an unresolvable device or a bad quality flag is REJECTED (recorded in the
// result) without failing the rest; a sample older than the device's current
// reading is a no-op (the newest reading is kept). A real DB error aborts.
func IngestBatch(ctx context.Context, db bun.IDB, reports []Report) (*IngestResult, error) {
	res := &IngestResult{Rejected: []RejectedReport{}}

	const upsert = `
INSERT INTO ops.device_telemetry
    (feature_class_code, feature_id, state, measurements, quality, source, measured_at, ingested_at)
VALUES (?, ?, ?, ?, ?, ?, ?, now())
ON CONFLICT (feature_class_code, feature_id) DO UPDATE SET
    state = EXCLUDED.state,
    measurements = EXCLUDED.measurements,
    quality = EXCLUDED.quality,
    source = EXCLUDED.source,
    measured_at = EXCLUDED.measured_at,
    ingested_at = now()
WHERE EXCLUDED.measured_at >= ops.device_telemetry.measured_at`

	for i, r := range reports {
		quality := strings.TrimSpace(r.Quality)
		if quality == "" {
			quality = "good"
		}
		if !validQuality[quality] {
			res.Rejected = append(res.Rejected, RejectedReport{Index: i, Reason: fmt.Sprintf("invalid quality %q", quality)})
			continue
		}
		class, id, err := resolveDevice(ctx, db, r)
		if err != nil {
			if IsInvalid(err) {
				res.Rejected = append(res.Rejected, RejectedReport{Index: i, Reason: err.Error()})
				continue
			}
			return nil, err
		}

		measuredAt := time.Now()
		if r.MeasuredAt != nil {
			measuredAt = *r.MeasuredAt
		}
		var meas any
		if len(r.Measurements) > 0 {
			meas = r.Measurements
		}
		if _, err := db.ExecContext(ctx, upsert, class, id, trimToNil(r.State), meas, quality, trimToNil(r.Source), measuredAt); err != nil {
			return nil, fmt.Errorf("telemetry: upserting %s/%s: %w", class, id, err)
		}
		res.Accepted++
	}
	return res, nil
}

// resolveDevice resolves a report's device: a direct feature_class+feature_id, or
// an external_id + system_code looked up in the external_ref reverse index
// (runtime.external_feature_refs, migration 0145) -- the same correlation the
// webhook device adapters use.
func resolveDevice(ctx context.Context, db bun.IDB, r Report) (string, string, error) {
	class := strings.TrimSpace(r.FeatureClass)
	id := strings.TrimSpace(r.FeatureID)
	if class != "" && id != "" {
		return class, id, nil
	}

	ext := strings.TrimSpace(r.ExternalID)
	sys := strings.TrimSpace(r.SystemCode)
	if ext == "" || sys == "" {
		return "", "", invalidf("a device is required -- give feature_class+feature_id, or external_id with system_code")
	}
	var resolved struct {
		Class string `bun:"feature_class_code"`
		ID    string `bun:"feature_id"`
	}
	err := db.NewSelect().
		TableExpr("runtime.external_feature_refs").
		Column("feature_class_code", "feature_id").
		Where("system_code = ? AND external_id = ?", sys, ext).
		Scan(ctx, &resolved)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", invalidf("no feature claims external id %q in system %q (stamp its external_ref first)", ext, sys)
	}
	if err != nil {
		return "", "", fmt.Errorf("telemetry: external-ref lookup: %w", err)
	}
	return resolved.Class, resolved.ID, nil
}

// ListTelemetry returns the latest telemetry for every device, newest reading
// first. featureClass, when set, scopes to one class.
func ListTelemetry(ctx context.Context, db bun.IDB, featureClass string) ([]DeviceTelemetry, error) {
	var rows []DeviceTelemetry
	q := db.NewSelect().Model(&rows).Order("measured_at DESC")
	if featureClass != "" {
		q = q.Where("feature_class_code = ?", featureClass)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("telemetry: listing: %w", err)
	}
	return rows, nil
}

// GetDeviceTelemetry returns one device's latest telemetry, or nil when none has
// been reported for it.
func GetDeviceTelemetry(ctx context.Context, db bun.IDB, class, id string) (*DeviceTelemetry, error) {
	dt := new(DeviceTelemetry)
	err := db.NewSelect().Model(dt).
		Where("feature_class_code = ?", class).
		Where("feature_id = ?", id).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("telemetry: loading %s/%s: %w", class, id, err)
	}
	return dt, nil
}

func trimToNil(p *string) *string {
	if p == nil {
		return nil
	}
	s := strings.TrimSpace(*p)
	if s == "" {
		return nil
	}
	return &s
}
