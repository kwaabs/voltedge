package oms

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/deviceops"
	"github.com/kwaabs/voltedge/api/internal/trace"
	"github.com/uptrace/bun"
)

// Routes returns the Outage Management endpoints:
//
//	GET  /trouble-calls              the call queue (optional ?status=)
//	POST /trouble-calls              log a call
//	POST /trouble-calls/{id}/associate  resolve the call's SP (manual or snap)
//	GET  /incidents                  the incident board (?status=, ?open=true)
//	GET  /incidents/map              incidents projected onto their device (map overlay)
//	GET  /incidents/{id}             one incident
//	GET  /incidents/{id}/affected    the incident's affected service points (map overlay)
//	GET  /incidents/{id}/timeline    the incident's lifecycle events + its calls
//	GET  /trouble-calls/{id}/timeline  a call's events + the incident it joined
//	GET  /incidents/{id}/energization  the incident's LIVE de-energized cone (current switch state)
//	GET  /incidents/{id}/restoration   FLISR: suggested back-feed plans to restore the dead section
//	POST /incidents/predict          roll open calls up into predicted incidents
//	POST /incidents/planned          schedule a planned outage around a device
//	POST /incidents/{id}/confirm     confirm/execute + snapshot interruptions
//	POST /incidents/{id}/dispatch    confirmed -> dispatched (crew en route)
//	POST /incidents/{id}/restore     mark restored, close interruption windows
//	POST /incidents/{id}/cancel      cancel + release the calls
//	GET  /incidents/{id}/switching-orders   the incident's switching orders
//	POST /incidents/{id}/switching-orders   generate a switching order from the device
//	POST /incidents/{id}/restoration-orders FLISR: generate a close-tie restoration order from a chosen tie
//	POST /switching-orders/{id}/clearance   issue a permit-to-work from the order (tags opened devices, grounds earthed points) — also needs safety:write
//	GET  /restoration                FLISR for an ad-hoc device (?device_class=&device_id=)
//	GET  /switching-orders/{id}      one switching order with its steps
//	POST /switching-orders/{id}/cancel      cancel a switching order
//	POST /switching-steps/{id}/execute      mark a step done / outstanding
//	GET  /indices                    reliability indices (SAIDI/SAIFI/CAIDI/ENS)
//
// The GET verbs read (oms:read) and the POST verbs write (oms:write); the
// method-aware gate RequirePermission(oms:read, oms:write) is applied on the
// mount in api/cmd/server/main.go, so this router carries no auth of its own --
// the same arrangement circuit and territory use.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()

	r.Get("/trouble-calls", listTroubleCallsHandler(db))
	r.Post("/trouble-calls", createTroubleCallHandler(db))
	r.Post("/trouble-calls/{id}/associate", associateTroubleCallHandler(db))
	r.Get("/trouble-calls/{id}/timeline", callTimelineHandler(db))

	// The geographic lens: cluster open calls into trouble spots, and dispatch a
	// crew to one by raising an unattributed incident from its calls.
	r.Get("/trouble-spots", listTroubleSpotsHandler(db))
	r.Post("/trouble-spots/raise", raiseIncidentFromSpotHandler(db))

	// Crew / resource management (blocker #2): crew entities + assignment lifecycle.
	r.Get("/crews", listCrewsHandler(db))
	r.Post("/crews", createCrewHandler(db))
	r.Patch("/crews/{id}", updateCrewHandler(db))
	r.Post("/crews/{id}/availability", setCrewAvailabilityHandler(db))
	r.Delete("/crews/{id}", deactivateCrewHandler(db))
	r.Get("/crews/{id}/members", listCrewMembersHandler(db))
	r.Post("/crews/{id}/members", addCrewMemberHandler(db))
	r.Delete("/crews/{id}/members/{userId}", removeCrewMemberHandler(db))
	r.Get("/crews/{id}/assignments", listCrewAssignmentsHandler(db))
	r.Get("/incidents/{id}/crews", listIncidentCrewsHandler(db))
	r.Post("/incidents/{id}/crews", dispatchCrewHandler(db))
	r.Post("/incidents/{id}/subcontractor", taskSubcontractorHandler(db))
	r.Patch("/crew-assignments/{id}", advanceAssignmentHandler(db))
	r.Patch("/incidents/{id}/etr", setIncidentETRHandler(db))

	// Customer callback queue (blocker #5, CIS/IVR): the external CIS pulls the
	// pending list and marks each notified; one callback per affected supply is
	// enqueued on restore.
	r.Get("/callbacks", listCallbacksHandler(db))
	r.Get("/incidents/{id}/callbacks", listIncidentCallbacksHandler(db))
	r.Post("/callbacks/{id}/notified", markCallbackNotifiedHandler(db))
	r.Post("/callbacks/{id}/cancel", cancelCallbackHandler(db))

	r.Get("/incidents", listIncidentsHandler(db))
	r.Get("/incidents/map", listIncidentsForMapHandler(db))
	r.Post("/incidents/predict", predictIncidentsHandler(db))
	r.Post("/incidents/planned", createPlannedOutageHandler(db))
	r.Get("/outage-causes", listOutageCausesHandler(db))
	r.Get("/incidents/{id}", getIncidentHandler(db))
	r.Get("/incidents/{id}/device-candidates", incidentDeviceCandidatesHandler(db))
	r.Patch("/incidents/{id}/attribution", setIncidentAttributionHandler(db))
	r.Patch("/incidents/{id}/device", attributeIncidentDeviceHandler(db))
	r.Get("/incidents/{id}/affected", affectedAreaHandler(db))
	r.Get("/incidents/{id}/timeline", incidentTimelineHandler(db))
	r.Get("/incidents/{id}/energization", energizationHandler(db))
	r.Get("/incidents/{id}/restoration", restorationForIncidentHandler(db))
	r.Post("/incidents/{id}/confirm", incidentActionHandler(db, ConfirmIncident))
	r.Post("/incidents/{id}/dispatch", incidentActionHandler(db, DispatchIncident))
	r.Post("/incidents/{id}/restore", incidentActionHandler(db, RestoreIncident))
	r.Post("/incidents/{id}/cancel", incidentActionHandler(db, CancelIncident))

	r.Get("/incidents/{id}/switching-orders", listSwitchingOrdersHandler(db))
	r.Post("/incidents/{id}/switching-orders", generateSwitchingOrderHandler(db))
	r.Post("/incidents/{id}/restoration-orders", generateRestorationOrderHandler(db))
	r.Get("/restoration", restorationForDeviceHandler(db))

	r.Post("/switching-orders/{id}/clearance", issueClearanceHandler(db))
	r.Get("/switching-orders/{id}", getSwitchingOrderHandler(db))
	r.Post("/switching-orders/{id}/cancel", cancelSwitchingOrderHandler(db))
	r.Post("/switching-steps/{id}/execute", executeStepHandler(db))

	// Operate a single device directly (open/close) outside a switching order --
	// the one-click "open this device to break a loop" (and close-back) action.
	// Same safety gate + device-operation log as a switching step.
	r.Post("/devices/{class}/{id}/operate", operateDeviceHandler(db))

	r.Get("/indices", reliabilityIndicesHandler(db))
	r.Get("/detection-metrics", detectionMetricsHandler(db))
	r.Get("/area-impact", areaImpactHandler(db))

	return r
}

