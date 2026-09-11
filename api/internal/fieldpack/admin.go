package fieldpack

// Admin pack management (Phase 21, docs/phase-21-field-tool.md). This is the
// second, separate HTTP surface onto ops.field_packs -- mounted by cmd/server
// (the main API), NOT cmd/fieldsync. The device-facing Routes above authenticate
// an iam.api_tokens bearer and are owner-scoped (a field tech only ever sees and
// cancels their OWN packs). Administration is the opposite: it runs under the
// web app's cookie session + RBAC (auth.RequirePermission), and lists/acts on
// EVERY user's packs, gated by the dedicated admin:field-packs permission
// (migration 0040).
//
//	GET    /field-packs           every user's packs, newest first (with owner + scope)
//	POST   /field-packs/{id}/disable  hide a pack from its owner's device list (reversible)
//	POST   /field-packs/{id}/enable   un-hide a disabled pack
//	DELETE /field-packs/{id}      reap both RustFS artifacts and delete the row (permanent)
//
// Disable is a soft flag (disabled_at) the device's listPacks filters out, so a
// pack an admin hid stops crowding the tablet while its artifacts stay in RustFS.
// Delete is the destructive path: it removes the two objects from RustFS and the
// row -- there is no owner scope on either action, that's the whole point of an
// admin surface.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/uptrace/bun"
)

// AdminRoutes returns the admin pack-management router, meant to be mounted at
// /field-packs behind an admin:field-packs RBAC gate in cmd/server. RequireCSRF
// is applied here (in addition to the mount's permission gate) because the
// mutating actions -- disable and especially delete, which reaps RustFS objects
// -- are exactly the state changes double-submit CSRF exists to protect.
func AdminRoutes(db *bun.DB, store *export.ObjectStore) chi.Router {
	r := chi.NewRouter()
	r.Use(auth.RequireCSRF)
	r.Get("/", adminListPacksHandler(db))
	r.Post("/{id}/disable", adminSetDisabledHandler(db, true))
	r.Post("/{id}/enable", adminSetDisabledHandler(db, false))
	r.Delete("/{id}", adminDeletePackHandler(db, store))
	return r
}

// adminPackRow is one ops.field_packs row joined to its owner, as read for the
// admin list. It carries the two object keys (not sent to the client) so the
// delete path can reap them.
type adminPackRow struct {
	ID             string          `bun:"id"`
	Status         string          `bun:"status"`
	RequestedBy    string          `bun:"requested_by"`
	Email          string          `bun:"email"`
	DisplayName    string          `bun:"display_name"`
	Scope           json.RawMessage `bun:"scope"`
	FeatureCount    *int            `bun:"feature_count"`
	AttachmentCount *int            `bun:"attachment_count"`
	TilesBytes      *int64          `bun:"tiles_bytes"`
	DataBytes      *int64          `bun:"data_bytes"`
	TilesObjectKey *string         `bun:"tiles_object_key"`
	DataObjectKey  *string         `bun:"data_object_key"`
	Error          *string         `bun:"error"`
	SnapshotAt     *time.Time      `bun:"snapshot_at"`
	DisabledAt     *time.Time      `bun:"disabled_at"`
	CreatedAt      time.Time       `bun:"created_at"`
	FinishedAt     *time.Time      `bun:"finished_at"`
}

