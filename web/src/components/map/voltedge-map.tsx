import Collection from "ol/Collection"
import type { Coordinate } from "ol/coordinate"
import { defaults as defaultControls } from "ol/control"
import type { EventsKey } from "ol/events"
import { isEmpty as isEmptyExtent } from "ol/extent"
import Feature from "ol/Feature"
import GeoJSON from "ol/format/GeoJSON"
import MVT from "ol/format/MVT"
import type Geometry from "ol/geom/Geometry"
import LineString from "ol/geom/LineString"
import MultiPolygon from "ol/geom/MultiPolygon"
import Point from "ol/geom/Point"
import Polygon from "ol/geom/Polygon"
import Draw, { createBox, type GeometryFunction } from "ol/interaction/Draw"
import Modify from "ol/interaction/Modify"
import PointerInteraction from "ol/interaction/Pointer"
import Snap from "ol/interaction/Snap"
import Translate from "ol/interaction/Translate"
import type Layer from "ol/layer/Layer"
import TileLayer from "ol/layer/Tile"
import VectorLayer from "ol/layer/Vector"
import VectorTileLayer from "ol/layer/VectorTile"
import Map from "ol/Map"
import type MapBrowserEvent from "ol/MapBrowserEvent"
import { unByKey } from "ol/Observable"
import Overlay from "ol/Overlay"
import { fromLonLat, toLonLat, transformExtent } from "ol/proj"
import { toFeature } from "ol/render/Feature"
import type TileSource from "ol/source/Tile"
import VectorSource from "ol/source/Vector"
import VectorTileSource from "ol/source/VectorTile"
import { getArea, getLength } from "ol/sphere"
import { Circle as CircleStyle, Fill, Stroke, Style } from "ol/style"
import TileState from "ol/TileState"
import View from "ol/View"
import { Loader2 } from "lucide-react"
import { useEffect, useRef } from "react"

import { useAllDisplayZooms, useAllLabelDefs, useAllSymbology } from "@/hooks/use-all-symbology"
import { usePermission } from "@/hooks/use-auth"
import { useCatalog } from "@/hooks/use-catalog"
import { type FeatureGeometry, useFeature } from "@/hooks/use-feature"
import type { AffectedArea } from "@/hooks/use-oms"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useBasemaps } from "@/hooks/use-basemaps"
import { useCreateAnnotation, useMapAnnotations } from "@/hooks/use-map-annotations"
import { useMapSettings } from "@/hooks/use-map-settings"
import { useTerritoryBoundaries } from "@/hooks/use-territories"
import { useSymbologyIcons } from "@/hooks/use-symbology-icons"
import { apiFetch } from "@/lib/api"
import { applyBasemap, resolveBasemap } from "@/lib/basemaps"
import { splitCoordinates } from "@/lib/geometry"
import {
  type AnnotationKind,
  annotationStyleForFeature,
  createSymbologyStyle,
  draftStyleForFeature,
  editStyleForFeature,
  type HighlightRole,
  highlightStyleForFeature,
  importedStyleForFeature,
  pinStyleForFeature,
  territoryStyleForFeature,
} from "@/lib/map-layers"
import { featureClassSourceIds, tileUrlTemplate } from "@/lib/martin"
import { formatArea, formatLength } from "@/lib/measure"
import { useAnnotationEditStore } from "@/stores/annotation-edit-store"
import { BLANK_BASEMAP, useBasemapStore } from "@/stores/basemap-store"
import { useBoxZoomStore } from "@/stores/box-zoom-store"
import { useConnectStore } from "@/stores/connect-store"
import { useDeletedFeaturesStore } from "@/stores/deleted-features-store"
import { useDiagramPlaceStore } from "@/stores/diagram-place-store"
import { type DrawGeometryType, useDrawStore } from "@/stores/draw-store"
import { useEditStore } from "@/stores/edit-store"
import { useFaultCurrentStore } from "@/stores/fault-current-store"
import { useImportedFeatureStore } from "@/stores/imported-feature-store"
import { useImportedOverlaysStore } from "@/stores/imported-overlays-store"
import { useIsolateStore } from "@/stores/isolate-store"
import { useLayersStore } from "@/stores/layers-store"
import { useLoadFlowStore } from "@/stores/load-flow-store"
import { useMapAnnotationsStore } from "@/stores/map-annotations-store"
import { useMeshedLoadFlowStore } from "@/stores/meshed-load-flow-store"
import { useNodalLoadFlowStore } from "@/stores/nodal-load-flow-store"
import { activeInner, useAutoLoadFlowStore } from "@/stores/auto-load-flow-store"
import { useMapGotoStore } from "@/stores/map-goto-store"
import { useMapPinsStore } from "@/stores/map-pins-store"
import { useMapPointerStore } from "@/stores/map-pointer-store"
import { useMapRefreshStore } from "@/stores/map-refresh-store"
import { type Bbox4326, useMapViewStore } from "@/stores/map-view-store"
import { useMeasureStore } from "@/stores/measure-store"
import { useOmsHighlightStore } from "@/stores/oms-highlight-store"
import { useOrthoStore } from "@/stores/ortho-store"
import { useQueryStore } from "@/stores/query-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useSnapStore } from "@/stores/snap-store"
import { type TerritoryGeometry, useTerritoryEditStore } from "@/stores/territory-edit-store"
import { useTerritoryOverlayStore } from "@/stores/territory-overlay-store"
import { useToastStore } from "@/stores/toast-store"
import { useTraceStore } from "@/stores/trace-store"
import { useUnitsStore } from "@/stores/units-store"

import "ol/ol.css"

const OL_DRAW_TYPE: Record<DrawGeometryType, "Point" | "LineString" | "Polygon"> = {
  point: "Point",
  line: "LineString",
  polygon: "Polygon",
}

// The OL Draw geometry type for each annotation kind (Phase 36 Slice 4) -- the
// client-side mirror of the server's kind->geom rule (mapannotation/sql.go):
// a text note is a Point, a box a Polygon, a line/arrow a LineString.
const OL_ANNOTATION_DRAW_TYPE: Record<AnnotationKind, "Point" | "LineString" | "Polygon"> = {
  text: "Point",
  box: "Polygon",
  line: "LineString",
  arrow: "LineString",
}

/**
 * Converts a just-drawn OL geometry (view projection, EPSG:3857) into
 * the EPSG:4326 GeoJSON `POST /features/{code}` expects -- point, line,
 * and polygon each shape their coordinates differently (a bare pair, a
 * list of pairs, a list of rings of pairs), so this branches on the
 * concrete geometry class rather than assuming Draw always hands back a
 * Point the way the original point-only version of this file did.
 */
function geometryToGeoJSON(geometry: Geometry): FeatureGeometry | null {
  if (geometry instanceof Point) {
    return { type: "Point", coordinates: toLonLat(geometry.getCoordinates()) }
  }
  if (geometry instanceof LineString) {
    return { type: "LineString", coordinates: geometry.getCoordinates().map((c) => toLonLat(c)) }
  }
  if (geometry instanceof Polygon) {
    return {
      type: "Polygon",
      coordinates: geometry.getCoordinates().map((ring) => ring.map((c) => toLonLat(c))),
    }
  }
  return null
}

/**
 * The inverse of geometryToGeoJSON above -- turns a fetched feature's
 * real GeoJSON geometry (EPSG:4326, from `GET /features/{code}/{id}`)
 * into an OL geometry in view projection (EPSG:3857), so the highlight
 * layer (see the effect below) can draw a halo around whichever feature
 * is currently selected or, mid connect/relate workflow, its source and
 * target. Same three-geometry-type support as the forward direction,
 * for the same reason: nothing this app creates or highlights is ever a
 * Multi* geometry.
 */
function geoJSONToGeometry(geometry: FeatureGeometry): Geometry | null {
  if (geometry.type === "Point") {
    return new Point(fromLonLat(geometry.coordinates as [number, number]))
  }
  if (geometry.type === "LineString") {
    return new LineString((geometry.coordinates as [number, number][]).map((c) => fromLonLat(c)))
  }
  if (geometry.type === "Polygon") {
    return new Polygon(
      (geometry.coordinates as [number, number][][]).map((ring) => ring.map((c) => fromLonLat(c))),
    )
  }
  return null
}

/**
 * Snaps the last (moving) vertex of an in-progress sketch to the nearest
 * 45° direction off the previous fixed vertex, keeping the cursor's
 * distance along that snapped bearing -- the orthogonal-drawing
 * constraint. Only the moving point is adjusted; every earlier vertex
 * stays exactly where it was placed (it was itself snapped at placement
 * time). Planar math in the view projection (EPSG:3857), which is fine
 * at the segment scale a user actually digitizes at.
 */
function snapLastOrtho(coords: Coordinate[]): Coordinate[] {
  if (coords.length < 2) return coords
  const out = coords.slice()
  const prev = out[out.length - 2]
  const last = out[out.length - 1]
  const dx = last[0] - prev[0]
  const dy = last[1] - prev[1]
  const dist = Math.hypot(dx, dy)
  if (dist === 0) return out
  const step = Math.PI / 4
  const snapped = Math.round(Math.atan2(dy, dx) / step) * step
  out[out.length - 1] = [prev[0] + Math.cos(snapped) * dist, prev[1] + Math.sin(snapped) * dist]
  return out
}

// Draw geometryFunctions that replicate OL's own defaults for
// LineString/Polygon exactly, but ortho-snap the moving vertex first
// when ortho-store says so. Read the flag live (getState) rather than
// closing over it, so toggling Ortho mid-draw takes effect on the next
// pointer move without tearing down and rebuilding the Draw interaction
// (which would drop the in-progress sketch).
const orthoLineGeometryFunction: GeometryFunction = (coordinates, geometry) => {
  const coords = useOrthoStore.getState().enabled
    ? snapLastOrtho(coordinates as Coordinate[])
    : (coordinates as Coordinate[])
  if (geometry) {
    ;(geometry as LineString).setCoordinates(coords)
    return geometry
  }
  return new LineString(coords)
}

const orthoPolygonGeometryFunction: GeometryFunction = (coordinates, geometry) => {
  const rings = coordinates as Coordinate[][]
  const ring = useOrthoStore.getState().enabled ? snapLastOrtho(rings[0]) : rings[0]
  if (geometry) {
    // Same closing-vertex handling OL's default Polygon geometryFunction
    // does: append the first point to close the ring, or an empty ring
    // while there's nothing placed yet.
    ;(geometry as Polygon).setCoordinates(ring.length ? [ring.concat([ring[0]])] : [])
    return geometry
  }
  return new Polygon([ring])
}

/**
 * Style for the ephemeral measure-tool sketch layer -- a dashed copper
 * line/outline with a translucent fill and small vertex dots, matching
 * the app's copper accent so it reads as a tool overlay distinct from
 * real feature symbology. Not tied to any feature class; the measure
 * layer is scratch geometry, discarded when the tool closes.
 */
const MEASURE_STYLE = new Style({
  fill: new Fill({ color: "rgba(194, 86, 15, 0.12)" }),
  stroke: new Stroke({ color: "#c2560f", width: 2, lineDash: [6, 5] }),
  image: new CircleStyle({ radius: 4, fill: new Fill({ color: "#c2560f" }) }),
})

/**
 * Style for the box-zoom tool's rubber-band rectangle (see box-zoom-store.ts) --
 * a translucent sky-blue fill with a dashed sky stroke, distinct from the copper
 * measure sketch so the two tools don't read alike. Scratch geometry, discarded
 * the moment the box is committed and the view fits to it.
 */
const BOX_ZOOM_STYLE = new Style({
  fill: new Fill({ color: "rgba(2, 132, 199, 0.12)" }),
  stroke: new Stroke({ color: "#0284c7", width: 2, lineDash: [6, 5] }),
})

/**
 * Scratch styles for the Split tool's live preview (Phase 20). While the user
 * hovers/clicks a line in split mode, the two halves are drawn over it so the
 * cut is visible before Save: the near half (stays on the original) solid blue,
 * the far half (becomes the new feature) dashed green, with a marker dot at the
 * cut point. Purely cosmetic -- PostGIS does the authoritative split on Save.
 */
const SPLIT_NEAR_STYLE = new Style({
  stroke: new Stroke({ color: "#2563eb", width: 3 }),
})
const SPLIT_FAR_STYLE = new Style({
  stroke: new Stroke({ color: "#16a34a", width: 3, lineDash: [8, 6] }),
})
const SPLIT_POINT_STYLE = new Style({
  image: new CircleStyle({
    radius: 5,
    fill: new Fill({ color: "#ffffff" }),
    stroke: new Stroke({ color: "#16a34a", width: 2 }),
  }),
})

/**
 * Rebuilds the snap-candidate source (snapSourceRef) from whichever
 * vector tiles are already loaded for the current view -- OL's Snap
 * interaction only accepts a plain VectorSource (see its Options
 * typedef), never a VectorTileSource, so there's no source it could
 * point at directly among the real, tile-served feature layers.
 *
 * Mirrors the exact tile-reading pattern OL's own canvas vector-tile
 * renderer uses for hit-detection (ol/renderer/canvas/VectorTileLayer.js's
 * `getFeatures`), confirmed by reading that source directly rather than
 * guessing: `source.getTile(z, x, y, pixelRatio, projection)` returns a
 * lightweight VectorRenderTile wrapper (cheap, not itself cached); its
 * `getSourceTiles()` looks up the *source's own* shared cache of raw MVT
 * tiles keyed by URL -- already populated and LOADED for whatever this
 * same source's real, rendered VectorTileLayer already fetched to draw
 * the current view, so this is a synchronous, no-network-request read
 * in the common case, not a fresh fetch.
 *
 * Each raw tile's already-parsed features are `ol/render/Feature`'s
 * lightweight RenderFeature (this app's `new MVT()` never sets a
 * featureClass), which has no usable geometry for Snap -- `toFeature`
 * (from the same module) is OL's own conversion to a real `ol/Feature`
 * with a real `ol/geom/*` geometry, already in the view's projection
 * (VectorTileSource transforms tile-pixel coordinates into it while
 * parsing, before a tile's features are ever exposed).
 *
 * Rebuilds from scratch every call rather than diffing -- only ever
 * called on moveend or when a snapping session starts (see the effects
 * below), not per frame, so this is cheap enough not to need to be
 * incremental.
 */
function refreshSnapSource(map: Map, layers: VectorTileLayer[], snapSource: VectorSource) {
  const view = map.getView()
  const resolution = view.getResolution()
  const size = map.getSize()
  if (!resolution || !size) return
  const projection = view.getProjection()
  const extent = view.calculateExtent(size)

  const features: Feature[] = []
  for (const layer of layers) {
    const source = layer.getSource()
    if (!source) continue
    const tileGrid = source.getTileGridForProjection(projection)
    const z = tileGrid.getZForResolution(resolution)
    tileGrid.forEachTileCoord(extent, z, (tileCoord) => {
      const [tz, tx, ty] = tileCoord
      const renderTile = source.getTile(tz, tx, ty, 1, projection)
      for (const sourceTile of renderTile.getSourceTiles()) {
        if (sourceTile.getState() !== TileState.LOADED) continue
        for (const renderFeature of sourceTile.getFeatures()) {
          features.push(toFeature(renderFeature))
        }
      }
    })
  }
  snapSource.clear()
  snapSource.addFeatures(features)
}

