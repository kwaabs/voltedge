import { useQuery } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/**
 * Wire types for the inbound-webhook admin API (api/internal/webhook,
 * migration 0124), behind the metadata:read/metadata:write gate. Each field
 * mirrors the Go endpointResponse / deliveryResponse DTOs; per this app's
 * convention the hook holds its own copy of the shape.
 */

/** direction/target/auth_mode enums accepted by the create/update endpoints. */
export type WebhookDirection = "push" | "pull"
export type WebhookTarget = "feature" | "reading" | "work" | "fault"
export type WebhookAuthMode = "hmac" | "bearer" | "both"

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
  /** Opaque per-endpoint config; the feature adapter reads config.feature_class. */
  config?: { feature_class?: string } & Record<string, unknown>
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
