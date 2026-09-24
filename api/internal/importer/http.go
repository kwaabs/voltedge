package importer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/kwaabs/voltedge/api/internal/secretbox"
)

// validStagedImportKey reports whether a CLIENT-supplied object key addresses a
// staged import artifact rather than an arbitrary object in the shared bucket.
//
// POST /uploads mints keys as "imports/<uuid><ext>" server-side and returns each
// only to the uploader. The introspection/run handlers (layers, dry-run, jobs)
// take the key back from the client, so without this check a holder of
// features:write could point them at ANY object in the shared export/import
// bucket -- notably an attachment ("attachments/<featureID>/<id>", whose ids are
// discoverable through the feature/attachment REST APIs) or another user's
// export -- and read its contents (layer metadata + sample rows). Constraining
// the key to the imports/ prefix blocks those cross-object reads; another user's
// import key is a random uuid and not guessable, so this closes the practical
// exposure. (Per-user staged-key ownership -- recording each minted key against
// its uploader and checking it here -- is the fuller fix, tracked as a follow-up.)
func validStagedImportKey(key string) bool {
	return strings.HasPrefix(key, "imports/") && !strings.Contains(key, "..")
}

// maxJobErrorReport caps how many per-row errors the status endpoint returns --
// enough to act on without shipping a pathological all-rows-failed report whole.
const maxJobErrorReport = 500

// Routes is the operational side of the import engine -- running an import, which
// writes features, so api/cmd/server/main.go mounts it under the features gate
// (features:read / features:write). Authoring a profile is separate config
// (metadata:write, under /admin, api/internal/admin/imports.go); this surface is
// where a holder of features:write stages an upload and pre-flights it.
//
//	GET  /profiles     the active profiles this operator can run (wizard picker)
//	POST /uploads      mint an object key + presigned PUT URL for the source artifact
//	POST /layers       list a staged GeoPackage's feature layers (server-side parse)
//	POST /dry-run      validate a sample of rows against a profile, writing nothing
//	POST /jobs         enqueue a load run over an uploaded artifact
//	GET  /jobs         the caller's recent runs (history panel)
//	GET  /jobs/{id}    one run's status + tallies + per-row error report
//
// The enqueued job is claimed and executed by the standalone cmd/importworker
// (worker.go), which streams the artifact this upload step staged and applies
// every row (load.go).
func Routes(db *bun.DB, store *export.ObjectStore, cipher *secretbox.Cipher) chi.Router {
	r := chi.NewRouter()
	r.Get("/profiles", listProfilesHandler(db))
	r.Post("/uploads", registerUploadHandler(store))
	r.Post("/layers", listLayersHandler(store))
	r.Post("/dry-run", dryRunHandler(db, store, cipher))
	r.Post("/jobs", enqueueJobHandler(db))
	r.Get("/jobs", listJobsHandler(db))
	r.Get("/jobs/{id}", jobStatusHandler(db))
	return r
}

// listProfilesHandler returns the active import profiles a features:write
// operator can run, so the wizard's picker never depends on metadata:read (the
// gate on the admin authoring list). Read-only, so it needs no scope beyond the
// features gate this surface already sits behind.
func listProfilesHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		profiles, err := listActiveProfiles(r.Context(), db)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "listing import profiles: %v", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"import_profiles": profiles})
	}
}

// uploadRegisterRequest carries the source filename for logging/UX only and an
// optional artifact kind. The stored object key is server-minted (never
// client-controlled) so an upload can't address another tenant's key. Artifact
// distinguishes the ingest shapes: "ndjson" (default) for the v1 formats the
// browser normalizes, or a raw SQLite-family file the browser uploads verbatim for
// the server to parse ("geopackage" / "spatialite"). It only picks the key's
// extension -- what actually parses the object is the run's profile source_format,
// not the key.
type uploadRegisterRequest struct {
	Filename string `json:"filename"`
	Artifact string `json:"artifact"`
}

