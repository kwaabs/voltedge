package attachment

// agentexports.go is the thin exported surface the agenttools registry adapts
// onto -- the package-private attachment operations that the REST handlers also
// call, re-exposed as exported wrappers so api/internal/agenttools can drive
// them without reaching into this package's unexported functions. Every wrapper
// is a straight pass-through to the private op and keeps the concrete *bun.DB
// the underlying queries take.
//
// DELIBERATELY EXCLUDED -- only the two PURE-DB operations (listReady,
// softDelete) are wrapped here. The register / commit / url flows are NOT, and
// cannot be, exposed as agent tools:
//   - register mints a presigned PUT url,
//   - commit reads the uploaded bytes back from the object store to build and
//     upload a thumbnail,
//   - url mints a presigned GET url.
// All three depend on the object store client (*export.ObjectStore). The
// agenttools registry's Execute choke point only holds (ctx, db, user, args) --
// it has no object-store handle -- so those presign/byte-copy flows can't be
// modelled as pure JSON-in/JSON-out tools and stay REST-only. What ListForFeature
// returns is therefore raw metadata rows (no presigned thumb URLs, which the
// REST handler layers on separately) -- honest metadata-only output.

import (
	"context"

	"github.com/uptrace/bun"
)

// ListForFeature returns a feature's committed (ready) attachments as metadata
// rows, newest first -- the raw listReady result, with no presigned thumbnail
// URLs layered on (that's an object-store concern the REST handler owns).
func ListForFeature(ctx context.Context, db *bun.DB, featureClass, featureID string) (any, error) {
	return listReady(ctx, db, featureClass, featureID)
}

// SoftDelete hides one attachment by id (sets deleted_at). The stored object
// bytes are left in place for the background retention sweep to reap later,
// mirroring how feature deletes work. Idempotent: an already-deleted row is a
// no-op.
func SoftDelete(ctx context.Context, db *bun.DB, id string) error {
	return softDelete(ctx, db, id)
}
