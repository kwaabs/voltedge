package wam

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/events"
	"github.com/kwaabs/voltedge/api/internal/webhookout"
)

// publishAssignment pushes a live "assigned to you" event to the order's assignee
// (if it has one), so their activity bell / Works board update instantly instead
// of on the next poll. Best-effort and non-blocking (see internal/events).
func publishAssignment(o *WorkOrder) {
	if o == nil || o.AssignedToUserID == nil {
		return
	}
	if uid := strings.TrimSpace(*o.AssignedToUserID); uid != "" {
		events.Publish(uid, "wam.assignment", map[string]any{"work_order_id": o.ID, "title": o.Title})
	}
}

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

func validateSeverity(v string) error {
	return oneOf("severity", v, SeverityLow, SeverityMedium, SeverityHigh, SeverityCritical)
}

func validateSource(v string) error {
	return oneOf("source", v, SourceInspection, SourceOutage, SourceConditionMonitoring, SourceAdHoc)
}

func validateWorkType(v string) error {
	return oneOf("work_type", v, WorkTypeInspection, WorkTypeCorrective, WorkTypePreventive, WorkTypeConstruction)
}

func validatePriority(v string) error {
	return oneOf("priority", v, PriorityLow, PriorityMedium, PriorityHigh, PriorityUrgent)
}

// ---------------------------------------------------------------------------
// Defects
// ---------------------------------------------------------------------------

// defectColumns is the scalar read shape; `location` has no Go field and is
// only read back through ST_X/ST_Y aliased onto the scanonly Longitude/Latitude
// fields (the same split oms/territory use).
// asset_class/asset_id are NOT in this list -- they are projected separately via
// COALESCE in selectDefects (nullable since 0101, orphan defects).
const defectColumns = "id, defect_type, severity, status, source, incident_id, title, description, reported_by, reported_at, resolved_at, notes, created_at, updated_at"

func selectDefects(db bun.IDB, dst any) *bun.SelectQuery {
	return db.NewSelect().Model(dst).
		ColumnExpr(defectColumns).
		// asset_class/asset_id are nullable since 0101 (orphan defects). Project a
		// NULL asset as "" so the scan targets stay plain strings (empty = no asset).
		ColumnExpr("COALESCE(asset_class, '') AS asset_class").
		ColumnExpr("COALESCE(asset_id::text, '') AS asset_id").
		ColumnExpr("ST_X(location) AS longitude").
		ColumnExpr("ST_Y(location) AS latitude").
		// Hide a defect whose asset is SOFT-DELETED: a deleted feature must not
		// surface anywhere, including as the subject of a defect. A NULL asset
		// (orphan defect) and a live/purged asset are kept; soft-deleted == a
		// feature_registry row with deleted_at set. See faults.selectFaults for the
		// shared rationale (correlated NOT EXISTS, index probe on asset_id).
		Where("NOT EXISTS (SELECT 1 FROM runtime.feature_registry r WHERE r.feature_id = asset_id AND r.deleted_at IS NOT NULL)")
}

// CreateDefectParams is the input to CreateDefect. Asset (class + id) and title
// are required; severity/source default when empty. A lat/lng pair is optional
// but must be given together. IncidentID links an outage-sourced defect to its
// OMS incident.
type CreateDefectParams struct {
	// ID, when set, is the device-minted primary key for an offline capture
	// (Phase 59 offline WAM). It makes the insert idempotent on sync replay --
	// ON CONFLICT (id) DO NOTHING, then the existing row is returned -- so a
	// re-pushed defect op is a no-op, the same retry-safety a feature create has.
	// nil lets the DB default the id (the online create path).
	ID          *string
	AssetClass  string
	AssetID     string
	DefectType  *string
	Severity    string
	Source      string
	IncidentID  *string
	Title       string
	Description *string
	Longitude   *float64
	Latitude    *float64
	ReportedBy  *string
	ReportedAt  *time.Time
	Notes       *string
}

