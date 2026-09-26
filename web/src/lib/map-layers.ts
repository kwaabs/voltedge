import { Circle, Fill, Icon, RegularShape, Stroke, Style, Text } from "ol/style"
import type { FeatureLike } from "ol/Feature"
import LineString from "ol/geom/LineString"
import Point from "ol/geom/Point"
import type View from "ol/View"

import { lineDashArray, type LineDashStyle } from "@/lib/line-dash"
import { iconDataUri, type IconRegistry } from "@/lib/symbology-icons"
import { resolveSymbology, type DisplayZoomBand, type LabelDef, type SymbologyRuleSchema } from "@/lib/symbology"
import { useDeletedFeaturesStore } from "@/stores/deleted-features-store"
import { useEditStore } from "@/stores/edit-store"

/**
 * Canvas-rendered styles can't consume the app's CSS custom properties
 * (see src/index.css) -- OpenLayers paints to a <canvas>, not the DOM,
 * so these are separate, hand-picked constants that echo the theme's
 * primary/chart tokens rather than reference them. Point = copper
 * (echoes --primary), line = teal (echoes --chart-2), polygon = slate
 * (a neutral -- no per-feature-class color modeling exists yet).
 */
const POINT_COLOR = "#d9752e"
const LINE_COLOR = "#3f8fa6"
const POLYGON_COLOR = "#64748b"
/** Emerald -- distinct from every committed-feature color above, so an
 * unsaved draft feature never reads as just another real one. */
const DRAFT_COLOR = "#10b981"
/** Cyan -- distinct from every other role color in this file (point/
 * line/polygon, draft, and every highlight role), so a feature actively
 * under geometry edit (geometry-edit-toolbar.tsx, voltedge-map.tsx's
 * Modify/Translate interactions) never reads as merely selected or a
 * fresh draft. Vertex handles themselves are Modify's own overlay, not
 * drawn here -- this only needs to make the geometry itself distinct. */
const EDIT_COLOR = "#06b6d4"

/** An empty Style renders nothing -- see deleted-features-store.ts for
 * why styleForFeature needs this at all: Martin has no idea a feature
 * was soft-deleted, so its tiles keep the feature's bytes; this is what
 * makes it actually disappear from the map anyway. */
const hiddenStyle = new Style({})

const pointStyle = new Style({
  image: new Circle({
    radius: 5,
    fill: new Fill({ color: POINT_COLOR }),
    stroke: new Stroke({ color: "#ffffff", width: 1.5 }),
  }),
})

const lineStyle = new Style({
  stroke: new Stroke({ color: LINE_COLOR, width: 3 }),
})

const polygonStyle = new Style({
  fill: new Fill({ color: `${POLYGON_COLOR}33` }),
  stroke: new Stroke({ color: POLYGON_COLOR, width: 2 }),
})

const draftPointStyle = new Style({
  image: new Circle({
    radius: 6,
    fill: new Fill({ color: `${DRAFT_COLOR}80` }),
    stroke: new Stroke({ color: DRAFT_COLOR, width: 2, lineDash: [4, 3] }),
  }),
})

const draftLineStyle = new Style({
  stroke: new Stroke({ color: DRAFT_COLOR, width: 3, lineDash: [6, 4] }),
})

const draftPolygonStyle = new Style({
  fill: new Fill({ color: `${DRAFT_COLOR}26` }),
  stroke: new Stroke({ color: DRAFT_COLOR, width: 2, lineDash: [6, 4] }),
})

const editPointStyle = new Style({
  image: new Circle({
    radius: 6,
    fill: new Fill({ color: `${EDIT_COLOR}80` }),
    stroke: new Stroke({ color: EDIT_COLOR, width: 2 }),
  }),
})

const editLineStyle = new Style({
  stroke: new Stroke({ color: EDIT_COLOR, width: 3 }),
})

const editPolygonStyle = new Style({
  fill: new Fill({ color: `${EDIT_COLOR}26` }),
  stroke: new Stroke({ color: EDIT_COLOR, width: 2 }),
})

/** Amber -- distinct from every role above (point copper, line teal,
 * polygon slate, draft emerald, edit cyan, and the territory blues/
 * violets/magenta). Imported-overlay features (Phase 15) are also drawn
 * *dashed* so a foreign reference layer never reads as native, saved data
 * regardless of hue. */
const IMPORTED_COLOR = "#f59e0b"

const importedPointStyle = new Style({
  image: new Circle({
    radius: 5,
    fill: new Fill({ color: `${IMPORTED_COLOR}80` }),
    stroke: new Stroke({ color: IMPORTED_COLOR, width: 2, lineDash: [4, 3] }),
  }),
})

const importedLineStyle = new Style({
  stroke: new Stroke({ color: IMPORTED_COLOR, width: 2.5, lineDash: [6, 4] }),
})

const importedPolygonStyle = new Style({
  fill: new Fill({ color: `${IMPORTED_COLOR}26` }),
  stroke: new Stroke({ color: IMPORTED_COLOR, width: 2, lineDash: [6, 4] }),
})

/**
 * The imported-overlay layer's style function (Phase 15 data import) --
 * an amber, dashed reference style branched by geometry type, so external
 * data the user drops on the map (GeoJSON/KMZ/Shapefile/DXF) reads clearly
 * as a foreign overlay and never as a native, saved feature. Read-only:
 * these features are never selection/edit targets.
 */
export function importedStyleForFeature(feature: FeatureLike): Style {
  switch (feature.getGeometry()?.getType()) {
    case "Point":
    case "MultiPoint":
      return importedPointStyle
    case "LineString":
    case "MultiLineString":
      return importedLineStyle
    default:
      return importedPolygonStyle
  }
}