// areaImpactHandler returns the network-wide outage impact rolled up by territory
// (region → district → …) across all open incidents -- "where is it worst now".
func areaImpactHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		impact, err := ComputeAreaImpact(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, impact)
	}
}

// detectionMetricsHandler returns the source/cause analytics for a window
// (?from=&to= RFC3339, default trailing 12 months): the detection-source mix,
// the machine-detected (awareness) share, the cause-family breakdown, and the
// mean detection->restore time.
func detectionMetricsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var from, to *time.Time
		if s := q.Get("from"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				writeError(w, invalidf("invalid from: want RFC3339, got %q", s))
				return
			}
			from = &t
		}
		if s := q.Get("to"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				writeError(w, invalidf("invalid to: want RFC3339, got %q", s))
				return
			}
			to = &t
		}
		metrics, err := ComputeDetectionMetrics(r.Context(), db, from, to)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, metrics)
	}
}

// operateDeviceHandler flips one switching device's device_state to OPEN or CLOSED
// (OperateDevice: safety-gated, validated, history-logged) and records a crew-source
// device operation, atomically. Backs the loop-fix "Open" action and its close-back.
func operateDeviceHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		class := chi.URLParam(r, "class")
		id := chi.URLParam(r, "id")
		var body struct {
			State string `json:"state"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		state := strings.ToUpper(strings.TrimSpace(body.State))
		if state != deviceStateOpen && state != deviceStateClosed {
			writeError(w, invalidf("state must be %q or %q", deviceStateOpen, deviceStateClosed))
			return
		}
		opType := "close"
		if state == deviceStateOpen {
			opType = "open"
		}

		if err := db.RunInTx(r.Context(), nil, func(ctx context.Context, tx bun.Tx) error {
			if e := OperateDevice(ctx, tx, class, id, state); e != nil {
				return e
			}
			_, e := deviceops.Record(ctx, tx, deviceops.RecordInput{
				DeviceClass: class,
				DeviceID:    id,
				OpType:      opType,
				Source:      "crew",
				OccurredAt:  time.Now(),
			})
			return e
		}); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"device_class": class, "device_id": id, "device_state": state})
	}
}

// ---------------------------------------------------------------------------
// trouble calls
// ---------------------------------------------------------------------------

func listTroubleCallsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// limit defaults to 0 (all, so callers scoped by incident_id or small
		// deployments are unaffected); the board passes limit=20/50 to paginate a
		// storm's volume. Capped so a client can't request an unbounded page.
		limit, offset := 0, 0
		if s := q.Get("limit"); s != "" {
			if v, err := strconv.Atoi(s); err == nil && v >= 0 {
				limit = v
			}
		}
		if limit > 200 {
			limit = 200
		}
		if s := q.Get("offset"); s != "" {
			if v, err := strconv.Atoi(s); err == nil && v >= 0 {
				offset = v
			}
		}
		calls, total, err := ListTroubleCalls(r.Context(), db, TroubleCallFilter{
			Status:     q.Get("status"),
			IncidentID: q.Get("incident_id"),
			Limit:      limit,
			Offset:     offset,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"trouble_calls": calls, "total": total})
	}
}

// createTroubleCallRequest is the POST body. Every locator is optional
// individually; the store enforces that at least one is present.
type createTroubleCallRequest struct {
	ServicePointID *string    `json:"service_point_id"`
	Address        *string    `json:"address"`
	Longitude      *float64   `json:"longitude"`
	Latitude       *float64   `json:"latitude"`
	ReportedAt     *time.Time `json:"reported_at"`
	CallerRef      *string    `json:"caller_ref"`
	AccountRef     *string    `json:"account_ref"`
	Phone          *string    `json:"phone"`
	CallType       string     `json:"call_type"`
	Notes          *string    `json:"notes"`
}

func createTroubleCallHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createTroubleCallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		call, err := CreateTroubleCall(r.Context(), db, CreateCallParams{
			ServicePointID: req.ServicePointID,
			Address:        req.Address,
			Longitude:      req.Longitude,
			Latitude:       req.Latitude,
			ReportedAt:     req.ReportedAt,
			CallerRef:      req.CallerRef,
			AccountRef:     req.AccountRef,
			Phone:          req.Phone,
			CallType:       req.CallType,
			Notes:          req.Notes,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, call)
	}
}

// associateTroubleCallRequest resolves the call's service point: pass a
// service_point_id for a manual association, or snap=true to bind the nearest
// service point to the call's own location.
type associateTroubleCallRequest struct {
	ServicePointID *string `json:"service_point_id"`
	Snap           bool    `json:"snap"`
}

func associateTroubleCallHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req associateTroubleCallRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		call, err := AssociateTroubleCall(r.Context(), db, id, req.ServicePointID, req.Snap)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, call)
	}
}

// listTroubleSpotsHandler clusters the open, located calls into geographic trouble
// spots (?eps= join distance in metres, default 250) a crew can be dispatched to.
func listTroubleSpotsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		eps := 0.0
		if s := r.URL.Query().Get("eps"); s != "" {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil || v < 0 {
				writeError(w, invalidf("invalid eps: want a non-negative number of metres, got %q", s))
				return
			}
			eps = v
		}
		spots, err := ClusterTroubleSpots(r.Context(), db, eps)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"trouble_spots": spots})
	}
}

// raiseIncidentFromSpotHandler dispatches a crew to a trouble spot by raising a
// confirmed, unattributed incident from its calls.
func raiseIncidentFromSpotHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			CallIDs []string `json:"call_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		inc, err := RaiseIncidentFromCalls(r.Context(), db, req.CallIDs)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, inc)
	}
}

