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
//	POST   /voltage-profile run the same radial sweep as load-flow and
//	                        report a voltage profile (per-node per-unit
//	                        voltage vs an ANSI C84.1 band) plus a series
//	                        I²R loss summary for the feeder
//	POST   /contingency     N-1 / backfeed restoration study: for each
//	                        normally-open tie on a feeder, simulate closing
//	                        it and report the outaged load the donor feeder
//	                        would pick up plus any post-transfer voltage
//	                        violation or thermal overload
//	POST   /hosting-capacity DER hosting-capacity study: for a node (or every
//	                        interconnection node on a feeder) binary-search the
//	                        largest generator injection it can accept before
//	                        the first over-voltage or thermal overload binds
//	POST   /meshed-load-flow weakly-meshed load flow: solve a radial spanning
//	                        tree, then close each loop (a ring main / closed
//	                        link box) by compensation, returning the same per-
//	                        node voltage/loading as load-flow plus the loops
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
	r.Post("/voltage-profile", voltageProfileHandler(db))
	r.Post("/contingency", contingencyHandler(db))
	r.Post("/hosting-capacity", hostingHandler(db))
	r.Post("/meshed-load-flow", meshedLoadFlowHandler(db))
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

func voltageProfileHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req voltageProfileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runVoltageProfile(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func contingencyHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req contingencyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runContingency(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func hostingHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req hostingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runHosting(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func meshedLoadFlowHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req meshedLoadFlowRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runMeshLoadFlow(r.Context(), db, req)
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