/** The one feature currently under geometry edit -- see EDIT_COLOR. */
export function editStyleForFeature(feature: FeatureLike): Style {
  switch (feature.getGeometry()?.getType()) {
    case "Point":
    case "MultiPoint":
      return editPointStyle
    case "LineString":
    case "MultiLineString":
      return editLineStyle
    default:
      return editPolygonStyle
  }
}

/** The roles voltedge-map.tsx's highlight layer ever draws: the single
 * feature currently shown in the floating panel; while a connectivity/
 * relationship workflow is active (connect-store.ts), the source and
 * target ends of it; while a trace result is active (trace-store.ts),
 * every feature the trace reached ("trace"), distinctly whichever of
 * those a live barrier condition actually stopped it at
 * ("trace-barrier"), and distinctly again whichever matched a
 * trace_configurations target_class ("trace-target", e.g. the
 * transformer a "trace to transformer" run found); while an isolation
 * result is active (isolate-store.ts), everything that would
 * de-energize ("isolation-deenergized"), the upstream protective
 * boundary device ("isolation-boundary"), and any possible backfeed
 * path ("isolation-backfeed"); or, while a load-flow result is active
 * (load-flow-store.ts), every node the sweep reached
 * ("load-flow"), distinctly whichever conductor exceeded its own
 * ampacity ("load-flow-overload"), and distinctly again whichever node
 * the walk actually stopped at because of a live open device
 * ("load-flow-barrier"); or, while a fault-current result is active
 * (fault-current-store.ts), every node the walk reached
 * ("fault-current") and the open device it stopped at
 * ("fault-current-barrier"); or, while a query-builder result is active
 * (query-store.ts), every feature the structured query matched
 * ("query-result") -- each role in its own color so none are
 * ever confused with each other or with a plain selection. */
export type HighlightRole =
  | "selected"
  | "connect-source"
  | "connect-target"
  | "trace"
  | "trace-barrier"
  | "trace-target"
  | "isolation-deenergized"
  | "isolation-boundary"
  | "isolation-backfeed"
  | "load-flow"
  | "load-flow-overload"
  | "load-flow-barrier"
  | "fault-current"
  | "fault-current-barrier"
  | "query-result"
  | "outage-affected"

/** Exported so diagram-canvas.tsx can highlight a selected/connect-role
 * placement with the exact same colors the map's own highlight layer
 * uses, instead of inventing a second palette for the same concepts. */
export const HIGHLIGHT_COLORS: Record<HighlightRole, string> = {
  selected: "#38bdf8",
  "connect-source": "#f59e0b",
  "connect-target": "#a855f7",
  trace: "#84cc16",
  "trace-barrier": "#ef4444",
  "trace-target": "#eab308",
  "isolation-deenergized": "#f97316",
  "isolation-boundary": "#8b5cf6",
  "isolation-backfeed": "#dc2626",
  "load-flow": "#06b6d4",
  "load-flow-overload": "#dc2626",
  "load-flow-barrier": "#f97316",
  "fault-current": "#d946ef",
  "fault-current-barrier": "#f97316",
  // Query-builder matches (docs/phase-13-query-builder.md). Pink -- a hue
  // no other highlight role uses, so a query result set never reads as a
  // trace, isolation, or selection halo.
  "query-result": "#ec4899",
  // The service points in a selected OMS incident's outage cone (Phase 49
  // map overlay). Rose -- distinct from the orange isolation/load-flow
  // roles and the pink query-result -- so an affected-customer set reads as
  // its own thing, not a trace or isolation.
  "outage-affected": "#e11d48",
}

function highlightStyleFor(color: string, geometryType: string | undefined): Style {
  switch (geometryType) {
    case "Point":
    case "MultiPoint":
      return new Style({
        image: new Circle({
          radius: 12,
          fill: new Fill({ color: `${color}26` }),
          stroke: new Stroke({ color, width: 2.5 }),
        }),
      })
    case "LineString":
    case "MultiLineString":
      // Wide and translucent (the highlight layer renders above the
      // real feature -- see voltedge-map.tsx -- so an opaque stroke here
      // would hide the line's own real color underneath it) so it reads
      // as a glow rather than replacing the line's real styling.
      return new Style({ stroke: new Stroke({ color: `${color}66`, width: 9, lineCap: "round" }) })
    default:
      return new Style({
        fill: new Fill({ color: `${color}1f` }),
        stroke: new Stroke({ color, width: 3 }),
      })
  }
}

/**
 * The highlight layer's style function -- reads which role a given
 * highlight feature was tagged with (voltedge-map.tsx sets a `role`
 * property when it builds each one from a fetched feature's real
 * geometry) rather than taking the role as a style-function argument,
 * since OL always calls a layer's style function with the feature
 * alone.
 */
export function highlightStyleForFeature(feature: FeatureLike): Style {
  const role = (feature.get("role") as HighlightRole | undefined) ?? "selected"
  return highlightStyleFor(HIGHLIGHT_COLORS[role], feature.getGeometry()?.getType())
}

/**
 * The trace layer's own style function -- identical to highlightStyleForFeature
 * except a traced LINE gets a wider, softer halo (14px @ 0x40 alpha vs the shared
 * 9px @ 0x66) so it reads as a diffuse glow BEHIND the animated marching ants
 * (see voltedge-map.tsx's trace postrender effect) rather than as a second solid
 * dashed line competing with them. Points/polygons and every other highlight layer
 * are left on the shared style -- only the trace layer points here.
 */
export function traceHighlightStyleForFeature(feature: FeatureLike): Style {
  const role = (feature.get("role") as HighlightRole | undefined) ?? "trace"
  const color = HIGHLIGHT_COLORS[role]
  const geometryType = feature.getGeometry()?.getType()
  if (geometryType === "LineString" || geometryType === "MultiLineString") {
    return new Style({ stroke: new Stroke({ color: `${color}40`, width: 14, lineCap: "round" }) })
  }
  return highlightStyleFor(color, geometryType)
}

