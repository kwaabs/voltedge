package boq

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns the router for the Bill of Quantities engine, meant to be
// mounted at /boq:
//
//	POST /  price a scope of the network (territory / radius / bbox / whole
//	        network by classes, or an explicit feature_refs set) and return a
//	        grouped, totalled schedule in a chosen currency.
//
// Read-only, like api/internal/trace and api/internal/powerflow -- it only
// counts and prices what is already committed.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Post("/", boqHandler(db))
	return r
}

func boqHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		result, err := RunBoQ(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

type apiError struct {
	Error string `json:"error"`
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
	}
	writeJSON(w, status, apiError{Error: err.Error()})
}
