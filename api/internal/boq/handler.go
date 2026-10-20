package boq

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path"

	"github.com/go-chi/chi/v5"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/uptrace/bun"
)

// FileStore is the slice of the RustFS object store the asset-register status
// endpoint needs: mint a presigned GET URL for a finished register's artifact.
// *export.ObjectStore satisfies it structurally, so boq presigns without
// importing a concrete store type into its signatures.
type FileStore interface {
	PresignGet(ctx context.Context, key, filename string) (string, error)
}

// Routes returns the router for the Bill of Quantities engine, meant to be
// mounted at /boq:
//
//	POST /  price a scope of the network (territory / radius / bbox / whole
//	        network by classes, or an explicit feature_refs set) and return a
//	        grouped, totalled schedule in a chosen currency.
//
//	POST /asset-register  the per-feature sibling: the same scope returned as an
//	        itemised asset register (one row per feature), optionally valued
//	        through the same catalog.
//
//	POST /asset-register.xlsx  enqueue an async multi-sheet .xlsx build (one sheet
//	        per class, full attribute columns). Returns a job id; the worker builds
//	        it and stores it in RustFS.
//	GET  /asset-register/jobs/{id}  poll one register job; when succeeded it
//	        carries a presigned download URL.
//
// Read-only, like api/internal/trace and api/internal/powerflow -- it only
// counts and prices what is already committed.
func Routes(db *bun.DB, store FileStore) chi.Router {
	r := chi.NewRouter()
	r.Post("/", boqHandler(db))
	r.Post("/asset-register", assetRegisterHandler(db))
	r.Post("/asset-register.xlsx", enqueueAssetRegisterXLSXHandler(db))
	r.Get("/asset-register/jobs/{id}", assetRegisterJobHandler(db, store))
	return r
}

// enqueueAssetRegisterXLSXHandler validates the request and enqueues an async
// asset-register build on the shared export-jobs queue, returning 202 + the job
// id. The build (and RustFS upload) happens in the export worker; the request
// snapshot is the RegisterRequest JSON, and the requester is recorded so the
// worker can reconstruct their read scope.
func enqueueAssetRegisterXLSXHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "authentication required"})
			return
		}
		var req RegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		raw, err := json.Marshal(req)
		if err != nil {
			writeError(w, err)
			return
		}
		id, err := export.EnqueueAssetRegister(r.Context(), db, user.ID, raw)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"job_id": id})
	}
}

// assetRegisterJobView is one register job over the wire. download_url + filename
// appear only once the job has succeeded; error only on failure.
type assetRegisterJobView struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	DownloadURL string `json:"download_url,omitempty"`
	Filename    string `json:"filename,omitempty"`
	ByteSize    int64  `json:"byte_size,omitempty"`
}

// assetRegisterJobHandler polls one of the caller's register jobs. On success it
// mints a short-lived presigned URL (at poll time, so the TTL is spent on the
// download) for the artifact the worker stored in RustFS.
func assetRegisterJobHandler(db *bun.DB, store FileStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.UserFrom(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, apiError{Error: "authentication required"})
			return
		}
		job, err := export.GetJobForUser(r.Context(), db, chi.URLParam(r, "id"), user.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, apiError{Error: "asset-register job not found"})
				return
			}
			writeError(w, err)
			return
		}

		view := assetRegisterJobView{ID: job.ID, Status: job.Status}
		if job.Error != nil {
			view.Error = *job.Error
		}
		if job.Status == "succeeded" && job.ObjectKey != nil && store != nil {
			filename := path.Base(*job.ObjectKey)
			url, err := store.PresignGet(r.Context(), *job.ObjectKey, filename)
			if err != nil {
				writeError(w, err)
				return
			}
			view.DownloadURL = url
			view.Filename = filename
			if job.ByteSize != nil {
				view.ByteSize = *job.ByteSize
			}
		}
		writeJSON(w, http.StatusOK, view)
	}
}

func assetRegisterHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req RegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		result, err := RunAssetRegister(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func boqHandler(db *bun.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, invalid("request body is not valid JSON: %v", err))
			return
		}
		result, err := RunBoQ(r.Context(), db, req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

type apiError struct {
	Error string `json:"error"`
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
