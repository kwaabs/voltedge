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
	// Cells (migration 0131, shared packs). Registered BEFORE the /{id} pack routes
	// so chi matches the literal "cells" segment first; a pack id is a UUID so it
	// never collides, but the explicit sub-router keeps the two surfaces separate.
	r.Route("/cells", func(r chi.Router) {
		r.Get("/", adminListCellsHandler(db))
		r.Post("/", adminCreateCellHandler(db))
		// Grid helper (S7): expand an outer bbox into a rows x cols set of cells
		// sharing one template, in one atomic call. A literal segment, so it's
		// matched ahead of the "/{id}" routes below.
		r.Post("/grid", adminCreateGridHandler(db))
		r.Get("/{id}", adminGetCellHandler(db))
		r.Put("/{id}", adminUpdateCellHandler(db))
		r.Delete("/{id}", adminDeleteCellHandler(db))
		r.Post("/{id}/build", adminBuildCellHandler(db))
		// Usage / reconciliation (S5): who holds which version, who has synced, who
		// has gone quiet -- the download ledger reconciled against the sync inbox.
		r.Get("/{id}/usage", adminCellUsageHandler(db))
		// Retention (S6): apply the cell's policy now, reaping old versions no crew
		// still holds. A no-op for a manual/pinned cell; keep_n reaps the eligible.
		r.Post("/{id}/reap", adminReapCellHandler(db, store))
	})
	r.Get("/", adminListPacksHandler(db))
	// Build-on-behalf (S8): stage a personal pack AS a named crew member, scoped from
	// THEIR RBAC. A literal segment, matched ahead of the "/{id}/..." routes.
	r.Post("/on-behalf", adminBuildOnBehalfHandler(db))
	r.Post("/{id}/disable", adminSetDisabledHandler(db, true))
	r.Post("/{id}/enable", adminSetDisabledHandler(db, false))
	r.Delete("/{id}", adminDeletePackHandler(db, store))
	return r
}

// adminPackRow is one ops.field_packs row joined to its owner, as read for the
// admin list. It carries the two object keys (not sent to the client) so the
// delete path can reap them.
type adminPackRow struct {
	ID              string          `bun:"id"`
	Status          string          `bun:"status"`
	RequestedBy     string          `bun:"requested_by"`
	Email           string          `bun:"email"`
	DisplayName     string          `bun:"display_name"`
	Scope           json.RawMessage `bun:"scope"`
	FeatureCount    *int            `bun:"feature_count"`
	AttachmentCount *int            `bun:"attachment_count"`
	TilesBytes      *int64          `bun:"tiles_bytes"`
	DataBytes       *int64          `bun:"data_bytes"`
	TilesObjectKey  *string         `bun:"tiles_object_key"`
	DataObjectKey   *string         `bun:"data_object_key"`
	Error           *string         `bun:"error"`
	SnapshotAt      *time.Time      `bun:"snapshot_at"`
	DisabledAt      *time.Time      `bun:"disabled_at"`
	CreatedAt       time.Time       `bun:"created_at"`
	FinishedAt      *time.Time      `bun:"finished_at"`
}

