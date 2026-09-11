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
//
// client vs presignClient is the internal/public endpoint split. Uploads +
// bucket ops go through client, bound to RUSTFS_ENDPOINT -- the host the API
// process reaches RustFS at (in compose, rustfs:9000). Presigned download URLs
// are signed by presignClient, bound to RUSTFS_PUBLIC_ENDPOINT when set -- the
// host the *downloader* (a browser, or a field device off the compose network)
// reaches RustFS at (e.g. localhost:3702 or a LAN/public host). The signature is
// computed over the endpoint host, so a URL signed for rustfs:9000 fails from
// anywhere that can't resolve it; signing with a separate public-endpoint client
// fixes that. When RUSTFS_PUBLIC_ENDPOINT is unset, presignClient == client and
// behaviour is unchanged.
type ObjectStore struct {
	client        *minio.Client
	presignClient *minio.Client
	bucket        string
	urlTTL        time.Duration
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

	client, err := newRustFSClient(endpoint, os.Getenv("RUSTFS_USE_SSL") == "1")
	if err != nil {
		return nil, fmt.Errorf("configuring RustFS client: %w", err)
	}

	// The presign client is bound to the public endpoint when one is configured,
	// so download URLs are signed for a host the downloader can actually reach.
	// With the region set explicitly (see newRustFSClient) presigning is pure
	// signature math -- no network -- so an unreachable-from-here public host is
	// fine. Falls back to the internal client when unset.
	presignClient := client
	if pub := os.Getenv("RUSTFS_PUBLIC_ENDPOINT"); pub != "" {
		presignClient, err = newRustFSClient(pub, os.Getenv("RUSTFS_PUBLIC_USE_SSL") == "1")
		if err != nil {
			return nil, fmt.Errorf("configuring RustFS presign client: %w", err)
		}
	}

	return &ObjectStore{client: client, presignClient: presignClient, bucket: bucket, urlTTL: urlTTL}, nil
}

// newRustFSClient builds a minio client for endpoint with the shared static
// credentials. Region is set EXPLICITLY (RUSTFS_REGION, default us-east-1) rather
// than left empty: with no region, minio-go resolves the bucket region on first
// use via a GetBucketLocation round-trip -- including inside PresignedGetObject.
// That breaks the public presign client, whose endpoint (e.g. localhost:3702) is
// deliberately not reachable from the API process; a fixed region makes
// presigning fully offline (pure signature math) and matches the us-east-1 the
// signatures already carry.
func newRustFSClient(endpoint string, secure bool) (*minio.Client, error) {
	region := os.Getenv("RUSTFS_REGION")
	if region == "" {
		region = "us-east-1"
	}
	return minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(os.Getenv("RUSTFS_ACCESS_KEY"), os.Getenv("RUSTFS_SECRET_KEY"), ""),
		Secure: secure,
		Region: region,
	})
}

// EnsureBucket creates the export bucket if it does not exist. Called once at
// boot; safe to call repeatedly (a present bucket is a no-op).
//
// Note on CORS: the web app PUTs attachment bytes straight to RustFS via a
// presigned URL (the register -> PUT -> commit flow), and that cross-origin PUT
// is preflighted -- so RustFS must answer the OPTIONS with the right CORS
// headers. Unlike AWS S3/MinIO, RustFS has no per-bucket PutBucketCors
// subresource (rustfs/rustfs#1386), so there is nothing to set here: CORS is a
// server-level RustFS env (RUSTFS_CORS_ALLOWED_ORIGINS, wired in the compose
// file), not a bucket policy. Cross-origin GETs (<img> thumbnails) aren't
// preflighted, which is why downloads worked without any CORS config; the field
// device (mobile) PUTs over native HTTP, which has no CORS at all.
func (s *ObjectStore) EnsureBucket(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("checking bucket %q: %w", s.bucket, err)
	}
	if !exists {
		if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("creating bucket %q: %w", s.bucket, err)
		}
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
// local (no RustFS round-trip): it only computes a signature, so it is signed by
// presignClient -- bound to the public endpoint (RUSTFS_PUBLIC_ENDPOINT) when set
// -- so the host in the URL is one the downloader can reach, not an internal-only
// one like rustfs:9000.
func (s *ObjectStore) PresignGet(ctx context.Context, key, filename string) (string, error) {
	params := make(url.Values)
	params.Set("response-content-disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
	u, err := s.presignClient.PresignedGetObject(ctx, s.bucket, key, s.urlTTL, params)
	if err != nil {
		return "", fmt.Errorf("presigning download URL: %w", err)
	}
	return u.String(), nil
}

// PresignGetInline mints a time-limited GET URL for key with NO
// content-disposition, so RustFS serves the object inline under its stored
// Content-Type -- an image the web app drops straight into an <img>, or a
// thumbnail tile in the attachments grid. Use PresignGet instead when the intent
// is a download saved under a chosen filename (documents). Pure signature math on
// presignClient, like PresignGet.
func (s *ObjectStore) PresignGetInline(ctx context.Context, key string) (string, error) {
	u, err := s.presignClient.PresignedGetObject(ctx, s.bucket, key, s.urlTTL, nil)
	if err != nil {
		return "", fmt.Errorf("presigning inline URL: %w", err)
	}
	return u.String(), nil
}

// PresignPut mints a time-limited PUT URL for key, so a client (a browser, or a
// field device off the compose network) can upload the bytes straight to RustFS
// without proxying them through the API process. Like PresignGet it is pure
// signature math signed by presignClient -- bound to the public endpoint when set
// -- so the host in the URL is one the uploader can actually reach. The uploader
// PUTs the raw body to this URL; RustFS stores it under key. The two-step
// attachment flow (register -> PUT -> commit) uses this for the middle step.
func (s *ObjectStore) PresignPut(ctx context.Context, key string) (string, error) {
	u, err := s.presignClient.PresignedPutObject(ctx, s.bucket, key, s.urlTTL)
	if err != nil {
		return "", fmt.Errorf("presigning upload URL: %w", err)
	}
	return u.String(), nil
}

// Open returns a reader over the object at key; the caller must Close it. The
// commit path uses it to read a just-uploaded image back for thumbnailing. Note
// minio defers the real fetch to the first Read, so a missing key surfaces as a
// read error, not here.
func (s *ObjectStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	return obj, nil
}

// Stat returns the stored object's size and content type, erroring if the key
// does not exist. The commit path calls it to confirm the client's presigned PUT
// actually landed (and to record the true byte size) before marking a row ready.
func (s *ObjectStore) Stat(ctx context.Context, key string) (size int64, contentType string, err error) {
	info, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return 0, "", err
	}
	return info.Size, info.ContentType, nil
}
