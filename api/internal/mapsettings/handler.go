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
	// The global annotation visible-zoom band (map markup/redlines): below
	// min / above max the whole annotations overlay hides, so notes don't
	// clutter an overview zoom. Both are OPTIONAL -- an absent/blank row means
	// "no bound on that side," so an unconfigured deployment shows annotations
	// at every zoom exactly as before this existed. No migration seeds these
	// (unlike the default-view keys); the PUT's upsert creates them on first
	// save and readView degrades a missing row to nil.
	keyAnnMinZoom = "map_annotation_min_zoom"
	keyAnnMaxZoom = "map_annotation_max_zoom"
	// The optional OPERATING-AREA bounds (the utility's service territory): a lon/lat
	// bbox the map's view is constrained to, outside which nothing is shown and no
	// tiles are fetched. All four are set together or all unset -- nil = "no bound",
	// so the map roams the whole world exactly as before. No migration seeds them;
	// the PUT upsert creates them on first save and readView degrades a missing row
	// to nil.
	keyBoundsMinLng = "map_bounds_min_lng"
	keyBoundsMinLat = "map_bounds_min_lat"
	keyBoundsMaxLng = "map_bounds_max_lng"
	keyBoundsMaxLat = "map_bounds_max_lat"

	defaultCenterLng = -0.127
	defaultCenterLat = 51.507
	defaultZoom      = 15.0
)

// mapView is the wire shape for both GET (response) and PUT (request body).
// The default-view trio stays plain floats -- the form always submits all
// three, so there's no partial-patch case to model. The annotation band is
// *float64 on purpose: nil is a real value here ("no bound on that side"),
// distinct from a supplied 0, so it round-trips as JSON null rather than
// collapsing an unset bound into zoom 0.
type mapView struct {
	CenterLng float64 `json:"center_lng"`
	CenterLat float64 `json:"center_lat"`
	Zoom      float64 `json:"zoom"`
	// Optional global annotation visible-zoom band (see keyAnnMinZoom).
	AnnotationMinZoom *float64 `json:"annotation_min_zoom"`
	AnnotationMaxZoom *float64 `json:"annotation_max_zoom"`
	// Optional operating-area bounds (see keyBoundsMinLng). All four together or all
	// nil; when set the client constrains the OL view to this lon/lat bbox.
	BoundsMinLng *float64 `json:"bounds_min_lng"`
	BoundsMinLat *float64 `json:"bounds_min_lat"`
	BoundsMaxLng *float64 `json:"bounds_max_lng"`
	BoundsMaxLat *float64 `json:"bounds_max_lat"`
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
	// Annotation band bounds are each optional; when present they share the
	// view's 0..22 zoom domain, and a supplied min must not exceed a supplied
	// max (an empty band that could never show anything).
	if v.AnnotationMinZoom != nil && (*v.AnnotationMinZoom < 0 || *v.AnnotationMinZoom > 22) {
		return "annotation_min_zoom must be between 0 and 22"
	}
	if v.AnnotationMaxZoom != nil && (*v.AnnotationMaxZoom < 0 || *v.AnnotationMaxZoom > 22) {
		return "annotation_max_zoom must be between 0 and 22"
	}
	if v.AnnotationMinZoom != nil && v.AnnotationMaxZoom != nil && *v.AnnotationMinZoom > *v.AnnotationMaxZoom {
		return "annotation_min_zoom must not exceed annotation_max_zoom"
	}
	// Operating bounds: all four together or none (a partial bbox can't constrain a
	// view). When set, within the geographic domain and min strictly below max (a
	// zero-area or inverted bbox would hide the whole map).
	boundsSet := 0
	for _, b := range []*float64{v.BoundsMinLng, v.BoundsMinLat, v.BoundsMaxLng, v.BoundsMaxLat} {
		if b != nil {
			boundsSet++
		}
	}
	if boundsSet != 0 && boundsSet != 4 {
		return "operating bounds require all four of bounds_min_lng/min_lat/max_lng/max_lat, or none"
	}
	if boundsSet == 4 {
		if *v.BoundsMinLng < -180 || *v.BoundsMaxLng > 180 {
			return "bounds longitude must be between -180 and 180"
		}
		if *v.BoundsMinLat < -90 || *v.BoundsMaxLat > 90 {
			return "bounds latitude must be between -90 and 90"
		}
		if *v.BoundsMinLng >= *v.BoundsMaxLng {
			return "bounds_min_lng must be less than bounds_max_lng"
		}
		if *v.BoundsMinLat >= *v.BoundsMaxLat {
			return "bounds_min_lat must be less than bounds_max_lat"
		}
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
		CenterLng:         floatOr(settings[keyCenterLng], defaultCenterLng),
		CenterLat:         floatOr(settings[keyCenterLat], defaultCenterLat),
		Zoom:              floatOr(settings[keyZoom], defaultZoom),
		AnnotationMinZoom: floatPtrOrNil(settings[keyAnnMinZoom]),
		AnnotationMaxZoom: floatPtrOrNil(settings[keyAnnMaxZoom]),
		BoundsMinLng:      floatPtrOrNil(settings[keyBoundsMinLng]),
		BoundsMinLat:      floatPtrOrNil(settings[keyBoundsMinLat]),
		BoundsMaxLng:      floatPtrOrNil(settings[keyBoundsMaxLng]),
		BoundsMaxLat:      floatPtrOrNil(settings[keyBoundsMaxLat]),
	}, nil
}

func loadSettings(ctx context.Context, db *bun.DB) (map[string]string, error) {
	var rows []auth.SystemSetting
	err := db.NewSelect().Model(&rows).
		Where("key IN (?)", bun.In([]string{
			keyCenterLng, keyCenterLat, keyZoom, keyAnnMinZoom, keyAnnMaxZoom,
			keyBoundsMinLng, keyBoundsMinLat, keyBoundsMaxLng, keyBoundsMaxLat,
		})).
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
		// An unset bound stores "" -- readView's floatPtrOrNil maps it straight
		// back to nil, so clearing a bound in the UI persists as "no bound".
		{Key: keyAnnMinZoom, Value: floatPtrToStr(v.AnnotationMinZoom), UpdatedAt: now, UpdatedBy: actor},
		{Key: keyAnnMaxZoom, Value: floatPtrToStr(v.AnnotationMaxZoom), UpdatedAt: now, UpdatedBy: actor},
		{Key: keyBoundsMinLng, Value: floatPtrToStr(v.BoundsMinLng), UpdatedAt: now, UpdatedBy: actor},
		{Key: keyBoundsMinLat, Value: floatPtrToStr(v.BoundsMinLat), UpdatedAt: now, UpdatedBy: actor},
		{Key: keyBoundsMaxLng, Value: floatPtrToStr(v.BoundsMaxLng), UpdatedAt: now, UpdatedBy: actor},
		{Key: keyBoundsMaxLat, Value: floatPtrToStr(v.BoundsMaxLat), UpdatedAt: now, UpdatedBy: actor},
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

// floatPtrOrNil parses an optional stored bound: a blank or corrupt row is nil
// ("no bound"), which is a meaningful value here rather than a fallback to zero.
func floatPtrOrNil(raw string) *float64 {
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	return &v
}

// floatPtrToStr is floatPtrOrNil's inverse for persistence: nil stores as ""
// (an unset bound), any value as its plain decimal.
func floatPtrToStr(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
