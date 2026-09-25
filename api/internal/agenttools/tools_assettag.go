package agenttools

// The asset-tagging module's tools -- the scannable-tag surface exposed through
// the same single agenttools registry as every other activity, closing the
// [[mcp-frontend-parity]] mandate for the tagging module (S6). Every feature
// carries one or more short tag codes (rendered as a QR + Code128 + text label
// and affixed on the physical asset), and a scan resolves a code back to the GIS
// record. Each tool here is a thin adapter over an already-exported
// api/internal/assettag engine function -- the same code the REST /tags routes,
// the feature-panel Tags tab, the web /t/{code} deep link, and the Flutter field
// scanner call -- and declares the RBAC permission that route requires: the GET
// verbs read (tags:read), the mutating verbs write (tags:write). The read tools
// carry nil annotations (buildRegistry stamps readOnlyHint:true); the write
// tools carry explicit annotations (readOnlyHint:false) so a client confirms
// with a human first.
//
// Minting is NOT a tool: every feature is auto-tagged by the DB trigger on
// runtime.feature_registry (migration 0114), never over any front door. What is
// exposed is the read + lifecycle side -- resolve a scanned code, list a
// feature's tags, register a legacy/external number, set the primary, void,
// reissue, and mark printed.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/assettag"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/crud"
	"github.com/kwaabs/voltedge/api/internal/featureregistry"
	"github.com/kwaabs/voltedge/api/internal/metadata"
	"github.com/uptrace/bun"
)

