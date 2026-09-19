package fieldpack

// The cmd/fieldsync HTTP surface (Phase 21 Slice 1, docs/phase-21-field-tool.md).
//
// A device asks for an offline region pack and polls until it can download it.
// Every route authenticates an iam.api_tokens bearer (the Phase 18 trust
// boundary, auth.ResolveAPIToken) and runs under the token owner's RBAC -- there
// is no new authorization surface. This slice serves the pack lifecycle:
//
//   POST /packs             enqueue a build for a bbox + class set -> {pack_id}
//   GET  /packs             the caller's recent packs
//   GET  /packs/{id}        poll status; on success, the manifest (2 presigned URLs)
//   POST /packs/{id}/cancel cancel the caller's own queued/running build
//   GET  /feature-classes   the packable class catalog (for the device's pick-list)
//   GET  /sync/status       poll the apply outcome of the caller's own ops (features:read)
//   GET  /features/{code}/{id}/gps-captures  a feature's GNSS survey quality (features:read)
//   POST /sync              capture a device's offline-edit outbox (features:write)
//
// The pack lifecycle + the sync status pull sit behind features:read; /sync is a
// write, so it sits behind features:write (see sync.go for the receive/apply split
// and idempotency design, and syncworker.go for the drain-to-production worker).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/attachment"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/uptrace/bun"
)

// Routes returns the fieldsync HTTP router. /healthz is unauthenticated (for
// liveness probes); everything under /packs sits behind the bearer gate and the
// features:read permission -- the same base read gate the export mount uses.
func Routes(db *bun.DB, store *export.ObjectStore) http.Handler {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	r.Group(func(r chi.Router) {
		r.Use(bearerAuth(db))
		r.Use(requirePerm("features:read"))
		r.Post("/packs", enqueuePackHandler(db))
		r.Get("/packs", listPacksHandler(db))
		r.Get("/packs/{id}", getPackHandler(db, store))
		r.Post("/packs/{id}/cancel", cancelPackHandler(db))
		// The class catalog the device's "new pack" form picks from, so a
		// user checks boxes instead of typing UPPERCASE codes (classes.go).
		r.Get("/feature-classes", featureClassesHandler(db))
		// The sync status pull is a read (a device reconciling the apply outcome of
		// its own already-pushed ops), so it sits with the reads behind features:read
		// -- separate from the POST /sync write below (Phase 23 Slice 1).
		r.Get("/sync/status", statusHandler(db))
		// A feature's GNSS survey-quality captures (Phase gnss-quality Slice 3b):
		// server-only rows the device pulls on demand for the detail screen. Shares
		// crud.ListFeatureGpsCaptures with the web route, so the JSON matches
		// (gpscaptures.go). A plain read, so features:read.
		r.Get("/features/{code}/{id}/gps-captures", gpsCapturesHandler(db))
	})

	// The edit-replay endpoint is a write, so it sits behind features:write --
	// a device that can only pull packs (features:read) can't push edits back.
	r.Group(func(r chi.Router) {
		r.Use(bearerAuth(db))
		r.Use(requirePerm("features:write"))
		r.Post("/sync", syncHandler(db))
	})

	// The attachments surface (Phase 22 S4): the device lists a feature's images,
	// uploads new photos (register -> presigned PUT -> commit), and fetches
	// full-resolution originals on demand when online -- the offline preview rides
	// the pack (builder v6). Shares the exact handlers the web uses (no CSRF here --
	// a bearer request has no cookies), gated method-aware: GET -> attachments:read,
	// POST/DELETE -> attachments:write.
	r.Group(func(r chi.Router) {
		r.Use(bearerAuth(db))
		r.Use(requirePermMethod(auth.PermAttachmentsRead, auth.PermAttachmentsWrite))
		r.Mount("/attachments", attachment.BearerRoutes(db, store))
	})
	return r
}

// bearerAuth resolves the Authorization: Bearer iam.api_tokens credential and
// attaches the owning user to the request context (as the session middleware
// would), so downstream handlers read it via auth.UserFrom and the engine
// attributes writes via auth.ActorID. A missing/invalid token is 401.
func bearerAuth(db *bun.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
				unauthorized(w)
				return
			}
			raw := strings.TrimSpace(h[len(prefix):])
			if raw == "" {
				unauthorized(w)
				return
			}
			user, err := auth.ResolveAPIToken(r.Context(), db, raw)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "authentication error")
				return
			}
			if user == nil {
				unauthorized(w)
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), user)))
		})
	}
}