/** Fault-marker colors, keyed by fault status (Faults Slice 3 map overlay).
 * Color is the ONE visual variable the marker carries -- it reads status at a
 * glance (hot red = live/unattended down to muted grey = closed), leaving fault
 * TYPE for the popup rather than a second color axis. Hand-picked canvas
 * constants (OpenLayers paints to <canvas>, so CSS tokens are unavailable), and
 * distinct from the highlight-role palette above so a fault pin never reads as a
 * trace or an affected-customer halo. */
export const FAULT_STATUS_COLORS: Record<string, string> = {
  detected: "#dc2626", // red-600  -- live, not yet being worked
  investigating: "#f59e0b", // amber-500 -- being worked
  cleared: "#2563eb", // blue-600 -- off the network
  closed: "#6b7280", // gray-500 -- wrapped up
}

const FAULT_FALLBACK_COLOR = "#dc2626"

/** Pixel displacement of a point fault's triangle marker (and its pulse ring) --
 * up and to the RIGHT of the anchoring point, so the marker doesn't sit on top of
 * and hide the feature's own symbol. OL `displacement` is [right, up] in pixels.
 * Only point faults are offset; a line/area fault has no marker. */
export const FAULT_MARKER_DISPLACEMENT: [number, number] = [12, 12]

/** A #rrggbb hex color as an rgba() string at the given opacity -- for the
 * translucent polygon fill of an area-asset fault highlight. */
function faultFillAlpha(hex: string, alpha: number): string {
  const h = hex.replace("#", "")
  const r = parseInt(h.slice(0, 2), 16)
  const g = parseInt(h.slice(2, 4), 16)
  const b = parseInt(h.slice(4, 6), 16)
  return `rgba(${r}, ${g}, ${b}, ${alpha})`
}

/** A fault status's color at the given opacity -- used by the map's "radar ping"
 * pulse (voltedge-map.tsx) to fade an expanding ring out as it grows. */
export function faultStatusColorRgba(status: string, alpha: number): string {
  return faultFillAlpha(FAULT_STATUS_COLORS[status] ?? FAULT_FALLBACK_COLOR, alpha)
}

/** The fault overlay's style function, driven by the feature's geometry so a
 * fault reads correctly whatever it is anchored to:
 *
 *   - a POINT (a point asset, or a location-only pin) -> a status-colored warning
 *     triangle, distinct from the round selection/affected halos and real symbols;
 *   - a LINE (a conductor / cable span) -> the whole faulted run highlighted in
 *     the status color over a white casing, so it stands out from the network
 *     lines beneath it -- a centroid triangle would just float mid-span and, on a
 *     curved or multipart line, off the line entirely;
 *   - a POLYGON (an area asset, e.g. a substation footprint) -> a status-colored
 *     outline and translucent fill.
 *
 * voltedge-map.tsx tags every fault feature with a `faultStatus` property when it
 * builds the overlay from /faults/map (and, for a line/polygon fault, adds a
 * separate on-geometry point marker so the fault stays clickable and legible when
 * zoomed out). OL calls the style function with the feature alone, so the status
 * is read off the feature rather than passed in. */
export function faultStyleForFeature(feature: FeatureLike): Style | Style[] {
  const status = (feature.get("faultStatus") as string | undefined) ?? "detected"
  const color = FAULT_STATUS_COLORS[status] ?? FAULT_FALLBACK_COLOR
  const type = feature.getGeometry()?.getType()

  if (type === "LineString" || type === "MultiLineString") {
    return [
      new Style({ stroke: new Stroke({ color: "#ffffff", width: 7 }) }),
      new Style({ stroke: new Stroke({ color, width: 4 }) }),
    ]
  }

  if (type === "Polygon" || type === "MultiPolygon") {
    return new Style({
      fill: new Fill({ color: faultFillAlpha(color, 0.25) }),
      stroke: new Stroke({ color, width: 3 }),
    })
  }

  return new Style({
    image: new RegularShape({
      points: 3,
      radius: 9,
      displacement: FAULT_MARKER_DISPLACEMENT,
      fill: new Fill({ color }),
      stroke: new Stroke({ color: "#ffffff", width: 1.5 }),
    }),
  })
}

/** Incident-marker colors, keyed by OMS incident status (outage map overlay --
 * the twin of FAULT_STATUS_COLORS). Color is the ONE visual variable the marker
 * carries: it reads the incident's lifecycle at a glance (amber = inferred,
 * through red/orange = live & being worked, to green/grey = done), leaving
 * outage TYPE and cause for the click-through. Hand-picked canvas constants
 * (OpenLayers paints to <canvas>, so CSS tokens are unavailable). An incident
 * marker is drawn as a DIAMOND (see incidentStyleForFeature), a shape distinct
 * from the fault triangle and the round selection/affected halos, so the two
 * overlays never read as each other even where their hues overlap. */
export const INCIDENT_STATUS_COLORS: Record<string, string> = {
  predicted: "#f59e0b", // amber-500 -- inferred from calls, not yet confirmed
  scheduled: "#3b82f6", // blue-500  -- a planned outage, booked for the future
  confirmed: "#dc2626", // red-600   -- confirmed live outage
  dispatched: "#ea580c", // orange-600 -- crew dispatched, still out
  restored: "#16a34a", // green-600 -- supply back
  cancelled: "#6b7280", // gray-500  -- stood down
}

const INCIDENT_FALLBACK_COLOR = "#dc2626"

/** Pixel displacement of an incident's diamond marker (and its pulse ring) -- up
 * and to the RIGHT of the anchoring device point, so the marker doesn't sit on
 * top of and hide the device's own symbol. OL `displacement` is [right, up]. */
