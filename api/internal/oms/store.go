package oms

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/trace"
	"github.com/uptrace/bun"
)

// troubleCallColumns is the scalar read shape for a trouble call; the PostGIS
// `location` is never scanned directly (it has no Go field) -- it is only read
// back through ST_X/ST_Y aliased to the scanonly Longitude/Latitude fields, the
// same split api/internal/territory uses for its boundary geometry.
const troubleCallColumns = "id, service_point_id, address, reported_at, caller_ref, call_type, status, incident_id, notes, created_at, updated_at"

func selectTroubleCalls(db bun.IDB, dst any) *bun.SelectQuery {
	return db.NewSelect().Model(dst).
		ColumnExpr(troubleCallColumns).
		ColumnExpr("ST_X(location) AS longitude").
		ColumnExpr("ST_Y(location) AS latitude")
}

// ---------------------------------------------------------------------------
// Trouble calls
// ---------------------------------------------------------------------------

// CreateCallParams is the input to CreateTroubleCall. SP-optional intake: at
// least one locator (a service point id, an address, or a lat/lng pair) must be
// present. ReportedAt/CallType default at the DB when nil/empty.
type CreateCallParams struct {
	ServicePointID *string
	Address        *string
	Longitude      *float64
	Latitude       *float64
	ReportedAt     *time.Time
	CallerRef      *string
	CallType       string
	Notes          *string
}

// CreateTroubleCall logs one customer report. It validates the SP-optional
// locator rule and normalises the call type, then inserts -- letting the DB
// defaults for id, reported_at (when unset), and status='open' fire, the same
// column-listing approach circuit/territory use. The location, when a lat/lng
// is given, is built with ST_MakePoint (never bound through the scanonly
// Longitude/Latitude fields).
func CreateTroubleCall(ctx context.Context, db bun.IDB, p CreateCallParams) (*TroubleCall, error) {
	hasSP := p.ServicePointID != nil && strings.TrimSpace(*p.ServicePointID) != ""
	hasAddr := p.Address != nil && strings.TrimSpace(*p.Address) != ""
	hasLng := p.Longitude != nil
	hasLat := p.Latitude != nil
	if hasLng != hasLat {
		return nil, invalidf("a trouble call location needs both longitude and latitude, or neither")
	}
	if !hasSP && !hasAddr && !(hasLng && hasLat) {
		return nil, invalidf("a trouble call needs at least one locator: a service point, an address, or a lat/lng")
	}

	callType := strings.TrimSpace(p.CallType)
	if callType == "" {
		callType = CallTypeNoSupply
	}
	if err := validateCallType(callType); err != nil {
		return nil, err
	}

	cols := []string{"call_type", "status"}
	vals := []string{"?", "?"}
	args := []any{callType, CallStatusOpen}

	add := func(col, val string, arg any) {
		cols = append(cols, col)
		vals = append(vals, val)
		args = append(args, arg)
	}
	if hasSP {
		add("service_point_id", "?", strings.TrimSpace(*p.ServicePointID))
	}
	if hasAddr {
		add("address", "?", strings.TrimSpace(*p.Address))
	}
	if hasLng && hasLat {
		cols = append(cols, "location")
		vals = append(vals, "ST_SetSRID(ST_MakePoint(?, ?), 4326)")
		args = append(args, *p.Longitude, *p.Latitude)
	}
	if p.ReportedAt != nil {
		add("reported_at", "?", *p.ReportedAt)
	}
	if p.CallerRef != nil {
		add("caller_ref", "?", strings.TrimSpace(*p.CallerRef))
	}
	if p.Notes != nil {
		add("notes", "?", *p.Notes)
	}

	query := fmt.Sprintf(
		"INSERT INTO oms.trouble_calls (%s) VALUES (%s) RETURNING id",
		strings.Join(cols, ", "), strings.Join(vals, ", "),
	)
	var id string
	if err := db.QueryRowContext(ctx, query, args...).Scan(&id); err != nil {
		return nil, fmt.Errorf("oms: inserting trouble call: %w", err)
	}
	return GetTroubleCall(ctx, db, id)
}

