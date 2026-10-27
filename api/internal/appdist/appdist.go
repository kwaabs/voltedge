// Package appdist is the field-app distribution surface -- "our own play store".
// A published, SIGNED release APK lives in a dedicated RustFS bucket alongside a
// latest.json manifest (written by cmd/apkpublish); this package serves two
// PUBLIC endpoints over it so a field tech can install on a fresh phone with no
// web login:
//
//	GET /app/latest     the current release manifest (version, size, notes, sha256)
//	GET /app/download   302 -> a short-lived presigned RustFS URL for the APK
//
// Both are unauthenticated by design (the download page + QR are meant to be
// hit from a device that isn't signed in). The APK is what's trusted -- it's
// signed with the release keystore -- not the transport.
package appdist

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kwaabs/voltedge/api/internal/export"
)

const (
	// Bucket is the dedicated RustFS bucket for app releases -- separate from the
	// exports bucket so the export retention sweep never touches a shipped APK.
	Bucket = "voltedge-apps"
	// ManifestKey is the object holding the current-release pointer.
	ManifestKey = "latest.json"
	// APKContentType is the Android package MIME type.
	APKContentType = "application/vnd.android.package-archive"
)

// Manifest is the current-release pointer (latest.json). Shared with the
// publisher (cmd/apkpublish) so the two agree on the shape.
type Manifest struct {
	Version     string    `json:"version"`
	VersionCode int       `json:"version_code,omitempty"`
	ObjectKey   string    `json:"object_key"`
	Filename    string    `json:"filename"`
	SizeBytes   int64     `json:"size_bytes"`
	SHA256      string    `json:"sha256"`
	Notes       string    `json:"notes,omitempty"`
	ReleasedAt  time.Time `json:"released_at"`
}

// Routes serves the public distribution endpoints over the given APK store.
func Routes(store *export.ObjectStore) chi.Router {
	r := chi.NewRouter()
	r.Get("/latest", latestHandler(store))
	r.Get("/download", downloadHandler(store))
	return r
}

// loadManifest reads + decodes latest.json. A missing object (no release yet)
// returns os-not-found-shaped nil,nil-ish via the (nil, nil) + ok flag.
func loadManifest(r *http.Request, store *export.ObjectStore) (*Manifest, bool, error) {
	rc, err := store.Open(r.Context(), ManifestKey)
	if err != nil {
		return nil, false, nil // treat an unreachable/absent manifest as "no release"
	}
	defer rc.Close()
	raw, err := io.ReadAll(rc)
	if err != nil {
		return nil, false, nil // lazy minio errors surface here for a missing key
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false, err // a present-but-corrupt manifest is a real 500
	}
	return &m, true, nil
}

// latestHandler returns the current release manifest, plus a relative
// download_url the page/app can link to. 404 when nothing is published yet.
func latestHandler(store *export.ObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, ok, err := loadManifest(r, store)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "release manifest is unreadable"})
			return
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no field-app release has been published yet"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"version":      m.Version,
			"version_code": m.VersionCode,
			"filename":     m.Filename,
			"size_bytes":   m.SizeBytes,
			"sha256":       m.SHA256,
			"notes":        m.Notes,
			"released_at":  m.ReleasedAt,
			"download_url": "/app/download",
		})
	}
}

// downloadHandler 302-redirects to a short-lived presigned URL for the current
// APK, so the bytes stream from RustFS, not through the API.
func downloadHandler(store *export.ObjectStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, ok, err := loadManifest(r, store)
		if err != nil || !ok {
			http.Error(w, "no field-app release is available", http.StatusNotFound)
			return
		}
		url, err := store.PresignGet(r.Context(), m.ObjectKey, m.Filename)
		if err != nil {
			http.Error(w, "could not prepare the download", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, url, http.StatusFound)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