// artifactExt maps an upload artifact kind to the object key's file extension. The
// extension is cosmetic (the profile's source_format drives parsing), but a real
// extension keeps staged objects legible.
func artifactExt(artifact string) string {
	switch artifact {
	case "geopackage":
		return ".gpkg"
	case "spatialite":
		return ".sqlite"
	default:
		return ".ndjson"
	}
}

type uploadRegisterResponse struct {
	ObjectKey string `json:"object_key"`
	UploadURL string `json:"upload_url"`
}

// registerUploadHandler mints a fresh object key and a short-lived presigned PUT
// URL; the browser PUTs the artifact straight to RustFS (off the API hot path,
// same as the attachment register->PUT->commit flow). No DB row is created here
// -- the import_jobs row is minted when the run is enqueued (S4), referencing this
// key.
func registerUploadHandler(store *export.ObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req uploadRegisterRequest
		if r.Body != nil {
			// The body is optional (filename is informational); ignore a decode
			// error rather than reject an empty POST.
			_ = json.NewDecoder(r.Body).Decode(&req)
		}

		key := "imports/" + uuid.NewString() + artifactExt(req.Artifact)
		uploadURL, err := store.PresignPut(r.Context(), key)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "presigning upload URL: %v", err)
			return
		}
		writeJSON(w, http.StatusCreated, uploadRegisterResponse{ObjectKey: key, UploadURL: uploadURL})
	}
}

// layersRequest names a staged SQLite-family object to introspect, plus which
// format it is (so the server reads the right metadata catalog -- GeoPackage's
// gpkg_* tables vs SpatiaLite's geometry_columns). SourceFormat defaults to
// geopackage when omitted for backward compatibility.
type layersRequest struct {
	ObjectKey    string `json:"object_key"`
	SourceFormat string `json:"source_format"`
}

// listLayersHandler opens a staged SQLite-family file (GeoPackage / SpatiaLite)
// server-side and returns its feature layers (name, geometry column/type, SRID,
// feature count) so the run wizard can pin or override which layer to import. The
// browser can't read the file itself, which is exactly why this endpoint exists.
// It is read-only (downloads to a temp file, opens it, removes it) and sits behind
// the same features gate as the rest of this surface.
func listLayersHandler(store *export.ObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req layersRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "request body is not valid JSON: %v", err)
			return
		}
		if !validStagedImportKey(req.ObjectKey) {
			writeErr(w, http.StatusBadRequest, "object_key must reference a staged import artifact")
			return
		}
		format := req.SourceFormat
		if format == "" {
			format = "geopackage"
		}
		layers, err := inspectSQLiteLayers(r.Context(), store, req.ObjectKey, format)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"layers": layers})
	}
}

// dryRunRequest is a profile code plus the rows to pre-flight, in one of two
// shapes. For the v1 NDJSON formats the browser parses the file itself and sends
// an inline Sample. For a GeoPackage the browser can't parse it, so it sends the
// staged ObjectKey (+ optional SourceLayer) and the server reads the sample from
// object storage. Sample takes precedence; ObjectKey is the server-side path.
type dryRunRequest struct {
	ProfileCode string     `json:"profile_code"`
	Sample      []*Feature `json:"sample"`
	ObjectKey   string     `json:"object_key"`
	SourceLayer string     `json:"source_layer"`
}

