import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/** One change in the cross-feature activity log (gis.feature_history, sliced by
 * actor + time rather than by feature). The accountability view -- who changed
 * what, when -- as opposed to a single feature's timeline. The post-change
 * snapshot is deliberately omitted; drill into the feature's own history for the
 * state. */
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

export interface ActivityFilter {
  actorId?: string
  featureClass?: string
  action?: string
  /** RFC3339 timestamps (inclusive from, exclusive to). */
  from?: string
  to?: string
  limit?: number
}

/** The cross-feature activity log, newest first. Needs admin:users (the endpoint
 * escalates above the features:read its mount requires -- an accountability view
 * is a monitoring capability, not a general read). */
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
    queryKey: ["activity", filter],
    queryFn: () => apiFetch<{ activity: ActivityEntry[] }>(`/features/activity${qs ? `?${qs}` : ""}`),
  })
}
