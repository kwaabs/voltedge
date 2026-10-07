package fieldpack

// The DEVICE-facing shared-cell surface (S2, docs/shared-field-packs-design.md).
// A crew browses the cells their access COVERS and picks one up -- downloading the
// shared, pre-built base an admin built overnight instead of waiting on a personal
// build. Two routes, both behind the existing bearer + features:read gate:
//
//   GET  /cells            the cells this crew may pull, each with its latest build
//   POST /cells/{id}/pickup gate -> record the pickup in the ledger -> the base manifest
//
// Pickup gating is Option A from the design (no reduced fallback, no leak): a crew
// may pull a cell ONLY if their entitlements COVER it -- territory access (Phase 58)
// AND the *:read permission for every extra the base carries. A cell the crew can't
// fully cover is hidden from the browse list and refused (403) on a direct pickup.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/uptrace/bun"
)

// cellCoveredBy reports whether user's entitlements COVER cell -- the pickup gate.
// It returns (true, "") when covered, or (false, reason) naming the first missing
// entitlement (a *:read permission, or "territory") so the device can tell the crew
// WHY a cell is unavailable. The rule is deliberately strict (design Option A):
//
//   - Content: the crew must hold the *:read permission for every extra the base
//     carries (connectivity/relationships/faults/tags). features:read is already a
//     precondition -- the route group requires it -- so it isn't re-checked here.
//   - Territory (Phase 58, only when enforcement is on and the user is scoped): the
//     cell's territory must be inside the user's accessible set. AllowedTerritories is
//     already subtree-expanded, so membership of the cell's territory implies the whole
//     cell subtree is covered. A cell with NO territory (the "public baseline") applies
//     no territory filter to its base, so its features could span anywhere -- only an
//     unscoped user (or enforcement-off) may pull it, never a scoped crew (fail closed).
func cellCoveredBy(ctx context.Context, db bun.IDB, user *auth.SessionUser, cell *Cell) (bool, string, error) {
	if cell.IncludeConnectivity && !user.Has("connectivity:read") {
		return false, "connectivity:read", nil
	}
	if cell.IncludeRelationships && !user.Has("relationships:read") {
		return false, "relationships:read", nil
	}
	if cell.IncludeFaults && !user.Has("faults:read") {
		return false, "faults:read", nil
	}
	if cell.IncludeTags && !user.Has("tags:read") {
		return false, "tags:read", nil
	}

	enabled, err := territory.EnforcementEnabled(ctx, db)
	if err != nil {
		return false, "", err
	}
	if !enabled || user.Unscoped {
		return true, "", nil
	}
	// Enforcement on and the user is scoped: the base must be territory-bounded and
	// that territory must be one the user can reach.
	if cell.TerritoryID == nil || *cell.TerritoryID == "" {
		return false, "territory", nil
	}
	ids, _ := user.TerritoryFilter()
	if slices.Contains(ids, *cell.TerritoryID) {
		return true, "", nil
	}
	return false, "territory", nil
}

// deviceCellView is one browsable cell over the wire. Available is true (and Version
// populated) only when the cell has at least one succeeded build the crew can pull;
// an active-but-never-built cell lists with Available=false so the crew sees it is
// coming but can't yet download it.
type deviceCellView struct {
	ID                   string                 `json:"id"`
	Name                 string                 `json:"name"`
	Bbox                 []float64              `json:"bbox"`
	FeatureClasses       []string               `json:"feature_classes"`
	IncludeConnectivity  bool                   `json:"include_connectivity"`
	IncludeRelationships bool                   `json:"include_relationships"`
	IncludeFaults        bool                   `json:"include_faults"`
	IncludeTags          bool                   `json:"include_tags"`
	Available            bool                   `json:"available"`
	Version              *deviceCellVersionView `json:"version,omitempty"`
}

// deviceCellVersionView summarizes the latest succeeded build of a cell -- enough for
// the crew to judge size/freshness before pulling. The manifest (presigned URLs) is
// minted only on pickup, not here, so the browse list stays cheap.
type deviceCellVersionView struct {
	PackID       string     `json:"pack_id"`
	FeatureCount *int       `json:"feature_count,omitempty"`
	TilesBytes   *int64     `json:"tiles_bytes,omitempty"`
	DataBytes    *int64     `json:"data_bytes,omitempty"`
	SnapshotAt   *time.Time `json:"snapshot_at,omitempty"`
	BuiltAt      *time.Time `json:"built_at,omitempty"`
}

