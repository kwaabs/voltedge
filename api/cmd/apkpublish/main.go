// Command apkpublish uploads a built, signed field-app APK to the RustFS app
// bucket and updates the latest.json release manifest the /app endpoints serve.
// It's the "publish a new version" half of the self-distribution ("our own play
// store") flow; the other half is `flutter build apk --release`.
//
// Usage (reads the same RUSTFS_* env the API uses):
//
//	go run ./cmd/apkpublish \
//	  -apk ../mobile/build/app/outputs/flutter-apk/app-release.apk \
//	  -version 1.2.0 [-version-code 5] [-notes "What changed"]
//
// It computes the APK's sha256 + size, PUTs it under releases/<version>/, then
// overwrites latest.json so /app/latest and /app/download point at it.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/kwaabs/voltedge/api/internal/appdist"
	"github.com/kwaabs/voltedge/api/internal/export"
)

func main() {
	apkPath := flag.String("apk", "", "path to the signed release APK (required)")
	version := flag.String("version", "", "release version name, e.g. 1.2.0 (required)")
	versionCode := flag.Int("version-code", 0, "release version code (optional, integer)")
	notes := flag.String("notes", "", "release notes shown on the download page (optional)")
	flag.Parse()

	if *apkPath == "" || *version == "" {
		flag.Usage()
		log.Fatal("apkpublish: -apk and -version are required")
	}

	ctx := context.Background()
	store, err := export.NewObjectStoreForBucket(appdist.Bucket)
	if err != nil {
		log.Fatalf("apkpublish: object store: %v", err)
	}
	if err := store.EnsureBucket(ctx); err != nil {
		log.Fatalf("apkpublish: ensure bucket %q: %v", appdist.Bucket, err)
	}

	f, err := os.Open(*apkPath)
	if err != nil {
		log.Fatalf("apkpublish: open apk: %v", err)
	}
	defer f.Close()

	// Hash + size by streaming the file once, then rewind and upload it -- no need
	// to hold the whole APK in memory.
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		log.Fatalf("apkpublish: hashing apk: %v", err)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		log.Fatalf("apkpublish: rewinding apk: %v", err)
	}

	filename := fmt.Sprintf("voltedge-field-%s.apk", *version)
	objectKey := fmt.Sprintf("releases/%s/%s", *version, filename)
	if _, err := store.Upload(ctx, objectKey, f, appdist.APKContentType); err != nil {
		log.Fatalf("apkpublish: upload apk: %v", err)
	}

	m := appdist.Manifest{
		Version:     *version,
		VersionCode: *versionCode,
		ObjectKey:   objectKey,
		Filename:    filename,
		SizeBytes:   size,
		SHA256:      sum,
		Notes:       *notes,
		ReleasedAt:  time.Now().UTC(),
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		log.Fatalf("apkpublish: marshal manifest: %v", err)
	}
	if _, err := store.Upload(ctx, appdist.ManifestKey, bytes.NewReader(raw), "application/json"); err != nil {
		log.Fatalf("apkpublish: upload manifest: %v", err)
	}

	log.Printf("apkpublish: published %s (%d bytes, sha256 %s) -> %s/%s; latest.json updated",
		*version, size, sum[:12], appdist.Bucket, objectKey)
}
