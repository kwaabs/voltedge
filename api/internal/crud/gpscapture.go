package crud

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// GpsCaptureInput is the GNSS survey-quality snapshot a field device stamps onto
// a GPS-sourced placement or move (Phase gnss-quality Slice 3). It rides inside
// the sync op payload under a "gps_capture" key -- only the field tool's "place
// at my location" sets it, so the REST/MCP/map-tap write paths never carry one --
// and is persisted append-only to gis.feature_gps_captures, one row per capture,
// mirroring the feature_history pattern (a shared table, no per-class churn).
//
// Every DOP/sat/accuracy field is a pointer: an iPhone's internal receiver
// reports only accuracy (no NMEA => no DOP), so those arrive null and must stay
// null rather than defaulting to zero. The JSON keys match the mobile
// GnssSample.toCaptureJson() wire shape verbatim.
type GpsCaptureInput struct {
	Hdop       *float64 `json:"hdop"`
	Pdop       *float64 `json:"pdop"`
	Vdop       *float64 `json:"vdop"`
	HAccuracyM *float64 `json:"h_accuracy_m"`
	VAccuracyM *float64 `json:"v_accuracy_m"`
	AltitudeM  *float64 `json:"altitude_m"`
	SatsUsed   *int     `json:"sats_used"`
	SatsInView *int     `json:"sats_in_view"`
	FixType    string   `json:"fix_type"`
	Source     string   `json:"source"`
	CapturedAt string   `json:"captured_at"` // RFC3339 from the device clock
}

// featureGpsCapture mirrors gis.feature_gps_captures (migration 0079): one
// append-only row per GPS capture. Written from RecordGpsCapture in the same
// bun.IDB the feature write used, so on the sync-apply path it commits atomically
// inside applySyncOp's transaction with the create/update it belongs to.
type featureGpsCapture struct {
	bun.BaseModel `bun:"table:gis.feature_gps_captures"`

	ID               int64     `bun:"id,pk,autoincrement"`
	FeatureClassCode string    `bun:"feature_class_code"`
	FeatureID        string    `bun:"feature_id"`
	Hdop             *float64  `bun:"hdop"`
	Pdop             *float64  `bun:"pdop"`
	Vdop             *float64  `bun:"vdop"`
	HAccuracyM       *float64  `bun:"h_accuracy_m"`
	VAccuracyM       *float64  `bun:"v_accuracy_m"`
	AltitudeM        *float64  `bun:"altitude_m"`
	SatsUsed         *int      `bun:"sats_used"`
	SatsInView       *int      `bun:"sats_in_view"`
	FixType          *string   `bun:"fix_type"`
	Source           string    `bun:"source"`
	CapturedBy       any       `bun:"captured_by"` // uuid string, or nil when unknown
	CapturedAt       time.Time `bun:"captured_at"`
}

// RecordGpsCapture appends one GPS-capture row for a feature write. capturedBy is
// the device's submitting user (the sync worker's row.SubmittedBy), left nil when
// empty so captured_by lands NULL rather than an empty-string uuid. An unparseable
// or absent captured_at falls back to now() -- the capture is still worth keeping.
// An error here fails the enclosing write, so inside applySyncOp's transaction it
// rolls the whole op back (a placement without its recorded quality is not what
// the field crew asked for).
func RecordGpsCapture(ctx context.Context, db bun.IDB, classCode, featureID, capturedBy string, in GpsCaptureInput) error {
	capturedAt := time.Now().UTC()
	if in.CapturedAt != "" {
		if t, err := time.Parse(time.RFC3339, in.CapturedAt); err == nil {
			capturedAt = t.UTC()
		}
	}
	var by any
	if capturedBy != "" {
		by = capturedBy
	}
	var fix *string
	if in.FixType != "" {
		f := in.FixType
		fix = &f
	}
	_, err := db.NewInsert().Model(&featureGpsCapture{
		FeatureClassCode: classCode,
		FeatureID:        featureID,
		Hdop:             in.Hdop,
		Pdop:             in.Pdop,
		Vdop:             in.Vdop,
		HAccuracyM:       in.HAccuracyM,
		VAccuracyM:       in.VAccuracyM,
		AltitudeM:        in.AltitudeM,
		SatsUsed:         in.SatsUsed,
		SatsInView:       in.SatsInView,
		FixType:          fix,
		Source:           in.Source,
		CapturedBy:       by,
		CapturedAt:       capturedAt,
	}).Exec(ctx)
	if err != nil {
		return fmt.Errorf("crud: record gps capture: %w", err)
	}
	return nil
}

// gpsCaptureEntry is one GPS capture as the API returns it. captured_by is
// resolved to a name/email via a LEFT JOIN to iam.users (both nil for an unknown
// or since-deleted user), so the detail panel can show "who" without a second
// lookup.
type gpsCaptureEntry struct {
	ID              int64     `bun:"id" json:"id"`
	Hdop            *float64  `bun:"hdop" json:"hdop"`
	Pdop            *float64  `bun:"pdop" json:"pdop"`
	Vdop            *float64  `bun:"vdop" json:"vdop"`
	HAccuracyM      *float64  `bun:"h_accuracy_m" json:"h_accuracy_m"`
	VAccuracyM      *float64  `bun:"v_accuracy_m" json:"v_accuracy_m"`
	AltitudeM       *float64  `bun:"altitude_m" json:"altitude_m"`
	SatsUsed        *int      `bun:"sats_used" json:"sats_used"`
	SatsInView      *int      `bun:"sats_in_view" json:"sats_in_view"`
	FixType         *string   `bun:"fix_type" json:"fix_type"`
	Source          string    `bun:"source" json:"source"`
	CapturedBy      *string   `bun:"captured_by" json:"captured_by"`
	CapturedByName  *string   `bun:"captured_by_name" json:"captured_by_name"`
	CapturedByEmail *string   `bun:"captured_by_email" json:"captured_by_email"`
	CapturedAt      time.Time `bun:"captured_at" json:"captured_at"`
	RecordedAt      time.Time `bun:"recorded_at" json:"recorded_at"`
}

// ListFeatureGpsCaptures returns one feature's GPS captures, newest first.
// Exported so the cmd/fieldsync read surface (Phase gnss-quality Slice 3b) can
// serve the field tool the exact same rows the web GPS tab reads here, without a
// second query implementation. The returned element type stays unexported (its
// fields are public with json tags, so it still marshals correctly) -- callers
// hold the slice and re-encode it rather than naming the type.
func ListFeatureGpsCaptures(ctx context.Context, db bun.IDB, classCode, featureID string) ([]gpsCaptureEntry, error) {
	var entries []gpsCaptureEntry
	err := db.NewSelect().
		ColumnExpr("c.id, c.hdop, c.pdop, c.vdop, c.h_accuracy_m, c.v_accuracy_m, c.altitude_m, "+
			"c.sats_used, c.sats_in_view, c.fix_type, c.source, c.captured_by, "+
			"u.display_name AS captured_by_name, u.email AS captured_by_email, "+
			"c.captured_at, c.recorded_at").
		TableExpr("gis.feature_gps_captures AS c").
		Join("LEFT JOIN iam.users AS u ON u.id = c.captured_by").
		Where("c.feature_class_code = ?", classCode).
		Where("c.feature_id = ?", featureID).
		OrderExpr("c.captured_at DESC, c.id DESC").
		Scan(ctx, &entries)
	if err != nil {
		return nil, fmt.Errorf("crud: list gps captures: %w", err)
	}
	return entries, nil
}
