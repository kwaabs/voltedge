import { useEffect, useRef } from "react"
import { X } from "lucide-react"

import { useImportedFeatureStore } from "@/stores/imported-feature-store"

/** Keeps the popup fully on-screen regardless of where in the viewport the click landed. */
const MARGIN = 8
const WIDTH = 288

/** Renders a property value for display: primitives as-is, everything else
 * (nested objects/arrays a source file may carry) as compact JSON. Empty and
 * null-ish values collapse to an em dash so a row never looks broken. */
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

/**
 * A small floating card showing the attributes of a clicked imported-overlay
 * feature (Phase 15 Slice 5). Imported overlays are read-only reference layers
 * that never touch the database, so they can't open the DB-coupled FeaturePanel
 * -- this is their equivalent: the source overlay's name plus every property the
 * feature carried in from its GeoJSON/KML/shapefile/DXF (DXF v1 carries none, so
 * the card then just names the overlay). Positioned with `fixed` at the click's
 * viewport coordinates and clamped so it never renders off-screen, mirroring
 * PeerPicker. Dismissed by the next map click (voltedge-map.tsx clears the store
 * on any non-imported click), an outside click, or Escape.
 */
export function ImportedFeaturePopup() {
  const selected = useImportedFeatureStore((s) => s.selected)
  const clear = useImportedFeatureStore((s) => s.clear)
  const containerRef = useRef<HTMLDivElement>(null)

  // Close on a click outside the card or an Escape press -- the map's own
  // click handler already replaces/clears this on the next click that lands
  // back on the map, but a click elsewhere in the UI never reaches it.
  useEffect(() => {
    if (!selected) return

    const handlePointerDown = (evt: PointerEvent) => {
      if (!containerRef.current?.contains(evt.target as Node)) clear()
    }
    const handleKeyDown = (evt: KeyboardEvent) => {
      if (evt.key === "Escape") clear()
    }

    document.addEventListener("pointerdown", handlePointerDown)
    document.addEventListener("keydown", handleKeyDown)
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown)
      document.removeEventListener("keydown", handleKeyDown)
    }
  }, [selected, clear])

  if (!selected) return null

  const entries = Object.entries(selected.properties)
  const left = Math.min(Math.max(selected.position.x, MARGIN), window.innerWidth - WIDTH - MARGIN)
  const top = Math.min(Math.max(selected.position.y, MARGIN), window.innerHeight - 260 - MARGIN)

  return (
    <div
      ref={containerRef}
      style={{ left, top, width: WIDTH }}
      className="bg-popover text-popover-foreground fixed z-50 overflow-hidden rounded-md border shadow-lg"
    >
      <div className="flex items-start justify-between gap-2 border-b px-3 py-2">
        <div className="min-w-0">
          <p className="text-muted-foreground text-[10px] tracking-wide uppercase">Imported feature</p>
          <p className="truncate text-sm font-medium">{selected.overlayName}</p>
        </div>
        <button
          type="button"
          onClick={() => clear()}
          className="text-muted-foreground hover:text-foreground -mr-1 shrink-0 rounded-sm p-0.5"
          aria-label="Close"
        >
          <X className="size-4" />
        </button>
      </div>

      {entries.length === 0 ? (
        <p className="text-muted-foreground px-3 py-3 text-xs">This feature carries no attributes.</p>
      ) : (
        <dl className="max-h-64 divide-y overflow-y-auto">
          {entries.map(([key, value]) => (
            <div key={key} className="grid grid-cols-[minmax(0,7rem)_1fr] gap-2 px-3 py-1.5">
              <dt className="text-muted-foreground truncate text-xs" title={key}>
                {key}
              </dt>
              <dd className="text-foreground min-w-0 font-mono text-xs break-words">{formatValue(value)}</dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  )
}
