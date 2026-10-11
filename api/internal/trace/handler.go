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
//	POST   /isolate        de-energized set + upstream protective
//	                       boundary for a switching device, composed
//	                       from two ordinary downstream/upstream traces
//	POST   /feeder         the whole feeder a feature belongs to,
//	                       regardless of which point along it you start
//	                       from -- find the network root(s) upstream,
//	                       then downstream from there
//	POST   /section        the section a feature sits in -- everything
//	                       between the nearest bounding device upstream
//	                       and downstream of it
//	POST   /supply-path    the upstream supply chain feeding a
//	                       consumption point -- LV switching devices, the
//	                       serving distribution transformer, then on to
//	                       the MV devices, power transformer, and source
//	POST   /service        the consumption below a feature -- a downstream
//	                       walk rolled up by load, customers, and
//	                       restoration priority, with the per-SERVICE_POINT
//	                       breakdown behind it
//
// Read-only -- unlike api/internal/connectivity and
// api/internal/relationship, there's nothing here to establish or
// revoke; a trace never writes anything, it only walks what those two
// packages have already committed.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Post("/", runHandler(db))
	r.Get("/configurations", listConfigurationsHandler(db))
	r.Post("/isolate", isolateHandler(db))
	r.Post("/feeder", feederHandler(db))
	r.Post("/section", sectionHandler(db))
	r.Post("/supply-path", supplyPathHandler(db))
	r.Post("/service", serviceHandler(db))
	r.Post("/energization", energizationHandler(db))
	return r
}

// energizationHandler classifies a set of features live vs de-energised: it
// returns the subset of the posted features currently cut off from supply (below
// an open device). The client greys those on the schematic overlay / one-line.
func energizationHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req energizationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		refs := make([]FeatureRef, 0, len(req.FeatureRefs))
		for _, f := range req.FeatureRefs {
			if f.FeatureClass == "" || f.FeatureID == "" {
				continue
			}
			refs = append(refs, FeatureRef{FeatureClass: f.FeatureClass, FeatureID: f.FeatureID})
		}

		dead, err := NetworkDeEnergized(r.Context(), db, refs)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, energizationResponse{DeEnergized: dead})
	}
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

func isolateHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req isolationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := isolate(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func feederHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req feederRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := feederTrace(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func sectionHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req sectionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := sectionTrace(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func supplyPathHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req supplyPathRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := supplyPathTrace(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func serviceHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req serviceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := serviceTrace(r.Context(), db, req)
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
