package faults

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/wam"
	"github.com/kwaabs/voltedge/api/internal/webhookout"
)

// ---------------------------------------------------------------------------
// Validation helpers -- each maps an out-of-set value to a client-fixable 400.
// ---------------------------------------------------------------------------

func oneOf(field, value string, allowed ...string) error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return invalidf("invalid %s %q: want one of %s", field, value, strings.Join(allowed, ", "))
}

func validateFaultType(v string) error {
	return oneOf("fault_type", v, FaultTypeSLG, FaultTypeLL, FaultTypeLLG, FaultTypeLLL, FaultTypeLLLG, FaultTypeEarth, FaultTypeUnknown)
}

// ListFaultModes returns the failure modes catalogued for a feature class
// (faults.fault_modes, migration 0154), in display order -- the class-aware
// "cause" picker behind the fault form. includeInactive adds retired modes (for
// the admin editor); the picker passes false. An unknown or uncatalogued class
// returns an empty list, never an error: the form then falls back to free text.
func ListFaultModes(ctx context.Context, db bun.IDB, featureClass string, includeInactive bool) ([]FaultMode, error) {
	featureClass = strings.TrimSpace(featureClass)
	if featureClass == "" {
		return []FaultMode{}, nil
	}
	modes := []FaultMode{}
	q := db.NewSelect().Model(&modes).
		Where("feature_class_code = ?", featureClass).
		OrderExpr("sort_order, label")
	if !includeInactive {
		q = q.Where("active")
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("faults: listing fault modes for %q: %w", featureClass, err)
	}
	return modes, nil
}

func validateDimension(v string) error {
	return oneOf("dimension", v, "electrical", "mechanical", "thermal", "environmental", "ageing", "operational", "other")
}

func validateSeverity(v string) error {
	return oneOf("severity", v, "low", "medium", "high", "critical")
}

// FaultModeParams is the create/update input for a catalogue entry. FeatureClassCode
// and Code identify the mode and are fixed after create; the rest are its FMEA
// payload. Active defaults true on create.
type FaultModeParams struct {
	FeatureClassCode string
	Code             string
	Label            string
	Dimension        string
	TypicalCause     *string
	Effect           *string
	Severity         string
	SortOrder        int
	Active           *bool
}

// CreateFaultMode adds a catalogue entry for a class. The (class, code) pair is
// unique; a duplicate surfaces as an error from the constraint.
func CreateFaultMode(ctx context.Context, db bun.IDB, p FaultModeParams) (*FaultMode, error) {
	class, code, label := strings.TrimSpace(p.FeatureClassCode), strings.TrimSpace(p.Code), strings.TrimSpace(p.Label)
	if class == "" || code == "" || label == "" {
		return nil, invalidf("feature_class_code, code and label are required")
	}
	if err := validateDimension(p.Dimension); err != nil {
		return nil, err
	}
	if err := validateSeverity(p.Severity); err != nil {
		return nil, err
	}
	m := &FaultMode{
		FeatureClassCode: class,
		Code:             code,
		Label:            label,
		Dimension:        p.Dimension,
		TypicalCause:     p.TypicalCause,
		Effect:           p.Effect,
		Severity:         p.Severity,
		SortOrder:        p.SortOrder,
		Active:           p.Active == nil || *p.Active,
	}
	if _, err := db.NewInsert().Model(m).Returning("*").Exec(ctx); err != nil {
		return nil, fmt.Errorf("faults: creating fault mode: %w", err)
	}
	return m, nil
}

// UpdateFaultMode replaces the mutable fields of a catalogue entry by id (the
// identity pair class/code is fixed). Returns a not-found error for an unknown id.
func UpdateFaultMode(ctx context.Context, db bun.IDB, id string, p FaultModeParams) (*FaultMode, error) {
	if strings.TrimSpace(p.Label) == "" {
		return nil, invalidf("label is required")
	}
	if err := validateDimension(p.Dimension); err != nil {
		return nil, err
	}
	if err := validateSeverity(p.Severity); err != nil {
		return nil, err
	}
	m := &FaultMode{
		ID:           id,
		Label:        strings.TrimSpace(p.Label),
		Dimension:    p.Dimension,
		TypicalCause: p.TypicalCause,
		Effect:       p.Effect,
		Severity:     p.Severity,
		SortOrder:    p.SortOrder,
		Active:       p.Active == nil || *p.Active,
	}
	res, err := db.NewUpdate().Model(m).
		Column("label", "dimension", "typical_cause", "effect", "severity", "sort_order", "active").
		WherePK().Returning("*").Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("faults: updating fault mode %q: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, invalidf("fault mode %q not found", id)
	}
	return m, nil
}

