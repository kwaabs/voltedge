import { boundingExtent } from "ol/extent"
import Feature from "ol/Feature"
import LineString from "ol/geom/LineString"
import Point from "ol/geom/Point"
import TileLayer from "ol/layer/Tile"
import VectorLayer from "ol/layer/Vector"
import Map from "ol/Map"
import { fromLonLat } from "ol/proj"
import type TileSource from "ol/source/Tile"
import VectorSource from "ol/source/Vector"
import { Circle as CircleStyle, Fill, RegularShape, Stroke, Style, Text } from "ol/style"
import View from "ol/View"
import { useEffect, useMemo, useRef } from "react"

import { useBasemaps } from "@/hooks/use-basemaps"
import type { Trail, TrackWaypoint } from "@/hooks/use-field-tracks"
import { applyBasemap, resolveBasemap } from "@/lib/basemaps"
import { useBasemapStore } from "@/stores/basemap-store"

// A self-contained OpenLayers map showing ONE field session's trail (field
// breadcrumb audit trail, migration 0178): the breadcrumb movement line, start
// and end markers, and the located edit actions as clickable pins. Its own map
// instance, deliberately not the main /map — a compact supervisor audit panel.
// Mirrors the outage-map pattern (basemap + vector layers + one-shot fit).

const TRAIL_STYLE = new Style({
  stroke: new Stroke({ color: "#2563eb", width: 3 }),
})

const START_STYLE = new Style({
  image: new CircleStyle({
    radius: 6,
    fill: new Fill({ color: "#16a34a" }),
    stroke: new Stroke({ color: "#ffffff", width: 2 }),
  }),
})
const END_STYLE = new Style({
  image: new CircleStyle({
    radius: 6,
    fill: new Fill({ color: "#1d4ed8" }),
    stroke: new Stroke({ color: "#ffffff", width: 2 }),
  }),
})

// A located edit: a square pin, red for a delete, amber otherwise, labelled with
// the action kind so the supervisor sees "create / update / delete" on the map.
function waypointStyle(kind: string): Style {
  const destructive = kind === "delete"
  return new Style({
    image: new RegularShape({
      points: 4,
      radius: 7,
      angle: Math.PI / 4,
      fill: new Fill({ color: destructive ? "#dc2626" : "#f59e0b" }),
      stroke: new Stroke({ color: "#ffffff", width: 1.5 }),
    }),
    text: new Text({
      text: kind.replace(/_/g, " ").replace("attrs", "edit"),
      offsetY: -14,
      font: "600 10px ui-sans-serif, system-ui",
      fill: new Fill({ color: destructive ? "#b91c1c" : "#b45309" }),
      stroke: new Stroke({ color: "#ffffff", width: 3 }),
    }),
  })
}

