import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"
import type { WorkOrder } from "@/hooks/use-wam"

// --- Cross-feature activity audit (admin) ----------------------------------
// The "who changed what, in this window" log behind /admin/activity. Backed by
// GET /features/activity (crud.activityHandler), escalated to admin:users
// server-side. The per-feature timeline lives elsewhere; this is the cross-
// feature slice by actor + time.

/** One change in the cross-feature activity log. actor_* are null for a system
 * write or a since-deleted user. */
export interface ActivityEntry {
  id: number
  feature_class_code: string
  feature_id: string
  action: string
  actor_id: string | null
  actor_name: string | null
  actor_email: string | null
  at: string
}

/** Narrows the activity log; all optional. from/to are RFC3339 strings. */
export interface ActivityFilter {
  actorId?: string
  featureClass?: string
  action?: string
  from?: string
  to?: string
  limit?: number
}

/** The cross-feature activity log, newest first. Needs admin:users (the endpoint
 * escalates above the mount's features:read). */
export function useActivity(filter: ActivityFilter) {
  const params = new URLSearchParams()
  if (filter.actorId) params.set("actor_id", filter.actorId)
  if (filter.featureClass) params.set("feature_class", filter.featureClass)
  if (filter.action) params.set("action", filter.action)
  if (filter.from) params.set("from", filter.from)
  if (filter.to) params.set("to", filter.to)
  if (filter.limit) params.set("limit", String(filter.limit))
  const qs = params.toString()
  return useQuery({
    queryKey: ["activity", "log", filter],
    queryFn: () => apiFetch<{ activity: ActivityEntry[] }>(`/features/activity${qs ? `?${qs}` : ""}`),
  })
}

// --- Header activity-centre (the bell's own polled data) -------------------
// The work-order board is the ACTIONABLE personal item (jobs assigned to me),
// polled so a freshly-assigned fix shows up without a reload; the spatial
// due-items (easements expiring, vegetation trims due) are slower-moving network
// attention, fetched via the existing spatial hooks from the bell.

// 20s so a freshly-assigned (or re-assigned) job surfaces on the bell within a
// few beats rather than up to a minute. react-query pauses the interval while the
// tab is hidden, so a backgrounded tab isn't polling. True push (SSE) would
// replace this timer with a NOTIFY-driven invalidation, not a rewrite.
const POLL_MS = 20_000

/** The signed-in user's OPEN work orders assigned to them, polled for the bell.
 * Reuses the WAM board endpoint with mine=true&open=true. */
export function useMyOpenWorkOrders(enabled: boolean) {
  return useQuery({
    queryKey: ["activity", "my-open-work-orders"],
    queryFn: () => apiFetch<{ work_orders: WorkOrder[] }>("/wam/work-orders?mine=true&open=true"),
    enabled,
    refetchInterval: POLL_MS,
    staleTime: 30_000,
  })
}