// GetTroubleCall fetches one call by id, location projected to lng/lat.
func GetTroubleCall(ctx context.Context, db bun.IDB, id string) (*TroubleCall, error) {
	c := new(TroubleCall)
	if err := selectTroubleCalls(db, c).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("trouble call %q does not exist", id)
		}
		return nil, fmt.Errorf("oms: loading trouble call %s: %w", id, err)
	}
	return c, nil
}

// ListTroubleCalls returns calls newest-first, optionally filtered by status.
func ListTroubleCalls(ctx context.Context, db bun.IDB, status string) ([]TroubleCall, error) {
	var calls []TroubleCall
	q := selectTroubleCalls(db, &calls).Order("reported_at DESC")
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("oms: listing trouble calls: %w", err)
	}
	return calls, nil
}

// AssociateTroubleCall resolves a call's service point so it can feed the
// prediction rollup -- either to an explicitly supplied SP (manual association)
// or, when snap is true, to the nearest service point to the call's own
// location. It does NOT change the call's status (a resolved-but-not-yet-rolled
// call is still 'open'); rolling into an incident is PredictAndPersist's job.
func AssociateTroubleCall(ctx context.Context, db bun.IDB, id string, servicePointID *string, snap bool) (*TroubleCall, error) {
	call, err := GetTroubleCall(ctx, db, id)
	if err != nil {
		return nil, err
	}
	if call.Status == CallStatusCancelled || call.Status == CallStatusCleared {
		return nil, invalidf("trouble call %q is %s and cannot be re-associated", id, call.Status)
	}

	var spID string
	switch {
	case snap:
		if call.Longitude == nil || call.Latitude == nil {
			return nil, invalidf("cannot snap trouble call %q: it has no location", id)
		}
		// Nearest service point by KNN distance to the call's stored location.
		const nearest = `
SELECT id FROM gis.service_point
WHERE deleted_at IS NULL AND geom IS NOT NULL
ORDER BY geom <-> (SELECT location FROM oms.trouble_calls WHERE id = ?)
LIMIT 1`
		if err := db.QueryRowContext(ctx, nearest, id).Scan(&spID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, invalidf("no service point exists to snap trouble call %q to", id)
			}
			return nil, fmt.Errorf("oms: snapping trouble call %s: %w", id, err)
		}
	case servicePointID != nil && strings.TrimSpace(*servicePointID) != "":
		spID = strings.TrimSpace(*servicePointID)
		if err := ensureServicePointExists(ctx, db, spID); err != nil {
			return nil, err
		}
	default:
		return nil, invalidf("associate needs either a service_point_id or snap=true")
	}

	if _, err := db.NewUpdate().Model((*TroubleCall)(nil)).
		Set("service_point_id = ?", spID).
		Set("updated_at = now()").
		Where("id = ?", id).Exec(ctx); err != nil {
		return nil, fmt.Errorf("oms: associating trouble call %s: %w", id, err)
	}
	return GetTroubleCall(ctx, db, id)
}

// ---------------------------------------------------------------------------
// Incidents
// ---------------------------------------------------------------------------

// GetIncident fetches one incident by id.
func GetIncident(ctx context.Context, db bun.IDB, id string) (*Incident, error) {
	inc := new(Incident)
	if err := db.NewSelect().Model(inc).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("incident %q does not exist", id)
		}
		return nil, fmt.Errorf("oms: loading incident %s: %w", id, err)
	}
	return inc, nil
}

