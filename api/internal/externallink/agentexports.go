package externallink

// Agent/MCP export layer. The externallink operations (createLink /
// listLinks / updateLink / deleteLink) are package-private, so this file
// re-exports them as thin `any`-returning wrappers the stateless
// api/internal/agenttools registry can adapt into feature_link_* tools --
// the same code the REST /feature-links routes and the feature panel's
// Links tab call. The request bodies are re-exported as type aliases so a
// handler outside this package can unmarshal straight into them.
//
// fetchLinkData is deliberately NOT re-exported: it needs a live
// *secretbox.Cipher (to decrypt a system's stored credentials) and makes
// an outbound HTTP fetch through the proxy. The stateless tool registry
// has no cipher to supply and the tool surface is DB-only, so the live
// api-link fetch stays out of scope -- only the four pure-DB CRUD
// operations are exposed.

import (
	"context"
	"encoding/json"

	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/uptrace/bun"
)

// CreateLinkInput is the create body (feature_class, feature_id, kind,
// label, url / system_id + resource_path), re-exported for the agent
// tool handler.
type CreateLinkInput = createRequest

// UpdateLinkInput is the sparse patch body (label, url, resource_path;
// all optional), re-exported for the agent tool handler.
type UpdateLinkInput = updateRequest

// CreateLink records one external link on a feature after validating the
// target feature exists and the kind-specific target (http/https url, or
// a catalogued system + relative resource path).
func CreateLink(ctx context.Context, db bun.IDB, req CreateLinkInput) (any, error) {
	return createLink(ctx, db, req)
}

// ListLinks returns every link attached to one feature, newest last.
func ListLinks(ctx context.Context, db bun.IDB, featureClass, featureID string) (any, error) {
	return listLinks(ctx, db, featureClass, featureID)
}

// UpdateLink renames a link and/or repoints its target; only the fields
// present in the request change.
func UpdateLink(ctx context.Context, db bun.IDB, id string, req UpdateLinkInput) (any, error) {
	return updateLink(ctx, db, id, req)
}

// DeleteLink permanently removes one link (no soft-delete). Returns only
// an error; the caller reports the deleted id it already holds.
func DeleteLink(ctx context.Context, db bun.IDB, id string) error {
	return deleteLink(ctx, db, id)
}

// ListFeatureExternalRefs returns a feature's whole external_ref map (the
// migration-0145 jsonb of this feature's ids in other systems), one entry per
// system. Scoped to a feature the caller can read (same not-found answer a read
// gives), so a hidden feature's foreign ids don't leak. The live-data FETCH of a
// ref is deliberately NOT exported here, for the same reason as fetchLinkData
// above: it needs a *secretbox.Cipher and an outbound HTTP call the stateless
// tool registry can't supply -- the MCP surface stays DB-only.
func ListFeatureExternalRefs(ctx context.Context, db bun.IDB, featureClass, featureID string) (any, error) {
	loaded, err := loadFeatureClass(ctx, db, featureClass)
	if err != nil {
		return nil, err
	}
	if err := crud.EnforceFeatureReadScope(ctx, db, loaded, featureID); err != nil {
		if crud.IsNotFoundError(err) {
			return nil, notFound("feature %s (%s) not found", featureID, featureClass)
		}
		return nil, err
	}
	return listFeatureExternalRefs(ctx, db, loaded, featureID)
}

// SetFeatureExternalRefs replaces a feature's whole external_ref map (whole-map
// replace, not a merge; an empty map clears it). Write-scoped (features:write
// plus the Phase 58 territory guard). The reverse-index trigger re-derives
// runtime.external_feature_refs from the new value on the write.
func SetFeatureExternalRefs(ctx context.Context, db bun.IDB, featureClass, featureID string, refs map[string]json.RawMessage) (any, error) {
	loaded, err := loadFeatureClass(ctx, db, featureClass)
	if err != nil {
		return nil, err
	}
	if err := crud.EnforceFeatureWriteScope(ctx, db, loaded, featureID); err != nil {
		if crud.IsNotFoundError(err) {
			return nil, notFound("feature %s (%s) not found", featureID, featureClass)
		}
		return nil, err
	}
	return setFeatureExternalRef(ctx, db, loaded, featureID, refs)
}