func dryRunHandler(db *bun.DB, store *export.ObjectStore, cipher *secretbox.Cipher) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req dryRunRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "request body is not valid JSON: %v", err)
			return
		}
		if req.ProfileCode == "" {
			writeErr(w, http.StatusBadRequest, "profile_code is required")
			return
		}

		sample := req.Sample
		if len(sample) == 0 {
			// No inline sample: the source is one the browser can't parse itself, so
			// the server reads the preview. Resolve the profile once for both its
			// source_format (which reader) and its pinned default layer, so the
			// pre-flight reads exactly what the run will. The effective layer is the
			// run-time override if given, else that default.
			rp, err := ResolveProfile(r.Context(), db, req.ProfileCode)
			if err != nil {
				writeError(w, err)
				return
			}
			layer := req.SourceLayer
			if layer == "" && rp.Profile.SourceLayer != nil {
				layer = *rp.Profile.SourceLayer
			}
			switch {
			case isLiveDBSourceFormat(rp.Profile.SourceFormat):
				// A live database (PostGIS): sample straight off the remote
				// table/query -- no staged object at all.
				scope := ImportScope{ProfileCode: req.ProfileCode, SourceLayer: layer}
				sample, err = sampleLiveDB(r.Context(), rp, &scope, cipher, liveDryRunSample)
			case req.ObjectKey != "":
				// A staged SQLite-family file (GeoPackage / SpatiaLite). The key is
				// client-supplied, so constrain it to a staged import artifact before
				// opening it (see validStagedImportKey).
				if !validStagedImportKey(req.ObjectKey) {
					writeErr(w, http.StatusBadRequest, "object_key must reference a staged import artifact")
					return
				}
				sample, err = sampleSQLite(r.Context(), store, req.ObjectKey, layer, gpkgDryRunSample, rp.Profile.SourceFormat)
			}
			if err != nil {
				writeError(w, err)
				return
			}
		}

		report, err := DryRun(r.Context(), db, req.ProfileCode, sample)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

// enqueueJobRequest starts a load run: the profile that drives it and the object
// key of the NDJSON artifact the browser already staged (via POST /uploads).
// dry_run runs the whole file through validation + the would-insert/update
// decision without writing -- a full-file pre-flight, distinct from the inline
// sample POST /dry-run does synchronously.
type enqueueJobRequest struct {
	ProfileCode string `json:"profile_code"`
	ObjectKey   string `json:"object_key"`
	DryRun      bool   `json:"dry_run"`
	// SourceLayer overrides which feature layer of a multi-layer source
	// (GeoPackage) this run imports. Empty falls back to the profile's pinned
	// default; ignored by the single-layer v1 formats.
	SourceLayer string `json:"source_layer"`
}

type enqueueJobResponse struct {
	JobID string `json:"job_id"`
}

// enqueueJobHandler validates the request, snapshots the caller's authz scope,
// and enqueues an ops.import_jobs row for cmd/importworker to claim. It resolves
// the profile up front so an unknown profile is a clean 404 here rather than a
// worker-side failure, and so the job carries the profile id + class code for
// display even if the profile is later deleted.
func enqueueJobHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}

		var req enqueueJobRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "request body is not valid JSON: %v", err)
			return
		}
		if req.ProfileCode == "" {
			writeErr(w, http.StatusBadRequest, "profile_code is required")
			return
		}

		rp, err := ResolveProfile(r.Context(), db, req.ProfileCode)
		if err != nil {
			writeError(w, err)
			return
		}

		// A file import needs the staged artifact's key; a live-DB import has no
		// artifact (the worker connects out to the remote table/query), so it runs
		// with an empty object key. The key is client-supplied, so a file import
		// must reference a staged import artifact -- the worker later streams this
		// key, so an arbitrary bucket object here would be read at run time too.
		if !isLiveDBSourceFormat(rp.Profile.SourceFormat) && !validStagedImportKey(req.ObjectKey) {
			writeErr(w, http.StatusBadRequest, "object_key must reference a staged import artifact")
			return
		}

		// Snapshot the caller's Phase 58 territory scope so the worker enforces
		// exactly the requester's rights when it writes each row.
		ids, unscoped := user.TerritoryFilter()

		// Freeze the effective source layer: the run-time override if given, else
		// the profile's pinned default, so the worker imports exactly what was
		// requested even if the profile default later changes.
		layer := req.SourceLayer
		if layer == "" && rp.Profile.SourceLayer != nil {
			layer = *rp.Profile.SourceLayer
		}
		scope := ImportScope{ProfileCode: req.ProfileCode, TerritoryIDs: ids, Unscoped: unscoped, SourceLayer: layer}

		profileID := rp.Profile.ID
		jobID, err := EnqueueImport(r.Context(), db, user.ID, &profileID, rp.Loaded.FeatureClass.Code, req.ObjectKey, req.DryRun, scope)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "enqueuing import: %v", err)
			return
		}
		writeJSON(w, http.StatusAccepted, enqueueJobResponse{JobID: jobID})
	}
}

