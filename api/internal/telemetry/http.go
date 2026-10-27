package telemetry

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// Routes returns the live-state / telemetry endpoints:
//
//	GET  /telemetry                 the latest telemetry for every device (?feature_class=)       [telemetry:read]
//	POST /telemetry                 ingest a batch of samples ({"reports":[...]}), latest-value   [telemetry:ingest]
//	GET  /telemetry/reconciliation  field-vs-model disagreements + stale readings (?stale_seconds=) [telemetry:read]
//	GET  /telemetry/device/{class}/{id}       one device's latest telemetry (null when none)       [telemetry:read]
//	POST /telemetry/device/{class}/{id}/apply write the telemetry state onto the model device_state [oms:write]
//
// Unlike operations/oms/safety, these are NOT one method-aware mount gate: ingest
// is a MACHINE write (the SCADA gateway, telemetry:ingest) while apply is an
// OPERATOR write (sync the model to the field, gated like a device operation,
// oms:write) -- different writers, so each route carries its own permission. The
// mount in main.go is a plain Mount; the global SessionLoader still populates the
// context these gates read.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	// RequirePermission is the method-aware (read, write) gate; on a single-method
	// route we pass the same permission twice so it governs that one method.
	read := auth.RequirePermission(auth.PermTelemetryRead, auth.PermTelemetryRead)
	r.With(read).Get("/", listHandler(db))
	r.With(read).Get("/reconciliation", reconciliationHandler(db))
	r.With(read).Get("/device/{class}/{id}", getDeviceHandler(db))
	r.With(auth.RequirePermission(auth.PermTelemetryIngest, auth.PermTelemetryIngest)).Post("/", ingestHandler(db))
	r.With(auth.RequirePermission(auth.PermOMSWrite, auth.PermOMSWrite)).Post("/device/{class}/{id}/apply", applyHandler(db))
	return r
}

func applyHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, err := ApplyTelemetry(r.Context(), db, chi.URLParam(r, "class"), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func reconciliationHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stale := 0
		if s := r.URL.Query().Get("stale_seconds"); s != "" {
			if v, err := strconv.Atoi(s); err == nil && v > 0 {
				stale = v
			}
		}
		recon, err := ReconcileTelemetry(r.Context(), db, stale)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, recon)
	}
}

func listHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := ListTelemetry(r.Context(), db, r.URL.Query().Get("feature_class"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"device_telemetry": rows})
	}
}

func ingestHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Reports []Report `json:"reports"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		if len(body.Reports) == 0 {
			writeError(w, invalidf("reports is required (a non-empty array of telemetry samples)"))
			return
		}
		res, err := IngestBatch(r.Context(), db, body.Reports)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func getDeviceHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dt, err := GetDeviceTelemetry(r.Context(), db, chi.URLParam(r, "class"), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"telemetry": dt})
	}
}
