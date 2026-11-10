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

		// ttl is ignored now (the field session model fixes the access-token TTL and
		// pairs it with a refresh token); passed as 0 for signature compatibility.
		res, err := auth.FieldLogin(r.Context(), db, r, req.Email, req.Password, name, 0)
		if err != nil {
			writeFieldAuthErr(w, err)
			return
		}
		writeFieldSession(w, r, db, res)
	}
}