// CreateDefect logs one asset-health finding, letting the DB defaults for id,
// status='open', and reported_at (when unset) fire. The location, when a
// lat/lng is given, is built with ST_MakePoint (never bound through the scanonly
// fields) -- the same column-listing insert oms uses.
func CreateDefect(ctx context.Context, db bun.IDB, p CreateDefectParams) (*Defect, error) {
	if strings.TrimSpace(p.Title) == "" {
		return nil, invalidf("a defect needs a title")
	}
	// A defect names an asset (both class and id) or is an "orphan" pinned by a
	// location -- but never a partial asset, and never floating with neither. The
	// DB has a matching CHECK (defects_anchor_ck, 0101) as a backstop.
	assetClass := strings.TrimSpace(p.AssetClass)
	assetID := strings.TrimSpace(p.AssetID)
	if (assetClass == "") != (assetID == "") {
		return nil, invalidf("a defect asset needs both asset_class and asset_id")
	}
	hasAsset := assetClass != "" && assetID != ""
	hasLng := p.Longitude != nil
	hasLat := p.Latitude != nil
	if hasLng != hasLat {
		return nil, invalidf("a defect location needs both longitude and latitude, or neither")
	}
	if !hasAsset && !(hasLng && hasLat) {
		return nil, invalidf("a defect needs an asset (asset_class and asset_id) or a location")
	}

	severity := strings.TrimSpace(p.Severity)
	if severity == "" {
		severity = SeverityMedium
	}
	if err := validateSeverity(severity); err != nil {
		return nil, err
	}
	source := strings.TrimSpace(p.Source)
	if source == "" {
		source = SourceAdHoc
	}
	if err := validateSource(source); err != nil {
		return nil, err
	}

	cols := []string{"severity", "source", "status", "title"}
	vals := []string{"?", "?", "?", "?"}
	args := []any{severity, source, DefectStatusOpen, strings.TrimSpace(p.Title)}

	add := func(col, val string, arg any) {
		cols = append(cols, col)
		vals = append(vals, val)
		args = append(args, arg)
	}
	// Asset columns only when present -- an orphan defect omits them (anchored by
	// its location below instead).
	if hasAsset {
		add("asset_class", "?", assetClass)
		add("asset_id", "?", assetID)
	}
	if p.DefectType != nil {
		add("defect_type", "?", strings.TrimSpace(*p.DefectType))
	}
	if p.IncidentID != nil && strings.TrimSpace(*p.IncidentID) != "" {
		add("incident_id", "?", strings.TrimSpace(*p.IncidentID))
	}
	if p.Description != nil {
		add("description", "?", *p.Description)
	}
	if hasLng && hasLat {
		cols = append(cols, "location")
		vals = append(vals, "ST_SetSRID(ST_MakePoint(?, ?), 4326)")
		args = append(args, *p.Longitude, *p.Latitude)
	}
	if p.ReportedBy != nil {
		add("reported_by", "?", strings.TrimSpace(*p.ReportedBy))
	}
	if p.ReportedAt != nil {
		add("reported_at", "?", *p.ReportedAt)
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
		"INSERT INTO wam.defects (%s) VALUES (%s)%s RETURNING id",
		strings.Join(cols, ", "), strings.Join(vals, ", "), conflict,
	)
	var id string
	err := db.QueryRowContext(ctx, query, args...).Scan(&id)
	if idempotent && errors.Is(err, sql.ErrNoRows) {
		// ON CONFLICT DO NOTHING returned no row: the defect already exists (a
		// replayed op). Return the existing row -- an idempotent no-op.
		return GetDefect(ctx, db, strings.TrimSpace(*p.ID))
	}
	if err != nil {
		return nil, fmt.Errorf("wam: inserting defect: %w", err)
	}
	d, err := GetDefect(ctx, db, id)
	if err != nil {
		return nil, err
	}
	// Outbound event (docs/outbound-webhooks-design.md): emitted on the write's db
	// handle, so a tx-carrying caller is atomic; the idempotent no-op above returned
	// before here, so a replayed capture never re-emits.
	if err := webhookout.Emit(ctx, db, webhookout.EventDefectCreated, webhookout.KindDefect, d.ID, d); err != nil {
		return nil, err
	}
	return d, nil
}

// GetDefect fetches one defect by id, location projected to lng/lat.
func GetDefect(ctx context.Context, db bun.IDB, id string) (*Defect, error) {
	d := new(Defect)
	if err := selectDefects(db, d).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("defect %q does not exist", id)
		}
		return nil, fmt.Errorf("wam: loading defect %s: %w", id, err)
	}
	return d, nil
}

// DefectFilter narrows ListDefects. Empty fields are ignored; Open=true limits
// to still-open defects (the triage board).
type DefectFilter struct {
	Status     string
	Severity   string
	AssetClass string
	AssetID    string
	Open       bool
}

