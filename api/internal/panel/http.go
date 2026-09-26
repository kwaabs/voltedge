package panel

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

// Routes returns the composite panel endpoint:
//
//	GET  /options  list the panels addable to an open diagram, by its
//	               container class + diagram type (Phase 42 Slice 3)
//	POST /         create a switchgear panel and fan out its role's standard
//	               device set, wiring all containment in one transaction
//	               (Phase 42 Slice 2)
//
// The POST route both creates features and writes containment relationships, so
// it needs BOTH capabilities. api/cmd/server/main.go mounts this under the
// method-aware features gate (RequirePermission(features:read, features:write)),
// which covers the feature writes (and features:read on GET); the inner gate on
// POST adds relationships:commit so a caller who may write features but not
// relationships can't half-build an unlinked assembly -- the same layered-gate
// shape circuit's assign route uses to require two capabilities at once. GET
// /options is a plain read that rides the mount's features:read.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/options", listPanelOptionsHandler(db))
	r.With(auth.RequirePermission(auth.PermRelationshipsCommit, auth.PermRelationshipsCommit)).
		Post("/", createPanelHandler(db))
	return r
}

// listPanelOptionsHandler answers "what panels can I add to this diagram?" for
// the frontend's add-panel menu: it takes the open diagram's container class and
// diagram type as query params and returns the addable (panel class, role)
// options, empty for any diagram that isn't a board schematic. Read-only.
func listPanelOptionsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		options, err := ListPanelOptions(
			r.Context(), db,
			r.URL.Query().Get("container_feature_class"),
			r.URL.Query().Get("diagram_type"),
		)
		if err != nil {
			writeError(w, err)
			return
		}
		// Never nil -- an empty list is a valid "nothing addable here" answer.
		if options == nil {
			options = []PanelOption{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"options": options})
	}
}

// createPanelRequest is the POST body. board_* names the container to place the
// panel in; panel_feature_class is the panel class to create; subtype/name/
// attributes are the panel's own, exactly as a plain create takes them. The
// panel's role (attributes.panel_role) selects which device template fans out.
type createPanelRequest struct {
	BoardFeatureClass string         `json:"board_feature_class"`
	BoardFeatureID    string         `json:"board_feature_id"`
	PanelFeatureClass string         `json:"panel_feature_class"`
	Subtype           *int           `json:"subtype,omitempty"`
	Name              *string        `json:"name,omitempty"`
	Attributes        map[string]any `json:"attributes"`
}

func createPanelHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createPanelRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		in := CreatePanelInput{
			BoardFeatureClass: req.BoardFeatureClass,
			BoardFeatureID:    req.BoardFeatureID,
			PanelFeatureClass: req.PanelFeatureClass,
			Subtype:           req.Subtype,
			Name:              req.Name,
			Attributes:        req.Attributes,
		}
		result, err := CreatePanel(r.Context(), db, in)
		if err != nil {
			writeError(w, err)
			return
		}
		// Auto-build the panel's single-line diagram (best-effort): the panel is
		// already committed, so a diagram failure is logged, never fatal.
		if _, derr := buildAutoDiagram(r.Context(), db, in, result); derr != nil {
			log.Printf("panel: auto single-line diagram: %v", derr)
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
// the same split circuit and crud draw. The composite create surfaces this
// package's own InvalidError (missing role, no template, missing board) plus
// validation errors bubbling up from the shared crud.CreateFeature (a bad panel
// attribute, a template item that fails create validation) and
// relationship.EstablishRelationship (no matching containment class, cardinality
// exhausted); all three are the caller's to fix, so all three are 400. The crud
// not-found case (an id that vanished) maps to 404.
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
