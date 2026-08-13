import { Circle, Fill, Stroke, Style } from "ol/style"
import type { FeatureLike } from "ol/Feature"

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

/** The three roles voltedge-map.tsx's highlight layer ever draws: the
 * single feature currently shown in the docked panel, or -- while a
 * connectivity/relationship workflow is active (connect-store.ts) --
 * the source and target ends of it, each in its own color so the two
 * are never confused with each other or with a plain selection. */
export type HighlightRole = "selected" | "connect-source" | "connect-target"

const HIGHLIGHT_COLORS: Record<HighlightRole, string> = {
  selected: "#38bdf8",
  "connect-source": "#f59e0b",
  "connect-target": "#a855f7",
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
 * Geometry-type-only styling -- no metadata-driven symbology yet
 * (icon_ref resolution, shape vs. modifier, subtype/domain-value
 * rules). That's docs/metadata-core.md's whole "Symbology resolution"
 * section and a real subsystem on its own; this is deliberately just
 * enough to tell feature classes apart on the map by shape while that
 * lands as its own later slice.
 */
export function styleForFeature(feature: FeatureLike): Style {
  const id = feature.get("id") as string | undefined
  if (id && useDeletedFeaturesStore.getState().ids.has(id)) return hiddenStyle
  // The real, tile-rendered copy of whichever feature is under
  // geometry edit is hidden for the whole edit session (not just while
  // a geometry tool is actively engaged) -- the cyan edit-geometry
  // layer (see editStyleForFeature, voltedge-map.tsx) stands in for it
  // instead, so there's never a stale copy sitting at the old position
  // underneath the one the user can actually drag.
  if (id && useEditStore.getState().editing?.featureId === id) return hiddenStyle

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