// ---------------------------------------------------------------------------
// crews (blocker #2)
// ---------------------------------------------------------------------------

func listCrewsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		crews, err := ListCrews(r.Context(), db, r.URL.Query().Get("include_inactive") == "true")
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"crews": crews})
	}
}

type crewRequest struct {
	Name        string  `json:"name"`
	Kind        *string `json:"kind"`
	Phone       *string `json:"phone"`
	TerritoryID *string `json:"territory_id"`
}

func (c crewRequest) params() CrewParams {
	return CrewParams{Name: c.Name, Kind: c.Kind, Phone: c.Phone, TerritoryID: c.TerritoryID}
}

func createCrewHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req crewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		crew, err := CreateCrew(r.Context(), db, req.params())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, crew)
	}
}

func updateCrewHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req crewRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		crew, err := UpdateCrew(r.Context(), db, chi.URLParam(r, "id"), req.params())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, crew)
	}
}

func setCrewAvailabilityHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Available bool `json:"available"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		crew, err := SetCrewAvailability(r.Context(), db, chi.URLParam(r, "id"), req.Available)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, crew)
	}
}

func deactivateCrewHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := DeactivateCrew(r.Context(), db, chi.URLParam(r, "id")); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"deactivated": true})
	}
}

func listCrewMembersHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		members, err := ListCrewMembers(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"members": members})
	}
}

func addCrewMemberHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UserID     string     `json:"user_id"`
			Role       *string    `json:"role"`
			ValidUntil *time.Time `json:"valid_until"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		m, err := AddCrewMember(r.Context(), db, chi.URLParam(r, "id"), req.UserID, req.Role, req.ValidUntil)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, m)
	}
}

func removeCrewMemberHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := RemoveCrewMember(r.Context(), db, chi.URLParam(r, "id"), chi.URLParam(r, "userId")); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
	}
}

func listCrewAssignmentsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		assignments, err := ListCrewAssignments(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"assignments": assignments})
	}
}

func listIncidentCrewsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		crews, err := ListIncidentCrews(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"assignments": crews})
	}
}

func dispatchCrewHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			CrewID         string  `json:"crew_id"`
			Note           *string `json:"note"`
			RaiseWorkOrder bool    `json:"raise_work_order"`
			WorkOrderTitle *string `json:"work_order_title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		if strings.TrimSpace(req.CrewID) == "" {
			writeError(w, invalidf("crew_id is required"))
			return
		}
		ca, err := DispatchCrewToIncident(r.Context(), db, req.CrewID, chi.URLParam(r, "id"), req.Note,
			DispatchOptions{RaiseWorkOrder: req.RaiseWorkOrder, WorkOrderTitle: req.WorkOrderTitle})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, ca)
	}
}

func taskSubcontractorHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			SubcontractorID string  `json:"subcontractor_id"`
			Title           *string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		if strings.TrimSpace(req.SubcontractorID) == "" {
			writeError(w, invalidf("subcontractor_id is required"))
			return
		}
		wo, err := TaskSubcontractorToIncident(r.Context(), db, chi.URLParam(r, "id"), req.SubcontractorID, req.Title)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, wo)
	}
}

func listCallbacksHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		callbacks, err := ListCallbacks(r.Context(), db, CallbackFilter{
			Status:     q.Get("status"),
			IncidentID: q.Get("incident_id"),
			Limit:      limit,
			Offset:     offset,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"callbacks": callbacks})
	}
}

func listIncidentCallbacksHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		callbacks, err := ListCallbacks(r.Context(), db, CallbackFilter{IncidentID: chi.URLParam(r, "id")})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"callbacks": callbacks})
	}
}

func markCallbackNotifiedHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Note *string `json:"note"`
		}
		// Body optional (a bare mark-notified carries no note).
		_ = json.NewDecoder(r.Body).Decode(&req)
		cb, err := MarkCallbackNotified(r.Context(), db, chi.URLParam(r, "id"), req.Note)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, cb)
	}
}

func cancelCallbackHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cb, err := CancelCallback(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, cb)
	}
}

func advanceAssignmentHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		ca, err := AdvanceCrewAssignment(r.Context(), db, chi.URLParam(r, "id"), req.Status)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, ca)
	}
}

func setIncidentETRHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ETR *time.Time `json:"etr"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		inc, err := SetIncidentETR(r.Context(), db, chi.URLParam(r, "id"), req.ETR)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, inc)
	}
}

// ---------------------------------------------------------------------------
// incidents
// ---------------------------------------------------------------------------

func listIncidentsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		incidents, err := ListIncidents(r.Context(), db, q.Get("status"), q.Get("open") == "true", q.Get("territory_id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"incidents": incidents})
	}
}

