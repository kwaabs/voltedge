import { type FormEvent, useState } from "react"
import { ChevronsUpDown, Map as MapIcon, MapPin } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { type AttributionLink, useBasemaps } from "@/hooks/use-basemaps"
import { useHealth } from "@/hooks/use-health"
import { cn } from "@/lib/utils"
import { BLANK_BASEMAP, useBasemapStore } from "@/stores/basemap-store"
import { useMapGotoStore } from "@/stores/map-goto-store"
import { useMapPinsStore } from "@/stores/map-pins-store"
import { useMapPointerStore } from "@/stores/map-pointer-store"
import { useMapViewStore } from "@/stores/map-view-store"
import { PinsDialog } from "./pins-dialog"

/** The picker's built-in "no basemap" entry -- appended to the server list, it
 * is never a catalog row (see basemap-store.ts's BLANK_BASEMAP). */
const BLANK_LABEL = "Blank (no basemap)"

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
 * The current map zoom level, pushed from voltedge-map.tsx's moveend handler
 * (map-view-store.ts). Shown to one decimal because OL zoom is continuous
 * (mouse-wheel/pinch land between integer levels), so "12.3" is honest where a
 * rounded "12" would imply a snap that didn't happen. Em dash until the map has
 * rendered once and published a zoom.
 */
function ZoomReadout() {
  const zoom = useMapViewStore((s) => s.zoom)
  return <span className="tabular-nums">Zoom: {zoom === null ? "—" : zoom.toFixed(1)}</span>
}

/**
 * Parses a "lat, lng" (or "lat lng") coordinate string into a lon/lat pair,
 * or null when it isn't two in-range numbers. Latitude first -- the order the
 * coordinate readout to its left prints, and what a user copying a pin from a
 * web map pastes. Accepts an optional trailing degree sign on either number so
 * pasting "5.60000°, -0.18000°" works too.
 */
function parseLatLng(input: string): { lat: number; lng: number } | null {
  const parts = input
    .trim()
    .split(/[\s,]+/)
    .map((p) => p.replace(/°$/, ""))
    .filter(Boolean)
  if (parts.length !== 2) return null
  const lat = Number(parts[0])
  const lng = Number(parts[1])
  if (!Number.isFinite(lat) || !Number.isFinite(lng)) return null
  if (lat < -90 || lat > 90 || lng < -180 || lng > 180) return null
  return { lat, lng }
}

/**
 * The go-to-coordinates search -- type a "lat, lng" and the map animates there
 * (map-goto-store.ts's one-shot token -> voltedge-map.tsx). Coordinate-only for
 * now; place-name geocoding is deferred (it needs an external geocoder). A
 * plain form so Enter submits; the input turns red only once the text is
 * non-empty and unparseable, so it doesn't nag while you're still typing.
 */
function GoToSearch() {
  const [value, setValue] = useState("")
  const goTo = useMapGotoStore((s) => s.goTo)

  const parsed = parseLatLng(value)
  const invalid = value.trim() !== "" && parsed === null

  function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!parsed) return
    goTo({ lng: parsed.lng, lat: parsed.lat })
  }

  return (
    <form onSubmit={handleSubmit} className="flex items-center">
      <div className="relative">
        <MapPin className="text-muted-foreground pointer-events-none absolute top-1/2 left-1.5 size-3 -translate-y-1/2" />
        <input
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="Go to lat, lng"
          aria-label="Go to coordinates"
          aria-invalid={invalid}
          className={cn(
            "border-input bg-background text-foreground placeholder:text-muted-foreground h-5 w-36 rounded border pr-1.5 pl-6 text-xs tabular-nums outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]",
            invalid && "border-destructive focus-visible:border-destructive focus-visible:ring-destructive/50",
          )}
        />
      </div>
    </form>
  )
}

/**
 * The saved-pins (bookmarks) opener -- shows the current pin count and opens
 * the pins manager (pins-dialog.tsx). Pins are per-browser navigation
 * bookmarks (map-pins-store.ts); the go-to search to its left just recenters,
 * this is where a location is actually saved and revisited.
 */
