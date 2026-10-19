package agenttools

// The operational-safety module's tools -- tags, grounds and clearances, exposed
// through the same single agenttools registry as every other activity. Each tool
// is a thin adapter over an already-exported api/internal/safety function (the
// same code the REST /safety routes and the feature-panel Safety tab call), gated
// by the capability that route needs: GET-style reads (safety:read), the
// place/remove/issue/surrender writes (safety:write). A BLOCKING tag
// (danger/out_of_service) bars the device from being operated -- the switching
// interlock -- so the write tools carry a confirm-with-a-human annotation.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/oms"
	"github.com/kwaabs/voltedge/api/internal/safety"
	"github.com/uptrace/bun"
)

func safetyTools() []Tool {
	return []Tool{
		// --- reads (safety:read) ---
		{
			Name: "safety_list_tags",
			Description: "List safety tags (REST GET /safety/tags). A tag restricts a device; danger/" +
				"out_of_service BLOCK operation, caution/information advise. Filter by device " +
				"(device_class + device_id), by clearance, or set active=true for only live tags.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"device_class":{"type":"string","description":"Filter to tags on this feature class (pair with device_id)."},
					"device_id":{"type":"string","description":"Filter to tags on this device's id (uuid)."},
					"clearance_id":{"type":"string","description":"Filter to tags bound to this clearance (uuid)."},
					"active":{"type":"boolean","description":"Only active (not removed) tags."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermSafetyRead,
			Handler:      toolSafetyListTags,
		},
		{
			Name: "safety_list_grounds",
			Description: "List applied safety earths (REST GET /safety/grounds). A ground must be removed " +
				"before the point it protects is re-energised. Filter by feature (feature_class + " +
				"feature_id), by clearance, or set active=true for only live grounds.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"Filter to grounds on this feature class (pair with feature_id)."},
					"feature_id":{"type":"string","description":"Filter to grounds on this feature's id (uuid)."},
					"clearance_id":{"type":"string","description":"Filter to grounds bound to this clearance (uuid)."},
					"active":{"type":"boolean","description":"Only active (not removed) grounds."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermSafetyRead,
			Handler:      toolSafetyListGrounds,
		},
		{
			Name: "safety_list_clearances",
			Description: "List clearances (permits-to-work) newest-first (REST GET /safety/clearances). " +
				"Filter by status (requested/issued/surrendered/cancelled), by incident, or set " +
				"open_only=true for only live (issued) clearances.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"status":{"type":"string","enum":["requested","issued","surrendered","cancelled"],"description":"Filter to a single status."},
					"incident_id":{"type":"string","description":"Filter to clearances on this incident (uuid)."},
					"open_only":{"type":"boolean","description":"Only live (issued) clearances."}
				},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermSafetyRead,
			Handler:      toolSafetyListClearances,
		},
		{
			Name: "safety_get_clearance",
			Description: "Fetch one clearance with its tags and grounds (REST GET /safety/clearances/{id}).",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{"id":{"type":"string","description":"The clearance's id (uuid)."}},
				"required":["id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermSafetyRead,
			Handler:      toolSafetyGetClearance,
		},

		// --- writes (safety:write) ---
		{
			Name: "safety_place_tag",
			Description: "Place a safety tag on a device (REST POST /safety/tags). tag_type danger or " +
				"out_of_service BARS the device from being operated (a switching step on it will be " +
				"refused); caution/information are advisory. Optionally bind it to an issued clearance. " +
				"Returns the tag. This WRITES and can block operation: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"device_class":{"type":"string","description":"The device's feature class code."},
					"device_id":{"type":"string","description":"The device's id (uuid)."},
					"tag_type":{"type":"string","enum":["danger","out_of_service","caution","information"],"description":"The tag type; danger/out_of_service block operation."},
					"reason":{"type":"string","description":"Why the tag is placed (e.g. crew working downstream)."},
					"clearance_id":{"type":"string","description":"Bind the tag to this issued clearance (uuid); omit for an ad-hoc tag."}
				},
				"required":["device_class","device_id","tag_type"],
				"additionalProperties":false
			}`),
			Annotations:  &Annotations{Title: "Place safety tag", ReadOnlyHint: &falseHint, DestructiveHint: &falseHint, IdempotentHint: &falseHint, OpenWorldHint: &falseHint},
			RequiredPerm: auth.PermSafetyWrite,
			Handler:      toolSafetyPlaceTag,
		},
		{
			Name: "safety_remove_tag",
			Description: "Clear a safety tag (REST POST /safety/tags/{id}/remove), re-permitting operation " +
				"if it was a blocking tag. A clearance-bound tag is released by surrendering the clearance, " +
				"not here. Returns the removed tag. This WRITES: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{"id":{"type":"string","description":"The tag's id (uuid)."}},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations:  &Annotations{Title: "Remove safety tag", ReadOnlyHint: &falseHint, DestructiveHint: &trueHint, IdempotentHint: &falseHint, OpenWorldHint: &falseHint},
			RequiredPerm: auth.PermSafetyWrite,
			Handler:      toolSafetyRemoveTag,
		},
		{
			Name: "safety_apply_ground",
			Description: "Apply a safety earth at a network point (REST POST /safety/grounds). Returns the " +
				"ground. This WRITES: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"feature_class":{"type":"string","description":"The earthed feature's class code."},
					"feature_id":{"type":"string","description":"The earthed feature's id (uuid)."},
					"reason":{"type":"string","description":"Why the earth is applied."},
					"clearance_id":{"type":"string","description":"Bind to this issued clearance (uuid); omit for ad-hoc."}
				},
				"required":["feature_class","feature_id"],
				"additionalProperties":false
			}`),
			Annotations:  &Annotations{Title: "Apply safety earth", ReadOnlyHint: &falseHint, DestructiveHint: &falseHint, IdempotentHint: &falseHint, OpenWorldHint: &falseHint},
			RequiredPerm: auth.PermSafetyWrite,
			Handler:      toolSafetyApplyGround,
		},
		{
			Name: "safety_remove_ground",
			Description: "Remove an applied safety earth (REST POST /safety/grounds/{id}/remove). Returns the " +
				"removed ground. This WRITES: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{"id":{"type":"string","description":"The ground's id (uuid)."}},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations:  &Annotations{Title: "Remove safety earth", ReadOnlyHint: &falseHint, DestructiveHint: &trueHint, IdempotentHint: &falseHint, OpenWorldHint: &falseHint},
			RequiredPerm: auth.PermSafetyWrite,
			Handler:      toolSafetyRemoveGround,
		},
		{
			Name: "safety_issue_clearance_from_order",
			Description: "Issue a permit-to-work from a switching order (REST POST /oms/switching-orders/" +
				"{id}/clearance): it danger-tags every device the order OPENS and grounds every point it " +
				"EARTHS, binding them to the new clearance -- so those devices can no longer be operated " +
				"until the clearance is surrendered. Returns the clearance with its tags/grounds. This " +
				"WRITES and blocks operation: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"order_id":{"type":"string","description":"The switching order's id (uuid)."},
					"work_description":{"type":"string","description":"What the crew is doing (optional)."},
					"issued_to":{"type":"string","description":"The crew/person the permit is handed to (optional)."}
				},
				"required":["order_id"],
				"additionalProperties":false
			}`),
			Annotations:  &Annotations{Title: "Issue clearance from order", ReadOnlyHint: &falseHint, DestructiveHint: &falseHint, IdempotentHint: &falseHint, OpenWorldHint: &falseHint},
			RequiredPerm: auth.PermSafetyWrite,
			Handler:      toolSafetyIssueClearanceFromOrder,
		},
		{
			Name: "safety_surrender_clearance",
			Description: "Surrender a clearance (REST POST /safety/clearances/{id}/surrender): the work is " +
				"done, the permit is handed back, and every tag and ground it owns is RELEASED -- the " +
				"devices it tagged may be operated again. Returns the clearance. This WRITES and " +
				"re-permits operation: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{"id":{"type":"string","description":"The clearance's id (uuid)."}},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations:  &Annotations{Title: "Surrender clearance", ReadOnlyHint: &falseHint, DestructiveHint: &falseHint, IdempotentHint: &falseHint, OpenWorldHint: &falseHint},
			RequiredPerm: auth.PermSafetyWrite,
			Handler:      toolSafetySurrenderClearance,
		},
		{
			Name: "safety_cancel_clearance",
			Description: "Cancel a clearance issued in error (REST POST /safety/clearances/{id}/cancel): " +
				"voids it and releases its active tags and grounds. Returns the clearance. This WRITES: " +
				"confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{"id":{"type":"string","description":"The clearance's id (uuid)."}},
				"required":["id"],
				"additionalProperties":false
			}`),
			Annotations:  &Annotations{Title: "Cancel clearance", ReadOnlyHint: &falseHint, DestructiveHint: &trueHint, IdempotentHint: &falseHint, OpenWorldHint: &falseHint},
			RequiredPerm: auth.PermSafetyWrite,
			Handler:      toolSafetyCancelClearance,
		},
	}
}