// listIncidentsForMapHandler returns incidents projected onto their predicted
// device (an on-geometry marker point + the device geometry) for the GIS map
// overlay -- the twin of the fault overlay's /faults/map. The overlay fetches
// every status once and filters restored/cancelled client-side via the legend,
// so no open-only filter is forced here.
func listIncidentsForMapHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		markers, err := ListIncidentsForMap(r.Context(), db, q.Get("status"), q.Get("open") == "true")
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"incidents": markers})
	}
}

func getIncidentHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inc, err := GetIncident(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, inc)
	}
}

// listOutageCausesHandler returns the coded cause catalog for the incident cause
// picker (the coarse outage-level "why"; the detailed FMEA is on the fault).
func listOutageCausesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		causes, err := ListOutageCauses(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"outage_causes": causes})
	}
}

// incidentDeviceCandidatesHandler suggests the protective devices to attribute an
// incident to (the devices upstream of its calling service points), for the
// re-attribution picker's quick-pick chips.
func incidentDeviceCandidatesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		candidates, err := IncidentDeviceCandidates(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"candidates": candidates})
	}
}

// attributeIncidentDeviceHandler sets/changes the protective device an incident is
// blamed on and re-traces its cone (affected + territory), re-freezing a confirmed
// incident's interruptions. The operator override prediction can't do.
func attributeIncidentDeviceHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DeviceClass string `json:"device_class"`
			DeviceID    string `json:"device_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		inc, err := AttributeIncidentDevice(r.Context(), db, chi.URLParam(r, "id"), req.DeviceClass, req.DeviceID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, inc)
	}
}

// setIncidentAttributionHandler refines an incident's cause (code + status) and/or
// corrects its detection source -- a partial update, only the supplied fields apply.
func setIncidentAttributionHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req SetAttributionParams
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		inc, err := SetIncidentAttribution(r.Context(), db, chi.URLParam(r, "id"), req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, inc)
	}
}

// affectedAreaHandler returns the selected incident's outage cone as service
// point points, for the map to highlight. Frozen once confirmed, live-traced
// while still predicted (see store.IncidentAffectedArea).
func affectedAreaHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		area, err := IncidentAffectedArea(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, area)
	}
}

// callTimelineHandler returns a trouble call's lifecycle timeline -- its own
// events plus the incident it rolled into -- for the call detail panel.
func callTimelineHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		events, err := CallTimeline(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events})
	}
}

// incidentTimelineHandler returns an incident's lifecycle timeline plus the
// "received" events of the calls that fed it.
func incidentTimelineHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		events, err := IncidentTimeline(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events})
	}
}

// energizationHandler returns the selected incident's LIVE de-energized cone --
// what is cut off from supply given the network's current switch positions, so
// the map tracks the crew's switching in real time (see store.IncidentEnergization).
func energizationHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, err := IncidentEnergization(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, state)
	}
}

// restorationForIncidentHandler suggests back-feed restoration plans for an
// incident (FLISR): the normally-open ties that could re-energize its dead section
// from an alternate source, with restored-load and capacity. Read-only.
func restorationForIncidentHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		suggestion, err := SuggestRestorationForIncident(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, suggestion)
	}
}

// restorationForDeviceHandler is the ad-hoc counterpart: suggest restoration for
// any open device named by ?device_class=&device_id=, without an incident.
func restorationForDeviceHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		class := r.URL.Query().Get("device_class")
		id := r.URL.Query().Get("device_id")
		if class == "" || id == "" {
			writeError(w, invalidf("device_class and device_id are required"))
			return
		}
		suggestion, err := SuggestRestoration(r.Context(), db, trace.FeatureRef{FeatureClass: class, FeatureID: id})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, suggestion)
	}
}

func predictIncidentsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		created, err := PredictAndPersist(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"incidents": created})
	}
}

// createPlannedOutageRequest schedules a planned outage around operating one
// device. The device is required; the window and metadata are optional.
type createPlannedOutageRequest struct {
	DeviceClass  string     `json:"device_class"`
	DeviceID     string     `json:"device_id"`
	PlannedStart *time.Time `json:"planned_start"`
	PlannedEnd   *time.Time `json:"planned_end"`
	CauseCode    *string    `json:"cause_code"`
	CircuitID    *string    `json:"circuit_id"`
	Notes        *string    `json:"notes"`
}

func createPlannedOutageHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createPlannedOutageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		inc, err := CreatePlannedOutage(r.Context(), db, PlannedOutageParams{
			DeviceClass:  req.DeviceClass,
			DeviceID:     req.DeviceID,
			PlannedStart: req.PlannedStart,
			PlannedEnd:   req.PlannedEnd,
			CauseCode:    req.CauseCode,
			CircuitID:    req.CircuitID,
			Notes:        req.Notes,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, inc)
	}
}

// ---------------------------------------------------------------------------
// switching orders
// ---------------------------------------------------------------------------

func listSwitchingOrdersHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orders, err := ListSwitchingOrders(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"switching_orders": orders})
	}
}

func generateSwitchingOrderHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		order, err := GenerateSwitchingOrder(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, order)
	}
}

// generateRestorationOrderRequest names the tie to close (FLISR Slice 2). The
// tie must be one SuggestRestoration currently suggests for this incident.
type generateRestorationOrderRequest struct {
	TieClass string `json:"tie_class"`
	TieID    string `json:"tie_id"`
}

func generateRestorationOrderHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req generateRestorationOrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		if req.TieClass == "" || req.TieID == "" {
			writeError(w, invalidf("tie_class and tie_id are required"))
			return
		}
		order, err := GenerateRestorationOrder(r.Context(), db, chi.URLParam(r, "id"), req.TieClass, req.TieID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, order)
	}
}

// issueClearanceRequest is POST /switching-orders/{id}/clearance's body (Slice 3).
type issueClearanceRequest struct {
	WorkDescription *string `json:"work_description"`
	IssuedTo        *string `json:"issued_to"`
}

// issueClearanceHandler issues a permit-to-work from a switching order, tagging
// the devices it opens and grounding the points it earths. It escalates beyond the
// mount's oms:write to also require safety:write in-handler -- issuing a clearance
// (and so placing blocking tags) is a safety authority, not a general switching
// right (mirrors the diagram-promote metadata:write escalation).
func issueClearanceHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok || !(user.BreakGlass || user.Has(auth.PermSafetyWrite)) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "you do not have the required permission (safety:write)"})
			return
		}
		var req issueClearanceRequest
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&req) // body is optional
		}
		c, err := IssueClearanceFromOrder(r.Context(), db, chi.URLParam(r, "id"), req.WorkDescription, req.IssuedTo)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, c)
	}
}

func getSwitchingOrderHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		order, err := GetSwitchingOrder(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, order)
	}
}

func cancelSwitchingOrderHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		order, err := CancelSwitchingOrder(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, order)
	}
}

// executeStepRequest toggles a step's executed state. Absent body defaults to
// executed=true (the common "tick it off" case).
type executeStepRequest struct {
	Executed *bool `json:"executed"`
}

func executeStepHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req := executeStepRequest{}
		// Body is optional; ignore an empty/malformed body and default to true.
		_ = json.NewDecoder(r.Body).Decode(&req)
		executed := true
		if req.Executed != nil {
			executed = *req.Executed
		}
		order, err := SetStepExecuted(r.Context(), db, chi.URLParam(r, "id"), executed)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, order)
	}
}

// reliabilityIndicesHandler computes SAIDI/SAIFI/CAIDI/ENS over the confirmed
// interruption history. Optional query params: from / to (RFC3339, default the
// trailing 12 months) narrow the analysis window; avg_load_kw overrides the
// per-customer demand assumption behind the (estimated) ENS; circuit_id scopes
// the whole report to the service points in one circuit (per-feeder indices).
func reliabilityIndicesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var p IndicesParams

		if s := q.Get("from"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				writeError(w, invalidf("invalid from: want RFC3339, got %q", s))
				return
			}
			p.From = &t
		}
		if s := q.Get("to"); s != "" {
			t, err := time.Parse(time.RFC3339, s)
			if err != nil {
				writeError(w, invalidf("invalid to: want RFC3339, got %q", s))
				return
			}
			p.To = &t
		}
		if s := q.Get("avg_load_kw"); s != "" {
			v, err := strconv.ParseFloat(s, 64)
			if err != nil {
				writeError(w, invalidf("invalid avg_load_kw: want a number, got %q", s))
				return
			}
			p.AvgLoadKW = &v
		}
		if s := q.Get("circuit_id"); s != "" {
			p.CircuitID = &s
		}

		report, err := ReliabilityIndicesReport(r.Context(), db, p)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

// incidentActionHandler adapts the confirm/restore/cancel lifecycle functions
// (all *bun.DB, id -> (*Incident, error)) to a chi handler, so the three share
// one thin shell.
func incidentActionHandler(db *bun.DB, action func(context.Context, *bun.DB, string) (*Incident, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inc, err := action(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, inc)
	}
}
