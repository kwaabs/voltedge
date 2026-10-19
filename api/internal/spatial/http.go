package spatial

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// Routes returns the spatial-engine endpoints (mounted at /spatial, read-only,
// gated features:read):
//
//	GET /spatial/related   features spatially related to one reference feature:
//	  ?feature_class=&feature_id=   the reference feature (required)
//	  &mode=intersects|contains|contained_by|within   (default intersects)
//	  &distance=   metres (required for within)
//	  &classes=A,B,C   limit targets to these class codes (default: every geometric class)
//	  &limit=   cap the result (default 500, max 5000)
//
// Examples: assets inside an easement -> ref = the EASEMENT, mode=contains; the
// land covering a pole -> ref = the pole, mode=contained_by, classes=EASEMENT,
// LAND_PARCEL,UTILITY_CORRIDOR; vegetation within 5 m of a line -> ref = the line,
// mode=within, distance=5, classes=VEGETATION_ZONE.
// Also:
//
//	GET /spatial/vegetation-encroachments   vegetation too close to a conductor:
//	  ?feature_class=&feature_id=   a single conductor, OR
//	  ?circuit_id=                  every conductor on a feeder
//	  &clearance=   uniform clearance (m) override; omit for per-voltage defaults
//	  &classes=     vegetation class codes (default VEGETATION_ZONE)
//	  &limit=       cap (default 1000, max 10000)
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/related", relatedHandler(db))
	r.Get("/vegetation-encroachments", encroachmentsHandler(db))
	r.Post("/vegetation-encroachments/work-order", trimOrderHandler(db))
	r.Get("/easements/expiring", expiringEasementsHandler(db))
	r.Get("/vegetation/trim-due", trimDueHandler(db))
	return r
}

// GET /spatial/easements/expiring?within_days=  -- easements expired or expiring
// within the window (default 90 days), soonest first.
func expiringEasementsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		days, _ := strconv.Atoi(r.URL.Query().Get("within_days"))
		rows, err := ExpiringEasements(r.Context(), db, days)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"easements": rows, "count": len(rows)})
	}
}

// GET /spatial/vegetation/trim-due?within_days=  -- vegetation zones due (or
// overdue) for their trim cycle within the window (default 30 days), most overdue first.
func trimDueHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		days, _ := strconv.Atoi(r.URL.Query().Get("within_days"))
		rows, err := VegetationTrimDue(r.Context(), db, days)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"vegetation": rows, "count": len(rows)})
	}
}

// trimOrderRequest raises a WAM trim work order for one encroachment (Slice C2).
// The numbers come from the scan result; priority is derived unless overridden.
type trimOrderRequest struct {
	ConductorClass  string  `json:"conductor_class"`
	ConductorID     string  `json:"conductor_id"`
	VegetationClass string  `json:"vegetation_class"`
	VegetationID    string  `json:"vegetation_id"`
	DistanceM       float64 `json:"distance_m"`
	ClearanceM      float64 `json:"clearance_m"`
	Species         string  `json:"species"`
	Priority        string  `json:"priority"`
}

// trimOrderHandler raises a preventive WAM work order to trim a vegetation zone
// breaching clearance -- closing the detect-to-dispatch loop. The order's asset is
// the vegetation zone (what gets cut); the conductor and the measured breach ride
// in the title/description. Escalates beyond the mount's features:read to require
// wam:write in-handler (raising work is a WAM authority), mirroring the OMS
// clearance escalation.
func trimOrderHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok || !(user.BreakGlass || user.Has(auth.PermWAMWrite)) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "you do not have the required permission (wam:write)"})
			return
		}
		var req trimOrderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		order, err := RaiseTrimOrder(r.Context(), db, TrimOrderInput{
			ConductorClass:  req.ConductorClass,
			ConductorID:     req.ConductorID,
			VegetationClass: req.VegetationClass,
			VegetationID:    req.VegetationID,
			DistanceM:       req.DistanceM,
			ClearanceM:      req.ClearanceM,
			Species:         req.Species,
			Priority:        req.Priority,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, order)
	}
}

func encroachmentsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var vegClasses []string
		if raw := strings.TrimSpace(q.Get("classes")); raw != "" {
			for _, c := range strings.Split(raw, ",") {
				if c = strings.TrimSpace(c); c != "" {
					vegClasses = append(vegClasses, c)
				}
			}
		}
		clearance, _ := strconv.ParseFloat(q.Get("clearance"), 64)
		limit, _ := strconv.Atoi(q.Get("limit"))

		rows, err := VegetationEncroachments(r.Context(), db, EncroachmentInput{
			ConductorClass:    q.Get("feature_class"),
			ConductorID:       q.Get("feature_id"),
			CircuitID:         q.Get("circuit_id"),
			VegetationClasses: vegClasses,
			ClearanceM:        clearance,
			Limit:             limit,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"encroachments": rows, "count": len(rows)})
	}
}

func relatedHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		var targets []string
		if raw := strings.TrimSpace(q.Get("classes")); raw != "" {
			for _, c := range strings.Split(raw, ",") {
				if c = strings.TrimSpace(c); c != "" {
					targets = append(targets, c)
				}
			}
		}
		distance, _ := strconv.ParseFloat(q.Get("distance"), 64)
		limit, _ := strconv.Atoi(q.Get("limit"))

		matches, err := Query(r.Context(), db, QueryInput{
			RefClass:      q.Get("feature_class"),
			RefID:         q.Get("feature_id"),
			TargetClasses: targets,
			Mode:          q.Get("mode"),
			DistanceM:     distance,
			Limit:         limit,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"matches": matches, "count": len(matches)})
	}
}
