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
import { useCallback, useEffect, useMemo, useRef } from "react"

import { useBasemaps } from "@/hooks/use-basemaps"
import type { Trail, TrackWaypoint } from "@/hooks/use-field-tracks"
import { applyBasemap, resolveBasemap } from "@/lib/basemaps"
import { BLANK_BASEMAP, useBasemapStore } from "@/stores/basemap-store"

// A self-contained OpenLayers map showing field-session trails (field breadcrumb
// audit trail, migration 0178): each trail's breadcrumb movement line + start/end
// markers in its own colour, and the located edit actions as clickable pins. Its
// own map instance, deliberately not the main /map — a compact supervisor audit
// panel. Several trails can be overlaid at once (the page caps the count), and the
// whole thing can be replayed over time via `playheadRealMs` (the timeline strip).

/** Distinct categorical colours for overlaid trails — matches the palette
 * coordination-panel.tsx uses for overlaying multiple device curves, so the two
 * map overlays read consistently. Index-assigned by the page. */
export const TRAIL_COLORS = [
  "#2563eb",
  "#dc2626",
  "#16a34a",
  "#d97706",
  "#7c3aed",
  "#0891b2",
  "#db2777",
  "#65a30d",
]

/** One trail to draw: its session id, the colour to draw it in, and its loaded
 * data (null while still loading). */
export interface TrailLayerInput {
  sessionId: string
  color: string
  trail: Trail | null
}

function ms(iso: string): number {
  return Date.parse(iso)
}

function trailLineStyle(color: string): Style {
  return new Style({ stroke: new Stroke({ color, width: 3 }) })
}

// start = filled dot in the trail colour; end = hollow ring; head (the live
// position during playback) = a filled dot inside a soft halo so it reads as "here
// now". All in the trail's colour so direction and liveness read at a glance.
function markerStyle(color: string, role: string): Style[] {
  if (role === "head") {
    return [
      new Style({ image: new CircleStyle({ radius: 10, fill: new Fill({ color: `${color}33` }), stroke: new Stroke({ color, width: 1 }) }) }),
      new Style({ image: new CircleStyle({ radius: 5, fill: new Fill({ color }), stroke: new Stroke({ color: "#ffffff", width: 2 }) }) }),
    ]
  }
  if (role === "end") {
    return [new Style({ image: new CircleStyle({ radius: 5, fill: new Fill({ color: "#ffffff" }), stroke: new Stroke({ color, width: 2 }) }) })]
  }
  return [new Style({ image: new CircleStyle({ radius: 6, fill: new Fill({ color }), stroke: new Stroke({ color: "#ffffff", width: 2 }) }) })]
}

// Human labels for the action-waypoint kinds (the raw op kind the field app syncs).
// Covers GIS edits and the WAM / fault captures, which are located the same way
// (field breadcrumb audit trail). Unknown kinds fall back to a readable de-snake.
const ACTION_LABELS: Record<string, string> = {
  create: "Create",
  update_attrs: "Update",
  delete: "Delete",
  establish_connection: "Connect",
  establish_relationship: "Relate",
  wam_defect_create: "Defect raised",
  wam_defect_status: "Defect status",
  wam_workorder_update: "Work order",
  wam_inspection_record: "Inspection",
  fault_create: "Fault raised",
  fault_status: "Fault status",
  fault_book_defect: "Defect booked",
}

export function kindLabel(kind: string): string {
  return ACTION_LABELS[kind] ?? kind.replace(/_/g, " ").replace("attrs", "edit")
}

// A located edit: a square pin, red for a delete, amber otherwise, labelled with
// the humanized action kind. Selected → larger pin + a highlight ring.
function waypointStyle(kind: string, selected: boolean): Style[] {
  const destructive = kind === "delete"
  const styles: Style[] = []
  if (selected) {
    styles.push(
      new Style({
        image: new CircleStyle({
          radius: 13,
          fill: new Fill({ color: "rgba(37,99,235,0.14)" }),
          stroke: new Stroke({ color: "#2563eb", width: 2 }),
        }),
      }),
    )
  }
  styles.push(
    new Style({
      image: new RegularShape({
        points: 4,
        radius: selected ? 9 : 7,
        angle: Math.PI / 4,
        fill: new Fill({ color: destructive ? "#dc2626" : "#f59e0b" }),
        stroke: new Stroke({ color: "#ffffff", width: selected ? 2 : 1.5 }),
      }),
      text: new Text({
        text: kindLabel(kind),
        offsetY: -15,
        font: `${selected ? "700" : "600"} 10px ui-sans-serif, system-ui`,
        fill: new Fill({ color: destructive ? "#b91c1c" : "#b45309" }),
        stroke: new Stroke({ color: "#ffffff", width: 3 }),
      }),
    }),
  )
  return styles
}