/**
 * Placeholder default view -- centered on the seed data used to
 * validate this slice (seed/pole_example.sql, seed/connectivity_example.sql),
 * not a real service-territory extent. Nothing in the metadata model
 * defines a default extent yet; revisit once/if that becomes a real
 * concept (e.g. derived from compiled feature classes' actual bounds).
 */
const DEFAULT_CENTER = fromLonLat([-0.127, 51.507])
const DEFAULT_ZOOM = 15

/** Only ever hit-test the real feature layers, never the OSM base tile layer. */
const isVectorLayer = (layer: Layer) => layer instanceof VectorTileLayer

/**
 * initialCenter/initialZoom seed the map's starting View from the
 * admin-configured default (map.tsx fetches it via useMapSettings and mounts
 * this only once the setting has settled). Both optional -- absent falls back
 * to the DEFAULT_CENTER/DEFAULT_ZOOM placeholder below. Captured into refs at
 * mount (the View is built once), so a later settings change never yanks a live
 * map's viewport.
 */
interface VoltEdgeMapProps {
  /** [lon, lat] in EPSG:4326 -- the same order fromLonLat takes. */
  initialCenter?: [number, number]
  initialZoom?: number
}

export function VoltEdgeMap({ initialCenter, initialZoom }: VoltEdgeMapProps = {}) {
  const containerRef = useRef<HTMLDivElement>(null)
  const mapRef = useRef<Map | null>(null)
  // The initial View, resolved once from props (or the placeholder). Refs, not
  // reactive state: the map is built a single time in the init effect and must
  // not rebuild when the settings query later refetches.
  const initialCenterRef = useRef(initialCenter ? fromLonLat(initialCenter) : DEFAULT_CENTER)
  const initialZoomRef = useRef(initialZoom ?? DEFAULT_ZOOM)
  // The single base tile layer -- kept in a ref so the basemap-change
  // effect below can swap its source in place (see lib/basemaps.ts's
  // applyBasemap) without rebuilding the map or disturbing the z-order.
  const baseLayerRef = useRef<TileLayer<TileSource> | null>(null)
  const vectorLayersRef = useRef<VectorTileLayer[]>([])
  // The territory boundary overlay (Phase 11 Slice H) -- one client-side
  // VectorLayer/VectorSource pair (not a Martin tile layer: territories
  // are few and their boundaries are large MultiPolygons, so a single
  // GeoJSON fetch beats tiling), kept in refs so the boundary-load effect
  // below can repopulate/toggle it without rebuilding the map. Rendered
  // below the feature tiles (see the init effect) so it reads as a
  // backdrop, never covering the features it contains.
  const territorySourceRef = useRef<VectorSource | null>(null)
  const territoryLayerRef = useRef<VectorLayer | null>(null)
  // The imported-overlay layer (Phase 15 data import) -- one client-side
  // VectorLayer/VectorSource pair holding every visible imported overlay's
  // features (amber/dashed, see importedStyleForFeature), kept in refs so the
  // population effect below can repopulate/toggle it as overlays are
  // added/removed/hidden without rebuilding the map. Read-only reference data,
  // never written to the DB.
  const importedSourceRef = useRef<VectorSource | null>(null)
  const importedLayerRef = useRef<VectorLayer | null>(null)
  // The map-annotation (redlining) overlay (Phase 36) -- one client-side
  // VectorLayer/VectorSource pair holding every annotation, each rendered from
  // its own per-instance style (see annotationStyleForFeature), kept in refs so
  // the load effect below can repopulate/toggle it without rebuilding the map.
  // A geographic sibling of the diagram furniture; read-only in this slice, its
  // create/edit UX lands in Slice 4.
  const annotationSourceRef = useRef<VectorSource | null>(null)
  const annotationLayerRef = useRef<VectorLayer | null>(null)
  // The user's saved-pin (bookmark) layer -- one client-side VectorLayer of
  // teardrop markers, kept above everything else so a personal navigation pin
  // is never hidden behind a feature. Per-browser, not network data (see
  // map-pins-store.ts). Same clear-and-repopulate pattern; the pins effect
  // below owns its contents.
  const pinsSourceRef = useRef<VectorSource | null>(null)
  // The single OL Feature holding the MultiPolygon boundary currently
  // under authoring (Phase 11 Slice I) -- its own source/layer, kept
  // above the overlay so the in-progress boundary reads distinctly. The
  // Draw tool appends each finished polygon to this one feature's
  // geometry (a MultiPolygon), and Modify reshapes its rings in place;
  // both mutate it and write the result back to territory-edit-store.
  // Seeded once per session (territoryEditSeededForRef guards against a
  // boundaries refetch re-stomping mid-edit work, same lesson as the
  // feature edit seeding).
  const territoryEditSourceRef = useRef<VectorSource | null>(null)
  const territoryEditFeatureRef = useRef<Feature | null>(null)
  const territoryEditSeededForRef = useRef<string | null>(null)
  const draftSourceRef = useRef<VectorSource | null>(null)
  const highlightSourceRef = useRef<VectorSource | null>(null)
  const editSourceRef = useRef<VectorSource | null>(null)
  // A trace result's reached/barrier features, highlighted with their
  // own two HighlightRole colors -- kept as a dedicated source/layer
  // rather than folded into highlightSourceRef, since the trace result
  // is fetched asynchronously (see the effect below) while the
  // selected/connect halo effect clears and rebuilds highlightSource
  // synchronously on its own, unrelated render -- sharing one source
  // between an async effect and a sync one would mean whichever last
  // happened to run on a given commit silently wins, clobbering the
  // other's features.
  const traceSourceRef = useRef<VectorSource | null>(null)
  // Isolation results (isolate-store.ts) -- same reasoning as
  // traceSourceRef's own comment: fetched asynchronously, so it needs
  // its own source rather than sharing traceSource's, even though the
  // two workflows are mutually exclusive in the UI (running one clears
  // the other's store) -- a shared source would leave the two effects'
  // clear/populate timing racing each other across that transition.
  const isolateSourceRef = useRef<VectorSource | null>(null)
  // Load-flow results (load-flow-store.ts) -- same reasoning as
  // traceSourceRef/isolateSourceRef's own comments: fetched
  // asynchronously, own source, mutually exclusive with trace/isolate
  // in the UI so the three never race each other's clear/populate.
  const loadFlowSourceRef = useRef<VectorSource | null>(null)
  // Fault-current results (fault-current-store.ts) -- same reasoning
  // again: own async-fetched source, mutually exclusive with the other
  // three analysis workflows so none race each other's clear/populate.
  const faultCurrentSourceRef = useRef<VectorSource | null>(null)
  // Query-builder matches (query-store.ts, docs/phase-13-query-builder.md)
  // -- its own dedicated source, same reasoning as the analysis sources
  // above. Unlike them it's populated synchronously (the query response
  // already carries each match's geometry, so there's no per-feature
  // re-fetch), but it still gets its own source so it never clobbers, or
  // is clobbered by, the sync selected/connect halo on highlightSource.
  const queryResultSourceRef = useRef<VectorSource | null>(null)
  // Affected service points of a selected OMS incident (oms-highlight-store.ts,
  // Phase 49 map overlay) -- its own async-fetched source, same reasoning as
  // the analysis sources above. The /oms/incidents/{id}/affected response
  // already carries each SP's lon/lat, so there's no per-feature re-fetch, but
  // it still gets its own source so it never clobbers, or is clobbered by, the
  // sync selected/connect halo or the other analysis layers.
  const affectedSourceRef = useRef<VectorSource | null>(null)
  // Snap candidates -- never added to the map as a rendered layer, just
  // a plain VectorSource for the Snap interaction below to read (see
  // refreshSnapSource for why this indirection is needed at all: every
  // real feature lives in a VectorTileSource, which Snap can't consume
  // directly).
  const snapSourceRef = useRef<VectorSource | null>(null)
  // The single OL Feature representing the geometry currently under
  // edit -- created once per edit session (see the seeding effect
  // below) and mutated in place by whichever of Modify/Translate is
  // attached, so it keeps whatever progress the user's made across
  // switching between those two tools mid-session.
  const editFeatureRef = useRef<Feature | null>(null)
  // The "code:id" the edit-geometry source was last seeded for -- lets
  // the seeding effect tell "a brand new edit session started" (reseed
  // from the real feature) apart from "this session's data just
  // refetched for an unrelated reason" (don't stomp the user's
  // in-progress drag).
  const editSeededForRef = useRef<string | null>(null)
  // The original line's coordinates (EPSG:3857), captured when the Split tool
  // (edit-store.ts's "split") is active, so the split-preview effect can keep
  // drawing the two halves after edit mode ends and editFeatureRef is cleared
  // -- while the create form for the new part is still open. The line doesn't
  // change between entering split mode and Save, so a one-time capture is safe.
  const splitPreviewCoordsRef = useRef<number[][] | null>(null)
  // The last focusToken the pan-to-feature effect below already handled
  // -- see that effect for why it needs this instead of just depending
  // on focusToken alone.
  const fittedTokenRef = useRef(0)
  // The last OMS-highlight focusToken the affected-area effect already fit the
  // view to -- same one-shot idiom as fittedTokenRef, so a "Show on map" fits
  // once and a manual pan afterward isn't yanked back on a later re-render.
  const affectedFittedTokenRef = useRef(0)
  // Finalized measure-tool labels (the frozen tooltip Overlays left
  // behind by each completed measurement), kept in a ref so the
  // units-change effect below can re-format them all in place without
  // the measure effect (which owns their creation) having to re-run.
  // Each entry keeps the raw meters + kind so a metric/imperial flip is
  // a pure re-format, not a re-measure.
  const measureOverlaysRef = useRef<
    { overlay: Overlay; el: HTMLElement; meters: number; kind: "length" | "area" }[]
  >([])
  const { data: catalog } = useCatalog()
  const { data: featureClasses } = useFeatureClasses()
  const symbologyByClassCode = useAllSymbology()
  const labelDefsByClassCode = useAllLabelDefs()
  const displayZoomByClassCode = useAllDisplayZooms()
  const symbologyIcons = useSymbologyIcons()
  const drawingFeatureClassCode = useDrawStore((s) => s.drawingFeatureClassCode)
  const drawingGeometryType = useDrawStore((s) => s.drawingGeometryType)
  const draft = useDrawStore((s) => s.draft)
  const connectMode = useConnectStore((s) => s.mode)
  const connectSource = useConnectStore((s) => s.source)
  const connectTarget = useConnectStore((s) => s.target)
  // Phase 12: while a diagram's "Add from map" pick is armed, the map is
  // the picker -- see the click handler's own diagram-place block and
  // the Escape effect below.
  const diagramPlacePhase = useDiagramPlaceStore((s) => s.phase)
  const editing = useEditStore((s) => s.editing)
  const geometryTool = useEditStore((s) => s.geometryTool)
  const deletedIds = useDeletedFeaturesStore((s) => s.ids)
  const snapEnabled = useSnapStore((s) => s.enabled)
  const basemap = useBasemapStore((s) => s.basemap)
  // The admin-managed basemap catalog (hooks/use-basemaps.ts). The store only
  // remembers which code is chosen; the actual tile source is resolved against
  // this fetched list (an unknown/deleted code falls back to the first enabled
  // basemap -- see resolveBasemap). undefined while the query is still loading.
  const { data: basemaps } = useBasemaps()
  const traceResult = useTraceStore((s) => s.result)
  const isolateResult = useIsolateStore((s) => s.result)
  const loadFlowResult = useLoadFlowStore((s) => s.result)
  const meshedLoadFlowResult = useMeshedLoadFlowStore((s) => s.result)
  const nodalLoadFlowResult = useNodalLoadFlowStore((s) => s.result)
  const autoLoadFlowResult = useAutoLoadFlowStore((s) => s.result)
  const faultCurrentResult = useFaultCurrentStore((s) => s.result)
  const queryResults = useQueryStore((s) => s.results)
  const omsIncidentId = useOmsHighlightStore((s) => s.incidentId)
  const omsFocusToken = useOmsHighlightStore((s) => s.focusToken)
  const refreshVersion = useMapRefreshStore((s) => s.version)
  const hiddenLayers = useLayersStore((s) => s.hidden)
  const labelsHidden = useLayersStore((s) => s.labelsHidden)
  const selected = useSelectionStore((s) => s.selected)
  const focusToken = useSelectionStore((s) => s.focusToken)
  const measureMode = useMeasureStore((s) => s.mode)
  const boxZoomActive = useBoxZoomStore((s) => s.active)
  const units = useUnitsStore((s) => s.units)
  const territoryOverlayVisible = useTerritoryOverlayStore((s) => s.visible)
  const territoryHiddenLevels = useTerritoryOverlayStore((s) => s.hiddenLevels)
  const importedOverlays = useImportedOverlaysStore((s) => s.overlays)
  const territoryEditing = useTerritoryEditStore((s) => s.editing)
  const territoryEditTool = useTerritoryEditStore((s) => s.tool)
  const territoryEditCleared = useTerritoryEditStore((s) => s.cleared)
  // Fetched while the overlay is on OR a boundary is being authored -- the
  // authoring seed reads the territory's current boundary from this same
  // collection (see the seed effect) rather than a second single-territory
  // fetch.
  const { data: territoryBoundaries } = useTerritoryBoundaries(
    territoryOverlayVisible || Boolean(territoryEditing),
  )

  // The map-annotation (redlining) overlay (Phase 36) -- its visibility toggle
  // and the annotations it draws. The fetch is gated on the same toggle, so an
  // off overlay costs nothing.
  const annotationsVisible = useMapAnnotationsStore((s) => s.visible)
  const { data: annotationsData } = useMapAnnotations(annotationsVisible)

  // The global annotation visible-zoom band (admin Map settings) and the live
  // zoom, both read reactively so the annotation overlay hides/shows as the user
  // zooms across the band or an admin retunes it. useMapSettings dedupes the
  // query map.tsx already issued for the initial view; the band is a rarely-set
  // pair of optional bounds, null = no bound on that side.
  const { data: mapSettings } = useMapSettings()
  const annMinZoom = mapSettings?.annotation_min_zoom ?? null
  const annMaxZoom = mapSettings?.annotation_max_zoom ?? null
  const currentZoom = useMapViewStore((s) => s.zoom)

  // Annotation authoring (Phase 36 Slice 4): the kind currently being drawn,
  // the create mutation the Draw effect fires on drawend, and whether this
  // caller may author at all -- a viewer without annotations:write never gets
  // the draw interaction or the click-to-select edit path, matching the
  // server's method-aware gate.
  const annotationDrawingKind = useAnnotationEditStore((s) => s.drawingKind)
  const annotationSelectedId = useAnnotationEditStore((s) => s.selectedId)
  const annotationTool = useAnnotationEditStore((s) => s.tool)
  // `mutate` is referentially stable across renders (React Query memoises it),
  // so the Draw effect can depend on it without tearing down its interaction.
  const { mutate: createAnnotation } = useCreateAnnotation()
  const canAnnotate = usePermission()("annotations:write")

  // Whichever feature(s) the highlight layer needs a real geometry for
  // -- the plain-selected feature normally, or the connect/relate
  // workflow's source and target while one is active (see connect-store.ts).
  // TanStack Query dedupes these against the same queries feature-panel.tsx
  // and connect-confirm-panel.tsx already run for the same ids, so this
  // is a cache hit in the common case, not a second real fetch.
  const { data: selectedFeature, isLoading: selectedFeatureLoading } = useFeature(
    selected?.featureClassCode,
    selected?.featureId,
  )
  const { data: connectSourceFeature, isLoading: connectSourceLoading } = useFeature(
    connectSource?.featureClassCode,
    connectSource?.featureId,
  )
  const { data: connectTargetFeature, isLoading: connectTargetLoading } = useFeature(
    connectTarget?.featureClassCode,
    connectTarget?.featureId,
  )
  // The feature currently under geometry edit -- same dedup reasoning
  // as the three above (feature-edit-form.tsx already runs this exact
  // query).
  const { data: editingFeature } = useFeature(editing?.featureClassCode, editing?.featureId)
  // Lets the layer-creation effect below read the *current* hidden set
  // without depending on it -- depending on it directly would rebuild
  // every VectorTileLayer (and re-request every tile) on each single
  // checkbox toggle, when only that one layer's visible flag actually
  // needs to change (the second effect below already does that in
  // place). Kept in sync on every render, not inside an effect --
  // assigning a ref during render is fine, it's only reads during
  // render that render fresh data is not safe for.
  const hiddenLayersRef = useRef(hiddenLayers)
  hiddenLayersRef.current = hiddenLayers
  // Same ref trick for the per-layer label suppression, for the same reason:
  // the creation effect reads the current set to seed each layer's style
  // without depending on it (a label toggle only needs a setStyle in place,
  // the dedicated effect below), so toggling labels never rebuilds layers.
  const labelsHiddenRef = useRef(labelsHidden)
  labelsHiddenRef.current = labelsHidden

  // Base map + OL Map instance -- created once. attribution: false drops
  // the default Attribution control -- src/components/map/status-bar.tsx
  // renders the active basemap's required attribution text itself
  // instead, so it doesn't need to also float over the map in its own
  // corner. The base layer is created empty and hidden; the basemap-change
  // effect below applies the real source once the catalog (use-basemaps)
  // has loaded and whenever the user picks a different basemap afterward --
  // the source now depends on server data, not a synchronous store value.
  useEffect(() => {
    if (!containerRef.current) return

    const baseLayer = new TileLayer<TileSource>({ visible: false })
    baseLayerRef.current = baseLayer

    const map = new Map({
      target: containerRef.current,
      layers: [baseLayer],
      view: new View({ center: initialCenterRef.current, zoom: initialZoomRef.current, maxZoom: 22 }),
      controls: defaultControls({ attribution: false }),
    })
    mapRef.current = map

    // OL sizes its canvas off the target element's dimensions at
    // creation/resize-event time only -- it has no idea the container
    // itself can shrink/grow without a window resize, which is exactly
    // what happens when map.tsx opens/closes the side-by-side diagram
    // panel. Without this, the map keeps rendering at its pre-split
    // width until the next window resize.
    const resizeObserver = new ResizeObserver(() => map.updateSize())
    resizeObserver.observe(containerRef.current)

    const { setPosition, clearPosition } = useMapPointerStore.getState()
    map.on("pointermove", (evt) => {
      const [lon, lat] = toLonLat(evt.coordinate)
      setPosition(lon, lat)

      const target = map.getTargetElement()
      if (!target) return

      // Crosshair while a draw interaction is active (see the draw
      // effect below) -- otherwise, pointer cursor over a selectable
      // feature and default cursor everywhere else, the only thing that
      // needs to distinguish select-able-here from pan-able-everywhere
      // now that there's no separate select/pan mode to do it instead.
      if (
        useDrawStore.getState().drawingFeatureClassCode ||
        useConnectStore.getState().mode ||
        useDiagramPlaceStore.getState().phase === "picking" ||
        useTerritoryEditStore.getState().tool === "draw"
      ) {
        target.style.cursor = "crosshair"
        return
      }
      // Imported overlay features (Phase 15) are clickable too (they open the
      // read-only attribute popup), so the pointer cursor covers them as well
      // as the DB feature tiles.
      const hasFeature = map.hasFeatureAtPixel(evt.pixel, {
        layerFilter: (l) => isVectorLayer(l) || l === importedLayerRef.current,
        hitTolerance: 5,
      })
      target.style.cursor = hasFeature ? "pointer" : ""
    })
    map.getTargetElement().addEventListener("mouseleave", clearPosition)

    // The territory boundary overlay (see territoryStyleForFeature and
    // the boundary-load effect below) -- added right after the base
    // layer and before every other client layer, so with its default
    // zIndex 0 it renders above the basemap but below the feature tile
    // layers (added later in the catalog-driven effect, same zIndex 0 --
    // insertion order breaks the tie) and below every higher-zIndex
    // overlay (draft/highlight/edit). Starts hidden; the load effect
    // flips it on with the store. Same clear-and-repopulate pattern as
    // the sources below.
    const territorySource = new VectorSource()
    const territoryLayer = new VectorLayer({
      source: territorySource,
      style: territoryStyleForFeature,
      visible: false,
    })
    map.addLayer(territoryLayer)
    territorySourceRef.current = territorySource
    territoryLayerRef.current = territoryLayer

    // The imported-overlay layer (Phase 15) -- a single client-side
    // VectorLayer holding every visible imported overlay's features, at
    // zIndex 4 so it draws above the feature tiles and territory overlay
    // (both zIndex 0) but below the selection/analysis highlights (zIndex 5+),
    // which must never be hidden behind a reference layer. Same
    // clear-and-repopulate pattern as the sources below; the population effect
    // owns its contents.
    const importedSource = new VectorSource()
    const importedLayer = new VectorLayer({ source: importedSource, style: importedStyleForFeature, zIndex: 4 })
    map.addLayer(importedLayer)
    importedSourceRef.current = importedSource
    importedLayerRef.current = importedLayer

    // The map-annotation (redlining) overlay (Phase 36) -- one client-side
    // VectorLayer holding every annotation, each drawn from its own per-instance
    // style. At zIndex 7 it sits above the feature tiles, territory, imported
    // overlays, and the analysis highlights (0-6), since redline markup is
    // deliberate content meant to stay readable on top of them, but below the
    // active-authoring draft/edit layers (8/10). Starts hidden; the load effect
    // flips it on with the store. Same clear-and-repopulate pattern as the
    // sources below; the load effect owns its contents.
    const annotationSource = new VectorSource()
    const annotationLayer = new VectorLayer({
      source: annotationSource,
      style: annotationStyleForFeature,
      zIndex: 7,
      visible: false,
    })
    map.addLayer(annotationLayer)
    annotationSourceRef.current = annotationSource
    annotationLayerRef.current = annotationLayer

    // The saved-pins (bookmark) layer -- at zIndex 11, above even the
    // draft/edit authoring layers (8/10), so a user's navigation pin is always
    // visible on top of whatever else is on the map. Per-browser bookmarks, not
    // network data (map-pins-store.ts). Same clear-and-repopulate pattern; the
    // pins effect below owns its contents.
    const pinsSource = new VectorSource()
    const pinsLayer = new VectorLayer({ source: pinsSource, style: pinStyleForFeature, zIndex: 11 })
    map.addLayer(pinsLayer)
    pinsSourceRef.current = pinsSource

    // The boundary currently under authoring (Phase 11 Slice I) -- reuses
    // editStyleForFeature (its default branch is the polygon/multipolygon
    // cyan edit style), at zIndex 8 like the feature edit layer (the two
    // never coexist -- editing a feature and authoring a territory
    // boundary are separate workflows) so the in-progress boundary sits
    // above the read-only overlay and every feature tile. Same
    // clear-and-repopulate pattern; the seed effect below owns its
    // contents.
    const territoryEditSource = new VectorSource()
    const territoryEditLayer = new VectorLayer({ source: territoryEditSource, style: editStyleForFeature, zIndex: 8 })
    map.addLayer(territoryEditLayer)
    territoryEditSourceRef.current = territoryEditSource

    // The unsaved-draft marker layer (see draftStyleForFeature) --
    // created once alongside the map, not per-draw, so its source can
    // just be cleared/repopulated across multiple draw sessions instead
    // of tearing down and rebuilding a layer every time.
    const draftSource = new VectorSource()
    const draftLayer = new VectorLayer({ source: draftSource, style: draftStyleForFeature, zIndex: 10 })
    map.addLayer(draftLayer)
    draftSourceRef.current = draftSource

    // The highlight layer (see highlightStyleForFeature) -- above the
    // vector tile layers (zIndex 0 by default) so a halo is never hidden
    // behind other features, but below the draft layer; translucent
    // enough (see the style's alpha channels) that it reads as a glow
    // around the real feature rather than covering it. Same
    // clear-and-repopulate pattern as draftSource.
    const highlightSource = new VectorSource()
    const highlightLayer = new VectorLayer({ source: highlightSource, style: highlightStyleForFeature, zIndex: 5 })
    map.addLayer(highlightLayer)
    highlightSourceRef.current = highlightSource

    // Trace results (see the effect below and trace-store.ts) -- same
    // highlightStyleForFeature style function as the layer above (it
    // only ever reads a feature's own `role` tag, indifferent to which
    // source/layer it's attached to), just a separate source so an
    // async trace fetch and the synchronous selected/connect halo
    // effect never clobber each other's work.
    const traceSource = new VectorSource()
    const traceLayer = new VectorLayer({ source: traceSource, style: highlightStyleForFeature, zIndex: 6 })
    map.addLayer(traceLayer)
    traceSourceRef.current = traceSource

    // Isolation results (see the effect below and isolate-store.ts) --
    // same style function, own source, same zIndex as traceLayer (the
    // two never render simultaneously in practice, see isolateSourceRef's
    // own comment).
    const isolateSource = new VectorSource()
    const isolateLayer = new VectorLayer({ source: isolateSource, style: highlightStyleForFeature, zIndex: 6 })
    map.addLayer(isolateLayer)
    isolateSourceRef.current = isolateSource

    // Load-flow results (see the effect below and load-flow-store.ts) --
    // same style function, own source, same zIndex as traceLayer/
    // isolateLayer (all three are mutually exclusive in practice).
    const loadFlowSource = new VectorSource()
    const loadFlowLayer = new VectorLayer({ source: loadFlowSource, style: highlightStyleForFeature, zIndex: 6 })
    map.addLayer(loadFlowLayer)
    loadFlowSourceRef.current = loadFlowSource

    // Fault-current results (see the effect below and
    // fault-current-store.ts) -- same style function, own source, same
    // zIndex as the other analysis layers (all four are mutually
    // exclusive in practice).
    const faultCurrentSource = new VectorSource()
    const faultCurrentLayer = new VectorLayer({ source: faultCurrentSource, style: highlightStyleForFeature, zIndex: 6 })
    map.addLayer(faultCurrentLayer)
    faultCurrentSourceRef.current = faultCurrentSource

    // Query-builder matches (see the effect below and query-store.ts) --
    // same style function and zIndex as the analysis layers; its "query-result"
    // role gives it its own color (see HIGHLIGHT_COLORS).
    const queryResultSource = new VectorSource()
    const queryResultLayer = new VectorLayer({ source: queryResultSource, style: highlightStyleForFeature, zIndex: 6 })
    map.addLayer(queryResultLayer)
    queryResultSourceRef.current = queryResultSource

    // Affected service points of a selected OMS incident (see the effect below
    // and oms-highlight-store.ts) -- same style function and zIndex as the
    // analysis layers; its "outage-affected" role gives it its own rose color
    // (see HIGHLIGHT_COLORS).
    const affectedSource = new VectorSource()
    const affectedLayer = new VectorLayer({ source: affectedSource, style: highlightStyleForFeature, zIndex: 6 })
    map.addLayer(affectedLayer)
    affectedSourceRef.current = affectedSource

    // The one feature currently under geometry edit (see
    // editStyleForFeature) -- above the highlight layer so its cyan
    // isn't washed out underneath a selected halo, below the draft
    // layer (the two never coexist in practice: drawing a brand new
    // feature and editing an existing one are different workflows, but
    // keeping edit below draft costs nothing and avoids relying on that
    // never happening). Same clear-and-repopulate pattern as the above.
    const editSource = new VectorSource()
    const editLayer = new VectorLayer({ source: editSource, style: editStyleForFeature, zIndex: 8 })
    map.addLayer(editLayer)
    editSourceRef.current = editSource

    // Never rendered as its own layer -- just a plain VectorSource the
    // Snap interaction (see the effect below) reads from. Kept fresh as
    // the user pans/zooms during an active draw or geometry-edit
    // session; the Snap-attaching effect itself handles the initial
    // population at session start, this just keeps it current
    // afterward, since Snap only sees whatever's already in the source
    // at the moment of a drag, not a live view of the map.
    const snapSource = new VectorSource()
    snapSourceRef.current = snapSource
    map.on("moveend", () => {
      const drawingActive = Boolean(useDrawStore.getState().drawingFeatureClassCode)
      const editState = useEditStore.getState()
      const editingActive = Boolean(editState.editing && editState.geometryTool)
      if (!useSnapStore.getState().enabled || (!drawingActive && !editingActive)) return
      refreshSnapSource(map, vectorLayersRef.current, snapSource)
    })

    // Publish the current viewport as a 4326 bbox for the export dialog (the
    // only reader) -- the view is 3857, so transform the calculated extent to
    // lon/lat. Kept fresh on every pan/zoom, plus once after the first render
    // so a bbox exists before the user ever moves the map.
    const pushViewBbox = () => {
      const size = map.getSize()
      if (!size) return
      const view = map.getView()
      const extent = transformExtent(view.calculateExtent(size), "EPSG:3857", "EPSG:4326")
      useMapViewStore.getState().setBbox(extent as Bbox4326)
      // Publish the current zoom too (same handler, same cadence) so the status
      // bar can show it -- undefined only before the view has a resolution.
      const zoom = view.getZoom()
      if (zoom !== undefined) useMapViewStore.getState().setZoom(zoom)
    }
    map.on("moveend", pushViewBbox)
    map.once("rendercomplete", pushViewBbox)

    return () => {
      resizeObserver.disconnect()
      map.getTargetElement()?.removeEventListener("mouseleave", clearPosition)
      map.setTarget(undefined)
      mapRef.current = null
      baseLayerRef.current = null
      territorySourceRef.current = null
      territoryLayerRef.current = null
      annotationSourceRef.current = null
      annotationLayerRef.current = null
      pinsSourceRef.current = null
      territoryEditSourceRef.current = null
      territoryEditFeatureRef.current = null
      draftSourceRef.current = null
      highlightSourceRef.current = null
      traceSourceRef.current = null
      isolateSourceRef.current = null
      loadFlowSourceRef.current = null
      faultCurrentSourceRef.current = null
      queryResultSourceRef.current = null
      affectedSourceRef.current = null
      editSourceRef.current = null
      snapSourceRef.current = null
    }
  }, [])

  // Swaps the base tile layer's source (or hides it, for the blank choice) --
  // when the user picks a different basemap, and once the catalog first loads
  // (the layer is created empty, so this is what paints the initial tiles). It
  // waits for the catalog: until use-basemaps resolves, `basemaps` is undefined
  // and there's nothing to resolve a code against, so it holds the empty layer
  // rather than flashing blank. applyBasemap is idempotent, so re-running on any
  // catalog refetch is harmless.
  useEffect(() => {
    if (!baseLayerRef.current || basemaps === undefined) return
    applyBasemap(baseLayerRef.current, resolveBasemap(basemap, basemaps))
  }, [basemap, basemaps])

  // Adds/removes an OL Draw interaction for the feature class the user
  // picked to place (see src/components/map/new-feature-menu.tsx and
  // draw-store.ts), typed to match its geometry -- a single click for a
  // point, or multiple clicks plus a double-click/Enter to finish for a
  // line or polygon (OL's own default Draw behavior, no extra wiring
  // needed for that part). On drawend, the drawn feature is already
  // sitting in draftSource (Draw's target); geometryToGeoJSON converts
  // it from the view's EPSG:3857 into the EPSG:4326 GeoJSON the create
  // API expects.
  useEffect(() => {
    const map = mapRef.current
    const source = draftSourceRef.current
    if (!map || !source || !drawingFeatureClassCode || !drawingGeometryType) return

    const draw = new Draw({
      source,
      type: OL_DRAW_TYPE[drawingGeometryType],
      // Orthogonal constraint applies only to the multi-segment
      // geometries; a point has no segment to snap. The functions read
      // the ortho flag live, so no dependency on it is needed here.
      ...(drawingGeometryType === "line"
        ? { geometryFunction: orthoLineGeometryFunction }
        : drawingGeometryType === "polygon"
          ? { geometryFunction: orthoPolygonGeometryFunction }
          : {}),
    })
    draw.on("drawend", (evt: { feature: Feature }) => {
      const geometry = evt.feature.getGeometry()
      const geojson = geometry && geometryToGeoJSON(geometry)
      if (!geojson) return
      useDrawStore.getState().finishDrawing(geojson)
    })
    map.addInteraction(draw)

    const handleEscape = (evt: KeyboardEvent) => {
      if (evt.key === "Escape") useDrawStore.getState().cancelDrawing()
    }
    document.addEventListener("keydown", handleEscape)

    return () => {
      map.removeInteraction(draw)
      document.removeEventListener("keydown", handleEscape)
    }
  }, [drawingFeatureClassCode, drawingGeometryType])

  // Adds/removes an OL Draw interaction for the annotation kind the user picked
  // to draw (Phase 36 Slice 4 -- see new-annotation-menu.tsx and
  // annotation-edit-store.ts), typed to match the kind (text->Point,
  // box->Polygon, line/arrow->LineString). Unlike a feature, an annotation has
  // no attribute form: on drawend the geometry is converted to EPSG:4326 GeoJSON
  // and POSTed straight away (a text note seeded with placeholder text the
  // server will accept), then the created row is selected so
  // annotation-edit-panel.tsx opens for captioning/styling. A throwaway scratch
  // layer holds the in-progress shape; the real annotation layer repopulates
  // from the refetch the mutation triggers.
  useEffect(() => {
    const map = mapRef.current
    if (!map || !annotationDrawingKind) return

    const source = new VectorSource()
    const layer = new VectorLayer({ source, style: draftStyleForFeature, zIndex: 10 })
    map.addLayer(layer)

    const kind = annotationDrawingKind
    const draw = new Draw({ source, type: OL_ANNOTATION_DRAW_TYPE[kind] })
    draw.on("drawend", (evt: { feature: Feature }) => {
      const geometry = evt.feature.getGeometry()
      const geojson = geometry && geometryToGeoJSON(geometry)
      if (!geojson) {
        useAnnotationEditStore.getState().cancelDrawing()
        return
      }
      createAnnotation(
        // A text note must carry non-empty text; seed a placeholder the editor
        // then replaces. The other kinds start caption-less.
        { kind, geometry: geojson, ...(kind === "text" ? { text: "New note" } : {}) },
        {
          onSuccess: (created) => useAnnotationEditStore.getState().select(created.id),
          onError: (err) =>
            useToastStore.getState().pushError(err.message || "Could not create the annotation."),
        },
      )
      // End the draw session immediately; the select() above (once the create
      // resolves) opens the edit panel on the new annotation.
      useAnnotationEditStore.getState().cancelDrawing()
    })
    map.addInteraction(draw)

    const handleEscape = (evt: KeyboardEvent) => {
      if (evt.key === "Escape") useAnnotationEditStore.getState().cancelDrawing()
    }
    document.addEventListener("keydown", handleEscape)

    return () => {
      map.removeInteraction(draw)
      map.removeLayer(layer)
      document.removeEventListener("keydown", handleEscape)
    }
  }, [annotationDrawingKind, createAnnotation])

  // Renders an already-placed draft into the draft layer and lets the user
  // drag it (Phase 20 -- docs/phase-20-split-copy.md). The draw flow's own
  // Draw interaction leaves the just-drawn feature sitting in draftSource, but
  // a draft *seeded* from an existing feature (Duplicate -- draw-store.ts's
  // startCopyDraft) has nothing there yet, so this seeds it from draft.geometry
  // once. Either way a Translate keeps draft.geometry in sync as it's dragged,
  // so feature-create-form.tsx's Save posts the final position. Not gated on a
  // geometry-edit tool -- an unsaved draft is always freely movable. Snap is
  // deliberately left off here (it only attaches while drawing or editing);
  // positioning a copy is a coarse nudge, not a precise re-snap.
  useEffect(() => {
    const map = mapRef.current
    const source = draftSourceRef.current
    if (!map || !source || !draft?.geometry) return

    // A drawn draft is already in the source (Draw put it there); a seeded
    // copy draft is not -- add it once so it renders and can be grabbed.
    let feature = source.getFeatures()[0] ?? null
    if (!feature) {
      const geometry = geoJSONToGeometry(draft.geometry)
      if (!geometry) return
      feature = new Feature(geometry)
      source.addFeature(feature)
    }

    const dragged = feature
    const translate = new Translate({ features: new Collection([dragged]) })
    translate.on("translateend", () => {
      const geometry = dragged.getGeometry()
      const geojson = geometry && geometryToGeoJSON(geometry)
      if (geojson) useDrawStore.getState().updateDraftGeometry(geojson)
    })
    map.addInteraction(translate)
    return () => {
      map.removeInteraction(translate)
    }
  }, [draft])

  // The ad-hoc measure tool (see src/components/map/measure-toolbar.tsx
  // and measure-store.ts). While a mode is armed this owns a throwaway
  // scratch layer + Draw interaction that never touches the real feature
  // data -- nothing is created, and turning the tool off (or unmounting)
  // clears every measurement. A live tooltip Overlay trails the sketch,
  // re-reading getLength/getArea on each vertex/pointer move; drawend
  // freezes that tooltip in place (pushed onto measureOverlaysRef so the
  // units-change effect below can re-format it) and a fresh one arms for
  // the next measurement, so you can lay down several without disarming.
  // Measurement is geodesic via ol/sphere on the view's EPSG:3857
  // geometry (getLength/getArea default to that projection), so it stays
  // correct regardless of latitude. handleClick bails while a mode is
  // armed, so map clicks feed the Draw interaction, not selection.
  useEffect(() => {
    const map = mapRef.current
    if (!map || !measureMode) return

    const source = new VectorSource()
    const layer = new VectorLayer({ source, style: MEASURE_STYLE, zIndex: 9 })
    map.addLayer(layer)

    const measureOf = (geometry: Geometry | undefined) => {
      if (geometry instanceof Polygon) {
        const ring = geometry.getLinearRing(0)
        // A polygon still being drawn always has OL's auto-closing
        // segment; measure the ring the user has actually laid down.
        return { meters: getArea(geometry), kind: "area" as const, position: geometry.getInteriorPoint().getCoordinates().slice(0, 2), hasSpan: (ring?.getCoordinates().length ?? 0) > 3 }
      }
      if (geometry instanceof LineString) {
        return { meters: getLength(geometry), kind: "length" as const, position: geometry.getLastCoordinate(), hasSpan: geometry.getCoordinates().length > 1 }
      }
      return null
    }

    const labelText = (meters: number, kind: "length" | "area") =>
      kind === "area" ? formatArea(meters, useUnitsStore.getState().units) : formatLength(meters, useUnitsStore.getState().units)

    const makeTooltipEl = (frozen: boolean) => {
      const el = document.createElement("div")
      el.className = frozen
        ? "pointer-events-none rounded border border-primary/30 bg-background/95 px-1.5 py-0.5 text-xs font-medium text-foreground shadow-sm"
        : "pointer-events-none rounded bg-primary px-1.5 py-0.5 text-xs font-medium text-primary-foreground shadow-sm"
      return el
    }

    const draw = new Draw({
      source,
      type: measureMode === "area" ? "Polygon" : "LineString",
    })

    let liveEl: HTMLElement | null = null
    let liveOverlay: Overlay | null = null
    let changeKey: EventsKey | null = null

    draw.on("drawstart", (evt: { feature: Feature }) => {
      liveEl = makeTooltipEl(false)
      liveOverlay = new Overlay({ element: liveEl, offset: [12, 0], positioning: "center-left", stopEvent: false })
      map.addOverlay(liveOverlay)
      const geometry = evt.feature.getGeometry()
      if (!geometry) return
      changeKey = geometry.on("change", () => {
        const m = measureOf(geometry)
        if (!m || !liveEl || !liveOverlay) return
        liveEl.textContent = m.hasSpan ? labelText(m.meters, m.kind) : ""
        liveOverlay.setPosition(m.position as Coordinate)
      })
    })

    draw.on("drawend", (evt: { feature: Feature }) => {
      if (changeKey) {
        unByKey(changeKey)
        changeKey = null
      }
      const geometry = evt.feature.getGeometry()
      const m = geometry && measureOf(geometry)
      // Retire the trailing live tooltip and drop a frozen one in its
      // place, tracked so a units flip re-formats it (see below).
      if (liveOverlay) {
        map.removeOverlay(liveOverlay)
        liveOverlay = null
        liveEl = null
      }
      if (m && m.hasSpan) {
        const el = makeTooltipEl(true)
        el.textContent = labelText(m.meters, m.kind)
        const overlay = new Overlay({ element: el, offset: [12, 0], positioning: "center-left", stopEvent: false })
        overlay.setPosition(m.position as Coordinate)
        map.addOverlay(overlay)
        measureOverlaysRef.current.push({ overlay, el, meters: m.meters, kind: m.kind })
      }
    })

    map.addInteraction(draw)

    const handleEscape = (evt: KeyboardEvent) => {
      if (evt.key === "Escape") useMeasureStore.getState().setMode(null)
    }
    document.addEventListener("keydown", handleEscape)

    return () => {
      if (changeKey) unByKey(changeKey)
      if (liveOverlay) map.removeOverlay(liveOverlay)
      for (const { overlay } of measureOverlaysRef.current) map.removeOverlay(overlay)
      measureOverlaysRef.current = []
      map.removeInteraction(draw)
      map.removeLayer(layer)
      document.removeEventListener("keydown", handleEscape)
    }
  }, [measureMode])

  // The box-zoom tool (see src/components/map/box-zoom-button.tsx and
  // box-zoom-store.ts). While armed this owns a throwaway scratch layer +
  // Draw interaction that rubber-bands a rectangle -- press one corner, drag
  // (or click-move-click) to the opposite corner -- and on drawend fits the
  // view to that box's extent, then DISARMS itself (single-shot: one box per
  // arm, so it never fights the pan/select interactions once you're done).
  // Nothing is created; like the measure tool it never touches feature data,
  // and handleClick bails while it's armed so the box clicks feed the Draw,
  // not selection. Escape cancels without zooming. maxZoom caps how far a
  // tiny box can dive so a stray flick can't bury the view.
  useEffect(() => {
    const map = mapRef.current
    if (!map || !boxZoomActive) return

    const source = new VectorSource()
    const layer = new VectorLayer({ source, style: BOX_ZOOM_STYLE, zIndex: 9 })
    map.addLayer(layer)

    // freehand: press one corner, drag to the opposite, release -- Draw owns
    // the whole pointer gesture so DragPan doesn't pan the map underneath it.
    const draw = new Draw({ source, type: "Circle", geometryFunction: createBox(), freehand: true })

    draw.on("drawend", (evt: { feature: Feature }) => {
      const geometry = evt.feature.getGeometry()
      if (geometry) {
        map.getView().fit(geometry.getExtent(), { duration: 300, maxZoom: 22 })
      }
      // Single-shot: disarm after one box (this tears the effect down).
      useBoxZoomStore.getState().deactivate()
    })

    map.addInteraction(draw)

    const handleEscape = (evt: KeyboardEvent) => {
      if (evt.key === "Escape") useBoxZoomStore.getState().deactivate()
    }
    document.addEventListener("keydown", handleEscape)

    return () => {
      map.removeInteraction(draw)
      map.removeLayer(layer)
      document.removeEventListener("keydown", handleEscape)
    }
  }, [boxZoomActive])

  // Re-labels every frozen measurement in place when the metric/imperial
  // toggle flips (see units-store.ts). The measure effect above owns the
  // Overlays' lifecycle; this only rewrites their text from the raw
  // meters it stashed, so a units change is a pure re-format, never a
  // re-measure or a teardown of the in-progress sketch.
  useEffect(() => {
    for (const { el, meters, kind } of measureOverlaysRef.current) {
      el.textContent = kind === "area" ? formatArea(meters, units) : formatLength(meters, units)
    }
  }, [units])

  // Clears the draft marker once its form is dismissed -- saved
  // (feature-create-form.tsx clears the draft on success, after
  // selecting the real, now-persisted feature) or canceled.
  useEffect(() => {
    if (!draft) draftSourceRef.current?.clear()
  }, [draft])

  // Escape cancels an in-progress connect/relate workflow (see
  // connect-store.ts and the click handler below) while it's still
  // waiting on a target feature -- same affordance as canceling a draw.
  useEffect(() => {
    if (!connectMode) return
    const handleEscape = (evt: KeyboardEvent) => {
      if (evt.key === "Escape") useConnectStore.getState().cancel()
    }
    document.addEventListener("keydown", handleEscape)
    return () => document.removeEventListener("keydown", handleEscape)
  }, [connectMode])

  // Escape also cancels an armed diagram-place pick (Phase 12) at either
  // step -- while picking on the map, or once a feature is armed and
  // waiting on a place-click in the diagram canvas -- same affordance the
  // connect workflow above gets.
  useEffect(() => {
    if (diagramPlacePhase === "idle") return
    const handleEscape = (evt: KeyboardEvent) => {
      if (evt.key === "Escape") useDiagramPlaceStore.getState().cancel()
    }
    document.addEventListener("keydown", handleEscape)
    return () => document.removeEventListener("keydown", handleEscape)
  }, [diagramPlacePhase])

  // Escape also cancels an already-placed draft waiting on
  // feature-create-form.tsx -- one step later than the drawing-in-progress
  // Escape above (which only clears drawingFeatureClassCode, not draft).
  // Covers both the map's own draw flow (geometry already placed, form
  // open) and a geometry-less draft started from a diagram's digitizing
  // tool (draw-store.ts's diagramPlacement) -- neither had a Escape-to-
  // cancel affordance at this stage before, only the form's own Cancel
  // button.
  useEffect(() => {
    if (!draft) return
    const handleEscape = (evt: KeyboardEvent) => {
      if (evt.key === "Escape") useDrawStore.getState().clearDraft()
    }
    document.addEventListener("keydown", handleEscape)
    return () => document.removeEventListener("keydown", handleEscape)
  }, [draft])

  // Escape also cancels an in-progress feature edit (see edit-store.ts
  // and feature-edit-form.tsx) -- same affordance as canceling a draw
  // or a connect workflow, discarding whatever's been typed so far.
  useEffect(() => {
    if (!editing) return
    const handleEscape = (evt: KeyboardEvent) => {
      if (evt.key === "Escape") useEditStore.getState().stop()
    }
    document.addEventListener("keydown", handleEscape)
    return () => document.removeEventListener("keydown", handleEscape)
  }, [editing])

  // Draws (or clears) the highlight halo -- while a connect/relate
  // workflow is active it takes over entirely (source/target, each in
  // their own color), otherwise it's just whichever single feature is
  // selected. Each fetched feature's real geometry (see the useFeature
  // calls above) is what makes this a true highlight of the actual
  // shape rather than a generic marker -- a temporary OL Feature built
  // fresh each time this runs, tagged with a `role` the style function
  // reads (see highlightStyleForFeature), added to highlightSource, and
  // nothing else keeps a reference to it once the effect reruns and
  // clears the source again.
  useEffect(() => {
    const source = highlightSourceRef.current
    if (!source) return
    source.clear()

    const add = (geometry: FeatureGeometry | undefined, role: HighlightRole) => {
      const olGeometry = geometry && geoJSONToGeometry(geometry)
      if (!olGeometry) return
      const feature = new Feature(olGeometry)
      feature.set("role", role)
      source.addFeature(feature)
    }

    // Skips the "selected" halo entirely for a feature currently under
    // geometry edit -- selectedFeature is the server-fetched, cached
    // geometry (only refreshed by Save's query invalidation), so while
    // a drag is in progress this halo would sit at the feature's real,
    // pre-drag position -- a stale ghost underneath the live cyan
    // edit-geometry copy (editStyleForFeature, seeded/moved by the
    // effects below) that already represents the same feature
    // correctly. Harmless during attribute-only editing (nothing on
    // the map moves then); only became visible now that geometry can
    // actually change mid-session.
    const isEditingSelected = editing && selected && editing.featureId === selected.featureId
    if (connectMode) {
      add(connectSourceFeature?.geometry, "connect-source")
      add(connectTargetFeature?.geometry, "connect-target")
    } else if (!isEditingSelected) {
      add(selectedFeature?.geometry, "selected")
    }
  }, [connectMode, connectSourceFeature, connectTargetFeature, selectedFeature, editing, selected])

  // Fetches and highlights a trace result's own two feature sets (every
  // reached feature, and, distinctly, whichever of those a barrier
  // condition actually stopped at -- see trace-store.ts and
  // feature-panel.tsx's Trace downstream/upstream triggers). Unlike the
  // selected/connect halo effect above, this can't just read a fixed
  // number of `useFeature` hooks -- a trace result is an arbitrary-length
  // list -- so it fetches every reached feature's geometry imperatively
  // via apiFetch instead, into its own dedicated traceSource (see why in
  // that ref's own comment). `cancelled` guards against a slow fetch
  // resolving after a newer trace (or Clear) has already superseded it.
  useEffect(() => {
    const source = traceSourceRef.current
    if (!source) return

    if (!traceResult) {
      source.clear()
      return
    }

    // Reassigned into typed locals (rather than relying on the null
    // checks above) purely so TypeScript's control-flow narrowing --
    // which doesn't propagate into the nested async function below --
    // sees both as definitely non-null there too.
    const src: VectorSource = source
    const result: NonNullable<typeof traceResult> = traceResult

    let cancelled = false
    const barrierKeys = new Set(result.barriers.map((b) => `${b.feature_class}:${b.feature_id}`))
    const targetKeys = new Set((result.targets ?? []).map((t) => `${t.feature_class}:${t.feature_id}`))

    async function loadTraceFeatures() {
      const fetched = await Promise.all(
        result.features.map(async (ref) => {
          try {
            const feature = await apiFetch<{ geometry?: FeatureGeometry }>(
              `/features/${ref.feature_class}/${ref.feature_id}`,
            )
            return { ref, geometry: feature.geometry }
          } catch {
            // A feature the trace reached that fails to load individually
            // (e.g. deleted between the trace running and this fetch)
            // just doesn't get a halo -- not worth failing the whole
            // highlight over one stale reference.
            return null
          }
        }),
      )
      if (cancelled) return

      src.clear()
      for (const f of fetched) {
        if (!f?.geometry) continue
        const olGeometry = geoJSONToGeometry(f.geometry)
        if (!olGeometry) continue
        const feature = new Feature(olGeometry)
        const key = `${f.ref.feature_class}:${f.ref.feature_id}`
        // Barrier takes precedence over target in the unlikely case a
        // node is somehow both -- the two are mutually exclusive in
        // every seeded scenario (a barrier halts expansion before a
        // target-class check would even run against it, see
        // api/internal/trace/graph.go's bfs), this is just a
        // deterministic tie-break, not a real modeled case.
        feature.set("role", barrierKeys.has(key) ? "trace-barrier" : targetKeys.has(key) ? "trace-target" : "trace")
        src.addFeature(feature)
      }
    }

    loadTraceFeatures()
    return () => {
      cancelled = true
    }
  }, [traceResult])

  // Isolation result highlight -- same async-fetch-into-a-dedicated-source
  // shape as the trace effect above, just three named lists
  // (de_energized, upstream_boundary, possible_backfeed_paths) instead
  // of one feature list plus subsets of it.
  useEffect(() => {
    const source = isolateSourceRef.current
    if (!source) return

    if (!isolateResult) {
      source.clear()
      return
    }

    const src: VectorSource = source
    const result: NonNullable<typeof isolateResult> = isolateResult

    let cancelled = false

    async function loadIsolationFeatures() {
      const refs = [
        ...result.de_energized.map((ref) => ({ ref, role: "isolation-deenergized" as const })),
        ...(result.upstream_boundary ? [{ ref: result.upstream_boundary, role: "isolation-boundary" as const }] : []),
        ...(result.possible_backfeed_paths ?? []).map((ref) => ({ ref, role: "isolation-backfeed" as const })),
      ]

      const fetched = await Promise.all(
        refs.map(async ({ ref, role }) => {
          try {
            const feature = await apiFetch<{ geometry?: FeatureGeometry }>(
              `/features/${ref.feature_class}/${ref.feature_id}`,
            )
            return { geometry: feature.geometry, role }
          } catch {
            return null
          }
        }),
      )
      if (cancelled) return

      src.clear()
      for (const f of fetched) {
        if (!f?.geometry) continue
        const olGeometry = geoJSONToGeometry(f.geometry)
        if (!olGeometry) continue
        const feature = new Feature(olGeometry)
        feature.set("role", f.role)
        src.addFeature(feature)
      }
    }

    loadIsolationFeatures()
    return () => {
      cancelled = true
    }
  }, [isolateResult])

  // Affected-service-point highlight for a selected OMS incident
  // (oms-highlight-store.ts, Phase 49). Same async-fetch-into-a-dedicated-source
  // shape as the trace/isolation effects above, but the affected endpoint
  // already returns each SP's lon/lat, so it's a single fetch drawing Point
  // features -- no per-feature geometry re-fetch. Re-runs on either the
  // selected incident OR the focusToken changing, so "Show on map" on the
  // already-selected incident still re-fetches and re-fits. The fit is a
  // one-shot per focusToken (affectedFittedTokenRef), like the pan-to-feature
  // effect, so a manual pan afterward isn't yanked back.
  useEffect(() => {
    const source = affectedSourceRef.current
    if (!source) return

    if (!omsIncidentId) {
      source.clear()
      return
    }

    const src: VectorSource = source
    const id = omsIncidentId
    const token = omsFocusToken
    let cancelled = false

    async function loadAffectedArea() {
      let area: AffectedArea
      try {
        area = await apiFetch<AffectedArea>(`/oms/incidents/${id}/affected`)
      } catch {
        return
      }
      if (cancelled) return

      src.clear()
      for (const sp of area.service_points) {
        const feature = new Feature(new Point(fromLonLat([sp.longitude, sp.latitude])))
        feature.set("role", "outage-affected")
        src.addFeature(feature)
      }

      // Fit the view to the outage cone once per "Show on map" request.
      if (token !== 0 && token !== affectedFittedTokenRef.current) {
        const map = mapRef.current
        const extent = src.getExtent()
        if (map && !isEmptyExtent(extent)) {
          map.getView().fit(extent, { padding: [80, 80, 80, 80], maxZoom: 18, duration: 400 })
        }
        affectedFittedTokenRef.current = token
      }
    }

    loadAffectedArea()
    return () => {
      cancelled = true
    }
  }, [omsIncidentId, omsFocusToken])

  // Load-flow results (see load-flow-store.ts) -- same async-fetch-into-
  // a-dedicated-source shape as the trace/isolation effects above.
  // Every reached node gets the base "load-flow" role; a node whose own
  // feature ref appears in Overloaded is tagged "load-flow-overload"
  // instead (takes precedence -- an overloaded conductor is more
  // actionable than "just reached"); a barrier node never appears in
  // `nodes` at all (the sweep stopped before resolving its electrical
  // state), so its geometry is fetched separately and tagged
  // "load-flow-barrier".
  useEffect(() => {
    const source = loadFlowSourceRef.current
    if (!source) return

    // The radial load flow, the weakly-meshed load flow (meshed-load-flow-
    // store.ts), the nodal Y-bus load flow (nodal-load-flow-store.ts) and the
    // dispatch router (auto-load-flow-store.ts, whose active engine's inner
    // result is one of those same shapes) are mutually exclusive in the UI
    // (arming one clears the others) and all report the same node/overloaded/
    // barrier shape, so they share this one highlight source and the load-flow
    // roles/styles -- whichever has a result is the active one.
    const result = loadFlowResult ?? meshedLoadFlowResult ?? nodalLoadFlowResult ?? activeInner(autoLoadFlowResult)
    if (!result) {
      source.clear()
      return
    }

    const src: VectorSource = source

    let cancelled = false
    const overloadedKeys = new Set(result.overloaded.map((r) => `${r.feature_class}:${r.feature_id}`))

    async function loadLoadFlowFeatures() {
      const refs = [
        ...result.nodes.map((n) => ({
          ref: { feature_class: n.feature_class, feature_id: n.feature_id },
          role: overloadedKeys.has(`${n.feature_class}:${n.feature_id}`)
            ? ("load-flow-overload" as const)
            : ("load-flow" as const),
        })),
        ...result.barriers.map((ref) => ({ ref, role: "load-flow-barrier" as const })),
      ]

      const fetched = await Promise.all(
        refs.map(async ({ ref, role }) => {
          try {
            const feature = await apiFetch<{ geometry?: FeatureGeometry }>(
              `/features/${ref.feature_class}/${ref.feature_id}`,
            )
            return { geometry: feature.geometry, role }
          } catch {
            return null
          }
        }),
      )
      if (cancelled) return

      src.clear()
      for (const f of fetched) {
        if (!f?.geometry) continue
        const olGeometry = geoJSONToGeometry(f.geometry)
        if (!olGeometry) continue
        const feature = new Feature(olGeometry)
        feature.set("role", f.role)
        src.addFeature(feature)
      }
    }

    loadLoadFlowFeatures()
    return () => {
      cancelled = true
    }
  }, [loadFlowResult, meshedLoadFlowResult, nodalLoadFlowResult, autoLoadFlowResult])

  // Fault-current results (see fault-current-store.ts) -- same async-
  // fetch-into-a-dedicated-source shape as the load-flow effect above.
  // Every reached node gets the "fault-current" role; a barrier node
  // (the open device the walk stopped at) never appears in `nodes`, so
  // its geometry is fetched separately and tagged "fault-current-barrier".
  useEffect(() => {
    const source = faultCurrentSourceRef.current
    if (!source) return

    if (!faultCurrentResult) {
      source.clear()
      return
    }

    const src: VectorSource = source
    const result: NonNullable<typeof faultCurrentResult> = faultCurrentResult

    let cancelled = false

    async function loadFaultCurrentFeatures() {
      const refs = [
        ...result.nodes.map((n) => ({
          ref: { feature_class: n.feature_class, feature_id: n.feature_id },
          role: "fault-current" as const,
        })),
        ...result.barriers.map((ref) => ({ ref, role: "fault-current-barrier" as const })),
      ]

      const fetched = await Promise.all(
        refs.map(async ({ ref, role }) => {
          try {
            const feature = await apiFetch<{ geometry?: FeatureGeometry }>(
              `/features/${ref.feature_class}/${ref.feature_id}`,
            )
            return { geometry: feature.geometry, role }
          } catch {
            return null
          }
        }),
      )
      if (cancelled) return

      src.clear()
      for (const f of fetched) {
        if (!f?.geometry) continue
        const olGeometry = geoJSONToGeometry(f.geometry)
        if (!olGeometry) continue
        const feature = new Feature(olGeometry)
        feature.set("role", f.role)
        src.addFeature(feature)
      }
    }

    loadFaultCurrentFeatures()
    return () => {
      cancelled = true
    }
  }, [faultCurrentResult])

  // Query-builder matches (see query-store.ts and feature-query-panel.tsx).
  // Unlike the four analysis effects above this one is fully synchronous:
  // each match's geometry already rides along in the query response
  // (selectColumns' geom_json, see docs/phase-13-query-builder.md), so
  // there's nothing to fetch -- it just reads the rows and repaints the
  // dedicated queryResultSource, every match tagged "query-result".
  useEffect(() => {
    const source = queryResultSourceRef.current
    if (!source) return

    source.clear()
    if (!queryResults) return

    for (const row of queryResults) {
      if (!row.geometry) continue
      const olGeometry = geoJSONToGeometry(row.geometry)
      if (!olGeometry) continue
      const feature = new Feature(olGeometry)
      feature.set("role", "query-result")
      source.addFeature(feature)
    }
  }, [queryResults])

  // Pans/zooms to a feature selected from a connection/relationship
  // list row (feature-panel.tsx) rather than a direct map click -- see
  // selection-store.ts's focusToken for why a click doesn't also do
  // this (it's already on screen). maxZoom caps how far a point (a
  // zero-size extent) gets zoomed in; a line or polygon's real extent
  // fits within that same padding regardless of size.
  //
  // Depends on both focusToken and selectedFeature, not focusToken
  // alone -- a freshly clicked row almost always targets a feature this
  // tab hasn't fetched before, so selectedFeature is still `undefined`
  // on the very render where focusToken changes; a token-only dependency
  // would silently skip the pan for exactly that (the common) case and
  // only happen to work when the feature's data was already cached from
  // earlier in the session. fittedTokenRef makes sure the actual fit
  // still only runs once per focus request (not on every later refetch
  // of the same feature) by remembering the last token it already
  // handled.
  useEffect(() => {
    if (focusToken === 0 || focusToken === fittedTokenRef.current) return
    const map = mapRef.current
    const geometry = selectedFeature?.geometry && geoJSONToGeometry(selectedFeature.geometry)
    const extent = geometry?.getExtent()
    if (!map || !extent || isEmptyExtent(extent)) return
    map.getView().fit(extent, { padding: [80, 80, 80, 80], maxZoom: 18, duration: 400 })
    fittedTokenRef.current = focusToken
  }, [focusToken, selectedFeature])

  // Go-to-coordinates search (status-bar.tsx). Same one-shot token pattern as
  // the focus pan above: a bumped token animates the view to the target lon/lat.
  // With no explicit zoom it keeps the current scale but never stays so far out
  // that the point is a speck -- so a jump from a whole-network view still lands
  // somewhere useful.
  const gotoToken = useMapGotoStore((s) => s.token)
  const handledGotoRef = useRef(0)
  useEffect(() => {
    if (gotoToken === 0 || gotoToken === handledGotoRef.current) return
    handledGotoRef.current = gotoToken
    const map = mapRef.current
    const target = useMapGotoStore.getState().target
    if (!map || !target) return
    const view = map.getView()
    const zoom = target.zoom ?? Math.max(view.getZoom() ?? 15, 15)
    view.animate({ center: fromLonLat([target.lng, target.lat]), zoom, duration: 500 })
  }, [gotoToken])

  // Renders the user's saved pins (bookmarks) -- one teardrop marker per pin,
  // repopulated whenever the persisted list changes (add/remove/clear). Each OL
  // Feature carries the pin's `name` so pinStyleForFeature can label it. Purely
  // a client-side navigation aid; the list itself lives per-browser in
  // map-pins-store.ts.
  const pins = useMapPinsStore((s) => s.pins)
  useEffect(() => {
    const source = pinsSourceRef.current
    if (!source) return
    source.clear()
    for (const pin of pins) {
      const feature = new Feature({ geometry: new Point(fromLonLat([pin.lng, pin.lat])) })
      feature.set("name", pin.name)
      source.addFeature(feature)
    }
  }, [pins])

  // Forces every vector tile source to refetch -- bumped by
  // feature-create-form.tsx after a successful save, since the catalog
  // itself (which sources exist) doesn't change when a feature is
  // added, so nothing about the catalog-driven layer effect below would
  // otherwise signal that tiles are stale. Deliberately not
  // `source.refresh()`: VectorTileSource doesn't override `clear()`, so
  // refresh() only fires a 'change' event without actually discarding
  // the renderer's already-loaded tile cache -- a tile fetched once
  // (even an empty one, before a feature existed there) would otherwise
  // never be requested again without a full page reload. Calling
  // setUrl() with a new cache-busting version query param changes the
  // source's key, which is what actually makes the renderer discard its
  // cached tiles and refetch.
  useEffect(() => {
    if (refreshVersion === 0) return
    for (const layer of vectorLayersRef.current) {
      const sourceId = layer.get("sourceId") as string
      const source = layer.getSource()
      source?.setUrl(tileUrlTemplate(sourceId, refreshVersion))
    }
  }, [refreshVersion])

  // Re-renders every vector tile layer in place after a successful
  // delete (see deleted-features-store.ts for why this is needed at
  // all -- Martin's tiles never stop containing the deleted feature's
  // bytes) -- deliberately `layer.changed()`, not a refetch: the id
  // needs hiding client-side regardless of what Martin returns, and the
  // already-loaded tile data in OL's own cache is enough for that, no
  // network round trip required.
  useEffect(() => {
    if (deletedIds.size === 0) return
    for (const layer of vectorLayersRef.current) layer.changed()
  }, [deletedIds])

  // Re-renders every vector tile layer in place whenever an edit
  // session starts or ends -- styleForFeature's hidden-while-editing
  // check (map-layers.ts) needs this same nudge deletedIds does, for
  // the same reason: nothing else tells OL's renderer that a feature
  // it already rasterized needs a different style now.
  useEffect(() => {
    for (const layer of vectorLayersRef.current) layer.changed()
  }, [editing])

  // Seeds (or clears) the edit-geometry source from the real feature
  // under edit -- once per session, not once per render: editSeededForRef
  // remembers which session was last seeded, so a later refetch of the
  // same feature (e.g. after Save invalidates its query) never stomps
  // whatever the user's mid-drag progress is. Waits for editingFeature's
  // geometry to actually be loaded (same async-arrives-later lesson as
  // the pan-to-feature effect above) rather than assuming it's already
  // there the instant `editing` is set.
  useEffect(() => {
    const source = editSourceRef.current
    if (!source) return

    if (!editing) {
      source.clear()
      editFeatureRef.current = null
      editSeededForRef.current = null
      return
    }

    const key = `${editing.featureClassCode}:${editing.featureId}`
    if (editSeededForRef.current === key) return
    if (!editingFeature?.geometry) return

    const geometry = geoJSONToGeometry(editingFeature.geometry)
    if (!geometry) return
    source.clear()
    const feature = new Feature(geometry)
    source.addFeature(feature)
    editFeatureRef.current = feature
    editSeededForRef.current = key
  }, [editing, editingFeature])

  // Attaches whichever of Modify/Translate geometry-edit-toolbar.tsx's
  // buttons picked (see edit-store.ts's `geometryTool`) to the single
  // feature the effect above seeded -- never both at once, since they'd
  // fight over the same drag gestures. Both interactions mutate
  // editFeatureRef's geometry in place; their own end events convert it
  // back to GeoJSON (geometryToGeoJSON, the same conversion the draw
  // interaction's drawend already uses) and hand it to edit-store.ts's
  // `dirtyGeometry` for feature-edit-form.tsx's Save to pick up.
  // Deliberately no custom deleteCondition/insertVertexCondition on
  // Modify -- its defaults (alt+click to delete a vertex, drag a
  // segment midpoint to insert one) are exactly the behavior
  // geometry-edit-toolbar.tsx's hint text already describes.
  //
  // Depends on editingFeature too, not just geometryTool/editing --
  // editFeatureRef.current is only populated once the seeding effect
  // above actually runs with loaded data; picking a tool before that
  // fetch resolves would otherwise attach nothing and silently strand
  // the click, the same async-arrives-later race the pan-to-feature
  // effect already had to account for.
  useEffect(() => {
    const map = mapRef.current
    const feature = editFeatureRef.current
    if (!map || !editing || !feature || !geometryTool) return

    const features = new Collection([feature])
    const handleEnd = () => {
      const geometry = feature.getGeometry()
      const geojson = geometry && geometryToGeoJSON(geometry)
      if (geojson) useEditStore.getState().setDirtyGeometry(geojson)
    }

    if (geometryTool === "move") {
      const translate = new Translate({ features })
      translate.on("translateend", handleEnd)
      map.addInteraction(translate)
      return () => {
        map.removeInteraction(translate)
      }
    }

    // "vertices" is the only other tool that attaches a drag interaction here.
    // "split" is a one-off click handled by its own effect below and must not
    // also get a Modify (that would let the user reshape the line mid-split),
    // so bail without one for anything that isn't vertex editing.
    if (geometryTool !== "vertices") return

    const modify = new Modify({ features })
    modify.on("modifyend", handleEnd)
    map.addInteraction(modify)
    return () => {
      map.removeInteraction(modify)
    }
  }, [geometryTool, editing, editingFeature])

  // Attaches OL's Snap interaction whenever a workflow that manipulates
  // geometry is active -- drawing a brand new feature (draft-store.ts's
  // drawingFeatureClassCode) or geometry-edit-toolbar.tsx's Move/Edit
  // vertices tools (edit-store.ts's geometryTool) -- and snap-store.ts's
  // `enabled` flag is on. Both workflows share the one toggle and the
  // one snap source, per the user's own framing: "when drawing a new
  // feature, snapping should also be available."
  //
  // Placed textually after both the Draw effect above and the
  // Modify/Translate effect just above this one -- not just logically
  // gated to run after them. OL only lets a Snap interaction influence a
  // drag already being handled by another interaction if Snap was added
  // to the map *after* that interaction (confirmed by reading
  // ol/interaction/Snap.js directly), and React runs a single commit's
  // effects in the order they're defined in the component, regardless of
  // which dependency actually changed -- so Snap has to physically sit
  // below both to reliably attach after them on every render that
  // recreates any of the three.
  useEffect(() => {
    const map = mapRef.current
    const snapSource = snapSourceRef.current
    if (!map || !snapSource) return
    const drawingActive = Boolean(drawingFeatureClassCode)
    // Only the drag tools (Move/Edit vertices) have a gesture Snap can bend;
    // "split" is a single click with no interaction to influence, so it doesn't
    // arm snapping.
    const editingActive = Boolean(editing && (geometryTool === "move" || geometryTool === "vertices"))
    if (!snapEnabled || (!drawingActive && !editingActive)) return

    refreshSnapSource(map, vectorLayersRef.current, snapSource)
    const snap = new Snap({ source: snapSource })
    map.addInteraction(snap)
    return () => {
      map.removeInteraction(snap)
    }
  }, [snapEnabled, drawingFeatureClassCode, editing, geometryTool])

  // The Split tool (geometry-edit-toolbar.tsx, edit-store.ts's "split") with its
  // live preview (Phase 20). Two phases share one scratch layer so the cut stays
  // visible from hover straight through to the open create form:
  //
  //  1. Hover phase (editing && geometryTool === "split"): pointermove projects
  //     the cursor perpendicularly onto the line under edit with getClosestPoint
  //     (EPSG:3857, the geometry's own projection), splitCoordinates carves the
  //     two halves at that point, and they're drawn -- near half solid, far half
  //     dashed, plus a marker at the cut. A click within the pixel tolerance
  //     commits: toLonLat brings the point back to the 4326 the API speaks,
  //     startSplitDraft opens a create draft seeded from the original, and edit
  //     mode ends (the far half PostGIS carves out on Save --
  //     api/internal/crud/split.go). The global click-to-select handler already
  //     bails while `editing` is set, so this is the only reaction to the click.
  //  2. Draft phase (draft.split): edit mode is gone and editFeatureRef cleared,
  //     but the create form is open -- keep the committed cut on screen using the
  //     original line's coords cached in splitPreviewCoordsRef during the hover
  //     phase and the frozen split point from the draft.
  //
  // There's no drag interaction either way (the Modify/Translate effect above
  // skips "split"). Depends on editingFeature too, to re-run once that fetch
  // resolves and editFeatureRef is set.
  useEffect(() => {
    const map = mapRef.current
    const splitting = Boolean(editing && geometryTool === "split")
    const draftSplit = draft?.split
    if (!map || (!splitting && !draftSplit)) return

    const source = new VectorSource()
    const layer = new VectorLayer({ source, zIndex: 9 })
    map.addLayer(layer)

    // Draw the two halves + cut marker for a split at `point` (EPSG:3857)
    // against `coords` (the original line, EPSG:3857). A null point clears.
    const render = (coords: number[][] | null, point: number[] | null) => {
      source.clear()
      if (!coords || !point) return
      const [near, far] = splitCoordinates(coords, point)
      const nearFeature = new Feature(new LineString(near))
      nearFeature.setStyle(SPLIT_NEAR_STYLE)
      const farFeature = new Feature(new LineString(far))
      farFeature.setStyle(SPLIT_FAR_STYLE)
      const marker = new Feature(new Point(point))
      marker.setStyle(SPLIT_POINT_STYLE)
      source.addFeatures([nearFeature, farFeature, marker])
    }

    if (splitting) {
      const feature = editFeatureRef.current
      const line = feature?.getGeometry()
      if (!(line instanceof LineString)) {
        map.removeLayer(layer)
        return
      }
      // Cache the original's coords so the preview survives into the draft
      // phase, where editFeatureRef has been cleared.
      const coords = line.getCoordinates()
      splitPreviewCoordsRef.current = coords

      // getClosestPoint always returns *some* point on the line, even for a
      // cursor nowhere near it -- which would preview/split at a surprising
      // spot. Ignore anything more than a small pixel tolerance off the line,
      // so only a point actually on it previews or cuts.
      const project = (evt: MapBrowserEvent<PointerEvent | KeyboardEvent | WheelEvent>) => {
        const onLine = line.getClosestPoint(evt.coordinate)
        const onLinePixel = map.getPixelFromCoordinate(onLine)
        if (onLinePixel && Math.hypot(onLinePixel[0] - evt.pixel[0], onLinePixel[1] - evt.pixel[1]) > 12) return null
        return onLine
      }
      const onMove = (evt: MapBrowserEvent<PointerEvent | KeyboardEvent | WheelEvent>) => {
        render(coords, project(evt))
      }
      const onClick = (evt: MapBrowserEvent<PointerEvent | KeyboardEvent | WheelEvent>) => {
        const onLine = project(evt)
        if (!onLine) return
        const [lng, lat] = toLonLat(onLine)
        useDrawStore.getState().startSplitDraft(
          editing!.featureClassCode,
          editing!.featureId,
          { lng, lat },
          {
            name: editingFeature?.name ?? undefined,
            subtype: editingFeature?.subtype,
            attributes: { ...(editingFeature?.attributes ?? {}) },
          },
        )
        // Leave edit mode -- the create form (seeded above) takes over the
        // panel; the draft-phase branch keeps the cut preview on screen.
        useEditStore.getState().stop()
      }
      map.on("pointermove", onMove)
      map.on("click", onClick)
      return () => {
        map.un("pointermove", onMove)
        map.un("click", onClick)
        map.removeLayer(layer)
      }
    }

    // Draft phase: freeze-frame the committed cut. The original's coords were
    // cached during the hover phase; the split point rides on the draft.
    render(splitPreviewCoordsRef.current, fromLonLat([draftSplit!.splitPoint.lng, draftSplit!.splitPoint.lat]))
    return () => {
      map.removeLayer(layer)
    }
  }, [editing, geometryTool, editingFeature, draft])

  // Click-to-select: hit-test every vector layer at the clicked pixel,
  // resolve each hit's feature class (a layer's sourceId is always a
  // feature class code, lowercased -- see the catalog-driven effect
  // below), and dedupe by (code, id) -- a line feature clipped across
  // an MVT tile boundary can otherwise show up as two hits for the same
  // real feature. Zero hits clears whatever was selected; exactly one
  // hit selects it directly; more than one hands the whole list to the
  // selection store as `candidates` for src/components/map/peer-picker.tsx
  // to disambiguate, rather than guessing which stacked feature the
  // user meant.
  useEffect(() => {
    const map = mapRef.current
    if (!map || !featureClasses) return

    // globalThis.Map, not Map -- this file's `Map` import is
    // ol/Map (the map instance class), which shadows the built-in
    // collection constructor.
    const codeBySourceId = new globalThis.Map(
      featureClasses.feature_classes.map((fc) => [fc.code.toLowerCase(), fc.code]),
    )

    // The deepest territory boundary under a pixel (Phase 11 Slice H),
    // or null when the overlay is off or nothing's there. Deepest wins
    // because the boundaries nest -- a plot sits inside its district
    // inside its region -- so a click always means the most specific area
    // it landed in, not the utility-wide root that also covers it.
    // Filtered to the overlay's own layer so it never hit-tests a feature
    // tile. Reads the endpoint's properties straight off each feature
    // (see the boundary-load effect).
    const territoryAtPixel = (pixel: number[]) => {
      const layer = territoryLayerRef.current
      if (!layer || !layer.getVisible()) return null
      let best: {
        id: string
        parent_id: string | null
        name: string
        level_code: string
        level_name: string
        level_rank: number
      } | null = null
      map.forEachFeatureAtPixel(
        pixel,
        (feature) => {
          const id = feature.get("id") as string | undefined
          if (!id) return undefined
          const rank = (feature.get("level_rank") as number | undefined) ?? 0
          if (!best || rank > best.level_rank) {
            best = {
              id,
              parent_id: (feature.get("parent_id") as string | null | undefined) ?? null,
              name: (feature.get("name") as string | undefined) ?? "",
              level_code: (feature.get("level_code") as string | undefined) ?? "",
              level_name: (feature.get("level_name") as string | undefined) ?? "",
              level_rank: rank,
            }
          }
          return undefined
        },
        { layerFilter: (l) => l === layer },
      )
      return best
    }

    const handleClick = (evt: MapBrowserEvent<PointerEvent | KeyboardEvent | WheelEvent>) => {
      // A click while drawing belongs to the Draw interaction (placing
      // the geometry), and a click while the create form is open for an
      // already-placed draft shouldn't silently change what's selected
      // out from under an in-progress, unsaved form.
      const drawState = useDrawStore.getState()
      if (drawState.drawingFeatureClassCode || drawState.draft) return

      // A click while drawing an annotation (Phase 36 Slice 4) belongs to that
      // Draw interaction, not selection -- same reasoning as the feature-draw
      // guard just above. A click while an annotation geometry gesture is armed
      // (Slice 4b Move/Reshape) belongs to that Translate/Modify and must not
      // reselect or clear out from under the in-progress edit.
      const annotationEdit = useAnnotationEditStore.getState()
      if (annotationEdit.drawingKind || annotationEdit.tool) return

      // A click while the ad-hoc measure tool is armed feeds the measure
      // Draw interaction (see the measure effect above), not selection --
      // same reasoning as the drawing guard just above.
      if (useMeasureStore.getState().mode) return

      // Likewise a click while the box-zoom tool is armed belongs to that
      // tool's rubber-band Draw (see the box-zoom effect above), not selection.
      if (useBoxZoomStore.getState().active) return

      // A click while a feature is under edit (see edit-store.ts,
      // feature-edit-form.tsx) shouldn't silently change the selection
      // out from under an in-progress, unsaved edit either -- same
      // reasoning as the draft guard just above. Escape or the form's
      // own Cancel/Save ends the edit; a click never does.
      if (useEditStore.getState().editing) return

      // A click while a territory boundary is under authoring (Phase 11
      // Slice I) belongs to that workflow's Draw/Modify interaction, not
      // feature selection -- same reasoning as the feature-edit guard
      // above. The toolbar's Cancel/Save or Escape ends it.
      if (useTerritoryEditStore.getState().editing) return

      // A click while connect-store is waiting on a target feature
      // (see connect-store.ts, feature-panel.tsx's "+ New connection",
      // and connect-confirm-panel.tsx) picks that target instead of
      // changing the normal selection. Stacked/overlapping features get
      // the same peer-picker disambiguation normal selection already
      // gets below, reusing selection-store's candidates/showCandidates
      // (peer-picker.tsx branches its own pick action on connect mode
      // being active rather than this needing a second, parallel
      // candidates store) -- picking the wrong one of several stacked
      // features used to be a silent, unrecoverable topmost-hit guess.
      // A click on empty space is still ignored (Escape is the only way
      // to cancel) rather than treated as "clear," so an imprecise click
      // doesn't silently abandon the workflow.
      // A click while a diagram's "Add from map" pick is armed (Phase 12,
      // diagram-place-store.ts) picks that feature for the diagram instead
      // of changing the normal selection -- the map is the palette. Same
      // stacked-feature disambiguation every other click gets: one hit is
      // handed straight to the store's `pick` (the owning canvas then
      // validates + arms it), several land in the peer picker (which
      // routes to `pick` while this phase is active). A click on empty
      // space is ignored, not treated as "clear," so an imprecise click
      // doesn't silently abandon the pick -- Escape is the way out.
      const placeState = useDiagramPlaceStore.getState()
      if (placeState.phase === "picking") {
        const pickHits: { code: string; id: string }[] = []
        const seenPicks = new Set<string>()

        map.forEachFeatureAtPixel(
          evt.pixel,
          (feature, layer) => {
            const sourceId = layer?.get("sourceId") as string | undefined
            const id = feature.get("id") as string | undefined
            const code = sourceId ? codeBySourceId.get(sourceId) : undefined
            if (code && id) {
              const key = `${code}:${id}`
              if (!seenPicks.has(key)) {
                seenPicks.add(key)
                pickHits.push({ code, id })
              }
            }
            return undefined
          },
          { layerFilter: isVectorLayer, hitTolerance: 5 },
        )

        if (pickHits.length === 1) {
          placeState.pick({ featureClassCode: pickHits[0].code, featureId: pickHits[0].id })
        } else if (pickHits.length > 1) {
          const { clientX, clientY } = evt.originalEvent as PointerEvent
          useSelectionStore.getState().showCandidates(
            pickHits.map((hit) => ({ featureClassCode: hit.code, featureId: hit.id })),
            { x: clientX, y: clientY },
            { clearSelected: false },
          )
        }
        return
      }

      const connectState = useConnectStore.getState()
      if (connectState.mode && !connectState.target) {
        const targetHits: { code: string; id: string }[] = []
        const seenTargets = new Set<string>()

        map.forEachFeatureAtPixel(
          evt.pixel,
          (feature, layer) => {
            const sourceId = layer?.get("sourceId") as string | undefined
            const id = feature.get("id") as string | undefined
            const code = sourceId ? codeBySourceId.get(sourceId) : undefined
            if (code && id) {
              const key = `${code}:${id}`
              if (!seenTargets.has(key)) {
                seenTargets.add(key)
                targetHits.push({ code, id })
              }
            }
            return undefined
          },
          { layerFilter: isVectorLayer, hitTolerance: 5 },
        )

        if (targetHits.length === 1) {
          connectState.pickTarget({ featureClassCode: targetHits[0].code, featureId: targetHits[0].id })
        } else if (targetHits.length > 1) {
          const { clientX, clientY } = evt.originalEvent as PointerEvent
          useSelectionStore.getState().showCandidates(
            targetHits.map((hit) => ({ featureClassCode: hit.code, featureId: hit.id })),
            { x: clientX, y: clientY },
            { clearSelected: false },
          )
        }
        return
      }

      // A map annotation (Phase 36 Slice 4) sits at the top of the stack
      // (zIndex 7) and, for a caller who can author them, can be clicked to
      // edit it (annotation-edit-panel.tsx). But a redline -- especially a
      // filled box/area like a "working area" -- must NOT swallow a click that
      // lands on a real feature under it: the network features are the primary
      // content, so they win. We therefore only claim the click for the
      // annotation when NO DB feature sits under the cursor. A large area
      // annotation is still easily selectable via its (mostly feature-free)
      // interior or its stroke; a line beneath it stays selectable, peer
      // picker included. Gated on annotations:write so a viewer never selects
      // an annotation at all. The annotation features carry their row id as
      // the OL feature id (set by the GeoJSON reader in the load effect).
      const annotationLayer = annotationLayerRef.current
      if (canAnnotate && annotationLayer && annotationLayer.getVisible()) {
        let featureUnderCursor = false
        map.forEachFeatureAtPixel(
          evt.pixel,
          (feature, layer) => {
            const sourceId = layer?.get("sourceId") as string | undefined
            const code = sourceId ? codeBySourceId.get(sourceId) : undefined
            if (code && feature.get("id")) {
              featureUnderCursor = true
              return true
            }
            return undefined
          },
          { layerFilter: isVectorLayer, hitTolerance: 5 },
        )

        if (!featureUnderCursor) {
          let annotationHit: string | null = null
          map.forEachFeatureAtPixel(
            evt.pixel,
            (feature) => {
              const id = feature.getId() ?? (feature.get("id") as string | undefined)
              if (id != null) {
                annotationHit = String(id)
                return true
              }
              return undefined
            },
            { layerFilter: (l) => l === annotationLayer, hitTolerance: 5 },
          )
          if (annotationHit) {
            useAnnotationEditStore.getState().select(annotationHit)
            return
          }
        }
      }

      // An imported overlay feature (Phase 15) is a read-only reference layer
      // drawn on top of the DB feature tiles (zIndex 4). A click on one shows
      // its attributes in a lightweight popup (imported-feature-popup.tsx)
      // rather than the DB-coupled FeaturePanel it can't use. Hit-tested on its
      // own layer -- a plain VectorLayer, so it's excluded from the isVectorLayer
      // filter every DB hit-test uses -- and, being the topmost thing under the
      // cursor, it wins the click. The features were tagged with their source
      // overlay's id/name in the population effect; the geometry and those
      // internal tags are stripped so only the file's own attributes show.
      const importedLayer = importedLayerRef.current
      if (importedLayer && importedLayer.getVisible()) {
        let importedHit: Feature | null = null
        map.forEachFeatureAtPixel(
          evt.pixel,
          (feature) => {
            importedHit = feature as Feature
            return true
          },
          { layerFilter: (l) => l === importedLayer, hitTolerance: 5 },
        )
        if (importedHit) {
          const props = { ...importedHit.getProperties() }
          delete props.geometry
          const overlayName = (props.__overlayName as string | undefined) ?? "Imported overlay"
          delete props.__overlayId
          delete props.__overlayName
          const { clientX, clientY } = evt.originalEvent as PointerEvent
          useImportedFeatureStore.getState().show({
            overlayName,
            properties: props,
            position: { x: clientX, y: clientY },
          })
          return
        }
      }
      // Any other click dismisses an open imported-feature popup (the branches
      // below then run their normal DB selection / territory / clear logic).
      useImportedFeatureStore.getState().clear()

      const hits: { code: string; id: string }[] = []
      const seen = new Set<string>()

      map.forEachFeatureAtPixel(
        evt.pixel,
        (feature, layer) => {
          const sourceId = layer?.get("sourceId") as string | undefined
          const id = feature.get("id") as string | undefined
          const code = sourceId ? codeBySourceId.get(sourceId) : undefined
          if (code && id) {
            const key = `${code}:${id}`
            if (!seen.has(key)) {
              seen.add(key)
              hits.push({ code, id })
            }
          }
          // Always continue -- forEachFeatureAtPixel stops as soon as
          // the callback returns truthy, but every feature under the
          // click needs visiting to know whether a peer picker is
          // needed at all.
          return undefined
        },
        { layerFilter: isVectorLayer, hitTolerance: 5 },
      )

      if (hits.length === 0) {
        // No feature under the click. If the boundary overlay is on and a
        // territory sits here, features lost the click to empty space, so
        // this unambiguously means "inspect this area" -- open the
        // read-only territory panel (Phase 11 Slice H). The territories
        // tile the whole map, so this is the only gesture that can select
        // one without stealing clicks from the features drawn on top of
        // them. Otherwise a click on true empty space clears both the
        // feature selection and any open territory panel.
        const territory = territoryAtPixel(evt.pixel)
        if (territory) {
          useTerritoryOverlayStore.getState().selectTerritory(territory)
        } else {
          useSelectionStore.getState().clear()
          useTerritoryOverlayStore.getState().clearSelected()
        }
      } else if (hits.length === 1) {
        useSelectionStore.getState().select(hits[0].code, hits[0].id)
      } else {
        // Click events are always backed by a PointerEvent (never the
        // Keyboard/Wheel members of this handler's broader type), so
        // clientX/clientY are always present here.
        const { clientX, clientY } = evt.originalEvent as PointerEvent
        useSelectionStore.getState().showCandidates(
          hits.map((hit) => ({ featureClassCode: hit.code, featureId: hit.id })),
          { x: clientX, y: clientY },
        )
      }
    }

    map.on("click", handleClick)
    return () => map.un("click", handleClick)
  }, [featureClasses, canAnnotate])

  // One VectorTileLayer per Martin catalog entry -- added/replaced
  // whenever the catalog changes, independent of the base map's own
  // lifecycle above. Also re-runs when symbologyByClassCode or
  // symbologyIcons changes (in practice, once each -- the moment their
  // queries first resolve after mount) so a layer created before that
  // data arrived still ends up styled correctly, rather than being stuck
  // on createSymbologyStyle's plain-geometry fallback (or the hardcoded
  // fallback icon set) for the rest of the session. Martin's source id is
  // always the lowercased feature class
  // code (see api/internal/compiler/compiler.go), reversed here the
  // same way layers-panel.tsx already does to look up a real name.
  useEffect(() => {
    const map = mapRef.current
    if (!map || !catalog) return

    for (const layer of vectorLayersRef.current) map.removeLayer(layer)
    vectorLayersRef.current = []

    for (const sourceId of featureClassSourceIds(catalog)) {
      const classCode = sourceId.toUpperCase()
      const layer = new VectorTileLayer({
        source: new VectorTileSource({
          format: new MVT(),
          url: tileUrlTemplate(sourceId),
        }),
        style: createSymbologyStyle(
          symbologyByClassCode[classCode],
          map.getView(),
          symbologyIcons,
          // Omit the label def when this layer's labels are toggled off, so
          // it's created label-less rather than flashing labels on first
          // paint before the label effect below can strip them.
          labelsHiddenRef.current[sourceId] ? undefined : labelDefsByClassCode[classCode],
          displayZoomByClassCode[classCode],
        ),
        visible: !hiddenLayersRef.current[sourceId],
        properties: { sourceId },
      })
      map.addLayer(layer)
      vectorLayersRef.current.push(layer)
    }
  }, [catalog, symbologyByClassCode, labelDefsByClassCode, displayZoomByClassCode, symbologyIcons])

  // Reacts to the layers store -- src/components/map/layers-panel.tsx
  // -- toggling an existing layer's visibility in place, independent of
  // the catalog-driven effect above that only runs when the set of
  // layers itself changes.
  useEffect(() => {
    for (const layer of vectorLayersRef.current) {
      const sourceId = layer.get("sourceId") as string
      layer.setVisible(!hiddenLayers[sourceId])
    }
  }, [hiddenLayers])

  // Reacts to the per-layer label toggle (layers-panel.tsx) by rebuilding
  // each layer's style in place -- passing the label def through only when
  // that layer's labels are on, so createSymbologyStyle renders (or omits)
  // the label second style accordingly. Kept separate from visibility so a
  // label toggle is a cheap setStyle, not a tile re-request. Re-runs on the
  // same symbology/label/icon data the creation effect depends on so a
  // toggle done before that data resolved still lands the right style; the
  // creation effect (declared above, so it runs first on those shared deps)
  // has already rebuilt the layers by then, this just re-styles them.
  useEffect(() => {
    const map = mapRef.current
    if (!map) return
    for (const layer of vectorLayersRef.current) {
      const sourceId = layer.get("sourceId") as string
      const classCode = sourceId.toUpperCase()
      layer.setStyle(
        createSymbologyStyle(
          symbologyByClassCode[classCode],
          map.getView(),
          symbologyIcons,
          labelsHidden[sourceId] ? undefined : labelDefsByClassCode[classCode],
          displayZoomByClassCode[classCode],
        ),
      )
    }
  }, [labelsHidden, symbologyByClassCode, labelDefsByClassCode, displayZoomByClassCode, symbologyIcons])

  // Populates (or clears) the territory boundary overlay and toggles its
  // visibility (Phase 11 Slice H). ol/format/GeoJSON is used directly
  // here -- unlike geoJSONToGeometry above, it natively parses the
  // MultiPolygon boundaries the endpoint returns, and reprojects them
  // from the payload's EPSG:4326 into the view's EPSG:3857 in one pass.
  // Each parsed feature carries the endpoint's properties (id, name,
  // level_*), which territoryStyleForFeature and the click handler's
  // territoryAtPixel both read. Fetch is gated on `visible` (see
  // useTerritoryBoundaries), so `territoryBoundaries` is undefined until
  // the overlay is first turned on.
  useEffect(() => {
    const source = territorySourceRef.current
    const layer = territoryLayerRef.current
    if (!source || !layer) return

    layer.setVisible(territoryOverlayVisible)
    if (!territoryOverlayVisible) {
      source.clear()
      return
    }
    if (!territoryBoundaries) return

    // Per-level filtering (Phase 11 Slice J): drop the levels the user has
    // hidden before they ever reach the source, so they're gone from both
    // the draw *and* the click hit-test (territoryAtPixel walks this same
    // source) -- no stale features lingering under the cursor.
    const visibleFeatures = territoryBoundaries.features.filter(
      (f) => !territoryHiddenLevels[f.properties.level_code],
    )

    source.clear()
    source.addFeatures(
      new GeoJSON().readFeatures(
        { type: "FeatureCollection", features: visibleFeatures },
        { dataProjection: "EPSG:4326", featureProjection: "EPSG:3857" },
      ),
    )
  }, [territoryOverlayVisible, territoryBoundaries, territoryHiddenLevels])

  // The map-annotation (redlining) overlay (Phase 36) -- repopulate the single
  // annotation layer from the fetched annotations whenever they change or the
  // overlay is toggled. Each annotation's kind/text/style ride along as feature
  // properties so annotationStyleForFeature can render it per-instance; stored
  // geometry is canonical EPSG:4326, reprojected to the view CRS here. Same
  // clear-and-repopulate pattern as the territory overlay above; the fetch is
  // gated on `annotationsVisible`, so `annotationsData` is undefined while off.
  useEffect(() => {
    const source = annotationSourceRef.current
    const layer = annotationLayerRef.current
    if (!source || !layer) return

    // Layer visibility is owned by the zoom-band effect below (it depends on the
    // live zoom too); this effect only manages the source's contents.
    if (!annotationsVisible) {
      source.clear()
      return
    }
    // Hold off repopulating while an in-map geometry gesture is live (Slice 4b):
    // the Modify/Translate interaction is bound to a feature in this very source,
    // and a background refetch landing mid-drag would otherwise clear it out from
    // under the gesture. `annotationTool` is a dependency, so the moment the tool
    // clears (Cancel or a successful Save) this re-runs and redraws with the
    // freshest data, including the saved geometry.
    if (annotationTool) return
    if (!annotationsData) return

    // Build one FeatureCollection so a single readFeatures reprojects the whole
    // set in one pass (same shape the territory effect above uses). Each
    // feature carries its kind/text/style as properties for
    // annotationStyleForFeature to read per-instance.
    const features = annotationsData.annotations.map((a) => ({
      type: "Feature" as const,
      id: a.id,
      geometry: a.geometry,
      properties: { id: a.id, kind: a.kind, text: a.text ?? null, style: a.style ?? {}, z_index: a.z_index },
    }))

    source.clear()
    source.addFeatures(
      new GeoJSON().readFeatures(
        { type: "FeatureCollection", features },
        { dataProjection: "EPSG:4326", featureProjection: "EPSG:3857" },
      ),
    )
  }, [annotationsVisible, annotationsData, annotationTool])

  // Annotation overlay visibility = the store toggle AND the global zoom band
  // (admin Map settings). The single owner of the annotation layer's visibility,
  // split out from the load effect above so it can react to the live zoom
  // without re-clearing/repopulating the source on every pan/zoom. Both band
  // bounds are optional (null = no bound); an unknown zoom (before the first
  // render publishes one) fails open so annotations aren't hidden by a
  // not-yet-known level. Same inclusive min..max semantics as a feature class's
  // Display zoom band.
  useEffect(() => {
    const layer = annotationLayerRef.current
    if (!layer) return
    const withinBand =
      currentZoom === null ||
      ((annMinZoom === null || currentZoom >= annMinZoom) && (annMaxZoom === null || currentZoom <= annMaxZoom))
    layer.setVisible(annotationsVisible && withinBand)
  }, [annotationsVisible, currentZoom, annMinZoom, annMaxZoom])

  // In-map geometry editing of the selected annotation (Phase 36 Slice 4b) --
  // attaches whichever gesture annotation-edit-panel.tsx armed (Move->Translate,
  // Reshape->Modify) to the selected annotation's own feature in the overlay
  // source, and stages the result as EPSG:4326 GeoJSON for the panel's Save. The
  // load effect above holds off repopulating while `tool` is set, so the bound
  // feature survives across multiple drags. Text notes are a single Point with
  // no vertices, so the panel only offers Move for them; Modify would be a no-op
  // anyway.
  useEffect(() => {
    const map = mapRef.current
    const source = annotationSourceRef.current
    if (!map || !source || !annotationSelectedId || !annotationTool) return

    const feature = source.getFeatureById(annotationSelectedId)
    if (!feature) return

    const features = new Collection([feature])
    const handleEnd = () => {
      const geometry = feature.getGeometry()
      const geojson = geometry && geometryToGeoJSON(geometry)
      if (geojson) useAnnotationEditStore.getState().setDirtyGeometry(geojson)
    }

    if (annotationTool === "move") {
      const translate = new Translate({ features })
      translate.on("translateend", handleEnd)
      map.addInteraction(translate)
      return () => {
        map.removeInteraction(translate)
      }
    }

    // Rotate (text notes only): a text note's angle is a STYLE value on a lone
    // Point, not geometry, so there's no OL interaction for it -- we roll a
    // Pointer that spins the note to face wherever the user drags. The angle is
    // the screen-space bearing from the note's anchor pixel to the cursor
    // (atan2 on y-down pixels = clockwise degrees, matching OL Text.rotation),
    // so dragging right = horizontal, and the text tracks the drag direction.
    // We live-write it onto the feature's `style` prop (re-renders through
    // annotationStyleForFeature) and stage it in the store for the panel's Save.
    if (annotationTool === "rotate") {
      const anchor = () => {
        const geom = feature.getGeometry()
        const coord = geom instanceof Point ? geom.getCoordinates() : null
        return coord ? map.getPixelFromCoordinate(coord) : null
      }
      const spinTo = (pixel: number[]) => {
        const a = anchor()
        if (!a) return
        const deg = (Math.atan2(pixel[1] - a[1], pixel[0] - a[0]) * 180) / Math.PI
        const current = (feature.get("style") ?? {}) as Record<string, unknown>
        feature.set("style", { ...current, rotation: deg })
        useAnnotationEditStore.getState().setDirtyRotation(deg)
      }
      const rotate = new PointerInteraction({
        // Claim the gesture (return true) so DragPan doesn't also fire, and spin
        // immediately so a click without a drag still orients the note.
        handleDownEvent: (evt) => {
          spinTo(evt.pixel)
          return true
        },
        handleDragEvent: (evt) => spinTo(evt.pixel),
      })
      map.addInteraction(rotate)
      return () => {
        map.removeInteraction(rotate)
      }
    }

    const modify = new Modify({ features })
    modify.on("modifyend", handleEnd)
    map.addInteraction(modify)
    return () => {
      map.removeInteraction(modify)
    }
  }, [annotationSelectedId, annotationTool])

  // Imported overlays (Phase 15) -- repopulate the single imported layer from
  // the store whenever an overlay is added, removed, toggled, or hydrated from
  // localStorage. Only visible overlays contribute features; each feature is
  // tagged with its overlay id/name so a later slice's click popup can trace it
  // back. Stored geometry is canonical 4326, reprojected to the view CRS here.
  useEffect(() => {
    const source = importedSourceRef.current
    if (!source) return

    source.clear()
    const reader = new GeoJSON()
    for (const overlay of importedOverlays) {
      if (!overlay.visible) continue
      const features = reader.readFeatures(overlay.geojson, {
        dataProjection: "EPSG:4326",
        featureProjection: "EPSG:3857",
      })
      for (const f of features) {
        f.set("__overlayId", overlay.id)
        f.set("__overlayName", overlay.name)
      }
      source.addFeatures(features)
    }
  }, [importedOverlays])

  // Escape cancels an in-progress boundary edit (Phase 11 Slice I) --
  // same affordance as canceling a draw/connect/feature-edit. Discards
  // the whole session (removing the interactions in the effects below
  // drops any half-drawn sketch); the toolbar's Cancel does the same.
  useEffect(() => {
    if (!territoryEditing) return
    const handleEscape = (evt: KeyboardEvent) => {
      if (evt.key === "Escape") useTerritoryEditStore.getState().stop()
    }
    document.addEventListener("keydown", handleEscape)
    return () => document.removeEventListener("keydown", handleEscape)
  }, [territoryEditing])

  // Seeds (or clears) the boundary-authoring source from the territory's
  // current boundary -- once per session (territoryEditSeededForRef), so a
  // boundaries refetch never stomps mid-edit work, same guard as the
  // feature-edit seeding. The geometry comes from the same boundaries
  // collection the overlay uses (fetched now that editing forces it on);
  // a territory with no boundary yet simply isn't in the collection, so
  // it starts from an empty MultiPolygon ready for the Draw tool. Always
  // a MultiPolygon (the column stores one), so appendPolygon has
  // something to grow.
  useEffect(() => {
    const source = territoryEditSourceRef.current
    if (!source) return

    if (!territoryEditing) {
      source.clear()
      territoryEditFeatureRef.current = null
      territoryEditSeededForRef.current = null
      return
    }

    if (territoryEditSeededForRef.current === territoryEditing.id) return
    // Wait for the boundaries query to resolve before deciding "no
    // boundary" -- undefined means still loading, not empty.
    if (!territoryBoundaries) return

    const boundary = territoryBoundaries.features.find((f) => f.properties.id === territoryEditing.id)
    let geometry: MultiPolygon
    if (boundary) {
      const parsed = new GeoJSON().readGeometry(boundary.geometry as object, {
        dataProjection: "EPSG:4326",
        featureProjection: "EPSG:3857",
      })
      geometry =
        parsed instanceof MultiPolygon
          ? parsed
          : parsed instanceof Polygon
            ? new MultiPolygon([parsed.getCoordinates()])
            : new MultiPolygon([])
    } else {
      geometry = new MultiPolygon([])
    }

    source.clear()
    const feature = new Feature(geometry)
    source.addFeature(feature)
    territoryEditFeatureRef.current = feature
    territoryEditSeededForRef.current = territoryEditing.id
  }, [territoryEditing, territoryBoundaries])

  // Empties the boundary in place when Clear is pressed (the store's
  // `cleared` flag) -- the seed effect above is guarded to run once per
  // session, so this is what actually wipes the on-map geometry. Drawing
  // afterward appends to this now-empty MultiPolygon (setDirtyGeometry
  // resets `cleared`), so Clear-then-redraw works without leaving the
  // session.
  useEffect(() => {
    if (!territoryEditCleared) return
    territoryEditFeatureRef.current?.setGeometry(new MultiPolygon([]))
  }, [territoryEditCleared])

  // Attaches the picked boundary-authoring interaction (Phase 11 Slice I)
  // to the one MultiPolygon feature the seed effect produced. "vertices"
  // is a Modify over its existing rings; "draw" is a Draw whose every
  // finished polygon is appended to that same MultiPolygon (no source of
  // its own -- the sketch is discarded, keeping the boundary a single
  // feature). Both convert the result to EPSG:4326 GeoJSON and hand it to
  // the store for the toolbar's Save. Depends on territoryBoundaries too,
  // since territoryEditFeatureRef is only populated once the seed effect
  // ran with loaded data -- the same async-arrives-later race the feature
  // Modify/Translate effect already accounts for.
  useEffect(() => {
    const map = mapRef.current
    const feature = territoryEditFeatureRef.current
    if (!map || !territoryEditing || !feature || !territoryEditTool) return

    const writeDirty = () => {
      const geometry = feature.getGeometry()
      if (!geometry) return
      const geojson = new GeoJSON().writeGeometryObject(geometry, {
        dataProjection: "EPSG:4326",
        featureProjection: "EPSG:3857",
      })
      useTerritoryEditStore.getState().setDirtyGeometry(geojson as TerritoryGeometry)
    }

    if (territoryEditTool === "vertices") {
      const modify = new Modify({ features: new Collection([feature]) })
      modify.on("modifyend", writeDirty)
      map.addInteraction(modify)
      return () => {
        map.removeInteraction(modify)
      }
    }

    const draw = new Draw({ type: "Polygon" })
    draw.on("drawend", (evt: { feature: Feature }) => {
      const poly = evt.feature.getGeometry()
      const multi = feature.getGeometry()
      if (poly instanceof Polygon && multi instanceof MultiPolygon) {
        multi.appendPolygon(poly)
        feature.changed()
        writeDirty()
      }
    })
    map.addInteraction(draw)
    return () => {
      map.removeInteraction(draw)
    }
  }, [territoryEditTool, territoryEditing, territoryBoundaries])

  // Whether the highlight halo is currently waiting on a feature fetch
  // it'll need a geometry from -- e.g. right after clicking a connection/
  // relationship row for a feature that isn't already cached (the
  // clicked-through feature panel's own tab shows its own spinner for
  // the same fetch; the map had nothing, so a halo just silently
  // appeared a beat late with no indication anything was happening).
  // Mirrors the halo effect's own isEditingSelected skip so this never
  // shows for a fetch the halo itself is going to ignore.
  const isEditingSelectedForHalo = editing && selected && editing.featureId === selected.featureId
  const haloLoading = connectMode
    ? connectSourceLoading || connectTargetLoading
    : !isEditingSelectedForHalo && selectedFeatureLoading

  return (
    <>
      {/* Suppress the browser context menu on the canvas -- a right-click
          here has no app action, so the native menu only gets in the way
          (and eats a right-drag). */}
      {/* On the blank basemap there are no tiles to cover the container, so
          the container's own background shows through. Force a clean canvas --
          true white in light mode, near-black in dark mode (otherwise the
          hardcoded white glared through when the app was themed dark). Any
          tiled basemap paints over this anyway. */}
      <div
        ref={containerRef}
        className={`h-full w-full ${basemap === BLANK_BASEMAP ? "bg-white dark:bg-neutral-950" : ""}`}
        onContextMenu={(e) => e.preventDefault()}
      />
      {haloLoading && (
        <div className="text-muted-foreground bg-background/90 pointer-events-none absolute top-3 left-3 z-10 flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs shadow-sm">
          <Loader2 className="size-3.5 animate-spin" />
          Loading feature…
        </div>
      )}
    </>
  )
}
