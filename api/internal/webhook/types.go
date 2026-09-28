// Package webhook is VoltEdge's inbound ingestion engine (0124): configured
// endpoints receive feature (later reading/work/fault) changes pushed by
// external systems -- or pulled by VoltEdge -- store every delivery in an async
// queue, and apply it through the normal crud/package path as the endpoint's
// configured service user, so ingested writes inherit that user's RBAC
// permissions, territory scope, validation, and audit trail. Nothing here
// bypasses the engines that already guard a hand edit.
//
// The public receiver (receiver.go) only authenticates and enqueues; the worker
// (worker.go, run out-of-process by cmd/webhookworker, mirroring cmd/exportworker)
// claims deliveries and applies them via an adapter (adapter.go). Endpoints and
// the delivery log are managed under the admin surface (handler.go).
package webhook

import (
	"encoding/json"
	"time"

	"github.com/uptrace/bun"
)

// Endpoint mirrors ops.webhook_endpoints (0124). SigningSecretEnc is the sealed
// HMAC secret and is never serialized to a client -- the response DTO exposes
// only HasSigningSecret.
type Endpoint struct {
	bun.BaseModel `bun:"table:ops.webhook_endpoints,alias:webhook_endpoints"`

	ID               string          `bun:"id,pk,nullzero"`
	Name             string          `bun:"name"`
	Active           bool            `bun:"active"`
	Direction        string          `bun:"direction"`
	Target           string          `bun:"target"`
	AuthMode         string          `bun:"auth_mode"`
	SigningSecretEnc []byte          `bun:"signing_secret_enc"`
	ServiceUserID    string          `bun:"service_user_id"`
	Mapping          json.RawMessage `bun:"mapping"`
	Config           json.RawMessage `bun:"config"`
	CreatedBy        *string         `bun:"created_by"`
	CreatedAt        time.Time       `bun:"created_at,nullzero"`
	UpdatedAt        time.Time       `bun:"updated_at,nullzero"`
}

// Delivery mirrors ops.webhook_deliveries (0124) -- one received/fetched/manual
// payload and its queue + apply lifecycle.
type Delivery struct {
	bun.BaseModel `bun:"table:ops.webhook_deliveries,alias:webhook_deliveries"`

	ID             string          `bun:"id,pk,nullzero"`
	EndpointID     string          `bun:"endpoint_id"`
	Status         string          `bun:"status"`
	Source         string          `bun:"source"`
	IdempotencyKey *string         `bun:"idempotency_key"`
	Payload        json.RawMessage `bun:"payload"`
	Headers        json.RawMessage `bun:"headers"`
	Attempts       int             `bun:"attempts"`
	WorkerID       *string         `bun:"worker_id"`
	LeaseExpiresAt *time.Time      `bun:"lease_expires_at"`
	Result         json.RawMessage `bun:"result"`
	Error          *string         `bun:"error"`
	CreatedAt      time.Time       `bun:"created_at,nullzero"`
	StartedAt      *time.Time      `bun:"started_at"`
	FinishedAt     *time.Time      `bun:"finished_at"`
}

// FeatureRef mirrors ops.webhook_feature_refs (0124): the (endpoint, external_id)
// -> feature correlation an adapter reads to decide create vs update, and writes
// after a create.
type FeatureRef struct {
	bun.BaseModel `bun:"table:ops.webhook_feature_refs,alias:webhook_feature_refs"`

	EndpointID     string    `bun:"endpoint_id"`
	ExternalID     string    `bun:"external_id"`
	FeatureClassID string    `bun:"feature_class_id"`
	FeatureID      string    `bun:"feature_id"`
	CreatedAt      time.Time `bun:"created_at,nullzero"`
	UpdatedAt      time.Time `bun:"updated_at,nullzero"`
}

// EntityRef mirrors ops.webhook_entity_refs (0125): the (endpoint, external_id)
// -> work-order / fault-event correlation the non-feature adapters read to decide
// create vs update, and write after a create. Features keep their own richer
// FeatureRef (which also carries the class); a work order / fault event needs
// only its id, so one small table keyed by target serves both. EntityID is
// polymorphic (no FK) into wam.work_orders / faults.fault_events, same convention
// as FeatureRef.
type EntityRef struct {
	bun.BaseModel `bun:"table:ops.webhook_entity_refs,alias:webhook_entity_refs"`

	EndpointID string    `bun:"endpoint_id"`
	ExternalID string    `bun:"external_id"`
	Target     string    `bun:"target"`
	EntityID   string    `bun:"entity_id"`
	CreatedAt  time.Time `bun:"created_at,nullzero"`
	UpdatedAt  time.Time `bun:"updated_at,nullzero"`
}

