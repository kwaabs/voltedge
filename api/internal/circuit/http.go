package circuit

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
)

// Routes returns the circuit catalog endpoints:
//
//	GET    /       the catalog (optional ?network=MV|LV filter)
//	POST   /       create a circuit
//	PATCH  /{id}   rename / edit voltage / edit-or-clear the source reference
//	DELETE /{id}   delete a circuit
//	GET    /{id}/assign/preview  dry-run: the add/remove churn an assign would apply
//	POST   /{id}/assign  trace from the source and bulk-stamp membership
//	POST   /assign-stale  re-sync every stale circuit in one call
//
// Catalog reads and writes share one mount but split by capability, exactly
// like the territory routes: the GET is gated by features:read (every map
// viewer reads the catalog to filter and to resolve a circuit's name for a
// label), while the mutating verbs require circuits:write -- an administrative
// capability (migration 0063). api/cmd/server/main.go wires that method-aware
// gate (RequirePermission(features:read, circuits:write)) on the mount.
//
// The assign route is the exception, and the only reason this package touches
// auth at all: it bulk-writes feature rows (not catalog rows), so it carries an
// extra features:write requirement on top of the mount's circuits:write. The
// net effect is that assigning a circuit needs BOTH capabilities -- you are
// curating a circuit and mutating features -- which admin / break-glass-admin
// both hold.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/", listCircuitsHandler(db))
	r.Post("/", createCircuitHandler(db))
	r.Patch("/{id}", updateCircuitHandler(db))
	r.Delete("/{id}", deleteCircuitHandler(db))
	// Preview is a READ (it traces + diffs, writes nothing), so it rides the
	// mount's features:read GET gate -- an operator can see what an assign would
	// churn before anyone commits it.
	r.Get("/{id}/assign/preview", previewCircuitHandler(db))
	// The writing verbs carry an extra features:write on top of the mount's
	// circuits:write (they bulk-write feature rows): per-circuit assign, and the
	// bulk "assign every stale circuit".
	r.With(auth.RequirePermission(auth.PermFeaturesWrite, auth.PermFeaturesWrite)).Group(func(r chi.Router) {
		r.Post("/{id}/assign", assignCircuitHandler(db))
		r.Post("/assign-stale", assignStaleHandler(db))
	})
	return r
}

func listCircuitsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		circuits, err := ListCircuits(r.Context(), db, r.URL.Query().Get("network"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"circuits": circuits})
	}
}

// createCircuitRequest is the POST body. voltage_kv and the source reference
// are optional.
type createCircuitRequest struct {
	Name               string   `json:"name"`
	Network            string   `json:"network"`
	VoltageKV          *float64 `json:"voltage_kv"`
	SourceFeatureClass *string  `json:"source_feature_class"`
	SourceFeatureID    *string  `json:"source_feature_id"`
}

func createCircuitHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createCircuitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		c, err := CreateCircuit(r.Context(), db, CreateParams{
			Name:               req.Name,
			Network:            req.Network,
			VoltageKV:          req.VoltageKV,
			SourceFeatureClass: req.SourceFeatureClass,
			SourceFeatureID:    req.SourceFeatureID,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, c)
	}
}

// updateCircuitRequest is the PATCH body. Every field is optional; only what's
// present is changed. clear_voltage / clear_source remove a value (so
// "unknown" is distinguishable from "leave it alone"); network is absent
// because it is immutable (see UpdateParams).
type updateCircuitRequest struct {
	Name               *string  `json:"name"`
	VoltageKV          *float64 `json:"voltage_kv"`
	ClearVoltage       bool     `json:"clear_voltage"`
	SourceFeatureClass *string  `json:"source_feature_class"`
	SourceFeatureID    *string  `json:"source_feature_id"`
	ClearSource        bool     `json:"clear_source"`
}

func updateCircuitHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req updateCircuitRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		c, err := UpdateCircuit(r.Context(), db, id, UpdateParams{
			Name:               req.Name,
			VoltageKV:          req.VoltageKV,
			ClearVoltage:       req.ClearVoltage,
			SourceFeatureClass: req.SourceFeatureClass,
			SourceFeatureID:    req.SourceFeatureID,
			ClearSource:        req.ClearSource,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, c)
	}
}

func deleteCircuitHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := DeleteCircuit(r.Context(), db, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// assignCircuitHandler traces from the circuit's source and full-syncs
// membership (see AssignCircuit). Returns the run's counts. A circuit with no
// source, or an unconfigured network, comes back as an InvalidError -> 400.
func assignCircuitHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		res, err := AssignCircuit(r.Context(), db, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// previewCircuitHandler is the DRY-RUN of assign (option B): it traces from the
// source and returns the add/remove/unchanged churn an assign would apply, writing
// nothing (see PreviewAssignCircuit). Read-only, so it rides features:read.
func previewCircuitHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		res, err := PreviewAssignCircuit(r.Context(), db, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// assignStaleHandler re-syncs every stale circuit in one call (option D), isolating
// per-circuit failures (see AssignAllStale). Returns the aggregate counts plus any
// circuits it couldn't assign.
func assignStaleHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, err := AssignAllStale(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// writeJSON writes a JSON body with the given status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// writeError maps a client-fixable InvalidError to 400 and everything else to
// 500 -- the same split territory and crud draw.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if IsInvalid(err) {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
