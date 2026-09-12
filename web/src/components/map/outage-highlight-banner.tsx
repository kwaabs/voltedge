import { useEffect, useState } from "react"
import { Siren, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import type { AffectedArea } from "@/hooks/use-oms"
import { apiFetch } from "@/lib/api"
import { useOmsHighlightStore } from "@/stores/oms-highlight-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

/**
 * Floating banner shown while the map is highlighting an outage's affected
 * service points (the outage-affected layer in voltedge-map.tsx, driven by
 * oms-highlight-store.ts). It exists for one reason: to give that rose
 * highlight an explicit dismiss control. The highlight is written from the
 * /outages board ("Show on map") and the store is a module-level singleton, so
 * before this the highlight could only be cleared by selecting another incident
 * or reloading -- a papercut once you've panned away and forgotten which
 * incident is lit.
 *
 * Self-contained, same shape as trace-result-table.tsx: reads the store
 * directly and returns null when nothing is highlighted rather than being
 * conditionally rendered by a parent. Its X calls store.clear() -- the map's
 * affected-area effect keys on incidentId and wipes the layer when it goes
 * null, so no map coordination is needed here.
 *
 * Fetches the affected area itself (the same GET the map overlay uses) only to
 * label the banner with the blamed device and the highlighted count; a failed
 * or pending fetch still shows the banner with its Clear button, since the
 * dismiss control is the point, not the label.
 */
export function OutageHighlightBanner() {
  const incidentId = useOmsHighlightStore((s) => s.incidentId)
  const [area, setArea] = useState<AffectedArea | null>(null)

  useEffect(() => {
    if (!incidentId) {
      setArea(null)
      return
    }
    let cancelled = false
    setArea(null)
    apiFetch<AffectedArea>(`/oms/incidents/${incidentId}/affected`)
      .then((a) => {
        if (!cancelled) setArea(a)
      })
      .catch(() => {
        // The label just falls back to a generic string; the Clear button,
        // which needs no fetch, is what this banner is really for.
      })
    return () => {
      cancelled = true
    }
  }, [incidentId])

  if (!incidentId) return null

  const count = area?.service_points.length
  const device = area?.device_class
    ? `${area.device_class}${area.device_id ? ` · ${shortId(area.device_id)}` : ""}`
    : null

  return (
    <div className="bg-popover text-popover-foreground fixed top-4 left-1/2 z-40 flex max-w-[calc(100vw-2rem)] -translate-x-1/2 items-center gap-3 rounded-md border px-3 py-2 shadow-lg">
      <Siren className="size-4 shrink-0 text-rose-600" />
      <div className="min-w-0 text-sm">
        <p className="font-medium">Outage impact highlighted</p>
        <p className="text-muted-foreground truncate text-xs">
          {device ? `${device} · ` : ""}
          {count != null
            ? `${count} service point${count === 1 ? "" : "s"} affected`
            : "affected service points on map"}
        </p>
      </div>
      <Button
        variant="ghost"
        size="icon"
        className="size-6 shrink-0"
        onClick={() => useOmsHighlightStore.getState().clear()}
      >
        <X className="size-3.5" />
        <span className="sr-only">Clear outage highlight</span>
      </Button>
    </div>
  )
}
