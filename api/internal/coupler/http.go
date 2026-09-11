package coupler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/crud"
)

// Routes returns the composite bus-coupler endpoint:
//
//	GET  /options  the addable breaker-as-coupler classes (+ the bus classes
//	               each may bridge) driving the map's "Add bus coupler" menu
//	POST /         place a breaker and wire it between two named buses (rules
//	               C + D) in one transaction
//
// The POST route creates a feature AND commits two connections, so it needs both
// features:write (from the mount) and connectivity:commit. api/cmd/server/main.go
// mounts this under the method-aware features gate (features:read/features:write),
// which covers the create AND the GET; the inner gate on POST adds
// connectivity:commit so a caller who may write features but not commit topology
// can't place an unwired coupler -- the same layered-gate shape rmu's create
// route uses (one capability shallower: a coupler writes no containment
// relationship, only connectivity).
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/options", listCouplerOptionsHandler(db))
	r.With(
		auth.RequirePermission(auth.PermConnectivityCommit, auth.PermConnectivityCommit),
	).Post("/", createCouplerHandler(db))
	return r
}

// listCouplerOptionsHandler serves the addable-coupler list. It rides the mount's
// features:read gate (a GET) -- reading the catalog needs no write capability.
func listCouplerOptionsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		options, err := ListCouplerOptions(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		if options == nil {
			options = []CouplerOption{}
		}
		writeJSON(w, http.StatusOK, options)
	}
}

// createCouplerRequest is the POST body. breaker_feature_class is the breaker
// class to create; geometry is where on the map it sits; subtype/name/
// territory_id/attributes are the breaker's own, exactly as a plain create takes
// them (attributes.switch_function defaults to BUS_COUPLER server-side if
// unset). bus_a / bus_b are the two buses to bridge.
type createCouplerRequest struct {
	BreakerFeatureClass string          `json:"breaker_feature_class"`
	Subtype             *int            `json:"subtype,omitempty"`
	Name                *string         `json:"name,omitempty"`
	Geometry            json.RawMessage `json:"geometry,omitempty"`
	TerritoryID         *string         `json:"territory_id,omitempty"`
	Attributes          map[string]any  `json:"attributes"`
	BusA                BusRef          `json:"bus_a"`
	BusB                BusRef          `json:"bus_b"`
}

func createCouplerHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createCouplerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		result, err := CreateCoupler(r.Context(), db, CreateCouplerInput{
			BreakerFeatureClass: req.BreakerFeatureClass,
			Subtype:             req.Subtype,
			Name:                req.Name,
			Geometry:            req.Geometry,
			TerritoryID:         req.TerritoryID,
			Attributes:          req.Attributes,
			BusA:                req.BusA,
			BusB:                req.BusB,
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

// writeError maps a client-fixable problem to 400 and everything else to 500.
// Unlike rmu -- whose connectivity wiring is engine-internal, so a rule miss is
// a seed bug (500) -- a coupler's two buses are USER-PICKED, so a connectivity
// rejection (no active network rule authorizing bus.T2 -> breaker.T1, a bad
// terminal, a phase mismatch) is the caller's to fix and maps to 400, and a
// missing bus feature to 404. This package's own InvalidError (missing geometry,
// blank/duplicate bus refs) plus crud validation errors (a bad breaker attribute)
// are 400; crud / connectivity not-found are 404.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case IsInvalid(err), crud.IsValidationError(err), connectivity.IsValidationError(err):
		status = http.StatusBadRequest
	case crud.IsNotFoundError(err), connectivity.IsNotFoundError(err):
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