// ListDefects returns defects newest-first under the given filter.
func ListDefects(ctx context.Context, db bun.IDB, f DefectFilter) ([]Defect, error) {
	var defects []Defect
	q := selectDefects(db, &defects).Order("reported_at DESC")
	if f.Open {
		q = q.Where("status = ?", DefectStatusOpen)
	} else if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Severity != "" {
		q = q.Where("severity = ?", f.Severity)
	}
	if f.AssetClass != "" {
		q = q.Where("asset_class = ?", f.AssetClass)
	}
	if f.AssetID != "" {
		q = q.Where("asset_id = ?", f.AssetID)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("wam: listing defects: %w", err)
	}
	return defects, nil
}

// SetDefectStatus transitions a defect and keeps resolved_at consistent with
// the DB CHECK ((resolved|dismissed) = resolved_at IS NOT NULL): it stamps
// resolved_at when moving to a terminal state and clears it when reopening.
func SetDefectStatus(ctx context.Context, db bun.IDB, id, status string) (*Defect, error) {
	if err := oneOf("status", status, DefectStatusOpen, DefectStatusWorkOrdered, DefectStatusResolved, DefectStatusDismissed); err != nil {
		return nil, err
	}
	terminal := status == DefectStatusResolved || status == DefectStatusDismissed
	set := "status = ?, updated_at = now(), resolved_at = "
	args := []any{status}
	if terminal {
		set += "COALESCE(resolved_at, now())"
	} else {
		set += "NULL"
	}
	res, err := db.ExecContext(ctx, fmt.Sprintf("UPDATE wam.defects SET %s WHERE id = ?", set), append(args, id)...)
	if err != nil {
		return nil, fmt.Errorf("wam: updating defect %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, invalidf("defect %q does not exist", id)
	}
	return GetDefect(ctx, db, id)
}

// ---------------------------------------------------------------------------
// Work orders
// ---------------------------------------------------------------------------

const workOrderColumns = "id, reference, work_type, status, priority, title, description, asset_class, asset_id, source, defect_id, incident_id, assigned_to, assigned_to_user_id, crew_id, subcontractor_id, origin_system, external_id, scheduled_for, due_by, started_at, completed_at, completion_notes, created_at, updated_at"

func selectWorkOrders(db bun.IDB, dst any) *bun.SelectQuery {
	return db.NewSelect().Model(dst).
		ColumnExpr(workOrderColumns).
		ColumnExpr("ST_X(location) AS longitude").
		ColumnExpr("ST_Y(location) AS latitude").
		// Assigned crew's / tasked subcontractor's name via correlated subqueries
		// (not joins) to avoid unqualified-column ambiguity with the bare list above.
		ColumnExpr("(SELECT c.name FROM oms.crews c WHERE c.id = crew_id) AS crew_name").
		ColumnExpr("(SELECT s.name FROM wam.subcontractors s WHERE s.id = subcontractor_id) AS subcontractor_name").
		// Hide a work order whose asset is SOFT-DELETED (NULL/live/purged asset kept)
		// -- a deleted feature must not surface as the subject of a work order. See
		// faults.selectFaults for the shared rationale.
		Where("NOT EXISTS (SELECT 1 FROM runtime.feature_registry r WHERE r.feature_id = asset_id AND r.deleted_at IS NOT NULL)")
}

// CreateWorkOrderParams is the input to CreateWorkOrder. WorkType and title are
// required; status defaults to draft and priority to medium. Everything else is
// optional (an asset, a location, a defect link, an assignee, a schedule).
type CreateWorkOrderParams struct {
	Reference        *string
	WorkType         string
	Priority         string
	Title            string
	Description      *string
	AssetClass       *string
	AssetID          *string
	Longitude        *float64
	Latitude         *float64
	Source           *string
	DefectID         *string
	IncidentID       *string
	AssignedTo       *string
	AssignedToUserID *string
	CrewID           *string
	SubcontractorID  *string
	ScheduledFor     *time.Time
	DueBy            *time.Time
}

// CreateWorkOrder inserts one work order at status='draft' (unless an assignee
// or schedule is given, which bumps the initial status accordingly). The
// location, when a lat/lng is given, is built with ST_MakePoint.
func CreateWorkOrder(ctx context.Context, db bun.IDB, p CreateWorkOrderParams) (*WorkOrder, error) {
	workType := strings.TrimSpace(p.WorkType)
	if err := validateWorkType(workType); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Title) == "" {
		return nil, invalidf("a work order needs a title")
	}
	// A feature-asset order with no explicit coordinate inherits its asset's
	// on-geometry location, so every such order is pinnable on the field map and
	// locatable on the web board. Best-effort: an uncompiled / geometry-less /
	// missing asset just leaves the order locationless (as before).
	if p.Longitude == nil && p.Latitude == nil &&
		p.AssetClass != nil && strings.TrimSpace(*p.AssetClass) != "" &&
		p.AssetID != nil && strings.TrimSpace(*p.AssetID) != "" {
		lon, lat, ok, err := resolveAssetLocation(ctx, db, strings.TrimSpace(*p.AssetClass), strings.TrimSpace(*p.AssetID))
		if err != nil {
			return nil, err
		}
		if ok {
			p.Longitude, p.Latitude = &lon, &lat
		}
	}
	hasLng := p.Longitude != nil
	hasLat := p.Latitude != nil
	if hasLng != hasLat {
		return nil, invalidf("a work order location needs both longitude and latitude, or neither")
	}
	priority := strings.TrimSpace(p.Priority)
	if priority == "" {
		priority = PriorityMedium
	}
	if err := validatePriority(priority); err != nil {
		return nil, err
	}
	// A brand-new order is a draft; naming an assignee or a schedule up front is
	// a convenience that advances it one notch so it lands on the right board.
	assigned := (p.AssignedTo != nil && strings.TrimSpace(*p.AssignedTo) != "") ||
		(p.AssignedToUserID != nil && strings.TrimSpace(*p.AssignedToUserID) != "") ||
		(p.CrewID != nil && strings.TrimSpace(*p.CrewID) != "") ||
		(p.SubcontractorID != nil && strings.TrimSpace(*p.SubcontractorID) != "")
	status := WorkStatusDraft
	if assigned {
		status = WorkStatusAssigned
	} else if p.ScheduledFor != nil {
		status = WorkStatusScheduled
	}

	cols := []string{"work_type", "status", "priority", "title"}
	vals := []string{"?", "?", "?", "?"}
	args := []any{workType, status, priority, strings.TrimSpace(p.Title)}

	add := func(col, val string, arg any) {
		cols = append(cols, col)
		vals = append(vals, val)
		args = append(args, arg)
	}
	if p.Reference != nil {
		add("reference", "?", strings.TrimSpace(*p.Reference))
	}
	if p.Description != nil {
		add("description", "?", *p.Description)
	}
	if p.AssetClass != nil && strings.TrimSpace(*p.AssetClass) != "" {
		add("asset_class", "?", strings.TrimSpace(*p.AssetClass))
	}
	if p.AssetID != nil && strings.TrimSpace(*p.AssetID) != "" {
		add("asset_id", "?", strings.TrimSpace(*p.AssetID))
	}
	if hasLng && hasLat {
		cols = append(cols, "location")
		vals = append(vals, "ST_SetSRID(ST_MakePoint(?, ?), 4326)")
		args = append(args, *p.Longitude, *p.Latitude)
	}
	if p.Source != nil && strings.TrimSpace(*p.Source) != "" {
		add("source", "?", strings.TrimSpace(*p.Source))
	}
	if p.DefectID != nil && strings.TrimSpace(*p.DefectID) != "" {
		add("defect_id", "?", strings.TrimSpace(*p.DefectID))
	}
	if p.IncidentID != nil && strings.TrimSpace(*p.IncidentID) != "" {
		add("incident_id", "?", strings.TrimSpace(*p.IncidentID))
	}
	if p.AssignedTo != nil && strings.TrimSpace(*p.AssignedTo) != "" {
		add("assigned_to", "?", strings.TrimSpace(*p.AssignedTo))
	}
	if p.AssignedToUserID != nil && strings.TrimSpace(*p.AssignedToUserID) != "" {
		add("assigned_to_user_id", "?", strings.TrimSpace(*p.AssignedToUserID))
	}
	if p.CrewID != nil && strings.TrimSpace(*p.CrewID) != "" {
		add("crew_id", "?", strings.TrimSpace(*p.CrewID))
	}
	if p.SubcontractorID != nil && strings.TrimSpace(*p.SubcontractorID) != "" {
		add("subcontractor_id", "?", strings.TrimSpace(*p.SubcontractorID))
	}
	if p.ScheduledFor != nil {
		add("scheduled_for", "?", *p.ScheduledFor)
	}
	if p.DueBy != nil {
		add("due_by", "?", *p.DueBy)
	}

	query := fmt.Sprintf(
		"INSERT INTO wam.work_orders (%s) VALUES (%s) RETURNING id",
		strings.Join(cols, ", "), strings.Join(vals, ", "),
	)
	var id string
	if err := db.QueryRowContext(ctx, query, args...).Scan(&id); err != nil {
		return nil, fmt.Errorf("wam: inserting work order: %w", err)
	}
	o, err := GetWorkOrder(ctx, db, id)
	if err != nil {
		return nil, err
	}
	if err := webhookout.Emit(ctx, db, webhookout.EventWorkOrderCreated, webhookout.KindWorkOrder, o.ID, o); err != nil {
		return nil, err
	}
	publishAssignment(o) // a new order created already assigned to a user
	return o, nil
}

