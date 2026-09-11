package duct

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/relationship"
)

// Routes returns the composite duct endpoint:
//
//	GET  /options  list the duct classes that fan out ways, for the add-feature
//	               menu (Phase 44 Slice 4)
//	POST /         create a cable duct and fan out the DUCT_WAY children its
//	               way_count calls for, wiring all containment in one transaction
//
// The POST route both creates features and writes containment relationships, so it
// needs BOTH capabilities. api/cmd/server/main.go mounts this under the method-aware
// features gate (RequirePermission(features:read, features:write)), which covers the
// feature writes (and features:read on GET); the inner gate on POST adds
// relationships:commit so a caller who may write features but not relationships can't
// half-build a duct with orphan ways -- the same layered-gate shape /panels uses.
// GET /options is a plain read that rides the mount's features:read.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/options", listDuctOptionsHandler(db))
	r.With(auth.RequirePermission(auth.PermRelationshipsCommit, auth.PermRelationshipsCommit)).
		Post("/", createDuctHandler(db))
	return r
}

// listDuctOptionsHandler answers "what duct classes fan out ways?" for the frontend's
// add-feature menu. Read-only.
func listDuctOptionsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		options, err := ListDuctOptions(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		// Never nil -- an empty list is a valid "no fan-out duct classes" answer.
		// Returned as a bare array (like /rmus/options), not wrapped.
		if options == nil {
			options = []DuctOption{}
		}
		writeJSON(w, http.StatusOK, options)
	}
}

// createDuctRequest is the POST body. duct_feature_class is the duct class to
// create; subtype/name/geometry/territory_id/attributes are the duct's own, exactly
// as a plain create takes them. attributes.way_count sets how many ways fan out.
type createDuctRequest struct {
	DuctFeatureClass string          `json:"duct_feature_class"`
	Subtype          *int            `json:"subtype,omitempty"`
	Name             *string         `json:"name,omitempty"`
	Geometry         json.RawMessage `json:"geometry,omitempty"`
	TerritoryID      *string         `json:"territory_id,omitempty"`
	Attributes       map[string]any  `json:"attributes"`
}

func createDuctHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createDuctRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		result, err := CreateDuct(r.Context(), db, CreateDuctInput{
			DuctFeatureClass: req.DuctFeatureClass,
			Subtype:          req.Subtype,
			Name:             req.Name,
			Geometry:         req.Geometry,
			TerritoryID:      req.TerritoryID,
			Attributes:       req.Attributes,
		})
		if err != nil {
			writeError(w, err)
			return
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
// same split panel/rmu draw. The composite create surfaces this package's own
// InvalidError (missing class, bad way_count) plus validation errors bubbling up
// from the shared crud.CreateFeature (a bad duct attribute, a way that fails create
// validation) and relationship.EstablishRelationship (no matching containment class,
// cardinality exhausted); all are the caller's to fix, so all are 400. The crud
// not-found case maps to 404.
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
