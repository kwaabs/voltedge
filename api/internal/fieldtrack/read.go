package fieldtrack

// The supervisor read side (gated field:tracks:read on the /field-tracks mount):
// pick a worker → their field sessions in a window → the trail (breadcrumb line +
// located action pins) for one session. Breadcrumbs and waypoints are stored as
// PostGIS points; the reads project them to lon/lat for the web map.

import (
	"context"
	"fmt"
	"time"

	"github.com/uptrace/bun"
)

// Worker is a field user who has recorded a trail — the picker row.
type Worker struct {
	UserID      string    `json:"user_id"`
	DisplayName *string   `json:"display_name"`
	Email       *string   `json:"email"`
	LastSeen    time.Time `json:"last_seen"`
	SessionDays int       `json:"session_days"`
}

// ListWorkers returns the field users who have any breadcrumb, most-recently-active
// first — the "whose trail do you want?" picker. Names joined from iam.users.
func ListWorkers(ctx context.Context, db bun.IDB) ([]Worker, error) {
	var out []Worker
	err := db.NewSelect().
		ColumnExpr("p.user_id::text AS user_id").
		ColumnExpr("u.display_name AS display_name").
		ColumnExpr("u.email AS email").
		ColumnExpr("max(p.recorded_at) AS last_seen").
		ColumnExpr("count(DISTINCT date_trunc('day', p.recorded_at)) AS session_days").
		TableExpr("ops.field_track_points AS p").
		Join("LEFT JOIN iam.users AS u ON u.id = p.user_id").
		GroupExpr("p.user_id, u.display_name, u.email").
		OrderExpr("max(p.recorded_at) DESC").
		Scan(ctx, &out)
	if err != nil {
		return nil, fmt.Errorf("fieldtrack: listing workers: %w", err)
	}
	return out, nil
}

// Session summarises one field session (one device-minted session_id): its span,
// how many breadcrumbs and located actions it holds.
type Session struct {
	SessionID   string    `json:"session_id"`
	UserID      string    `json:"user_id"`
	StartedAt   time.Time `json:"started_at"`
	EndedAt     time.Time `json:"ended_at"`
	PointCount  int       `json:"point_count"`
	ActionCount int       `json:"action_count"`
}

// ListSessions returns a worker's sessions overlapping [from, to), newest-first.
// A zero `from`/`to` is unbounded on that side.
func ListSessions(ctx context.Context, db bun.IDB, userID string, from, to time.Time) ([]Session, error) {
	q := db.NewSelect().
		ColumnExpr("p.session_id::text AS session_id").
		ColumnExpr("p.user_id::text AS user_id").
		ColumnExpr("min(p.recorded_at) AS started_at").
		ColumnExpr("max(p.recorded_at) AS ended_at").
		ColumnExpr("count(*) AS point_count").
		ColumnExpr("(SELECT count(*) FROM ops.field_action_waypoints w WHERE w.session_id = p.session_id) AS action_count").
		TableExpr("ops.field_track_points AS p").
		Where("p.user_id = ?", userID).
		GroupExpr("p.session_id, p.user_id").
		OrderExpr("min(p.recorded_at) DESC")
	if !from.IsZero() {
		q = q.Having("max(p.recorded_at) >= ?", from)
	}
	if !to.IsZero() {
		q = q.Having("min(p.recorded_at) < ?", to)
	}
	var out []Session
	if err := q.Scan(ctx, &out); err != nil {
		return nil, fmt.Errorf("fieldtrack: listing sessions: %w", err)
	}
	return out, nil
}

// TrackPoint is one breadcrumb projected to lon/lat for the map.
type TrackPoint struct {
	RecordedAt time.Time `json:"recorded_at"`
	Longitude  float64   `json:"longitude"`
	Latitude   float64   `json:"latitude"`
	// bun's name deriver yields "accuracym" (no underscore before a trailing
	// single capital), so pin the column explicitly or the scan can't map it.
	AccuracyM *float64 `bun:"accuracy_m" json:"accuracy_m"`
	SpeedMPS  *float64 `json:"speed_mps"`
}

// Waypoint is one located edit projected to lon/lat, with the feature it changed.
type Waypoint struct {
	OpID         string    `json:"op_id"`
	Kind         string    `json:"kind"`
	FeatureClass *string   `json:"feature_class"`
	FeatureID    *string   `json:"feature_id"`
	RecordedAt   time.Time `json:"recorded_at"`
	Longitude    float64   `json:"longitude"`
	Latitude     float64   `json:"latitude"`
	AccuracyM    *float64  `bun:"accuracy_m" json:"accuracy_m"`
}

// Trail is one session's full picture: the ordered breadcrumb line and the located
// action pins.
type Trail struct {
	SessionID string       `json:"session_id"`
	Points    []TrackPoint `json:"points"`
	Waypoints []Waypoint   `json:"waypoints"`
}

// GetTrail returns a session's breadcrumbs (time-ordered) and its action waypoints.
func GetTrail(ctx context.Context, db bun.IDB, sessionID string) (*Trail, error) {
	trail := &Trail{SessionID: sessionID, Points: []TrackPoint{}, Waypoints: []Waypoint{}}

	if err := db.NewSelect().
		ColumnExpr("recorded_at").
		ColumnExpr("ST_X(geom) AS longitude").
		ColumnExpr("ST_Y(geom) AS latitude").
		ColumnExpr("accuracy_m").
		ColumnExpr("speed_mps").
		TableExpr("ops.field_track_points").
		Where("session_id = ?", sessionID).
		OrderExpr("recorded_at").
		Scan(ctx, &trail.Points); err != nil {
		return nil, fmt.Errorf("fieldtrack: loading trail points: %w", err)
	}

	if err := db.NewSelect().
		ColumnExpr("op_id").
		ColumnExpr("kind").
		ColumnExpr("feature_class").
		ColumnExpr("feature_id").
		ColumnExpr("recorded_at").
		ColumnExpr("ST_X(geom) AS longitude").
		ColumnExpr("ST_Y(geom) AS latitude").
		ColumnExpr("accuracy_m").
		TableExpr("ops.field_action_waypoints").
		Where("session_id = ?", sessionID).
		OrderExpr("recorded_at").
		Scan(ctx, &trail.Waypoints); err != nil {
		return nil, fmt.Errorf("fieldtrack: loading trail waypoints: %w", err)
	}

	return trail, nil
}
