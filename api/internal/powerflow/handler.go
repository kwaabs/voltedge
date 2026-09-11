package powerflow

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns a router for the power-flow analysis engine, meant to be
// mounted at /analysis:
//
//	POST   /load-flow       run a radial forward-backward sweep from one
//	                        feature, returning per-node voltage and
//	                        per-branch loading
//	POST   /fault-current   accumulate series impedance from one source
//	                        feature, returning the symmetrical bolted-
//	                        fault current at every downstream node; with
//	                        fault_type = "slg" it also builds the zero-
//	                        sequence network and returns the single-line-
//	                        to-ground fault current per node
//	POST   /ampacity        first-principles (IEEE 738) steady-state
//	                        thermal ampacity for one conductor feature
//	POST   /coordination    grade the time-overcurrent protective devices
//	                        on the source->fault path for a fault at one
//	                        node, checking each backup clears its primary
//	                        by at least the coordination time interval
//
// Read-only, same as api/internal/trace -- neither route ever writes
// anything, they only compute over what api/internal/connectivity and
// the materials catalog have already committed.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Post("/load-flow", loadFlowHandler(db))
	r.Post("/fault-current", faultCurrentHandler(db))
	r.Post("/ampacity", ampacityHandler(db))
	r.Post("/coordination", coordinationHandler(db))
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

func faultCurrentHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req faultCurrentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runFaultCurrent(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func ampacityHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ampacityRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runAmpacity(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func coordinationHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req coordinationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runCoordination(r.Context(), db, req)
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
