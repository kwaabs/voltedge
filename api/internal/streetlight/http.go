package streetlight

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

// Routes exposes the attach-a-lamp-to-a-pole action:
//
//	POST /   create a geometry-less pole-mounted street light, link it to the
//	         pole (POLE supports STREET_LIGHT), and best-effort auto-wire it to
//	         the one LV conductor the pole carries nearby.
//
// The create + the support link always run (features:write via the mount +
// relationships:commit here). The auto-wire is gated separately on
// connectivity:commit, checked in the handler: EstablishConnection has no RBAC
// gate of its own, and a caller who may attach but not commit connections should
// still be able to attach (the lamp is just left unwired).
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.With(auth.RequirePermission(auth.PermRelationshipsCommit, auth.PermRelationshipsCommit)).
		Post("/", attachHandler(db))
	return r
}

type attachRequest struct {
	PoleFeatureClass string         `json:"pole_feature_class"`
	PoleFeatureID    string         `json:"pole_feature_id"`
	Subtype          *int           `json:"subtype,omitempty"`
	Attributes       map[string]any `json:"attributes"`
	RadiusMeters     float64        `json:"radius_meters,omitempty"`
}

func attachHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req attachRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		// Auto-wire only when the caller may commit connections; otherwise the lamp
		// is attached and left for manual wiring (never silently skipped elsewhere).
		canWire := false
		if u, ok := auth.UserFrom(r.Context()); ok {
			canWire = u.Has(auth.PermConnectivityCommit)
		}
		result, err := AttachStreetLight(r.Context(), db, AttachInput{
			PoleFeatureClass: req.PoleFeatureClass,
			PoleFeatureID:    req.PoleFeatureID,
			SubtypeCode:      req.Subtype,
			Attributes:       req.Attributes,
			RadiusMeters:     req.RadiusMeters,
			CanWire:          canWire,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

// InvalidError is a client-fixable problem originating in this package (a missing
// pole ref). crud / relationship raise their own validation errors, mapped the
// same way in writeError.
type InvalidError struct{ msg string }

func (e *InvalidError) Error() string { return e.msg }

func invalidf(format string, args ...any) error { return &InvalidError{msg: fmt.Sprintf(format, args...)} }

// IsInvalid reports whether err is this package's InvalidError (errors.As, so a
// wrapped one still matches).
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
// everything else to 500 -- the same split the assembly engine draws. A failed
// auto-wire is NOT an error here (it's folded into the 201 result's wire_note),
// so only the create / relate failures reach this.
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
