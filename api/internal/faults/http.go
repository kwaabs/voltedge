package faults

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns the Faults endpoints:
//
//	GET   /faults                     the fault board (?status=, ?open=true, ?fault_type=, ?asset_class=&asset_id=, ?incident_id=)
//	GET   /faults/map                 faults with a placeable point, for the map overlay (same filters)
//	POST  /faults                     log a fault event
//	GET   /faults/{id}                one fault event
//	GET   /faults/{id}/history        the fault's audit trail (who changed what, when)
//	GET   /faults/{id}/impact         the fault's de-energized downstream section (for the overlay)
//	PATCH /faults/{id}                patch a fault (investigation findings)
//	POST  /faults/{id}/status         transition a fault (detected/investigating/cleared/closed)
//	POST  /faults/{id}/defect         book a WAM defect from the fault (links defect_id)
//	POST  /faults/from-incident/{id}  raise a fault from an OMS incident (links incident_id)
//
// The GET verbs read (faults:read) and the mutating verbs write (faults:write);
// the method-aware gate RequirePermission(faults:read, faults:write) is applied
// on the mount in api/cmd/server/main.go, so this router carries no auth of its
// own -- the same arrangement oms / wam use.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()

	// Routes are relative to the /faults mount point (see cmd/server/main.go) --
	// the same convention oms (/incidents, /trouble-calls) and wam (/defects,
	// /work-orders) follow. Mounting at /faults AND prefixing each route with
	// /faults would double the prefix to /faults/faults; the fault event is this
	// router's root resource, so it lives at "/".
	r.Get("/", listFaultsHandler(db))
	r.Get("/map", listFaultsForMapHandler(db))
	r.Post("/", createFaultHandler(db))
	r.Get("/{id}", getFaultHandler(db))
	r.Get("/{id}/history", faultHistoryHandler(db))
	r.Get("/{id}/impact", faultImpactHandler(db))
	r.Patch("/{id}", updateFaultHandler(db))
	r.Post("/{id}/status", setFaultStatusHandler(db))
	r.Post("/{id}/defect", bookDefectHandler(db))
	r.Post("/from-incident/{id}", raiseFromIncidentHandler(db))

	return r
}

func listFaultsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		faults, err := ListFaults(r.Context(), db, FaultFilter{
			Status:     q.Get("status"),
			FaultType:  q.Get("fault_type"),
			AssetClass: q.Get("asset_class"),
			AssetID:    q.Get("asset_id"),
			IncidentID: q.Get("incident_id"),
			Open:       q.Get("open") == "true",
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"faults": faults})
	}
}

// listFaultsForMapHandler backs the map overlay: the same filter as the board,
// but only faults with a placeable point (asset centroid or location pin), each
// carrying its resolved longitude/latitude.
func listFaultsForMapHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		faults, err := ListFaultsForMap(r.Context(), db, FaultFilter{
			Status:     q.Get("status"),
			FaultType:  q.Get("fault_type"),
			AssetClass: q.Get("asset_class"),
			AssetID:    q.Get("asset_id"),
			IncidentID: q.Get("incident_id"),
			Open:       q.Get("open") == "true",
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"faults": faults})
	}
}

type createFaultRequest struct {
	Reference             *string    `json:"reference"`
	AssetClass            *string    `json:"asset_class"`
	AssetID               *string    `json:"asset_id"`
	Longitude             *float64   `json:"longitude"`
	Latitude              *float64   `json:"latitude"`
	FaultType             *string    `json:"fault_type"`
	Cause                 *string    `json:"cause"`
	FaultCurrentA         *float64   `json:"fault_current_a"`
	ProtectiveDeviceClass *string    `json:"protective_device_class"`
	ProtectiveDeviceID    *string    `json:"protective_device_id"`
	ClearingTimeMS        *float64   `json:"clearing_time_ms"`
	DetectedBy            *string    `json:"detected_by"`
	DetectedAt            *time.Time `json:"detected_at"`
	IncidentID            *string    `json:"incident_id"`
	DefectID              *string    `json:"defect_id"`
	Title                 string     `json:"title"`
	Description           *string    `json:"description"`
	Notes                 *string    `json:"notes"`
}

func createFaultHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createFaultRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		fault, err := CreateFault(r.Context(), db, CreateFaultParams{
			Reference:             req.Reference,
			AssetClass:            req.AssetClass,
			AssetID:               req.AssetID,
			Longitude:             req.Longitude,
			Latitude:              req.Latitude,
			FaultType:             req.FaultType,
			Cause:                 req.Cause,
			FaultCurrentA:         req.FaultCurrentA,
			ProtectiveDeviceClass: req.ProtectiveDeviceClass,
			ProtectiveDeviceID:    req.ProtectiveDeviceID,
			ClearingTimeMS:        req.ClearingTimeMS,
			DetectedBy:            req.DetectedBy,
			DetectedAt:            req.DetectedAt,
			IncidentID:            req.IncidentID,
			DefectID:              req.DefectID,
			Title:                 req.Title,
			Description:           req.Description,
			Notes:                 req.Notes,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, fault)
	}
}

func getFaultHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fault, err := GetFault(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, fault)
	}
}

func faultHistoryHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := ListFaultHistory(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"history": entries})
	}
}

// faultImpactHandler returns a fault's de-energized downstream section (the cone
// below its protective device) for the map overlay.
func faultImpactHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		impact, err := FaultImpactArea(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, impact)
	}
}

type updateFaultRequest struct {
	Reference             *string  `json:"reference"`
	FaultType             *string  `json:"fault_type"`
	Cause                 *string  `json:"cause"`
	FaultCurrentA         *float64 `json:"fault_current_a"`
	ProtectiveDeviceClass *string  `json:"protective_device_class"`
	ProtectiveDeviceID    *string  `json:"protective_device_id"`
	ClearingTimeMS        *float64 `json:"clearing_time_ms"`
	DetectedBy            *string  `json:"detected_by"`
	IncidentID            *string  `json:"incident_id"`
	Title                 *string  `json:"title"`
	Description           *string  `json:"description"`
	Notes                 *string  `json:"notes"`
}

func updateFaultHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req updateFaultRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		fault, err := UpdateFault(r.Context(), db, chi.URLParam(r, "id"), UpdateFaultParams{
			Reference:             req.Reference,
			FaultType:             req.FaultType,
			Cause:                 req.Cause,
			FaultCurrentA:         req.FaultCurrentA,
			ProtectiveDeviceClass: req.ProtectiveDeviceClass,
			ProtectiveDeviceID:    req.ProtectiveDeviceID,
			ClearingTimeMS:        req.ClearingTimeMS,
			DetectedBy:            req.DetectedBy,
			IncidentID:            req.IncidentID,
			Title:                 req.Title,
			Description:           req.Description,
			Notes:                 req.Notes,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, fault)
	}
}

type setFaultStatusRequest struct {
	Status string `json:"status"`
}

func setFaultStatusHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req setFaultStatusRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		fault, err := SetFaultStatus(r.Context(), db, chi.URLParam(r, "id"), req.Status)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, fault)
	}
}

type bookDefectRequest struct {
	Title       *string `json:"title"`
	DefectType  *string `json:"defect_type"`
	Severity    *string `json:"severity"`
	Description *string `json:"description"`
	ReportedBy  *string `json:"reported_by"`
}

func bookDefectHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req := bookDefectRequest{}
		// Body is optional -- all overrides default from the fault.
		_ = json.NewDecoder(r.Body).Decode(&req)
		fault, err := BookDefectFromFault(r.Context(), db, chi.URLParam(r, "id"), BookDefectFromFaultParams{
			Title:       req.Title,
			DefectType:  req.DefectType,
			Severity:    req.Severity,
			Description: req.Description,
			ReportedBy:  req.ReportedBy,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, fault)
	}
}

type raiseFromIncidentRequest struct {
	Title      *string `json:"title"`
	FaultType  *string `json:"fault_type"`
	Cause      *string `json:"cause"`
	DetectedBy *string `json:"detected_by"`
	Notes      *string `json:"notes"`
}

func raiseFromIncidentHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req := raiseFromIncidentRequest{}
		// Body is optional -- all overrides default from the incident.
		_ = json.NewDecoder(r.Body).Decode(&req)
		fault, err := RaiseFaultFromIncident(r.Context(), db, chi.URLParam(r, "id"), RaiseFaultFromIncidentParams{
			Title:      req.Title,
			FaultType:  req.FaultType,
			Cause:      req.Cause,
			DetectedBy: req.DetectedBy,
			Notes:      req.Notes,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, fault)
	}
}
