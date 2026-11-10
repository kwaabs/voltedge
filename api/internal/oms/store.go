package oms

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/trace"
	"github.com/kwaabs/voltedge/api/internal/webhookout"
	"github.com/uptrace/bun"
)

// troubleCallColumns is the scalar read shape for a trouble call; the PostGIS
// `location` is never scanned directly (it has no Go field) -- it is only read
// back through ST_X/ST_Y aliased to the scanonly Longitude/Latitude fields, the
// same split api/internal/territory uses for its boundary geometry.
const troubleCallColumns = "id, service_point_id, address, reported_at, caller_ref, account_ref, phone, call_type, status, incident_id, notes, created_at, updated_at"

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
	AccountRef     *string
	Phone          *string
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
	if p.AccountRef != nil && strings.TrimSpace(*p.AccountRef) != "" {
		add("account_ref", "?", strings.TrimSpace(*p.AccountRef))
	}
	if p.Phone != nil && strings.TrimSpace(*p.Phone) != "" {
		add("phone", "?", strings.TrimSpace(*p.Phone))
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
	call, err := GetTroubleCall(ctx, db, id)
	if err != nil {
		return nil, err
	}
	// Timeline: the call's first event, at its reported time. The source note
	// tells a machine report (AMI last-gasp) from a phoned-in one.
	srcNote := "phoned in"
	if call.CallerRef != nil && *call.CallerRef == lastGaspCallerRef {
		srcNote = "AMI last-gasp"
	}
	if err := recordEventAt(ctx, db, EntityCall, call.ID, EventCallReceived, call.ReportedAt, noteOf(srcNote)); err != nil {
		return nil, err
	}
	return call, nil
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
// TroubleCallFilter scopes and paginates the call queue. Status/IncidentID
// filter; Limit>0 pages the result (with Offset) -- so a storm's call volume is
// fetched a page at a time, not all at once. Total (returned alongside) is the
// count under the filter ignoring Limit/Offset, for the page controls.
type TroubleCallFilter struct {
	Status     string
	IncidentID string
	Limit      int
	Offset     int
}

func ListTroubleCalls(ctx context.Context, db bun.IDB, f TroubleCallFilter) ([]TroubleCall, int, error) {
	applyWhere := func(q *bun.SelectQuery) *bun.SelectQuery {
		if f.Status != "" {
			q = q.Where("status = ?", f.Status)
		}
		if f.IncidentID != "" {
			q = q.Where("incident_id = ?", f.IncidentID)
		}
		return q
	}

	total, err := applyWhere(db.NewSelect().Model((*TroubleCall)(nil))).Count(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("oms: counting trouble calls: %w", err)
	}

	var calls []TroubleCall
	q := applyWhere(selectTroubleCalls(db, &calls)).Order("reported_at DESC")
	if f.Limit > 0 {
		q = q.Limit(f.Limit).Offset(f.Offset)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, 0, fmt.Errorf("oms: listing trouble calls: %w", err)
	}
	return calls, total, nil
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
	// Timeline: the call is now located to a service point (manual pick or snap).
	located := "→ SP " + spID
	if snap {
		located = "snapped → SP " + spID
	}
	if err := recordEvent(ctx, db, EntityCall, id, EventCallLocated, noteOf(located)); err != nil {
		return nil, err
	}
	return GetTroubleCall(ctx, db, id)
}

// CloseTroubleCall ends an OPEN call that doesn't belong on the board -- a false
// alarm / customer-back (status=cleared) or a test/duplicate/mistake
// (status=cancelled) -- without routing it through an incident. Only an open call
// can be closed here: a call already linked to an incident (associated) follows
// that incident's lifecycle and clears when it restores, and a call that's already
// terminal is a no-op error. Pure status change (no closed_at column), plus a
// timeline event.
func CloseTroubleCall(ctx context.Context, db bun.IDB, id, status string) (*TroubleCall, error) {
	if status != CallStatusCleared && status != CallStatusCancelled {
		return nil, invalidf("a trouble call can be closed as 'cleared' or 'cancelled', not %q", status)
	}
	call, err := GetTroubleCall(ctx, db, id)
	if err != nil {
		return nil, err
	}
	switch call.Status {
	case CallStatusCancelled, CallStatusCleared:
		return nil, invalidf("trouble call %q is already %s", id, call.Status)
	case CallStatusAssociated:
		return nil, invalidf("trouble call %q is linked to an incident; it clears when that incident is restored", id)
	}
	if _, err := db.NewUpdate().Model((*TroubleCall)(nil)).
		Set("status = ?", status).
		Set("updated_at = now()").
		Where("id = ?", id).Exec(ctx); err != nil {
		return nil, fmt.Errorf("oms: closing trouble call %s: %w", id, err)
	}
	if err := recordEvent(ctx, db, EntityCall, id, EventCallClosed, noteOf(status)); err != nil {
		return nil, err
	}
	return GetTroubleCall(ctx, db, id)
}

// ---------------------------------------------------------------------------
// Incidents
// ---------------------------------------------------------------------------

// GetIncident fetches one incident by id.
func GetIncident(ctx context.Context, db bun.IDB, id string) (*Incident, error) {
	inc := new(Incident)
	if err := db.NewSelect().Model(inc).
		ColumnExpr("inc.*").
		ColumnExpr("actor.display_name AS last_actor_name").
		ColumnExpr("terr.name AS territory_name").
		Join("LEFT JOIN iam.users AS actor ON actor.id::text = inc.last_actor_user_id").
		Join("LEFT JOIN gis.territories AS terr ON terr.id = inc.territory_id").
		Where("inc.id = ?", id).Scan(ctx); err != nil {
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
	// ByTerritory groups the affected customers up the geographic hierarchy
	// (region / district / ...), one entry per level that has affected supplies.
	// Empty when no territories are seeded or no affected supply is assigned.
	ByTerritory []AffectedTerritoryLevel `json:"by_territory"`
	// MissingGeometry is how many affected supplies no longer resolve to a map
	// point (soft-deleted or geometry-less) -- they still count toward the frozen
	// total but can't be drawn, so the console can say "N no longer on the map"
	// instead of silently showing fewer pins than the headline count.
	MissingGeometry int `json:"missing_geometry"`
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
	// gis.meter, not gis.service_point, and its frozen customer count so the
	// territory rollup matches the headline affected total exactly.
	var frozen []struct {
		FeatureClass   string `bun:"feature_class"`
		ServicePointID string `bun:"service_point_id"`
		Customers      int    `bun:"customers"`
	}
	if err := db.NewSelect().
		ColumnExpr("feature_class, service_point_id, customers").
		TableExpr("oms.customer_interruptions").
		Where("incident_id = ?", id).
		Scan(ctx, &frozen); err != nil {
		return nil, fmt.Errorf("oms: loading interruptions for incident %s: %w", id, err)
	}
	supplies := make([]affectedSupply, 0, len(frozen))
	for _, f := range frozen {
		supplies = append(supplies, affectedSupply{Class: f.FeatureClass, ID: f.ServicePointID, Customers: f.Customers})
	}

	// No frozen rows yet -> live downstream trace from the blamed device.
	if len(supplies) == 0 && inc.PredictedDeviceClass != nil && inc.PredictedDeviceID != nil {
		live, err := DownstreamSupplies(ctx, db, *inc.PredictedDeviceClass, *inc.PredictedDeviceID)
		if err != nil {
			return nil, err
		}
		for _, s := range live {
			supplies = append(supplies, affectedSupply{Class: s.FeatureClass, ID: s.FeatureID, Customers: s.Customers})
		}
	}

	spRefs := make([]trace.FeatureRef, len(supplies))
	for i, s := range supplies {
		spRefs[i] = trace.FeatureRef{FeatureClass: s.Class, FeatureID: s.ID}
	}

	sps, err := AffectedSupplyGeometry(ctx, db, spRefs)
	if err != nil {
		return nil, err
	}
	// Stamp each resolved supply with its class + frozen customer count (both live
	// in `supplies`, keyed by id) so the console can render a supply-by-supply
	// table, not just a sum. AffectedSupplyGeometry only knows the map geometry.
	byID := make(map[string]affectedSupply, len(supplies))
	for _, s := range supplies {
		byID[s.ID] = s
	}
	for i := range sps {
		if s, ok := byID[sps[i].ID]; ok {
			sps[i].FeatureClass = s.Class
			sps[i].Customers = s.Customers
		}
	}

	// Phase 62/63: roll the affected supplies up by restoration tier. Same shared
	// engine the live energization uses, so "critical customers affected" means
	// the same thing on the frozen snapshot as on the live cone.
	rollup, err := trace.SummarizeService(ctx, db, spRefs)
	if err != nil {
		return nil, fmt.Errorf("oms: rolling up affected load for incident %s: %w", id, err)
	}

	// Geographic rollup (region / district / ...) over the same supplies.
	byTerritory, err := territoryRollup(ctx, db, supplies)
	if err != nil {
		return nil, err
	}

	return &AffectedArea{
		IncidentID:    id,
		DeviceClass:   inc.PredictedDeviceClass,
		DeviceID:      inc.PredictedDeviceID,
		ServicePoints: sps,
		Service:       rollup,
		ByTerritory:   byTerritory,
		// Affected supplies that no longer resolve to a map point (deleted /
		// geometry-less): they count toward the frozen total but AffectedSupplyGeometry
		// drops them, so surface the gap rather than show fewer pins silently.
		MissingGeometry: len(spRefs) - len(sps),
	}, nil
}

// ListIncidents returns incidents newest-first. status filters exactly; when
// openOnly is true only unrestored incidents are returned (the active board).
// territoryID, when set, scopes to incidents in that territory's SUBTREE (the
// territory itself and every descendant), so filtering by a region catches its
// districts' incidents (0169); an unassigned incident is excluded from a scoped
// view.
func ListIncidents(ctx context.Context, db bun.IDB, status string, openOnly bool, territoryID string) ([]Incident, error) {
	var incidents []Incident
	q := db.NewSelect().Model(&incidents).
		ColumnExpr("inc.*").
		ColumnExpr("actor.display_name AS last_actor_name").
		ColumnExpr("terr.name AS territory_name").
		Join("LEFT JOIN iam.users AS actor ON actor.id::text = inc.last_actor_user_id").
		Join("LEFT JOIN gis.territories AS terr ON terr.id = inc.territory_id").
		Order("started_at DESC")
	if status != "" {
		q = q.Where("inc.status = ?", status)
	}
	if openOnly {
		q = q.Where("inc.restored_at IS NULL")
	}
	if territoryID != "" {
		subtree, err := territorySubtreeIDs(ctx, db, territoryID)
		if err != nil {
			return nil, err
		}
		q = q.Where("inc.territory_id IN (?)", bun.In(subtree))
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
		// caller_ref + reported_at come along so the predicted incident can be
		// stamped with its detection source (all-AMI cluster -> 'ami', else the
		// human-reported 'customer') and detected_at (its earliest call), 0168.
		var rows []struct {
			ID             string    `bun:"id"`
			ServicePointID string    `bun:"service_point_id"`
			CallerRef      *string   `bun:"caller_ref"`
			ReportedAt     time.Time `bun:"reported_at"`
		}
		if err := tx.NewSelect().
			ColumnExpr("id, service_point_id, caller_ref, reported_at").
			TableExpr("oms.trouble_calls").
			Where("status = ?", CallStatusOpen).
			Where("service_point_id IS NOT NULL").
			Scan(ctx, &rows); err != nil {
			return fmt.Errorf("oms: loading open calls: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}

		// Per-call provenance, keyed by call id, for the source/detected_at stamp.
		type callMeta struct {
			ami        bool
			reportedAt time.Time
		}
		meta := make(map[string]callMeta, len(rows))
		calls := make([]CallLocation, len(rows))
		for i, r := range rows {
			calls[i] = CallLocation{CallID: r.ID, ServicePointID: r.ServicePointID}
			meta[r.ID] = callMeta{ami: r.CallerRef != nil && *r.CallerRef == lastGaspCallerRef, reportedAt: r.ReportedAt}
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

			// Detection source: a cluster of only AMI last-gasp calls was detected by
			// the meters ('ami'); any human call makes it customer-reported. detected_at
			// is the earliest call in the cluster -- when we first knew (0168).
			source := "customer"
			allAMI := true
			var detectedAt time.Time
			for _, cid := range p.CallIDs {
				m, ok := meta[cid]
				if !ok {
					continue
				}
				if !m.ami {
					allAMI = false
				}
				if detectedAt.IsZero() || m.reportedAt.Before(detectedAt) {
					detectedAt = m.reportedAt
				}
			}
			if allAMI && len(p.CallIDs) > 0 {
				source = "ami"
			}
			if detectedAt.IsZero() {
				detectedAt = time.Now()
			}

			// Primary territory from the calling SPs' dominant territory -- where the
			// reports came from, the geographic signal a predicted incident carries
			// (0169). Cheap (no cone re-trace); 1 vote per calling SP.
			callerSupplies := make([]affectedSupply, 0, len(p.ServicePointIDs))
			for _, spid := range p.ServicePointIDs {
				callerSupplies = append(callerSupplies, affectedSupply{Class: ServicePointClass, ID: spid, Customers: 1})
			}
			territoryID, err := dominantTerritoryID(ctx, tx, callerSupplies)
			if err != nil {
				return err
			}

			var incidentID string
			const insertInc = `
INSERT INTO oms.incidents
    (predicted_device_class, predicted_device_id, status, confidence,
     affected_customers, affected_service_points, detection_source, detected_at, territory_id)
VALUES (?, ?, 'predicted', ?, ?, ?, ?, ?, ?)
RETURNING id`
			if err := tx.QueryRowContext(ctx, insertInc,
				deviceClass, deviceID, confidence,
				p.AffectedCustomers, p.AffectedServicePoints, source, detectedAt, territoryID,
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

			// Timeline: the incident is born, and each call that rolled into it is
			// attributed -- so a call's panel can show "-> Incident X".
			if err := recordEvent(ctx, tx, EntityIncident, incidentID, EventIncidentPredicted, nil); err != nil {
				return err
			}
			for _, cid := range p.CallIDs {
				if err := recordEvent(ctx, tx, EntityCall, cid, EventCallAttributed, noteOf("→ incident "+incidentID)); err != nil {
					return err
				}
			}

			inc, err := GetIncident(ctx, tx, incidentID)
			if err != nil {
				return err
			}
			if err := webhookout.Emit(ctx, tx, webhookout.EventIncidentRaised, webhookout.KindIncident, inc.ID, inc); err != nil {
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

// freezeInterruptions materialises one customer_interruptions row per counting
// load leaf (SERVICE_POINT or PRIMARY METER), each carrying its frozen class so
// the map overlay can draw it back and its customer count exactly as the shared
// rollup computed it -- no re-query of the gis tables, so the frozen customers
// can never disagree with the affected/energization counts that used the same
// trace.ServicePoint lines. ON CONFLICT keys on the widened (incident, class, id)
// uniqueness (migration 0119), so re-freezing the same incident is a no-op. Empty
// supplies (a device with nothing below) freezes nothing. Shared by
// ConfirmIncident and RaiseIncidentFromDevice.
func freezeInterruptions(ctx context.Context, tx bun.IDB, incidentID string, supplies []trace.ServicePoint, interruptedAt time.Time) error {
	if len(supplies) == 0 {
		return nil
	}
	rows := make([]interruptionRow, 0, len(supplies))
	for _, s := range supplies {
		rows = append(rows, interruptionRow{
			IncidentID:     incidentID,
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
		return fmt.Errorf("oms: materialising interruptions for incident %s: %w", incidentID, err)
	}
	return nil
}

// lastGaspCallerRef tags a trouble call minted from an AMI "last gasp" frame, so
// the board can tell a meter's dying power-down signal from a phoned-in report.
const lastGaspCallerRef = "AMI:last-gasp"

// RaiseFromDeviceParams names the device a field/SCADA feed reports as operated.
type RaiseFromDeviceParams struct {
	DeviceClass string
	DeviceID    string
	Cause       *string
	OccurredAt  *time.Time
}

// RaiseIncidentFromDevice raises (or returns the already-open) incident blamed on
// a device that a field/SCADA feed reports as tripped or locked out -- the
// telemetry counterpart to call-driven prediction. An outage appears on the board
// the instant SCADA reports the operation, without waiting for a customer to ring
// in.
//
// Idempotent: if a non-terminal incident is already blamed on this device it is
// returned unchanged, so a re-delivered event or a repeated trip never duplicates
// the outage. A new incident is created CONFIRMED (SCADA is ground truth that the
// device really opened), unplanned, started at the reported time, with its
// downstream supplies frozen as interruptions (the SAIDI clock starts now).
// confidence is null -- it was not inferred from a call cluster. It emits
// oms.incident.raised. The device's live device_state is left untouched (a paired
// `operation` feed or an operator sets it); this records the OUTAGE, not the
// switch position, so the frozen affected set is independent of live state.
func RaiseIncidentFromDevice(ctx context.Context, db bun.IDB, p RaiseFromDeviceParams) (*Incident, error) {
	deviceClass := strings.TrimSpace(p.DeviceClass)
	deviceID := strings.TrimSpace(p.DeviceID)
	if deviceClass == "" || deviceID == "" {
		return nil, invalidf("a device (device_class and device_id) is required to raise an incident")
	}

	// Idempotent: an open (non-terminal) incident already blamed on this device is
	// returned as-is, so a re-delivered trip event can't double-raise.
	existing := new(Incident)
	err := db.NewSelect().Model(existing).
		Where("predicted_device_class = ?", deviceClass).
		Where("predicted_device_id = ?", deviceID).
		Where("status NOT IN (?, ?)", StatusRestored, StatusCancelled).
		Order("started_at DESC").
		Limit(1).
		Scan(ctx)
	if err == nil {
		return GetIncident(ctx, db, existing.ID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("oms: checking for an open incident on device %s/%s: %w", deviceClass, deviceID, err)
	}

	started := time.Now()
	if p.OccurredAt != nil {
		started = *p.OccurredAt
	}

	supplies, err := DownstreamSupplies(ctx, db, deviceClass, deviceID)
	if err != nil {
		return nil, err
	}
	customers := 0
	affSupplies := make([]affectedSupply, len(supplies))
	for i, s := range supplies {
		customers += s.Customers
		affSupplies[i] = affectedSupply{Class: s.FeatureClass, ID: s.FeatureID, Customers: s.Customers}
	}
	territoryID, err := dominantTerritoryID(ctx, db, affSupplies)
	if err != nil {
		return nil, err
	}

	var id string
	// SCADA/DMS told us the device operated: detection_source 'scada', detected_at
	// = the reported trip time (0168). The cause (why it tripped) is still unknown
	// here -- a crew or a raised fault fills that in. territory_id = the cone's
	// dominant territory (0169).
	const insertInc = `
INSERT INTO oms.incidents
    (predicted_device_class, predicted_device_id, outage_type, cause_code,
     status, started_at, affected_customers, affected_service_points,
     detection_source, detected_at, territory_id)
VALUES (?, ?, 'unplanned', ?, 'confirmed', ?, ?, ?, 'scada', ?, ?)
RETURNING id`
	if err := db.QueryRowContext(ctx, insertInc,
		deviceClass, deviceID, p.Cause, started, customers, len(supplies), started, territoryID,
	).Scan(&id); err != nil {
		return nil, fmt.Errorf("oms: raising incident from device %s/%s: %w", deviceClass, deviceID, err)
	}

	if err := freezeInterruptions(ctx, db, id, supplies, started); err != nil {
		return nil, err
	}

	var scadaNote *string
	if p.Cause != nil {
		scadaNote = noteOf("SCADA: " + *p.Cause)
	}
	if err := recordEventAt(ctx, db, EntityIncident, id, EventIncidentScadaRaise, started, scadaNote); err != nil {
		return nil, err
	}

	inc, err := GetIncident(ctx, db, id)
	if err != nil {
		return nil, err
	}
	if err := webhookout.Emit(ctx, db, webhookout.EventIncidentRaised, webhookout.KindIncident, inc.ID, inc); err != nil {
		return nil, err
	}
	return inc, nil
}

// RecordLastGasp logs an AMI "last gasp" (a meter's dying power-down frame) as a
// machine trouble call for its service point -- field ground truth that THIS
// supply is actually dark, feeding the same intake/prediction pipeline a human
// call does (a device-trip incident can then roll it in, or Predict can cluster a
// street's worth of them). Deduped: if an OPEN last-gasp call already stands for
// this SP, the existing one is returned rather than piling up a call per
// retransmit. The call is tagged caller_ref 'AMI:last-gasp'.
func RecordLastGasp(ctx context.Context, db bun.IDB, servicePointID string, occurredAt *time.Time) (*TroubleCall, error) {
	sp := strings.TrimSpace(servicePointID)
	if sp == "" {
		return nil, invalidf("a service point id is required for a last-gasp")
	}
	if err := ensureServicePointExists(ctx, db, sp); err != nil {
		return nil, err
	}

	// Dedup an open last-gasp for the same SP (last-gasp frames are often
	// retransmitted); a new human call or a fresh outage after restoration still
	// creates a new call -- only an already-open AMI call for this SP is reused.
	existing := new(TroubleCall)
	err := selectTroubleCalls(db, existing).
		Where("service_point_id = ?", sp).
		Where("status = ?", CallStatusOpen).
		Where("caller_ref = ?", lastGaspCallerRef).
		Order("reported_at DESC").
		Limit(1).
		Scan(ctx)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("oms: checking for an open last-gasp call: %w", err)
	}

	ref := lastGaspCallerRef
	return CreateTroubleCall(ctx, db, CreateCallParams{
		ServicePointID: &sp,
		CallType:       CallTypeNoSupply,
		CallerRef:      &ref,
		ReportedAt:     occurredAt,
	})
}

// CustomerReportParams is one customer outage report filed by an external IVR/CIS
// (blocker #5). CallerRef is the CIS's own report/ticket id and is REQUIRED — it is
// the idempotency key (a re-delivered report with the same ref returns the existing
// open call rather than piling up duplicates). A locator is required like any call:
// a service point, an address, or a lat/lng. AccountRef/Phone carry the caller's
// CIS premise id and callback number through to the restoration callback queue.
type CustomerReportParams struct {
	ServicePointID *string
	Address        *string
	Longitude      *float64
	Latitude       *float64
	CallerRef      string
	AccountRef     *string
	Phone          *string
	CallType       string
	ReportedAt     *time.Time
	Notes          *string
}

// RecordCustomerReport logs a customer outage report that arrived through an
// external IVR / CIS as a trouble call, carrying the caller's premise id and
// callback number — the human-report counterpart to RecordLastGasp (AMI). It feeds
// the same intake/prediction pipeline an operator-logged call does. Idempotent on
// the CIS report id (caller_ref): a re-delivered report returns the standing open
// call rather than duplicating it.
func RecordCustomerReport(ctx context.Context, db bun.IDB, p CustomerReportParams) (*TroubleCall, error) {
	ref := strings.TrimSpace(p.CallerRef)
	if ref == "" {
		return nil, invalidf("a customer report needs a caller_ref (the CIS report id, used as the idempotency key)")
	}

	// Idempotent re-delivery: an open call already standing for this report id is
	// returned as-is (the CIS retried); a cleared/cancelled one does not block a
	// fresh report reusing the ref after a later outage.
	existing := new(TroubleCall)
	err := selectTroubleCalls(db, existing).
		Where("caller_ref = ?", ref).
		Where("status = ?", CallStatusOpen).
		Order("reported_at DESC").
		Limit(1).
		Scan(ctx)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("oms: checking for an open customer report: %w", err)
	}

	return CreateTroubleCall(ctx, db, CreateCallParams{
		ServicePointID: p.ServicePointID,
		Address:        p.Address,
		Longitude:      p.Longitude,
		Latitude:       p.Latitude,
		ReportedAt:     p.ReportedAt,
		CallerRef:      &ref,
		AccountRef:     p.AccountRef,
		Phone:          p.Phone,
		CallType:       p.CallType,
		Notes:          p.Notes,
	})
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
		if err := assertLocalIncident(inc); err != nil {
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
			if err := freezeInterruptions(ctx, tx, id, supplies, interruptedAt); err != nil {
				return err
			}
		}

		upd := tx.NewUpdate().Model((*Incident)(nil)).
			Set("status = ?", StatusConfirmed).
			Set("updated_at = now()")
		upd = setActor(ctx, upd)
		if planned {
			upd = upd.Set("started_at = ?", interruptedAt)
		}
		if _, err := upd.Where("id = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("oms: confirming incident %s: %w", id, err)
		}
		out, err = GetIncident(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := recordEvent(ctx, tx, EntityIncident, id, EventIncidentConfirmed, nil); err != nil {
			return err
		}
		return webhookout.Emit(ctx, tx, webhookout.EventIncidentUpdated, webhookout.KindIncident, out.ID, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// DispatchIncident advances a confirmed incident to 'dispatched' -- a crew has
// been sent to the field. It is the one lifecycle step that was defined in the
// model since 0087 (StatusDispatched) but never had a transition to set it, so
// an incident could only ever jump confirmed->restored with no "crew en route"
// state the board could show. CrewRef, when given, records who was sent. It
// stamps the actor and fires oms.incident.updated.
func DispatchIncident(ctx context.Context, db *bun.DB, id string) (*Incident, error) {
	var out *Incident
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		inc, err := GetIncident(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := assertLocalIncident(inc); err != nil {
			return err
		}
		if inc.Status != StatusConfirmed {
			return invalidf("incident %q is %s; only a confirmed incident can be dispatched", id, inc.Status)
		}
		upd := tx.NewUpdate().Model((*Incident)(nil)).
			Set("status = ?", StatusDispatched).
			Set("updated_at = now()")
		upd = setActor(ctx, upd)
		if _, err := upd.Where("id = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("oms: dispatching incident %s: %w", id, err)
		}
		out, err = GetIncident(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := recordEvent(ctx, tx, EntityIncident, id, EventIncidentDispatched, nil); err != nil {
			return err
		}
		return webhookout.Emit(ctx, tx, webhookout.EventIncidentUpdated, webhookout.KindIncident, out.ID, out)
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
	// The planned cone's dominant territory, for board scoping/rollup (0169).
	plannedSupplies, err := DownstreamSupplies(ctx, db, device.FeatureClass, device.FeatureID)
	if err != nil {
		return nil, err
	}
	affSupplies := make([]affectedSupply, len(plannedSupplies))
	for i, s := range plannedSupplies {
		affSupplies[i] = affectedSupply{Class: s.FeatureClass, ID: s.FeatureID, Customers: s.Customers}
	}
	territoryID, err := dominantTerritoryID(ctx, db, affSupplies)
	if err != nil {
		return nil, err
	}

	var id string
	// A planned outage is utility-initiated: the "detection" is that we scheduled
	// it, stamped detected_at = now (0168).
	const insertInc = `
INSERT INTO oms.incidents
    (predicted_device_class, predicted_device_id, circuit_id, outage_type, cause_code,
     status, planned_start, planned_end, affected_customers, affected_service_points, notes,
     detection_source, detected_at, cause_status, territory_id)
VALUES (?, ?, ?, 'planned', ?, 'scheduled', ?, ?, ?, ?, ?, 'planned', now(), ?, ?)
RETURNING id`
	// A planned outage's cause is known the moment it's booked (the work), so it's
	// 'confirmed' when a cause was given, else 'unknown'.
	plannedCauseStatus := "unknown"
	if p.CauseCode != nil && strings.TrimSpace(*p.CauseCode) != "" {
		plannedCauseStatus = "confirmed"
	}
	if err := db.QueryRowContext(ctx, insertInc,
		device.FeatureClass, device.FeatureID, p.CircuitID, p.CauseCode,
		p.PlannedStart, p.PlannedEnd, customers, spCount, p.Notes,
		plannedCauseStatus, territoryID,
	).Scan(&id); err != nil {
		return nil, fmt.Errorf("oms: creating planned outage: %w", err)
	}
	if err := recordEvent(ctx, db, EntityIncident, id, EventIncidentPlanned, nil); err != nil {
		return nil, err
	}
	inc, err := GetIncident(ctx, db, id)
	if err != nil {
		return nil, err
	}
	if err := webhookout.Emit(ctx, db, webhookout.EventIncidentRaised, webhookout.KindIncident, inc.ID, inc); err != nil {
		return nil, err
	}
	return inc, nil
}

// AttributeIncidentDevice sets (or changes) the protective device an incident is
// blamed on -- the operator override that prediction can't do (OMS v2). It
// re-traces the new device's downstream cone and re-stamps the incident's
// affected counts and dominant territory, keeps its linked calls, and -- if the
// incident is already confirmed (its interruptions are frozen) -- re-freezes them
// from the new cone so reliability reflects the corrected device. confidence is
// cleared (a human attribution is not a statistical prediction). This is what
// turns an "Unattributed" incident from a dead-end into a switchable one.
func AttributeIncidentDevice(ctx context.Context, db *bun.DB, incidentID, deviceClass, deviceID string) (*Incident, error) {
	deviceClass = strings.TrimSpace(deviceClass)
	deviceID = strings.TrimSpace(deviceID)
	if deviceClass == "" || deviceID == "" {
		return nil, invalidf("a device (device_class and device_id) is required")
	}

	var out *Incident
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		inc, err := GetIncident(ctx, tx, incidentID)
		if err != nil {
			return err
		}
		if err := assertLocalIncident(inc); err != nil {
			return err
		}
		if inc.Status == StatusRestored || inc.Status == StatusCancelled {
			return invalidf("incident %q is %s; a closed incident cannot be re-attributed", incidentID, inc.Status)
		}

		// Trace the new device's cone for the corrected affected set + territory.
		supplies, err := DownstreamSupplies(ctx, tx, deviceClass, deviceID)
		if err != nil {
			return err
		}
		customers := 0
		affSupplies := make([]affectedSupply, len(supplies))
		for i, s := range supplies {
			customers += s.Customers
			affSupplies[i] = affectedSupply{Class: s.FeatureClass, ID: s.FeatureID, Customers: s.Customers}
		}
		territoryID, err := dominantTerritoryID(ctx, tx, affSupplies)
		if err != nil {
			return err
		}

		upd := tx.NewUpdate().Model((*Incident)(nil)).
			Set("predicted_device_class = ?", deviceClass).
			Set("predicted_device_id = ?", deviceID).
			Set("affected_customers = ?", customers).
			Set("affected_service_points = ?", len(supplies)).
			Set("territory_id = ?", territoryID).
			Set("confidence = NULL"). // a manual attribution is not a predicted confidence
			Set("updated_at = now()")
		upd = setActor(ctx, upd)
		if _, err := upd.Where("id = ?", incidentID).Exec(ctx); err != nil {
			return fmt.Errorf("oms: attributing device to incident %s: %w", incidentID, err)
		}

		// If this incident was already confirmed, its interruptions were frozen on
		// the OLD (or empty) cone -- replace them with the new one so SAIDI/SAIFI
		// reflect the corrected device. interruptedAt stays the original start.
		var frozen int
		if err := tx.NewSelect().
			ColumnExpr("COUNT(*)").
			TableExpr("oms.customer_interruptions").
			Where("incident_id = ?", incidentID).
			Scan(ctx, &frozen); err != nil {
			return fmt.Errorf("oms: counting interruptions for %s: %w", incidentID, err)
		}
		if frozen > 0 {
			if _, err := tx.NewDelete().
				Model((*CustomerInterruption)(nil)).
				Where("incident_id = ?", incidentID).Exec(ctx); err != nil {
				return fmt.Errorf("oms: clearing old interruptions for %s: %w", incidentID, err)
			}
			if err := freezeInterruptions(ctx, tx, incidentID, supplies, inc.StartedAt); err != nil {
				return err
			}
		}

		out, err = GetIncident(ctx, tx, incidentID)
		if err != nil {
			return err
		}
		if err := recordEvent(ctx, tx, EntityIncident, incidentID, EventIncidentAttributed,
			noteOf(deviceClass+" "+deviceID)); err != nil {
			return err
		}
		return webhookout.Emit(ctx, tx, webhookout.EventIncidentUpdated, webhookout.KindIncident, out.ID, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RaiseIncidentFromCalls raises a confirmed, UNATTRIBUTED incident from the open
// calls of one geographic trouble spot -- the "dispatch a crew to this spot"
// action (OMS v2 geographic lens). Unlike prediction (which clusters by the
// upstream device), this takes the calls of a single proximity cluster and makes
// ONE incident a crew is sent to, with no device yet -- a human attributes a
// device later, or a raised fault does. It links the calls, stamps the detection
// source from them (ami if all last-gasp, else customer), detected_at = earliest
// call, the dominant territory, the acting user, and an affected estimate = the
// distinct calling supplies (1 customer each; the true extent is unknown until a
// device is attributed). No device means no cone, so nothing is frozen.
func RaiseIncidentFromCalls(ctx context.Context, db *bun.DB, callIDs []string) (*Incident, error) {
	if len(callIDs) == 0 {
		return nil, invalidf("at least one trouble call is required to raise an incident")
	}
	var out *Incident
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var rows []struct {
			ID         string    `bun:"id"`
			SP         *string   `bun:"service_point_id"`
			CallerRef  *string   `bun:"caller_ref"`
			ReportedAt time.Time `bun:"reported_at"`
		}
		if err := tx.NewSelect().
			ColumnExpr("id, service_point_id, caller_ref, reported_at").
			TableExpr("oms.trouble_calls").
			Where("id IN (?)", bun.In(callIDs)).
			Where("status = ?", CallStatusOpen).
			Scan(ctx, &rows); err != nil {
			return fmt.Errorf("oms: loading calls for spot incident: %w", err)
		}
		if len(rows) == 0 {
			return invalidf("none of the given calls are open")
		}

		source := "customer"
		allAMI := true
		var detectedAt time.Time
		spSet := map[string]bool{}
		var supplies []affectedSupply
		ids := make([]string, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
			if r.CallerRef == nil || *r.CallerRef != lastGaspCallerRef {
				allAMI = false
			}
			if detectedAt.IsZero() || r.ReportedAt.Before(detectedAt) {
				detectedAt = r.ReportedAt
			}
			if r.SP != nil && *r.SP != "" && !spSet[*r.SP] {
				spSet[*r.SP] = true
				supplies = append(supplies, affectedSupply{Class: ServicePointClass, ID: *r.SP, Customers: 1})
			}
		}
		if allAMI {
			source = "ami"
		}
		if detectedAt.IsZero() {
			detectedAt = time.Now()
		}
		territoryID, err := dominantTerritoryID(ctx, tx, supplies)
		if err != nil {
			return err
		}
		affected := len(supplies) // distinct resolved supplies; 1 customer each

		var actorPtr *string
		if a := auth.ActorID(ctx); a != "" {
			actorPtr = &a
		}

		var id string
		const insertInc = `
INSERT INTO oms.incidents
    (outage_type, status, detection_source, detected_at, territory_id,
     affected_customers, affected_service_points, cause_status, last_actor_user_id)
VALUES ('unplanned', 'confirmed', ?, ?, ?, ?, ?, 'unknown', ?)
RETURNING id`
		if err := tx.QueryRowContext(ctx, insertInc,
			source, detectedAt, territoryID, affected, affected, actorPtr,
		).Scan(&id); err != nil {
			return fmt.Errorf("oms: raising incident from spot: %w", err)
		}

		if _, err := tx.NewUpdate().Model((*TroubleCall)(nil)).
			Set("status = ?", CallStatusAssociated).
			Set("incident_id = ?", id).
			Set("updated_at = now()").
			Where("id IN (?)", bun.In(ids)).Exec(ctx); err != nil {
			return fmt.Errorf("oms: linking calls to spot incident %s: %w", id, err)
		}

		if err := recordEvent(ctx, tx, EntityIncident, id, EventIncidentFromSpot, noteOf(fmt.Sprintf("%d call(s)", len(rows)))); err != nil {
			return err
		}
		for _, cid := range ids {
			if err := recordEvent(ctx, tx, EntityCall, cid, EventCallAttributed, noteOf("→ incident "+id)); err != nil {
				return err
			}
		}

		inc, err := GetIncident(ctx, tx, id)
		if err != nil {
			return err
		}
		out = inc
		return webhookout.Emit(ctx, tx, webhookout.EventIncidentRaised, webhookout.KindIncident, inc.ID, inc)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
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
		if err := assertLocalIncident(inc); err != nil {
			return err
		}
		if inc.Status == StatusRestored {
			return invalidf("incident %q is already restored", id)
		}
		if inc.Status == StatusCancelled {
			return invalidf("incident %q is cancelled and cannot be restored", id)
		}

		upd := tx.NewUpdate().Model((*Incident)(nil)).
			Set("status = ?", StatusRestored).
			Set("restored_at = now()").
			Set("updated_at = now()")
		upd = setActor(ctx, upd)
		if _, err := upd.Where("id = ?", id).Exec(ctx); err != nil {
			return fmt.Errorf("oms: restoring incident %s: %w", id, err)
		}
		if _, err := tx.NewUpdate().Model((*CustomerInterruption)(nil)).
			Set("restored_at = now()").
			Where("incident_id = ?", id).
			Where("restored_at IS NULL").Exec(ctx); err != nil {
			return fmt.Errorf("oms: closing interruptions for incident %s: %w", id, err)
		}
		out, err = GetIncident(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := recordEvent(ctx, tx, EntityIncident, id, EventIncidentRestoredE, nil); err != nil {
			return err
		}
		// Blocker #5: enqueue a "supply is back" callback per affected supply, and
		// signal the external CIS/IVR that callbacks are waiting (only when some were
		// enqueued -- an incident with no frozen interruptions notifies no one).
		n, err := enqueueRestorationCallbacks(ctx, tx, id)
		if err != nil {
			return err
		}
		if n > 0 {
			if err := emitCallbackDue(ctx, tx, id, n); err != nil {
				return err
			}
		}
		return webhookout.Emit(ctx, tx, webhookout.EventIncidentRestored, webhookout.KindIncident, out.ID, out)
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
		if err := assertLocalIncident(inc); err != nil {
			return err
		}
		if inc.Status == StatusRestored {
			return invalidf("incident %q is restored and cannot be cancelled", id)
		}
		if inc.Status == StatusCancelled {
			return invalidf("incident %q is already cancelled", id)
		}

		upd := tx.NewUpdate().Model((*Incident)(nil)).
			Set("status = ?", StatusCancelled).
			Set("updated_at = now()")
		upd = setActor(ctx, upd)
		if _, err := upd.Where("id = ?", id).Exec(ctx); err != nil {
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
		if err != nil {
			return err
		}
		if err := recordEvent(ctx, tx, EntityIncident, id, EventIncidentCancelled, nil); err != nil {
			return err
		}
		return webhookout.Emit(ctx, tx, webhookout.EventIncidentUpdated, webhookout.KindIncident, out.ID, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// setActor stamps last_actor_user_id onto an incident update when the request
// carries an authenticated user (HTTP handler or an MCP tool, which both put the
// SessionUser on ctx). A system/engine path with no user on ctx leaves the column
// untouched, so an un-attributed transition never nulls out a prior actor.
func setActor(ctx context.Context, upd *bun.UpdateQuery) *bun.UpdateQuery {
	if actor := auth.ActorID(ctx); actor != "" {
		return upd.Set("last_actor_user_id = ?", actor)
	}
	return upd
}

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