function PinsButton() {
  const [open, setOpen] = useState(false)
  const count = useMapPinsStore((s) => s.pins.length)

  return (
    <>
      <Button
        variant="ghost"
        size="sm"
        onClick={() => setOpen(true)}
        className="text-slate-200 dark:text-muted-foreground hover:text-foreground h-5 gap-1.5 px-1.5 text-xs font-normal"
      >
        <MapPin className="size-3.5" />
        Pins
        {count > 0 && <span className="tabular-nums">({count})</span>}
      </Button>
      <PinsDialog open={open} onOpenChange={setOpen} />
    </>
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
  const { data: basemaps } = useBasemaps()

  // The active choice's display label: the matching catalog row, the blank
  // built-in, or -- while the list is still loading or after a deleted code --
  // the raw code as a harmless placeholder.
  const activeLabel =
    basemap === BLANK_BASEMAP
      ? BLANK_LABEL
      : (basemaps?.find((b) => b.code === basemap)?.label ?? basemap)

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          className="text-slate-200 hover:text-white dark:text-muted-foreground dark:hover:text-foreground h-5 gap-1.5 px-1.5 text-xs font-normal"
        >
          <MapIcon className="size-3.5" />
          {activeLabel}
          <ChevronsUpDown className="size-3 opacity-60" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" side="top" className="min-w-44">
        <DropdownMenuRadioGroup value={basemap} onValueChange={setBasemap}>
          {(basemaps ?? []).map((option) => (
            <DropdownMenuRadioItem key={option.code} value={option.code}>
              {option.label}
            </DropdownMenuRadioItem>
          ))}
          {/* Blank is always offered, after the server list -- it's a
              client-side built-in, never a catalog row. */}
          <DropdownMenuRadioItem value={BLANK_BASEMAP}>
            {BLANK_LABEL}
          </DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

/**
 * The required attribution for whichever basemap is active -- read off the
 * active record in the fetched catalog (hooks/use-basemaps.ts) rather than
 * hardcoded, so it stays correct as the picker switches sources and as an
 * admin edits the list (and shows nothing for the blank choice, which has no
 * tiles to attribute). Replaces OpenLayers' own Attribution control, disabled
 * in voltedge-map.tsx so attribution isn't shown twice.
 */
function BasemapAttribution() {
  const basemap = useBasemapStore((s) => s.basemap)
  const { data: basemaps } = useBasemaps()

  // The active record's own required wording (blank and any unresolved code
  // attribute nothing). Resolved off the fetched catalog so it tracks the
  // picker as sources switch.
  const attribution: AttributionLink[] =
    basemap === BLANK_BASEMAP
      ? []
      : (basemaps?.find((b) => b.code === basemap)?.attribution ?? [])

  if (attribution.length === 0) return null

  return (
    <span className="text-slate-200 dark:text-muted-foreground flex items-center gap-1">
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
 * (from the fetched catalog, hooks/use-basemaps.ts) -- they replace
 * OpenLayers' default Attribution control, which is disabled in
 * voltedge-map.tsx so it isn't shown twice.
 */
export function StatusBar() {
  // Same medium dark-blue surface as the header (app-header.tsx) in LIGHT MODE
  // ONLY, with light text so the readouts stay legible; dark mode is left
  // untouched via `dark:` overrides back to the neutral theme tokens. Soft
  // upward shadow lifts it off the map (custom -y shadow because Tailwind's
  // shadow utilities only cast downward).
  return (
    <div className="bg-[oklch(0.45_0.10_252)] text-slate-100 dark:bg-background dark:text-foreground flex h-7 shrink-0 items-center gap-4 border-t border-white/10 dark:border-border px-3 text-xs shadow-[0_-1px_3px_rgb(0_0_0/0.15)]">
      <ApiStatusIndicator />
      <div className="bg-border h-4 w-px" />
      <CoordinateReadout />
      <ZoomReadout />
      <span className="text-slate-200 dark:text-muted-foreground">{MAP_PROJECTION}</span>
      <div className="bg-border h-4 w-px" />
      <GoToSearch />
      <div className="ml-auto flex items-center gap-3">
        <BasemapAttribution />
        <PinsButton />
        <BasemapPicker />
      </div>
    </div>
  )
}