// AffectedArea is an incident's geographic impact: the blamed device (if any)
// plus the point geometry of every service point in its outage cone -- what the
// map overlay draws when an incident is selected.
type AffectedArea struct {
	IncidentID    string                 `json:"incident_id"`
	DeviceClass   *string                `json:"device_class"`
	DeviceID      *string                `json:"device_id"`
	ServicePoints []AffectedServicePoint `json:"service_points"`
	// Service is the Phase 62 consumption rollup of the affected cone -- load +
	// customers split by restoration priority, so the console can report how
	// many CRITICAL customers this incident hit, not just a raw count. Frozen
	// or live, it rolls up whatever ids the area resolved to. See
	// trace.ServiceRollup.
	Service *trace.ServiceRollup `json:"service,omitempty"`
}

// IncidentAffectedArea resolves an incident's affected service points for the
// map overlay. It prefers the FROZEN set once interruptions have been
// materialised (confirm onward), so a restored/re-modelled network still
// highlights exactly who was out; before that (a still-predicted incident) it
// falls back to a LIVE downstream trace from the blamed device. An incident
// with neither frozen rows nor a device yields an empty (but non-null) set.
func IncidentAffectedArea(ctx context.Context, db bun.IDB, id string) (*AffectedArea, error) {
	inc, err := GetIncident(ctx, db, id)
	if err != nil {
		return nil, err
	}

	// Frozen snapshot first (present once confirmed), each supply carrying the
	// class it was frozen as (Phase 63) so a PRIMARY-METER supply resolves to
	// gis.meter, not gis.service_point.
	var frozen []struct {
		FeatureClass   string `bun:"feature_class"`
		ServicePointID string `bun:"service_point_id"`
	}
	if err := db.NewSelect().
		ColumnExpr("feature_class, service_point_id").
		TableExpr("oms.customer_interruptions").
		Where("incident_id = ?", id).
		Scan(ctx, &frozen); err != nil {
		return nil, fmt.Errorf("oms: loading interruptions for incident %s: %w", id, err)
	}
	spRefs := make([]trace.FeatureRef, 0, len(frozen))
	for _, f := range frozen {
		spRefs = append(spRefs, trace.FeatureRef{FeatureClass: f.FeatureClass, FeatureID: f.ServicePointID})
	}

	// No frozen rows yet -> live downstream trace from the blamed device.
	if len(spRefs) == 0 && inc.PredictedDeviceClass != nil && inc.PredictedDeviceID != nil {
		supplies, err := DownstreamSupplies(ctx, db, *inc.PredictedDeviceClass, *inc.PredictedDeviceID)
		if err != nil {
			return nil, err
		}
		for _, s := range supplies {
			spRefs = append(spRefs, trace.FeatureRef{FeatureClass: s.FeatureClass, FeatureID: s.FeatureID})
		}
	}

	sps, err := AffectedSupplyGeometry(ctx, db, spRefs)
	if err != nil {
		return nil, err
	}

	// Phase 62/63: roll the affected supplies up by restoration tier. Same shared
	// engine the live energization uses, so "critical customers affected" means
	// the same thing on the frozen snapshot as on the live cone.
	rollup, err := trace.SummarizeService(ctx, db, spRefs)
	if err != nil {
		return nil, fmt.Errorf("oms: rolling up affected load for incident %s: %w", id, err)
	}

	return &AffectedArea{
		IncidentID:    id,
		DeviceClass:   inc.PredictedDeviceClass,
		DeviceID:      inc.PredictedDeviceID,
		ServicePoints: sps,
		Service:       rollup,
	}, nil
}

// ListIncidents returns incidents newest-first. status filters exactly; when
// openOnly is true only unrestored incidents are returned (the active board).
func ListIncidents(ctx context.Context, db bun.IDB, status string, openOnly bool) ([]Incident, error) {
	var incidents []Incident
	q := db.NewSelect().Model(&incidents).Order("started_at DESC")
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if openOnly {
		q = q.Where("restored_at IS NULL")
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("oms: listing incidents: %w", err)
	}
	return incidents, nil
}

