package basemap

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
)

// Routes returns the basemap catalog endpoints:
//
//	GET    /       the basemaps the map may show (enabled only; ?all=true for
//	               the admin page, which lists disabled ones too)
//	POST   /       add a custom basemap
//	PATCH  /{id}   relabel / re-point / reorder / enable-disable a basemap
//	DELETE /{id}   delete a custom basemap (built-ins are protected)
//
// Reads and writes share one mount but split by capability, exactly like
// /map-settings: GET is gated by features:read (every map viewer fetches the
// list to build its picker) while the mutating verbs require metadata:write
// (admin config, same gate as the rest of /admin). api/cmd/server/main.go wires
// that method-aware gate (RequirePermission(features:read, metadata:write)).
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/", listBasemapsHandler(db))
	r.Post("/", createBasemapHandler(db))
	r.Patch("/{id}", updateBasemapHandler(db))
	r.Delete("/{id}", deleteBasemapHandler(db))
	return r
}

func listBasemapsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The map wants only usable basemaps; the admin page passes ?all=true to
		// also see disabled rows it can re-enable.
		enabledOnly := r.URL.Query().Get("all") != "true"
		basemaps, err := ListBasemaps(r.Context(), db, enabledOnly)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"basemaps": basemaps})
	}
}

// createBasemapRequest is the POST body. attribution/max_zoom are optional;
// is_enabled defaults to true when omitted so a new basemap is usable at once.
type createBasemapRequest struct {
	Code        string            `json:"code"`
	Label       string            `json:"label"`
	URLTemplate string            `json:"url_template"`
	Attribution []AttributionLink `json:"attribution"`
	MaxZoom     *int              `json:"max_zoom"`
	MinZoom     int               `json:"min_zoom"`
	TMS         bool              `json:"tms"`
	SortOrder   int               `json:"sort_order"`
	IsEnabled   *bool             `json:"is_enabled"`
}

func createBasemapHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createBasemapRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		enabled := true
		if req.IsEnabled != nil {
			enabled = *req.IsEnabled
		}
		b, err := CreateBasemap(r.Context(), db, CreateParams{
			Code:        req.Code,
			Label:       req.Label,
			URLTemplate: req.URLTemplate,
			Attribution: req.Attribution,
			MaxZoom:     req.MaxZoom,
			MinZoom:     req.MinZoom,
			TMS:         req.TMS,
			SortOrder:   req.SortOrder,
			IsEnabled:   enabled,
			UpdatedBy:   auth.ActorID(r.Context()),
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, b)
	}
}

// updateBasemapRequest is the PATCH body. Every field is optional; only what's
// present changes. clear_max_zoom removes the zoom cap (distinct from "leave it
// alone"); code is absent because it is immutable (see UpdateParams).
type updateBasemapRequest struct {
	Label        *string            `json:"label"`
	URLTemplate  *string            `json:"url_template"`
	Attribution  *[]AttributionLink `json:"attribution"`
	MaxZoom      *int               `json:"max_zoom"`
	ClearMaxZoom bool               `json:"clear_max_zoom"`
	MinZoom      *int               `json:"min_zoom"`
	TMS          *bool              `json:"tms"`
	SortOrder    *int               `json:"sort_order"`
	IsEnabled    *bool              `json:"is_enabled"`
}

func updateBasemapHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req updateBasemapRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("invalid request body: %v", err))
			return
		}
		b, err := UpdateBasemap(r.Context(), db, id, UpdateParams{
			Label:        req.Label,
			URLTemplate:  req.URLTemplate,
			Attribution:  req.Attribution,
			MaxZoom:      req.MaxZoom,
			ClearMaxZoom: req.ClearMaxZoom,
			MinZoom:      req.MinZoom,
			TMS:          req.TMS,
			SortOrder:    req.SortOrder,
			IsEnabled:    req.IsEnabled,
			UpdatedBy:    auth.ActorID(r.Context()),
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, b)
	}
}

func deleteBasemapHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := DeleteBasemap(r.Context(), db, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// writeJSON writes a JSON body with the given status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// writeError maps a client-fixable InvalidError to 400 and everything else to
// 500 -- the same split circuit and crud draw.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if IsInvalid(err) {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
