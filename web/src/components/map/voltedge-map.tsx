import Collection from "ol/Collection"
import { defaults as defaultControls } from "ol/control"
import { isEmpty as isEmptyExtent } from "ol/extent"
import Feature from "ol/Feature"
import MVT from "ol/format/MVT"
import type Geometry from "ol/geom/Geometry"
import LineString from "ol/geom/LineString"
import Point from "ol/geom/Point"
import Polygon from "ol/geom/Polygon"
import Draw from "ol/interaction/Draw"
import Modify from "ol/interaction/Modify"
import Snap from "ol/interaction/Snap"
import Translate from "ol/interaction/Translate"
import type Layer from "ol/layer/Layer"
import TileLayer from "ol/layer/Tile"
import VectorLayer from "ol/layer/Vector"
import VectorTileLayer from "ol/layer/VectorTile"
import Map from "ol/Map"
import type MapBrowserEvent from "ol/MapBrowserEvent"
import { fromLonLat, toLonLat } from "ol/proj"
import { toFeature } from "ol/render/Feature"
import OSM from "ol/source/OSM"
import VectorSource from "ol/source/Vector"
import VectorTileSource from "ol/source/VectorTile"
import TileState from "ol/TileState"
import View from "ol/View"
import { useEffect, useRef } from "react"

import { useCatalog } from "@/hooks/use-catalog"
import { type FeatureGeometry, useFeature } from "@/hooks/use-feature"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import {
  draftStyleForFeature,
  editStyleForFeature,
  type HighlightRole,
  highlightStyleForFeature,
  styleForFeature,
} from "@/lib/map-layers"
import { tileUrlTemplate } from "@/lib/martin"
import { useConnectStore } from "@/stores/connect-store"
import { useDeletedFeaturesStore } from "@/stores/deleted-features-store"
import { type DrawGeometryType, useDrawStore } from "@/stores/draw-store"
import { useEditStore } from "@/stores/edit-store"
import { useLayersStore } from "@/stores/layers-store"
import { useMapPointerStore } from "@/stores/map-pointer-store"
import { useMapRefreshStore } from "@/stores/map-refresh-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useSnapStore } from "@/stores/snap-store"

import "ol/ol.css"

