package oms

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
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
//	GET  /incidents/{id}/energization  the incident's LIVE de-energized cone (current switch state)
//	POST /incidents/predict          roll open calls up into predicted incidents
//	POST /incidents/planned          schedule a planned outage around a device
//	POST /incidents/{id}/confirm     confirm/execute + snapshot interruptions
//	POST /incidents/{id}/restore     mark restored, close interruption windows
//	POST /incidents/{id}/cancel      cancel + release the calls
//	POST /incidents/{id}/attribute   name (or correct) the blamed device of a predicted/scheduled incident
//	GET  /incidents/{id}/switching-orders   the incident's switching orders
//	POST /incidents/{id}/switching-orders   generate a switching order from the device
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

	r.Get("/incidents", listIncidentsHandler(db))
	r.Get("/incidents/map", listIncidentsForMapHandler(db))
	r.Post("/incidents/predict", predictIncidentsHandler(db))
	r.Post("/incidents/planned", createPlannedOutageHandler(db))
	r.Get("/incidents/{id}", getIncidentHandler(db))
	r.Get("/incidents/{id}/affected", affectedAreaHandler(db))
	r.Get("/incidents/{id}/energization", energizationHandler(db))
	r.Post("/incidents/{id}/confirm", incidentActionHandler(db, ConfirmIncident))
	r.Post("/incidents/{id}/restore", incidentActionHandler(db, RestoreIncident))
	r.Post("/incidents/{id}/cancel", incidentActionHandler(db, CancelIncident))
	r.Post("/incidents/{id}/attribute", attributeIncidentHandler(db))

	r.Get("/incidents/{id}/switching-orders", listSwitchingOrdersHandler(db))
	r.Post("/incidents/{id}/switching-orders", generateSwitchingOrderHandler(db))
	r.Get("/switching-orders/{id}", getSwitchingOrderHandler(db))
	r.Post("/switching-orders/{id}/cancel", cancelSwitchingOrderHandler(db))
	r.Post("/switching-steps/{id}/execute", executeStepHandler(db))

	r.Get("/indices", reliabilityIndicesHandler(db))

	return r
}

// ---------------------------------------------------------------------------
// trouble calls
// ---------------------------------------------------------------------------

func listTroubleCallsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		calls, err := ListTroubleCalls(r.Context(), db, r.URL.Query().Get("status"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"trouble_calls": calls})
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

// ---------------------------------------------------------------------------
// incidents
// ---------------------------------------------------------------------------

func listIncidentsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		incidents, err := ListIncidents(r.Context(), db, q.Get("status"), q.Get("open") == "true")
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

// attributeIncidentRequest is the POST /incidents/{id}/attribute body.
type attributeIncidentRequest struct {
	DeviceClass string `json:"device_class"`
	DeviceID    string `json:"device_id"`
}

func attributeIncidentHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req attributeIncidentRequest
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
