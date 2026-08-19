package powerflow

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns a router for the load-flow engine, meant to be
// mounted at /analysis:
//
//	POST   /load-flow   run a radial forward-backward sweep from one
//	                    feature, returning per-node voltage and
//	                    per-branch loading
//
// Read-only, same as api/internal/trace -- a load flow never writes
// anything, it only computes over what api/internal/connectivity and
// the materials catalog have already committed.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Post("/load-flow", loadFlowHandler(db))
	return r
}

func loadFlowHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loadFlowRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runLoadFlow(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
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
