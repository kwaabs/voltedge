package servicelayers

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/secretbox"
)

// Routes returns the service-layer catalog endpoints:
//
//	GET    /       the external service layers the map may overlay (enabled only;
//	               ?all=true for the admin page, which lists disabled ones too)
//	POST   /       add a service layer
//	PATCH  /{id}   edit a service layer (credential is write-only)
//	DELETE /{id}   delete a service layer
//
// Reads and writes share one mount but split by capability, exactly like
// /basemaps and /map-settings: GET is gated by features:read (every map viewer
// fetches the list to build its layers panel) while the mutating verbs require
// metadata:write (admin config). api/cmd/server/main.go wires that method-aware
// gate. cipher is the at-rest credential encryptor; nil when the encryption key
// is unset, in which case storing a credential is refused with a clear error.
func Routes(db *bun.DB, cipher *secretbox.Cipher) chi.Router {
	s := &store{db: db, cipher: cipher}
	r := chi.NewRouter()
	r.Get("/", listHandler(s))
	r.Post("/", createHandler(s))
	r.Patch("/{id}", updateHandler(s))
	r.Delete("/{id}", deleteHandler(s))

	// Authenticated same-origin proxy (Slice 2). All GETs, so they ride the
	// mount's features:read gate -- every map viewer can fetch, but the upstream
	// URL and any credential stay server-side. tiles/{z}/{x}/{y} substitutes an
	// xyz/vector-tile template; proxy[/*] forwards a KVP/Esri request (WMS GetMap,
	// WMTS GetTile, Esri export) to the layer's endpoint. See proxy.go.
	r.Get("/{id}/tiles/{z}/{x}/{y}", tileProxyHandler(s))
	r.Get("/{id}/proxy", genericProxyHandler(s))
	r.Get("/{id}/proxy/*", genericProxyHandler(s))

	// Capabilities discovery (Slice 5): a POST -- so it rides the metadata:write
	// gate -- that fetches a GetCapabilities / ?f=json document on the admin form's
	// behalf (SSRF-vetted) and returns it for the browser to parse. See discover.go.
	r.Post("/discover", discoverHandler(s))
	return r
}

func listHandler(s *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The map wants only usable layers; the admin page passes ?all=true to
		// also see disabled rows it can re-enable.
		enabledOnly := r.URL.Query().Get("all") != "true"
		layers, err := ListServiceLayers(r.Context(), s, enabledOnly)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"service_layers": layers})
	}
}

func createHandler(s *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := createServiceLayer(r.Context(), s, req, auth.ActorID(r.Context()))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func updateHandler(s *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req updateRequest
		if !decodeJSON(w, r, &req) {
			return
		}
		result, err := updateServiceLayer(r.Context(), s, id, req, auth.ActorID(r.Context()))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func deleteHandler(s *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := deleteServiceLayer(r.Context(), s, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case isValidationError(err):
		status = http.StatusBadRequest
	case isNotFoundError(err):
		status = http.StatusNotFound
	case isUpstreamError(err):
		status = http.StatusBadGateway
	}
	writeJSON(w, status, apiError{Error: err.Error()})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, invalid("request body is not valid JSON: %v", err))
		return false
	}
	return true
}
