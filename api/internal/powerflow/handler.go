package powerflow

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/studies"
	"github.com/uptrace/bun"
)

// enforceStudy wraps an analysis handler so a study that is not available for the
// start feature's class -- either inapplicable by role or admin-disabled
// (api/internal/studies) -- is refused BEFORE the engine runs. It buffers the
// request body (the handler still reads it fully), peeks at the start feature's
// class (top-level feature_class or a nested "start"), and checks studies.Allowed.
// A request whose class can't be determined or loaded falls through -- the engine
// then produces its own clear error -- so this never blocks on a lookup miss.
func enforceStudy(db *bun.DB, studyID string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			writeError(w, invalid("reading request body: %v", err))
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))

		var peek struct {
			FeatureClass string      `json:"feature_class"`
			Start        *featureRef `json:"start"`
		}
		_ = json.Unmarshal(body, &peek)
		class := peek.FeatureClass
		if class == "" && peek.Start != nil {
			class = peek.Start.FeatureClass
		}
		if class != "" {
			fc := new(metadata.FeatureClass)
			if err := db.NewSelect().Model(fc).Where("code = ?", class).Scan(r.Context()); err == nil {
				allowed, err := studies.Allowed(r.Context(), db, fc, studyID)
				if err != nil {
					writeError(w, err)
					return
				}
				if !allowed {
					writeError(w, invalid("the %q analysis is not available for %s features", studyID, class))
					return
				}
			}
		}
		next(w, r)
	}
}

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
//	POST   /nodal-load-flow full nodal (Y-bus) load flow (Phase 52, Tier 2): a
//	                        distinct solver that assembles a per-unit admittance
//	                        matrix over the GENERAL graph and solves it by
//	                        Gauss-Seidel -- so a densely meshed LV network is
//	                        solved directly, not refused. Single slack, PQ buses
//	                        (loads + embedded generation as negative-demand PQ);
//	                        multi-slack / PV buses and Newton-Raphson are later
//	                        slices
//	POST   /load-flow-auto  dispatch ROUTER (Phase 52, S4): classify the network's
//	                        topology once (buildTree's radial/mesh verdict) and
//	                        auto-select the engine -- radial tree -> the fast
//	                        ladder, a few loops -> Tier 1 mesh compensation, a
//	                        dense mesh or a multi-slack/PV request -> Tier 2 nodal
//	                        Y-bus -- so one entry solves any shape and reports
//	                        which engine ran (an "engine" override forces one)
//	POST   /arc-flash       IEEE 1584-2002 arc-flash hazard study (Phase 53): a
//	                        DERIVED analysis reusing the fault engine (bolted
//	                        fault current + voltage) and the coordination engine's
//	                        device evaluators (clearing time at the ARCING current)
//	                        to report per-node incident energy (cal/cm2), arc-flash
//	                        boundary, and NFPA 70E PPE category, on IEEE typical
//	                        geometry until per-feature fields exist (S2)
//	POST   /fault-location  distance-to-fault: the INVERSE of /fault-current -- a
//	                        measured fault current pins a target impedance, and each
//	                        conductor span whose source-to-span impedance brackets it
//	                        is a candidate location (distance from source + an
//	                        interpolated point). Three-phase, faulted voltage level.
//	POST   /ev-hosting      EV feeder-headroom study (Phase 54): the under-voltage
//	                        mirror of /hosting-capacity -- for a node (or every
//	                        connection node on a feeder) binary-search the largest
//	                        additional charging load it can absorb before the first
//	                        under-voltage (0.95 pu floor) or thermal overload binds
//	POST   /unbalanced-load-flow three-phase (unbalanced) load flow (Phase 55):
//	                        the per-phase twin of /load-flow -- reports per-node
//	                        Va/Vb/Vc, the NEMA voltage unbalance between them, and the
//	                        neutral return current, so a single-phase-heavy LV feeder's
//	                        imbalance is visible instead of averaged away. Default
//	                        (Tier 1) solves phases A/B/C as three DECOUPLED ladders;
//	                        `coupled:true` (Tier 2) runs a COMPLEX sweep over each
//	                        conductor's full 3x3 Carson phase-impedance matrix
//	                        (metadata.MaterialSpec line geometry, migrations/0117), so
//	                        the phase-to-phase mutual coupling a real line has is
//	                        modelled, not omitted
//
// Read-only, same as api/internal/trace -- neither route ever writes
// anything, they only compute over what api/internal/connectivity and
// the materials catalog have already committed.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	// Each route is wrapped so a study that isn't available for the start
	// feature's class is refused before the engine runs (api/internal/studies).
	// /arc-flash has no menu study of its own (it's derived from the fault engine),
	// so it isn't gated here.
	r.Post("/load-flow", enforceStudy(db, "load_flow", loadFlowHandler(db)))
	r.Post("/fault-current", enforceStudy(db, "fault_current", faultCurrentHandler(db)))
	r.Post("/ampacity", enforceStudy(db, "ampacity", ampacityHandler(db)))
	r.Post("/coordination", enforceStudy(db, "coordination", coordinationHandler(db)))
	r.Post("/voltage-profile", enforceStudy(db, "voltage_profile", voltageProfileHandler(db)))
	r.Post("/contingency", enforceStudy(db, "contingency", contingencyHandler(db)))
	r.Post("/hosting-capacity", enforceStudy(db, "hosting", hostingHandler(db)))
	r.Post("/meshed-load-flow", enforceStudy(db, "meshed_load_flow", meshedLoadFlowHandler(db)))
	r.Post("/nodal-load-flow", enforceStudy(db, "nodal_load_flow", nodalLoadFlowHandler(db)))
	r.Post("/load-flow-auto", enforceStudy(db, "load_flow_auto", dispatchLoadFlowHandler(db)))
	r.Post("/arc-flash", arcFlashHandler(db))
	// /fault-location, like /arc-flash, is a derived operational tool (the inverse
	// of the fault-current study), not a menu study of its own -- so it skips the
	// per-class studies gate and relies on the /analysis mount's permission.
	r.Post("/fault-location", faultLocationHandler(db))
	r.Post("/ev-hosting", enforceStudy(db, "ev_hosting", evHostingHandler(db)))
	r.Post("/unbalanced-load-flow", enforceStudy(db, "unbalanced_load_flow", unbalancedLoadFlowHandler(db)))
	r.Post("/volt-var", enforceStudy(db, "volt_var", voltVarHandler(db)))
	r.Post("/tap-optimization", enforceStudy(db, "tap_optimization", tapOptimizationHandler(db)))
	r.Post("/storage-dispatch", enforceStudy(db, "storage_dispatch", storageDispatchHandler(db)))
	r.Post("/vvo", enforceStudy(db, "vvo", vvoHandler(db)))
	r.Post("/load-growth", enforceStudy(db, "load_growth", loadGrowthHandler(db)))
	return r
}

