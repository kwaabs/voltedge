package webhook

// This file is the webhook engine's agent-facing surface: the exported wrappers
// the shared agenttools registry (the external MCP server and the in-app
// assistant) calls, one per admin REST route, returning plain values instead of
// writing HTTP. They keep the tool handlers in agenttools thin adapters, exactly
// as internal/externallink exposes ListLinks/CreateLink for its tool file.
//
// Only the cipher-free operations are exposed -- listing/inspecting endpoints and
// their deliveries, applying a test payload inline, replaying a stored delivery,
// and deleting an endpoint. Creating or editing an endpoint is intentionally
// absent: sealing an HMAC signing secret needs the at-rest cipher, which the
// stateless tool registry (a bare *bun.DB, no cipher) can't supply -- the same
// reason externallink omits its live api fetch. Endpoints are configured from the
// admin UI / REST surface, which has the cipher.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/uptrace/bun"
)

// agentDB narrows the registry's bun.IDB to the *bun.DB these wrappers need (a
// cipher-free store, and RunInTx for the inline apply). The registry always
// hands the top-level pool, never a transaction, so this assertion holds; a tx
// is refused loudly rather than silently mishandled.
func agentDB(db bun.IDB) (*bun.DB, error) {
	d, ok := db.(*bun.DB)
	if !ok {
		return nil, fmt.Errorf("webhook agent tools require the top-level *bun.DB")
	}
	return d, nil
}

// AgentListEndpoints returns every configured endpoint (never a secret), newest
// first -- mirrors GET /webhook-endpoints.
func AgentListEndpoints(ctx context.Context, db bun.IDB) (any, error) {
	d, err := agentDB(db)
	if err != nil {
		return nil, err
	}
	rows, err := listEndpoints(ctx, &store{db: d})
	if err != nil {
		return nil, err
	}
	return map[string]any{"endpoints": rows}, nil
}

// AgentGetEndpoint returns one endpoint by id -- mirrors GET /webhook-endpoints/{id}.
func AgentGetEndpoint(ctx context.Context, db bun.IDB, id string) (any, error) {
	d, err := agentDB(db)
	if err != nil {
		return nil, err
	}
	ep, err := getEndpoint(ctx, &store{db: d}, id)
	if err != nil {
		return nil, err
	}
	return formatEndpoint(ep), nil
}

// AgentListDeliveries returns an endpoint's recent delivery log, newest first
// (capped like the REST route) -- mirrors GET /webhook-endpoints/{id}/deliveries.
// The endpoint is resolved first so a bad id is a clean not-found, not empty.
func AgentListDeliveries(ctx context.Context, db bun.IDB, endpointID string, limit int) (any, error) {
	d, err := agentDB(db)
	if err != nil {
		return nil, err
	}
	if _, err := getEndpoint(ctx, &store{db: d}, endpointID); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > deliveryListLimit {
		limit = deliveryListLimit
	}
	rows, err := listDeliveries(ctx, d, endpointID, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"deliveries": toDeliveryResponses(rows)}, nil
}

// AgentTestApply records a source="manual" delivery and applies it inline as the
// endpoint's service user -- mirrors POST /webhook-endpoints/{id}/test. A failure
// dead-letters the delivery (never requeued) and surfaces as the tool error.
func AgentTestApply(ctx context.Context, db bun.IDB, endpointID string, payload json.RawMessage) (any, error) {
	d, err := agentDB(db)
	if err != nil {
		return nil, err
	}
	if len(payload) == 0 || !json.Valid(payload) {
		return nil, invalid("payload must be a JSON object of changes")
	}
	ep, err := getEndpoint(ctx, &store{db: d}, endpointID)
	if err != nil {
		return nil, err
	}
	deliveryID, _, err := enqueueDelivery(ctx, d, ep.ID, "manual", nil, payload, nil)
	if err != nil {
		return nil, err
	}
	dv, err := getDelivery(ctx, d, ep.ID, deliveryID)
	if err != nil {
		return nil, err
	}
	result, err := applyInline(ctx, d, dv)
	if err != nil {
		return nil, err
	}
	return map[string]any{"delivery_id": deliveryID, "result": result}, nil
}

// AgentReplayDelivery re-enqueues a stored delivery's payload as a NEW
// source="replay" delivery for the async worker -- mirrors POST
// .../deliveries/{id}/replay. The original row is left untouched as the audit
// record; the apply happens later on the worker, so this only returns the new id.
func AgentReplayDelivery(ctx context.Context, db bun.IDB, endpointID, deliveryID string) (any, error) {
	d, err := agentDB(db)
	if err != nil {
		return nil, err
	}
	if _, err := getEndpoint(ctx, &store{db: d}, endpointID); err != nil {
		return nil, err
	}
	orig, err := getDelivery(ctx, d, endpointID, deliveryID)
	if err != nil {
		return nil, err
	}
	newID, _, err := enqueueDelivery(ctx, d, endpointID, "replay", nil, orig.Payload, orig.Headers)
	if err != nil {
		return nil, err
	}
	return map[string]any{"delivery_id": newID, "queued": true}, nil
}

// AgentDeleteEndpoint removes an endpoint and cascades its delivery log --
// mirrors DELETE /webhook-endpoints/{id}. Cipher-free (no secret to unseal).
func AgentDeleteEndpoint(ctx context.Context, db bun.IDB, id string) (any, error) {
	d, err := agentDB(db)
	if err != nil {
		return nil, err
	}
	if err := deleteEndpoint(ctx, &store{db: d}, id); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true, "id": id}, nil
}
