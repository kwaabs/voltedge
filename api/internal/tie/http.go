package tie

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/crud"
)

// Routes returns the composite feeder-to-feeder tie endpoint:
//
//	GET  /options  the addable switch-as-tie classes (+ the feeder classes each
//	               may join) driving the map's "Create tie" menu
//	POST /         place a switch and wire it between two named feeders (rules
//	               1 + 3) in one transaction, born NORMALLY_OPEN + device OPEN
//
// The POST route creates a feature AND commits two connections, so it needs both
// features:write (from the mount) and connectivity:commit. api/cmd/server/main.go
// mounts this under the method-aware features gate (features:read/features:write),
// which covers the create AND the GET; the inner gate on POST adds
// connectivity:commit so a caller who may write features but not commit topology
// can't place an unwired tie -- the same layered-gate shape coupler's create
// route uses.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/options", listTieOptionsHandler(db))
	r.With(
		auth.RequirePermission(auth.PermConnectivityCommit, auth.PermConnectivityCommit),
	).Post("/", createTieHandler(db))
	return r
}

// listTieOptionsHandler serves the addable-tie list. It rides the mount's
// features:read gate (a GET) -- reading the catalog needs no write capability.
func listTieOptionsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		options, err := ListTieOptions(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		if options == nil {
			options = []TieOption{}
		}
		writeJSON(w, http.StatusOK, options)
	}
}

// createTieRequest is the POST body. switch_feature_class is the switch class to
// create (defaults to MV_SWITCH server-side if blank); geometry is where on the
// map it sits; subtype/name/territory_id/attributes are the switch's own,
// exactly as a plain create takes them (attributes.normal_state defaults to
// NORMALLY_OPEN and attributes.device_state to OPEN server-side if unset).
// feeder_a / feeder_b are the two conductors to join.
type createTieRequest struct {
	SwitchFeatureClass string          `json:"switch_feature_class"`
	Subtype            *int            `json:"subtype,omitempty"`
	Name               *string         `json:"name,omitempty"`
	Geometry           json.RawMessage `json:"geometry,omitempty"`
	TerritoryID        *string         `json:"territory_id,omitempty"`
	Attributes         map[string]any  `json:"attributes"`
	FeederA            FeederRef       `json:"feeder_a"`
	FeederB            FeederRef       `json:"feeder_b"`
}

func createTieHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createTieRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		result, err := CreateTie(r.Context(), db, CreateTieInput{
			SwitchFeatureClass: req.SwitchFeatureClass,
			Subtype:            req.Subtype,
			Name:               req.Name,
			Geometry:           req.Geometry,
			TerritoryID:        req.TerritoryID,
			Attributes:         req.Attributes,
			FeederA:            req.FeederA,
			FeederB:            req.FeederB,
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
// Like a coupler -- whose two buses are USER-PICKED -- a tie's two feeders are
// user-picked, so a connectivity rejection (no active network rule authorizing
// conductor -> switch.T1, a bad terminal, a phase mismatch) is the caller's to
// fix and maps to 400, and a missing feeder feature to 404. This package's own
// InvalidError (missing geometry, blank/duplicate feeder refs) plus crud
// validation errors (a bad switch attribute) are 400; crud / connectivity
// not-found are 404.
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
