package oms

// crew.go is real CREW / resource management (blocker #2, migration 0170):
// dispatchable crew entities with a status, and an assignment with a lifecycle
// (dispatched → on-site → cleared) tracked per crew per incident -- replacing the
// incident's free-text crew_ref and the stand-alone "dispatched" board flag. A
// crew carries one active assignment at a time; dispatching a crew to a confirmed
// incident also advances the incident to "dispatched" (so the board flag finally
// reflects a real crew). ETR management (set/clear the estimate) lives here too.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/wam"
	"github.com/kwaabs/voltedge/api/internal/webhookout"
	"github.com/uptrace/bun"
)

const (
	crewAvailable   = "available"
	crewDispatched  = "dispatched"
	crewOnSite      = "on_site"
	crewUnavailable = "unavailable"

	assignDispatched = "dispatched"
	assignOnSite     = "on_site"
	assignCleared    = "cleared"
)

// Crew mirrors oms.crews. TerritoryName/ActiveIncidentID/ActiveStatus are
// scan-only, filled on read so the board can show "Team B — on-site, inc 5b51".
type Crew struct {
	bun.BaseModel `bun:"table:oms.crews,alias:crew"`

	ID          string    `bun:"id,pk" json:"id"`
	Name        string    `bun:"name" json:"name"`
	Kind        *string   `bun:"kind" json:"kind"`
	Status      string    `bun:"status" json:"status"`
	TerritoryID *string   `bun:"territory_id" json:"territory_id"`
	Phone       *string   `bun:"phone" json:"phone"`
	Active      bool      `bun:"active" json:"active"`
	CreatedAt   time.Time `bun:"created_at" json:"created_at"`
	UpdatedAt   time.Time `bun:"updated_at" json:"updated_at"`

	TerritoryName     *string `bun:"territory_name,scanonly" json:"territory_name"`
	MemberCount       int     `bun:"member_count,scanonly" json:"member_count"`
	// OpenWorkOrderCount is the crew's load: work orders assigned to it that are
	// neither completed nor cancelled. Surfaced so an assigner can see a crew's
	// backlog before piling on more (a crew can hold any number).
	OpenWorkOrderCount int `bun:"open_work_order_count,scanonly" json:"open_work_order_count"`
	// ActiveAssignmentCount is how many incidents the crew is currently on (it can
	// be on several at once). ActiveIncidentID/ActiveAssignment/ActiveSince describe
	// the MOST RECENT of those, for a one-line summary.
	ActiveAssignmentCount int        `bun:"-" json:"active_assignment_count"`
	ActiveIncidentID      *string    `bun:"-" json:"active_incident_id"`
	ActiveAssignment      *string    `bun:"-" json:"active_assignment_id"`
	ActiveSince           *time.Time `bun:"-" json:"active_since"`
}

// CrewAssignment mirrors oms.crew_assignments. CrewName is scan-only (joined).
type CrewAssignment struct {
	bun.BaseModel `bun:"table:oms.crew_assignments,alias:ca"`

	ID           string     `bun:"id,pk" json:"id"`
	CrewID       string     `bun:"crew_id" json:"crew_id"`
	IncidentID   string     `bun:"incident_id" json:"incident_id"`
	Status       string     `bun:"status" json:"status"`
	DispatchedAt time.Time  `bun:"dispatched_at" json:"dispatched_at"`
	OnSiteAt     *time.Time `bun:"on_site_at" json:"on_site_at"`
	ClearedAt    *time.Time `bun:"cleared_at" json:"cleared_at"`
	Note         *string    `bun:"note" json:"note"`
	AssignedBy   *string    `bun:"assigned_by" json:"assigned_by"`
	CreatedAt    time.Time  `bun:"created_at" json:"created_at"`
	UpdatedAt    time.Time  `bun:"updated_at" json:"updated_at"`

	CrewName *string `bun:"crew_name,scanonly" json:"crew_name"`

	// WorkOrderID is set (not a stored column) when dispatching also raised a
	// restoration work order for the crew, so the caller can link to it.
	WorkOrderID *string `bun:"-" json:"work_order_id,omitempty"`
}

