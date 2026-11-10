import { useEffect } from "react"
import { useQueryClient } from "@tanstack/react-query"

// Generic Server-Sent Events subscriber. The backend SSE hub (api/internal/events)
// sends one channel at /api/events where every frame is
// `data: {"type":"<eventType>","data":...}` (the type is a JSON field, not an SSE
// `event:` name), so a single EventSource keyed on `evt.type` fans each event to the
// right React Query invalidations. This generalises the older per-feature streams
// (useAlarmStream, useAssignmentStream) for surfaces -- like the operations schematic
// -- that care about several event types at once.
//
// Event types emitted today (see api/internal/events call sites):
//   "alarms"         -- an alarm raised/cleared, or any device operation reconciled
//   "operations"     -- a device changed state (interactive op or switching step)
//   "wam.assignment" -- a work order assigned (per-user)
const EVENT_INVALIDATIONS: Record<string, readonly (readonly unknown[])[]> = {
  alarms: [["alarms"]],
  // A device-state change ripples into energisation, off-normal config, and the
  // affected-cone of any open incident -- refresh all three so the schematic tracks
  // a switching program live.
  operations: [["energization"], ["operations", "abnormal"], ["operations", "inventory"], ["oms", "incidents"]],
  "wam.assignment": [["activity", "my-open-work-orders"], ["wam"]],
}

/**
 * Subscribes to /api/events and invalidates the mapped query keys as events arrive.
 * Pass `enabled=false` to stay disconnected. One connection per mounted caller --
 * mount it once per surface that needs live push (e.g. the operations schematic).
 */
export function useEventStream(enabled = true) {
  const qc = useQueryClient()
  useEffect(() => {
    if (!enabled) return
    let es: EventSource | null = null
    try {
      es = new EventSource("/api/events")
      es.onmessage = (e) => {
        try {
          const evt = JSON.parse(e.data) as { type?: string }
          const keys = evt.type ? EVENT_INVALIDATIONS[evt.type] : undefined
          if (keys) for (const key of keys) qc.invalidateQueries({ queryKey: key as unknown[] })
        } catch {
          // Ignore a malformed frame (e.g. a keep-alive comment that slips through).
        }
      }
    } catch {
      // EventSource unavailable (very old browser / SSR) -- polling still refreshes.
    }
    return () => es?.close()
  }, [enabled, qc])
}
