package issuework

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/connectivity"
	"github.com/kwaabs/voltedge/api/internal/wam"
)

// Routes exposes assigning a data-quality issue as a fix:
//
//	POST /{issueID}/assign   raise a WAM work order for the issue and link it,
//	                         body { assigned_to_user_id?, assigned_to?, priority? }.
//
// Gated on wam:write -- raising and assigning work is a WAM authority, and the
// issue's move to acknowledged is a side effect of that assignment (not the public
// connectivity triage path). The assignee is any user wam_list_assignable_users
// returns, office or field; a field user receives it through their work pack.
func Routes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(auth.RequirePermission(auth.PermWAMWrite, auth.PermWAMWrite))
		// Static /assign-bulk (one segment) never collides with /{issueID}/assign (two).
		r.Post("/assign-bulk", bulkAssignHandler(db))
		r.Post("/{issueID}/assign", assignHandler(db))
	})
	return r
}

type assignRequest struct {
	AssignedToUserID string `json:"assigned_to_user_id"`
	AssignedTo       string `json:"assigned_to"`
	Priority         string `json:"priority"`
}

type bulkAssignRequest struct {
	IssueIDs         []string `json:"issue_ids"`
	AssignedToUserID string   `json:"assigned_to_user_id"`
	AssignedTo       string   `json:"assigned_to"`
	Priority         string   `json:"priority"`
}

func bulkAssignHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req bulkAssignRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		if len(req.IssueIDs) == 0 {
			writeError(w, invalidf("issue_ids is required"))
			return
		}
		result, err := AssignIssues(r.Context(), db, BulkAssignInput{
			IssueIDs:         req.IssueIDs,
			AssignedToUserID: req.AssignedToUserID,
			AssignedTo:       req.AssignedTo,
			Priority:         req.Priority,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func assignHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		issueID := chi.URLParam(r, "issueID")
		var req assignRequest
		// An empty body is allowed (raise an unassigned draft); only malformed JSON is an error.
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, invalidf("request body is not valid JSON: %v", err))
			return
		}
		result, err := AssignIssue(r.Context(), db, AssignInput{
			IssueID:          issueID,
			AssignedToUserID: req.AssignedToUserID,
			AssignedTo:       req.AssignedTo,
			Priority:         req.Priority,
		})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError maps a client-fixable problem (already-assigned, bad work-order
// params) to 400, a missing issue to 404, everything else to 500.
func writeError(w http.ResponseWriter, err error) {
	var invalid *InvalidError
	status := http.StatusInternalServerError
	switch {
	case errors.As(err, &invalid), wam.IsInvalid(err), connectivity.IsValidationError(err):
		status = http.StatusBadRequest
	case connectivity.IsNotFoundError(err):
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
