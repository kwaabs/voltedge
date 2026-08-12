import { Circle, Fill, Stroke, Style } from "ol/style"
import type { FeatureLike } from "ol/Feature"

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

/**
 * Geometry-type-only styling -- no metadata-driven symbology yet
 * (icon_ref resolution, shape vs. modifier, subtype/domain-value
 * rules). That's docs/metadata-core.md's whole "Symbology resolution"
 * section and a real subsystem on its own; this is deliberately just
 * enough to tell feature classes apart on the map by shape while that
 * lands as its own later slice.
 */
export function styleForFeature(feature: FeatureLike): Style {
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