func toDeviceCellView(cell *Cell, version *FieldPack) deviceCellView {
	v := deviceCellView{
		ID:                   cell.ID,
		Name:                 cell.Name,
		Bbox:                 cell.Bbox,
		FeatureClasses:       cell.FeatureClasses,
		IncludeConnectivity:  cell.IncludeConnectivity,
		IncludeRelationships: cell.IncludeRelationships,
		IncludeFaults:        cell.IncludeFaults,
		IncludeTags:          cell.IncludeTags,
	}
	if version != nil {
		v.Available = true
		v.Version = &deviceCellVersionView{
			PackID:       version.ID,
			FeatureCount: version.FeatureCount,
			TilesBytes:   version.TilesBytes,
			DataBytes:    version.DataBytes,
			SnapshotAt:   version.SnapshotAt,
			BuiltAt:      version.FinishedAt,
		}
	}
	return v
}

// deviceListCellsHandler lists the ACTIVE cells this crew's access covers, each with
// its latest succeeded build. Cells the crew can't fully cover (territory or a missing
// *:read) are omitted entirely -- the browse list never advertises a pack the crew
// would be refused on pickup.
func deviceListCellsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			unauthorized(w)
			return
		}
		cells, err := listCells(r.Context(), db)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		views := make([]deviceCellView, 0, len(cells))
		for i := range cells {
			cell := &cells[i]
			if !cell.Active {
				continue
			}
			covered, _, err := cellCoveredBy(r.Context(), db, user, cell)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if !covered {
				continue
			}
			version, err := latestCellVersion(r.Context(), db, cell.ID)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			views = append(views, toDeviceCellView(cell, version))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"cells": views})
	}
}

// pickupRequest is the POST /cells/{id}/pickup body: the optional device id so one
// crew member's two tablets are distinguishable in the usage view.
type pickupRequest struct {
	DeviceID string `json:"device_id"`
}

// deviceCellPickupHandler is the gated download of a shared cell base. It re-checks
// coverage (the browse list hid uncovered cells, but a direct POST must still be
// enforced), records the pickup in the ledger, and returns the base manifest -- the
// same two-presigned-URL envelope a personal pack download returns, so the device
// downloads a shared base through the exact code path it already has for packs.
func deviceCellPickupHandler(db *bun.DB, store *export.ObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			unauthorized(w)
			return
		}
		id := chi.URLParam(r, "id")

		cell, err := getCell(r.Context(), db, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "cell not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !cell.Active {
			writeErr(w, http.StatusConflict, "this cell is not active")
			return
		}

		covered, missing, err := cellCoveredBy(r.Context(), db, user, cell)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !covered {
			if missing == "territory" {
				writeErr(w, http.StatusForbidden, "your territory access does not cover this cell")
				return
			}
			writeErr(w, http.StatusForbidden, "your access does not cover this cell (missing "+missing+")")
			return
		}

		version, err := latestCellVersion(r.Context(), db, cell.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if version == nil {
			writeErr(w, http.StatusConflict, "this cell has no build available yet")
			return
		}

		var req pickupRequest
		if r.Body != nil {
			// A pickup body is optional (device_id only); a malformed/empty body is not
			// fatal -- the pickup proceeds with no device id rather than 400.
			_ = json.NewDecoder(r.Body).Decode(&req)
		}

		if err := recordDownload(r.Context(), db, user.ID, version.ID, &cell.ID, req.DeviceID); err != nil {
			writeErr(w, http.StatusInternalServerError, "could not record the pickup: "+err.Error())
			return
		}

		manifest, err := buildManifest(r.Context(), store, version)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not prepare pack download: "+err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"pack_id":  version.ID,
			"cell_id":  cell.ID,
			"manifest": manifest,
		})
	}
}

// deviceCellOverlayHandler serves the crew member's PERSONAL overlay over a cell
// (S2b): their own work board + their own private drafts, as live JSON the device
// caches on pickup and refreshes each sync. Gated exactly like pickup -- the overlay
// is only ever served for a cell the crew may pull -- though its contents are the
// caller's OWN data (no cross-user leak). The work board rides only when the caller
// holds wam:read; drafts need only the route's features:read.
func deviceCellOverlayHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			unauthorized(w)
			return
		}
		id := chi.URLParam(r, "id")

		cell, err := getCell(r.Context(), db, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "cell not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !cell.Active {
			writeErr(w, http.StatusConflict, "this cell is not active")
			return
		}

		covered, missing, err := cellCoveredBy(r.Context(), db, user, cell)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !covered {
			if missing == "territory" {
				writeErr(w, http.StatusForbidden, "your territory access does not cover this cell")
				return
			}
			writeErr(w, http.StatusForbidden, "your access does not cover this cell (missing "+missing+")")
			return
		}

		overlay, err := buildOverlay(r.Context(), db, cell, user.ID, user.Has("wam:read"))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not build the overlay: "+err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(overlay)
	}
}