// GetWorkOrder fetches one work order by id, location projected to lng/lat.
func GetWorkOrder(ctx context.Context, db bun.IDB, id string) (*WorkOrder, error) {
	o := new(WorkOrder)
	if err := selectWorkOrders(db, o).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("work order %q does not exist", id)
		}
		return nil, fmt.Errorf("wam: loading work order %s: %w", id, err)
	}
	return o, nil
}

// WorkOrderFilter narrows ListWorkOrders. Empty fields are ignored; Open=true
// limits to orders that are neither completed nor cancelled (the live board).
type WorkOrderFilter struct {
	Status           string
	WorkType         string
	Priority         string
	AssetClass       string
	AssetID          string
	AssignedTo       string
	AssignedToUserID string // narrow to work assigned to one user (the "my work orders" filter)
	CrewID           string // narrow to work assigned to one crew
	IncidentID       string // narrow to the restoration work raised for one OMS incident
	SubcontractorID  string // narrow to work tasked to one external firm
	Open             bool
}

// ListWorkOrders returns work orders newest-first under the given filter.
func ListWorkOrders(ctx context.Context, db bun.IDB, f WorkOrderFilter) ([]WorkOrder, error) {
	var orders []WorkOrder
	q := selectWorkOrders(db, &orders).Order("created_at DESC")
	if f.Open {
		q = q.Where("status NOT IN (?, ?)", WorkStatusCompleted, WorkStatusCancelled)
	} else if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.WorkType != "" {
		q = q.Where("work_type = ?", f.WorkType)
	}
	if f.Priority != "" {
		q = q.Where("priority = ?", f.Priority)
	}
	if f.AssetClass != "" {
		q = q.Where("asset_class = ?", f.AssetClass)
	}
	if f.AssetID != "" {
		q = q.Where("asset_id = ?", f.AssetID)
	}
	if f.AssignedTo != "" {
		q = q.Where("assigned_to = ?", f.AssignedTo)
	}
	if f.AssignedToUserID != "" {
		q = q.Where("assigned_to_user_id = ?", f.AssignedToUserID)
	}
	if f.CrewID != "" {
		q = q.Where("crew_id = ?", f.CrewID)
	}
	if f.IncidentID != "" {
		q = q.Where("incident_id = ?", f.IncidentID)
	}
	if f.SubcontractorID != "" {
		q = q.Where("subcontractor_id = ?", f.SubcontractorID)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("wam: listing work orders: %w", err)
	}
	return orders, nil
}