// --- read handlers ---

func toolSafetyListTags(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		DeviceClass string `json:"device_class"`
		DeviceID    string `json:"device_id"`
		ClearanceID string `json:"clearance_id"`
		Active      bool   `json:"active"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	tags, err := safety.ListTags(ctx, db, safety.TagFilter{
		DeviceClass: in.DeviceClass, DeviceID: in.DeviceID, ClearanceID: in.ClearanceID, ActiveOnly: in.Active,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"tags": tags, "count": len(tags)}, nil
}

func toolSafetyListGrounds(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string `json:"feature_class"`
		FeatureID    string `json:"feature_id"`
		ClearanceID  string `json:"clearance_id"`
		Active       bool   `json:"active"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	grounds, err := safety.ListGrounds(ctx, db, safety.GroundFilter{
		FeatureClass: in.FeatureClass, FeatureID: in.FeatureID, ClearanceID: in.ClearanceID, ActiveOnly: in.Active,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"grounds": grounds, "count": len(grounds)}, nil
}

func toolSafetyListClearances(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		Status     string `json:"status"`
		IncidentID string `json:"incident_id"`
		OpenOnly   bool   `json:"open_only"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	rows, err := safety.ListClearances(ctx, db, safety.ClearanceFilter{Status: in.Status, IncidentID: in.IncidentID, OpenOnly: in.OpenOnly})
	if err != nil {
		return nil, err
	}
	return map[string]any{"clearances": rows, "count": len(rows)}, nil
}

func toolSafetyGetClearance(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	c, err := safety.GetClearance(ctx, db, in.ID)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("no clearance %q", in.ID)
	}
	return c, nil
}

// --- write handlers ---

func toolSafetyPlaceTag(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		DeviceClass string  `json:"device_class"`
		DeviceID    string  `json:"device_id"`
		TagType     string  `json:"tag_type"`
		Reason      *string `json:"reason"`
		ClearanceID *string `json:"clearance_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return safety.PlaceTag(ctx, db, safety.PlaceTagInput{
		DeviceClass: in.DeviceClass, DeviceID: in.DeviceID, TagType: in.TagType, Reason: in.Reason, ClearanceID: in.ClearanceID,
	})
}

