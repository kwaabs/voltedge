package drafts

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes exposes the private-drafts rollout switch:
//
//	GET /enforcement   report whether the private-drafts filter is on
//	PUT /enforcement   flip it (body: {"enabled": bool})
//
// The GET is a read (features:read via the method-aware mount, so any admin
// screen can show current state); the PUT is the rollout action and rides the
// same metadata:write gate the other system-settings toggles use (map-settings),
// wired in api/cmd/server/main.go so this package stays unaware of auth beyond
// resolving the session for the filter itself.
//
// The path mirrors territory's /enforcement so the two rollout gates read the
// same on the wire. Turning it ON retroactively privatizes every never-published
// feature to its author chain, so it is a deliberate admin step, never a deploy
// default (see SettingEnabled).
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/enforcement", getEnforcementHandler(db))
	r.Put("/enforcement", setEnforcementHandler(db))
	return r
}

// getEnforcementHandler reports whether the private-drafts filter is currently on.
func getEnforcementHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		enabled, err := Enabled(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"enforcement": enabled})
	}
}

// setEnforcementRequest is the PUT /enforcement body: the desired flag state.
type setEnforcementRequest struct {
	Enabled bool `json:"enabled"`
}

// setEnforcementHandler flips the private_drafts_enabled toggle -- the rollout
// step that turns draft privacy on (or back off) for the whole install.
func setEnforcementHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req setEnforcementRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body: " + err.Error()})
			return
		}
		if err := SetEnabled(r.Context(), db, req.Enabled); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"enforcement": req.Enabled})
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
}