export const INCIDENT_MARKER_DISPLACEMENT: [number, number] = [12, 12]

/** An incident status's color at the given opacity -- used by the map's "radar
 * ping" pulse (voltedge-map.tsx) to fade an expanding ring out as it grows.
 * Reuses the fault block's faultFillAlpha (same #rrggbb -> rgba() conversion). */
export function incidentStatusColorRgba(status: string, alpha: number): string {
  return faultFillAlpha(INCIDENT_STATUS_COLORS[status] ?? INCIDENT_FALLBACK_COLOR, alpha)
}

/** The outage overlay's style function, driven by the feature's geometry so an
 * incident reads correctly whatever its predicted device is anchored to:
 *
 *   - a POINT device (e.g. a switch / recloser) -> a status-colored DIAMOND,
 *     distinct from the fault triangle and the round selection/affected halos;
 *   - a LINE device (e.g. a sectionalized conductor) -> the whole run highlighted
 *     in the status color over a white casing, so it stands out from the network
 *     lines beneath it;
 *   - a POLYGON device (an area asset) -> a status-colored outline + translucent fill.
 *
 * voltedge-map.tsx tags every incident feature with an `incidentStatus` property
 * when it builds the overlay from /oms/incidents/map (and, for a line/polygon
 * device, adds a separate on-geometry diamond marker so the incident stays
 * clickable and legible when zoomed out). OL calls the style function with the
 * feature alone, so the status is read off the feature rather than passed in. */
export function incidentStyleForFeature(feature: FeatureLike): Style | Style[] {
  const status = (feature.get("incidentStatus") as string | undefined) ?? "confirmed"
  const color = INCIDENT_STATUS_COLORS[status] ?? INCIDENT_FALLBACK_COLOR
  const type = feature.getGeometry()?.getType()

  if (type === "LineString" || type === "MultiLineString") {
    return [
      new Style({ stroke: new Stroke({ color: "#ffffff", width: 7 }) }),
      new Style({ stroke: new Stroke({ color, width: 4 }) }),
    ]
  }

  if (type === "Polygon" || type === "MultiPolygon") {
    return new Style({
      fill: new Fill({ color: faultFillAlpha(color, 0.25) }),
      stroke: new Stroke({ color, width: 3 }),
    })
  }

  // A diamond (square rotated 45 degrees) -- deliberately NOT the fault triangle,
  // so an outage marker and a fault marker never read as the same thing.
  return new Style({
    image: new RegularShape({
      points: 4,
      radius: 8,
      angle: Math.PI / 4,
      displacement: INCIDENT_MARKER_DISPLACEMENT,
      fill: new Fill({ color }),
      stroke: new Stroke({ color: "#ffffff", width: 1.5 }),
    }),
  })
}

/** Shared by styleForFeature and createSymbologyStyle -- a feature that's
 * soft-deleted (client-side hidden, see deleted-features-store.ts) or
 * currently under geometry edit (the cyan edit-geometry layer stands in
 * for it instead, see editStyleForFeature) never gets styled by either
 * one; both need this exact same check, so it's factored out instead of
 * duplicated. */
function isHiddenFeature(feature: FeatureLike): boolean {
  const id = feature.get("id") as string | undefined
  if (!id) return false
  if (useDeletedFeaturesStore.getState().ids.has(id)) return true
  if (useEditStore.getState().editing?.featureId === id) return true
  return false
}

/**
 * Geometry-type-only styling, no symbology_rules involved -- the
 * fallback createSymbologyStyle itself uses when a feature class has no
 * rules loaded at all (not yet seeded, or its schema hasn't finished
 * fetching), so a layer never renders invisible just because symbology
 * resolution came up empty.
 */
export function styleForFeature(feature: FeatureLike): Style {
  if (isHiddenFeature(feature)) return hiddenStyle

  switch (feature.getGeometry()?.getType()) {
    case "Point":
    case "MultiPoint":
      return pointStyle
    case "LineString":
    case "MultiLineString":
      return lineStyle
    default:
      return polygonStyle
  }
}

/** Icons are authored at a 24x24 viewBox (symbology-icons.ts) -- scaled
 * down so a resolved icon reads at roughly the same on-screen size the
 * plain pointStyle circle above already used, not a raw 24px blob. */
const ICON_BASE_SCALE = 0.65

/** Every distinct (icon_ref, color, scale) combination this session
 * has resolved, reused across every call rather than constructing a
 * fresh `Icon`/`Style` per render. This isn't just an optimization --
 * an OL `Icon`'s image loads asynchronously, and a vector tile layer's
 * style function is called on every render pass; a brand-new `Icon`
 * (and thus a brand-new, not-yet-loaded `Image`) built inline on every
 * single call means the load effectively never finishes before the
 * next render discards it and starts over, so the icon never actually
 * draws. Line/polygon styles below don't need this (Stroke/Fill have
 * no async image involved), which is exactly why only point features
 * were affected the one time this shipped without the cache. */
const iconStyleCache = new Map<string, Style>()

function iconStyleFor(iconRef: string | undefined, color: string, scale: number, icons?: IconRegistry): Style {
  // Key on the resolved data URI, not the bare icon_ref -- the URI encodes
  // the actual SVG markup, so a layer that built with the hardcoded
  // fallback before the DB icon set landed doesn't get a stale cached
  // style handed back once the real markup arrives (the same ref now
  // resolves to a different URI, hence a different key). scale isn't in the
  // URI, so it's appended.
  const src = iconDataUri(iconRef, color, icons)
  const key = `${src}|${scale}`
  const cached = iconStyleCache.get(key)
  if (cached) return cached
  const style = new Style({ image: new Icon({ src, scale }) })
  iconStyleCache.set(key, style)
  return style
}

