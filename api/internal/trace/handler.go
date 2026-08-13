package trace

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns a router for the trace engine, meant to be mounted at
// /trace:
//
//	POST   /               run a trace, per a named
//	                       metadata.trace_configurations profile,
//	                       starting from one feature
//	GET    /configurations list every trace_configurations row --
//	                       lets a client discover what's available
//	                       instead of a fixed set of hardcoded codes
//
// Read-only -- unlike api/internal/connectivity and
// api/internal/relationship, there's nothing here to establish or
// revoke; a trace never writes anything, it only walks what those two
// packages have already committed.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Post("/", runHandler(db))
	r.Get("/configurations", listConfigurationsHandler(db))
	return r
}

func runHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req traceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runTrace(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func listConfigurationsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := listConfigurations(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"trace_configurations": result})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case isValidationError(err):
		status = http.StatusBadRequest
	case isNotFoundError(err):
		status = http.StatusNotFound
	}
	writeJSON(w, status, apiError{Error: err.Error()})
}