// ListCrews returns crews (active unless includeInactive), each with its home
// territory name and current active assignment (if any), name-ordered.
func ListCrews(ctx context.Context, db bun.IDB, includeInactive bool) ([]Crew, error) {
	var crews []Crew
	q := db.NewSelect().Model(&crews).
		ColumnExpr("crew.*").
		ColumnExpr("terr.name AS territory_name").
		ColumnExpr("(SELECT count(*) FROM oms.crew_members cm WHERE cm.crew_id = crew.id AND (cm.valid_until IS NULL OR cm.valid_until > now())) AS member_count").
		ColumnExpr("(SELECT count(*) FROM wam.work_orders wo WHERE wo.crew_id = crew.id AND wo.status NOT IN ('completed', 'cancelled')) AS open_work_order_count").
		Join("LEFT JOIN gis.territories AS terr ON terr.id = crew.territory_id").
		Order("crew.name")
	if !includeInactive {
		q = q.Where("crew.active")
	}
	if err := q.Scan(ctx); err != nil {
		return nil, fmt.Errorf("oms: listing crews: %w", err)
	}
	if len(crews) == 0 {
		return crews, nil
	}

	// A crew can be on several incidents at once; load every active (non-cleared)
	// assignment, newest first, so we can both count them and pick the most recent
	// as the one-line summary.
	var active []struct {
		CrewID       string    `bun:"crew_id"`
		ID           string    `bun:"id"`
		IncidentID   string    `bun:"incident_id"`
		DispatchedAt time.Time `bun:"dispatched_at"`
	}
	if err := db.NewSelect().
		ColumnExpr("crew_id, id, incident_id, dispatched_at").
		TableExpr("oms.crew_assignments").
		Where("status <> ?", assignCleared).
		OrderExpr("dispatched_at DESC").
		Scan(ctx, &active); err != nil {
		return nil, fmt.Errorf("oms: loading active assignments: %w", err)
	}
	// Index by crew: count all, and keep the first seen (most recent) as the summary.
	summary := make(map[string]int, len(active)) // crew id -> index of most-recent active
	counts := make(map[string]int, len(active))
	for i, a := range active {
		counts[a.CrewID]++
		if _, ok := summary[a.CrewID]; !ok {
			summary[a.CrewID] = i
		}
	}
	for i := range crews {
		crews[i].ActiveAssignmentCount = counts[crews[i].ID]
		if idx, ok := summary[crews[i].ID]; ok {
			a := active[idx]
			crews[i].ActiveIncidentID = &a.IncidentID
			crews[i].ActiveAssignment = &a.ID
			d := a.DispatchedAt
			crews[i].ActiveSince = &d
		}
	}
	return crews, nil
}

// CrewParams is the create/update shape. Empty strings on update clear the
// optional fields.
type CrewParams struct {
	Name        string
	Kind        *string
	Phone       *string
	TerritoryID *string
}

// CreateCrew adds a crew (status 'available').
func CreateCrew(ctx context.Context, db bun.IDB, p CrewParams) (*Crew, error) {
	if strings.TrimSpace(p.Name) == "" {
		return nil, invalidf("a crew needs a name")
	}
	crew := &Crew{Name: strings.TrimSpace(p.Name), Status: crewAvailable, Active: true}
	crew.Kind = trimToNil(p.Kind)
	crew.Phone = trimToNil(p.Phone)
	crew.TerritoryID = trimToNil(p.TerritoryID)
	if _, err := db.NewInsert().Model(crew).
		Column("name", "kind", "status", "territory_id", "phone", "active").
		Returning("*").Exec(ctx); err != nil {
		return nil, fmt.Errorf("oms: creating crew: %w", err)
	}
	return crew, nil
}