/** Default text styling for a configured label, when the rule leaves a
 * knob unset. Dark ink on a white halo (the same legibility trick the
 * territory overlay uses) so a name reads over any basemap. */
const LABEL_COLOR = "#1f2937"
const LABEL_SIZE = 12
const LABEL_HALO_COLOR = "#ffffff"
const LABEL_HALO_WIDTH = 3

/** Reused per resolved appearance rather than rebuilt every render pass --
 * the layer style fn runs on every pass and per feature, same as the icon
 * cache above. Text has no async image to load, so the only per-feature
 * bit (the string) is set with setText on the shared style before it's
 * returned, exactly the territory overlay's pattern below. Keyed to
 * include every appearance input (font, halo, placement) because a line
 * labels ALONG itself while a point/polygon labels at a single anchor. */
const labelStyleCache = new Map<string, Style>()

function labelStyleFor(def: LabelDef, placement: "point" | "line", pointOffsetY: number): Style {
  const size = def.size ?? LABEL_SIZE
  const color = def.color ?? LABEL_COLOR
  const bold = def.bold === true
  const italic = def.italic === true
  // Halo defaults ON -- text over an arbitrary basemap needs it to stay
  // readable; an author can turn it off explicitly.
  const halo = def.halo !== false
  const haloColor = def.halo_color ?? LABEL_HALO_COLOR
  const haloWidth = def.halo_width ?? LABEL_HALO_WIDTH
  // Class-wide placement (point/polygon only): the authored offset shifts the
  // label from its anchor, offset_y overriding the default drop below the glyph;
  // rotation spins the text. A line label rides the line, where OL ignores
  // offset and rotation would fight the placement, so all three collapse to 0.
  const isPoint = placement === "point"
  const offsetX = isPoint ? (def.offset_x ?? 0) : 0
  const offsetY = isPoint ? (def.offset_y ?? pointOffsetY) : 0
  const rotation = isPoint ? ((def.rotation ?? 0) * Math.PI) / 180 : 0
  const key = `${size}|${color}|${bold}|${italic}|${halo}|${haloColor}|${haloWidth}|${placement}|${offsetX}|${offsetY}|${rotation}`
  const cached = labelStyleCache.get(key)
  if (cached) return cached
  // CSS font shorthand order: style (italic) then weight (bold) then size.
  const font = `${italic ? "italic " : ""}${bold ? "bold " : ""}${size}px ui-sans-serif, system-ui, sans-serif`
  const style = new Style({
    text: new Text({
      font,
      placement,
      rotation,
      fill: new Fill({ color }),
      stroke: halo ? new Stroke({ color: haloColor, width: haloWidth }) : undefined,
      // A point/polygon name sits below the glyph by default (offset_y can
      // move it); a line name rides the line itself (offset meaningless with
      // placement:"line", so both offsets are 0 there).
      offsetX,
      offsetY,
      textBaseline: isPoint ? "top" : "middle",
      overflow: true,
    }),
  })
  labelStyleCache.set(key, style)
  return style
}

/** Resolves a label template against one feature's tile properties, or ""
 * to draw nothing. Each {field} placeholder is replaced with that
 * property coerced to a trimmed string (absent/null -> "" so a missing
 * attribute collapses out rather than printing "null"); literal text
 * between placeholders is kept verbatim. Runs of whitespace left by a
 * dropped field are collapsed and the result trimmed, so a template like
 * "{name} ({voltage} kV)" with no voltage reads "Feeder (kV)" rather than
 * "Feeder (  kV)", and a template whose every field is empty yields ""
 * (no label at all). */
function renderLabelTemplate(template: string, props: Record<string, unknown>): string {
  const filled = template.replace(/\{([^}]+)\}/g, (_, field: string) => {
    const raw = props[field.trim()]
    if (raw === null || raw === undefined) return ""
    return String(raw).trim()
  })
  return filled.replace(/\s+/g, " ").trim()
}

/**
 * Builds one layer's style function, bound to that feature class's own
 * resolved symbology_rules (see symbology.ts's resolveSymbology and
 * docs/metadata-core.md's "Symbology resolution" section) -- each
 * Martin catalog entry is already scoped to exactly one feature class,
 * so voltedge-map.tsx creates one of these per VectorTileLayer at
 * layer-creation time rather than resolving rules generically per
 * feature. Falls back to styleForFeature's plain geometry-type styling
 * whenever no rule resolves at all (rules is empty/undefined, the
 * feature's own subtype+attributes don't match even the mandatory
 * class-level default, or every matching rule is zoom-gated out at the
 * current level -- a metadata-authoring gap or a deliberate zoom
 * restriction, not something worth hiding the feature entirely over).
 *
 * view is the map's own View -- an OL StyleFunction is called with
 * (feature, resolution) on every render, and resolution only means
 * something as a zoom level once converted through the same View
 * that's actually rendering it (getZoomForResolution respects whatever
 * projection/resolutions config the view uses, unlike a hardcoded
 * EPSG:3857 formula would).
 */
