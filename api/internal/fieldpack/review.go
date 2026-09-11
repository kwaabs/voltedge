package fieldpack

// The field-sync review queue: the web/admin surface of QA mode (Phase 23 Slice 3,
// docs/phase-23-sync-inbox.md). This is the second, session-authenticated face of
// ops.sync_inbox -- the counterpart to the device-facing, bearer-authed status pull
// in sync.go. When a feature class is set to 'review' mode (policy.go), its captured
// ops land 'pending_review' and the drain worker skips them; a supervisor lists them
// here, sees the proposed change, and POSTs the ones to apply (flipping them to
// 'received' for the worker) or REJECTs the rest.
//
//	GET    /sync-review               ops in a status (default pending_review), + owner
//	GET    /sync-review/{id}           one op + the current production row, for a diff (Slice 4)
//	POST   /sync-review/{id}/post      post a reviewed op -> worker applies it (sync:post)
//	POST   /sync-review/{id}/reject    reject a reviewed op -> quarantined (sync:post)
//	POST   /sync-review/{id}/resolve   resolve a 'conflict' op: apply-anyway | keep (sync:post, Slice 4)
//	GET    /sync-review/policy         the configured per-class + global apply modes
//	PUT    /sync-review/policy         set a class's (or the global) apply mode (sync:post)
//	DELETE /sync-review/policy         clear a class override, reverting to the default
//
// Mounted by cmd/server behind a method-aware sync:review / sync:post gate (GET =>
// review, mutations => post), like the field-packs admin surface. RequireCSRF guards
// the mutations (double-submit), matching AdminRoutes.

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// AdminReviewRoutes returns the review-queue router. Mirrors AdminRoutes: CSRF on the
// mutating verbs (the mount adds the RBAC gate), owner is NOT scoped -- a reviewer
// acts on every user's held ops, which is the whole point of a review surface.
func AdminReviewRoutes(db *bun.DB) chi.Router {
	r := chi.NewRouter()
	r.Use(auth.RequireCSRF)
	r.Get("/", listReviewHandler(db))
	// Static /policy routes are registered before the /{id} wildcard so chi matches
	// them first; an op id is a UUID, so it never collides with the literal "policy".
	r.Get("/policy", listPolicyHandler(db))
	r.Put("/policy", setPolicyHandler(db))
	r.Delete("/policy", deletePolicyHandler(db))
	r.Get("/{id}", getReviewHandler(db))
	r.Post("/{id}/post", postReviewHandler(db))
	r.Post("/{id}/reject", rejectReviewHandler(db))
	r.Post("/{id}/resolve", resolveReviewHandler(db))
	return r
}

// reviewStatuses is the allow-list the queue can be filtered to. pending_review is the
// default (the work); the terminal states let a reviewer audit what was posted or
// quarantined. 'applying'/'received' are transient worker states, not review states,
// so they're intentionally not offered here.
var reviewStatuses = map[string]bool{
	"pending_review": true,
	"rejected":       true,
	"applied":        true,
	"conflict":       true,
}

// reviewRow is one ops.sync_inbox row joined to its submitter, as read for the queue.
type reviewRow struct {
	OpID         string          `bun:"op_id"`
	RecvSeq      int64           `bun:"recv_seq"`
	SubmittedBy  string          `bun:"submitted_by"`
	Email        string          `bun:"email"`
	DisplayName  string          `bun:"display_name"`
	DeviceID     *string         `bun:"device_id"`
	SubmissionID *string         `bun:"submission_id"`
	Kind         string          `bun:"kind"`
	FeatureClass *string         `bun:"feature_class"`
	FeatureID    *string         `bun:"feature_id"`
	Payload      json.RawMessage `bun:"payload"`
	BaseVersion  *string         `bun:"base_version"`
	Status       string          `bun:"status"`
	Error        *string         `bun:"error"`
	ReviewedBy   *string         `bun:"reviewed_by"`
	ReviewerName *string         `bun:"reviewer_name"`
	ReviewedAt   *time.Time      `bun:"reviewed_at"`
	ReceivedAt   time.Time       `bun:"received_at"`
}