// tagTools returns the asset-tag tools in advertised order: the reads first
// (resolve / list), then the writes (register / set-primary / void / reissue /
// mark-printed).
func tagTools() []Tool {
	return []Tool{
		{
			Name: "resolve_asset_tag",
			Description: "Resolve a scanned asset-tag code back to its feature -- exactly what a field scan " +
				"of the QR/barcode on a plant label does, the same read the REST GET /tags?code= route " +
				"returns. The code may be the bare tag code or the value carried by the tag's QR (the web " +
				"deep link <origin>/t/<code>); pass the bare code. Applies the same read gate a feature GET " +
				"applies -- territory scope, private-draft visibility, the pending-edit overlay -- so a code " +
				"the caller may not see answers not-found, never revealing an asset out of scope. Voided " +
				"(retired) labels answer not-found; active and superseded codes resolve. Returns the exact " +
				"tag scanned, the full feature it points to, and every tag on that feature (so you can say " +
				"'you scanned X; the primary is Y'). Read-only.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"code":{"type":"string","description":"The scanned tag code (the bare code, or the value from the tag's QR deep link)."}
				},
				"required":["code"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermTagsRead,
			Handler:      toolResolveAssetTag,
		},
		{
			Name: "list_feature_tags",
			Description: "List every asset tag on a feature -- the feature-panel Tags tab's data, the same " +
				"read the REST GET /tags?feature_class_code=&feature_id= route returns. A feature may hold " +
				"many codes: the system-minted one plus any pre-existing asset numbers a utility already " +
				"paints on its plant. Exactly one is primary (the one that gets printed). Each row is the " +
				"full tag (code, source generated|external, is_primary, status, print_count). Read-only.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class_code":{"type":"string","description":"The feature's class code (pair with feature_id)."},
					"feature_id":{"type":"string","description":"The feature's id (uuid; pair with feature_class_code)."}
				},
				"required":["feature_class_code","feature_id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermTagsRead,
			Handler:      toolListFeatureTags,
		},
		{
			Name: "primary_asset_tags",
			Description: "Get the one printable (primary) tag for each of many features of ONE class -- the " +
				"data behind the /features browser's bulk label print, the same read the REST GET " +
				"/tags?feature_class_code=&feature_ids= route returns. Only active tags are eligible; a " +
				"feature's primary wins, else its most-recent active tag stands in. A feature with no active " +
				"tag is simply absent from the result, so the returned count may be less than the number of " +
				"ids you asked for. Read-only.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class_code":{"type":"string","description":"The class code all the features share."},
					"feature_ids":{"type":"array","items":{"type":"string"},"description":"The feature ids (uuids) to fetch a printable tag for."}
				},
				"required":["feature_class_code","feature_ids"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermTagsRead,
			Handler:      toolPrimaryAssetTags,
		},
		{
			Name: "find_features_by_tag",
			Description: "Find every feature of ONE class by its asset tag -- the query engine's tag filter, the " +
				"same read the REST POST /{feature_class}/query route runs with a tag predicate. Beyond a " +
				"single scanned code (resolve_asset_tag), this searches across a class: match a tag_code " +
				"exactly, or as a case-insensitive prefix (match=prefix) for a partial scan or typed fragment; " +
				"narrow to source generated|external; or omit code entirely and use present to list features " +
				"that DO (present=true, the default) or do NOT (present=false) carry a matching tag -- e.g. " +
				"source=external, present=false finds plant still missing its external asset number. Each " +
				"returned feature envelope carries its primary_tag_code. Applies the same territory read " +
				"scope, private-draft visibility, and soft-delete exclusion as every other read; voided tags " +
				"never match. Returns at most 500. Read-only.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature class code to search."},
					"code":{"type":"string","description":"A tag_code to match (exact by default). Omit for a pure presence test."},
					"match":{"type":"string","enum":["exact","prefix"],"description":"How to match code: exact (default) or a case-insensitive prefix."},
					"source":{"type":"string","enum":["generated","external"],"description":"Restrict to system-minted (generated) or legacy (external) tags; omit for both."},
					"present":{"type":"boolean","description":"true (default) = features that HAVE a matching tag; false = features that have NONE."},
					"limit":{"type":"integer","description":"Max features to return, 1-500 (engine default when omitted)."}
				},
				"required":["feature_class"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermFeaturesRead,
			ExtraPerms:   []string{auth.PermTagsRead},
			Handler:      toolFindFeaturesByTag,
		},
		{
			Name: "list_asset_tags",
			Description: "Browse and search the whole asset-tag population -- the data behind the Admin -> Asset " +
				"Tags console, the same read the REST GET /tags?view=list route returns. Unlike list_feature_tags " +
				"(one feature) or resolve_asset_tag (one scanned code), this pages across every tag: filter by a " +
				"case-insensitive tag_code substring (search), by lifecycle status (active|void|superseded), by " +
				"source (generated|external), by feature class (feature_class_code), or to only the printed code " +
				"per feature (primary_only). Applies the same territory + private-draft read scope as every other " +
				"read, so it lists only tags whose feature the caller could resolve. Returns one page {rows, total, " +
				"limit, offset}; page with limit (1-200, default 50) and offset. Read-only.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"search":{"type":"string","description":"Case-insensitive substring match on the tag code."},
					"status":{"type":"string","enum":["active","void","superseded"],"description":"Restrict to one lifecycle status; omit for all."},
					"source":{"type":"string","enum":["generated","external"],"description":"Restrict to system-minted (generated) or legacy (external) tags; omit for both."},
					"feature_class_code":{"type":"string","description":"Restrict to one feature class."},
					"primary_only":{"type":"boolean","description":"When true, keep only the single printed (primary) code per feature."},
					"limit":{"type":"integer","description":"Page size, 1-200 (default 50)."},
					"offset":{"type":"integer","description":"Rows to skip for paging (default 0)."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermTagsRead,
			Handler:      toolListAssetTags,
		},
		{
			Name: "register_asset_tag",
			Description: "Register a pre-existing / legacy asset number on a feature -- the same write the REST " +
				"POST /tags route performs (the Tags tab's 'Register existing number' action). Use this for a " +
				"brownfield asset a utility already labels with its own number of record, so that number also " +
				"resolves; the code is recorded source='external'. Set make_primary to promote it over the " +
				"system-minted code (the promoted code is the one that gets printed). Returns the created " +
				"tag. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class_code":{"type":"string","description":"The feature's class code (pair with feature_id)."},
					"feature_id":{"type":"string","description":"The feature's id (uuid; pair with feature_class_code)."},
					"tag_code":{"type":"string","description":"The legacy/external code to record on the feature."},
					"make_primary":{"type":"boolean","description":"When true, promote this code to the feature's primary (the printed one)."}
				},
				"required":["feature_class_code","feature_id","tag_code"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Register asset tag",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // additive: it records a code, removing nothing (void it to retire)
				IdempotentHint:  &falseHint, // re-registering the same code on the feature is rejected
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermTagsWrite,
			Handler:      toolRegisterAssetTag,
		},
		{
			Name: "set_primary_asset_tag",
			Description: "Promote a tag to its feature's primary -- the same write the REST POST /tags/{code}/primary " +
				"route performs. Exactly one tag on a feature is primary (the one that gets printed); promoting " +
				"one demotes the previous primary, atomically. Idempotent: promoting the tag that is already " +
				"primary re-applies the same state. Returns the updated tag. This WRITES to the database: " +
				"confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"code":{"type":"string","description":"The tag code to promote to primary."}
				},
				"required":["code"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Set primary asset tag",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // a primary flip, reversible by promoting another
				IdempotentHint:  &trueHint,  // promoting the current primary re-applies the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermTagsWrite,
			Handler:      toolSetPrimaryAssetTag,
		},
		{
			Name: "void_asset_tag",
			Description: "Void (retire) an asset tag -- the same write the REST POST /tags/{code}/void route " +
				"performs. A voided code no longer resolves (a scan of a retired label answers not-found), so " +
				"use this when a label is destroyed or a legacy number is decommissioned. The feature keeps " +
				"its other codes; voiding the primary leaves the feature with no primary until another is " +
				"promoted. Returns the updated (voided) tag. This WRITES to the database: confirm with a " +
				"human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"code":{"type":"string","description":"The tag code to void (retire)."}
				},
				"required":["code"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Void asset tag",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // retires a code: scans of it stop resolving
				IdempotentHint:  &trueHint,  // voiding an already-void code re-applies the same state
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermTagsWrite,
			Handler:      toolVoidAssetTag,
		},
		{
			Name: "reissue_asset_tag",
			Description: "Reissue a fresh code for an asset, superseding the old one -- the same write the REST " +
				"POST /tags/{code}/reissue route performs. Use this when a printed label is damaged: it mints " +
				"a new code for the same feature and marks the old one superseded (the old label still resolves " +
				"while the field is re-labelled). The new code inherits the old one's primary status. Returns " +
				"the new tag. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"code":{"type":"string","description":"The existing tag code to reissue (it becomes superseded)."}
				},
				"required":["code"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Reissue asset tag",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // additive: mints a new code, the old one is superseded not deleted
				IdempotentHint:  &falseHint, // each call mints another fresh code
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermTagsWrite,
			Handler:      toolReissueAssetTag,
		},
		{
			Name: "mark_asset_tag_printed",
			Description: "Record that an asset tag's label was sent to print -- the same write the REST POST " +
				"/tags/{code}/printed route performs (the label-print sheet's book-keeping). It bumps the " +
				"tag's print_count so a crew can tell a freshly-generated label from one already produced. " +
				"Returns the updated tag. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"code":{"type":"string","description":"The tag code whose label was printed."}
				},
				"required":["code"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Mark asset tag printed",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &falseHint, // only bumps a counter, removes nothing
				IdempotentHint:  &falseHint, // each call bumps print_count again
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermTagsWrite,
			Handler:      toolMarkAssetTagPrinted,
		},
	}
}

// --- handlers ---

func toolResolveAssetTag(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.Code == "" {
		return nil, fmt.Errorf("code is required")
	}
	// Resolve owns the scope/draft-aware read via crud.GetFeatureScoped, so it needs
	// the concrete *bun.DB; the acting user is on ctx for the scope gate.
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return assettag.Resolve(ctx, sdb, in.Code)
}

func toolListFeatureTags(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClassCode string `json:"feature_class_code"`
		FeatureID        string `json:"feature_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClassCode == "" || in.FeatureID == "" {
		return nil, fmt.Errorf("feature_class_code and feature_id are required")
	}
	tags, err := assettag.ListForFeature(ctx, db, in.FeatureClassCode, in.FeatureID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"tags": tags, "count": len(tags)}, nil
}

func toolPrimaryAssetTags(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClassCode string   `json:"feature_class_code"`
		FeatureIDs       []string `json:"feature_ids"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClassCode == "" || len(in.FeatureIDs) == 0 {
		return nil, fmt.Errorf("feature_class_code and feature_ids are required")
	}
	tags, err := assettag.PrimaryForFeatures(ctx, db, in.FeatureClassCode, in.FeatureIDs)
	if err != nil {
		return nil, err
	}
	return map[string]any{"tags": tags, "count": len(tags)}, nil
}

func toolFindFeaturesByTag(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string `json:"feature_class"`
		Code         string `json:"code"`
		Match        string `json:"match"`
		Source       string `json:"source"`
		Present      *bool  `json:"present"`
		Limit        int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.FeatureClass == "" {
		return nil, fmt.Errorf("feature_class is required")
	}
	loaded, err := metadata.Load(ctx, db, in.FeatureClass)
	if err != nil {
		return nil, fmt.Errorf("no such feature class %q: %w", in.FeatureClass, err)
	}
	rows, err := crud.FindFeaturesByTag(ctx, db, loaded, crud.TagQuery{
		Code:    in.Code,
		Match:   in.Match,
		Source:  in.Source,
		Present: in.Present,
		Limit:   in.Limit,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"feature_class": in.FeatureClass,
		"features":      rows,
		"count":         len(rows),
	}, nil
}

func toolListAssetTags(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Search           string `json:"search"`
		Status           string `json:"status"`
		Source           string `json:"source"`
		FeatureClassCode string `json:"feature_class_code"`
		PrimaryOnly      bool   `json:"primary_only"`
		Limit            int    `json:"limit"`
		Offset           int    `json:"offset"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	// ResolveScope reads the acting user's territory/draft scope off ctx and needs
	// the concrete *bun.DB, as List's registry join does.
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	scope, err := featureregistry.ResolveScope(ctx, sdb)
	if err != nil {
		return nil, err
	}
	return assettag.List(ctx, sdb, assettag.ListFilter{
		Search:      in.Search,
		Status:      in.Status,
		Source:      in.Source,
		Class:       in.FeatureClassCode,
		PrimaryOnly: in.PrimaryOnly,
		Limit:       in.Limit,
		Offset:      in.Offset,
	}, scope)
}

func toolRegisterAssetTag(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in assettag.RegisterInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	// Register owns its transaction (the at-most-one-primary invariant), so it needs
	// the concrete *bun.DB.
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	// The engine re-validates the class/id/code and rejects a duplicate; its errors
	// surface in-band as the tool error. The acting user stamps created_by.
	return assettag.Register(ctx, sdb, in, auth.ActorID(ctx))
}

func toolSetPrimaryAssetTag(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.Code == "" {
		return nil, fmt.Errorf("code is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return assettag.SetPrimary(ctx, sdb, in.Code, auth.ActorID(ctx))
}

func toolVoidAssetTag(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.Code == "" {
		return nil, fmt.Errorf("code is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return assettag.Void(ctx, sdb, in.Code, auth.ActorID(ctx))
}

func toolReissueAssetTag(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.Code == "" {
		return nil, fmt.Errorf("code is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return assettag.Reissue(ctx, sdb, in.Code, auth.ActorID(ctx))
}

func toolMarkAssetTagPrinted(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.Code == "" {
		return nil, fmt.Errorf("code is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return assettag.MarkPrinted(ctx, sdb, in.Code)
}