// --- request/response DTOs (admin surface) ---

// createEndpointRequest is the POST body. SigningSecret is write-only (sealed at
// rest, never returned). Mapping/Config are opaque JSON passed straight through.
type createEndpointRequest struct {
	Name          string          `json:"name"`
	Active        *bool           `json:"active"`
	Direction     string          `json:"direction"`
	Target        string          `json:"target"`
	AuthMode      string          `json:"auth_mode"`
	SigningSecret string          `json:"signing_secret"`
	ServiceUserID string          `json:"service_user_id"`
	Mapping       json.RawMessage `json:"mapping"`
	Config        json.RawMessage `json:"config"`
}

// updateEndpointRequest is the PATCH body. Every field is a pointer so an absent
// field is left unchanged. SigningSecret is three-state like external systems'
// credential: absent = keep, present+empty = clear, present+non-empty = re-seal.
type updateEndpointRequest struct {
	Name          *string         `json:"name"`
	Active        *bool           `json:"active"`
	Direction     *string         `json:"direction"`
	Target        *string         `json:"target"`
	AuthMode      *string         `json:"auth_mode"`
	SigningSecret *string         `json:"signing_secret"`
	ServiceUserID *string         `json:"service_user_id"`
	Mapping       json.RawMessage `json:"mapping"`
	Config        json.RawMessage `json:"config"`
}

// endpointResponse is what a client sees -- the secret is reduced to a boolean.
type endpointResponse struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Active            bool            `json:"active"`
	Direction         string          `json:"direction"`
	Target            string          `json:"target"`
	AuthMode          string          `json:"auth_mode"`
	HasSigningSecret  bool            `json:"has_signing_secret"`
	ServiceUserID     string          `json:"service_user_id"`
	Mapping           json.RawMessage `json:"mapping,omitempty"`
	Config            json.RawMessage `json:"config"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
	// ReceiveURL is the public path a push sender targets, filled by the handler.
	ReceiveURL string `json:"receive_url"`
}

// deliveryResponse is one row of the admin delivery log.
type deliveryResponse struct {
	ID             string          `json:"id"`
	EndpointID     string          `json:"endpoint_id"`
	Status         string          `json:"status"`
	Source         string          `json:"source"`
	IdempotencyKey *string         `json:"idempotency_key,omitempty"`
	Payload        json.RawMessage `json:"payload"`
	Attempts       int             `json:"attempts"`
	Result         json.RawMessage `json:"result,omitempty"`
	Error          *string         `json:"error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
}

// --- native change format (no mapping) ---

// nativeChange is one create/update/delete a sender expresses in VoltEdge's own
// vocabulary when the endpoint has no mapping spec. external_id is the sender's
// stable id for the entity -- the correlation key that makes a repeat delivery
// an update rather than a duplicate. feature_class falls back to the endpoint's
// config.feature_class when absent, so a single-class integration need not
// repeat it on every change.
type nativeChange struct {
	Op           string          `json:"op"`
	ExternalID   string          `json:"external_id"`
	FeatureClass string          `json:"feature_class"`
	Subtype      *int            `json:"subtype"`
	Name         *string         `json:"name"`
	TerritoryID  *string         `json:"territory_id"`
	Geometry     json.RawMessage `json:"geometry"`
	Attributes   map[string]any  `json:"attributes"`
}

// changeEnvelope is the wire envelope shared by every target: a `changes` array
// of raw per-change objects. parseChanges pulls the array out (or wraps a single
// bare change object) and hands each raw element to the endpoint's target
// adapter, which decodes its own typed shape -- feature into nativeChange, work
// into workChange, fault into faultChange. Keeping the envelope target-agnostic
// is what lets one pipeline serve all the adapters.
type changeEnvelope struct {
	Changes []json.RawMessage `json:"changes"`
}

// itemResult is one applied change's outcome, collected into Delivery.Result.
// FeatureClass/FeatureID identify a feature-target result; Target/EntityID
// identify a work- or fault-target result (its wam.work_orders / faults.
// fault_events id). Skipped marks an idempotent no-op (e.g. deleting an
// already-absent feature).
type itemResult struct {
	Op           string `json:"op"`
	ExternalID   string `json:"external_id"`
	FeatureClass string `json:"feature_class,omitempty"`
	FeatureID    string `json:"feature_id,omitempty"`
	Target       string `json:"target,omitempty"`
	EntityID     string `json:"entity_id,omitempty"`
	Skipped      bool   `json:"skipped,omitempty"`
}

// apiError is the JSON body for any 4xx/5xx.
type apiError struct {
	Error string `json:"error"`
}