// PredictAndPersist is the /incidents/predict action: it rolls every open,
// SP-resolved trouble call up into predicted incidents (PredictIncidents) and
// persists them in one transaction -- inserting an oms.incidents row per
// cluster and linking its calls (status -> associated, incident_id set). It
// operates only on status='open' calls, so re-running it picks up newly logged
// calls without duplicating incidents for calls already rolled up. It does NOT
// materialise customer_interruptions (that happens on confirm) and touches
// nothing in the live network. Returns the incidents it created.
func PredictAndPersist(ctx context.Context, db *bun.DB) ([]Incident, error) {
	var created []Incident
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Open calls with a resolved service point -- the only calls that roll up.
		var rows []struct {
			ID             string `bun:"id"`
			ServicePointID string `bun:"service_point_id"`
		}
		if err := tx.NewSelect().
			ColumnExpr("id, service_point_id").
			TableExpr("oms.trouble_calls").
			Where("status = ?", CallStatusOpen).
			Where("service_point_id IS NOT NULL").
			Scan(ctx, &rows); err != nil {
			return fmt.Errorf("oms: loading open calls: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}

		calls := make([]CallLocation, len(rows))
		for i, r := range rows {
			calls[i] = CallLocation{CallID: r.ID, ServicePointID: r.ServicePointID}
		}
		predictions, err := PredictIncidents(ctx, tx, calls)
		if err != nil {
			return err
		}

		for _, p := range predictions {
			var deviceClass, deviceID *string
			if p.Device != nil {
				deviceClass = &p.Device.FeatureClass
				deviceID = &p.Device.FeatureID
			}
			var confidence *float64
			if p.Device != nil {
				c := p.Confidence
				confidence = &c
			}

			var incidentID string
			const insertInc = `
INSERT INTO oms.incidents
    (predicted_device_class, predicted_device_id, status, confidence,
     affected_customers, affected_service_points)
VALUES (?, ?, 'predicted', ?, ?, ?)
RETURNING id`
			if err := tx.QueryRowContext(ctx, insertInc,
				deviceClass, deviceID, confidence,
				p.AffectedCustomers, p.AffectedServicePoints,
			).Scan(&incidentID); err != nil {
				return fmt.Errorf("oms: inserting predicted incident: %w", err)
			}

			if _, err := tx.NewUpdate().Model((*TroubleCall)(nil)).
				Set("status = ?", CallStatusAssociated).
				Set("incident_id = ?", incidentID).
				Set("updated_at = now()").
				Where("id IN (?)", bun.In(p.CallIDs)).Exec(ctx); err != nil {
				return fmt.Errorf("oms: linking calls to incident %s: %w", incidentID, err)
			}

			inc, err := GetIncident(ctx, tx, incidentID)
			if err != nil {
				return err
			}
			created = append(created, *inc)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}

// interruptionRow is the insert shape for oms.customer_interruptions (Phase 63:
// feature_class distinguishes a SERVICE_POINT supply from a PRIMARY-METER one).
// A bare struct with ModelTableExpr rather than a registered bun model, since
// this table is otherwise only ever read via raw SQL in indices.go.
type interruptionRow struct {
	IncidentID     string    `bun:"incident_id"`
	FeatureClass   string    `bun:"feature_class"`
	ServicePointID string    `bun:"service_point_id"`
	Customers      int       `bun:"customers"`
	InterruptedAt  time.Time `bun:"interrupted_at"`
}

// ConfirmIncident advances an incident to confirmed and materialises the
// per-supply customer_interruptions the reliability indices sum over: it
// re-traces the blamed/chosen device's downstream supplies (SERVICE_POINTs and
// PRIMARY METERs) and freezes each one's customer count (null -> 1). A manually
// raised incident with no device writes no interruptions.
//
// It confirms both a PREDICTED unplanned incident (the interruption began at the
// prediction time, inc.StartedAt) and a SCHEDULED planned outage (confirm = "the
// planned outage is executing NOW", so both started_at and the interruption
// clock are reset to now). No other status can be confirmed.
func ConfirmIncident(ctx context.Context, db *bun.DB, id string) (*Incident, error) {
	var out *Incident
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		inc, err := GetIncident(ctx, tx, id)
		if err != nil {
			return err
		}
		if inc.Status != StatusPredicted && inc.Status != StatusScheduled {
			return invalidf("incident %q is %s; only a predicted or scheduled incident can be confirmed", id, inc.Status)
		}

		// A planned outage's clock starts when it is executed (now), not when it
		// was booked; an unplanned outage started when the engine predicted it.
		planned := inc.OutageType == OutageTypePlanned
		interruptedAt := inc.StartedAt
		if planned {
			interruptedAt = time.Now()
		}

		if inc.PredictedDeviceClass != nil && inc.PredictedDeviceID != nil {
			supplies, err := DownstreamSupplies(ctx, tx, *inc.PredictedDeviceClass, *inc.PredictedDeviceID)
			if err != nil {
				return err
			}
			if len(supplies) > 0 {
				// Freeze one row per counting load leaf (SERVICE_POINT or PRIMARY
				// METER), recording its class so the map overlay can draw it back
				// and its customer count exactly as the shared rollup computed it
				// -- no re-query of the gis tables, so the frozen customers can
				// never disagree with the affected/energization counts that used
				// the same trace.ServicePoint lines. ON CONFLICT keys on the
				// widened (incident, class, id) uniqueness from migration 0119.
				rows := make([]interruptionRow, 0, len(supplies))
				for _, s := range supplies {
					rows = append(rows, interruptionRow{
						IncidentID:     id,
						FeatureClass:   s.FeatureClass,
						ServicePointID: s.FeatureID,
						Customers:      s.Customers,
						InterruptedAt:  interruptedAt,
					})
				}
				if _, err := tx.NewInsert().
					Model(&rows).
					ModelTableExpr("oms.customer_interruptions").
					On("CONFLICT (incident_id, feature_class, service_point_id) DO NOTHING").
					Exec(ctx); err != nil {
					return fmt.Errorf("oms: materialising interruptions for incident %s: %w", id, err)
				}
			}
		}

		upd := tx.NewUpdate().Model((*Incident)(nil)).
			Set("status = ?", StatusConfirmed).
			Set("updated_at = now()")
		if planned {
			upd = upd.Set("started_at = ?", interruptedAt)
		}
		if _, err := upd.Where("id = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("oms: confirming incident %s: %w", id, err)
		}
		out, err = GetIncident(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CreatePlannedOutage raises a scheduled planned outage around operating one
// device. Unlike a predicted incident it is created directly (not rolled up from
// calls): the caller names the device whose operation causes the outage and the
// booked window, and the engine traces the downstream cone for the affected
// snapshot. It starts at status 'scheduled' and materialises no interruptions
// until confirmed (executed) -- see ConfirmIncident.
type PlannedOutageParams struct {
	DeviceClass  string
	DeviceID     string
	PlannedStart *time.Time
	PlannedEnd   *time.Time
	CauseCode    *string
	CircuitID    *string
	Notes        *string
}

func CreatePlannedOutage(ctx context.Context, db *bun.DB, p PlannedOutageParams) (*Incident, error) {
	if strings.TrimSpace(p.DeviceClass) == "" || strings.TrimSpace(p.DeviceID) == "" {
		return nil, invalidf("a planned outage needs a device (device_class and device_id)")
	}
	if p.PlannedStart != nil && p.PlannedEnd != nil && !p.PlannedStart.Before(*p.PlannedEnd) {
		return nil, invalidf("planned_start must be before planned_end")
	}

	device := trace.FeatureRef{FeatureClass: strings.TrimSpace(p.DeviceClass), FeatureID: strings.TrimSpace(p.DeviceID)}
	spCount, customers, err := AffectedCustomers(ctx, db, device)
	if err != nil {
		return nil, err
	}

	var id string
	const insertInc = `
INSERT INTO oms.incidents
    (predicted_device_class, predicted_device_id, circuit_id, outage_type, cause_code,
     status, planned_start, planned_end, affected_customers, affected_service_points, notes)
VALUES (?, ?, ?, 'planned', ?, 'scheduled', ?, ?, ?, ?, ?)
RETURNING id`
	if err := db.QueryRowContext(ctx, insertInc,
		device.FeatureClass, device.FeatureID, p.CircuitID, p.CauseCode,
		p.PlannedStart, p.PlannedEnd, customers, spCount, p.Notes,
	).Scan(&id); err != nil {
		return nil, fmt.Errorf("oms: creating planned outage: %w", err)
	}
	return GetIncident(ctx, db, id)
}

// RestoreIncident marks an incident restored (now), closing every open
// interruption window it owns. A restored or cancelled incident cannot be
// restored again.
func RestoreIncident(ctx context.Context, db *bun.DB, id string) (*Incident, error) {
	var out *Incident
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		inc, err := GetIncident(ctx, tx, id)
		if err != nil {
			return err
		}
		if inc.Status == StatusRestored {
			return invalidf("incident %q is already restored", id)
		}
		if inc.Status == StatusCancelled {
			return invalidf("incident %q is cancelled and cannot be restored", id)
		}

		if _, err := tx.NewUpdate().Model((*Incident)(nil)).
			Set("status = ?", StatusRestored).
			Set("restored_at = now()").
			Set("updated_at = now()").
			Where("id = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("oms: restoring incident %s: %w", id, err)
		}
		if _, err := tx.NewUpdate().Model((*CustomerInterruption)(nil)).
			Set("restored_at = now()").
			Where("incident_id = ?", id).
			Where("restored_at IS NULL").Exec(ctx); err != nil {
			return fmt.Errorf("oms: closing interruptions for incident %s: %w", id, err)
		}
		out, err = GetIncident(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CancelIncident marks a (mis-predicted) incident cancelled and releases its
// linked calls back to 'open' so they can re-roll into a corrected incident. A
// restored incident cannot be cancelled.
func CancelIncident(ctx context.Context, db *bun.DB, id string) (*Incident, error) {
	var out *Incident
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		inc, err := GetIncident(ctx, tx, id)
		if err != nil {
			return err
		}
		if inc.Status == StatusRestored {
			return invalidf("incident %q is restored and cannot be cancelled", id)
		}
		if inc.Status == StatusCancelled {
			return invalidf("incident %q is already cancelled", id)
		}

		if _, err := tx.NewUpdate().Model((*Incident)(nil)).
			Set("status = ?", StatusCancelled).
			Set("updated_at = now()").
			Where("id = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("oms: cancelling incident %s: %w", id, err)
		}
		if _, err := tx.NewUpdate().Model((*TroubleCall)(nil)).
			Set("status = ?", CallStatusOpen).
			Set("incident_id = NULL").
			Set("updated_at = now()").
			Where("incident_id = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("oms: releasing calls from incident %s: %w", id, err)
		}
		out, err = GetIncident(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func validateCallType(t string) error {
	switch t {
	case CallTypeNoSupply, CallTypePartial, CallTypeFlicker:
		return nil
	default:
		return invalidf("call_type must be one of no_supply/partial/flicker, got %q", t)
	}
}

// ensureServicePointExists rejects a manual association to an SP that isn't a
// live service point.
func ensureServicePointExists(ctx context.Context, db bun.IDB, id string) error {
	var exists bool
	const q = `SELECT EXISTS (SELECT 1 FROM gis.service_point WHERE id = ? AND deleted_at IS NULL)`
	if err := db.QueryRowContext(ctx, q, id).Scan(&exists); err != nil {
		return fmt.Errorf("oms: checking service point %s: %w", id, err)
	}
	if !exists {
		return invalidf("service point %q does not exist", id)
	}
	return nil
}
