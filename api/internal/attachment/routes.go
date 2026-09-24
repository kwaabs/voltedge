package attachment

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/export"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// Routes is the web (cookie+CSRF+RBAC) attachments surface, mounted at
// /attachments behind a method-aware attachments:read/write gate (see
// cmd/server/main.go). Every route is scoped to a feature by the (class code,
// feature id) pair, since features have no shared table:
//
//	GET    /{code}/{id}                    list a feature's committed attachments
//	POST   /{code}/{id}                    register one -> returns a presigned PUT URL
//	POST   /{code}/{id}/{attachmentID}/commit   confirm the upload landed (+ thumbnail)
//	GET    /{code}/{id}/{attachmentID}/url      a presigned URL to view/download the file
//	DELETE /{code}/{id}/{attachmentID}     soft-delete an attachment
//
// The mount's RBAC gate picks read vs write by HTTP method; the mutating routes
// additionally carry RequireCSRF, matching the newer write surfaces (assistant,
// fieldpack admin). Bytes never flow through this process: register/commit hand
// out presigned URLs the client PUTs/GETs directly against RustFS. The one
// exception is the commit thumbnail, which the server generates by reading the
// just-uploaded image back and writing a small JPEG beside it.
func Routes(db *bun.DB, store *export.ObjectStore) chi.Router {
	r := chi.NewRouter()
	r.Get("/{code}/{id}", listHandler(db, store))
	r.With(auth.RequireCSRF).Post("/{code}/{id}", registerHandler(db, store))
	r.With(auth.RequireCSRF).Post("/{code}/{id}/{attachmentID}/commit", commitHandler(db, store))
	r.Get("/{code}/{id}/{attachmentID}/url", urlHandler(db, store))
	r.With(auth.RequireCSRF).Delete("/{code}/{id}/{attachmentID}", deleteHandler(db, store))
	return r
}

// BearerRoutes is the same attachments surface for the token-authenticated
// fieldsync server (the offline field tool), minus the cookie CSRF guard -- a
// bearer request carries no cookies, so double-submit CSRF is not applicable. The
// caller (cmd/fieldsync via fieldpack.Routes) applies the bearer gate and the
// method-aware attachments:read/write RBAC. Handlers are shared verbatim with the
// web Routes, so register -> presigned PUT -> commit and the presigned view URL
// behave identically; the device just uploads photos (images only) and pulls
// full-resolution originals on demand when it's online.
func BearerRoutes(db *bun.DB, store *export.ObjectStore) chi.Router {
	r := chi.NewRouter()
	r.Get("/{code}/{id}", listHandler(db, store))
	r.Post("/{code}/{id}", registerHandler(db, store))
	r.Post("/{code}/{id}/{attachmentID}/commit", commitHandler(db, store))
	r.Get("/{code}/{id}/{attachmentID}/url", urlHandler(db, store))
	r.Delete("/{code}/{id}/{attachmentID}", deleteHandler(db, store))
	return r
}

