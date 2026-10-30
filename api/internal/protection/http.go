package protection

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns the protection-settings register endpoints. Mounted at
// /protection under the method-aware features gate (read to view, write to edit):
//
//	GET    /settings                the whole register (every device with settings)
//	GET    /resolve?feature_id=      locate a protective device by its bare id (GUID)
//	GET    /settings/{class}/{id}   one device's settings (404 when none)
//	PUT    /settings/{class}/{id}   create or replace a device's settings
//	DELETE /settings/{class}/{id}   remove a device's settings
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/settings", listHandler(db))
	r.Get("/resolve", resolveHandler(db))
	r.Get("/settings/{class}/{id}", getHandler(db))
	r.Put("/settings/{class}/{id}", upsertHandler(db))
	r.Delete("/settings/{class}/{id}", deleteHandler(db))
	return r
}

func listHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := List(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		if rows == nil {
			rows = []Settings{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"settings": rows})
	}
}

func resolveHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dev, err := ResolveDevice(r.Context(), db, r.URL.Query().Get("feature_id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, dev)
	}
}

func getHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s, err := GetByFeature(r.Context(), db, chi.URLParam(r, "class"), chi.URLParam(r, "id"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, s)
	}
}

type upsertRequest struct {
	RelayModel        *string  `json:"relay_model"`
	CTRatio           *string  `json:"ct_ratio"`
	PhasePickupA      *float64 `json:"phase_pickup_a"`
	PhaseTimeDial     *float64 `json:"phase_time_dial"`
	PhaseCurve        *string  `json:"phase_curve"`
	PhaseInstA        *float64 `json:"phase_instantaneous_a"`
	GroundPickupA     *float64 `json:"ground_pickup_a"`
	GroundTimeDial    *float64 `json:"ground_time_dial"`
	GroundCurve       *string  `json:"ground_curve"`
	GroundInstA       *float64 `json:"ground_instantaneous_a"`
	RecloseOperations *int     `json:"reclose_operations"`
	RecloseIntervals  *string  `json:"reclose_intervals"`
	Notes             *string  `json:"notes"`
}

func upsertHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req upsertRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		manual := "manual"
		s, err := Upsert(r.Context(), db, UpsertParams{
			FeatureClassCode:  chi.URLParam(r, "class"),
			FeatureID:         chi.URLParam(r, "id"),
			Source:            &manual, // a register-UI edit; a webhook ingest stamps its system code
			RelayModel:        req.RelayModel,
			CTRatio:           req.CTRatio,
			PhasePickupA:      req.PhasePickupA,
			PhaseTimeDial:     req.PhaseTimeDial,
			PhaseCurve:        req.PhaseCurve,
			PhaseInstA:        req.PhaseInstA,
			GroundPickupA:     req.GroundPickupA,
			GroundTimeDial:    req.GroundTimeDial,
			GroundCurve:       req.GroundCurve,
			GroundInstA:       req.GroundInstA,
			RecloseOperations: req.RecloseOperations,
			RecloseIntervals:  req.RecloseIntervals,
			Notes:             req.Notes,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, s)
	}
}

func deleteHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := DeleteByFeature(r.Context(), db, chi.URLParam(r, "class"), chi.URLParam(r, "id")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