export function createSymbologyStyle(
  rules: SymbologyRuleSchema[] | undefined,
  view: View,
  icons?: IconRegistry,
  labelDef?: LabelDef,
  displayZoom?: DisplayZoomBand,
) {
  return function symbologyStyleForFeature(feature: FeatureLike, resolution: number): Style | Style[] {
    if (isHiddenFeature(feature)) return hiddenStyle

    const geometryType = feature.getGeometry()?.getType()
    const props = feature.getProperties() as Record<string, unknown>
    const subtype = typeof props.subtype === "number" ? props.subtype : undefined
    const zoom = view.getZoomForResolution(resolution)

    // Per-class display-zoom band (migration 0090): a true visibility gate --
    // outside the class's declared band the whole feature (glyph AND label)
    // draws nothing, so it's also not hittable for click/select on the tile.
    // Unlike a symbology rule's own min_zoom/max_zoom (which only pick which
    // style applies, never hide), this returns before geometry or label is
    // built. undefined band or unresolvable zoom = no gate (draw as normal).
    if (displayZoom && zoom !== undefined) {
      if (displayZoom.min !== undefined && zoom < displayZoom.min) return hiddenStyle
      if (displayZoom.max !== undefined && zoom > displayZoom.max) return hiddenStyle
    }

    const def = resolveSymbology(rules, subtype, props, "map", zoom ?? undefined)

    // A line labels ALONG itself; a point/polygon labels at a single
    // anchor just below the glyph. Derived from geometry alone so it holds
    // whether or not a symbology rule resolved.
    const isLine = geometryType === "LineString" || geometryType === "MultiLineString"
    const labelPlacement: "point" | "line" = isLine ? "line" : "point"
    const pointOffsetY = geometryType === "Point" || geometryType === "MultiPoint" ? 14 : 0

    // The geometry appearance: the resolved symbology rule if one matched,
    // else the plain geometry-type fallback. Labelling is orthogonal, so
    // even an unstyled feature (no matching rule) can still carry a label.
    let geometryStyle: Style
    if (!def) {
      geometryStyle = styleForFeature(feature)
    } else {
      switch (geometryType) {
        case "Point":
        case "MultiPoint":
          geometryStyle = iconStyleFor(def.icon_ref, def.color ?? POINT_COLOR, ICON_BASE_SCALE * (def.scale ?? 1), icons)
          break
        case "LineString":
        case "MultiLineString": {
          const lineDash = lineDashArray(def.dash)
          geometryStyle = new Style({
            stroke: new Stroke({
              color: def.color ?? LINE_COLOR,
              width: def.width ?? 3,
              lineDash,
              // A round cap turns the dotted pattern's near-zero dashes into
              // real dots; harmless on solid/dashed (no lineDash or long
              // dashes), so only forced on for dotted.
              lineCap: def.dash === "dotted" ? "round" : undefined,
            }),
          })
          break
        }
        default: {
          const color = def.fill_color ?? def.color ?? POLYGON_COLOR
          geometryStyle = new Style({
            fill: new Fill({ color: `${color}33` }),
            stroke: new Stroke({ color: def.stroke_color ?? color, width: def.width ?? 2 }),
          })
        }
      }
    }

    // Labels (Phase 30): the class-wide label_def (not the per-feature
    // symbology rule) drives text -- only when it has a template, isn't
    // zoom-gated out (min_zoom is the declutter lever), and the template
    // resolves to non-empty text for this feature. Returned as a SECOND
    // style so the shared/cached geometry style is never mutated per feature.
    if (labelDef?.template && (labelDef.min_zoom === undefined || (zoom ?? Infinity) >= labelDef.min_zoom)) {
      const text = renderLabelTemplate(labelDef.template, props)
      if (text) {
        const labelStyle = labelStyleFor(labelDef, labelPlacement, pointOffsetY)
        labelStyle.getText()?.setText(text)
        return [geometryStyle, labelStyle]
      }
    }
    return geometryStyle
  }
}

/**
 * The territory boundary overlay's palette, indexed by level rank (0 =
 * utility, deepest = the min-feature level). Canvas-rendered like every
 * other constant in this file (see POINT_COLOR's note), so hand-picked
 * hues distinct from the feature/highlight/draft/edit roles above: a
 * cool blue -> violet -> magenta ramp that reads as "administrative
 * area" rather than "a feature you can select". Ranks past the ramp
 * reuse the last (deepest) color rather than going undefined.
 */
const TERRITORY_COLORS = ["#2563eb", "#7c3aed", "#c026d3", "#db2777"]

export function territoryColorForRank(rank: number): string {
  if (rank < 0) return TERRITORY_COLORS[0]
  return TERRITORY_COLORS[Math.min(rank, TERRITORY_COLORS.length - 1)]
}

/** Reused across every territory feature at the same rank rather than
 * built per render pass (the overlay layer's style fn is called on every
 * pass, like the feature layers') -- keyed by rank, the only thing that
 * varies the stroke/fill. The name label's Text does vary per feature,
 * so it's cloned on top rather than cached. */
const territoryBaseStyleByRank = new Map<number, Style>()

function territoryBaseStyle(rank: number): Style {
  const cached = territoryBaseStyleByRank.get(rank)
  if (cached) return cached
  const color = territoryColorForRank(rank)
  const style = new Style({
    fill: new Fill({ color: `${color}1a` }),
    stroke: new Stroke({ color, width: 2 }),
    // Deeper levels label smaller -- a region's name shouldn't shout as
    // loud as its many child areas', and stacked overlays get busy fast.
    text: new Text({
      font: `${Math.max(11, 15 - rank)}px ui-sans-serif, system-ui, sans-serif`,
      fill: new Fill({ color }),
      stroke: new Stroke({ color: "#ffffff", width: 3 }),
      overflow: true,
    }),
  })
  territoryBaseStyleByRank.set(rank, style)
  return style
}

/**
 * The territory boundary overlay's style function -- colors each drawn
 * boundary by its level rank (TERRITORY_COLORS) and labels it with the
 * territory's name, both read from the GeoJSON properties the overlay's
 * boundaries endpoint attaches (see useTerritoryBoundaries /
 * api/internal/territory/http.go's boundariesHandler). Translucent fill
 * + a plain stroke so the real feature layers underneath stay legible;
 * this overlay is a reference backdrop, never a selection target for the
 * features it contains.
 *
 * `showLabels` (default on) gates the name label -- with it off the boundaries
 * still draw in their level colours but without the name text, for users who
 * want the areas without the clutter (territory-overlay-store's showLabels).
 * Off is rendered as an empty label string rather than a separate style so the
 * per-rank base style stays cached; the caller passes one value for the whole
 * overlay, so there's no half-labelled state to worry about.
 */
