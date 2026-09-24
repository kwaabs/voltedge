import { useEffect, useState } from "react"
import { ChevronLeft, ChevronRight, Loader2, MapPin, X } from "lucide-react"

import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { type IdentifyLayerGroup, useIdentifyStore } from "@/stores/identify-store"
import { cn } from "@/lib/utils"

/** Renders one attribute value: primitives as-is, objects/arrays as compact JSON,
 * empty/null-ish as an em dash so a row never reads as broken. */
function formatValue(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—"
  if (typeof value === "object") {
    try {
      return JSON.stringify(value)
    } catch {
      return String(value)
    }
  }
  return String(value)
}

/** One layer's results: its label, and -- when a click lands on several stacked
 * features -- a compact "Feature n of N" stepper so they page through one at a
 * time instead of a long scroll. Own index state; remounted per click (keyed on
 * the click point in the parent), so it starts at the first feature each time. */
function IdentifyGroup({ group }: { group: IdentifyLayerGroup }) {
  const [index, setIndex] = useState(0)
  const count = group.features.length
  // Guard against a stale index if this instance is ever reused with fewer hits.
  const current = count > 0 ? group.features[Math.min(index, count - 1)] : null

  return (
    <div className="mb-3 last:mb-0">
      <div className="mb-1 flex items-center justify-between gap-2 px-1">
        <p className="text-muted-foreground truncate text-[10px] font-medium tracking-wide uppercase">
          {group.layerLabel}
        </p>
        {count > 1 && (
          <div className="text-muted-foreground flex shrink-0 items-center gap-1 text-[10px]">
            <button
              type="button"
              onClick={() => setIndex((i) => Math.max(0, i - 1))}
              disabled={index <= 0}
              className="hover:text-foreground rounded-sm p-0.5 disabled:opacity-40"
              aria-label="Previous feature"
            >
              <ChevronLeft className="size-3.5" />
            </button>
            <span className="tabular-nums">
              {Math.min(index, count - 1) + 1} / {count}
            </span>
            <button
              type="button"
              onClick={() => setIndex((i) => Math.min(count - 1, i + 1))}
              disabled={index >= count - 1}
              className="hover:text-foreground rounded-sm p-0.5 disabled:opacity-40"
              aria-label="Next feature"
            >
              <ChevronRight className="size-3.5" />
            </button>
          </div>
        )}
      </div>
      {group.error ? (
        <p className="text-destructive px-1 py-1">Could not identify: {group.error}</p>
      ) : (
        current && (
          <div className="border-border/60 rounded-md border px-2 py-1.5">
            {current.label && <p className="mb-1 truncate text-xs font-medium">{current.label}</p>}
            <dl className="divide-border/40 divide-y">
              {Object.entries(current.properties).map(([key, value]) => (
                <div key={key} className="grid grid-cols-[minmax(0,7rem)_1fr] gap-2 py-1">
                  <dt className="text-muted-foreground truncate" title={key}>
                    {key}
                  </dt>
                  <dd className="text-foreground min-w-0 font-mono break-words">{formatValue(value)}</dd>
                </div>
              ))}
            </dl>
          </div>
        )
      )}
    </div>
  )
}

/**
 * The click-to-identify result panel for external service layers (Slice 4). A
 * click that misses every DB feature but hits a visible, queryable service layer
 * opens this (see the identify branch in voltedge-map.tsx). It's a floating,
 * draggable/resizable panel -- results can be several features across several
 * layers -- and read-only: these features belong to a third-party service and
 * have no VoltEdge identity, so unlike FeaturePanel there's nothing to edit or
 * relate. Closed with its X or Escape; the next identifying click replaces it.
 */
export function ServiceIdentifyPanel() {
  const open = useIdentifyStore((s) => s.open)
  const loading = useIdentifyStore((s) => s.loading)
  const lonLat = useIdentifyStore((s) => s.lonLat)
  const groups = useIdentifyStore((s) => s.groups)
  const close = useIdentifyStore((s) => s.close)
  const fp = useFloatingPanel(360, "max-h-[70vh]")

  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [open, close])

  if (!open) return null

  // Only layers that actually reported something (or errored) are worth a row.
  const shown = groups.filter((g) => g.features.length > 0 || g.error)

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-2">
          {fp.grip}
          <MapPin className="text-primary/80 size-3.5 shrink-0" />
          <div className="min-w-0">
            <p className="text-muted-foreground text-[10px] tracking-wide uppercase">Identify</p>
            {lonLat && (
              <p className="truncate font-mono text-xs">
                {lonLat[1].toFixed(5)}, {lonLat[0].toFixed(5)}
              </p>
            )}
          </div>
        </div>
        <button
          type="button"
          onClick={() => close()}
          className="text-muted-foreground hover:text-foreground -mr-1 shrink-0 rounded-sm p-0.5"
          aria-label="Close"
        >
          <X className="size-4" />
        </button>
      </div>

      <div className={cn("overflow-y-auto p-2 text-xs", fp.resized && "flex-1")}>
        {loading ? (
          <p className="text-muted-foreground flex items-center gap-2 px-2 py-3">
            <Loader2 className="size-3.5 animate-spin" /> Querying service layers…
          </p>
        ) : shown.length === 0 ? (
          <p className="text-muted-foreground px-2 py-3">No features found at this point.</p>
        ) : (
          shown.map((group) => (
            // Key includes the click point so a new identify remounts each group,
            // resetting its stepper to the first feature.
            <IdentifyGroup key={`${lonLat?.join(",") ?? ""}-${group.layerId}`} group={group} />
          ))
        )}
      </div>

      {fp.resizeHandle}
    </div>
  )
}
