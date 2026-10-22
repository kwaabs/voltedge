import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/** One change in the signed-in user's own activity feed -- who is always the
 * caller, so only what/when/which-feature matter here. Mirrors the backend
 * ActivityEntry (crud/activity.go): the feature class + id let the panel locate
 * and open the changed feature on the map; `at` is an ISO timestamp. */
export interface MyActivityEntry {
  id: number
  feature_class_code: string
  feature_id: string
  action: string
  at: string
}

/** The panel's filters. actor is NEVER sent -- the server pins it to the caller.
 * `from`/`to` are RFC3339 strings; all optional (an empty filter is the whole
 * recent feed). */
export interface MyActivityFilter {
  featureClass?: string
  action?: string
  from?: string
  to?: string
  limit?: number
}

/** The signed-in user's recent changes, newest first (GET /features/my-activity).
 * Self-scoped on the server to the caller, so it needs no monitoring permission;
 * a viewer who makes no writes simply gets an empty list. Only fetched while the
 * panel is open, so an unopened "My edits" panel costs nothing. The filter is part
 * of the query key, so changing it refetches. */
export function useMyActivity(enabled: boolean, filter: MyActivityFilter = {}) {
  const params = new URLSearchParams()
  params.set("limit", String(filter.limit ?? 200))
  if (filter.featureClass) params.set("feature_class", filter.featureClass)
  if (filter.action) params.set("action", filter.action)
  if (filter.from) params.set("from", filter.from)
  if (filter.to) params.set("to", filter.to)
  const qs = params.toString()
  return useQuery({
    queryKey: ["my-activity", qs],
    queryFn: () => apiFetch<{ activity: MyActivityEntry[] }>(`/features/my-activity?${qs}`),
    enabled,
  })
}