// reviewView is one held op over the wire to the review UI: the proposed change
// (kind + class + feature + raw payload the web decodes into a diff), who submitted
// it and when, and -- once acted on -- who reviewed it. Payload is passed through
// verbatim so the client renders the same crud/graph input the worker would apply.
type reviewView struct {
	OpID         string          `json:"op_id"`
	RecvSeq      int64           `json:"recv_seq"`
	SubmittedBy  string          `json:"submitted_by"`
	OwnerEmail   string          `json:"owner_email"`
	OwnerName    string          `json:"owner_name"`
	DeviceID     *string         `json:"device_id,omitempty"`
	SubmissionID *string         `json:"submission_id,omitempty"`
	Kind         string          `json:"kind"`
	FeatureClass *string         `json:"feature_class,omitempty"`
	FeatureID    *string         `json:"feature_id,omitempty"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	BaseVersion  *string         `json:"base_version,omitempty"`
	Status       string          `json:"status"`
	Error        *string         `json:"error,omitempty"`
	ReviewedBy   *string         `json:"reviewed_by,omitempty"`
	ReviewerName *string         `json:"reviewer_name,omitempty"`
	ReviewedAt   *time.Time      `json:"reviewed_at,omitempty"`
	ReceivedAt   time.Time       `json:"received_at"`
}

func toReviewView(row *reviewRow) reviewView {
	return reviewView{
		OpID:         row.OpID,
		RecvSeq:      row.RecvSeq,
		SubmittedBy:  row.SubmittedBy,
		OwnerEmail:   row.Email,
		OwnerName:    row.DisplayName,
		DeviceID:     row.DeviceID,
		SubmissionID: row.SubmissionID,
		Kind:         row.Kind,
		FeatureClass: row.FeatureClass,
		FeatureID:    row.FeatureID,
		Payload:      row.Payload,
		BaseVersion:  row.BaseVersion,
		Status:       row.Status,
		Error:        row.Error,
		ReviewedBy:   row.ReviewedBy,
		ReviewerName: row.ReviewerName,
		ReviewedAt:   row.ReviewedAt,
		ReceivedAt:   row.ReceivedAt,
	}
}

// listReviewHandler lists the ops in a given status (default pending_review), oldest
// first -- the order a reviewer works through them and the order the worker will
// apply posted ones (recv_seq). Joined to the submitter (and the reviewer, if any).
func listReviewHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		status := strings.TrimSpace(r.URL.Query().Get("status"))
		if status == "" {
			status = "pending_review"
		}
		if !reviewStatuses[status] {
			writeErr(w, http.StatusBadRequest, "unsupported status filter "+status)
			return
		}

		const q = `
SELECT s.op_id, s.recv_seq, s.submitted_by, u.email, u.display_name,
       s.device_id, s.submission_id, s.kind, s.feature_class, s.feature_id,
       s.payload, s.base_version, s.status, s.error,
       s.reviewed_by, ru.display_name AS reviewer_name, s.reviewed_at, s.received_at
FROM ops.sync_inbox s
JOIN iam.users u ON u.id = s.submitted_by
LEFT JOIN iam.users ru ON ru.id = s.reviewed_by
WHERE s.status = ?
ORDER BY s.recv_seq
LIMIT 500`

		var rows []reviewRow
		if err := db.NewRaw(q, status).Scan(r.Context(), &rows); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		views := make([]reviewView, len(rows))
		for i := range rows {
			views[i] = toReviewView(&rows[i])
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ops": views})
	}
}

// postReviewHandler posts one held op: pending_review -> received, stamped with the
// reviewer, so the drain worker applies it in recv_seq order like any auto op. A post
// of an op that isn't pending_review (already posted/applied/rejected, or unknown id)
// changes nothing -> 409, so a stale click can't resurrect a terminal op.
func postReviewHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			unauthorized(w)
			return
		}
		ok2, err := postInboxOp(r.Context(), db, chi.URLParam(r, "id"), user.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok2 {
			writeErr(w, http.StatusConflict, "op is not pending review (already posted, applied, or rejected)")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// rejectReviewHandler rejects one held op: pending_review -> rejected, with the
// reviewer's reason. It's the same terminal 'rejected' state an apply failure uses,
// so the device surfaces it through the existing "needs attention" channel (Slice 2).
func rejectReviewHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			unauthorized(w)
			return
		}
		var body struct {
			Reason string `json:"reason"`
		}
		// A body is optional -- an empty/absent one just yields a default reason.
		_ = json.NewDecoder(r.Body).Decode(&body)
		reason := strings.TrimSpace(body.Reason)
		if reason == "" {
			reason = "rejected in review"
		}
		ok2, err := rejectInboxReview(r.Context(), db, chi.URLParam(r, "id"), user.ID, reason)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok2 {
			writeErr(w, http.StatusConflict, "op is not pending review (already posted, applied, or rejected)")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// getReviewRowByID reads one op (joined to submitter + reviewer) by op_id -- the same
// projection as the list, for the conflict-detail view. Returns (nil, nil) when absent.
func getReviewRowByID(db *bun.DB, r *http.Request, opID string) (*reviewRow, error) {
	const q = `
SELECT s.op_id, s.recv_seq, s.submitted_by, u.email, u.display_name,
       s.device_id, s.submission_id, s.kind, s.feature_class, s.feature_id,
       s.payload, s.base_version, s.status, s.error,
       s.reviewed_by, ru.display_name AS reviewer_name, s.reviewed_at, s.received_at
FROM ops.sync_inbox s
JOIN iam.users u ON u.id = s.submitted_by
LEFT JOIN iam.users ru ON ru.id = s.reviewed_by
WHERE s.op_id = ?`

	var rows []reviewRow
	if err := db.NewRaw(q, opID).Scan(r.Context(), &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// getReviewHandler returns one op plus the CURRENT production row it targets, so the
// conflict UI can diff the device's proposed change against the live server value
// (Phase 23 Slice 4). current is null for a create (no prior row), an establish (no
// single target feature), or a target that no longer exists. A failure to load the
// current row is non-fatal -- the op is still returned, just without a diff basis.
func getReviewHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		row, err := getReviewRowByID(db, r, chi.URLParam(r, "id"))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if row == nil {
			writeErr(w, http.StatusNotFound, "no such op")
			return
		}
		view := toReviewView(row)

		var current map[string]any
		if (row.Kind == "update_attrs" || row.Kind == "delete") && row.FeatureClass != nil && row.FeatureID != nil {
			if loaded, e := metadata.Load(r.Context(), db, *row.FeatureClass); e == nil {
				if f, e := crud.GetFeature(r.Context(), db, loaded, *row.FeatureID); e == nil {
					current = f
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"op": view, "current": current})
	}
}

// resolveReviewHandler resolves a 'conflict' op (Phase 23 Slice 4). action "apply"
// force-applies the device's edit (-> received + conflict_override, the worker re-runs
// it skipping the version check); action "keep" discards it and keeps the production
// value (-> rejected, surfaced to the device via the Slice 2 "needs attention" channel).
// A resolve of an op that isn't in conflict (already resolved, or never was) is a 409.
func resolveReviewHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			unauthorized(w)
			return
		}
		var body struct {
			Action string `json:"action"`
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		id := chi.URLParam(r, "id")

		var resolved bool
		var err error
		switch body.Action {
		case "apply":
			resolved, err = resolveConflictApply(r.Context(), db, id, user.ID)
		case "keep":
			reason := strings.TrimSpace(body.Reason)
			if reason == "" {
				reason = "kept production value in conflict review"
			}
			resolved, err = resolveConflictKeep(r.Context(), db, id, user.ID, reason)
		default:
			writeErr(w, http.StatusBadRequest, "action must be 'apply' or 'keep'")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !resolved {
			writeErr(w, http.StatusConflict, "op is not in conflict (already resolved, or never was)")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// policyView is one apply-policy row over the wire. FeatureClass is the class code,
// or "*" for the global default. The client presents the built-in default (auto)
// when no "*" row is returned.
type policyView struct {
	FeatureClass    string    `json:"feature_class"`
	Mode            string    `json:"mode"`
	DetectConflicts bool      `json:"detect_conflicts"`
	UpdatedBy       *string   `json:"updated_by,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// listPolicyHandler returns the configured policies plus the effective global default
// (the built-in 'auto' when no '*' row has been set), so the UI can show what any
// class without an override will do.
func listPolicyHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := listSyncPolicies(r.Context(), db)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		defaultMode := syncModeAuto
		defaultDetect := false
		views := make([]policyView, 0, len(rows))
		for i := range rows {
			if rows[i].FeatureClass == syncPolicyGlobalKey {
				defaultMode = rows[i].Mode
				defaultDetect = rows[i].DetectConflicts
			}
			views = append(views, policyView{
				FeatureClass:    rows[i].FeatureClass,
				Mode:            rows[i].Mode,
				DetectConflicts: rows[i].DetectConflicts,
				UpdatedBy:       rows[i].UpdatedBy,
				UpdatedAt:       rows[i].UpdatedAt,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"default_mode":            defaultMode,
			"default_detect_conflicts": defaultDetect,
			"policies":                views,
		})
	}
}

