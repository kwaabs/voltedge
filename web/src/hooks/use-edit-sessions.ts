import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/** One edit-mode session: a batch of changes made between clicking Edit and View,
 * grouped by the edit_session_id stamped on each feature_history row. Only shows
 * sessions where something actually changed (an empty session writes no history).
 * started_at/ended_at are the first/last change in the session; classes is a
 * comma-joined list of the distinct feature classes it touched. */
export interface EditSession {
  session_id: string
  actor_id: string | null
  actor_name: string | null
  actor_email: string | null
  started_at: string
  ended_at: string
  change_count: number
  classes: string
}

export interface EditSessionFilter {
  actorId?: string
  /** RFC3339 timestamps -- a session counts if it ended at/after `from` and started before `to`. */
  from?: string
  to?: string
  limit?: number
}

/** The edit-session roll-up, most-recently-ended first. Needs admin:users (the
 * endpoint escalates above its mount's features:read -- aggregating what people
 * did is a monitoring capability). */
export function useEditSessions(filter: EditSessionFilter) {
  const params = new URLSearchParams()
  if (filter.actorId) params.set("actor_id", filter.actorId)
  if (filter.from) params.set("from", filter.from)
  if (filter.to) params.set("to", filter.to)
  if (filter.limit) params.set("limit", String(filter.limit))
  const qs = params.toString()
  return useQuery({
    queryKey: ["edit-sessions", filter],
    queryFn: () => apiFetch<{ edit_sessions: EditSession[] }>(`/features/edit-sessions${qs ? `?${qs}` : ""}`),
  })
}