// DeleteFaultMode removes a catalogue entry by id. Logged faults keep their
// fault_mode code (no FK), so deleting a mode never orphans a record -- it just
// drops it from the picker. Returns a not-found error for an unknown id.
func DeleteFaultMode(ctx context.Context, db bun.IDB, id string) error {
	res, err := db.NewDelete().Model((*FaultMode)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("faults: deleting fault mode %q: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return invalidf("fault mode %q not found", id)
	}
	return nil
}

func validateStatus(v string) error {
	return oneOf("status", v, StatusDetected, StatusInvestigating, StatusCleared, StatusClosed)
}

// ---------------------------------------------------------------------------
// Fault events
// ---------------------------------------------------------------------------

// faultColumns is the scalar read shape; `location` has no Go field and is only
// read back through ST_X/ST_Y aliased onto the scanonly Longitude/Latitude
// fields (the same split oms/wam use). asset_id / protective_device_id are cast
// to text so the nullable-uuid columns scan into *string.
const faultColumns = "id, reference, asset_class, asset_id::text AS asset_id, fault_type, cause, fault_current_a, protective_device_class, protective_device_id::text AS protective_device_id, clearing_time_ms, status, detected_by, detected_at, cleared_at, incident_id::text AS incident_id, defect_id::text AS defect_id, title, description, notes, created_at, updated_at"

func selectFaults(db bun.IDB, dst any) *bun.SelectQuery {
	return db.NewSelect().Model(dst).
		ColumnExpr(faultColumns).
		ColumnExpr("ST_X(location) AS longitude").
		ColumnExpr("ST_Y(location) AS latitude").
		// Hide a fault whose asset has been SOFT-DELETED: a deleted feature must not
		// surface anywhere, including as the subject of a fault record. A fault with
		// no asset (anchored by a location or an incident) is kept, as is one whose
		// asset is still live or was purged (no registry row -> orphaned history, a
		// separate concern, not a soft-deleted feature). "Soft-deleted" == a
		// feature_registry row (the cross-class liveness index) with deleted_at set;
		// correlated NOT EXISTS so it's an index probe on the asset_id, not a scan.
		// WHERE sees the base uuid asset_id column (the faultColumns ::text alias is
		// output-only), so it compares uuid = uuid against r.feature_id.
		Where("NOT EXISTS (SELECT 1 FROM runtime.feature_registry r WHERE r.feature_id = asset_id AND r.deleted_at IS NOT NULL)")
}

// CreateFaultParams is the input to CreateFault. Title is required; status
// defaults to 'detected'. A fault must be anchored by an asset (both class and
// id), a location, or an incident -- the DB has a matching CHECK
// (fault_events_anchor_ck) as a backstop. A lat/lng pair is optional but must be
// given together, as must an asset (class + id) and a protective device
// (class + id).
type CreateFaultParams struct {
	// ID, when set, is the device-minted primary key for an offline capture
	// (offline faults module). It makes the insert idempotent on sync replay --
	// ON CONFLICT (id) DO NOTHING, then the existing row is returned -- so a
	// re-pushed fault op is a no-op, the same retry-safety a feature or offline
	// defect create has. nil lets the DB default the id (the online create path).
	ID                    *string
	Reference             *string
	AssetClass            *string
	AssetID               *string
	Longitude             *float64
	Latitude              *float64
	FaultType             *string
	FaultMode             *string
	Cause                 *string
	FaultCurrentA         *float64
	ProtectiveDeviceClass *string
	ProtectiveDeviceID    *string
	ClearingTimeMS        *float64
	DetectedBy            *string
	DetectedAt            *time.Time
	IncidentID            *string
	DefectID              *string
	Title                 string
	Description           *string
	Notes                 *string
}

// CreateFault logs one fault event, letting the DB defaults for id,
// status='detected', and detected_at (when unset) fire. The location, when a
// lat/lng is given, is built with ST_MakePoint (never bound through the scanonly
// fields) -- the same column-listing insert oms and wam use.
func CreateFault(ctx context.Context, db bun.IDB, p CreateFaultParams) (*FaultEvent, error) {
	if strings.TrimSpace(p.Title) == "" {
		return nil, invalidf("a fault needs a title")
	}

	assetClass := trimPtr(p.AssetClass)
	assetID := trimPtr(p.AssetID)
	if (assetClass == "") != (assetID == "") {
		return nil, invalidf("a fault asset needs both asset_class and asset_id")
	}
	deviceClass := trimPtr(p.ProtectiveDeviceClass)
	deviceID := trimPtr(p.ProtectiveDeviceID)
	if (deviceClass == "") != (deviceID == "") {
		return nil, invalidf("a protective device needs both protective_device_class and protective_device_id")
	}
	hasLng := p.Longitude != nil
	hasLat := p.Latitude != nil
	if hasLng != hasLat {
		return nil, invalidf("a fault location needs both longitude and latitude, or neither")
	}
	incidentID := trimPtr(p.IncidentID)

	hasAsset := assetClass != "" && assetID != ""
	hasLoc := hasLng && hasLat
	hasIncident := incidentID != ""
	if !hasAsset && !hasLoc && !hasIncident {
		return nil, invalidf("a fault needs an anchor: an asset (asset_class and asset_id), a location, or an incident_id")
	}

	if p.FaultType != nil && strings.TrimSpace(*p.FaultType) != "" {
		if err := validateFaultType(strings.TrimSpace(*p.FaultType)); err != nil {
			return nil, err
		}
	}

	cols := []string{"status", "title"}
	vals := []string{"?", "?"}
	args := []any{StatusDetected, strings.TrimSpace(p.Title)}

	add := func(col, val string, arg any) {
		cols = append(cols, col)
		vals = append(vals, val)
		args = append(args, arg)
	}
	if p.Reference != nil {
		add("reference", "?", strings.TrimSpace(*p.Reference))
	}
	if hasAsset {
		add("asset_class", "?", assetClass)
		add("asset_id", "?", assetID)
	}
	if hasLoc {
		cols = append(cols, "location")
		vals = append(vals, "ST_SetSRID(ST_MakePoint(?, ?), 4326)")
		args = append(args, *p.Longitude, *p.Latitude)
	}
	if p.FaultType != nil && strings.TrimSpace(*p.FaultType) != "" {
		add("fault_type", "?", strings.TrimSpace(*p.FaultType))
	}
	if p.FaultMode != nil && strings.TrimSpace(*p.FaultMode) != "" {
		add("fault_mode", "?", strings.TrimSpace(*p.FaultMode))
	}
	if p.Cause != nil {
		add("cause", "?", strings.TrimSpace(*p.Cause))
	}
	if p.FaultCurrentA != nil {
		add("fault_current_a", "?", *p.FaultCurrentA)
	}
	if deviceClass != "" {
		add("protective_device_class", "?", deviceClass)
		add("protective_device_id", "?", deviceID)
	}
	if p.ClearingTimeMS != nil {
		add("clearing_time_ms", "?", *p.ClearingTimeMS)
	}
	if p.DetectedBy != nil {
		add("detected_by", "?", strings.TrimSpace(*p.DetectedBy))
	}
	if p.DetectedAt != nil {
		add("detected_at", "?", *p.DetectedAt)
	}
	if hasIncident {
		add("incident_id", "?", incidentID)
	}
	if id := trimPtr(p.DefectID); id != "" {
		add("defect_id", "?", id)
	}
	if p.Description != nil {
		add("description", "?", *p.Description)
	}
	if p.Notes != nil {
		add("notes", "?", *p.Notes)
	}
	// A device-minted id makes the insert idempotent: the second apply of a
	// re-pushed offline capture hits ON CONFLICT, inserts nothing, and the row is
	// read back below by its known id.
	idempotent := p.ID != nil && strings.TrimSpace(*p.ID) != ""
	if idempotent {
		add("id", "?", strings.TrimSpace(*p.ID))
	}

	conflict := ""
	if idempotent {
		conflict = " ON CONFLICT (id) DO NOTHING"
	}
	query := fmt.Sprintf(
		"INSERT INTO faults.fault_events (%s) VALUES (%s)%s RETURNING id",
		strings.Join(cols, ", "), strings.Join(vals, ", "), conflict,
	)
	var id string
	err := db.QueryRowContext(ctx, query, args...).Scan(&id)
	if idempotent && errors.Is(err, sql.ErrNoRows) {
		// ON CONFLICT DO NOTHING returned no row: the fault already exists (a
		// replayed op). Return the existing row without re-recording history -- an
		// idempotent no-op, so the audit trail keeps only the first raise.
		return GetFault(ctx, db, strings.TrimSpace(*p.ID))
	}
	if err != nil {
		return nil, fmt.Errorf("faults: inserting fault event: %w", err)
	}
	f, err := GetFault(ctx, db, id)
	if err != nil {
		return nil, err
	}
	if err := recordFaultHistory(ctx, db, historyCreated, f); err != nil {
		return nil, err
	}
	// Outbound event (docs/outbound-webhooks-design.md): emitted in the same db handle
	// the write used, right beside the audit-history row -- so on a tx-carrying caller
	// it is atomic, and the idempotent no-op above returns before here (no duplicate).
	if err := webhookout.Emit(ctx, db, webhookout.EventFaultRaised, webhookout.KindFault, f.ID, f); err != nil {
		return nil, err
	}
	// If the clearing device is already known, log the trip to the device-operation
	// log (deviceops). Idempotent per fault, so a later device-identifying update
	// (UpdateFault, below) won't double-log.
	if err := emitFaultTrip(ctx, db, f); err != nil {
		return nil, err
	}
	return f, nil
}

// GetFault fetches one fault event by id, location projected to lng/lat.
func GetFault(ctx context.Context, db bun.IDB, id string) (*FaultEvent, error) {
	f := new(FaultEvent)
	if err := selectFaults(db, f).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("fault %q does not exist", id)
		}
		return nil, fmt.Errorf("faults: loading fault %s: %w", id, err)
	}
	return f, nil
}

