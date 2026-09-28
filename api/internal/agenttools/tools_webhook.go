package agenttools

// The inbound-webhook module's tool surface (api/internal/webhook, migration
// 0124): configured endpoints external systems PUSH feature (later
// reading/work/fault) changes to, each delivery queued and applied through the
// normal crud path as the endpoint's service user. Each tool is a thin adapter
// over an already-exported webhook.Agent* operation -- the same code the admin
// REST /webhook-endpoints routes call. The module RIDES the metadata permission,
// like external systems and domains: the read tools require metadata:read, the
// write tools metadata:write. Reads carry nil annotations (buildRegistry stamps
// readOnlyHint:true); writes carry explicit annotations so a client confirms with
// a human first.
//
// Creating or editing an endpoint is intentionally absent: sealing an HMAC
// signing secret needs the at-rest cipher, which the stateless tool registry
// (a bare *bun.DB) can't supply -- exactly why feature_link's live fetch is
// omitted. Endpoints are configured from the admin UI/REST surface, which has the
// cipher; these tools cover inspection and the operational actions (test, replay,
// delete).

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kwaabs/voltedge/api/internal/auth"
	"github.com/kwaabs/voltedge/api/internal/webhook"
	"github.com/uptrace/bun"
)

// webhookTools returns the inbound-webhook tools in advertised order: the reads
// first (list / get / deliveries, metadata:read), then the writes (test / replay
// / delete, metadata:write).
func webhookTools() []Tool {
	return []Tool{
		{
			Name: "webhook_endpoint_list",
			Description: "List every configured inbound-webhook endpoint (migration 0124), newest " +
				"first -- the same list the admin GET /webhook-endpoints route returns. A signing " +
				"secret is never returned, only whether one is set (has_signing_secret). Each entry " +
				"carries the endpoint's id (pass it to the other webhook tools), its direction " +
				"(push/pull), target (feature/reading/work/fault), auth_mode (hmac/bearer/both), " +
				"service_user_id (whose permissions ingested writes run as), and -- for a push " +
				"endpoint -- the public receive_url a sender targets. Use this before inspecting or " +
				"acting on an endpoint.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{},
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermMetadataRead,
			Handler:      toolWebhookEndpointList,
		},
		{
			Name: "webhook_endpoint_get",
			Description: "Fetch one inbound-webhook endpoint by its id -- the same record the admin " +
				"GET /webhook-endpoints/{id} route returns (never the secret, only has_signing_secret). " +
				"Use webhook_endpoint_list to find the id first.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"endpoint_id":{"type":"string","description":"The endpoint's id (uuid)."}
				},
				"required":["endpoint_id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermMetadataRead,
			Handler:      toolWebhookEndpointGet,
		},
		{
			Name: "webhook_delivery_list",
			Description: "List one endpoint's most recent deliveries, newest first -- the same log the " +
				"admin GET /webhook-endpoints/{id}/deliveries route returns. Each entry carries the " +
				"delivery's id (pass it to webhook_delivery_replay), its status (queued/running/" +
				"succeeded/failed/dead), source (push/pull/manual/replay), attempt count, the received " +
				"payload, and -- once applied -- the per-item result or the error. Use this to see " +
				"what an integration has sent and how it landed.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"endpoint_id":{"type":"string","description":"The endpoint's id (uuid)."},
					"limit":{"type":"integer","description":"Max rows to return (1-100; defaults to 100)."}
				},
				"required":["endpoint_id"],
				"additionalProperties":false
			}`),
			RequiredPerm: auth.PermMetadataRead,
			Handler:      toolWebhookDeliveryList,
		},
		{
			Name: "webhook_test",
			Description: "Paste-and-apply a native VoltEdge payload against an endpoint INLINE -- the " +
				"same validated apply the admin POST /webhook-endpoints/{id}/test route performs, " +
				"without sending a real push. The payload is a JSON object with a `changes` array " +
				"(each change: op create/update/delete/upsert, external_id, optional feature_class " +
				"[falls back to the endpoint's config.feature_class], subtype, name, geometry, " +
				"attributes); a single bare change object is also accepted. It runs as the endpoint's " +
				"service user, so it inherits that user's permissions, territory scope, and validation, " +
				"and it really creates/updates/deletes features. A failure dead-letters the delivery " +
				"(never retried by the worker) and comes back as the tool error. This WRITES to the " +
				"database: confirm with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"endpoint_id":{"type":"string","description":"The endpoint's id (uuid) to apply against."},
					"payload":{"type":"object","description":"A VoltEdge native payload: {\"changes\":[{...}]} (or a single bare change object)."}
				},
				"required":["endpoint_id","payload"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Test webhook payload (applies it)",
				ReadOnlyHint:    &falseHint, // it really applies the changes
				DestructiveHint: &trueHint,  // the payload may include deletes
				IdempotentHint:  &falseHint, // a create in the payload repeats
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermMetadataWrite,
			Handler:      toolWebhookTest,
		},
		{
			Name: "webhook_delivery_replay",
			Description: "Re-enqueue a stored delivery's payload as a NEW replay delivery for the " +
				"async worker to apply -- the same action the admin POST " +
				"/webhook-endpoints/{id}/deliveries/{deliveryID}/replay route performs. The original " +
				"row is left untouched as the audit record; the apply happens on the worker, so this " +
				"returns the new delivery id (watch webhook_delivery_list for its outcome). Use this " +
				"to retry a dead-lettered delivery after fixing the underlying cause. This WRITES to " +
				"the database (the replay applies as the service user): confirm with a human before " +
				"calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"endpoint_id":{"type":"string","description":"The endpoint's id (uuid) the delivery belongs to."},
					"delivery_id":{"type":"string","description":"The stored delivery's id (uuid), e.g. from webhook_delivery_list."}
				},
				"required":["endpoint_id","delivery_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Replay webhook delivery",
				ReadOnlyHint:    &falseHint, // the replay applies changes
				DestructiveHint: &trueHint,  // the payload may include deletes
				IdempotentHint:  &falseHint, // each replay enqueues a fresh delivery
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermMetadataWrite,
			Handler:      toolWebhookDeliveryReplay,
		},
		{
			Name: "webhook_endpoint_delete",
			Description: "Permanently remove an inbound-webhook endpoint and its delivery log -- the " +
				"same write the admin DELETE /webhook-endpoints/{id} route performs. Senders pointing " +
				"at its receive URL will start getting 404s. This is DESTRUCTIVE and NOT reversible. " +
				"Use webhook_endpoint_list to find the id first. This WRITES to the database: confirm " +
				"with a human before calling.",
			InputSchema: json.RawMessage(`{
				"type":"object",
				"properties":{
					"endpoint_id":{"type":"string","description":"The endpoint's id (uuid) to delete."}
				},
				"required":["endpoint_id"],
				"additionalProperties":false
			}`),
			Annotations: &Annotations{
				Title:           "Delete webhook endpoint",
				ReadOnlyHint:    &falseHint, // this tool writes
				DestructiveHint: &trueHint,  // it removes the endpoint and its log outright
				IdempotentHint:  &falseHint, // a second delete of the same id is a not-found error
				OpenWorldHint:   &falseHint, // acts only on this system's own database
			},
			RequiredPerm: auth.PermMetadataWrite,
			Handler:      toolWebhookEndpointDelete,
		},
	}
}

// --- handlers ---

func toolWebhookEndpointList(ctx context.Context, db bun.IDB, _ json.RawMessage) (any, error) {
	return webhook.AgentListEndpoints(ctx, db)
}

func toolWebhookEndpointGet(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		EndpointID string `json:"endpoint_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.EndpointID == "" {
		return nil, fmt.Errorf("endpoint_id is required")
	}
	return webhook.AgentGetEndpoint(ctx, db, in.EndpointID)
}