// attachmentView is one attachment over the wire. thumb_url is an inline
// presigned URL, present only for ready images that have a thumbnail -- the web
// grid renders it directly; a document (or an image whose thumbnail failed to
// generate) has none, and the client shows a file-type placeholder instead.
type attachmentView struct {
	ID           string    `json:"id"`
	FeatureClass string    `json:"feature_class"`
	FeatureID    string    `json:"feature_id"`
	Filename     string    `json:"filename"`
	ContentType  string    `json:"content_type"`
	ByteSize     int64     `json:"byte_size"`
	Kind         string    `json:"kind"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	ThumbURL     string    `json:"thumb_url,omitempty"`
}

func toView(a *Attachment, thumbURL string) attachmentView {
	return attachmentView{
		ID:           a.ID,
		FeatureClass: a.FeatureClass,
		FeatureID:    a.FeatureID,
		Filename:     a.Filename,
		ContentType:  a.ContentType,
		ByteSize:     a.ByteSize,
		Kind:         a.Kind,
		Status:       a.Status,
		CreatedAt:    a.CreatedAt,
		ThumbURL:     thumbURL,
	}
}

type registerRequest struct {
	// ID is an optional client-minted uuid. The offline field tool supplies its
	// own so an upload replays idempotently; the web omits it and the server
	// mints one. An invalid uuid is a 400.
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	// ByteSize is advisory at register time; the true stored size is recorded at
	// commit from the object itself.
	ByteSize int64 `json:"byte_size"`
	// Kind is optional; when empty it's inferred from ContentType (image/* -> an
	// image, else a document).
	Kind string `json:"kind"`
}

type registerResponse struct {
	Attachment attachmentView `json:"attachment"`
	// UploadURL is a short-lived presigned PUT URL. The client PUTs the raw bytes
	// to it with Content-Type set to UploadContentType, then calls commit.
	UploadURL         string `json:"upload_url"`
	UploadContentType string `json:"upload_content_type"`
}

func listHandler(db *bun.DB, store *export.ObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		featureID := chi.URLParam(r, "id")
		if _, err := enforceFeatureReadScope(r.Context(), db, code, featureID); err != nil {
			writeError(w, err)
			return
		}

		rows, err := listReady(r.Context(), db, code, featureID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "listing attachments: %v", err)
			return
		}

		views := make([]attachmentView, len(rows))
		for i := range rows {
			a := &rows[i]
			views[i] = toView(a, thumbURLFor(r, store, a))
		}
		writeJSON(w, http.StatusOK, map[string]any{"attachments": views})
	}
}

func registerHandler(db *bun.DB, store *export.ObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		featureID := chi.URLParam(r, "id")

		loaded, err := enforceFeatureReadScope(r.Context(), db, code, featureID)
		if err != nil {
			writeError(w, err)
			return
		}
		exists, err := featureExists(r.Context(), db, loaded, featureID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "checking feature: %v", err)
			return
		}
		if !exists {
			writeErr(w, http.StatusNotFound, "feature %s/%s does not exist", code, featureID)
			return
		}

		var req registerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, "request body is not valid JSON: %v", err)
			return
		}
		req.Filename = strings.TrimSpace(req.Filename)
		req.ContentType = strings.TrimSpace(req.ContentType)
		if req.Filename == "" {
			writeErr(w, http.StatusBadRequest, "filename is required")
			return
		}
		if req.ContentType == "" {
			writeErr(w, http.StatusBadRequest, "content_type is required")
			return
		}

		kind := req.Kind
		if kind == "" {
			kind = inferKind(req.ContentType)
		}
		if kind != KindImage && kind != KindDocument {
			writeErr(w, http.StatusBadRequest, "kind must be %q or %q", KindImage, KindDocument)
			return
		}

		id := strings.TrimSpace(req.ID)
		if id == "" {
			id = uuid.NewString()
		} else if _, err := uuid.Parse(id); err != nil {
			writeErr(w, http.StatusBadRequest, "id is not a valid uuid: %v", err)
			return
		}

		now := time.Now()
		a := &Attachment{
			ID:           id,
			FeatureClass: code,
			FeatureID:    featureID,
			ObjectKey:    ObjectKey(featureID, id),
			Filename:     req.Filename,
			ContentType:  req.ContentType,
			ByteSize:     req.ByteSize,
			Kind:         kind,
			Status:       StatusPending,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if actor := auth.ActorID(r.Context()); actor != "" {
			a.UploadedBy = &actor
		}

		if err := insert(r.Context(), db, a); err != nil {
			writeErr(w, http.StatusInternalServerError, "registering attachment: %v", err)
			return
		}

		uploadURL, err := store.PresignPut(r.Context(), a.ObjectKey)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "presigning upload URL: %v", err)
			return
		}

		writeJSON(w, http.StatusCreated, registerResponse{
			Attachment:        toView(a, ""),
			UploadURL:         uploadURL,
			UploadContentType: a.ContentType,
		})
	}
}

func commitHandler(db *bun.DB, store *export.ObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		featureID := chi.URLParam(r, "id")
		attachmentID := chi.URLParam(r, "attachmentID")

		if _, err := enforceFeatureReadScope(r.Context(), db, code, featureID); err != nil {
			writeError(w, err)
			return
		}

		a, err := get(r.Context(), db, code, featureID, attachmentID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "attachment not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, "loading attachment: %v", err)
			return
		}

		// Confirm the client's presigned PUT actually landed, and record the true
		// stored size. A missing object means commit was called before the upload
		// finished -- a client error, so 400 rather than 500.
		size, _, err := store.Stat(r.Context(), a.ObjectKey)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "uploaded file not found -- PUT it to the upload_url before committing: %v", err)
			return
		}
		a.ByteSize = size

		// Generate the thumbnail for images. A decode failure (an odd format, or a
		// mislabeled non-image) is not fatal: the attachment still commits, just
		// without a thumb_url, and the UI falls back to a placeholder.
		if a.Kind == KindImage {
			if thumbKey, ok := makeThumbnail(r.Context(), store, a); ok {
				a.ThumbKey = &thumbKey
			}
		}

		if err := markReady(r.Context(), db, a); err != nil {
			writeErr(w, http.StatusInternalServerError, "committing attachment: %v", err)
			return
		}
		writeJSON(w, http.StatusOK, toView(a, thumbURLFor(r, store, a)))
	}
}

func urlHandler(db *bun.DB, store *export.ObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		featureID := chi.URLParam(r, "id")
		attachmentID := chi.URLParam(r, "attachmentID")

		if _, err := enforceFeatureReadScope(r.Context(), db, code, featureID); err != nil {
			writeError(w, err)
			return
		}

		a, err := get(r.Context(), db, code, featureID, attachmentID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "attachment not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, "loading attachment: %v", err)
			return
		}

		// Images serve inline (so the web app can open them in a lightbox); a
		// document serves as a download under its original filename.
		var url string
		if a.Kind == KindImage {
			url, err = store.PresignGetInline(r.Context(), a.ObjectKey)
		} else {
			url, err = store.PresignGet(r.Context(), a.ObjectKey, a.Filename)
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "presigning URL: %v", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"url": url})
	}
}

func deleteHandler(db *bun.DB, store *export.ObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		code := chi.URLParam(r, "code")
		featureID := chi.URLParam(r, "id")
		attachmentID := chi.URLParam(r, "attachmentID")

		if _, err := enforceFeatureReadScope(r.Context(), db, code, featureID); err != nil {
			writeError(w, err)
			return
		}

		if _, err := get(r.Context(), db, code, featureID, attachmentID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeErr(w, http.StatusNotFound, "attachment not found")
				return
			}
			writeErr(w, http.StatusInternalServerError, "loading attachment: %v", err)
			return
		}
		if err := softDelete(r.Context(), db, attachmentID); err != nil {
			writeErr(w, http.StatusInternalServerError, "deleting attachment: %v", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// makeThumbnail reads an image attachment's bytes back from RustFS, downscales
// them, and uploads the JPEG thumbnail beside the original. Returns the thumb key
// and true on success; false (with no error surfaced) when the object can't be
// read or decoded, so the caller commits without a thumbnail rather than failing.
func makeThumbnail(ctx context.Context, store *export.ObjectStore, a *Attachment) (string, bool) {
	rc, err := store.Open(ctx, a.ObjectKey)
	if err != nil {
		return "", false
	}
	defer rc.Close()

	thumb, err := Thumbnail(rc)
	if err != nil {
		return "", false
	}
	key := ThumbKey(a.FeatureID, a.ID)
	if _, err := store.Upload(ctx, key, bytes.NewReader(thumb), "image/jpeg"); err != nil {
		return "", false
	}
	return key, true
}

// thumbURLFor returns an inline presigned URL for a ready image's thumbnail, or
// "" when there's no thumbnail (a document, or an image whose thumb failed). A
// presign failure is swallowed to "" -- a missing thumb only degrades the grid to
// a placeholder, never fails the whole list.
func thumbURLFor(r *http.Request, store *export.ObjectStore, a *Attachment) string {
	if a.ThumbKey == nil {
		return ""
	}
	url, err := store.PresignGetInline(r.Context(), *a.ThumbKey)
	if err != nil {
		return ""
	}
	return url
}

func inferKind(contentType string) string {
	if strings.HasPrefix(strings.ToLower(contentType), "image/") {
		return KindImage
	}
	return KindDocument
}

// enforceFeatureReadScope resolves the feature class and gates the operation on
// the caller's ability to READ the parent feature (Phase 58 territory + Phase 61
// private-draft checks, via crud.EnforceFeatureReadScope). The attachment surface
// otherwise scoped only by (class, feature id, attachment id), so a caller who
// learned such a triple -- ids are returned by the feature/attachment APIs --
// could list or presign a file for a feature outside their territory grants, or on
// a peer's private draft. An out-of-scope or missing feature is reported as
// "attachment not found" (404), the same answer as an unknown attachment, so the
// id isn't confirmed. Returns the loaded class for callers that need it.
//
// This gates reads and, as a floor, the writes too (register/commit/delete already
// carry the attachments:write RBAC gate on the mount); tightening the writes to
// the stricter territory WRITE scope (a 403) is a possible follow-up.
func enforceFeatureReadScope(ctx context.Context, db *bun.DB, code, featureID string) (*metadata.Loaded, error) {
	loaded, err := loadClass(ctx, db, code)
	if err != nil {
		return nil, err
	}
	if err := crud.EnforceFeatureReadScope(ctx, db, loaded, featureID); err != nil {
		if crud.IsNotFoundError(err) {
			return nil, &httpStatusError{status: http.StatusNotFound, msg: "attachment not found"}
		}
		return nil, err
	}
	return loaded, nil
}

// loadClass resolves a feature class by code, mapping an unknown class to a 404
// httpStatusError so handlers can writeError it uniformly.
func loadClass(ctx context.Context, db *bun.DB, code string) (*metadata.Loaded, error) {
	loaded, err := metadata.Load(ctx, db, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, &httpStatusError{status: http.StatusNotFound, msg: fmt.Sprintf("feature class %q does not exist", code)}
		}
		return nil, err
	}
	return loaded, nil
}

// httpStatusError carries an HTTP status so writeError can render the right code
// for the domain errors this package raises (currently just class-not-found).
type httpStatusError struct {
	status int
	msg    string
}

func (e *httpStatusError) Error() string { return e.msg }

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, format string, args ...any) {
	writeJSON(w, status, apiError{Error: fmt.Sprintf(format, args...)})
}

// writeError renders an error carrying its own status (httpStatusError), else a
// generic 500.
func writeError(w http.ResponseWriter, err error) {
	var se *httpStatusError
	if errors.As(err, &se) {
		writeJSON(w, se.status, apiError{Error: se.msg})
		return
	}
	writeJSON(w, http.StatusInternalServerError, apiError{Error: err.Error()})
}