export function FieldTrackMap({
  trail,
  onWaypointClick,
}: {
  trail: Trail | null
  onWaypointClick?: (w: TrackWaypoint) => void
}) {
  const mapDivRef = useRef<HTMLDivElement | null>(null)
  const mapRef = useRef<Map | null>(null)
  const baseLayerRef = useRef<TileLayer<TileSource> | null>(null)
  const trailSourceRef = useRef<VectorSource | null>(null)
  const markerSourceRef = useRef<VectorSource | null>(null)
  const waypointSourceRef = useRef<VectorSource | null>(null)

  const basemaps = useBasemaps()
  const basemapCode = useBasemapStore((s) => s.basemap)

  // The waypoint-click handler is read through a ref so the one-time map's click
  // listener always calls the latest callback without being rebuilt.
  const onClickRef = useRef(onWaypointClick)
  useEffect(() => {
    onClickRef.current = onWaypointClick
  }, [onWaypointClick])

  // One-time OL map construction.
  useEffect(() => {
    if (!mapDivRef.current || mapRef.current) return

    const baseLayer = new TileLayer<TileSource>()
    const trailSource = new VectorSource()
    const markerSource = new VectorSource()
    const waypointSource = new VectorSource()

    const trailLayer = new VectorLayer({ source: trailSource, style: TRAIL_STYLE })
    const markerLayer = new VectorLayer({
      source: markerSource,
      style: (f) => (f.get("role") === "start" ? START_STYLE : END_STYLE),
    })
    const waypointLayer = new VectorLayer({
      source: waypointSource,
      style: (f) => waypointStyle(f.get("kind") ?? ""),
    })

    const map = new Map({
      target: mapDivRef.current,
      layers: [baseLayer, trailLayer, markerLayer, waypointLayer],
      view: new View({ center: fromLonLat([0, 0]), zoom: 2 }),
      controls: [],
    })

    // Click a waypoint pin → surface the located edit (the page links it to the
    // feature's history).
    map.on("singleclick", (e) => {
      const feat = map.forEachFeatureAtPixel(e.pixel, (f) => (f.get("waypoint") ? f : undefined), {
        hitTolerance: 6,
      })
      const w = feat?.get("waypoint") as TrackWaypoint | undefined
      if (w) onClickRef.current?.(w)
    })
    map.getTargetElement().style.cursor = "default"
    map.on("pointermove", (e) => {
      if (e.dragging) return
      const hit = map.hasFeatureAtPixel(e.pixel, { hitTolerance: 6 })
      map.getTargetElement().style.cursor = hit ? "pointer" : "default"
    })

    mapRef.current = map
    baseLayerRef.current = baseLayer
    trailSourceRef.current = trailSource
    markerSourceRef.current = markerSource
    waypointSourceRef.current = waypointSource

    return () => {
      map.setTarget(undefined)
      map.dispose()
      mapRef.current = null
    }
  }, [])

  // Apply the chosen basemap.
  useEffect(() => {
    if (!baseLayerRef.current || !basemaps.data) return
    applyBasemap(baseLayerRef.current, resolveBasemap(basemapCode, basemaps.data))
  }, [basemaps.data, basemapCode])

  const points = useMemo(() => trail?.points ?? [], [trail])
  const waypoints = useMemo(() => trail?.waypoints ?? [], [trail])

  // Redraw the trail line, start/end markers and waypoint pins, and fit once.
  useEffect(() => {
    const trailSource = trailSourceRef.current
    const markerSource = markerSourceRef.current
    const waypointSource = waypointSourceRef.current
    const map = mapRef.current
    if (!trailSource || !markerSource || !waypointSource || !map) return

    trailSource.clear()
    markerSource.clear()
    waypointSource.clear()

    const coords = points.map((p) => fromLonLat([p.longitude, p.latitude]))

    if (coords.length >= 2) {
      trailSource.addFeature(new Feature({ geometry: new LineString(coords) }))
    }
    if (coords.length >= 1) {
      const start = new Feature({ geometry: new Point(coords[0]) })
      start.set("role", "start")
      markerSource.addFeature(start)
      const end = new Feature({ geometry: new Point(coords[coords.length - 1]) })
      end.set("role", "end")
      markerSource.addFeature(end)
    }

    const allCoords = [...coords]
    for (const w of waypoints) {
      const c = fromLonLat([w.longitude, w.latitude])
      allCoords.push(c)
      const f = new Feature({ geometry: new Point(c) })
      f.set("kind", w.kind)
      f.set("waypoint", w)
      waypointSource.addFeature(f)
    }

    if (allCoords.length > 0) {
      map.getView().fit(boundingExtent(allCoords), {
        padding: [48, 48, 48, 48],
        maxZoom: 18,
        duration: 250,
      })
    }
  }, [points, waypoints])

  return (
    <div className="relative overflow-hidden rounded-lg border">
      <div ref={mapDivRef} className="h-[28rem] w-full bg-white dark:bg-neutral-950" />
      {(!trail || points.length === 0) && (
        <div className="text-muted-foreground absolute inset-0 flex items-center justify-center text-sm">
          {trail ? "This session recorded no breadcrumbs." : "Select a session to map its trail."}
        </div>
      )}
    </div>
  )
}
