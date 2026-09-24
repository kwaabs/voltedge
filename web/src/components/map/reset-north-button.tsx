import { Navigation2 } from "lucide-react"

import { useMapViewStore } from "@/stores/map-view-store"

/** Rotations smaller than this (radians) read as "north up" -- below a pixel of
 * visible tilt -- so the button stays hidden rather than flickering on tiny
 * residual rotations. */
const NORTH_EPSILON = 1e-3

/**
 * A "reset north" button that sits just below the map's zoom control (top-left)
 * and appears only when the view has been rotated off north -- most useful on
 * touch devices, where a two-finger twist rotates the map and there's otherwise
 * no easy way back. The arrow leans to show where north currently points (like
 * OpenLayers' own rotate control, which we disable in favour of this so there
 * isn't a duplicate at top-right); tapping it animates the view back to 0.
 *
 * It reads rotation from the module-level map-view store (pushed live from
 * voltedge-map's change:rotation handler), so only this small button re-renders
 * as the map is twisted, not the whole map component.
 */
export function ResetNorthButton({ onReset }: { onReset: () => void }) {
  const rotation = useMapViewStore((s) => s.rotation)
  if (Math.abs(rotation) < NORTH_EPSILON) return null

  return (
    <button
      type="button"
      onClick={onReset}
      title="Reset north"
      aria-label="Reset north"
      className="bg-background/90 text-foreground hover:bg-accent absolute top-16 left-2 z-10 flex size-8 items-center justify-center rounded-md border shadow-sm"
    >
      {/* Arrow points to geographic north: rotate by the view rotation, matching
          OpenLayers' rotate control. */}
      <Navigation2 className="text-primary size-4" style={{ transform: `rotate(${rotation}rad)` }} />
    </button>
  )
}
