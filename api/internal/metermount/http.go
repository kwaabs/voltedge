package metermount

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/relationship"
)

// Routes exposes the attach-a-meter-to-a-service-point action:
//
//	POST /   create a geometry-less meter, link it to the service point
//	         (SERVICE_POINT supports METER), and wire it to that SP
//	         (METER T2 -> SERVICE_POINT T1).
//
// The create + the support link always run (features:write via the mount +
// relationships:commit here). The wire is gated separately on connectivity:commit,
// checked in the handler: EstablishConnection has no RBAC gate of its own, and a
// caller who may attach but not commit connections should still be able to attach
// (the meter is just left unwired).
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.With(auth.RequirePermission(auth.PermRelationshipsCommit, auth.PermRelationshipsCommit)).
		Post("/", attachHandler(db))
	return r
}

type attachRequest struct {
	ServicePointFeatureClass string         `json:"service_point_feature_class"`
	ServicePointFeatureID    string         `json:"service_point_feature_id"`
	Subtype                  *int           `json:"subtype,omitempty"`
	Attributes               map[string]any `json:"attributes"`
}

func attachHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req attachRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		canWire := false
		if u, ok := auth.UserFrom(r.Context()); ok {
			canWire = u.Has(auth.PermConnectivityCommit)
		}
		result, err := AttachMeter(r.Context(), db, AttachInput{
			ServicePointFeatureClass: req.ServicePointFeatureClass,
			ServicePointFeatureID:    req.ServicePointFeatureID,
			SubtypeCode:              req.Subtype,
			Attributes:               req.Attributes,
			CanWire:                  canWire,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

// InvalidError is a client-fixable problem originating in this package (a missing
// service-point ref). crud / relationship raise their own validation errors,
// mapped the same way in writeError.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalidf(format string, args ...any) error { return &InvalidError{msg: fmt.Sprintf(format, args...)} }

// IsInvalid reports whether err is this package's InvalidError.
func IsInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError maps a client-fixable problem to 400, a missing feature to 404, and
// everything else to 500 -- the same split streetlight / the assembly engine draw.
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
