package connectivity

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"
)

// Routes returns a router for the connectivity engine, meant to be
// mounted at /connectivity:
//
//	POST   /                establish a connection, gated on network_rules
//	GET    /?feature_class=&feature_id=  list connections for one feature
//	GET    /audit            read-only connectivity consistency report
//	POST   /audit/cleanup     remove orphaned connections (connectivity:commit)
//	DELETE /{id}             revoke (permanently remove) a connection
//
// Unlike api/internal/crud, there's no {code} in the path -- a
// connection always spans two feature classes, named in the request body
// (establish) or query string (list), not the URL.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Post("/", establishHandler(db))
	r.Get("/", listHandler(db))
	r.Get("/audit", auditHandler(db))
	r.Post("/audit/cleanup", cleanupHandler(db))
	r.Delete("/{id}", revokeHandler(db))
	return r
}

// cleanupHandler removes orphaned connectivity rows (those pointing at a deleted
// or missing feature) reported by the audit. It is a POST (a write), so the
// /connectivity mount's method-aware gate requires connectivity:commit --
// editor and above, never a read-only viewer.
func cleanupHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conns, err := CleanupOrphanedConnections(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		rels, err := CleanupOrphanedRelationships(r.Context(), db)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"removed":               conns + rels,
			"removed_connections":   conns,
			"removed_relationships": rels,
		})
	}
}

// auditHandler serves the read-only consistency audit (audit.go). It writes
// nothing; optional tolerance_m and max_findings query params tune the run.
func auditHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in AuditInput
		if v := r.URL.Query().Get("tolerance_m"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				in.ToleranceMeters = f
			}
		}
		if v := r.URL.Query().Get("max_findings"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				in.MaxFindings = n
			}
		}

		report, err := AuditConnectivity(r.Context(), db, in)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

func establishHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req establishRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		if req.From.FeatureClass == "" || req.From.FeatureID == "" {
			writeError(w, invalid("from.feature_class and from.feature_id are required"))
			return
		}
		if req.To.FeatureClass == "" || req.To.FeatureID == "" {
			writeError(w, invalid("to.feature_class and to.feature_id are required"))
			return
		}

		result, err := establishConnection(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func listHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		featureClass := r.URL.Query().Get("feature_class")
		featureID := r.URL.Query().Get("feature_id")
		if featureClass == "" || featureID == "" {
			writeError(w, invalid("feature_class and feature_id query parameters are required"))
			return
		}

		results, err := listConnections(r.Context(), db, featureClass, featureID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"connections": results})
	}
}

func revokeHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		if err := revokeConnection(r.Context(), db, id); err != nil {
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
	}
	writeJSON(w, status, apiError{Error: err.Error()})
}
