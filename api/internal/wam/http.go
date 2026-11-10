package wam

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
)

// Routes returns the Work & Asset Management endpoints:
//
//	GET   /defects                    the defect board (?status=, ?open=true, ?severity=, ?asset_class=&asset_id=)
//	POST  /defects                    log an asset defect
//	GET   /defects/{id}               one defect
//	POST  /defects/{id}/status        transition a defect (open/work_ordered/resolved/dismissed)
//	POST  /defects/{id}/work-order    raise a corrective work order for the defect (flips it to work_ordered)
//	GET   /work-orders                the work board (?status=, ?open=true, ?work_type=, ?priority=, ?assigned_to=, ?asset_class=&asset_id=)
//	POST  /work-orders                create a work order
//	GET   /work-orders/{id}           one work order
//	PATCH /work-orders/{id}           patch a work order (status/priority/assignment/schedule/completion)
//	GET   /inspection-templates       list checklist templates (?active=true)
//	POST  /inspection-templates       create a checklist template (with items)
//	GET   /inspection-templates/{id}  one template with its items
//	POST  /inspection-templates/{id}/active   retire/restore a template ({active})
//	GET   /inspections                recorded inspections (?asset_class=&asset_id=, ?work_order_id=, ?template_id=)
//	POST  /inspections                record an inspection (auto-raises defects for failing items)
//	GET   /inspections/{id}           one inspection with its item results
//	GET   /assignable-users           field staff a work order can be assigned to (wam:write holders)
//
// The GET verbs read (wam:read) and the mutating verbs write (wam:write); the
// method-aware gate RequirePermission(wam:read, wam:write) is applied on the
// mount in api/cmd/server/main.go, so this router carries no auth of its own --
// the same arrangement oms / circuit / territory use.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()

	r.Get("/defects", listDefectsHandler(db))
	r.Post("/defects", createDefectHandler(db))
	r.Get("/defects/{id}", getDefectHandler(db))
	r.Post("/defects/{id}/status", setDefectStatusHandler(db))
	r.Post("/defects/{id}/work-order", raiseWorkOrderHandler(db))

	r.Get("/work-orders", listWorkOrdersHandler(db))
	r.Post("/work-orders", createWorkOrderHandler(db))
	r.Get("/work-orders/{id}", getWorkOrderHandler(db))
	r.Patch("/work-orders/{id}", updateWorkOrderHandler(db))

	r.Get("/inspections", listInspectionsHandler(db))
	r.Post("/inspections", recordInspectionHandler(db))
	r.Get("/inspections/{id}", getInspectionHandler(db))

	r.Get("/assignable-users", listAssignableUsersHandler(db))

	r.Get("/subcontractors", listSubcontractorsHandler(db))
	r.Post("/subcontractors", createSubcontractorHandler(db))
	r.Patch("/subcontractors/{id}", updateSubcontractorHandler(db))
	r.Post("/subcontractors/{id}/active", setSubcontractorActiveHandler(db))

	return r
}

// TemplateRoutes is the inspection-TEMPLATE surface, mounted separately from the
// main WAM router so it can carry a different gate: authoring a checklist is
// configuration (like the fault-mode catalog / data-quality rules), so its writes
// need metadata:write (admins/planners), while the GETs stay at wam:read so the
// inspection runner can still list and load templates to fill one out. The mount
// in cmd/server/main.go applies RequirePermission(wam:read, metadata:write).
func TemplateRoutes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/", listTemplatesHandler(db))
	r.Post("/", createTemplateHandler(db))
	r.Get("/{id}", getTemplateHandler(db))
	r.Post("/{id}/active", setTemplateActiveHandler(db))
	return r
}

// ---------------------------------------------------------------------------
// subcontractors (registry)
// ---------------------------------------------------------------------------

func listSubcontractorsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subs, err := ListSubcontractors(r.Context(), db, r.URL.Query().Get("include_inactive") == "true")
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"subcontractors": subs})
	}
}

type subcontractorRequest struct {
	Name        string  `json:"name"`
	ContactName *string `json:"contact_name"`
	Phone       *string `json:"phone"`
	Email       *string `json:"email"`
	Notes       *string `json:"notes"`
}

func (s subcontractorRequest) params() SubcontractorParams {
	return SubcontractorParams{Name: s.Name, ContactName: s.ContactName, Phone: s.Phone, Email: s.Email, Notes: s.Notes}
}

func createSubcontractorHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req subcontractorRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		sc, err := CreateSubcontractor(r.Context(), db, req.params())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, sc)
	}
}

func updateSubcontractorHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req subcontractorRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		sc, err := UpdateSubcontractor(r.Context(), db, chi.URLParam(r, "id"), req.params())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, sc)
	}
}

func setSubcontractorActiveHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Active bool `json:"active"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		sc, err := SetSubcontractorActive(r.Context(), db, chi.URLParam(r, "id"), req.Active)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, sc)
	}
}

// ---------------------------------------------------------------------------
// defects
// ---------------------------------------------------------------------------

func listDefectsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		defects, err := ListDefects(r.Context(), db, DefectFilter{
			Status:     q.Get("status"),
			Severity:   q.Get("severity"),
			AssetClass: q.Get("asset_class"),
			AssetID:    q.Get("asset_id"),
			Open:       q.Get("open") == "true",
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"defects": defects})
	}
}

type createDefectRequest struct {
	AssetClass  string     `json:"asset_class"`
	AssetID     string     `json:"asset_id"`
	DefectType  *string    `json:"defect_type"`
	Severity    string     `json:"severity"`
	Source      string     `json:"source"`
	IncidentID  *string    `json:"incident_id"`
	Title       string     `json:"title"`
	Description *string    `json:"description"`
	Longitude   *float64   `json:"longitude"`
	Latitude    *float64   `json:"latitude"`
	ReportedBy  *string    `json:"reported_by"`
	ReportedAt  *time.Time `json:"reported_at"`
	Notes       *string    `json:"notes"`
}

func createDefectHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createDefectRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		defect, err := CreateDefect(r.Context(), db, CreateDefectParams{
			AssetClass:  req.AssetClass,
			AssetID:     req.AssetID,
			DefectType:  req.DefectType,
			Severity:    req.Severity,
			Source:      req.Source,
			IncidentID:  req.IncidentID,
			Title:       req.Title,
			Description: req.Description,
			Longitude:   req.Longitude,
			Latitude:    req.Latitude,
			ReportedBy:  req.ReportedBy,
			ReportedAt:  req.ReportedAt,
			Notes:       req.Notes,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, defect)
	}
}

func getDefectHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defect, err := GetDefect(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, defect)
	}
}

type setDefectStatusRequest struct {
	Status string `json:"status"`
}

func setDefectStatusHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req setDefectStatusRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		defect, err := SetDefectStatus(r.Context(), db, chi.URLParam(r, "id"), req.Status)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, defect)
	}
}

type raiseWorkOrderRequest struct {
	Title            *string    `json:"title"`
	Priority         *string    `json:"priority"`
	Description      *string    `json:"description"`
	AssignedTo       *string    `json:"assigned_to"`
	AssignedToUserID *string    `json:"assigned_to_user_id"`
	CrewID           *string    `json:"crew_id"`
	SubcontractorID  *string    `json:"subcontractor_id"`
	ScheduledFor     *time.Time `json:"scheduled_for"`
	DueBy            *time.Time `json:"due_by"`
}

func raiseWorkOrderHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req := raiseWorkOrderRequest{}
		// Body is optional -- all overrides default from the defect.
		_ = json.NewDecoder(r.Body).Decode(&req)
		order, err := RaiseWorkOrderFromDefect(r.Context(), db, chi.URLParam(r, "id"), RaiseWorkOrderFromDefectParams{
			Title:            req.Title,
			Priority:         req.Priority,
			Description:      req.Description,
			AssignedTo:       req.AssignedTo,
			AssignedToUserID: req.AssignedToUserID,
			CrewID:           req.CrewID,
			SubcontractorID:  req.SubcontractorID,
			ScheduledFor:     req.ScheduledFor,
			DueBy:            req.DueBy,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, order)
	}
}

// ---------------------------------------------------------------------------
// work orders
// ---------------------------------------------------------------------------

func listWorkOrdersHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// ?mine=true scopes the board to the caller's own assignments (the field
		// tool's "my work orders"); an explicit assigned_to_user_id still wins.
		assignedToUserID := q.Get("assigned_to_user_id")
		if q.Get("mine") == "true" {
			if u, ok := auth.UserFrom(r.Context()); ok {
				assignedToUserID = u.ID
			}
		}
		orders, err := ListWorkOrders(r.Context(), db, WorkOrderFilter{
			Status:           q.Get("status"),
			WorkType:         q.Get("work_type"),
			Priority:         q.Get("priority"),
			AssetClass:       q.Get("asset_class"),
			AssetID:          q.Get("asset_id"),
			AssignedTo:       q.Get("assigned_to"),
			AssignedToUserID: assignedToUserID,
			CrewID:           q.Get("crew_id"),
			IncidentID:       q.Get("incident_id"),
			SubcontractorID:  q.Get("subcontractor_id"),
			Open:             q.Get("open") == "true",
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"work_orders": orders})
	}
}

type createWorkOrderRequest struct {
	Reference        *string    `json:"reference"`
	WorkType         string     `json:"work_type"`
	Priority         string     `json:"priority"`
	Title            string     `json:"title"`
	Description      *string    `json:"description"`
	AssetClass       *string    `json:"asset_class"`
	AssetID          *string    `json:"asset_id"`
	Longitude        *float64   `json:"longitude"`
	Latitude         *float64   `json:"latitude"`
	DefectID         *string    `json:"defect_id"`
	AssignedTo       *string    `json:"assigned_to"`
	AssignedToUserID *string    `json:"assigned_to_user_id"`
	CrewID           *string    `json:"crew_id"`
	SubcontractorID  *string    `json:"subcontractor_id"`
	ScheduledFor     *time.Time `json:"scheduled_for"`
	DueBy            *time.Time `json:"due_by"`
}

func createWorkOrderHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createWorkOrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		order, err := CreateWorkOrder(r.Context(), db, CreateWorkOrderParams{
			Reference:        req.Reference,
			WorkType:         req.WorkType,
			Priority:         req.Priority,
			Title:            req.Title,
			Description:      req.Description,
			AssetClass:       req.AssetClass,
			AssetID:          req.AssetID,
			Longitude:        req.Longitude,
			Latitude:         req.Latitude,
			DefectID:         req.DefectID,
			AssignedTo:       req.AssignedTo,
			AssignedToUserID: req.AssignedToUserID,
			CrewID:           req.CrewID,
			SubcontractorID:  req.SubcontractorID,
			ScheduledFor:     req.ScheduledFor,
			DueBy:            req.DueBy,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, order)
	}
}

func getWorkOrderHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		order, err := GetWorkOrder(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, order)
	}
}

type updateWorkOrderRequest struct {
	Status           *string    `json:"status"`
	Priority         *string    `json:"priority"`
	Title            *string    `json:"title"`
	Description      *string    `json:"description"`
	AssignedTo       *string    `json:"assigned_to"`
	AssignedToUserID *string    `json:"assigned_to_user_id"`
	CrewID           *string    `json:"crew_id"`
	SubcontractorID  *string    `json:"subcontractor_id"`
	ScheduledFor     *time.Time `json:"scheduled_for"`
	DueBy            *time.Time `json:"due_by"`
	CompletionNotes  *string    `json:"completion_notes"`
	ResolveDefect    *bool      `json:"resolve_defect"`
}

func updateWorkOrderHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req updateWorkOrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		order, err := UpdateWorkOrder(r.Context(), db, chi.URLParam(r, "id"), UpdateWorkOrderParams{
			Status:           req.Status,
			Priority:         req.Priority,
			Title:            req.Title,
			Description:      req.Description,
			AssignedTo:       req.AssignedTo,
			AssignedToUserID: req.AssignedToUserID,
			CrewID:           req.CrewID,
			SubcontractorID:  req.SubcontractorID,
			ScheduledFor:     req.ScheduledFor,
			DueBy:            req.DueBy,
			CompletionNotes:  req.CompletionNotes,
			ResolveDefect:    req.ResolveDefect,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, order)
	}
}

// ---------------------------------------------------------------------------
// inspection templates
// ---------------------------------------------------------------------------

func listTemplatesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		templates, err := ListTemplates(r.Context(), db, r.URL.Query().Get("active") == "true")
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"templates": templates})
	}
}

type templateItemRequest struct {
	Position     int     `json:"position"`
	Prompt       string  `json:"prompt"`
	ResponseType string  `json:"response_type"`
	FailSeverity *string `json:"fail_severity"`
	Guidance     *string `json:"guidance"`
}

type createTemplateRequest struct {
	Code        string                `json:"code"`
	Name        string                `json:"name"`
	Description *string               `json:"description"`
	AssetClass  *string               `json:"asset_class"`
	Items       []templateItemRequest `json:"items"`
}

func createTemplateHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createTemplateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		items := make([]TemplateItemInput, len(req.Items))
		for i, it := range req.Items {
			items[i] = TemplateItemInput{
				Position:     it.Position,
				Prompt:       it.Prompt,
				ResponseType: it.ResponseType,
				FailSeverity: it.FailSeverity,
				Guidance:     it.Guidance,
			}
		}
		tmpl, err := CreateTemplate(r.Context(), db, CreateTemplateParams{
			Code:        req.Code,
			Name:        req.Name,
			Description: req.Description,
			AssetClass:  req.AssetClass,
			Items:       items,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, tmpl)
	}
}

func getTemplateHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tmpl, err := GetTemplate(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tmpl)
	}
}

type setTemplateActiveRequest struct {
	Active bool `json:"active"`
}

func setTemplateActiveHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req setTemplateActiveRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		tmpl, err := SetTemplateActive(r.Context(), db, chi.URLParam(r, "id"), req.Active)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, tmpl)
	}
}

// ---------------------------------------------------------------------------
// inspections
// ---------------------------------------------------------------------------

func listInspectionsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		inspections, err := ListInspections(r.Context(), db, InspectionFilter{
			AssetClass:  q.Get("asset_class"),
			AssetID:     q.Get("asset_id"),
			WorkOrderID: q.Get("work_order_id"),
			TemplateID:  q.Get("template_id"),
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"inspections": inspections})
	}
}

type recordItemRequest struct {
	TemplateItemID *string  `json:"template_item_id"`
	Position       int      `json:"position"`
	Prompt         string   `json:"prompt"`
	ResponseType   string   `json:"response_type"`
	Passed         *bool    `json:"passed"`
	ValueText      *string  `json:"value_text"`
	ValueNumeric   *float64 `json:"value_numeric"`
	FailSeverity   *string  `json:"fail_severity"`
	Notes          *string  `json:"notes"`
}

type recordInspectionRequest struct {
	WorkOrderID       *string             `json:"work_order_id"`
	TemplateID        *string             `json:"template_id"`
	AssetClass        string              `json:"asset_class"`
	AssetID           string              `json:"asset_id"`
	InspectedBy       *string             `json:"inspected_by"`
	InspectedAt       *time.Time          `json:"inspected_at"`
	Notes             *string             `json:"notes"`
	Longitude         *float64            `json:"longitude"`
	Latitude          *float64            `json:"latitude"`
	OverallResult     *string             `json:"overall_result"`
	CompleteWorkOrder bool                `json:"complete_work_order"`
	Items             []recordItemRequest `json:"items"`
}

func recordInspectionHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req recordInspectionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		items := make([]RecordItemInput, len(req.Items))
		for i, it := range req.Items {
			items[i] = RecordItemInput{
				TemplateItemID: it.TemplateItemID,
				Position:       it.Position,
				Prompt:         it.Prompt,
				ResponseType:   it.ResponseType,
				Passed:         it.Passed,
				ValueText:      it.ValueText,
				ValueNumeric:   it.ValueNumeric,
				FailSeverity:   it.FailSeverity,
				Notes:          it.Notes,
			}
		}
		inspection, err := RecordInspection(r.Context(), db, RecordInspectionParams{
			WorkOrderID:       req.WorkOrderID,
			TemplateID:        req.TemplateID,
			AssetClass:        req.AssetClass,
			AssetID:           req.AssetID,
			InspectedBy:       req.InspectedBy,
			InspectedAt:       req.InspectedAt,
			Notes:             req.Notes,
			Longitude:         req.Longitude,
			Latitude:          req.Latitude,
			OverallResult:     req.OverallResult,
			CompleteWorkOrder: req.CompleteWorkOrder,
			Items:             items,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, inspection)
	}
}

func getInspectionHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inspection, err := GetInspection(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, inspection)
	}
}

// ---------------------------------------------------------------------------
// assignable users
// ---------------------------------------------------------------------------

func listAssignableUsersHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		users, err := ListAssignableUsers(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": users})
	}
}