export function FieldTrackMap({
  trails,
  selectedOpId,
  playheadRealMs,
  onWaypointClick,
}: {
  /** The trails to overlay, each with its own colour. */
  trails: TrailLayerInput[]
  /** The op_id of the edit the detail panel is showing, highlighted on the map. */
  selectedOpId?: string | null
  /** Replay cursor (ms since epoch): only breadcrumbs/edits at or before it are
   * drawn, with a live "head" dot per trail. null = show the whole trail (static). */
  playheadRealMs?: number | null
  /** Fired when a pin is clicked — the page opens that edit's details (it does NOT
   * navigate away; "Show on main map" is the explicit opt-in for that). */
  onWaypointClick?: (w: TrackWaypoint) => void
}) {
  const mapDivRef = useRef<HTMLDivElement | null>(null)
  const mapRef = useRef<Map | null>(null)
  const baseLayerRef = useRef<TileLayer<TileSource> | null>(null)
  const trailSourceRef = useRef<VectorSource | null>(null)
  const markerSourceRef = useRef<VectorSource | null>(null)
  const waypointSourceRef = useRef<VectorSource | null>(null)
  const waypointLayerRef = useRef<VectorLayer<VectorSource> | null>(null)

  const basemaps = useBasemaps()
  const basemapCode = useBasemapStore((s) => s.basemap)
  const setBasemap = useBasemapStore((s) => s.setBasemap)

  // Latest props read through refs so the one-time map's listeners/draw see them
  // without being rebuilt.
  const trailsRef = useRef(trails)
  trailsRef.current = trails
  const playheadRef = useRef<number | null>(playheadRealMs ?? null)
  const selectedRef = useRef<string | null>(selectedOpId ?? null)
  const onClickRef = useRef(onWaypointClick)
  useEffect(() => {
    onClickRef.current = onWaypointClick
  }, [onWaypointClick])

  // Redraw every trail, revealing only up to the playhead (or all of it when the
  // playhead is null). `doFit` fits to the FULL extent (never the revealed subset),
  // so the view stays put as playback advances; it's set only on data change.
  const redraw = useCallback((doFit: boolean) => {
    const trailSource = trailSourceRef.current
    const markerSource = markerSourceRef.current
    const waypointSource = waypointSourceRef.current
    const map = mapRef.current
    if (!trailSource || !markerSource || !waypointSource || !map) return

    trailSource.clear()
    markerSource.clear()
    waypointSource.clear()

    const head = playheadRef.current
    const fitCoords: number[][] = []

    for (const { color, trail } of trailsRef.current) {
      if (!trail || trail.points.length === 0) continue
      const pts = trail.points
      const coords = pts.map((p) => fromLonLat([p.longitude, p.latitude]))
      for (const c of coords) fitCoords.push(c)

      // How many breadcrumbs are revealed at the playhead.
      let revealed = pts.length
      if (head != null) {
        revealed = 0
        while (revealed < pts.length && ms(pts[revealed].recorded_at) <= head) revealed++
      }

      if (revealed > 0) {
        const shown = coords.slice(0, revealed)
        if (shown.length >= 2) {
          const line = new Feature({ geometry: new LineString(shown) })
          line.set("color", color)
          trailSource.addFeature(line)
        }
        const start = new Feature({ geometry: new Point(coords[0]) })
        start.set("role", "start")
        start.set("color", color)
        markerSource.addFeature(start)

        const complete = head == null || revealed >= pts.length
        if (complete) {
          const end = new Feature({ geometry: new Point(coords[revealed - 1]) })
          end.set("role", "end")
          end.set("color", color)
          markerSource.addFeature(end)
        } else {
          // Interpolate the live position between the last revealed fix and the next.
          const a = coords[revealed - 1]
          const b = coords[revealed]
          const ta = ms(pts[revealed - 1].recorded_at)
          const tb = ms(pts[revealed].recorded_at)
          const f = tb > ta ? Math.max(0, Math.min(1, (head! - ta) / (tb - ta))) : 0
          const headCoord = [a[0] + (b[0] - a[0]) * f, a[1] + (b[1] - a[1]) * f]
          const h = new Feature({ geometry: new Point(headCoord) })
          h.set("role", "head")
          h.set("color", color)
          markerSource.addFeature(h)
        }
      }

      for (const w of trail.waypoints) {
        const c = fromLonLat([w.longitude, w.latitude])
        fitCoords.push(c)
        if (head != null && ms(w.recorded_at) > head) continue
        const f = new Feature({ geometry: new Point(c) })
        f.set("kind", w.kind)
        f.set("op_id", w.op_id)
        f.set("waypoint", w)
        waypointSource.addFeature(f)
      }
    }

    if (doFit && fitCoords.length > 0) {
      map.getView().fit(boundingExtent(fitCoords), { padding: [48, 48, 48, 48], maxZoom: 18, duration: 250 })
    }
  }, [])

  // One-time OL map construction.
  useEffect(() => {
    if (!mapDivRef.current || mapRef.current) return

    const baseLayer = new TileLayer<TileSource>()
    const trailSource = new VectorSource()
    const markerSource = new VectorSource()
    const waypointSource = new VectorSource()

    const trailLayer = new VectorLayer({ source: trailSource, style: (f) => trailLineStyle(f.get("color") ?? "#2563eb") })
    const markerLayer = new VectorLayer({
      source: markerSource,
      style: (f) => markerStyle(f.get("color") ?? "#2563eb", f.get("role") ?? "start"),
    })
    const waypointLayer = new VectorLayer({
      source: waypointSource,
      style: (f) => waypointStyle(f.get("kind") ?? "", f.get("op_id") === selectedRef.current),
    })

    const map = new Map({
      target: mapDivRef.current,
      layers: [baseLayer, trailLayer, markerLayer, waypointLayer],
      view: new View({ center: fromLonLat([0, 0]), zoom: 2 }),
      controls: [],
    })

    map.on("singleclick", (e) => {
      const feat = map.forEachFeatureAtPixel(e.pixel, (f) => (f.get("waypoint") ? f : undefined), { hitTolerance: 6 })
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
    waypointLayerRef.current = waypointLayer

    redraw(true)

    return () => {
      map.setTarget(undefined)
      map.dispose()
      mapRef.current = null
    }
  }, [redraw])

  // Apply the chosen basemap.
  useEffect(() => {
    if (!baseLayerRef.current || !basemaps.data) return
    applyBasemap(baseLayerRef.current, resolveBasemap(basemapCode, basemaps.data))
  }, [basemaps.data, basemapCode])

  // A stable signature so the fit-redraw fires only when the trails or their loaded
  // data actually change, not on every parent render.
  const trailsKey = useMemo(
    () => trails.map((t) => `${t.sessionId}:${t.color}:${t.trail?.points.length ?? -1}`).join("|"),
    [trails],
  )
  const hasAnyPoints = trails.some((t) => (t.trail?.points.length ?? 0) > 0)

  // Data changed → redraw and refit to the full extent.
  useEffect(() => {
    redraw(true)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [trailsKey, redraw])

  // Playhead moved → redraw the revealed subset only (no refit, no zoom jump).
  useEffect(() => {
    playheadRef.current = playheadRealMs ?? null
    redraw(false)
  }, [playheadRealMs, redraw])

  // Selection changed → restyle pins, re-measure (the detail panel toggles the map
  // width) and ease to the selected pin so map and panel stay in step.
  useEffect(() => {
    selectedRef.current = selectedOpId ?? null
    waypointLayerRef.current?.changed()
    const map = mapRef.current
    if (!map) return
    const raf = requestAnimationFrame(() => map.updateSize())
    let selected: TrackWaypoint | undefined
    if (selectedOpId) {
      for (const t of trails) {
        selected = t.trail?.waypoints.find((x) => x.op_id === selectedOpId)
        if (selected) break
      }
    }
    if (selected) map.getView().animate({ center: fromLonLat([selected.longitude, selected.latitude]), duration: 300 })
    return () => cancelAnimationFrame(raf)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedOpId, trailsKey])

  const basemapRows = (basemaps.data ?? []).filter((b) => b.is_enabled)

  return (
    <div className="relative h-full min-h-[16rem] overflow-hidden rounded-lg border">
      <div ref={mapDivRef} className="h-full w-full bg-white dark:bg-neutral-950" />

      <select
        className="bg-background/90 absolute right-2 top-2 z-10 rounded-md border px-2 py-1 text-xs shadow-sm backdrop-blur"
        value={basemapCode}
        onChange={(e) => setBasemap(e.target.value)}
        title="Background map"
      >
        {basemapRows.map((b) => (
          <option key={b.code} value={b.code}>
            {b.label}
          </option>
        ))}
        <option value={BLANK_BASEMAP}>No basemap</option>
      </select>

      {!hasAnyPoints && (
        <div className="text-muted-foreground absolute inset-0 flex items-center justify-center text-sm">
          {trails.length === 0 ? "Pick one or more sessions to map their trails." : "These sessions recorded no breadcrumbs."}
        </div>
      )}
    </div>
  )
}