func toolWebhookDeliveryList(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		EndpointID string `json:"endpoint_id"`
		Limit      int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.EndpointID == "" {
		return nil, fmt.Errorf("endpoint_id is required")
	}
	return webhook.AgentListDeliveries(ctx, db, in.EndpointID, in.Limit)
}

func toolWebhookTest(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		EndpointID string          `json:"endpoint_id"`
		Payload    json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.EndpointID == "" {
		return nil, fmt.Errorf("endpoint_id is required")
	}
	// The engine re-validates the payload against the live model as the service
	// user; its 400s surface in-band as the tool error.
	return webhook.AgentTestApply(ctx, db, in.EndpointID, in.Payload)
}

func toolWebhookDeliveryReplay(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		EndpointID string `json:"endpoint_id"`
		DeliveryID string `json:"delivery_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.EndpointID == "" || in.DeliveryID == "" {
		return nil, fmt.Errorf("endpoint_id and delivery_id are required")
	}
	return webhook.AgentReplayDelivery(ctx, db, in.EndpointID, in.DeliveryID)
}

func toolWebhookEndpointDelete(ctx context.Context, db bun.IDB, args json.RawMessage) (any, error) {
	var in struct {
		EndpointID string `json:"endpoint_id"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if in.EndpointID == "" {
		return nil, fmt.Errorf("endpoint_id is required")
	}
	return webhook.AgentDeleteEndpoint(ctx, db, in.EndpointID)
}
