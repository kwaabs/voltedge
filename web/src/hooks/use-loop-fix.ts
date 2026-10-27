import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"
import type { Connection } from "@/hooks/use-connections"

/** One switchable device on a loop that could be OPENed to break it. Mirrors
 * api/internal/trace LoopBreakCandidate. */
export interface LoopBreakCandidate {
  feature_class: string
  feature_id: string
  device_state?: string
  recommended?: boolean
}

/** One closed loop + the devices that could break it (api/internal/trace
 * LoopBreakRing). */
export interface LoopBreakRing {
  features: { feature_class: string; feature_id: string }[]
  candidates: LoopBreakCandidate[]
}

interface LoopBreakResponse {
  rings: LoopBreakRing[]
  truncated: boolean
}

/** POST /trace/loops/break-options -- the switchable devices on each live closed
 * loop reachable from a feature. */
export function useLoopBreakOptions(start: { feature_class: string; feature_id: string } | null) {
  return useQuery({
    queryKey: ["loop-break-options", start?.feature_class, start?.feature_id],
    queryFn: () =>
      apiFetch<LoopBreakResponse>("/trace/loops/break-options", {
        method: "POST",
        body: JSON.stringify({ start }),
      }),
    enabled: !!start,
  })
}

/** Open or close one device (POST /oms/devices/{class}/{id}/operate). The
 * operational loop fix: open a point to break the ring; close it back to reconnect.
 * Invalidates break-options + the feature's connections/state caches. */
export function useOperateDevice() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ featureClass, featureId, state }: { featureClass: string; featureId: string; state: "OPEN" | "CLOSED" }) =>
      apiFetch(`/oms/devices/${encodeURIComponent(featureClass)}/${encodeURIComponent(featureId)}/operate`, {
        method: "POST",
        body: JSON.stringify({ state }),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["loop-break-options"] })
      qc.invalidateQueries({ queryKey: ["ring-connections"] })
    },
  })
}

/** Revoke (permanently delete) one connectivity edge (DELETE /connectivity/{id}) --
 * the data-error loop fix: remove a wrong connection. */
export function useRevokeConnection() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (connectionId: string) => apiFetch(`/connectivity/${connectionId}`, { method: "DELETE" }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["ring-connections"] })
      qc.invalidateQueries({ queryKey: ["loop-break-options"] })
      qc.invalidateQueries({ queryKey: ["connections"] })
    },
  })
}

/** The connectivity edges that lie WITHIN a ring (both endpoints are ring members),
 * fanned out over the members' connection lists and deduped by edge id -- the
 * "review the loop's connections" list to revoke a wrong one from. */
export function useRingConnections(ring: { feature_class: string; feature_id: string }[] | null) {
  const key = (ring ?? [])
    .map((f) => `${f.feature_class}:${f.feature_id}`)
    .sort()
    .join(",")
  return useQuery({
    queryKey: ["ring-connections", key],
    queryFn: async () => {
      const members = ring ?? []
      const memberSet = new Set(members.map((f) => `${f.feature_class}:${f.feature_id}`))
      const lists = await Promise.all(
        members.map((m) =>
          apiFetch<{ connections: Connection[] }>(
            `/connectivity?feature_class=${encodeURIComponent(m.feature_class)}&feature_id=${encodeURIComponent(m.feature_id)}`,
          ),
        ),
      )
      const byId = new Map<string, Connection>()
      for (const list of lists) {
        for (const c of list.connections) {
          const a = `${c.from.feature_class}:${c.from.feature_id}`
          const b = `${c.to.feature_class}:${c.to.feature_id}`
          if (memberSet.has(a) && memberSet.has(b)) byId.set(c.id, c)
        }
      }
      return [...byId.values()]
    },
    enabled: !!ring && ring.length > 0,
  })
}