// jobRowError is one per-row failure in a run's error report.
type jobRowError struct {
	RowNumber int    `json:"row_number"`
	Message   string `json:"message"`
}

// jobResponse is the wire shape of one import run: its lifecycle status, the
// tallies filled as it streamed, an optional fatal message, and (on the single-
// job view) the per-row error report.
type jobResponse struct {
	ID               string        `json:"id"`
	Status           string        `json:"status"`
	FeatureClassCode string        `json:"feature_class_code"`
	DryRun           bool          `json:"dry_run"`
	TotalRows        *int          `json:"total_rows"`
	InsertedCount    int           `json:"inserted_count"`
	UpdatedCount     int           `json:"updated_count"`
	SkippedCount     int           `json:"skipped_count"`
	ErrorCount       int           `json:"error_count"`
	Error            *string       `json:"error,omitempty"`
	CreatedAt        time.Time     `json:"created_at"`
	StartedAt        *time.Time    `json:"started_at,omitempty"`
	FinishedAt       *time.Time    `json:"finished_at,omitempty"`
	Errors           []jobRowError `json:"errors,omitempty"`
}

// toJobResponse maps a job row (and optional error rows) into its wire shape.
func toJobResponse(job *ImportJob, errs []ImportJobError) jobResponse {
	resp := jobResponse{
		ID:               job.ID,
		Status:           job.Status,
		FeatureClassCode: job.FeatureClassCode,
		DryRun:           job.DryRun,
		TotalRows:        job.TotalRows,
		InsertedCount:    job.InsertedCount,
		UpdatedCount:     job.UpdatedCount,
		SkippedCount:     job.SkippedCount,
		ErrorCount:       job.ErrorCount,
		Error:            job.Error,
		CreatedAt:        job.CreatedAt,
		StartedAt:        job.StartedAt,
		FinishedAt:       job.FinishedAt,
	}
	for i := range errs {
		resp.Errors = append(resp.Errors, jobRowError{RowNumber: errs[i].RowNumber, Message: errs[i].Message})
	}
	return resp
}

// listJobsHandler returns the caller's most recent runs (newest first), without
// the per-row error report -- that rides on the single-job view.
func listJobsHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		jobs, err := listImportJobs(r.Context(), db, user.ID, 50)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "listing import jobs: %v", err)
			return
		}
		out := make([]jobResponse, 0, len(jobs))
		for i := range jobs {
			out = append(out, toJobResponse(&jobs[i], nil))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// jobStatusHandler returns one run's status, tallies, and error report, scoped to
// its owner so a caller can never poll another user's run (an unknown or
// someone-else's id is an indistinguishable 404).
func jobStatusHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeErr(w, http.StatusUnauthorized, "authentication required")
			return
		}
		id := chi.URLParam(r, "id")
		job, err := getImportJob(r.Context(), db, id, user.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "import job %q not found", id)
				return
			}
			writeErr(w, http.StatusInternalServerError, "loading import job: %v", err)
			return
		}
		errs, err := listImportJobErrors(r.Context(), db, job.ID, maxJobErrorReport)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "loading import job errors: %v", err)
			return
		}
		writeJSON(w, http.StatusOK, toJobResponse(job, errs))
	}
}

// writeJSON writes a JSON body with the given status.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeErr writes a formatted error body with an explicit status.
func writeErr(w http.ResponseWriter, status int, format string, args ...any) {
	writeJSON(w, status, map[string]string{"error": fmt.Sprintf(format, args...)})
}

// writeError maps a returned error to its HTTP status: this package's own
// validation problems and crud validation errors (raised inside ValidateFeature)
// are 400s the caller can fix; an unknown profile and crud not-found are 404s;
// anything else is a 500.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case IsInvalid(err), crud.IsValidationError(err):
		status = http.StatusBadRequest
	case IsNotFound(err), crud.IsNotFoundError(err):
		status = http.StatusNotFound
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
