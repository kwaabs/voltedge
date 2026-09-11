// Package mapsettings serves the admin-configurable default map view -- the
// center lon/lat and zoom the geographic map opens on at app start. It reads
// and writes three scalar rows in iam.system_settings (seeded by migration
// 0066), the same global key/value store the auth break-glass and territory
// toggles use. Mounted at /map-settings:
//
//	GET /  read the default view (every map user calls this at startup)
//	PUT /  set the default view (admin)
//
// The mount's method-aware RBAC gate makes GET need features:read (so every
// map user gets the default) while PUT needs metadata:write (admin config,
// same gate as the rest of /admin).
package mapsettings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
)

// The three iam.system_settings keys backing the default view, and the
// fallbacks used when a row is missing or unparseable -- identical to the
// client's historic hardcoded placeholder (voltedge-map.tsx) so behaviour is
// unchanged until an admin saves.
const (
	keyCenterLng = "map_default_center_lng"
	keyCenterLat = "map_default_center_lat"
	keyZoom      = "map_default_zoom"

	defaultCenterLng = -0.127
	defaultCenterLat = 51.507
	defaultZoom      = 15.0
)

// mapView is the wire shape for both GET (response) and PUT (request body).
// Pointers on PUT would let a caller patch one axis, but the client always
// sends all three (the form submits the whole view), so plain floats keep it
// simple and the validation total.
type mapView struct {
	CenterLng float64 `json:"center_lng"`
	CenterLat float64 `json:"center_lat"`
	Zoom      float64 `json:"zoom"`
}

// Routes returns the /map-settings router.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/", getHandler(db))
	r.Put("/", putHandler(db))
	return r
}

func getHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		view, err := readView(r.Context(), db)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not read map settings")
			return
		}
		writeJSON(w, http.StatusOK, view)
	}
}

func putHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req mapView
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		if err := validate(req); err != "" {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		if err := writeView(r.Context(), db, req, auth.ActorID(r.Context())); err != nil {
			writeError(w, http.StatusInternalServerError, "could not save map settings")
			return
		}
		writeJSON(w, http.StatusOK, req)
	}
}

// validate rejects a view outside the geographic/zoom domain. Returns "" when
// the view is acceptable, else a human message.
func validate(v mapView) string {
	if v.CenterLng < -180 || v.CenterLng > 180 {
		return "center_lng must be between -180 and 180"
	}
	if v.CenterLat < -90 || v.CenterLat > 90 {
		return "center_lat must be between -90 and 90"
	}
	// OpenLayers' view is created with maxZoom 22; 0 is whole-world. Keep the
	// stored default inside that range so it can never open on an invalid zoom.
	if v.Zoom < 0 || v.Zoom > 22 {
		return "zoom must be between 0 and 22"
	}
	return ""
}

// readView loads the three keys, parsing each independently with a fallback so
// a missing or corrupt row degrades to the placeholder rather than erroring the
// whole map startup.
func readView(ctx context.Context, db *bun.DB) (mapView, error) {
	settings, err := loadSettings(ctx, db)
	if err != nil {
		return mapView{}, err
	}
	return mapView{
		CenterLng: floatOr(settings[keyCenterLng], defaultCenterLng),
		CenterLat: floatOr(settings[keyCenterLat], defaultCenterLat),
		Zoom:      floatOr(settings[keyZoom], defaultZoom),
	}, nil
}

func loadSettings(ctx context.Context, db *bun.DB) (map[string]string, error) {
	var rows []auth.SystemSetting
	err := db.NewSelect().Model(&rows).
		Where("key IN (?)", bun.In([]string{keyCenterLng, keyCenterLat, keyZoom})).
		Scan(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Key] = row.Value
	}
	return out, nil
}

// writeView upserts all three keys in one transaction, stamping who changed it.
// The rows are seeded by migration 0066, but ON CONFLICT keeps this correct even
// on a store that predates the seed.
func writeView(ctx context.Context, db *bun.DB, v mapView, byUserID string) error {
	var actor *string
	if byUserID != "" {
		actor = &byUserID
	}
	now := time.Now()
	rows := []auth.SystemSetting{
		{Key: keyCenterLng, Value: strconv.FormatFloat(v.CenterLng, 'f', -1, 64), UpdatedAt: now, UpdatedBy: actor},
		{Key: keyCenterLat, Value: strconv.FormatFloat(v.CenterLat, 'f', -1, 64), UpdatedAt: now, UpdatedBy: actor},
		{Key: keyZoom, Value: strconv.FormatFloat(v.Zoom, 'f', -1, 64), UpdatedAt: now, UpdatedBy: actor},
	}
	_, err := db.NewInsert().Model(&rows).
		On("CONFLICT (key) DO UPDATE").
		Set("value = EXCLUDED.value").
		Set("updated_at = EXCLUDED.updated_at").
		Set("updated_by = EXCLUDED.updated_by").
		Exec(ctx)
	return err
}

func floatOr(raw string, fallback float64) float64 {
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return v
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
