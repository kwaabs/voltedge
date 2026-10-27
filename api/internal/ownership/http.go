package ownership

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns the ownership-propagation endpoints:
//
//	POST /mark-private  declare a feature (a transformer) private and stamp
//	                    ownership=private onto everything downstream of it
//	POST /mark-utility  the inverse: clear ownership back to utility on a feature
//	                    and everything downstream of it
//	POST /resync        recompute the whole ownership map from every currently
//	                    private boundary (the drift fix)
//
// All bulk-write feature rows, so api/cmd/server/main.go mounts this behind a
// features:write gate -- the same capability a single attribute edit needs.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Post("/mark-private", markPrivateHandler(db))
	r.Post("/mark-utility", markUtilityHandler(db))
	r.Post("/resync", resyncHandler(db))
	return r
}

// markPrivateRequest is the POST /mark-private body: the boundary feature to
// declare private and propagate from.
type markPrivateRequest struct {
	FeatureClass string `json:"feature_class"`
	FeatureID    string `json:"feature_id"`
}

func markPrivateHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req markPrivateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		if req.FeatureClass == "" || req.FeatureID == "" {
			writeError(w, invalidf("feature_class and feature_id are required"))
			return
		}
		res, err := MarkDownstreamPrivate(r.Context(), db, FeatureRef{
			FeatureClass: req.FeatureClass,
			FeatureID:    req.FeatureID,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func markUtilityHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req markPrivateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		if req.FeatureClass == "" || req.FeatureID == "" {
			writeError(w, invalidf("feature_class and feature_id are required"))
			return
		}
		res, err := MarkDownstreamUtility(r.Context(), db, FeatureRef{
			FeatureClass: req.FeatureClass,
			FeatureID:    req.FeatureID,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func resyncHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res, err := Resync(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

// writeError maps a client-fixable InvalidError to 400 and everything else to
// 500 -- the same split circuit/crud draw. (A bad start feature surfaces from
// the trace engine as a wrapped error -> 500, which is honest: the panel only
// offers this action on a real, open feature.)
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if IsInvalid(err) {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