// UpdateCrew edits a crew's descriptive fields (name/kind/phone/territory). It
// does NOT touch status (that is governed by assignments and SetCrewAvailability).
func UpdateCrew(ctx context.Context, db bun.IDB, id string, p CrewParams) (*Crew, error) {
	if strings.TrimSpace(p.Name) == "" {
		return nil, invalidf("a crew needs a name")
	}
	res, err := db.NewUpdate().Model((*Crew)(nil)).
		Set("name = ?", strings.TrimSpace(p.Name)).
		Set("kind = ?", trimToNil(p.Kind)).
		Set("phone = ?", trimToNil(p.Phone)).
		Set("territory_id = ?", trimToNil(p.TerritoryID)).
		Set("updated_at = now()").
		Where("id = ?", id).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("oms: updating crew %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, invalidf("crew %q does not exist", id)
	}
	return getCrew(ctx, db, id)
}

// SetCrewAvailability flips a crew between 'available' and 'unavailable' (off
// shift). Refused while the crew has an active assignment -- its status is then
// governed by that assignment.
func SetCrewAvailability(ctx context.Context, db bun.IDB, id string, available bool) (*Crew, error) {
	busy, err := crewHasActiveAssignment(ctx, db, id)
	if err != nil {
		return nil, err
	}
	if busy {
		return nil, invalidf("crew %q is on an active assignment; clear it before changing availability", id)
	}
	status := crewAvailable
	if !available {
		status = crewUnavailable
	}
	res, err := db.NewUpdate().Model((*Crew)(nil)).
		Set("status = ?", status).Set("updated_at = now()").
		Where("id = ?", id).Exec(ctx)
	if err != nil {
		return nil, fmt.Errorf("oms: setting crew %s availability: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, invalidf("crew %q does not exist", id)
	}
	return getCrew(ctx, db, id)
}

// DeactivateCrew retires a crew (active = false), keeping its assignment history.
// Refused while it has an active assignment.
func DeactivateCrew(ctx context.Context, db bun.IDB, id string) error {
	busy, err := crewHasActiveAssignment(ctx, db, id)
	if err != nil {
		return err
	}
	if busy {
		return invalidf("crew %q is on an active assignment; clear it first", id)
	}
	res, err := db.NewUpdate().Model((*Crew)(nil)).
		Set("active = false").Set("status = ?", crewUnavailable).Set("updated_at = now()").
		Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("oms: deactivating crew %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return invalidf("crew %q does not exist", id)
	}
	return nil
}

// ListCrewAssignments returns a crew's assignments across all incidents,
// newest-first -- the crew's job history (active and cleared). Each row carries
// the incident id so the UI can link to it.
func ListCrewAssignments(ctx context.Context, db bun.IDB, crewID string) ([]CrewAssignment, error) {
	var out []CrewAssignment
	if err := db.NewSelect().Model(&out).
		ColumnExpr("ca.*").
		ColumnExpr("crew.name AS crew_name").
		Join("LEFT JOIN oms.crews AS crew ON crew.id = ca.crew_id").
		Where("ca.crew_id = ?", crewID).
		Order("ca.dispatched_at DESC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("oms: listing crew %s assignments: %w", crewID, err)
	}
	return out, nil
}

// ListIncidentCrews returns the crew assignments on an incident, newest-first,
// each with its crew name.
func ListIncidentCrews(ctx context.Context, db bun.IDB, incidentID string) ([]CrewAssignment, error) {
	var out []CrewAssignment
	if err := db.NewSelect().Model(&out).
		ColumnExpr("ca.*").
		ColumnExpr("crew.name AS crew_name").
		Join("LEFT JOIN oms.crews AS crew ON crew.id = ca.crew_id").
		Where("ca.incident_id = ?", incidentID).
		Order("ca.dispatched_at DESC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("oms: listing incident %s crews: %w", incidentID, err)
	}
	return out, nil
}

// DispatchOptions are the extras on a crew dispatch. RaiseWorkOrder closes the
// loop outage → dispatch → field job → mobile: it raises a crew-assigned
// restoration work order for the incident (pinned to the blamed device, source
// 'outage'), so the dispatched crew gets the job in their field pack.
// WorkOrderTitle overrides the default title.
type DispatchOptions struct {
	RaiseWorkOrder bool
	WorkOrderTitle *string
}

// DispatchCrewToIncident sends a crew to an incident: it creates a 'dispatched'
// assignment, marks the crew dispatched, and -- if the incident is confirmed --
// advances it to 'dispatched' too (so the board flag reflects a real crew). When
// opts.RaiseWorkOrder is set it also raises a crew-assigned restoration work order
// for the incident (same transaction). A crew already on an active assignment, or
// a closed incident, is refused.
func DispatchCrewToIncident(ctx context.Context, db *bun.DB, crewID, incidentID string, note *string, opts DispatchOptions) (*CrewAssignment, error) {
	var out *CrewAssignment
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		inc, err := GetIncident(ctx, tx, incidentID)
		if err != nil {
			return err
		}
		if err := assertLocalIncident(inc); err != nil {
			return err
		}
		if inc.Status == StatusRestored || inc.Status == StatusCancelled {
			return invalidf("incident %q is %s; cannot dispatch a crew to a closed incident", incidentID, inc.Status)
		}
		crew := new(Crew)
		if err := tx.NewSelect().Model(crew).Where("id = ?", crewID).Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return invalidf("crew %q does not exist", crewID)
			}
			return fmt.Errorf("oms: loading crew %s: %w", crewID, err)
		}
		if !crew.Active {
			return invalidf("crew %q is retired", crewID)
		}
		// A crew may run several incidents at once (storm work), so an existing
		// active assignment elsewhere no longer blocks a dispatch -- only a duplicate
		// active assignment to THIS incident is refused.
		dup, err := tx.NewSelect().Model((*CrewAssignment)(nil)).
			Where("crew_id = ?", crewID).Where("incident_id = ?", incidentID).
			Where("status <> ?", assignCleared).Exists(ctx)
		if err != nil {
			return fmt.Errorf("oms: checking crew %s assignment on incident %s: %w", crewID, incidentID, err)
		}
		if dup {
			return invalidf("crew %q is already assigned to this incident", crewID)
		}

		var actor *string
		if a := auth.ActorID(ctx); a != "" {
			actor = &a
		}
		ca := &CrewAssignment{CrewID: crewID, IncidentID: incidentID, Status: assignDispatched, Note: trimToNil(note), AssignedBy: actor}
		if _, err := tx.NewInsert().Model(ca).
			Column("crew_id", "incident_id", "status", "note", "assigned_by").
			Returning("*").Exec(ctx); err != nil {
			return fmt.Errorf("oms: creating assignment: %w", err)
		}
		if _, err := tx.NewUpdate().Model((*Crew)(nil)).
			Set("status = ?", crewDispatched).Set("updated_at = now()").
			Where("id = ?", crewID).Exec(ctx); err != nil {
			return fmt.Errorf("oms: marking crew %s dispatched: %w", crewID, err)
		}

		// Tie the incident's board flag to the real crew: a confirmed incident
		// becomes dispatched the moment a crew goes.
		if inc.Status == StatusConfirmed {
			upd := tx.NewUpdate().Model((*Incident)(nil)).
				Set("status = ?", StatusDispatched).Set("updated_at = now()")
			upd = setActor(ctx, upd)
			if _, err := upd.Where("id = ?", incidentID).Exec(ctx); err != nil {
				return fmt.Errorf("oms: advancing incident %s to dispatched: %w", incidentID, err)
			}
			refreshed, err := GetIncident(ctx, tx, incidentID)
			if err != nil {
				return err
			}
			if err := recordEvent(ctx, tx, EntityIncident, incidentID, EventIncidentDispatched, noteOf("crew "+crew.Name)); err != nil {
				return err
			}
			if err := webhookout.Emit(ctx, tx, webhookout.EventIncidentUpdated, webhookout.KindIncident, refreshed.ID, refreshed); err != nil {
				return err
			}
		}

		// Close the loop: raise a crew-assigned restoration work order for the
		// incident, pinned to the blamed device so the crew navigates to it in the
		// field pack. Same transaction -- a failed work order rolls back the dispatch.
		if opts.RaiseWorkOrder {
			cid := crewID
			wo, err := raiseRestorationWorkOrder(ctx, tx, inc, &cid, nil, opts.WorkOrderTitle)
			if err != nil {
				return err
			}
			ca.WorkOrderID = &wo.ID
		}

		out = ca
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// AdvanceCrewAssignment moves an assignment forward: dispatched → on_site →
// cleared. It stamps the transition time and keeps the crew's status in step
// (on_site → crew on_site; cleared → crew available when it has no other active
// assignment). Backwards moves are refused.
func AdvanceCrewAssignment(ctx context.Context, db *bun.DB, assignmentID, to string) (*CrewAssignment, error) {
	if to != assignOnSite && to != assignCleared {
		return nil, invalidf("assignment status must be %q or %q", assignOnSite, assignCleared)
	}
	var out *CrewAssignment
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		ca := new(CrewAssignment)
		if err := tx.NewSelect().Model(ca).Where("id = ?", assignmentID).Scan(ctx); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return invalidf("assignment %q does not exist", assignmentID)
			}
			return fmt.Errorf("oms: loading assignment %s: %w", assignmentID, err)
		}
		if ca.Status == assignCleared {
			return invalidf("assignment %q is already cleared", assignmentID)
		}
		if to == assignOnSite && ca.Status != assignDispatched {
			return invalidf("assignment %q is %s; can only go on-site from dispatched", assignmentID, ca.Status)
		}

		upd := tx.NewUpdate().Model((*CrewAssignment)(nil)).
			Set("status = ?", to).Set("updated_at = now()")
		if to == assignOnSite {
			upd = upd.Set("on_site_at = now()")
		} else {
			upd = upd.Set("cleared_at = now()")
		}
		if _, err := upd.Where("id = ?", assignmentID).Exec(ctx); err != nil {
			return fmt.Errorf("oms: advancing assignment %s: %w", assignmentID, err)
		}

		// Keep the crew's status in step.
		crewStatus := crewOnSite
		if to == assignCleared {
			// Cleared: free the crew unless it has another active assignment.
			other, err := tx.NewSelect().Model((*CrewAssignment)(nil)).
				Where("crew_id = ?", ca.CrewID).Where("status <> ?", assignCleared).
				Where("id <> ?", assignmentID).Exists(ctx)
			if err != nil {
				return fmt.Errorf("oms: checking crew %s other assignments: %w", ca.CrewID, err)
			}
			if other {
				crewStatus = crewDispatched // still busy elsewhere
			} else {
				crewStatus = crewAvailable
			}
		}
		if _, err := tx.NewUpdate().Model((*Crew)(nil)).
			Set("status = ?", crewStatus).Set("updated_at = now()").
			Where("id = ?", ca.CrewID).Exec(ctx); err != nil {
			return fmt.Errorf("oms: updating crew %s status: %w", ca.CrewID, err)
		}

		// Arriving on site puts the crew's restoration work for this incident
		// under way -- advance its not-yet-started orders to in_progress (same tx).
		// Completion stays a deliberate field action, not a side effect of clearing.
		if to == assignOnSite {
			if _, err := wam.StartIncidentCrewWorkOrders(ctx, tx, ca.IncidentID, ca.CrewID); err != nil {
				return err
			}
		}

		a, err := getAssignment(ctx, tx, assignmentID)
		if err != nil {
			return err
		}
		out = a
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// SetIncidentETR sets or clears an incident's estimated time of restoration. A
// nil etr clears it. Stamps the actor; a closed incident is refused.
func SetIncidentETR(ctx context.Context, db *bun.DB, incidentID string, etr *time.Time) (*Incident, error) {
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
			return invalidf("incident %q is %s; ETR no longer applies", incidentID, inc.Status)
		}
		upd := tx.NewUpdate().Model((*Incident)(nil)).Set("updated_at = now()")
		if etr == nil {
			upd = upd.Set("etr = NULL")
		} else {
			upd = upd.Set("etr = ?", *etr)
		}
		upd = setActor(ctx, upd)
		if _, err := upd.Where("id = ?", incidentID).Exec(ctx); err != nil {
			return fmt.Errorf("oms: setting ETR on incident %s: %w", incidentID, err)
		}
		out, err = GetIncident(ctx, tx, incidentID)
		if err != nil {
			return err
		}
		return webhookout.Emit(ctx, tx, webhookout.EventIncidentUpdated, webhookout.KindIncident, out.ID, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// --- helpers ---------------------------------------------------------------

// raiseRestorationWorkOrder creates the corrective work order an outage response
// raises -- tasked to a crew (crew dispatch) or a subcontractor (external
// tasking), exactly one of which is set. It pins to the incident's blamed device
// (so CreateWorkOrder inherits that asset's location), tags source 'outage', and
// links back to the incident. High priority -- an outage restoration is urgent
// field work.
func raiseRestorationWorkOrder(ctx context.Context, db bun.IDB, inc *Incident, crewID, subcontractorID, titleOverride *string) (*wam.WorkOrder, error) {
	title := "Restore outage"
	if inc.PredictedDeviceClass != nil && *inc.PredictedDeviceClass != "" {
		title = "Restore outage at " + *inc.PredictedDeviceClass
	}
	if titleOverride != nil && strings.TrimSpace(*titleOverride) != "" {
		title = strings.TrimSpace(*titleOverride)
	}
	source := wam.SourceOutage
	iid := inc.ID
	return wam.CreateWorkOrder(ctx, db, wam.CreateWorkOrderParams{
		WorkType:        wam.WorkTypeCorrective,
		Priority:        wam.PriorityHigh,
		Title:           title,
		AssetClass:      inc.PredictedDeviceClass,
		AssetID:         inc.PredictedDeviceID,
		Source:          &source,
		CrewID:          crewID,
		SubcontractorID: subcontractorID,
		IncidentID:      &iid,
	})
}

// TaskSubcontractorToIncident records that an EXTERNAL subcontractor has been
// tasked with an outage: it raises a subcontractor-tasked restoration work order
// linked to the incident (no crew, no field-pack fan-out -- the firm works off
// this system; the utility's coordinator tracks the order's status). A closed
// incident is refused. Returns the work order.
func TaskSubcontractorToIncident(ctx context.Context, db *bun.DB, incidentID, subcontractorID string, title *string) (*wam.WorkOrder, error) {
	if strings.TrimSpace(subcontractorID) == "" {
		return nil, invalidf("subcontractor_id is required")
	}
	var out *wam.WorkOrder
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		inc, err := GetIncident(ctx, tx, incidentID)
		if err != nil {
			return err
		}
		if err := assertLocalIncident(inc); err != nil {
			return err
		}
		if inc.Status == StatusRestored || inc.Status == StatusCancelled {
			return invalidf("incident %q is %s; cannot task work to a closed incident", incidentID, inc.Status)
		}
		sid := strings.TrimSpace(subcontractorID)
		wo, err := raiseRestorationWorkOrder(ctx, tx, inc, nil, &sid, title)
		if err != nil {
			return err
		}
		out = wo
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func getCrew(ctx context.Context, db bun.IDB, id string) (*Crew, error) {
	crews, err := ListCrews(ctx, db, true)
	if err != nil {
		return nil, err
	}
	for i := range crews {
		if crews[i].ID == id {
			return &crews[i], nil
		}
	}
	return nil, invalidf("crew %q does not exist", id)
}

func getAssignment(ctx context.Context, db bun.IDB, id string) (*CrewAssignment, error) {
	ca := new(CrewAssignment)
	if err := db.NewSelect().Model(ca).
		ColumnExpr("ca.*").
		ColumnExpr("crew.name AS crew_name").
		Join("LEFT JOIN oms.crews AS crew ON crew.id = ca.crew_id").
		Where("ca.id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, invalidf("assignment %q does not exist", id)
		}
		return nil, fmt.Errorf("oms: loading assignment %s: %w", id, err)
	}
	return ca, nil
}

func crewHasActiveAssignment(ctx context.Context, db bun.IDB, crewID string) (bool, error) {
	ok, err := db.NewSelect().Model((*CrewAssignment)(nil)).
		Where("crew_id = ?", crewID).Where("status <> ?", assignCleared).Exists(ctx)
	if err != nil {
		return false, fmt.Errorf("oms: checking crew %s assignments: %w", crewID, err)
	}
	return ok, nil
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