const OL_DRAW_TYPE: Record<DrawGeometryType, "Point" | "LineString" | "Polygon"> = {
  point: "Point",
  line: "LineString",
  polygon: "Polygon",
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

export function VoltEdgeMap() {
  const containerRef = useRef<HTMLDivElement>(null)
  const mapRef = useRef<Map | null>(null)
  const vectorLayersRef = useRef<VectorTileLayer[]>([])
  const draftSourceRef = useRef<VectorSource | null>(null)
  const highlightSourceRef = useRef<VectorSource | null>(null)
  const editSourceRef = useRef<VectorSource | null>(null)
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
  // The last focusToken the pan-to-feature effect below already handled
  // -- see that effect for why it needs this instead of just depending
  // on focusToken alone.
  const fittedTokenRef = useRef(0)
  const { data: catalog } = useCatalog()
  const { data: featureClasses } = useFeatureClasses()
  const drawingFeatureClassCode = useDrawStore((s) => s.drawingFeatureClassCode)
  const drawingGeometryType = useDrawStore((s) => s.drawingGeometryType)
  const draft = useDrawStore((s) => s.draft)
  const connectMode = useConnectStore((s) => s.mode)
  const connectSource = useConnectStore((s) => s.source)
  const connectTarget = useConnectStore((s) => s.target)
  const editing = useEditStore((s) => s.editing)
  const geometryTool = useEditStore((s) => s.geometryTool)
  const deletedIds = useDeletedFeaturesStore((s) => s.ids)
  const snapEnabled = useSnapStore((s) => s.enabled)
  const refreshVersion = useMapRefreshStore((s) => s.version)
  const hiddenLayers = useLayersStore((s) => s.hidden)
  const selected = useSelectionStore((s) => s.selected)
  const focusToken = useSelectionStore((s) => s.focusToken)

  // Whichever feature(s) the highlight layer needs a real geometry for
  // -- the plain-selected feature normally, or the connect/relate
  // workflow's source and target while one is active (see connect-store.ts).
  // TanStack Query dedupes these against the same queries feature-panel.tsx
  // and connect-confirm-panel.tsx already run for the same ids, so this
  // is a cache hit in the common case, not a second real fetch.
  const { data: selectedFeature } = useFeature(selected?.featureClassCode, selected?.featureId)
  const { data: connectSourceFeature } = useFeature(connectSource?.featureClassCode, connectSource?.featureId)
  const { data: connectTargetFeature } = useFeature(connectTarget?.featureClassCode, connectTarget?.featureId)
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

  // Base map + OL Map instance -- created once. attribution: false drops
  // the default Attribution control -- src/components/map/status-bar.tsx
  // renders OSM's required attribution text itself instead, so it
  // doesn't need to also float over the map in its own corner.
  useEffect(() => {
    if (!containerRef.current) return

    const map = new Map({
      target: containerRef.current,
      layers: [new TileLayer({ source: new OSM() })],
      view: new View({ center: DEFAULT_CENTER, zoom: DEFAULT_ZOOM, maxZoom: 22 }),
      controls: defaultControls({ attribution: false }),
    })
    mapRef.current = map

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
      if (useDrawStore.getState().drawingFeatureClassCode || useConnectStore.getState().mode) {
        target.style.cursor = "crosshair"
        return
      }
      const hasFeature = map.hasFeatureAtPixel(evt.pixel, { layerFilter: isVectorLayer, hitTolerance: 5 })
      target.style.cursor = hasFeature ? "pointer" : ""
    })
    map.getTargetElement().addEventListener("mouseleave", clearPosition)

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

    return () => {
      map.getTargetElement()?.removeEventListener("mouseleave", clearPosition)
      map.setTarget(undefined)
      mapRef.current = null
      draftSourceRef.current = null
      highlightSourceRef.current = null
      editSourceRef.current = null
      snapSourceRef.current = null
    }
  }, [])

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

    const draw = new Draw({ source, type: OL_DRAW_TYPE[drawingGeometryType] })
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
    const editingActive = Boolean(editing && geometryTool)
    if (!snapEnabled || (!drawingActive && !editingActive)) return

    refreshSnapSource(map, vectorLayersRef.current, snapSource)
    const snap = new Snap({ source: snapSource })
    map.addInteraction(snap)
    return () => {
      map.removeInteraction(snap)
    }
  }, [snapEnabled, drawingFeatureClassCode, editing, geometryTool])

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

    const handleClick = (evt: MapBrowserEvent<PointerEvent | KeyboardEvent | WheelEvent>) => {
      // A click while drawing belongs to the Draw interaction (placing
      // the geometry), and a click while the create form is open for an
      // already-placed draft shouldn't silently change what's selected
      // out from under an in-progress, unsaved form.
      const drawState = useDrawStore.getState()
      if (drawState.drawingFeatureClassCode || drawState.draft) return

      // A click while a feature is under edit (see edit-store.ts,
      // feature-edit-form.tsx) shouldn't silently change the selection
      // out from under an in-progress, unsaved edit either -- same
      // reasoning as the draft guard just above. Escape or the form's
      // own Cancel/Save ends the edit; a click never does.
      if (useEditStore.getState().editing) return

      // A click while connect-store is waiting on a target feature
      // (see connect-store.ts, feature-panel.tsx's "+ New connection",
      // and connect-confirm-panel.tsx) picks that target instead of
      // changing the normal selection -- only the topmost hit, no peer
      // picker here, to keep this first version's scope tight. A click
      // on empty space is ignored (Escape is the only way to cancel)
      // rather than treated as "clear," so an imprecise click doesn't
      // silently abandon the workflow.
      const connectState = useConnectStore.getState()
      if (connectState.mode && !connectState.target) {
        const hit = map.forEachFeatureAtPixel(
          evt.pixel,
          (feature, layer) => {
            const sourceId = layer?.get("sourceId") as string | undefined
            const id = feature.get("id") as string | undefined
            const code = sourceId ? codeBySourceId.get(sourceId) : undefined
            return code && id ? { code, id } : undefined
          },
          { layerFilter: isVectorLayer, hitTolerance: 5 },
        )
        if (hit) connectState.pickTarget({ featureClassCode: hit.code, featureId: hit.id })
        return
      }

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
        useSelectionStore.getState().clear()
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
  }, [featureClasses])

  // One VectorTileLayer per Martin catalog entry -- added/replaced
  // whenever the catalog changes, independent of the base map's own
  // lifecycle above.
  useEffect(() => {
    const map = mapRef.current
    if (!map || !catalog) return

    for (const layer of vectorLayersRef.current) map.removeLayer(layer)
    vectorLayersRef.current = []

    for (const sourceId of Object.keys(catalog.tiles)) {
      const layer = new VectorTileLayer({
        source: new VectorTileSource({
          format: new MVT(),
          url: tileUrlTemplate(sourceId),
        }),
        style: styleForFeature,
        visible: !hiddenLayersRef.current[sourceId],
        properties: { sourceId },
      })
      map.addLayer(layer)
      vectorLayersRef.current.push(layer)
    }
  }, [catalog])

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

  return <div ref={containerRef} className="h-full w-full" />
}
