/**
 * The named line-dash patterns a symbology rule can carry
 * (style_def.dash) -- solid vs dashed vs dotted vs dash-dot, exactly the
 * distinction a utility map leans on (overhead vs underground, primary vs
 * secondary). Kept as semantic names, not raw pixel arrays, so a rule
 * reads like the rest of style_def (dash names a pattern the way icon_ref
 * names an asset) and this one module owns the actual arrays -- the map
 * renderer (map-layers.ts) and the admin swatch (symbology-swatch.tsx)
 * both read it, so a pattern can never mean two different things.
 */

export const LINE_DASH_STYLES = ["solid", "dashed", "dotted", "dashdot"] as const
export type LineDashStyle = (typeof LINE_DASH_STYLES)[number]

/** An OpenLayers Stroke `lineDash` array for a dash name, in pixels.
 * `undefined` means a continuous line (solid, or an unset/unknown value),
 * so the renderer just omits lineDash and draws the default solid stroke. */
export function lineDashArray(dash: string | undefined): number[] | undefined {
  switch (dash) {
    case "dashed":
      return [8, 6]
    case "dotted":
      // A ~zero-length dash rendered with a round line cap is a real dot;
      // map-layers.ts pairs this with lineCap "round" for exactly that.
      return [1, 8]
    case "dashdot":
      return [10, 6, 1, 6]
    default:
      return undefined
  }
}

/** The same patterns as an SVG `stroke-dasharray` string, in the swatch's
 * own 0-24 viewBox units (not OL pixels, hence a separate scale from
 * lineDashArray) -- for the admin preview. Pairs with strokeLinecap
 * "round" so the near-zero dot dashes render as circles. */
export function svgDashArray(dash: string | undefined): string | undefined {
  switch (dash) {
    case "dashed":
      return "5 4"
    case "dotted":
      return "0.1 4"
    case "dashdot":
      return "6 3 0.1 3"
    default:
      return undefined
  }
}
