package export

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/drafts"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/kwaabs/voltedge/api/internal/territory"
	"github.com/uptrace/bun"
)

// exportRequest is the POST /export body. FeatureClasses are class codes (the
// same codes the CRUD endpoints key on). Scope is expressed by two optional,
// independent filters: Bbox is a [minX, minY, maxX, maxY] in EPSG:4326 that
// clips to the map's current viewport, and Territories is a set of territory
// ids that clips to those territories (and, expanded server-side, everything
// beneath them). With neither, the export is the whole dataset. The client
// sends at most one today, but both compose as an AND if given.
type exportRequest struct {
	Format         string    `json:"format"`
	FeatureClasses []string  `json:"feature_classes"`
	Bbox           []float64 `json:"bbox"`
	Territories    []string  `json:"territories"`
	// CimProfile names the metadata.cim_profiles row for a CIM export; empty
	// selects the is_default profile. Ignored by the other formats.
	CimProfile string `json:"cim_profile"`
	// TargetSRID (export reprojection engine) reprojects the output geometry to
	// this SRID via PostGIS ST_Transform. nil/absent keeps the stored 4326. Only
	// the formats that can declare a CRS accept it (shapefile, DXF); KMZ rejects
	// it (KML is WGS84-only) and CIM does not support it yet. Validated against
	// spatial_ref_sys at enqueue.
	TargetSRID *int `json:"target_srid"`
}

// format describes one supported output: its MIME type, file extension, and its
// encoder. A format encodes one of two ways (runExport dispatches on which is
// set):
//   - writeStream: the cursor path (KMZ/DXF), fed StreamLayers whose features
//     are pulled one at a time, so the read is uncapped and never materialized
//     (Phase 17 Slice 3d). Preferred where the encoder can stream.
//   - write: the materialized path (shapefile), given fully-read []Layer --
//     shapefile can't stream (its header holds offsets computed from a full
//     pass), so it stays capped at maxExportFeatures.
//
// Both nil marks a format handled on its own path in runExport (CIM, whose
// encoder needs the declarative mapping + whole-graph topology the generic
// signatures don't carry). The entry still supplies the content-type/extension.
type format struct {
	contentType string
	ext         string
	// write is the materialized encoder (shapefile). prjWKT is the CRS WKT for
	// the output (a shapefile .prj); the streaming/CIM formats don't take it.
	write       func(w io.Writer, docName string, layers []Layer, prjWKT string) error
	writeStream func(w io.Writer, docName string, layers []StreamLayer) (int, error)
}

var formats = map[string]format{
	"kmz": {contentType: "application/vnd.google-earth.kmz", ext: "kmz", writeStream: writeKMZStream},
	"shp": {contentType: "application/zip", ext: "zip", write: writeShapefiles},
	"dxf": {contentType: "application/dxf", ext: "dxf", writeStream: writeDXFStream},
	// CIM RDF/XML (Phase 16) -- both encoders nil; runExport routes it to
	// writeCIM with the loaded mapping. See docs/phase-16-cim-export.md.
	"cim": {contentType: "application/rdf+xml", ext: "xml"},
}

// Routes returns the export router, mounted at /export behind the features:read
// gate. As of Phase 17 (docs/phase-17-large-export.md) an export is an async
// job, not a streamed download: POST enqueues and returns a job id, and the
// caller polls GET /jobs/{id} until it can download. Every route is a read of
// the caller's own data, so the single features:read gate on the mount covers
// them all; per-user scoping (a caller only sees their own jobs) is enforced in
// the handlers.
func Routes(db *bun.DB, store *ObjectStore) chi.Router {
	r := chi.NewRouter()
	r.Post("/", enqueueHandler(db))
	r.Get("/jobs", listJobsHandler(db))
	r.Get("/jobs/{id}", getJobHandler(db))
	r.Post("/jobs/{id}/cancel", cancelHandler(db))
	r.Get("/jobs/{id}/download", downloadHandler(db, store))
	return r
}

