package connectivity

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
)

// Routes returns a router for the connectivity engine, meant to be
// mounted at /connectivity:
//
//	POST   /                establish a connection, gated on network_rules
//	GET    /?feature_class=&feature_id=  list connections for one feature
//	GET    /audit            read-only connectivity consistency report (?feature_ids= scopes it)
//	POST   /audit/cleanup     remove orphaned connections (connectivity:commit)
//	POST   /audit/reconcile   run the audit and persist it into the issue queue (connectivity:commit)
//	GET    /issues            the validation-issue queue (filter by status/severity/kind)
//	PATCH  /issues/{id}       triage one issue (acknowledge/resolve/suppress)
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
	// Incremental validation (Step 2): run the audit and persist it into the
	// self-pruning ops.validation_issues queue; list/triage that queue.
	r.Post("/audit/reconcile", reconcileHandler(db))
	r.Get("/issues", issuesListHandler(db))
	r.Patch("/issues/{id}", issuePatchHandler(db))
	// The completeness / business-rule catalog behind the attribute audit (rules.go,
	// migration 0155). GET lists a class's rules; the write verbs maintain the
	// catalog and escalate in-handler to metadata:write (admin policy), like the
	// cost/curve/fault-mode catalogs. Static "/rules..." routes match ahead of the
	// "/{id}" connection-revoke below.
	r.Get("/rules", rulesListHandler(db))
	r.Post("/rules", ruleCreateHandler(db))
	r.Patch("/rules/{id}", ruleUpdateHandler(db))
	r.Delete("/rules/{id}", ruleDeleteHandler(db))
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
// nothing; optional tolerance_m and max_findings query params tune the run, and
// an optional feature_ids (comma-separated) SCOPES it to those features' own
// connectivity/relationships/coincidences -- the "validate just this selection"
// path, versus the whole-database sweep when it's omitted.
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
		if v := r.URL.Query().Get("feature_ids"); strings.TrimSpace(v) != "" {
			for _, id := range strings.Split(v, ",") {
				if id = strings.TrimSpace(id); id != "" {
					in.FeatureIDs = append(in.FeatureIDs, id)
				}
			}
		}

		report, err := AuditAll(r.Context(), db, in)
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

// reconcileHandler runs the audit and PERSISTS it into the self-pruning issue queue
// (issues.go): upsert findings, auto-resolve the fixed ones in the audited scope. The
// optional JSON body { tolerance_m?, max_findings?, feature_ids?[] } scopes and tunes
// the run; an empty body reconciles the whole database.
func reconcileHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ToleranceM  float64  `json:"tolerance_m"`
			MaxFindings int      `json:"max_findings"`
			FeatureIDs  []string `json:"feature_ids"`
		}
		// Body is optional -- an empty/absent body is a whole-database reconcile.
		_ = json.NewDecoder(r.Body).Decode(&req)

		in := AuditInput{ToleranceMeters: req.ToleranceM, MaxFindings: req.MaxFindings, FeatureIDs: req.FeatureIDs}
		report, err := AuditAll(r.Context(), db, in)
		if err != nil {
			writeError(w, err)
			return
		}
		result, err := ReconcileIssues(r.Context(), db, report, req.FeatureIDs)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": result, "summary": report.Summary})
	}
}

// issuesListHandler serves the validation-issue queue. Default (no status) is the live
// queue (open + acknowledged), errors first; optional status / severity / kind / limit
// query params filter it.
func issuesListHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f := IssueFilter{
			Status:   r.URL.Query().Get("status"),
			Severity: r.URL.Query().Get("severity"),
			Kind:     r.URL.Query().Get("kind"),
		}
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				f.Limit = n
			}
		}
		issues, err := ListIssues(r.Context(), db, f)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"issues": issues})
	}
}

// issuePatchHandler triages one issue: { "status": "acknowledged" | "resolved" |
// "suppressed" | "open" }, stamping the acting user on an acknowledge.
func issuePatchHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req struct {
			Status string `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		if err := UpdateIssueStatus(r.Context(), db, id, req.Status, auth.ActorID(r.Context())); err != nil {
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
