import { ChevronsUpDown, Map as MapIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useHealth } from "@/hooks/use-health"
import { cn } from "@/lib/utils"
import {
  BASEMAP_OPTIONS,
  basemapOption,
  type BasemapId,
  useBasemapStore,
} from "@/stores/basemap-store"
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
 * The basemap switcher -- the "select" the status bar exposes for
 * swapping the underlying tile layer (osm / carto voyager / blank). A
 * DropdownMenu radio group rather than a native/shadcn Select because
 * the app has no @radix-ui/react-select dependency, and this matches the
 * radio-group primitive the layers panel already uses. `side="top"` so
 * it opens up into the map, not down off the bottom edge of the window.
 * Writing `setBasemap` is all this does; voltedge-map.tsx watches the
 * store and performs the actual OL source swap.
 */
function BasemapPicker() {
  const basemap = useBasemapStore((s) => s.basemap)
  const setBasemap = useBasemapStore((s) => s.setBasemap)

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          className="text-muted-foreground hover:text-foreground h-5 gap-1.5 px-1.5 text-xs font-normal"
        >
          <MapIcon className="size-3.5" />
          {basemapOption(basemap).label}
          <ChevronsUpDown className="size-3 opacity-60" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" side="top" className="min-w-44">
        <DropdownMenuRadioGroup
          value={basemap}
          onValueChange={(value) => setBasemap(value as BasemapId)}
        >
          {BASEMAP_OPTIONS.map((option) => (
            <DropdownMenuRadioItem key={option.id} value={option.id}>
              {option.label}
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * The required attribution for whichever basemap is active -- driven off
 * BASEMAP_OPTIONS rather than hardcoded, so it stays correct as the
 * picker switches sources (OSM's wording for osm, OSM + CARTO for carto
 * voyager, and nothing at all for the blank choice, which has no tiles
 * to attribute). Replaces OpenLayers' own Attribution control, disabled
 * in voltedge-map.tsx so attribution isn't shown twice.
 */
function BasemapAttribution() {
  const basemap = useBasemapStore((s) => s.basemap)
  const { attribution } = basemapOption(basemap)

  if (attribution.length === 0) return null

  return (
    <span className="text-muted-foreground flex items-center gap-1">
      {attribution.map((link, i) => (
        <span key={link.href} className="flex items-center gap-1">
          {i > 0 && <span aria-hidden>·</span>}
          <a
            href={link.href}
            target="_blank"
            rel="noreferrer"
            className="hover:underline"
          >
            {link.label}
          </a>
        </span>
      ))}
    </span>
  )
}

/**
 * A thin status bar, not another floating overlay -- API reachability,
 * coordinates, projection, the basemap switcher, and required basemap
 * attribution are reference info/controls a user glances at, not
 * something worth covering map area with a badge cluster for. The
 * attribution text/links are the active basemap's own required wording
 * (see basemap-store.ts's BASEMAP_OPTIONS) -- they replace OpenLayers'
 * default Attribution control, which is disabled in voltedge-map.tsx so
 * it isn't shown twice.
 */
export function StatusBar() {
  return (
    <div className="bg-background flex h-7 shrink-0 items-center gap-4 border-t px-3 text-xs">
      <ApiStatusIndicator />
      <div className="bg-border h-4 w-px" />
      <CoordinateReadout />
      <span className="text-muted-foreground">{MAP_PROJECTION}</span>
      <div className="ml-auto flex items-center gap-3">
        <BasemapAttribution />
        <BasemapPicker />
      </div>
    </div>
  )
}