export function territoryStyleForFeature(feature: FeatureLike, showLabels = true): Style {
  const rank = (feature.get("level_rank") as number | undefined) ?? 0
  const name = showLabels ? ((feature.get("name") as string | undefined) ?? "") : ""
  const style = territoryBaseStyle(rank)
  style.getText()?.setText(name)
  return style
}

/**
 * The in-progress/unsaved marker voltedge-map.tsx shows while a new
 * feature's create form is open -- see src/stores/draw-store.ts. Same
 * geometry-type branching as styleForFeature, dashed and in
 * DRAFT_COLOR instead of the real per-type colors, so a draft never
 * reads as already-saved data regardless of which geometry type it is.
 */
export function draftStyleForFeature(feature: FeatureLike): Style {
  switch (feature.getGeometry()?.getType()) {
    case "Point":
    case "MultiPoint":
      return draftPointStyle
    case "LineString":
    case "MultiLineString":
      return draftLineStyle
    default:
      return draftPolygonStyle
  }
}

/**
 * The map-annotation (redlining) kinds, mirroring the server's CHECK
 * constraint (migration 0060 / api/internal/mapannotation): text->Point,
 * box->Polygon, line/arrow->LineString. Kept here (the rendering module) so the
 * data hook (use-map-annotations.ts) and the style function below share one
 * definition.
 */
export type AnnotationKind = "text" | "box" | "line" | "arrow"

/**
 * One annotation's per-instance appearance, the parsed shape of
 * runtime.map_annotations.style. Every knob is optional -- an empty object is a
 * valid style, falling back to the redline defaults below. This is what makes
 * annotations a bespoke table rather than a feature class: a feature system
 * styles per class, but redlining styles each mark individually.
 */
export interface AnnotationStyle {
  /** Stroke / text colour (and the box outline). */
  color?: string
  stroke_width?: number
  dash?: LineDashStyle
  /** Box fill colour; falls back to `color` when unset. */
  fill?: string
  /** Box fill opacity, 0-1. */
  fill_opacity?: number
  font_size?: number
  bold?: boolean
  italic?: boolean
  /** Text-note rotation, degrees clockwise (0 = upright). Only meaningful for
   * kind='text'; the other kinds reshape via their geometry. */
  rotation?: number
  /** Arrow only: draw an arrowhead at the start vertex as well, so the mark
   * reads as a double-headed arrow. Unset/false = a single head at the end (the
   * plain directional arrow). */
  double?: boolean
  /** Text halo, defaulting on (a note over an arbitrary basemap needs it). */
  halo?: boolean
  halo_color?: string
}

/** The redline layer's defaults when a style knob is unset -- a warm red that
 * reads as "markup", distinct from every feature/overlay/highlight role above
 * (features copper/teal/slate, imported amber, territory blues, the highlight
 * palette). */
const ANNOTATION_COLOR = "#dc2626"
const ANNOTATION_WIDTH = 2
const ANNOTATION_FONT_SIZE = 14
const ANNOTATION_HALO_COLOR = "#ffffff"
const ANNOTATION_BOX_FILL_OPACITY = 0.15

/** The redline defaults, exported so the style editor (annotation-edit-panel.tsx)
 * seeds its controls from the same values the renderer falls back to -- an unset
 * knob reads the same whether it's drawn or being edited. */
export const ANNOTATION_DEFAULTS = {
  color: ANNOTATION_COLOR,
  strokeWidth: ANNOTATION_WIDTH,
  fontSize: ANNOTATION_FONT_SIZE,
  boxFillOpacity: ANNOTATION_BOX_FILL_OPACITY,
} as const

/** A two-digit hex alpha for an opacity in 0-1, appended to a #rrggbb colour
 * so OL fills read as `#rrggbbaa`. Clamped so an out-of-range style value can't
 * produce a malformed colour string. */
function alphaHex(opacity: number): string {
  const clamped = Math.max(0, Math.min(1, opacity))
  return Math.round(clamped * 255)
    .toString(16)
    .padStart(2, "0")
}

/** The Text for a text annotation, built from its style (font/halo/colour). */
function annotationText(text: string, style: AnnotationStyle, color: string): Text {
  const size = style.font_size ?? ANNOTATION_FONT_SIZE
  const bold = style.bold === true
  const italic = style.italic === true
  const halo = style.halo !== false
  const haloColor = style.halo_color ?? ANNOTATION_HALO_COLOR
  const font = `${italic ? "italic " : ""}${bold ? "bold " : ""}${size}px ui-sans-serif, system-ui, sans-serif`
  // Degrees clockwise -> OL's radians (also clockwise-positive for Text), so a
  // rotated label sits at the authored angle. Left rotateWithView at its default
  // (false) so the label keeps its angle relative to the screen, not the map.
  const rotation = ((style.rotation ?? 0) * Math.PI) / 180
  return new Text({
    text,
    font,
    rotation,
    fill: new Fill({ color }),
    stroke: halo ? new Stroke({ color: haloColor, width: 3 }) : undefined,
    overflow: true,
  })
}

/**
 * An optional caption for a non-text annotation (box / line / arrow). The
 * editor lets these kinds carry free-form text too, so we render it as a label
 * at the geometry's label point -- OL point-places text at a line's midpoint and
 * a polygon's interior point -- lifted a line-height above so it clears the
 * stroke. Null when there's no caption, so the caller draws only its shape.
 */
function annotationCaption(feature: FeatureLike, style: AnnotationStyle, color: string): Style | null {
  const text = ((feature.get("text") as string | null) ?? "").trim()
  if (!text) return null
  const label = annotationText(text, style, color)
  label.setOffsetY(-(style.font_size ?? ANNOTATION_FONT_SIZE))
  return new Style({ text: label })
}