// jobView is one export job over the wire. The result fields (feature_count,
// byte_size) and error are omitted until they exist; download_url is present
// only on a succeeded job. It points at this API's /download route (owner
// -checked, stable across polls), which 302-redirects to a freshly minted
// RustFS presigned URL at click time -- so the short TTL starts when the user
// actually downloads, not when the poll observed success.
type jobView struct {
	ID           string     `json:"id"`
	Status       string     `json:"status"`
	Format       string     `json:"format"`
	FeatureCount *int       `json:"feature_count,omitempty"`
	ByteSize     *int64     `json:"byte_size,omitempty"`
	Error        *string    `json:"error,omitempty"`
	DownloadURL  string     `json:"download_url,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

func toJobView(j *ExportJob) jobView {
	v := jobView{
		ID:           j.ID,
		Status:       j.Status,
		Format:       j.Format,
		FeatureCount: j.FeatureCount,
		ByteSize:     j.ByteSize,
		Error:        j.Error,
		CreatedAt:    j.CreatedAt,
		FinishedAt:   j.FinishedAt,
	}
	if j.Status == "succeeded" {
		v.DownloadURL = "/export/jobs/" + j.ID + "/download"
	}
	return v
}

// enqueueHandler validates the request, resolves its scope (the same authz
// resolution the synchronous path did), and inserts a queued job -- returning
// 202 + {job_id} with no bytes. Everything that can fail cleanly (bad format,
// missing class, unconfigured CIM mapping) is checked here so the caller gets a
// synchronous 4xx; anything that only fails at encode time surfaces later as a
// failed job.
func enqueueHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}

		var req exportRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("request body is not valid JSON: %v", err))
			return
		}

		if _, ok := formats[req.Format]; !ok {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("unsupported export format %q", req.Format))
			return
		}
		if len(req.FeatureClasses) == 0 {
			writeErr(w, http.StatusBadRequest, "no feature classes selected for export")
			return
		}
		if _, err := parseBBox(req.Bbox); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		// Export reprojection: a requested target CRS must be a format that can
		// carry one and a SRID PostGIS actually knows. Both are checked here so a
		// bad CRS is a synchronous 400, never a job that fails at encode time.
		if req.TargetSRID != nil {
			if !reprojectableFormats[req.Format] {
				writeErr(w, http.StatusBadRequest, fmt.Sprintf("export format %q cannot be reprojected: KMZ is EPSG:4326-only", req.Format))
				return
			}
			ok, err := sridExists(r.Context(), db, *req.TargetSRID)
			if err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
			if !ok {
				writeErr(w, http.StatusBadRequest, fmt.Sprintf("unknown target SRID %d (not present in PostGIS spatial_ref_sys)", *req.TargetSRID))
				return
			}
		}
		if status, msg := validateExportClasses(r.Context(), db, req.FeatureClasses); status != 0 {
			writeErr(w, status, msg)
			return
		}

		// Expand the selected territories to their subtrees (see
		// territory.DescendantIDs): nil = no territory filter, non-nil empty =
		// filter to nothing, populated = the descendant set. The expanded set is
		// snapshotted into the job scope so the worker never re-resolves it.
		territoryIDs, err := resolveTerritories(r.Context(), db, req.Territories)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Phase 58 spatial enforcement: constrain the export to the acting
		// user's territory scope. A no-op when enforcement is off or the user
		// bypasses scoping; otherwise the requested set is intersected with the
		// user's effective territories (or, if they requested no filter,
		// replaced by it) so a scoped user can never export beyond their scope.
		territoryIDs, err = enforceExportScope(r.Context(), db, user, territoryIDs)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		// CIM: fail synchronously if the declarative mapping isn't seeded, so the
		// caller learns now rather than via a failed job. The runner reloads it.
		if req.Format == "cim" {
			if _, err := metadata.LoadCIMMapping(r.Context(), db, req.CimProfile); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeErr(w, http.StatusBadRequest, "CIM export is not configured: "+err.Error())
					return
				}
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
		}

		// Private-drafts (Phase 61): resolve the requester's draft-visibility author
		// set while their session is on the context; the detached runner can't. nil
		// when the filter doesn't apply (gate off / unscoped / break-glass).
		draftAuthors, _, err := drafts.VisibleAuthors(r.Context(), db)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		scope := ExportScope{
			Format:         req.Format,
			FeatureClasses: req.FeatureClasses,
			Bbox:           req.Bbox,
			TerritoryIDs:   territoryIDs,
			CimProfile:     req.CimProfile,
			DraftAuthors:   draftAuthors,
			TargetSRID:     req.TargetSRID,
		}
		id, err := enqueueExport(r.Context(), db, user.ID, scope)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"job_id": id})
	}
}

// listJobsHandler returns the caller's most recent export jobs, newest first.
func listJobsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		jobs, err := listExportJobs(r.Context(), db, user.ID, 50)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		views := make([]jobView, len(jobs))
		for i := range jobs {
			views[i] = toJobView(&jobs[i])
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jobs": views})
	}
}

// getJobHandler returns one of the caller's jobs by id (404 for a job that
// doesn't exist or isn't theirs -- no cross-user peeking).
func getJobHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		job, err := getExportJob(r.Context(), db, chi.URLParam(r, "id"), user.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "export job not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toJobView(job))
	}
}

// cancelHandler cancels one of the caller's own pending (queued/running) jobs
// and returns its resulting state. A queued job is cancelled immediately; a
// running job is flipped to cancelled and its worker aborts on the next
// heartbeat (any artifact it already uploaded is reaped later by the retention
// sweep). 404 for a job that isn't the caller's; 409 for one that already
// finished (succeeded/failed) before the cancel could land.
func cancelHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		id := chi.URLParam(r, "id")

		cancelled, err := cancelExport(r.Context(), db, id, user.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Report the resulting state. getExportJob is owner-scoped, so a job that
		// isn't the caller's reads as 404 here exactly as it does on GET.
		job, err := getExportJob(r.Context(), db, id, user.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "export job not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		// Nothing flipped and it isn't cancelled -> it had already finished before
		// the cancel could land (the worker won the race). Report the conflict.
		if !cancelled && job.Status != "cancelled" {
			writeErr(w, http.StatusConflict, fmt.Sprintf("export already %s; cannot cancel", job.Status))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(toJobView(job))
	}
}

// downloadHandler redirects to a short-lived RustFS presigned URL for a
// succeeded job's artifact. The API mints the URL (owner scoping inherited from
// getExportJob) but never proxies the bytes -- the browser fetches them from
// RustFS directly, keeping a multi-gigabyte download off the API hot path. The
// URL is minted here, at click time, so its TTL is spent on the download rather
// than on however long the caller polled before clicking.
func downloadHandler(db *bun.DB, store *ObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		job, err := getExportJob(r.Context(), db, chi.URLParam(r, "id"), user.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "export job not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		if job.Status != "succeeded" || job.ObjectKey == nil {
			writeErr(w, http.StatusConflict, "export is not ready for download")
			return
		}

		spec := formats[job.Format]
		filename := "voltedge-export." + spec.ext
		signed, err := store.PresignGet(r.Context(), *job.ObjectKey, filename)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "could not prepare download: "+err.Error())
			return
		}
		http.Redirect(w, r, signed, http.StatusFound)
	}
}

// reprojectableFormats are the export formats whose output can declare a
// coordinate system, so a target SRID is meaningful. Shapefile stamps its .prj,
// DXF plots the reprojected coordinates verbatim, and CIM writes its GL
// PositionPoints in the target CRS and declares it on the CoordinateSystem
// (crsUrn). KMZ is excluded: KML is spec-locked to EPSG:4326 lon/lat.
var reprojectableFormats = map[string]bool{"shp": true, "dxf": true, "cim": true}

// sridExists reports whether PostGIS knows the SRID (it has a spatial_ref_sys
// row), which is the precondition for ST_Transform to it and for reading its
// WKT into a shapefile .prj. Unqualified table name resolves via search_path,
// wherever PostGIS is installed -- the same assumption every ST_* call here makes.
func sridExists(ctx context.Context, db *bun.DB, srid int) (bool, error) {
	var exists bool
	if err := db.NewRaw("SELECT EXISTS (SELECT 1 FROM spatial_ref_sys WHERE srid = ?)", srid).Scan(ctx, &exists); err != nil {
		return false, fmt.Errorf("export: validating target SRID: %w", err)
	}
	return exists, nil
}

// validateExportClasses cheaply checks every requested class exists and has
// geometry -- enough to reject a bad request synchronously at enqueue without
// the full feature read the runner does. Returns (0, "") when all pass, else an
// HTTP status + message.
func validateExportClasses(ctx context.Context, db *bun.DB, codes []string) (int, string) {
	for _, code := range codes {
		loaded, err := metadata.Load(ctx, db, code)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return http.StatusNotFound, fmt.Sprintf("feature class %q does not exist", code)
			}
			return http.StatusInternalServerError, err.Error()
		}
		if loaded.FeatureClass.GeometryType == "none" {
			return http.StatusBadRequest, fmt.Sprintf("feature class %q has no geometry, so it can't be exported", code)
		}
	}
	return 0, ""
}

// resolveTerritories turns the requested territory ids into the expanded
// descendant set the read path filters on. No requested territories yields a
// nil slice (no territory filter); requested-but-unresolvable ids yield a
// non-nil empty slice (filter to nothing), never a silent fall-back to the
// whole dataset.
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

// enforceExportScope constrains an export's territory scope to the acting
// user's spatial access (Phase 58). It returns requested unchanged when
// enforcement is off, there is no user, or the user bypasses scoping
// (territories:unscoped). Otherwise:
//
//   - requested == nil (the caller asked for no territory filter) is replaced
//     by the user's whole effective set, so an unfiltered scoped export yields
//     exactly what the user may see -- never everything;
//   - a non-nil requested set is intersected with the user's effective set, so
//     asking for territories outside one's scope silently drops them rather
//     than exporting features the user can't otherwise read.
//
// Both the user's effective set (resolved at session load) and requested (from
// resolveTerritories) are already expanded to subtrees, so intersecting the two
// flat id lists is correct. An empty result means "match nothing" (fail closed),
// which crud.ExportFeatures honors.
func enforceExportScope(ctx context.Context, db *bun.DB, user *auth.SessionUser, requested []string) ([]string, error) {
	enforced, err := territory.EnforcementEnabled(ctx, db)
	if err != nil {
		return nil, err
	}
	if !enforced || user == nil {
		return requested, nil
	}
	allowed, unscoped := user.TerritoryFilter()
	if unscoped {
		return requested, nil
	}
	if requested == nil {
		if allowed == nil {
			return []string{}, nil
		}
		return allowed, nil
	}
	allowSet := make(map[string]struct{}, len(allowed))
	for _, id := range allowed {
		allowSet[id] = struct{}{}
	}
	out := make([]string, 0, len(requested))
	for _, id := range requested {
		if _, ok := allowSet[id]; ok {
			out = append(out, id)
		}
	}
	return out, nil
}

// gatherLayers loads and reads every requested class. On success it returns
// the layers and a zero status; on failure it returns a nil slice plus the
// HTTP status and message the handler should send (before any file bytes).
func gatherLayers(ctx context.Context, db bun.IDB, codes []string, bbox *crud.ExportBBox, targetSRID *int, territoryIDs, draftAuthors []string) ([]Layer, int, string) {
	layers := make([]Layer, 0, len(codes))
	for _, code := range codes {
		loaded, err := metadata.Load(ctx, db, code)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, http.StatusNotFound, fmt.Sprintf("feature class %q does not exist", code)
			}
			return nil, http.StatusInternalServerError, err.Error()
		}
		if loaded.FeatureClass.GeometryType == "none" {
			return nil, http.StatusBadRequest, fmt.Sprintf("feature class %q has no geometry, so it can't be exported", code)
		}

		rows, err := crud.ExportFeatures(ctx, db, loaded, bbox, targetSRID, territoryIDs, draftAuthors)
		if err != nil {
			if crud.IsNotFoundError(err) {
				return nil, http.StatusNotFound, err.Error()
			}
			return nil, http.StatusInternalServerError, err.Error()
		}

		features := make([]Feature, 0, len(rows))
		for _, row := range rows {
			f, err := featureFrom(row)
			if err != nil {
				return nil, http.StatusInternalServerError, err.Error()
			}
			features = append(features, f)
		}

		fields := make([]AttributeField, len(loaded.Fields))
		for i, f := range loaded.Fields {
			fields[i] = AttributeField{Name: f.Name, DataType: f.DataType}
		}
		layers = append(layers, Layer{
			Code:         loaded.FeatureClass.Code,
			Name:         loaded.FeatureClass.Name,
			GeometryType: loaded.FeatureClass.GeometryType,
			Fields:       fields,
			Features:     features,
			// Topology inputs (CIM only; the geometry formats leave these unread).
			ClassID:     loaded.FeatureClass.ID,
			NetworkRole: loaded.FeatureClass.NetworkRole,
			Terminals:   loaded.Terminals,
			Subtypes:    loaded.Subtypes,
		})
	}
	return layers, 0, ""
}

// gatherStreamLayers is the streaming counterpart of gatherLayers (Phase 17
// Slice 3d): it loads every requested class's metadata up front -- so a bad
// class code or a geometry-less class still fails fast, before any bytes -- but
// DEFERS the feature read into each StreamLayer's Each, which opens a DB cursor
// on demand via crud.ExportFeaturesStream. The caller drives Each from inside
// its snapshot transaction while the encoder runs, so the features stream
// straight from the cursor to the wire, uncapped and never materialized. On a
// metadata failure it returns a nil slice plus the HTTP status and message.
func gatherStreamLayers(ctx context.Context, db bun.IDB, codes []string, bbox *crud.ExportBBox, targetSRID *int, territoryIDs, draftAuthors []string) ([]StreamLayer, int, string) {
	layers := make([]StreamLayer, 0, len(codes))
	for _, code := range codes {
		loaded, err := metadata.Load(ctx, db, code)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, http.StatusNotFound, fmt.Sprintf("feature class %q does not exist", code)
			}
			return nil, http.StatusInternalServerError, err.Error()
		}
		if loaded.FeatureClass.GeometryType == "none" {
			return nil, http.StatusBadRequest, fmt.Sprintf("feature class %q has no geometry, so it can't be exported", code)
		}

		fields := make([]AttributeField, len(loaded.Fields))
		for i, f := range loaded.Fields {
			fields[i] = AttributeField{Name: f.Name, DataType: f.DataType}
		}

		// loaded is declared per iteration above, so each Each closure captures
		// its own class -- no shadow needed.
		layers = append(layers, StreamLayer{
			Code:         loaded.FeatureClass.Code,
			Name:         loaded.FeatureClass.Name,
			GeometryType: loaded.FeatureClass.GeometryType,
			Fields:       fields,
			Each: func(emit func(Feature) error) (int, error) {
				return crud.ExportFeaturesStream(ctx, db, loaded, bbox, targetSRID, territoryIDs, draftAuthors, func(row map[string]any) error {
					f, err := featureFrom(row)
					if err != nil {
						return err
					}
					return emit(f)
				})
			},
		})
	}
	return layers, 0, ""
}

// parseBBox validates the optional bbox array. Empty/absent yields nil (a
// whole-dataset export). A present bbox must have exactly four numbers; the
// min/max pairs are normalized so a client that sends them swapped still
// produces a non-empty envelope.
func parseBBox(b []float64) (*crud.ExportBBox, error) {
	if len(b) == 0 {
		return nil, nil
	}
	if len(b) != 4 {
		return nil, fmt.Errorf("bbox must be [minX, minY, maxX, maxY] (4 numbers), got %d", len(b))
	}
	for _, v := range b {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("bbox contains a non-finite number")
		}
	}
	return &crud.ExportBBox{
		MinX: math.Min(b[0], b[2]),
		MinY: math.Min(b[1], b[3]),
		MaxX: math.Max(b[0], b[2]),
		MaxY: math.Max(b[1], b[3]),
	}, nil
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
