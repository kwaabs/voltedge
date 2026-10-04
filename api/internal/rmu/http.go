package rmu

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/logx"
	"github.com/kwaabs/voltedge/api/internal/relationship"
)

// Routes returns the composite RMU endpoint:
//
//	GET  /options  the addable (RMU class × config) list driving the map's
//	               "Add RMU" menu (Phase RMU Slice 3)
//	POST /         place a Ring Main Unit and fan out its config's ways +
//	               internal bus, wiring all containment AND the internal
//	               connectivity in one transaction (Phase RMU Slice 2)
//
// The POST route creates features, writes containment relationships AND commits
// connections, so it needs THREE capabilities. api/cmd/server/main.go mounts
// this under the method-aware features gate (features:read/features:write),
// which covers the feature writes AND the GET (a read); the inner gates on POST
// add relationships:commit and connectivity:commit so a caller who may write
// features but not the assembly's links can't half-build an unwired unit -- the
// same layered-gate shape circuit's assign and panel's create routes use, one
// more capability deep because the RMU is the first fan-out that wires topology.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/options", listRMUOptionsHandler(db))
	r.With(
		auth.RequirePermission(auth.PermRelationshipsCommit, auth.PermRelationshipsCommit),
		auth.RequirePermission(auth.PermConnectivityCommit, auth.PermConnectivityCommit),
	).Post("/", createRMUHandler(db))
	return r
}

// listRMUOptionsHandler serves the addable-RMU list. It rides the mount's
// features:read gate (a GET), the same way panel options do -- reading the
// catalog needs no write capability.
func listRMUOptionsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		options, err := ListRMUOptions(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		if options == nil {
			options = []RMUOption{}
		}
		writeJSON(w, http.StatusOK, options)
	}
}

// createRMURequest is the POST body. rmu_feature_class is the RMU class to
// create; geometry is where on the map it sits; subtype/name/territory_id/
// attributes are the RMU's own, exactly as a plain create takes them. The RMU's
// config (attributes.rmu_config) selects which way template fans out.
type createRMURequest struct {
	RMUFeatureClass string          `json:"rmu_feature_class"`
	Subtype         *int            `json:"subtype,omitempty"`
	Name            *string         `json:"name,omitempty"`
	Geometry        json.RawMessage `json:"geometry,omitempty"`
	TerritoryID     *string         `json:"territory_id,omitempty"`
	Attributes      map[string]any  `json:"attributes"`
}

func createRMUHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createRMURequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		in := CreateRMUInput{
			RMUFeatureClass: req.RMUFeatureClass,
			Subtype:         req.Subtype,
			Name:            req.Name,
			Geometry:        req.Geometry,
			TerritoryID:     req.TerritoryID,
			Attributes:      req.Attributes,
		}
		result, err := CreateRMU(r.Context(), db, in)
		if err != nil {
			writeError(w, err)
			return
		}
		// Auto-build the RMU's single-line diagram (best-effort): the assembly is
		// already committed, so a diagram failure is logged, never fatal.
		if _, derr := buildAutoDiagram(r.Context(), db, in, result); derr != nil {
			logx.From(r.Context()).Warn("auto single-line diagram failed", "component", "rmu", "err", derr)
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

// writeJSON writes a JSON body with the given status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// writeError maps a client-fixable problem to 400 and everything else to 500 --
// the same split panel and crud draw. The composite create surfaces this
// package's own InvalidError (missing config, no template, missing geometry)
// plus validation errors bubbling up from crud.CreateFeature (a bad RMU
// attribute, a template way that fails create validation) and
// relationship.EstablishRelationship (no matching containment class, cardinality
// exhausted); all are the caller's to fix, so all are 400. crud not-found maps
// to 404. A connectivity wiring failure (no active network rule authorizing
// way.T2 -> bus) is NOT a client error -- it means the Slice 1 catalog seed is
// incomplete -- so it falls through to 500, which is the honest signal.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case IsInvalid(err), crud.IsValidationError(err), relationship.IsValidationError(err):
		status = http.StatusBadRequest
	case crud.IsNotFoundError(err):
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
