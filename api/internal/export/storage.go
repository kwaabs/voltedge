package export

// RustFS object store (Phase 17 Slice 3, docs/phase-17-large-export.md).
//
// Export artifacts live in RustFS (the S3-compatible store already in the
// compose stack), not on any worker's local disk -- so a job encoded by one
// process can be downloaded via any other, and the bytes never touch the API's
// memory. The writer streams the encoder straight into a multipart PutObject
// with an unknown length (io.Pipe on the other end), so a 10 GB export uploads
// in constant memory. Download is a short-lived presigned GET URL: the API
// redirects the browser to RustFS instead of proxying the bytes, keeping large
// downloads off the API hot path.

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	// defaultExportBucket is the RustFS bucket export artifacts land in when
	// RUSTFS_BUCKET is unset. Created on boot if missing (EnsureBucket).
	defaultExportBucket = "voltedge-exports"

	// defaultURLTTL is how long a presigned download URL stays valid when
	// EXPORT_URL_TTL is unset. Short on purpose: the URL grants unauthenticated
	// access to the object, so it should outlive a click, not a session.
	defaultURLTTL = 15 * time.Minute

	// uploadPartSize bounds the multipart chunk PutObject buffers when the
	// object length is unknown (streaming). It caps the worker's per-upload
	// memory: one part in flight, not the whole artifact.
	uploadPartSize = 16 * 1024 * 1024 // 16 MiB
)

// ObjectStore is the thin RustFS wrapper the export path uses: upload an
// artifact under a key, and mint a presigned URL to fetch it back. It holds a
// configured minio client, the target bucket, and the presign TTL.
type ObjectStore struct {
	client *minio.Client
	bucket string
	urlTTL time.Duration
}

// NewObjectStoreFromEnv builds the store from the RUSTFS_* / EXPORT_URL_TTL
// environment. It only constructs the client (no network round-trip), so a
// RustFS that is momentarily unreachable does not stop the API from booting;
// the first Upload/EnsureBucket is where a real outage surfaces. A missing
// RUSTFS_ENDPOINT is a hard config error -- exports cannot work without it.
func NewObjectStoreFromEnv() (*ObjectStore, error) {
	endpoint := os.Getenv("RUSTFS_ENDPOINT")
	if endpoint == "" {
		return nil, fmt.Errorf("RUSTFS_ENDPOINT is not set (required for export storage)")
	}

	bucket := os.Getenv("RUSTFS_BUCKET")
	if bucket == "" {
		bucket = defaultExportBucket
	}

	urlTTL := defaultURLTTL
	if v := os.Getenv("EXPORT_URL_TTL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("EXPORT_URL_TTL is not a valid duration (%q): %w", v, err)
		}
		urlTTL = d
	}

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(os.Getenv("RUSTFS_ACCESS_KEY"), os.Getenv("RUSTFS_SECRET_KEY"), ""),
		Secure: os.Getenv("RUSTFS_USE_SSL") == "1",
	})
	if err != nil {
		return nil, fmt.Errorf("configuring RustFS client: %w", err)
	}

	return &ObjectStore{client: client, bucket: bucket, urlTTL: urlTTL}, nil
}

// EnsureBucket creates the export bucket if it does not exist. Called once at
// boot; safe to call repeatedly (a present bucket is a no-op).
func (s *ObjectStore) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("checking bucket %q: %w", s.bucket, err)
	}
	if exists {
		return nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
		return fmt.Errorf("creating bucket %q: %w", s.bucket, err)
	}
	return nil
}

// Upload streams r into the bucket under key with an unknown length, returning
// the stored byte size. Passing -1 as the object size makes minio-go do a
// streaming multipart upload bounded by uploadPartSize, so the artifact is
// never held whole in memory. A read error from r (e.g. the encoder failing
// mid-stream via pipe CloseWithError) aborts the multipart upload and surfaces
// here as the returned error, leaving no partial object behind.
func (s *ObjectStore) Upload(ctx context.Context, key string, r io.Reader, contentType string) (int64, error) {
	info, err := s.client.PutObject(ctx, s.bucket, key, r, -1, minio.PutObjectOptions{
		ContentType: contentType,
		PartSize:    uploadPartSize,
	})
	if err != nil {
		return 0, err
	}
	return info.Size, nil
}

// Delete removes an artifact by key. S3 DELETE is idempotent -- removing a key
// that doesn't exist succeeds -- so the retention sweep can call this without
// first checking existence, and a partly-reaped job is safe to reap again.
func (s *ObjectStore) Delete(ctx context.Context, key string) error {
	return s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}

// PresignGet mints a time-limited GET URL for key. The browser is redirected
// here, so response-content-disposition is baked into the signature to force a
// download with a friendly filename rather than an inline render. The call is
// local (no RustFS round-trip): it only computes a signature, so the configured
// endpoint must be the host the browser can reach, not an internal-only one.
func (s *ObjectStore) PresignGet(ctx context.Context, key, filename string) (string, error) {
	params := make(url.Values)
	params.Set("response-content-disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, s.urlTTL, params)
	if err != nil {
		return "", fmt.Errorf("presigning download URL: %w", err)
	}
	return u.String(), nil
}
