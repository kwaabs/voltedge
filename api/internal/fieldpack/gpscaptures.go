package fieldpack

// The field tool's GPS-captures read (Phase gnss-quality Slice 3b,
// docs/phase-gnss-quality memory): the office already sees a placement's GNSS
// survey quality in the web "GPS" tab; this exposes the same rows to the mobile
// feature-detail screen. The capture rows are server-only -- they never ride the
// offline pack -- so the device fetches them on demand when it's online, which is
// why this lives on the cmd/fieldsync bearer surface rather than in the pack.
//
// It shares crud.ListFeatureGpsCaptures with the cmd/server route so the JSON is
// byte-for-byte the same shape the web hook already parses. Behind features:read
// like every other read on this surface (wired in routes.go). Never 404s on a
// purged feature -- the capture outlives the row, and an empty list is the normal
// answer for any feature never placed by GPS -- only on an unknown class.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

func gpsCapturesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		id := chi.URLParam(r, "id")

		// Resolve the class through metadata.Load (same as the cmd/server route via
		// loadOrNotFound) so a case-insensitive code still works and an unknown class
		// is a clean 404 rather than an empty list.
		loaded, err := metadata.Load(r.Context(), db, code)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, fmt.Sprintf("feature class %q does not exist", code))
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		entries, err := crud.ListFeatureGpsCaptures(r.Context(), db, loaded.FeatureClass.Code, id)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		// A nil slice would marshal to JSON null; normalize to [] so the device
		// always parses an array (an empty one is the common answer here).
		var payload any = entries
		if entries == nil {
			payload = []any{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"gps_captures": payload})
	}
}
