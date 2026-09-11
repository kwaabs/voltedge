// Package attachment is the domain layer for files attached to features (Phase
// 22, docs/phase-22-attachments.md). A feature of any class can carry supporting
// files -- field photos, and on the web documents like PDFs/CAD -- whose bytes
// live in RustFS while this row is the source of truth for what is attached to
// what.
//
// Slice 1 is the foundation: the persistence model, the RustFS key layout, and
// the on-server thumbnail generator. The HTTP surface (register -> presigned PUT
// -> commit, list, delete) arrives in Slice 2.
package attachment

import (
	"time"

	"github.com/uptrace/bun"
)

// Kind partitions attachments by how the UI treats them: an image is previewable
// and gets a thumbnail; a document is download-only. The mobile field tool only
// captures images; the web accepts both.
const (
	KindImage    = "image"
	KindDocument = "document"
)

// Status models the two-step presigned upload. A row is Pending from the moment
// the client registers it (and receives a presigned PUT URL) until it commits the
// finished upload, at which point it is Ready. A Pending row whose upload never
// lands is reaped by a later sweep, so a half-finished upload never shows.
const (
	StatusPending = "pending"
	StatusReady   = "ready"
)

// Attachment is one file hung off a feature. The feature is addressed by the
// (FeatureClass code, FeatureID uuid) pair because features have no shared table
// -- each class compiles to its own gis.<code> table. ThumbKey is set for images
// only. ID is client-mintable so an offline field device can create the row with
// its own uuid (idempotent replay, like create-feature).
type Attachment struct {
	bun.BaseModel `bun:"table:files.attachments,alias:att"`

	ID           string     `bun:"id,pk" json:"id"`
	FeatureClass string     `bun:"feature_class" json:"feature_class"`
	FeatureID    string     `bun:"feature_id" json:"feature_id"`
	ObjectKey    string     `bun:"object_key" json:"-"`
	ThumbKey     *string    `bun:"thumb_key" json:"-"`
	Filename     string     `bun:"filename" json:"filename"`
	ContentType  string     `bun:"content_type" json:"content_type"`
	ByteSize     int64      `bun:"byte_size" json:"byte_size"`
	Kind         string     `bun:"kind" json:"kind"`
	Status       string     `bun:"status" json:"status"`
	UploadedBy   *string    `bun:"uploaded_by" json:"uploaded_by,omitempty"`
	CreatedAt    time.Time  `bun:"created_at" json:"created_at"`
	UpdatedAt    time.Time  `bun:"updated_at" json:"updated_at"`
	DeletedAt    *time.Time `bun:"deleted_at" json:"-"`
}

// ObjectKey is the RustFS key an attachment's full bytes live under, in the
// shared export bucket. Namespaced by feature id then attachment id -- one object
// per attachment -- so a feature's files group together and reaping a feature is
// a prefix delete. The original filename is NOT in the key (it rides the metadata
// row and the download's content-disposition), so no filename sanitizing is
// needed and odd characters can never break a key.
func ObjectKey(featureID, id string) string {
	return "attachments/" + featureID + "/" + id
}

// ThumbKey is the RustFS key an image attachment's downscaled JPEG thumbnail
// lives under -- the full object's key with a .thumb.jpg suffix, so it sits
// beside the original under the same feature prefix.
func ThumbKey(featureID, id string) string {
	return "attachments/" + featureID + "/" + id + ".thumb.jpg"
}
