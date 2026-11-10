import { useQueries, useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

// The field breadcrumb audit trail (migration 0178) read hooks — the supervisor
// map over where field workers physically were: their movement trails and the
// located stamp on every mutating edit. Gated server-side by field:tracks:read.

const KEY = ["field-tracks"] as const

/** A field user who has recorded a trail — the picker row. */
export interface TrackWorker {
  user_id: string
  display_name: string | null
  email: string | null
  last_seen: string
  session_days: number
}

/** One field session (one device-minted session_id) summary. */
export interface TrackSession {
  session_id: string
  user_id: string
  started_at: string
  ended_at: string
  point_count: number
  action_count: number
}

/** One breadcrumb on the movement trail. */
export interface TrackPoint {
  recorded_at: string
  longitude: number
  latitude: number
  accuracy_m: number | null
  speed_mps: number | null
}

/** One located edit (where the worker was when they made a mutating op). */
export interface TrackWaypoint {
  op_id: string
  kind: string
  feature_class: string | null
  feature_id: string | null
  recorded_at: string
  longitude: number
  latitude: number
  accuracy_m: number | null
}

export interface Trail {
  session_id: string
  points: TrackPoint[]
  waypoints: TrackWaypoint[]
}

/** Field users who have any recorded trail, most-recently-active first. */
export function useTrackWorkers() {
  return useQuery({
    queryKey: [...KEY, "workers"],
    queryFn: () => apiFetch<{ workers: TrackWorker[] }>("/field-tracks/workers"),
    staleTime: 60_000,
  })
}

/** A worker's field sessions, optionally bounded to [from, to) (RFC3339 or date). */
export function useTrackSessions(opts: { userId: string | null; from?: string; to?: string }) {
  const { userId, from, to } = opts
  return useQuery({
    queryKey: [...KEY, "sessions", userId ?? "", from ?? "", to ?? ""],
    queryFn: () => {
      const params = new URLSearchParams({ user_id: userId! })
      if (from) params.set("from", from)
      if (to) params.set("to", to)
      return apiFetch<{ sessions: TrackSession[] }>(`/field-tracks/sessions?${params.toString()}`)
    },
    enabled: userId != null,
    staleTime: 30_000,
  })
}

/** One session's trail: the ordered breadcrumbs + the located action pins. */
export function useTrail(sessionId: string | null) {
  return useQuery({
    queryKey: [...KEY, "trail", sessionId ?? ""],
    queryFn: () => apiFetch<Trail>(`/field-tracks/sessions/${sessionId}`),
    enabled: sessionId != null,
    staleTime: 30_000,
  })
}

/** Several sessions' trails at once — the supervisor can overlay multiple workers'
 * trails on one map (capped by the caller). Results are index-aligned to
 * `sessionIds` and share the same cache as useTrail (identical queryKey), so a
 * trail already opened singly is reused. */
export function useTrails(sessionIds: string[]) {
  return useQueries({
    queries: sessionIds.map((id) => ({
      queryKey: [...KEY, "trail", id],
      queryFn: () => apiFetch<Trail>(`/field-tracks/sessions/${id}`),
      staleTime: 30_000,
    })),
  })
}