// requirePerm gates a route on a single RBAC permission held by the bearer's user.
func requirePerm(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFrom(r.Context())
			if !ok {
				unauthorized(w)
				return
			}
			if !user.Has(perm) {
				writeErr(w, http.StatusForbidden, "this token's user lacks "+perm)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requirePermMethod is the method-aware sibling of requirePerm: a safe method
// (GET/HEAD/OPTIONS) needs readPerm, anything mutating needs writePerm. It's the
// bearer-side equivalent of auth.RequirePermission (which the cookie-authenticated
// web mount uses), for a sub-router that mixes reads and writes.
func requirePermMethod(readPerm, writePerm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFrom(r.Context())
			if !ok {
				unauthorized(w)
				return
			}
			perm := writePerm
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				perm = readPerm
			}
			if !user.Has(perm) {
				writeErr(w, http.StatusForbidden, "this token's user lacks "+perm)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// packRequest is the POST /packs body: a bbox (required -- a pack is always
// bounded) + the class codes to include, optionally clipped to territories.
type packRequest struct {
	Bbox           []float64 `json:"bbox"`
	FeatureClasses []string  `json:"feature_classes"`
	Territories    []string  `json:"territories"`
}

func enqueuePackHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			unauthorized(w)
			return
		}

		var req packRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("request body is not valid JSON: %v", err))
			return
		}
		if _, err := scopeBBox(req.Bbox); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if len(req.FeatureClasses) == 0 {
			writeErr(w, http.StatusBadRequest, "no feature classes selected for the pack")
			return
		}
		if status, msg := validatePackClasses(r.Context(), db, req.FeatureClasses); status != 0 {
			writeErr(w, status, msg)
			return
		}

		territoryIDs, err := resolveTerritories(r.Context(), db, req.Territories)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		scope := PackScope{
			Bbox:           req.Bbox,
			FeatureClasses: req.FeatureClasses,
			TerritoryIDs:   territoryIDs,
			// Freeze the graph-inclusion decision here, from the requester's RBAC:
			// the detached build worker holds no SessionUser to re-check.
			IncludeConnectivity:  user.Has("connectivity:read"),
			IncludeRelationships: user.Has("relationships:read"),
			// Phase 59 offline WAM: ship the field worker's defects/work-orders/
			// inspection templates only when they may read WAM.
			IncludeWAM: user.Has("wam:read"),
			// Offline faults: ship the open faults on the packed assets only when
			// they may read faults -- the fault-response crew's live board.
			IncludeFaults: user.Has("faults:read"),
		}
		id, err := enqueuePack(r.Context(), db, user.ID, scope)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"pack_id": id})
	}
}

func listPacksHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			unauthorized(w)
			return
		}
		packs, err := listPacks(r.Context(), db, user.ID, 50)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		views := make([]packView, len(packs))
		for i := range packs {
			views[i] = toPackView(&packs[i])
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"packs": views})
	}
}

func getPackHandler(db *bun.DB, store *export.ObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			unauthorized(w)
			return
		}
		pack, err := getPack(r.Context(), db, chi.URLParam(r, "id"), user.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "pack not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		view := toPackView(pack)
		// On success, mint the manifest: two short-lived presigned URLs (minted at
		// poll time so their TTL is spent on the download) + the scope the device
		// needs to render. A minting failure is surfaced rather than hidden.
		if pack.Status == "succeeded" && pack.TilesObjectKey != nil && pack.DataObjectKey != nil {
			m, err := buildManifest(r.Context(), store, pack)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, "could not prepare pack download: "+err.Error())
				return
			}
			view.Manifest = m
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(view)
	}
}

func cancelPackHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			unauthorized(w)
			return
		}
		id := chi.URLParam(r, "id")

		cancelled, err := cancelPack(r.Context(), db, id, user.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		pack, err := getPack(r.Context(), db, id, user.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "pack not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Nothing flipped and it isn't cancelled -> it already finished before the
		// cancel could land (the worker won the race).
		if !cancelled && pack.Status != "cancelled" {
			writeErr(w, http.StatusConflict, fmt.Sprintf("pack already %s; cannot cancel", pack.Status))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toPackView(pack))
	}
}

