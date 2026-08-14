import { Circle, Fill, Icon, Stroke, Style } from "ol/style"
import type { FeatureLike } from "ol/Feature"

import { iconDataUri } from "@/lib/symbology-icons"
import { resolveSymbology, type SymbologyRuleSchema } from "@/lib/symbology"
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
 * transformer a "trace to transformer" run found); or, while an
 * isolation result is active (isolate-store.ts), everything that would
 * de-energize ("isolation-deenergized"), the upstream protective
 * boundary device ("isolation-boundary"), and any possible backfeed
 * path ("isolation-backfeed") -- each role in its own color so none
 * are ever confused with each other or with a plain selection. */
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

const HIGHLIGHT_COLORS: Record<HighlightRole, string> = {
  selected: "#38bdf8",
  "connect-source": "#f59e0b",
  "connect-target": "#a855f7",
  trace: "#84cc16",
  "trace-barrier": "#ef4444",
  "trace-target": "#eab308",
  "isolation-deenergized": "#f97316",
  "isolation-boundary": "#8b5cf6",
  "isolation-backfeed": "#dc2626",
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

function iconStyleFor(iconRef: string | undefined, color: string, scale: number): Style {
  const key = `${iconRef ?? ""}|${color}|${scale}`
  const cached = iconStyleCache.get(key)
  if (cached) return cached
  const style = new Style({ image: new Icon({ src: iconDataUri(iconRef, color), scale }) })
  iconStyleCache.set(key, style)
  return style
}

/**
 * Builds one layer's style function, bound to that feature class's own
 * resolved symbology_rules (see symbology.ts's resolveSymbology and
 * docs/metadata-core.md's "Symbology resolution" section) -- each
 * Martin catalog entry is already scoped to exactly one feature class,
 * so voltedge-map.tsx creates one of these per VectorTileLayer at
 * layer-creation time rather than resolving rules generically per
 * feature. Falls back to styleForFeature's plain geometry-type styling
 * whenever no rule resolves at all (rules is empty/undefined, or the
 * feature's own subtype+attributes don't match even the mandatory
 * class-level default -- a metadata-authoring gap, not something worth
 * hiding the feature entirely over).
 */
export function createSymbologyStyle(rules: SymbologyRuleSchema[] | undefined) {
  return function symbologyStyleForFeature(feature: FeatureLike): Style {
    if (isHiddenFeature(feature)) return hiddenStyle

    const geometryType = feature.getGeometry()?.getType()
    const props = feature.getProperties() as Record<string, unknown>
    const subtype = typeof props.subtype === "number" ? props.subtype : undefined
    const def = resolveSymbology(rules, subtype, props, "map")
    if (!def) return styleForFeature(feature)

    switch (geometryType) {
      case "Point":
      case "MultiPoint":
        return iconStyleFor(def.icon_ref, def.color ?? POINT_COLOR, ICON_BASE_SCALE * (def.scale ?? 1))
      case "LineString":
      case "MultiLineString":
        return new Style({ stroke: new Stroke({ color: def.color ?? LINE_COLOR, width: def.width ?? 3 }) })
      default: {
        const color = def.fill_color ?? def.color ?? POLYGON_COLOR
        return new Style({
          fill: new Fill({ color: `${color}33` }),
          stroke: new Stroke({ color: def.stroke_color ?? color, width: 2 }),
        })
      }
    }
  }
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