// loadGrowthHandler is POST /analysis/load-growth: the load-growth forecast study
// (forecast.go). A scenario projection of the radial load-flow across a horizon under
// a growth assumption -- labelled Derived, not a statistical prediction.
func loadGrowthHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req forecastRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runLoadGrowthForecast(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
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

func arcFlashHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req arcFlashRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runArcFlash(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func faultLocationHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req faultLocationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runFaultLocation(r.Context(), db, req)
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

func voltVarHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req voltVarRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runVoltVar(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func tapOptimizationHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req tapOptRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runTapOptimization(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func storageDispatchHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req storageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runStorageDispatch(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func vvoHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req vvoRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runVVO(r.Context(), db, req)
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

func nodalLoadFlowHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req nodalLoadFlowRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runNodalLoadFlow(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func dispatchLoadFlowHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dispatchLoadFlowRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runDispatchLoadFlow(crud.WithAsDesigned(r.Context(), req.AsDesigned), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func unbalancedLoadFlowHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// start + nominal_voltage_v drive the per-phase split (from the model's own
		// conductor `phase` fields); the optional `coupled` flag switches on the
		// Tier 2 Carson-coupled solve, with optional frequency_hz / earth_
		// resistivity_ohm_m tuning its earth-return terms.
		var req unbalancedRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}

		result, err := runUnbalancedLoadFlow(r.Context(), db, req)
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
