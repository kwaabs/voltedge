import { boundingExtent } from "ol/extent"
import Feature from "ol/Feature"
import Point from "ol/geom/Point"
import TileLayer from "ol/layer/Tile"
import VectorLayer from "ol/layer/Vector"
import Map from "ol/Map"
import { fromLonLat } from "ol/proj"
import type TileSource from "ol/source/Tile"
import VectorSource from "ol/source/Vector"
import { Circle as CircleStyle, Fill, RegularShape, Stroke, Style, Text } from "ol/style"
import View from "ol/View"
import { PowerOff } from "lucide-react"
import { useEffect, useMemo, useRef } from "react"

import { useBasemaps } from "@/hooks/use-basemaps"
import { type AffectedServicePoint, useAffectedArea, useIncidentEnergization } from "@/hooks/use-oms"
import { applyBasemap, resolveBasemap } from "@/lib/basemaps"
import { useBasemapStore } from "@/stores/basemap-store"

// A self-contained OpenLayers map embedded in the outage console (its own map
// instance, deliberately NOT the main /map). It shows ONE selected incident:
// the blamed device, the frozen outage-cone service points (amber), and the
// LIVE de-energized service points (red) that shrink as switching steps are
// executed. That live shrink is the visual half of "mimic a real OMS" -- an
// operator watches the red cone collapse to nothing as the crew closes the
// device. Distinct from the main map's outage-highlight layer (which reuses the
// full feature map); this is a compact, purpose-built control-room panel.

// Amber = in the frozen affected cone but currently on supply (restored, or
// never actually cut by the live switch state).
const RESTORED_SP_STYLE = new Style({
  image: new CircleStyle({
    radius: 4,
    fill: new Fill({ color: "#f59e0b" }),
    stroke: new Stroke({ color: "#ffffff", width: 1 }),
  }),
})

// Red = de-energized right now.
const OFF_SP_STYLE = new Style({
  image: new CircleStyle({
    radius: 5,
    fill: new Fill({ color: "#dc2626" }),
    stroke: new Stroke({ color: "#ffffff", width: 1 }),
  }),
})

function deviceStyleWithLabel(label: string): Style {
  return new Style({
    image: new RegularShape({
      points: 3,
      radius: 9,
      fill: new Fill({ color: "#1d4ed8" }),
      stroke: new Stroke({ color: "#ffffff", width: 1.5 }),
    }),
    text: new Text({
      text: label,
      offsetY: -16,
      font: "600 11px ui-sans-serif, system-ui",
      fill: new Fill({ color: "#1d4ed8" }),
      stroke: new Stroke({ color: "#ffffff", width: 3 }),
    }),
  })
}

export function OutageMap({ incidentId }: { incidentId: string | null }) {
  const mapDivRef = useRef<HTMLDivElement | null>(null)
  const mapRef = useRef<Map | null>(null)
  const baseLayerRef = useRef<TileLayer<TileSource> | null>(null)
  const spSourceRef = useRef<VectorSource | null>(null)
  const deviceSourceRef = useRef<VectorSource | null>(null)

  const basemaps = useBasemaps()
  const basemapCode = useBasemapStore((s) => s.basemap)

  const affected = useAffectedArea(incidentId)
  const energization = useIncidentEnergization(incidentId)

  // Set of currently-de-energized SP ids, so the affected points can be split
  // into red (off) vs amber (restored/on) without a second geometry fetch.
  const offIds = useMemo(() => {
    const ids = new Set<string>()
    for (const sp of energization.data?.service_points ?? []) ids.add(sp.id)
    return ids
  }, [energization.data])

  // The layer style closure reads the latest off-set through this ref without
  // being rebuilt (the style fn is bound once, at map construction).
  const offIdsRef = useRef(offIds)

  // One-time OL map construction.
  useEffect(() => {
    if (!mapDivRef.current || mapRef.current) return

    const baseLayer = new TileLayer<TileSource>()
    const spSource = new VectorSource()
    const deviceSource = new VectorSource()

    const spLayer = new VectorLayer({
      source: spSource,
      style: (f) => (offIdsRef.current.has(f.get("id")) ? OFF_SP_STYLE : RESTORED_SP_STYLE),
    })
    const deviceLayer = new VectorLayer({
      source: deviceSource,
      style: (f) => deviceStyleWithLabel(f.get("label") ?? ""),
    })

    const map = new Map({
      target: mapDivRef.current,
      layers: [baseLayer, spLayer, deviceLayer],
      view: new View({ center: fromLonLat([0, 0]), zoom: 2 }),
      controls: [],
    })

    mapRef.current = map
    baseLayerRef.current = baseLayer
    spSourceRef.current = spSource
    deviceSourceRef.current = deviceSource

    return () => {
      map.setTarget(undefined)
      map.dispose()
      mapRef.current = null
    }
  }, [])

  useEffect(() => {
    offIdsRef.current = offIds
    // Re-render existing features against the new off-set.
    spSourceRef.current?.changed()
  }, [offIds])

  // Apply the chosen basemap.
  useEffect(() => {
    if (!baseLayerRef.current || !basemaps.data) return
    applyBasemap(baseLayerRef.current, resolveBasemap(basemapCode, basemaps.data))
  }, [basemaps.data, basemapCode])

  // Redraw the device + service points for the selected incident, and fit once.
  useEffect(() => {
    const spSource = spSourceRef.current
    const deviceSource = deviceSourceRef.current
    const map = mapRef.current
    if (!spSource || !deviceSource || !map) return

    spSource.clear()
    deviceSource.clear()

    if (!incidentId) return

    const coords: number[][] = []

    const area = affected.data
    const points: AffectedServicePoint[] = area?.service_points ?? []
    for (const sp of points) {
      const c = fromLonLat([sp.longitude, sp.latitude])
      coords.push(c)
      const f = new Feature({ geometry: new Point(c) })
      f.set("id", sp.id)
      spSource.addFeature(f)
    }

    // The blamed device, drawn from any of its cone points' centroid is not
    // available; the affected response carries no device geometry, so we place
    // the device label at the mean of the cone when there is one.
    if (area?.device_id && coords.length > 0) {
      const mean = coords.reduce((acc, c) => [acc[0] + c[0], acc[1] + c[1]], [0, 0])
      const center: number[] = [mean[0] / coords.length, mean[1] / coords.length]
      const f = new Feature({ geometry: new Point(center) })
      f.set("label", `${area.device_class} device`)
      deviceSource.addFeature(f)
    }

    if (coords.length > 0) {
      map.getView().fit(boundingExtent(coords), {
        padding: [40, 40, 40, 40],
        maxZoom: 17,
        duration: 250,
      })
    }
  }, [incidentId, affected.data])

  const offCount = energization.data?.customers ?? 0
  const deviceOpen = energization.data?.device_state === "OPEN"

  return (
    <div className="relative overflow-hidden rounded-lg border">
      <div ref={mapDivRef} className="h-72 w-full bg-white dark:bg-neutral-950" />
      {!incidentId && (
        <div className="text-muted-foreground absolute inset-0 flex items-center justify-center text-sm">
          Select an incident to map its outage.
        </div>
      )}
      {incidentId && deviceOpen && offCount > 0 && (
        <div className="absolute left-2 top-2 inline-flex items-center gap-1 rounded bg-red-600/90 px-2 py-1 text-xs font-medium text-white shadow">
          <PowerOff className="size-3.5" />
          {offCount.toLocaleString()} off supply
        </div>
      )}
    </div>
  )
}