// adminPackView is one pack over the wire to the admin UI: the build facts plus
// who owns it and what it covers (bbox + classes, unpacked from the scope jsonb),
// and whether it's currently hidden from the device.
type adminPackView struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	OwnerID         string     `json:"owner_id"`
	OwnerEmail      string     `json:"owner_email"`
	OwnerName       string     `json:"owner_name"`
	FeatureClasses  []string   `json:"feature_classes,omitempty"`
	Bbox            []float64  `json:"bbox,omitempty"`
	FeatureCount    *int       `json:"feature_count,omitempty"`
	AttachmentCount *int       `json:"attachment_count,omitempty"`
	TilesBytes      *int64     `json:"tiles_bytes,omitempty"`
	DataBytes       *int64     `json:"data_bytes,omitempty"`
	Error           *string    `json:"error,omitempty"`
	Disabled        bool       `json:"disabled"`
	SnapshotAt      *time.Time `json:"snapshot_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
}

func toAdminPackView(row *adminPackRow) adminPackView {
	var scope PackScope
	_ = json.Unmarshal(row.Scope, &scope) // written by us at enqueue; a parse miss just omits bbox/classes
	return adminPackView{
		ID:              row.ID,
		Status:          row.Status,
		OwnerID:         row.RequestedBy,
		OwnerEmail:      row.Email,
		OwnerName:       row.DisplayName,
		FeatureClasses:  scope.FeatureClasses,
		Bbox:            scope.Bbox,
		FeatureCount:    row.FeatureCount,
		AttachmentCount: row.AttachmentCount,
		TilesBytes:      row.TilesBytes,
		DataBytes:       row.DataBytes,
		Error:           row.Error,
		Disabled:        row.DisabledAt != nil,
		SnapshotAt:      row.SnapshotAt,
		CreatedAt:       row.CreatedAt,
		FinishedAt:      row.FinishedAt,
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

// ---- Cell admin (migration 0131, shared packs) ---------------------------------
//
// The CRUD + build surface for ops.field_pack_cells, mounted under /field-packs/cells
// behind the same admin:field-packs gate + CSRF as the pack-management routes above.
// A cell is a shared-coverage DEFINITION; "build" enqueues a shared_base version of
// it through the normal queue. Browse/pickup on the device is a later slice.

// cellVersionView is the latest succeeded build of a cell -- enough for the catalog
// to show whether (and how big) a cell's shared base currently is.
type cellVersionView struct {
	PackID       string     `json:"pack_id"`
	Status       string     `json:"status"`
	FeatureCount *int       `json:"feature_count,omitempty"`
	DataBytes    *int64     `json:"data_bytes,omitempty"`
	TilesBytes   *int64     `json:"tiles_bytes,omitempty"`
	SnapshotAt   *time.Time `json:"snapshot_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
}

// cellView is one cell over the wire to the admin catalog.
type cellView struct {
	ID                   string           `json:"id"`
	Name                 string           `json:"name"`
	TerritoryID          *string          `json:"territory_id,omitempty"`
	Bbox                 []float64        `json:"bbox"`
	FeatureClasses       []string         `json:"feature_classes"`
	IncludeConnectivity  bool             `json:"include_connectivity"`
	IncludeRelationships bool             `json:"include_relationships"`
	IncludeFaults        bool             `json:"include_faults"`
	IncludeTags          bool             `json:"include_tags"`
	Schedule             *string          `json:"schedule,omitempty"`
	Retention            string           `json:"retention"`
	Active               bool             `json:"active"`
	CreatedAt            time.Time        `json:"created_at"`
	UpdatedAt            time.Time        `json:"updated_at"`
	LatestVersion        *cellVersionView `json:"latest_version,omitempty"`
}

func toCellView(c *Cell, latest *FieldPack) cellView {
	v := cellView{
		ID:                   c.ID,
		Name:                 c.Name,
		TerritoryID:          c.TerritoryID,
		Bbox:                 c.Bbox,
		FeatureClasses:       c.FeatureClasses,
		IncludeConnectivity:  c.IncludeConnectivity,
		IncludeRelationships: c.IncludeRelationships,
		IncludeFaults:        c.IncludeFaults,
		IncludeTags:          c.IncludeTags,
		Schedule:             c.Schedule,
		Retention:            c.Retention,
		Active:               c.Active,
		CreatedAt:            c.CreatedAt,
		UpdatedAt:            c.UpdatedAt,
	}
	if latest != nil {
		v.LatestVersion = &cellVersionView{
			PackID:       latest.ID,
			Status:       latest.Status,
			FeatureCount: latest.FeatureCount,
			DataBytes:    latest.DataBytes,
			TilesBytes:   latest.TilesBytes,
			SnapshotAt:   latest.SnapshotAt,
			CreatedAt:    latest.CreatedAt,
		}
	}
	return v
}

