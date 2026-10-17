package agenttools

// The External Links module's tool surface -- a feature's outbound links to
// external systems (a CIS/billing record, a maintenance work order, a document
// store, ...). A feature can carry arbitrarily many links, so this is the
// "many outbound links per feature" surface a per-class field could never model.
// Each tool is a thin adapter over an already-exported api/internal/externallink
// operation -- the same code the REST /feature-links routes and the feature
// panel's Links tab call. The module has no permission of its own: it RIDES the
// features permission, so the list tool reads (features:read) and the mutating
// tools write (features:write). The read tool carries nil annotations
// (buildRegistry stamps readOnlyHint:true); the write tools carry explicit
// annotations (readOnlyHint:false) so a client confirms with a human first.
//
// Only the four pure-DB operations are wrapped. The live api-link fetch
// (externallink.fetchLinkData) is intentionally absent: it needs a secretbox
// cipher and makes an outbound HTTP request, neither of which the stateless tool
// registry can supply.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/externallink"
	"github.com/uptrace/bun"
)

// externalLinkTools returns the External Links tools in advertised order: the
// read first (list, features:read), then the writes (create / update / delete,
// features:write).
func externalLinkTools() []Tool {
	return []Tool{
		{
			Name: "feature_link_list",
			Description: "List every external link attached to one feature, oldest-first -- the " +
				"same list the REST GET /feature-links route returns (the feature panel's Links " +
				"tab). Each entry carries the link's id (pass it to feature_link_update or " +
				"feature_link_delete), its kind ('hyperlink' with a url, or 'api' with a " +
				"catalogued system + resource path), its label, and -- for an api link -- the " +
				"resolved system code/name. Use this to see a feature's links before changing them.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code the feature belongs to."},
					"feature_id":{"type":"string","description":"The feature's id (uuid)."}
				},
				"required":["feature_class","feature_id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolFeatureLinkList,
		},
		{
			Name: "feature_link_create",
			Description: "Attach a new external link to a feature -- the same validated write the " +
				"REST POST /feature-links route performs (the Links tab's Add action). A label is " +
				"required, and the target feature must exist and not be deleted. kind defaults to " +
				"'hyperlink', which needs an absolute http/https url; kind 'api' instead needs a " +
				"catalogued system_id (metadata.external_systems) and an optional relative " +
				"resource_path resolved against that system's base URL. Returns the created link " +
				"(including its new id). This WRITES to the database: confirm with a human before " +
				"calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code the feature belongs to."},
					"feature_id":{"type":"string","description":"The feature's id (uuid)."},
					"kind":{"type":"string","enum":["hyperlink","api"],"description":"Link kind; defaults to 'hyperlink'."},
					"label":{"type":"string","description":"Required human-readable label for the link."},
					"url":{"type":"string","description":"For a hyperlink: the absolute http/https URL (required for kind 'hyperlink', ignored otherwise)."},
					"system_id":{"type":"string","description":"For an api link: the catalogued external system's id (uuid; required for kind 'api')."},
					"resource_path":{"type":"string","description":"For an api link: a relative path (no scheme/host) resolved against the system's base URL; empty fetches the base URL itself."}
				},
				"required":["feature_class","feature_id","label"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Add external link",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // additive: it attaches a link, removing nothing
				IdempotentHint:  &falseHint, // repeating it attaches another link
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolFeatureLinkCreate,
		},
		{
			Name: "feature_link_update",
			Description: "Rename an external link and/or repoint its target -- the same sparse write " +
				"the REST PATCH /feature-links/{id} route performs. Every field is optional and " +
				"only what you send is changed: send label to rename, url to repoint a hyperlink " +
				"(re-validated as http/https), or resource_path to repoint an api link (a link's " +
				"kind is fixed at creation, so url only applies to a hyperlink and resource_path " +
				"only to an api link). Returns the updated link. This WRITES to the database: " +
				"confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The link's id (uuid), e.g. from feature_link_list."},
					"label":{"type":"string","description":"New label (cannot be blank)."},
					"url":{"type":"string","description":"New absolute http/https URL; only valid on a hyperlink."},
					"resource_path":{"type":"string","description":"New relative resource path; only valid on an api link."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Update external link",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a field patch overwrites, it doesn't remove the link
				IdempotentHint:  &trueHint,  // re-applying the same patch yields the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolFeatureLinkUpdate,
		},
		{
			Name: "feature_link_delete",
			Description: "Permanently remove an external link by its id -- the same write the REST " +
				"DELETE /feature-links/{id} route performs (the Links tab's Remove action). A link " +
				"has no soft-delete/restore: this deletes the record outright, so it is DESTRUCTIVE " +
				"and NOT reversible -- to restore it you must add it again. Use feature_link_list to " +
				"find the id first. This WRITES to the database: confirm with a human before " +
				"calling. Deleting an id that doesn't exist returns a not-found error.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The link's id (uuid), e.g. from feature_link_list."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Delete external link",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // it deletes the link outright, no restore
				IdempotentHint:  &falseHint, // a second delete of the same id is a not-found error
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolFeatureLinkDelete,
		},
		{
			Name: "feature_external_refs_get",
			Description: "Read a feature's external_ref map -- the ids this feature carries in OTHER " +
				"systems (SCADA / AMI / CIS), keyed by system code (the same data the feature " +
				"panel's External references section shows). Distinct from feature_link_list: a " +
				"link is a stored pointer, while external_ref is the correlation source of truth " +
				"an inbound push resolves against and the live-data fetch builds its URL from. " +
				"Returns one entry per system, each with the correlation id (a scalar, or an " +
				"object ref's 'id' field) and the raw value. Scoped to a feature you can read.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code the feature belongs to."},
					"feature_id":{"type":"string","description":"The feature's id (uuid)."}
				},
				"required":["feature_class","feature_id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFeaturesRead,
			Handler:      toolFeatureExternalRefsGet,
		},
		{
			Name: "feature_external_refs_set",
			Description: "Replace a feature's WHOLE external_ref map -- the ids it carries in other " +
				"systems, keyed by system code (the same write the panel's External references " +
				"add/remove performs). This is a whole-map REPLACE, not a merge: send the full " +
				"next map, so to add or remove one reference first read the current map with " +
				"feature_external_refs_get, change it, and send the result (an empty map clears " +
				"every reference). Each value is either a scalar string id (e.g. \"48213\") or an " +
				"object whose 'id' field is the correlation id and whose other fields fill the " +
				"system's fetch template. Setting a reference makes the feature resolvable from " +
				"that system's id and, if the system has a fetch path, live-data-fetchable. " +
				"Returns the resulting map. This WRITES to the database: confirm with a human " +
				"before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code the feature belongs to."},
					"feature_id":{"type":"string","description":"The feature's id (uuid)."},
					"external_ref":{
						"type":"object",
						"description":"The full replacement map, keyed by external system code; each value is a string id or an object with an 'id' field. An empty object clears all references.",
						"additionalProperties":{"type":["string","object"]}
					}
				},
				"required":["feature_class","feature_id","external_ref"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Set feature external references",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // a whole-map replace can drop existing references
				IdempotentHint:  &trueHint,  // sending the same map yields the same state
				OpenWorldHint:   &falseHint, // writes only this system's own database (no outbound call)
			},
			RequiredPerm: auth.PermFeaturesWrite,
			Handler:      toolFeatureExternalRefsSet,
		},
	}
}

