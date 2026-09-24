package assettag

import (
	"context"
	"database/sql"
	"errors"

	"github.com/uptrace/bun"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/metadata"
)

// Resolution is what a scan returns: the exact tag scanned, the feature it points
// to (the full read envelope, identical to GET /features/{code}/{id}), and every
// tag on that feature (so a field view can show "you scanned X; the primary is
// Y"). feature is nil only alongside an error.
type Resolution struct {
	Tag     *Tag           `json:"tag"`
	Feature map[string]any `json:"feature"`
	Tags    []Tag          `json:"tags"`
}

// Resolve turns a scanned code into its feature, applying the SAME read gate a
// normal feature GET applies -- territory scope (Phase 58), private-draft
// visibility (Phase 61), and the pending-edit overlay (Phase 61 B) -- via
// crud.GetFeatureScoped. A code the caller may not see resolves to a NotFound,
// exactly as an out-of-scope feature GET answers 404, so a scan never reveals the
// existence of an asset the scanner has no rights to. Voided codes (retired
// labels) also answer NotFound; active and superseded codes resolve (a superseded
// label still hangs on a real asset while the field is re-tagged).
func Resolve(ctx context.Context, db *bun.DB, code string) (*Resolution, error) {
	tag, err := LookupByCode(ctx, db, code)
	if err != nil {
		return nil, err
	}
	if tag == nil {
		return nil, notFoundf("no asset tag %q", code)
	}
	if tag.Status == "void" {
		// Retired/wrong label: don't resolve it to an asset. Distinct message
		// (the caller already holds tags:read, so telling an authenticated user a
		// scanned label is retired leaks nothing an unknown-code 404 wouldn't).
		return nil, notFoundf("asset tag %q has been retired", code)
	}

	loaded, err := metadata.Load(ctx, db, tag.FeatureClassCode)
	if err != nil {
		// The tag's class was uncompiled/removed: treat as not-found rather than a
		// 500, since the asset it named is no longer resolvable.
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFoundf("no asset tag %q", code)
		}
		return nil, err
	}

	feature, err := crud.GetFeatureScoped(ctx, db, loaded, tag.FeatureID)
	if err != nil {
		// Scope/draft-hidden or purged -> hide as not-found, never leak existence.
		if crud.IsNotFoundError(err) {
			return nil, notFoundf("no asset tag %q", code)
		}
		return nil, err
	}

	tags, err := ListForFeature(ctx, db, tag.FeatureClassCode, tag.FeatureID)
	if err != nil {
		return nil, err
	}

	return &Resolution{Tag: tag, Feature: feature, Tags: tags}, nil
}
