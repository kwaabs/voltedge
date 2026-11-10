import { useEffect } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

// The operator ALARM ANNUNCIATOR (backend: api/internal/alarms, /alarms). A
// prioritised, acknowledgeable worklist fed by device operations + off-normal
// configuration. The list/count poll is the source of truth; an SSE "alarms" event
// nudges a live refetch (useAlarmStream). All reads need oms:read, writes oms:write.

/** One alarm row. `active` = the condition is present now (standing); an event alarm
 * is inactive and shows until acknowledged. Shelved alarms are hidden from the live
 * list until `shelved_until`. */
export interface Alarm {
  id: string
  source: string
  category: string
  severity: "critical" | "high" | "medium" | "low"
  message: string
  device_class?: string | null
  device_id?: string | null
  active: boolean
  occurrence_count: number
  raised_at: string
  last_raised_at: string
  cleared_at?: string | null
  acknowledged_at?: string | null
  acknowledged_by?: string | null
  acknowledged_by_name?: string | null
  shelved_until?: string | null
  shelve_reason?: string | null
}

/** Live annunciator counts for the toolbar badge. */
export interface AlarmCounts {
  total: number
  unacknowledged: number
  critical: number
}

export interface AlarmFilter {
  status?: "live" | "shelved" | "cleared" | "all"
  severity?: "critical" | "high" | "medium" | "low"
  source?: string
}

/** The alarm queue (default: the live annunciator, severity-first). `enabled`
 * gates the fetch so an overlay that's toggled off costs nothing. */
export function useAlarms(filter: AlarmFilter = {}, enabled = true) {
  const params = new URLSearchParams()
  if (filter.status) params.set("status", filter.status)
  if (filter.severity) params.set("severity", filter.severity)
  if (filter.source) params.set("source", filter.source)
  const qs = params.toString()
  return useQuery({
    queryKey: ["alarms", "list", filter],
    queryFn: () => apiFetch<{ alarms: Alarm[] }>(`/alarms${qs ? `?${qs}` : ""}`),
    enabled,
    staleTime: 15_000,
  })
}

/** Live alarm counts for a badge. */
export function useAlarmCounts(enabled = true) {
  return useQuery({
    queryKey: ["alarms", "count"],
    queryFn: () => apiFetch<AlarmCounts>("/alarms/count"),
    enabled,
    refetchInterval: 30_000,
    staleTime: 15_000,
  })
}

export function useAcknowledgeAlarm() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiFetch(`/alarms/${id}/ack`, { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["alarms"] }),
  })
}

export function useShelveAlarm() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, until, reason }: { id: string; until: string; reason?: string }) =>
      apiFetch(`/alarms/${id}/shelve`, { method: "POST", body: JSON.stringify({ until, reason: reason ?? "" }) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["alarms"] }),
  })
}

export function useUnshelveAlarm() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiFetch(`/alarms/${id}/unshelve`, { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["alarms"] }),
  })
}

/** Rescan the standing sources (off-normal devices) so their alarms are current.
 * The annunciator calls this on mount and offers it as a manual "Rescan". */
export function useReconcileAlarms() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => apiFetch<AlarmCounts>("/alarms/reconcile", { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["alarms"] }),
  })
}

/** Subscribe to the live event stream and refetch alarms the instant any operator
 * (on any replica, via the broadcast) changes the annunciator — push, with the
 * list/count poll as the fallback. Same-origin, cookie-authenticated; reconnects on
 * its own. Gated to a user who can see the annunciator. */
export function useAlarmStream(enabled: boolean) {
  const qc = useQueryClient()
  useEffect(() => {
    if (!enabled) return
    const es = new EventSource("/api/events")
    es.onmessage = (e) => {
      try {
        const evt = JSON.parse(e.data) as { type?: string }
        if (evt.type === "alarms") qc.invalidateQueries({ queryKey: ["alarms"] })
      } catch {
        // ignore a malformed frame
      }
    }
    return () => es.close()
  }, [enabled, qc])
}
