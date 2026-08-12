import { Hand, MousePointer2 } from "lucide-react"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import { useInteractionStore } from "@/stores/interaction-store"
import { useMapPointerStore } from "@/stores/map-pointer-store"

/**
 * The map's working/display projection -- OpenLayers' default view
 * projection, and what every layer here actually renders in
 * (EPSG:4326 point/line/polygon data is reprojected into this on the
 * fly). Not configurable yet, so a plain constant rather than something
 * read off the map instance.
 */
const MAP_PROJECTION = "EPSG:3857"

function CoordinateReadout() {
  const lon = useMapPointerStore((s) => s.lon)
  const lat = useMapPointerStore((s) => s.lat)

  if (lon === null || lat === null) {
    return <span className="tabular-nums">Lat / Lon: —, —</span>
  }
  return (
    <span className="tabular-nums">
      Lat / Lon: {lat.toFixed(5)}°, {lon.toFixed(5)}°
    </span>
  )
}

function ModeToggle() {
  const mode = useInteractionStore((s) => s.mode)
  const setMode = useInteractionStore((s) => s.setMode)

  return (
    <div className="flex items-center gap-0.5">
      <Button
        variant="ghost"
        size="icon"
        className={cn("size-6", mode === "select" && "bg-accent text-accent-foreground")}
        aria-pressed={mode === "select"}
        title="Select"
        onClick={() => setMode("select")}
      >
        <MousePointer2 className="size-3.5" />
      </Button>
      <Button
        variant="ghost"
        size="icon"
        className={cn("size-6", mode === "pan" && "bg-accent text-accent-foreground")}
        aria-pressed={mode === "pan"}
        title="Pan"
        onClick={() => setMode("pan")}
      >
        <Hand className="size-3.5" />
      </Button>
    </div>
  )
}

/**
 * A thin status bar, not another floating overlay -- coordinates,
 * projection, and required basemap attribution are reference info a
 * user glances at, not something worth covering map area with a badge
 * cluster for. Attribution text/link here is OSM's own required
 * wording (see https://www.openstreetmap.org/copyright) -- it replaces
 * OpenLayers' default Attribution control, which is disabled in
 * voltedge-map.tsx so it isn't shown twice.
 */
export function StatusBar() {
  return (
    <div className="bg-background flex h-7 shrink-0 items-center gap-4 border-t px-3 text-xs">
      <ModeToggle />
      <div className="bg-border h-4 w-px" />
      <CoordinateReadout />
      <span className="text-muted-foreground">{MAP_PROJECTION}</span>
      <a
        href="https://www.openstreetmap.org/copyright"
        target="_blank"
        rel="noreferrer"
        className="text-muted-foreground ml-auto hover:underline"
      >
        © OpenStreetMap contributors
      </a>
    </div>
  )
}