// UpdateWorkOrderParams is a sparse patch: every field is a pointer, and only
// the non-nil ones are written. Status is validated and drives the started_at /
// completed_at side effects the DB CHECK requires.
type UpdateWorkOrderParams struct {
	Status           *string
	Priority         *string
	Title            *string
	Description      *string
	AssignedTo       *string
	AssignedToUserID *string // "" clears the assignment (SET NULL); a uuid assigns
	CrewID           *string // "" clears the crew (SET NULL); a uuid assigns
	SubcontractorID  *string // "" clears the subcontractor (SET NULL); a uuid tasks
	ScheduledFor     *time.Time
	DueBy            *time.Time
	CompletionNotes  *string
	// ResolveDefect, on a patch that moves the order to 'completed', also resolves
	// the defect this order was raised from -- but only when no OTHER active order
	// still references it. The UI defaults it on; other callers leave it nil (no
	// defect side effect). Ignored for any status other than completed.
	ResolveDefect *bool
}

// UpdateWorkOrder applies a sparse patch and keeps the lifecycle timestamps
// consistent with the DB CHECK ((status='completed') = completed_at NOT NULL):
// moving to in_progress stamps started_at (once), moving to completed stamps
// completed_at, and moving off completed clears it.
func UpdateWorkOrder(ctx context.Context, db bun.IDB, id string, p UpdateWorkOrderParams) (*WorkOrder, error) {
	// A mirrored (read-only) work order is owned by the external WAM it came from --
	// refuse any local edit (migration 0182). A cheap origin probe avoids loading the
	// whole row; the mirror ingest itself writes through MirrorWorkOrder, not here.
	if err := assertLocalWorkOrder(ctx, db, id); err != nil {
		return nil, err
	}
	sets := []string{"updated_at = now()"}
	args := []any{}
	set := func(expr string, vs ...any) {
		sets = append(sets, expr)
		args = append(args, vs...)
	}

	if p.Status != nil {
		status := strings.TrimSpace(*p.Status)
		if err := oneOf("status", status, WorkStatusDraft, WorkStatusScheduled, WorkStatusAssigned, WorkStatusInProgress, WorkStatusCompleted, WorkStatusCancelled); err != nil {
			return nil, err
		}
		set("status = ?", status)
		switch status {
		case WorkStatusInProgress:
			set("started_at = COALESCE(started_at, now())")
			set("completed_at = NULL")
		case WorkStatusCompleted:
			set("started_at = COALESCE(started_at, now())")
			set("completed_at = COALESCE(completed_at, now())")
		default:
			// draft/scheduled/assigned/cancelled are all not-completed states.
			set("completed_at = NULL")
		}
	}
	if p.Priority != nil {
		priority := strings.TrimSpace(*p.Priority)
		if err := validatePriority(priority); err != nil {
			return nil, err
		}
		set("priority = ?", priority)
	}
	if p.Title != nil {
		if strings.TrimSpace(*p.Title) == "" {
			return nil, invalidf("a work order needs a title")
		}
		set("title = ?", strings.TrimSpace(*p.Title))
	}
	if p.Description != nil {
		set("description = ?", *p.Description)
	}
	if p.AssignedTo != nil {
		set("assigned_to = ?", strings.TrimSpace(*p.AssignedTo))
	}
	if p.AssignedToUserID != nil {
		// A uuid FK can't take an empty string; an empty patch value means
		// "unassign" -> SET NULL.
		if uid := strings.TrimSpace(*p.AssignedToUserID); uid == "" {
			set("assigned_to_user_id = NULL")
		} else {
			set("assigned_to_user_id = ?", uid)
		}
	}
	if p.CrewID != nil {
		// Same as the user assignment: empty means "unassign crew" -> SET NULL.
		if cid := strings.TrimSpace(*p.CrewID); cid == "" {
			set("crew_id = NULL")
		} else {
			set("crew_id = ?", cid)
		}
	}
	if p.SubcontractorID != nil {
		if sid := strings.TrimSpace(*p.SubcontractorID); sid == "" {
			set("subcontractor_id = NULL")
		} else {
			set("subcontractor_id = ?", sid)
		}
	}
	if p.ScheduledFor != nil {
		set("scheduled_for = ?", *p.ScheduledFor)
	}
	if p.DueBy != nil {
		set("due_by = ?", *p.DueBy)
	}
	if p.CompletionNotes != nil {
		set("completion_notes = ?", *p.CompletionNotes)
	}

	if len(sets) == 1 { // only updated_at -- nothing to change
		return nil, invalidf("no fields to update")
	}

	query := fmt.Sprintf("UPDATE wam.work_orders SET %s WHERE id = ?", strings.Join(sets, ", "))
	res, err := db.ExecContext(ctx, query, append(args, id)...)
	if err != nil {
		return nil, fmt.Errorf("wam: updating work order %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, invalidf("work order %q does not exist", id)
	}
	o, err := GetWorkOrder(ctx, db, id)
	if err != nil {
		return nil, err
	}

	// Close the loop on the linked defect when THIS patch changed the order's
	// lifecycle. Completing the last active order on a defect resolves it (opt-in
	// via ResolveDefect -- the UI defaults it on); cancelling the last active order
	// reopens it (no one is working it any more). Both are guarded to the defect's
	// work_ordered state (never override a human's resolve/dismiss) and to "no other
	// active order" (a defect addressed by several orders isn't closed prematurely).
	if p.Status != nil && o.DefectID != nil && *o.DefectID != "" {
		switch o.Status {
		case WorkStatusCompleted:
			if p.ResolveDefect != nil && *p.ResolveDefect {
				if err := syncDefectFromWorkOrder(ctx, db, *o.DefectID, o.ID, DefectStatusResolved); err != nil {
					return nil, err
				}
			}
		case WorkStatusCancelled:
			if err := syncDefectFromWorkOrder(ctx, db, *o.DefectID, o.ID, DefectStatusOpen); err != nil {
				return nil, err
			}
		}
	}

	// A patch that lands the order on 'completed' is the lifecycle event subscribers
	// care about (the job is done); anything else is a plain update.
	event := webhookout.EventWorkOrderUpdated
	if o.Status == WorkStatusCompleted {
		event = webhookout.EventWorkOrderCompleted
	}
	if err := webhookout.Emit(ctx, db, event, webhookout.KindWorkOrder, o.ID, o); err != nil {
		return nil, err
	}
	// Only when THIS patch (re)assigned the order to a user -- not on an unrelated
	// edit of an already-assigned order, which would re-notify the same person.
	if p.AssignedToUserID != nil && strings.TrimSpace(*p.AssignedToUserID) != "" {
		publishAssignment(o)
	}
	return o, nil
}

// syncDefectFromWorkOrder transitions a work_ordered defect to `target` (resolved
// when its order completed, open when its order was cancelled) as long as no OTHER
// active order still references it. Guarded on status='work_ordered' so a defect a
// human already resolved/dismissed is never resurrected, and keeps resolved_at
// consistent with the DB CHECK. A no-op (0 rows) is fine. Runs on the caller's IDB
// so it rides whatever transaction the caller holds.
func syncDefectFromWorkOrder(ctx context.Context, db bun.IDB, defectID, excludeOrderID, target string) error {
	other, err := db.NewSelect().
		TableExpr("wam.work_orders").
		Where("defect_id = ?", defectID).
		Where("id <> ?", excludeOrderID).
		Where("status NOT IN (?, ?)", WorkStatusCompleted, WorkStatusCancelled).
		Exists(ctx)
	if err != nil {
		return fmt.Errorf("wam: checking other work orders for defect %s: %w", defectID, err)
	}
	if other {
		return nil // still being worked by another order -- leave the defect as is
	}
	resolvedAt := "NULL"
	if target == DefectStatusResolved {
		resolvedAt = "COALESCE(resolved_at, now())"
	}
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("UPDATE wam.defects SET status = ?, updated_at = now(), resolved_at = %s WHERE id = ? AND status = ?", resolvedAt),
		target, defectID, DefectStatusWorkOrdered,
	); err != nil {
		return fmt.Errorf("wam: syncing defect %s from work order: %w", defectID, err)
	}
	return nil
}