/**
 * A filled triangle at one end of an arrow annotation, rotated to the bearing of
 * that end's segment so it points outward along the line -- a directional
 * arrowhead. `atStart` puts it on the first vertex (pointing back off the start),
 * else on the last (the default single-arrow head); a double-headed arrow draws
 * both. Null when the geometry isn't a usable line (fewer than two vertices) --
 * the caller then just draws the bare stroke. The rotation converts a map
 * bearing (CCW from east) to RegularShape's screen convention (clockwise from
 * up): an up-pointing triangle rotated by (pi/2 - bearing) points along the
 * segment.
 */
function arrowHeadStyle(feature: FeatureLike, color: string, width: number, atStart = false): Style | null {
  const geom = feature.getGeometry()
  if (!(geom instanceof LineString)) return null
  const coords = geom.getCoordinates()
  if (coords.length < 2) return null
  // The tip is the terminal vertex; `prev` is the vertex just inside it, so the
  // bearing from prev->tip points outward off that end for either head.
  const tip = atStart ? coords[0] : coords[coords.length - 1]
  const prev = atStart ? coords[1] : coords[coords.length - 2]
  const bearing = Math.atan2(tip[1] - prev[1], tip[0] - prev[0])
  return new Style({
    geometry: new Point(tip),
    image: new RegularShape({
      points: 3,
      radius: 6 + width,
      fill: new Fill({ color }),
      rotation: Math.PI / 2 - bearing,
    }),
  })
}

/**
 * The map-annotation overlay's style function (Phase 36) -- reads each
 * annotation's own `kind`, `text`, and parsed `style` object off the feature
 * (voltedge-map.tsx sets them as properties when it builds the layer from the
 * fetched annotations) and renders per-instance markup: a text note, a filled/
 * outlined box, a (optionally dashed) line, or a line with an arrowhead. Unlike
 * every other style function here it branches on a `kind` property rather than
 * geometry type alone, because arrow and line share a geometry type but differ
 * only in styling.
 */
export function annotationStyleForFeature(feature: FeatureLike): Style | Style[] {
  const kind = feature.get("kind") as AnnotationKind | undefined
  const style = (feature.get("style") as AnnotationStyle | null) ?? {}
  const color = style.color ?? ANNOTATION_COLOR
  const width = style.stroke_width ?? ANNOTATION_WIDTH
  const lineDash = lineDashArray(style.dash)
  const lineCap = style.dash === "dotted" ? "round" : undefined

  switch (kind) {
    case "text": {
      const text = (feature.get("text") as string | null) ?? ""
      return new Style({ text: annotationText(text, style, color) })
    }
    case "box": {
      const fillColor = style.fill ?? color
      const fillOpacity = style.fill_opacity ?? ANNOTATION_BOX_FILL_OPACITY
      const box = new Style({
        fill: new Fill({ color: `${fillColor}${alphaHex(fillOpacity)}` }),
        stroke: new Stroke({ color, width, lineDash, lineCap }),
      })
      const caption = annotationCaption(feature, style, color)
      return caption ? [box, caption] : box
    }
    case "arrow": {
      const line = new Style({ stroke: new Stroke({ color, width, lineDash, lineCap }) })
      const head = arrowHeadStyle(feature, color, width)
      // A double-headed arrow adds a second head at the start vertex.
      const tail = style.double ? arrowHeadStyle(feature, color, width, true) : null
      const caption = annotationCaption(feature, style, color)
      return [line, ...(head ? [head] : []), ...(tail ? [tail] : []), ...(caption ? [caption] : [])]
    }
    default: {
      // "line" and any unknown kind: a plain stroke, plus any caption.
      const line = new Style({ stroke: new Stroke({ color, width, lineDash, lineCap }) })
      const caption = annotationCaption(feature, style, color)
      return caption ? [line, caption] : line
    }
  }
}

/** Violet -- a saved bookmark pin (map-pins-store.ts) is a personal navigation
 * aid, not network data, so it deliberately reads as none of the feature/
 * draft/edit/highlight colors above. */
const PIN_COLOR = "#7c3aed"

/** A teardrop marker whose tip sits on the coordinate, drawn as an OL Icon from
 * an inline SVG so it needs no asset. 24x32 with the point at the bottom-center,
 * anchored there. */
const PIN_ICON_SRC =
  "data:image/svg+xml;utf8," +
  encodeURIComponent(
    `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="32" viewBox="0 0 24 32">` +
      `<path d="M12 0C5.4 0 0 5.4 0 12c0 9 12 20 12 20s12-11 12-20C24 5.4 18.6 0 12 0z" fill="${PIN_COLOR}" stroke="#ffffff" stroke-width="1.5"/>` +
      `<circle cx="12" cy="12" r="4.5" fill="#ffffff"/>` +
      `</svg>`,
  )

/**
 * Style for a saved bookmark pin (voltedge-map.tsx's pins layer): the teardrop
 * marker plus the pin's name lifted above it. The name comes off the feature's
 * `name` property, set when the layer is populated from map-pins-store.ts.
 */
export function pinStyleForFeature(feature: FeatureLike): Style {
  const name = ((feature.get("name") as string | null) ?? "").trim()
  return new Style({
    image: new Icon({ src: PIN_ICON_SRC, anchor: [0.5, 1], scale: 1 }),
    text: name
      ? new Text({
          text: name,
          font: "12px ui-sans-serif, system-ui, sans-serif",
          // Above the 32px-tall pin so the label clears the marker.
          offsetY: -38,
          fill: new Fill({ color: PIN_COLOR }),
          stroke: new Stroke({ color: "#ffffff", width: 3 }),
          overflow: true,
        })
      : undefined,
  })
}