// setPolicyHandler upserts one class's (or the global "*") apply mode. An empty
// feature_class is treated as the global default. mode must be auto|review.
func setPolicyHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			unauthorized(w)
			return
		}
		var body struct {
			FeatureClass    string `json:"feature_class"`
			Mode            string `json:"mode"`
			DetectConflicts bool   `json:"detect_conflicts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		class := strings.TrimSpace(body.FeatureClass)
		if class == "" {
			class = syncPolicyGlobalKey
		}
		if body.Mode != syncModeAuto && body.Mode != syncModeReview {
			writeErr(w, http.StatusBadRequest, "mode must be 'auto' or 'review'")
			return
		}
		if err := setSyncPolicy(r.Context(), db, class, body.Mode, body.DetectConflicts, user.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// deletePolicyHandler clears a class override (?feature_class=code) or the global
// default (?feature_class=* or omitted), reverting to the inherited mode. A missing
// row is 404 so a stale delete is visible rather than a silent no-op.
func deletePolicyHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		class := strings.TrimSpace(r.URL.Query().Get("feature_class"))
		if class == "" {
			class = syncPolicyGlobalKey
		}
		removed, err := deleteSyncPolicy(r.Context(), db, class)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !removed {
			writeErr(w, http.StatusNotFound, "no policy set for "+class)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
