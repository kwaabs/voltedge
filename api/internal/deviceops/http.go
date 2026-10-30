package deviceops

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns the device-operation log endpoints:
//
//	GET  /operations                  the operations board (newest first), filters:
//	                                   ?device_class=&device_id=&op_type=&source=&since=&until=&limit=
//	POST /operations                  record one device operation
//	GET  /operations/counts           per-device operation tally (?op_type=&since=), busiest first
//	GET  /operations/inventory        every operable device with current state + last op (?device_class=&state=)
//	GET  /operations/device/{class}/{id}  one device's operation timeline
//	GET  /operations/{id}             one operation
//
// GET reads (operations:read), POST writes (operations:write); the method-aware
// gate RequirePermission(operations:read, operations:write) is applied on the
// mount in api/cmd/server/main.go -- this router carries no auth of its own, the
// same arrangement faults / oms / wam use.
//
// Routes are relative to the /operations mount, so the board lives at "/".
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/", listHandler(db))
	r.Post("/", recordHandler(db))
	r.Get("/counts", countsHandler(db))
	r.Get("/inventory", inventoryHandler(db))
	r.Get("/abnormal", abnormalHandler(db))
	r.Get("/device/{class}/{id}", deviceTimelineHandler(db))
	r.Get("/{id}", getHandler(db))
	return r
}

// parseTime reads an optional RFC3339 query param. A present-but-unparseable
// value is a client error; absent returns (nil, nil).
func parseTime(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, invalidf("not an RFC3339 timestamp: %q", raw)
	}
	return &t, nil
}

func listHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		since, err := parseTime(q.Get("since"))
		if err != nil {
			writeError(w, err)
			return
		}
		until, err := parseTime(q.Get("until"))
		if err != nil {
			writeError(w, err)
			return
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		ops, err := List(r.Context(), db, Filter{
			DeviceClass: q.Get("device_class"),
			DeviceID:    q.Get("device_id"),
			OpType:      q.Get("op_type"),
			Source:      q.Get("source"),
			Since:       since,
			Until:       until,
			Limit:       limit,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"operations": ops})
	}
}

func deviceTimelineHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		ops, err := List(r.Context(), db, Filter{
			DeviceClass: chi.URLParam(r, "class"),
			DeviceID:    chi.URLParam(r, "id"),
			Limit:       limit,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"operations": ops})
	}
}

func getHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		op, err := Get(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		if op == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such operation"})
			return
		}
		writeJSON(w, http.StatusOK, op)
	}
}

func inventoryHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		devices, err := Inventory(r.Context(), db, InventoryFilter{
			DeviceClass: r.URL.Query().Get("device_class"),
			State:       r.URL.Query().Get("state"),
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
	}
}

func abnormalHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		devices, err := Abnormal(r.Context(), db, AbnormalFilter{DeviceClass: r.URL.Query().Get("device_class")})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
	}
}

func countsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		since, err := parseTime(r.URL.Query().Get("since"))
		if err != nil {
			writeError(w, err)
			return
		}
		rows, err := Counts(r.Context(), db, CountsFilter{OpType: r.URL.Query().Get("op_type"), Since: since})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"counts": rows})
	}
}

// recordRequest is POST /operations' body. occurred_at is RFC3339; omitted it
// defaults to now (a quick "it just operated" crew log). source omitted defaults
// to 'crew' (this endpoint is the manual/API path).
type recordRequest struct {
	DeviceClass     string     `json:"device_class"`
	DeviceID        string     `json:"device_id"`
	OpType          string     `json:"op_type"`
	Source          string     `json:"source"`
	OccurredAt      *time.Time `json:"occurred_at"`
	Cause           *string    `json:"cause"`
	Notes           *string    `json:"notes"`
	FaultID         *string    `json:"fault_id"`
	IncidentID      *string    `json:"incident_id"`
	SwitchingStepID *string    `json:"switching_step_id"`
	SourceSystem    *string    `json:"source_system"`
	SourceEventID   *string    `json:"source_event_id"`
}

func recordHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req recordRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		if req.Source == "" {
			req.Source = "crew"
		}
		occurred := time.Now()
		if req.OccurredAt != nil {
			occurred = *req.OccurredAt
		}
		op, err := Record(r.Context(), db, RecordInput{
			DeviceClass:     req.DeviceClass,
			DeviceID:        req.DeviceID,
			OpType:          req.OpType,
			Source:          req.Source,
			OccurredAt:      occurred,
			Cause:           req.Cause,
			Notes:           req.Notes,
			FaultID:         req.FaultID,
			IncidentID:      req.IncidentID,
			SwitchingStepID: req.SwitchingStepID,
			SourceSystem:    req.SourceSystem,
			SourceEventID:   req.SourceEventID,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, op)
	}
}
