import { Circle, Fill, Stroke, Style } from "ol/style"
import type { FeatureLike } from "ol/Feature"

/**
 * Canvas-rendered styles can't consume the app's CSS custom properties
 * (see src/index.css) -- OpenLayers paints to a <canvas>, not the DOM,
 * so these are separate, hand-picked constants that echo the theme's
 * primary/chart tokens rather than reference them. Point = copper
 * (echoes --primary), line = teal (echoes --chart-2), polygon = slate
 * (a neutral, since no polygon feature class is compiled yet).
 */
const POINT_COLOR = "#d9752e"
const LINE_COLOR = "#3f8fa6"
const POLYGON_COLOR = "#64748b"

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