// cellRequest is the create/update body. Active is a pointer so a create defaults
// it to true while an update can explicitly deactivate a cell.
type cellRequest struct {
	Name                 string    `json:"name"`
	TerritoryID          *string   `json:"territory_id"`
	Bbox                 []float64 `json:"bbox"`
	FeatureClasses       []string  `json:"feature_classes"`
	IncludeConnectivity  bool      `json:"include_connectivity"`
	IncludeRelationships bool      `json:"include_relationships"`
	IncludeFaults        bool      `json:"include_faults"`
	IncludeTags          bool      `json:"include_tags"`
	Schedule             *string   `json:"schedule"`
	Retention            string    `json:"retention"`
	Active               *bool     `json:"active"`
}

func (req cellRequest) validate() string {
	if req.Name == "" {
		return "name is required"
	}
	if len(req.Bbox) != 4 {
		return "bbox must be [minX, minY, maxX, maxY] (4 numbers)"
	}
	if len(req.FeatureClasses) == 0 {
		return "at least one feature class is required"
	}
	return ""
}

func (req cellRequest) toInput() CellInput {
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	return CellInput{
		Name:                 req.Name,
		TerritoryID:          req.TerritoryID,
		Bbox:                 req.Bbox,
		FeatureClasses:       req.FeatureClasses,
		IncludeConnectivity:  req.IncludeConnectivity,
		IncludeRelationships: req.IncludeRelationships,
		IncludeFaults:        req.IncludeFaults,
		IncludeTags:          req.IncludeTags,
		Schedule:             req.Schedule,
		Retention:            req.Retention,
		Active:               active,
	}
}

func adminListCellsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cells, err := listCells(r.Context(), db)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		views := make([]cellView, len(cells))
		for i := range cells {
			latest, _ := latestCellVersion(r.Context(), db, cells[i].ID)
			views[i] = toCellView(&cells[i], latest)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"cells": views})
	}
}

func adminCreateCellHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "no session")
			return
		}
		var req cellRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		if msg := req.validate(); msg != "" {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		cell, err := createCell(r.Context(), db, req.toInput(), user.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(toCellView(cell, nil))
	}
}

func adminGetCellHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cell, err := getCell(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "cell not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		latest, _ := latestCellVersion(r.Context(), db, cell.ID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toCellView(cell, latest))
	}
}

func adminUpdateCellHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req cellRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "request body is not valid JSON")
			return
		}
		if msg := req.validate(); msg != "" {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		cell, err := updateCell(r.Context(), db, chi.URLParam(r, "id"), req.toInput())
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "cell not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		latest, _ := latestCellVersion(r.Context(), db, cell.ID)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toCellView(cell, latest))
	}
}

func adminDeleteCellHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		removed, err := deleteCell(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !removed {
			writeErr(w, http.StatusNotFound, "cell not found")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// adminBuildCellHandler enqueues a shared_base build (a new cell version) for the
// cell, through the normal pack queue. Returns the build's pack id (202) -- the
// admin polls the pack list / the cell's latest_version for its status.
func adminBuildCellHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "no session")
			return
		}
		cell, err := getCell(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "cell not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		packID, err := enqueueCellBuild(r.Context(), db, cell, user.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"pack_id": packID})
	}
}

// adminReapCellHandler applies a cell's retention policy on demand (S6): it reaps the
// old versions no crew still holds, returning how many went. keep_n reaps the
// eligible; manual/pinned report 0 (the admin deletes those by hand via the per-pack
// DELETE). The background sweep in cmd/fieldsync runs the same reapCellVersions on a
// timer; this is the admin's "run it now" / testing hook.
func adminReapCellHandler(db *bun.DB, store *export.ObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cell, err := getCell(r.Context(), db, chi.URLParam(r, "id"))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "cell not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		reaped, err := reapCellVersions(r.Context(), db, store, cell)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"reaped":    reaped,
			"retention": cell.Retention,
		})
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