// packView is one build over the wire; Manifest is present only on a succeeded
// pack (see getPackHandler).
type packView struct {
	ID           string        `json:"id"`
	Status       string        `json:"status"`
	FeatureCount *int          `json:"feature_count,omitempty"`
	TilesBytes   *int64        `json:"tiles_bytes,omitempty"`
	DataBytes    *int64        `json:"data_bytes,omitempty"`
	Error        *string       `json:"error,omitempty"`
	SnapshotAt   *time.Time    `json:"snapshot_at,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	FinishedAt   *time.Time    `json:"finished_at,omitempty"`
	Manifest     *manifestView `json:"manifest,omitempty"`
}

// manifestView is the download envelope for a succeeded pack: the two presigned
// artifact URLs plus the scope facts the device renders from.
type manifestView struct {
	SchemaVersion        int        `json:"schema_version"`
	TilesURL             string     `json:"tiles_url"`
	DataURL              string     `json:"data_url"`
	FeatureCount         *int       `json:"feature_count,omitempty"`
	Bbox                 []float64  `json:"bbox"`
	SnapshotAt           *time.Time `json:"snapshot_at,omitempty"`
	IncludeConnectivity  bool       `json:"include_connectivity"`
	IncludeRelationships bool       `json:"include_relationships"`
	IncludeWAM           bool       `json:"include_wam"`
	IncludeFaults        bool       `json:"include_faults"`
}

func toPackView(p *FieldPack) packView {
	return packView{
		ID:           p.ID,
		Status:       p.Status,
		FeatureCount: p.FeatureCount,
		TilesBytes:   p.TilesBytes,
		DataBytes:    p.DataBytes,
		Error:        p.Error,
		SnapshotAt:   p.SnapshotAt,
		CreatedAt:    p.CreatedAt,
		FinishedAt:   p.FinishedAt,
	}
}

// buildManifest mints the two presigned download URLs and folds in the pack's
// scope (bbox + graph-inclusion flags), which live in the scope jsonb.
func buildManifest(ctx context.Context, store *export.ObjectStore, p *FieldPack) (*manifestView, error) {
	tilesURL, err := store.PresignGet(ctx, *p.TilesObjectKey, "tiles.mbtiles")
	if err != nil {
		return nil, err
	}
	dataURL, err := store.PresignGet(ctx, *p.DataObjectKey, "data.sqlite")
	if err != nil {
		return nil, err
	}
	var scope PackScope
	_ = json.Unmarshal(p.Scope, &scope) // scope was written by us at enqueue; a parse miss just omits bbox/flags
	return &manifestView{
		SchemaVersion:        packSchemaVersion,
		TilesURL:             tilesURL,
		DataURL:              dataURL,
		FeatureCount:         p.FeatureCount,
		Bbox:                 scope.Bbox,
		SnapshotAt:           p.SnapshotAt,
		IncludeConnectivity:  scope.IncludeConnectivity,
		IncludeRelationships: scope.IncludeRelationships,
		IncludeWAM:           scope.IncludeWAM,
		IncludeFaults:        scope.IncludeFaults,
	}, nil
}

// validatePackClasses cheaply checks every requested class exists and has
// geometry -- enough to reject a bad request synchronously at enqueue. Mirrors
// export.validateExportClasses (unexported there).
func validatePackClasses(ctx context.Context, db *bun.DB, codes []string) (int, string) {
	for _, code := range codes {
		loaded, err := metadata.Load(ctx, db, code)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return http.StatusNotFound, fmt.Sprintf("feature class %q does not exist", code)
			}
			return http.StatusInternalServerError, err.Error()
		}
		if loaded.FeatureClass.GeometryType == "none" {
			return http.StatusBadRequest, fmt.Sprintf("feature class %q has no geometry, so it can't be packed", code)
		}
	}
	return 0, ""
}

// resolveTerritories turns requested territory ids into the expanded descendant
// set the read path filters on: nil = no filter, non-nil empty = filter to
// nothing, populated = the descendant set. Mirrors export.resolveTerritories.
func resolveTerritories(ctx context.Context, db *bun.DB, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	expanded, err := territory.DescendantIDs(ctx, db, ids)
	if err != nil {
		return nil, err
	}
	if expanded == nil {
		expanded = []string{}
	}
	return expanded, nil
}

func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="voltedge-fieldsync"`)
	writeErr(w, http.StatusUnauthorized, "missing or invalid bearer token")
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
