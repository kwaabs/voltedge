package assembly

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

// Routes returns the general kit-assembly endpoint:
//
//	GET  /options  the addable (anchor class x config) list driving the map's
//	               Assemblies menu
//	POST /         place an anchor and fan out its config's kit, wiring every
//	               structural relationship AND electrical connection in one tx
//
// The POST route creates features, writes relationships AND commits connections, so
// it needs THREE capabilities -- the same layered gate rmu/packagedsub use.
// api/cmd/server/main.go mounts this under the method-aware features gate
// (features:read/features:write), which covers the feature writes AND the GET; the
// inner gates on POST add relationships:commit and connectivity:commit so a caller
// who may write features but not the kit's links can't half-build an unwired unit.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/options", listAssemblyOptionsHandler(db))
	r.With(
		auth.RequirePermission(auth.PermRelationshipsCommit, auth.PermRelationshipsCommit),
		auth.RequirePermission(auth.PermConnectivityCommit, auth.PermConnectivityCommit),
	).Post("/", createAssemblyHandler(db))
	return r
}

// listAssemblyOptionsHandler serves the addable list. It rides the mount's
// features:read gate (a GET), the same way rmu/packagedsub options do.
func listAssemblyOptionsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		options, err := ListAssemblyOptions(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		if options == nil {
			options = []AssemblyOption{}
		}
		writeJSON(w, http.StatusOK, options)
	}
}

// createAssemblyRequest is the POST body. anchor_feature_class is the class to
// place; config names the kit recipe within that class; geometry is where on the
// map it sits; subtype/name/territory_id/attributes are the anchor's own, exactly
// as a plain create takes them (a template may override subtype).
type createAssemblyRequest struct {
	AnchorFeatureClass string          `json:"anchor_feature_class"`
	Config             string          `json:"config"`
	Subtype            *int            `json:"subtype,omitempty"`
	Name               *string         `json:"name,omitempty"`
	Geometry           json.RawMessage `json:"geometry,omitempty"`
	TerritoryID        *string         `json:"territory_id,omitempty"`
	Attributes         map[string]any  `json:"attributes"`
}

func createAssemblyHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createAssemblyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		in := CreateAssemblyInput{
			AnchorFeatureClass: req.AnchorFeatureClass,
			Config:             req.Config,
			Subtype:            req.Subtype,
			Name:               req.Name,
			Geometry:           req.Geometry,
			TerritoryID:        req.TerritoryID,
			Attributes:         req.Attributes,
		}
		result, err := CreateAssembly(r.Context(), db, in)
		if err != nil {
			writeError(w, err)
			return
		}
		// Auto-build the kit's single-line diagram (best-effort): the assembly is
		// already committed, so a diagram failure is logged, never fatal.
		if _, derr := buildAutoDiagram(r.Context(), db, in, result); derr != nil {
			log.Printf("assembly: auto single-line diagram: %v", derr)
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

// writeError maps a client-fixable problem to 400 and everything else to 500 -- the
// same split rmu/packagedsub/crud draw. This package's InvalidError (missing
// config, no template, missing geometry) plus validation errors bubbling up from
// crud.CreateFeature (a bad attribute, a template component that fails create
// validation) and relationship.EstablishRelationship (no matching relationship
// class, cardinality exhausted) are the caller's to fix, so all are 400. crud
// not-found maps to 404. A connectivity wiring failure (no active network rule
// authorizing an internal leg) is NOT a client error -- it means the seed catalog
// is incomplete -- so it falls through to 500, the honest signal.
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