// adminPackView is one pack over the wire to the admin UI: the build facts plus
// who owns it and what it covers (bbox + classes, unpacked from the scope jsonb),
// and whether it's currently hidden from the device.
type adminPackView struct {
	ID                 string     `json:"id"`
	Status             string     `json:"status"`
	OwnerID            string     `json:"owner_id"`
	OwnerEmail         string     `json:"owner_email"`
	OwnerName          string     `json:"owner_name"`
	FeatureClasses     []string   `json:"feature_classes,omitempty"`
	Bbox               []float64  `json:"bbox,omitempty"`
	FeatureCount       *int       `json:"feature_count,omitempty"`
	AttachmentCount    *int       `json:"attachment_count,omitempty"`
	TilesBytes         *int64     `json:"tiles_bytes,omitempty"`
	DataBytes          *int64     `json:"data_bytes,omitempty"`
	Error              *string    `json:"error,omitempty"`
	Disabled           bool       `json:"disabled"`
	SnapshotAt         *time.Time `json:"snapshot_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	FinishedAt         *time.Time `json:"finished_at,omitempty"`
}

func toAdminPackView(row *adminPackRow) adminPackView {
	var scope PackScope
	_ = json.Unmarshal(row.Scope, &scope) // written by us at enqueue; a parse miss just omits bbox/classes
	return adminPackView{
		ID:             row.ID,
		Status:         row.Status,
		OwnerID:        row.RequestedBy,
		OwnerEmail:     row.Email,
		OwnerName:      row.DisplayName,
		FeatureClasses:  scope.FeatureClasses,
		Bbox:            scope.Bbox,
		FeatureCount:    row.FeatureCount,
		AttachmentCount: row.AttachmentCount,
		TilesBytes:      row.TilesBytes,
		DataBytes:      row.DataBytes,
		Error:          row.Error,
		Disabled:       row.DisabledAt != nil,
		SnapshotAt:     row.SnapshotAt,
		CreatedAt:      row.CreatedAt,
		FinishedAt:     row.FinishedAt,
	}
}

func adminListPacksHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const q = `
SELECT fp.id, fp.status, fp.requested_by, u.email, u.display_name, fp.scope,
       fp.feature_count, fp.attachment_count, fp.tiles_bytes, fp.data_bytes,
       fp.tiles_object_key, fp.data_object_key,
       fp.error, fp.snapshot_at, fp.disabled_at, fp.created_at, fp.finished_at
FROM ops.field_packs fp
JOIN iam.users u ON u.id = fp.requested_by
ORDER BY fp.created_at DESC
LIMIT 500`

		var rows []adminPackRow
		if err := db.NewRaw(q).Scan(r.Context(), &rows); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		views := make([]adminPackView, len(rows))
		for i := range rows {
			views[i] = toAdminPackView(&rows[i])
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"packs": views})
	}
}

// adminSetDisabledHandler flips a pack's disabled_at (set on disable, cleared on
// enable). Not owner-scoped -- an admin manages any user's packs. A missing id
// is 404.
func adminSetDisabledHandler(db *bun.DB, disable bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		upd := db.NewUpdate().Model((*FieldPack)(nil)).Where("id = ?", id)
		if disable {
			upd = upd.Set("disabled_at = now()")
		} else {
			upd = upd.Set("disabled_at = NULL")
		}
		res, err := upd.Exec(r.Context())
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			writeErr(w, http.StatusNotFound, "pack not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// adminDeletePackHandler reaps a pack's two RustFS artifacts (best-effort -- a
// missing object is not an error) and hard-deletes the row. Not owner-scoped. A
// queued/running pack has no artifacts yet, so only the row is removed; should
// the worker later finish that build, its completePack no-ops (the row is gone)
// and the uploaded artifacts are left for the retention sweep to reap as orphans.
func adminDeletePackHandler(db *bun.DB, store *export.ObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		pack := new(FieldPack)
		if err := db.NewSelect().Model(pack).Where("id = ?", id).Scan(r.Context()); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "pack not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		reapArtifacts(r.Context(), store, pack)

		if _, err := db.NewDelete().Model((*FieldPack)(nil)).Where("id = ?", id).Exec(r.Context()); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// reapArtifacts best-effort removes both of a pack's RustFS objects. A delete
// error (already gone, transient) is swallowed on purpose: the row removal is the
// authoritative act, and a lingering object is exactly what the retention sweep
// exists to reap -- failing the whole request over it would strand the row.
func reapArtifacts(ctx context.Context, store *export.ObjectStore, pack *FieldPack) {
	for _, key := range []*string{pack.TilesObjectKey, pack.DataObjectKey} {
		if key != nil && *key != "" {
			_ = store.Delete(ctx, *key)
		}
	}
}
