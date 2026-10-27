package fieldpack

// The field-app front door: POST /auth/field-login (see routes.go for where it
// mounts). It is, with /healthz, one of only two unauthenticated routes on the
// fieldsync surface -- and deliberately so: this is the exchange that PRODUCES
// the bearer token every other route requires. A worker types an email +
// password; the server verifies them under the same policy as the web login and
// returns a long-lived token the device keystores. Everything the token can then
// do is the unchanged Phase 18 bearer model (auth.FieldLogin / auth.ResolveAPIToken).

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// fieldLoginHandler exchanges an email + password for an iam.api_tokens bearer.
// A rejected credential is a generic 401 (auth.FieldLogin never distinguishes
// unknown-user from wrong-password); a valid credential blocked by policy (local
// auth off + not break-glass) is 403; a malformed body is 400. The raw token is
// returned once, in the clear, over the same TLS the rest of the surface uses.
func fieldLoginHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Email      string `json:"email"`
			Password   string `json:"password"`
			DeviceName string `json:"device_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid request body")
			return
		}

		// A per-device label so an admin listing a user's tokens can tell field-app
		// logins apart (and revoke a lost device's token specifically).
		name := "field-app"
		if d := strings.TrimSpace(req.DeviceName); d != "" {
			name = "field-app · " + d
		}

		// ttl 0 = non-expiring: a field device must keep working offline for weeks,
		// governed by revocation, not a clock.
		res, err := auth.FieldLogin(r.Context(), db, r, req.Email, req.Password, name, 0)
		if err != nil {
			var le *auth.FieldLoginError
			if errors.As(err, &le) {
				writeErr(w, le.Status, le.Message)
			} else {
				writeErr(w, http.StatusInternalServerError, "login error")
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"token":        res.Token,
			"email":        res.Email,
			"display_name": res.DisplayName,
			// The user's effective permissions, so the device can show/hide modules.
			"permissions": res.Permissions,
		})
	}
}
