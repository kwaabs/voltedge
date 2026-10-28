import { useQuery } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/**
 * Wire types for the inbound-webhook admin API (api/internal/webhook,
 * migration 0124), behind the metadata:read/metadata:write gate. Each field
 * mirrors the Go endpointResponse / deliveryResponse DTOs; per this app's
 * convention the hook holds its own copy of the shape.
 */

/** direction/target/auth_mode enums accepted by the create/update endpoints.
 * 'outbound' is a subscription VoltEdge POSTs its own events to (migration 0134);
 * 'push'/'pull' are the inbound directions. */
export type WebhookDirection = "push" | "pull" | "outbound"
export type WebhookTarget =
  | "feature"
  | "reading"
  | "work"
  | "fault"
  | "operation"
  | "outage"
  | "incident"
  | "workorder"
export type WebhookAuthMode = "hmac" | "bearer" | "both"
/** Outbound auth shape for a pull endpoint (mirrors the Go outbound.Auth). */
export type WebhookPullAuthType = "none" | "api_key" | "basic" | "bearer"

/** Pull-direction settings carried in config: where to fetch and how to
 * authenticate. The credential itself rides the sealed signing_secret column, so
 * it is never present here. */
export interface WebhookPullConfig {
  pull_url?: string
  pull_auth?: {
    type?: WebhookPullAuthType
    header?: string
    query_param?: string
    username?: string
  }
}

export interface WebhookEndpoint {
  id: string
  name: string
  active: boolean
  direction: WebhookDirection
  target: WebhookTarget
  auth_mode: WebhookAuthMode
  /** The signing secret is never returned -- only whether one is stored. */
  has_signing_secret: boolean
  service_user_id: string
  /** Opaque per-endpoint config; the feature adapter reads config.feature_class,
   * a pull endpoint reads config.pull_url / config.pull_auth, an outbound
   * subscription reads config.target_url / config.event_types. */
  config?: {
    feature_class?: string
    target_url?: string
    event_types?: string[]
    class_filter?: string[]
    territory_id?: string
    mapping?: unknown
    /** Mirror targets (incident/workorder): the external system's code — the
     * origin tag + external-id namespace — and a {external_state: voltedge_status}
     * translation table. */
    system_code?: string
    status_map?: Record<string, string>
  } & WebhookPullConfig &
    Record<string, unknown>
  mapping?: unknown
  created_at: string
  updated_at: string
  /** Public path a push sender targets (e.g. /webhooks/{id}). */
  receive_url: string
}

export type WebhookDeliveryStatus = "queued" | "running" | "succeeded" | "failed" | "dead"
export type WebhookDeliverySource = "push" | "pull" | "manual" | "replay"

export interface WebhookDelivery {
  id: string
  endpoint_id: string
  status: WebhookDeliveryStatus
  source: WebhookDeliverySource
  idempotency_key?: string
  payload: unknown
  attempts: number
  result?: unknown
  error?: string
  created_at: string
  finished_at?: string
}

const ENDPOINTS_KEY = ["webhook-endpoints"] as const

/** All configured endpoints (never secrets). */
export function useWebhookEndpoints(enabled = true) {
  return useQuery<{ endpoints: WebhookEndpoint[] }, ApiError>({
    queryKey: ENDPOINTS_KEY,
    queryFn: () => apiFetch<{ endpoints: WebhookEndpoint[] }>("/webhook-endpoints"),
    staleTime: 30_000,
    enabled,
    retry: false,
  })
}

/**
 * The recent delivery log for one endpoint (newest first, server-capped at
 * 100). While the panel is open it polls every 4s so a queued/running
 * delivery visibly resolves without a manual refresh.
 */
export function useWebhookDeliveries(endpointId: string | null) {
  return useQuery<{ deliveries: WebhookDelivery[] }, ApiError>({
    queryKey: ["webhook-endpoints", endpointId, "deliveries"],
    queryFn: () => apiFetch<{ deliveries: WebhookDelivery[] }>(`/webhook-endpoints/${endpointId}/deliveries`),
    enabled: endpointId !== null,
    refetchInterval: 4_000,
    retry: false,
  })
}

/** One outbound delivery (mirrors the Go DeliveryView). The envelope payload is
 * not in the log list -- it carries status, timing, HTTP code and errors. */
export interface OutboundDelivery {
  id: string
  event_type: string
  entity_kind: string
  entity_id: string
  status: WebhookDeliveryStatus
  attempts: number
  response_code?: number
  error?: string
  next_attempt_at: string
  created_at: string
  finished_at?: string
}

/** The recent outbound delivery log for one subscription (newest first,
 * server-capped at 100), polled every 4s while open so a queued/running delivery
 * resolves without a manual refresh. */
export function useOutboundDeliveries(endpointId: string | null) {
  return useQuery<{ deliveries: OutboundDelivery[] }, ApiError>({
    queryKey: ["webhook-endpoints", endpointId, "outbound-deliveries"],
    queryFn: () =>
      apiFetch<{ deliveries: OutboundDelivery[] }>(`/webhook-endpoints/${endpointId}/outbound-deliveries`),
    enabled: endpointId !== null,
    refetchInterval: 4_000,
    retry: false,
  })
}