func toolSafetyRemoveTag(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	id, err := idArg(args)
	if err != nil {
		return nil, err
	}
	return safety.RemoveTag(ctx, db, id)
}

func toolSafetyApplyGround(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		FeatureClass string  `json:"feature_class"`
		FeatureID    string  `json:"feature_id"`
		Reason       *string `json:"reason"`
		ClearanceID  *string `json:"clearance_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return safety.ApplyGround(ctx, db, safety.ApplyGroundInput{
		FeatureClass: in.FeatureClass, FeatureID: in.FeatureID, Reason: in.Reason, ClearanceID: in.ClearanceID,
	})
}

func toolSafetyRemoveGround(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	id, err := idArg(args)
	if err != nil {
		return nil, err
	}
	return safety.RemoveGround(ctx, db, id)
}

func toolSafetyIssueClearanceFromOrder(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		OrderID         string  `json:"order_id"`
		WorkDescription *string `json:"work_description"`
		IssuedTo        *string `json:"issued_to"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.OrderID == "" {
		return nil, fmt.Errorf("order_id is required")
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return oms.IssueClearanceFromOrder(ctx, sdb, in.OrderID, in.WorkDescription, in.IssuedTo)
}

func toolSafetySurrenderClearance(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	id, err := idArg(args)
	if err != nil {
		return nil, err
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return safety.SurrenderClearance(ctx, sdb, id)
}

func toolSafetyCancelClearance(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	id, err := idArg(args)
	if err != nil {
		return nil, err
	}
	sdb, err := asDB(db)
	if err != nil {
		return nil, err
	}
	return safety.CancelClearance(ctx, sdb, id)
}

// idArg unmarshals the common {"id": "..."} write-tool argument.
func idArg(args json.RawMessage) (string, error) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}
	if in.ID == "" {
		return "", fmt.Errorf("id is required")
	}
	return in.ID, nil
}
