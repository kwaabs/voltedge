package agenttools

// The Attachment module's tools -- the file-attachment surface exposed through
// the same single agenttools registry as every other activity. An attachment is
// a file (a field photo, or on the web a PDF / CAD document) hung off a feature,
// whose bytes live in the object store while a files.attachments row is the
// source of truth for what is attached to what.
//
// Only the two PURE-DB operations are wrappable here: attachment_list (metadata
// listing) and attachment_delete (soft-delete). The module's register / commit /
// url flows are OBJECT-STORE operations -- register mints a presigned PUT url,
// commit reads the uploaded bytes back to build+upload a thumbnail, url mints a
// presigned GET url -- and all depend on the *export.ObjectStore client. The
// agenttools registry's Execute only holds (ctx, db, user, args) and has no
// object-store handle, so those presign/byte-copy flows can't be modelled as
// pure JSON tools and are intentionally OUT OF SCOPE here (they stay REST-only).
//
// Because attachment.ListForFeature / attachment.SoftDelete take the concrete
// *bun.DB, both handlers narrow the incoming bun.IDB with asDB first. The read
// tool carries nil annotations (buildRegistry stamps readOnlyHint:true); the
// delete tool carries explicit annotations (readOnlyHint:false + destructiveHint:
// true) so a client confirms with a human first.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/attachment"
	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/uptrace/bun"
)

// attachmentTools returns the Attachment tools in advertised order: the read
// (list) first, then the write (delete).
func attachmentTools() []Tool {
	return []Tool{
		{
			Name: "attachment_list",
			Description: "List a feature's committed file attachments newest-first -- the field photos and " +
				"documents hung off it, the metadata behind the attachments panel. Both feature_class (the " +
				"class code) and feature_id (its uuid) are required, because features share no table (each " +
				"class compiles to its own gis.<code> table). Returns attachment metadata rows only " +
				"(filename, content type, byte size, kind, status, timestamps); in-flight/abandoned uploads " +
				"are excluded, and no download or thumbnail URLs are minted here (that's an object-store " +
				"concern the REST route handles).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The feature's class code (pair with feature_id)."},
					"feature_id":{"type":"string","description":"The feature's id (uuid; pair with feature_class)."}
				},
				"required":["feature_class","feature_id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermAttachmentsRead,
			Handler:      toolAttachmentList,
		},
		{
			Name: "attachment_delete",
			Description: "Soft-delete a file attachment by its id -- hides it from the feature's attachments " +
				"panel (sets deleted_at), the same write the REST DELETE route performs. The stored file " +
				"bytes are NOT removed here; they are reaped asynchronously by the background retention " +
				"sweep later, mirroring how feature deletes work. Idempotent: deleting an already-deleted " +
				"attachment is a no-op. This WRITES to the database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"The attachment's id (uuid)."}
				},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Delete attachment",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // it removes the attachment from active use (bytes reaped later)
				IdempotentHint:  &trueHint,  // deleting an already-deleted row is a no-op
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermAttachmentsWrite,
			Handler:      toolAttachmentDelete,
		},
	}
}

// --- handlers ---

func toolAttachmentList(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
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
	// listReady takes the concrete *bun.DB, so narrow the incoming handle first.
	db2, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return attachment.ListForFeature(ctx, db2, in.FeatureClass, in.FeatureID)
}

func toolAttachmentDelete(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	// softDelete takes the concrete *bun.DB, so narrow the incoming handle first.
	db2, err := asDB(db)
	if err != nil {
		return nil, err
	}
	if err := attachment.SoftDelete(ctx, db2, in.ID); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "id": in.ID}, nil
}
