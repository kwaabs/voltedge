import { useHealth } from "@/hooks/use-health"
import { cn } from "@/lib/utils"
import { useMapPointerStore } from "@/stores/map-pointer-store"

/**
 * The map's working/display projection -- OpenLayers' default view
 * projection, and what every layer here actually renders in
 * (EPSG:4326 point/line/polygon data is reprojected into this on the
 * fly). Not configurable yet, so a plain constant rather than something
 * read off the map instance.
 */
const MAP_PROJECTION = "EPSG:3857"

function ApiStatusIndicator() {
  const { data, isPending, isError } = useHealth()

  let dotClass = "bg-muted-foreground"
  let label = "Checking API..."
  if (!isPending) {
    if (isError || data?.status !== "ok") {
      dotClass = "bg-destructive"
      label = "API unreachable"
    } else {
      dotClass = "bg-emerald-500"
      label = "API healthy"
    }
  }

  return (
    <span className="flex items-center gap-1.5">
      <span className={cn("size-1.5 shrink-0 rounded-full", dotClass)} />
      {label}
    </span>
  )
}

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

/**
 * A thin status bar, not another floating overlay -- API reachability,
 * coordinates, projection, and required basemap attribution are
 * reference info a user glances at, not something worth covering map
 * area with a badge cluster for. Attribution text/link here is OSM's
 * own required wording (see https://www.openstreetmap.org/copyright) --
 * it replaces OpenLayers' default Attribution control, which is
 * disabled in voltedge-map.tsx so it isn't shown twice.
 */
export function StatusBar() {
  return (
    <div className="bg-background flex h-7 shrink-0 items-center gap-4 border-t px-3 text-xs">
      <ApiStatusIndicator />
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
