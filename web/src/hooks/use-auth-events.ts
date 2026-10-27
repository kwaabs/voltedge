import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/** One row of the security audit log (iam.auth_events). user_* are null for an
 * event with no known subject (a login attempt on an unknown email) or a
 * since-deleted user. ip is the client IP (proxy-aware); user_agent is the
 * browser/client string. */
export interface AuthEvent {
  id: string
  user_id: string | null
  user_name: string | null
  user_email: string | null
  event_type: string
  detail: string | null
  ip: string | null
  user_agent: string | null
  created_at: string
}

export interface AuthEventFilter {
  userId?: string
  eventType?: string
  from?: string
  to?: string
  limit?: number
}

/** The security audit log, newest first (GET /auth/admin/events). Needs
 * admin:users (or a break-glass account) -- the same gate as the rest of the
 * user-management API. */
export function useAuthEvents(filter: AuthEventFilter) {
  const params = new URLSearchParams()
  if (filter.userId) params.set("user_id", filter.userId)
  if (filter.eventType) params.set("event_type", filter.eventType)
  if (filter.from) params.set("from", filter.from)
  if (filter.to) params.set("to", filter.to)
  if (filter.limit) params.set("limit", String(filter.limit))
  const qs = params.toString()
  return useQuery({
    queryKey: ["auth-events", filter],
    queryFn: () => apiFetch<{ events: AuthEvent[] }>(`/auth/admin/events${qs ? `?${qs}` : ""}`),
  })
}
