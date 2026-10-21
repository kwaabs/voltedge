package assetlife

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes exposes the asset-lifecycle aging engine. Mounted at /asset-life with a
// method-aware gate (features:read for GET, metadata:write for catalog edits), so
// the handlers themselves need no in-handler escalation.
//
//	GET    /report          the aging report (query: feature_class, due_within_years, max_items)
//	GET    /catalog         the expected-useful-life catalog (query: feature_class)
//	POST   /catalog         add a catalog row
//	PATCH  /catalog/{id}    edit a catalog row's years / notes
//	DELETE /catalog/{id}    remove a catalog row
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Get("/report", reportHandler(db))
	r.Get("/catalog", catalogListHandler(db))
	r.Post("/catalog", catalogCreateHandler(db))
	r.Patch("/catalog/{id}", catalogUpdateHandler(db))
	r.Delete("/catalog/{id}", catalogDeleteHandler(db))
	return r
}

func reportHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		in := AgingInput{FeatureClass: q.Get("feature_class")}
		if v := q.Get("due_within_years"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				in.DueWithinYears = f
			}
		}
		if v := q.Get("max_items"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				in.MaxItems = n
			}
		}
		report, err := Aging(r.Context(), db, in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

func catalogListHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := ListExpectancies(r.Context(), db, r.URL.Query().Get("feature_class"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"expectancies": rows})
	}
}

type expectancyRequest struct {
	FeatureClassCode string  `json:"feature_class_code"`
	SubtypeCode      *int    `json:"subtype_code"`
	ExpectedYears    int     `json:"expected_years"`
	Notes            *string `json:"notes"`
}

func (req expectancyRequest) params() ExpectancyParams {
	return ExpectancyParams{
		FeatureClassCode: req.FeatureClassCode,
		SubtypeCode:      req.SubtypeCode,
		ExpectedYears:    req.ExpectedYears,
		Notes:            req.Notes,
	}
}

func catalogCreateHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req expectancyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("invalid request body: %v", err))
			return
		}
		row, err := CreateExpectancy(r.Context(), db, req.params())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, row)
	}
}

func catalogUpdateHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req expectancyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("invalid request body: %v", err))
			return
		}
		row, err := UpdateExpectancy(r.Context(), db, chi.URLParam(r, "id"), req.params())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, row)
	}
}

func catalogDeleteHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := DeleteExpectancy(r.Context(), db, chi.URLParam(r, "id")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
