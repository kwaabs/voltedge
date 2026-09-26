package packagedsub

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/relationship"
)

// Routes returns the composite packaged-substation endpoint:
//
//	GET  /options  the addable (class x config x tenure subtype) list driving the
//	               map's Assemblies menu
//	POST /         place a packaged substation and fan out its config's internal
//	               kit (MV switch + transformer + LV board), wiring all
//	               containment AND the internal chain connectivity in one tx
//
// The POST route creates features, writes containment relationships AND commits
// connections, so it needs THREE capabilities -- the same layered gate the RMU
// endpoint uses. api/cmd/server/main.go mounts this under the method-aware
// features gate (features:read/features:write), which covers the feature writes
// AND the GET; the inner gates on POST add relationships:commit and
// connectivity:commit so a caller who may write features but not the assembly's
// links can't half-build an unwired unit.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/options", listPackagedSubOptionsHandler(db))
	r.With(
		auth.RequirePermission(auth.PermRelationshipsCommit, auth.PermRelationshipsCommit),
		auth.RequirePermission(auth.PermConnectivityCommit, auth.PermConnectivityCommit),
	).Post("/", createPackagedSubHandler(db))
	return r
}

// listPackagedSubOptionsHandler serves the addable list. It rides the mount's
// features:read gate (a GET), the same way RMU options do -- reading the catalog
// needs no write capability.
func listPackagedSubOptionsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		options, err := ListPackagedSubOptions(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		if options == nil {
			options = []PackagedSubOption{}
		}
		writeJSON(w, http.StatusOK, options)
	}
}

// createPackagedSubRequest is the POST body. packaged_feature_class is the class
// to create; geometry is where on the map it sits; subtype (permanent vs mobile)/
// name/territory_id/attributes are the unit's own, exactly as a plain create
// takes them. The config (attributes.package_config) selects which chain template
// fans out.
type createPackagedSubRequest struct {
	PackagedFeatureClass string          `json:"packaged_feature_class"`
	Subtype              *int            `json:"subtype,omitempty"`
	Name                 *string         `json:"name,omitempty"`
	Geometry             json.RawMessage `json:"geometry,omitempty"`
	TerritoryID          *string         `json:"territory_id,omitempty"`
	Attributes           map[string]any  `json:"attributes"`
}

func createPackagedSubHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createPackagedSubRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		in := CreatePackagedSubInput{
			PackagedFeatureClass: req.PackagedFeatureClass,
			Subtype:              req.Subtype,
			Name:                 req.Name,
			Geometry:             req.Geometry,
			TerritoryID:          req.TerritoryID,
			Attributes:           req.Attributes,
		}
		result, err := CreatePackagedSubstation(r.Context(), db, in)
		if err != nil {
			writeError(w, err)
			return
		}
		// Auto-build the single-line (best-effort): the unit is already committed,
		// so a diagram failure is logged, never fatal.
		if _, derr := buildAutoDiagram(r.Context(), db, in, result); derr != nil {
			log.Printf("packagedsub: auto single-line diagram: %v", derr)
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
// the same split RMU and crud draw. This package's InvalidError (missing config,
// no template, missing geometry) plus validation errors bubbling up from
// crud.CreateFeature (a bad attribute, a template component that fails create
// validation) and relationship.EstablishRelationship (no matching containment
// class, cardinality exhausted) are the caller's to fix, so all are 400. crud
// not-found maps to 404. A connectivity wiring failure (no active network rule
// authorizing an internal leg) is NOT a client error -- it means the Slice 1
// catalog seed is incomplete -- so it falls through to 500, the honest signal.
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