// FaultFilter narrows ListFaults. Empty fields are ignored; Open=true limits to
// faults still on the network (neither cleared nor closed -- the live board).
type FaultFilter struct {
	Status     string
	FaultType  string
	AssetClass string
	AssetID    string
	IncidentID string
	Open       bool
}

// ListFaults returns fault events newest-first under the given filter.
func ListFaults(ctx context.Context, db bun.IDB, f FaultFilter) ([]FaultEvent, error) {
	var faults []FaultEvent
	q := selectFaults(db, &faults).Order("detected_at DESC")
	if f.Open {
		q = q.Where("status NOT IN (?, ?)", StatusCleared, StatusClosed)
	} else if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.FaultType != "" {
		q = q.Where("fault_type = ?", f.FaultType)
	}
	if f.AssetClass != "" {
		q = q.Where("asset_class = ?", f.AssetClass)
	}
	if f.AssetID != "" {
		q = q.Where("asset_id = ?", f.AssetID)
	}
	if f.IncidentID != "" {
		q = q.Where("incident_id = ?", f.IncidentID)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("faults: listing faults: %w", err)
	}
	return faults, nil
}

// SetFaultStatus transitions a fault and keeps cleared_at consistent with the DB
// CHECK ((cleared|closed) = cleared_at IS NOT NULL): it stamps cleared_at when
// moving to a terminal state and clears it when reopening.
func SetFaultStatus(ctx context.Context, db bun.IDB, id, status string) (*FaultEvent, error) {
	if err := validateStatus(status); err != nil {
		return nil, err
	}
	terminal := status == StatusCleared || status == StatusClosed
	set := "status = ?, updated_at = now(), cleared_at = "
	args := []any{status}
	if terminal {
		set += "COALESCE(cleared_at, now())"
	} else {
		set += "NULL"
	}
	res, err := db.ExecContext(ctx, fmt.Sprintf("UPDATE faults.fault_events SET %s WHERE id = ?", set), append(args, id)...)
	if err != nil {
		return nil, fmt.Errorf("faults: updating fault %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, invalidf("fault %q does not exist", id)
	}
	f, err := GetFault(ctx, db, id)
	if err != nil {
		return nil, err
	}
	if err := recordFaultHistory(ctx, db, historyStatusChanged, f); err != nil {
		return nil, err
	}
	// A terminal status (cleared/closed) is the "fault cleared" event subscribers care
	// about most; any other transition is a plain status update.
	event := webhookout.EventFaultUpdated
	if terminal {
		event = webhookout.EventFaultCleared
	}
	if err := webhookout.Emit(ctx, db, event, webhookout.KindFault, f.ID, f); err != nil {
		return nil, err
	}
	return f, nil
}

// UpdateFaultParams is a sparse patch: every field is a pointer, and only the
// non-nil ones are written. It fills in investigation findings (classification,
// cause, the device that cleared it, timings) after a fault is first logged.
// Status is not patched here -- use SetFaultStatus, which keeps cleared_at
// consistent.
type UpdateFaultParams struct {
	Reference             *string
	FaultType             *string
	FaultMode             *string
	Cause                 *string
	FaultCurrentA         *float64
	ProtectiveDeviceClass *string
	ProtectiveDeviceID    *string
	ClearingTimeMS        *float64
	DetectedBy            *string
	IncidentID            *string
	Title                 *string
	Description           *string
	Notes                 *string
}

// UpdateFault applies a sparse patch. A protective device is still all-or-
// nothing: patch both class and id together, or clear both with empty strings.
func UpdateFault(ctx context.Context, db bun.IDB, id string, p UpdateFaultParams) (*FaultEvent, error) {
	// The protective-device pair moves together to keep fault_events_device_ck
	// satisfied.
	if (p.ProtectiveDeviceClass == nil) != (p.ProtectiveDeviceID == nil) {
		return nil, invalidf("patch both protective_device_class and protective_device_id together, or neither")
	}

	sets := []string{"updated_at = now()"}
	args := []any{}
	set := func(expr string, vs ...any) {
		sets = append(sets, expr)
		args = append(args, vs...)
	}

	if p.Reference != nil {
		set("reference = ?", strings.TrimSpace(*p.Reference))
	}
	if p.FaultType != nil {
		if ft := strings.TrimSpace(*p.FaultType); ft == "" {
			set("fault_type = NULL")
		} else {
			if err := validateFaultType(ft); err != nil {
				return nil, err
			}
			set("fault_type = ?", ft)
		}
	}
	if p.FaultMode != nil {
		if fm := strings.TrimSpace(*p.FaultMode); fm == "" {
			set("fault_mode = NULL")
		} else {
			set("fault_mode = ?", fm)
		}
	}
	if p.Cause != nil {
		set("cause = ?", strings.TrimSpace(*p.Cause))
	}
	if p.FaultCurrentA != nil {
		set("fault_current_a = ?", *p.FaultCurrentA)
	}
	if p.ProtectiveDeviceClass != nil && p.ProtectiveDeviceID != nil {
		dc := strings.TrimSpace(*p.ProtectiveDeviceClass)
		di := strings.TrimSpace(*p.ProtectiveDeviceID)
		if (dc == "") != (di == "") {
			return nil, invalidf("a protective device needs both class and id, or clear both")
		}
		if dc == "" {
			set("protective_device_class = NULL")
			set("protective_device_id = NULL")
		} else {
			set("protective_device_class = ?", dc)
			set("protective_device_id = ?", di)
		}
	}
	if p.ClearingTimeMS != nil {
		set("clearing_time_ms = ?", *p.ClearingTimeMS)
	}
	if p.DetectedBy != nil {
		set("detected_by = ?", strings.TrimSpace(*p.DetectedBy))
	}
	if p.IncidentID != nil {
		if iid := strings.TrimSpace(*p.IncidentID); iid == "" {
			// Only allow unlinking if the fault still has another anchor; the DB
			// CHECK backstops, but a clear 400 beats a 500.
			set("incident_id = NULL")
		} else {
			set("incident_id = ?", iid)
		}
	}
	if p.Title != nil {
		if strings.TrimSpace(*p.Title) == "" {
			return nil, invalidf("a fault needs a title")
		}
		set("title = ?", strings.TrimSpace(*p.Title))
	}
	if p.Description != nil {
		set("description = ?", *p.Description)
	}
	if p.Notes != nil {
		set("notes = ?", *p.Notes)
	}

	if len(sets) == 1 { // only updated_at -- nothing to change
		return nil, invalidf("no fields to update")
	}

	query := fmt.Sprintf("UPDATE faults.fault_events SET %s WHERE id = ?", strings.Join(sets, ", "))
	res, err := db.ExecContext(ctx, query, append(args, id)...)
	if err != nil {
		return nil, fmt.Errorf("faults: updating fault %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, invalidf("fault %q does not exist", id)
	}
	f, err := GetFault(ctx, db, id)
	if err != nil {
		return nil, err
	}
	if err := recordFaultHistory(ctx, db, historyUpdated, f); err != nil {
		return nil, err
	}
	// If this patch is what identified the clearing device, log the trip now
	// (deviceops). Idempotent per fault, so if CreateFault already logged it this
	// is a no-op.
	if err := emitFaultTrip(ctx, db, f); err != nil {
		return nil, err
	}
	return f, nil
}

// ---------------------------------------------------------------------------
// Composite: raise a fault from an OMS incident
// ---------------------------------------------------------------------------

// RaiseFaultFromIncidentParams carries the fault overrides. All optional: the
// fault inherits its title, protective device, and cause from the incident when
// unset.
type RaiseFaultFromIncidentParams struct {
	Title      *string
	FaultType  *string
	Cause      *string
	DetectedBy *string
	Notes      *string
}

// RaiseFaultFromIncident mints a fault event for an OMS incident and links the
// two, atomically. The fault inherits the incident's predicted protective device
// (as the device that operated) and cause, and is anchored by the incident_id.
// This is the fault-side analogue of WAM's RaiseWorkOrderFromDefect: one action
// records the electrical event behind a logged outage. The incident is read
// cross-schema by SQL (bare ref, no oms Go import), the same loose coupling the
// schemas keep at the DB level.
func RaiseFaultFromIncident(ctx context.Context, db *bun.DB, incidentID string, p RaiseFaultFromIncidentParams) (*FaultEvent, error) {
	var fault *FaultEvent
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Pull just the columns the fault inherits. A missing incident is a 400.
		var (
			predClass sql.NullString
			predID    sql.NullString
			cause     sql.NullString
			status    string
		)
		row := tx.QueryRowContext(ctx,
			"SELECT predicted_device_class, predicted_device_id::text, cause_code, status FROM oms.incidents WHERE id = ?",
			incidentID)
		if err := row.Scan(&predClass, &predID, &cause, &status); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return invalidf("incident %q does not exist", incidentID)
			}
			return fmt.Errorf("faults: loading incident %s: %w", incidentID, err)
		}

		title := "Fault behind incident"
		if p.Title != nil && strings.TrimSpace(*p.Title) != "" {
			title = strings.TrimSpace(*p.Title)
		}
		cp := CreateFaultParams{
			Title:      title,
			IncidentID: &incidentID,
			FaultType:  p.FaultType,
			DetectedBy: p.DetectedBy,
			Notes:      p.Notes,
		}
		// Inherit the incident's cause unless overridden.
		if p.Cause != nil && strings.TrimSpace(*p.Cause) != "" {
			cp.Cause = p.Cause
		} else if cause.Valid && strings.TrimSpace(cause.String) != "" {
			c := cause.String
			cp.Cause = &c
		}
		// Inherit the blamed device as the protective device that operated.
		if predClass.Valid && predID.Valid && predClass.String != "" && predID.String != "" {
			cp.ProtectiveDeviceClass = &predClass.String
			cp.ProtectiveDeviceID = &predID.String
		}

		f, err := CreateFault(ctx, tx, cp)
		if err != nil {
			return err
		}
		fault = f

		// Unify with the incident's cause axis (0168): raising a fault is the
		// corroborating evidence, so a cause that was merely guessed is now
		// CONFIRMED. Only when the incident actually names a cause -- a null cause
		// is left 'unknown' (you can't confirm a cause you haven't named). Best
		// effort within the same tx; cross-schema SQL, no oms Go import (the same
		// loose coupling the incident read above uses).
		if cause.Valid && strings.TrimSpace(cause.String) != "" {
			if _, err := tx.ExecContext(ctx,
				"UPDATE oms.incidents SET cause_status = 'confirmed', updated_at = now() WHERE id = ? AND cause_status <> 'confirmed'",
				incidentID); err != nil {
				return fmt.Errorf("faults: confirming incident %s cause: %w", incidentID, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return fault, nil
}

// ---------------------------------------------------------------------------
// Composite: book a WAM defect from a fault
// ---------------------------------------------------------------------------

// BookDefectFromFaultParams carries the defect overrides. All optional: the
// defect inherits the fault's title and asset when unset. Severity defaults to
// wam's medium when unset.
type BookDefectFromFaultParams struct {
	Title       *string
	DefectType  *string
	Severity    *string
	Description *string
	ReportedBy  *string
}

// BookDefectFromFault mints a WAM defect for the asset a fault hit and links it
// back onto the fault (defect_id), atomically. The defect is sourced 'outage'
// and carries the fault's incident_id (if any), so it flows straight into WAM's
// existing RaiseWorkOrderFromDefect loop -- closing event -> repair without a new
// WAM path. It reuses wam.CreateDefect wholesale (the defect anchor/validation
// discipline lives there); this package depends on wam one-directionally, which
// is the natural layering (faults orchestrates above both oms and wam).
//
// A defect names a specific asset or a location, so the fault must carry one or
// the other -- an incident-only fault has nothing concrete to book a defect
// against.
func BookDefectFromFault(ctx context.Context, db *bun.DB, faultID string, p BookDefectFromFaultParams) (*FaultEvent, error) {
	var fault *FaultEvent
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		f, err := GetFault(ctx, tx, faultID)
		if err != nil {
			return err
		}
		if f.DefectID != nil && *f.DefectID != "" {
			return invalidf("fault %q already has a booked defect", faultID)
		}
		hasAsset := f.AssetClass != nil && f.AssetID != nil && *f.AssetClass != "" && *f.AssetID != ""
		hasLoc := f.Longitude != nil && f.Latitude != nil
		if !hasAsset && !hasLoc {
			return invalidf("fault %q has no asset or location to book a defect against", faultID)
		}

		title := "Fault damage: " + f.Title
		if p.Title != nil && strings.TrimSpace(*p.Title) != "" {
			title = strings.TrimSpace(*p.Title)
		}
		dp := wam.CreateDefectParams{
			Source:      wam.SourceOutage,
			IncidentID:  f.IncidentID,
			Title:       title,
			DefectType:  p.DefectType,
			Description: p.Description,
			ReportedBy:  p.ReportedBy,
			Longitude:   f.Longitude,
			Latitude:    f.Latitude,
		}
		if hasAsset {
			dp.AssetClass = *f.AssetClass
			dp.AssetID = *f.AssetID
		}
		if p.Severity != nil && strings.TrimSpace(*p.Severity) != "" {
			dp.Severity = strings.TrimSpace(*p.Severity)
		}
		defect, err := wam.CreateDefect(ctx, tx, dp)
		if err != nil {
			return err
		}

		res, err := tx.ExecContext(ctx,
			"UPDATE faults.fault_events SET defect_id = ?, updated_at = now() WHERE id = ?",
			defect.ID, faultID)
		if err != nil {
			return fmt.Errorf("faults: linking defect to fault %s: %w", faultID, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return invalidf("fault %q does not exist", faultID)
		}
		fault, err = GetFault(ctx, tx, faultID)
		if err != nil {
			return err
		}
		return recordFaultHistory(ctx, tx, historyDefectBooked, fault)
	})
	if err != nil {
		return nil, err
	}
	return fault, nil
}

// trimPtr returns the trimmed string a *string points at, or "" when nil.
func trimPtr(s *string) string {
	if s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
}
