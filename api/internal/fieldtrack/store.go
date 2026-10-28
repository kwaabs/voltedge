// Package fieldtrack is the field breadcrumb audit trail (migration 0178): the
// record of where a field worker physically was while doing field work, so a
// supervisor can map how the work was actually done — were they at the site, how
// did they get there, where did they pass.
//
// Two record types, written through two channels:
//   - MOVEMENT TRAIL (ops.field_track_points) — a stream of GPS fixes the device
//     drops during a field session. Uploaded through a light, loss-tolerant
//     channel (IngestBreadcrumbs, from the fieldsync bearer surface): high-volume,
//     latest-value, a dropped batch is re-uploaded by device-minted point id.
//   - ACTION WAYPOINTS (ops.field_action_waypoints) — where the worker was at each
//     mutating sync op. RecordActionWaypoint is called by the sync drain worker as
//     it applies each op, so the location rides the DURABLE sync inbox and is never
//     lost. One row per op (op_id PK), idempotent on replay.
//
// The web supervisor view reads both through this package (ListWorkers / ListSessions
// / GetTrail), gated field:tracks:read on the /field-tracks mount. The field user
// pushes their own trail/waypoints under their existing features:write — seeing
// someone else's trail is the privileged action, not recording one's own.
package fieldtrack

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// BreadcrumbInput is one movement-trail point from a device. ID is device-minted
// (a uuid per point) so a re-upload is idempotent. RecordedAt is the device clock
// at the fix.
type BreadcrumbInput struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	DeviceID   string    `json:"device_id"`
	RecordedAt time.Time `json:"recorded_at"`
	Longitude  float64   `json:"longitude"`
	Latitude   float64   `json:"latitude"`
	AccuracyM  *float64  `json:"accuracy_m"`
	SpeedMPS   *float64  `json:"speed_mps"`
	Source     string    `json:"source"`
}

// IngestResult is the outcome of a breadcrumb upload: how many of the batch were
// newly stored vs already present (a safe re-upload).
type IngestResult struct {
	Accepted int `json:"accepted"`
	Received int `json:"received"`
}

// IngestBreadcrumbs stores a batch of movement-trail points for the field worker,
// idempotent by device-minted id (ON CONFLICT DO NOTHING). A point missing its id,
// session or a valid coordinate is skipped (counted in neither total) rather than
// failing the batch — the trail is loss-tolerant, one bad fix must not reject the
// rest.
func IngestBreadcrumbs(ctx context.Context, db bun.IDB, userID, deviceID string, points []BreadcrumbInput) (IngestResult, error) {
	var res IngestResult
	if len(points) == 0 {
		return res, nil
	}
	// Build one multi-row insert; parameters are bound, geom built from bound
	// lon/lat via ST_MakePoint. Skipped points don't contribute placeholders.
	cols := "(id, user_id, session_id, device_id, recorded_at, geom, accuracy_m, speed_mps, source)"
	vals := make([]string, 0, len(points))
	args := make([]any, 0, len(points)*8)
	i := 1
	for _, p := range points {
		if p.ID == "" || p.SessionID == "" {
			continue
		}
		dev := deviceID
		if p.DeviceID != "" {
			dev = p.DeviceID
		}
		when := p.RecordedAt
		if when.IsZero() {
			when = time.Now()
		}
		vals = append(vals, fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, ST_SetSRID(ST_MakePoint($%d, $%d), 4326), $%d, $%d, $%d)",
			i, i+1, i+2, i+3, i+4, i+5, i+6, i+7, i+8, i+9))
		args = append(args, p.ID, userID, p.SessionID, nilIfEmpty(dev), when, p.Longitude, p.Latitude, p.AccuracyM, p.SpeedMPS, nilIfEmpty(p.Source))
		i += 10
		res.Received++
	}
	if res.Received == 0 {
		return res, nil
	}
	q := "INSERT INTO ops.field_track_points " + cols + " VALUES " + strings.Join(vals, ", ") + " ON CONFLICT (id) DO NOTHING"
	r, err := db.ExecContext(ctx, q, args...)
	if err != nil {
		return res, fmt.Errorf("fieldtrack: inserting breadcrumbs: %w", err)
	}
	n, _ := r.RowsAffected()
	res.Accepted = int(n)
	return res, nil
}

// ActionLocationInput is where the worker was when a mutating sync op was made. It
// rides nested under the op payload's "actor_location" key (like a gps_capture),
// so the sync drain worker can stamp the op's waypoint as it applies it.
type ActionLocationInput struct {
	SessionID  string    `json:"session_id"`
	DeviceID   string    `json:"device_id"`
	RecordedAt time.Time `json:"recorded_at"`
	Longitude  float64   `json:"longitude"`
	Latitude   float64   `json:"latitude"`
	AccuracyM  *float64  `json:"accuracy_m"`
}

// RecordActionWaypoint stamps where the worker was for one applied sync op. Keyed
// by op_id (idempotent: a replayed op re-records the same row via ON CONFLICT DO
// NOTHING). Called best-effort by the drain worker after an op applies — a
// waypoint write must never fail the op it describes, so callers log-and-ignore.
func RecordActionWaypoint(ctx context.Context, db bun.IDB, opID, userID, kind, featureClass, featureID string, loc ActionLocationInput) error {
	when := loc.RecordedAt
	if when.IsZero() {
		when = time.Now()
	}
	_, err := db.ExecContext(ctx, `
INSERT INTO ops.field_action_waypoints
    (op_id, user_id, session_id, device_id, kind, feature_class, feature_id, recorded_at, geom, accuracy_m)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, ST_SetSRID(ST_MakePoint($9, $10), 4326), $11)
ON CONFLICT (op_id) DO NOTHING`,
		opID, userID, nilIfEmpty(loc.SessionID), nilIfEmpty(loc.DeviceID), kind,
		nilIfEmpty(featureClass), nilIfEmpty(featureID), when, loc.Longitude, loc.Latitude, loc.AccuracyM)
	if err != nil {
		return fmt.Errorf("fieldtrack: recording action waypoint for op %s: %w", opID, err)
	}
	return nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