// --- handlers ---

func toolFeatureLinkList(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string `json:"feature_class"`
		FeatureID    string `json:"feature_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" || in.FeatureID == "" {
		return nil, fmt.Errorf("feature_class and feature_id are required")
	}
	return externallink.ListLinks(ctx, db, in.FeatureClass, in.FeatureID)
}

func toolFeatureLinkCreate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in externallink.CreateLinkInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	// The engine re-validates the target feature, the required label, and the
	// kind-specific target (http/https url, or catalogued system + relative
	// resource path); its 400s surface in-band as the tool error.
	return externallink.CreateLink(ctx, db, in)
}

func toolFeatureLinkUpdate(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	// Peel the id off the same body the patch fields ride in.
	var id struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &id); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if id.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	var in externallink.UpdateLinkInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	// The engine applies only the present fields and enforces the kind/url and
	// kind/resource_path pairing; its 400s surface in-band as the tool error.
	return externallink.UpdateLink(ctx, db, id.ID, in)
}

func toolFeatureLinkDelete(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	if err := externallink.DeleteLink(ctx, db, in.ID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "id": in.ID}, nil
}

func toolFeatureExternalRefsGet(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string `json:"feature_class"`
		FeatureID    string `json:"feature_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" || in.FeatureID == "" {
		return nil, fmt.Errorf("feature_class and feature_id are required")
	}
	return externallink.ListFeatureExternalRefs(ctx, db, in.FeatureClass, in.FeatureID)
}

func toolFeatureExternalRefsSet(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string                     `json:"feature_class"`
		FeatureID    string                     `json:"feature_id"`
		ExternalRef  map[string]json.RawMessage `json:"external_ref"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" || in.FeatureID == "" {
		return nil, fmt.Errorf("feature_class and feature_id are required")
	}
	// A nil external_ref (key omitted) is rejected by the required-field schema;
	// an explicit empty object reaches here as a non-nil empty map and clears the
	// references, which the engine handles (whole-map replace).
	if in.ExternalRef == nil {
		in.ExternalRef = map[string]json.RawMessage{}
	}
	// The engine validates each value's shape and enforces write scope; its 400s/
	// 403s surface in-band as the tool error.
	return externallink.SetFeatureExternalRefs(ctx, db, in.FeatureClass, in.FeatureID, in.ExternalRef)
}
