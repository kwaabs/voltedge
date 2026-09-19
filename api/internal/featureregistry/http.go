package featureregistry

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns the feature-registry read endpoint:
//
//	GET /feature-registry?id=<uuid>                 resolve one id -> {feature_id, feature_class_code, class_name, ...} (404 if unknown)
//	GET /feature-registry?q=<prefix>&class=&live=   typeahead over the registry (id prefix, optional class, live-only)
//
// Read-only, gated features:read on the mount in api/cmd/server/main.go (the
// same capability the feature CRUD read side uses -- the registry is just an
// index of those same features). No write verbs: the registry is maintained by
// the compiler-attached DB trigger, never over HTTP.
//
// v1 does NOT territory-scope the typeahead to the caller's Phase-58 access
// (the store supports it via SearchFilter.TerritoryIDs, but this layer passes
// none). Wiring the session's resolved territories in is a documented follow-up;
// until then a holder of features:read can look up any id, matching the read
// scope the rest of the read surface grants today.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	// Relative to the /feature-registry mount (see cmd/server/main.go) -- the
	// oms/wam convention. The mount adds the prefix; routing "/feature-registry"
	// here would double it to /feature-registry/feature-registry.
	r.Get("/", listHandler(db))
	return r
}

func listHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		// An exact id wins: this is the "what is this id?" resolve path.
		if id := q.Get("id"); id != "" {
			entry, err := Lookup(r.Context(), db, id)
			if err != nil {
				writeError(w, err)
				return
			}
			if entry == nil {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "no feature with that id"})
				return
			}
			writeJSON(w, http.StatusOK, entry)
			return
		}

		entries, err := Search(r.Context(), db, SearchFilter{
			Q:     q.Get("q"),
			Class: q.Get("class"),
			Live:  q.Get("live") == "true",
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
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