// StartIncidentCrewWorkOrders advances a crew's not-yet-started work orders for an
// incident to 'in_progress' (stamping started_at) -- called when the crew arrives
// on site, so the field board reflects that the restoration job is under way
// without the worker having to open and start it by hand. Only orders still in
// draft/scheduled/assigned are moved; in_progress/completed/cancelled are left
// alone, and COMPLETION stays a deliberate field action (arriving on site is not
// the same as finishing the job). Scoped to this crew's own orders for the
// incident. Returns how many it advanced.
func StartIncidentCrewWorkOrders(ctx context.Context, db bun.IDB, incidentID, crewID string) (int, error) {
	if incidentID == "" || crewID == "" {
		return 0, nil
	}
	orders, err := ListWorkOrders(ctx, db, WorkOrderFilter{IncidentID: incidentID})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, o := range orders {
		if o.CrewID == nil || *o.CrewID != crewID {
			continue
		}
		switch o.Status {
		case WorkStatusDraft, WorkStatusScheduled, WorkStatusAssigned:
			st := WorkStatusInProgress
			if _, err := UpdateWorkOrder(ctx, db, o.ID, UpdateWorkOrderParams{Status: &st}); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// RaiseWorkOrderFromDefectParams carries the corrective-order overrides. All
// optional: title/priority default from the defect when unset.
type RaiseWorkOrderFromDefectParams struct {
	Title            *string
	Priority         *string
	Description      *string
	AssignedTo       *string
	AssignedToUserID *string
	CrewID           *string
	SubcontractorID  *string
	ScheduledFor     *time.Time
	DueBy            *time.Time
}

// RaiseWorkOrderFromDefect creates a corrective work order for a defect and
// flips the defect to 'work_ordered', atomically. The order inherits the
// defect's asset and (unless overridden) its title, and a priority derived from
// the defect severity. This is the WAM analogue of OMS "generate a switching
// order from the blamed device" -- one action wires the response to the finding.
func RaiseWorkOrderFromDefect(ctx context.Context, db *bun.DB, defectID string, p RaiseWorkOrderFromDefectParams) (*WorkOrder, error) {
	var order *WorkOrder
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		defect, err := GetDefect(ctx, tx, defectID)
		if err != nil {
			return err
		}
		if defect.Status == DefectStatusResolved || defect.Status == DefectStatusDismissed {
			return invalidf("defect %q is already %s; cannot raise work for it", defectID, defect.Status)
		}

		title := "Repair: " + defect.Title
		if p.Title != nil && strings.TrimSpace(*p.Title) != "" {
			title = strings.TrimSpace(*p.Title)
		}
		priority := severityToPriority(defect.Severity)
		if p.Priority != nil && strings.TrimSpace(*p.Priority) != "" {
			priority = strings.TrimSpace(*p.Priority)
		}

		order, err = CreateWorkOrder(ctx, tx, CreateWorkOrderParams{
			WorkType:    WorkTypeCorrective,
			Priority:    priority,
			Title:       title,
			Description: p.Description,
			AssetClass:  &defect.AssetClass,
			AssetID:     &defect.AssetID,
			// Carry the defect's pin onto the order -- for an orphan defect (no
			// asset) this location is the only anchor the crew has. CreateWorkOrder
			// skips empty asset strings, so an orphan yields an asset-less order.
			Longitude:        defect.Longitude,
			Latitude:         defect.Latitude,
			DefectID:         &defect.ID,
			AssignedTo:       p.AssignedTo,
			AssignedToUserID: p.AssignedToUserID,
			CrewID:           p.CrewID,
			SubcontractorID:  p.SubcontractorID,
			ScheduledFor:     p.ScheduledFor,
			DueBy:            p.DueBy,
		})
		if err != nil {
			return err
		}
		// Flip the defect to work_ordered (only from open -- a defect already
		// work_ordered stays so; a terminal one was rejected above).
		if defect.Status == DefectStatusOpen {
			if _, err := SetDefectStatus(ctx, tx, defect.ID, DefectStatusWorkOrdered); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return order, nil
}

// ---------------------------------------------------------------------------
// Assignable users
// ---------------------------------------------------------------------------

// AssignableUser is the slim identity the assignee picker needs -- who a work
// order can be handed to.
type AssignableUser struct {
	ID          string `bun:"id" json:"id"`
	DisplayName string `bun:"display_name" json:"display_name"`
	Email       string `bun:"email" json:"email"`
}

// ListAssignableUsers returns active internal users who can act on work orders,
// for the assignee picker. "Can act" is permission-driven, not a hardcoded role
// list: anyone whose roles grant wam:write can perform and close work, so anyone
// with wam:write is assignable -- which naturally includes the seeded
// field-technician role (migration 0099) plus editors and admins. Authorization
// as data: add a role that grants wam:write and its holders show up here without
// a code change. Sorted by display name.
func ListAssignableUsers(ctx context.Context, db bun.IDB) ([]AssignableUser, error) {
	var users []AssignableUser
	err := db.NewSelect().
		TableExpr("iam.users AS u").
		ColumnExpr("u.id, u.display_name, u.email").
		Where("u.status = ?", "active").
		Where(`EXISTS (
			SELECT 1 FROM iam.user_roles ur
			JOIN iam.role_permissions rp ON rp.role_id = ur.role_id
			WHERE ur.user_id = u.id AND rp.permission_key = ?
		)`, "wam:write").
		OrderExpr("u.display_name ASC").
		Scan(ctx, &users)
	if err != nil {
		return nil, fmt.Errorf("wam: listing assignable users: %w", err)
	}
	return users, nil
}

// severityToPriority maps a defect severity onto a work-order priority so a
// corrective order inherits urgency from the finding.
func severityToPriority(severity string) string {
	switch severity {
	case SeverityCritical:
		return PriorityUrgent
	case SeverityHigh:
		return PriorityHigh
	case SeverityLow:
		return PriorityLow
	default:
		return PriorityMedium
	}
}
