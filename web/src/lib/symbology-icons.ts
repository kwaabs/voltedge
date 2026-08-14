/**
 * Hand-authored SVGs keyed by icon_ref -- referenced from a
 * symbology_rules row's style_def (api/internal/metadata's
 * SymbologyRule, docs/metadata-core.md's "Symbology resolution"
 * section). Deliberately not a real sprite/asset pipeline
 * (docs/open-items.md's own framing for this gap) -- just a small,
 * hardcoded registry; worth revisiting with a real icon-serving system
 * if this set ever grows past a handful of shapes.
 *
 * Each template uses `{{COLOR}}` in place of a real fill/stroke value
 * -- iconDataUri substitutes a rule's resolved color in directly,
 * rather than relying on OpenLayers' Icon `color` option (a canvas
 * recolor trick that only works for OL's own rendering, not a plain
 * `<img>` -- layers-panel.tsx's legend needs the exact same icon to
 * render correctly outside OL entirely). "triangle-outline" is the one
 * deliberate shape exception -- a stroke with no fill, so its center
 * stays transparent, giving DIST_SWITCH's open state a genuinely
 * hollow silhouette instead of just a different color on the same
 * solid shape (the "shape vs. modifier" distinction
 * docs/metadata-core.md draws for exactly this example).
 */
const ICONS: Record<string, string> = {
  circle: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" fill="{{COLOR}}"/></svg>`,
  square: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="2" fill="{{COLOR}}"/></svg>`,
  triangle: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><polygon points="12,3 21,20 3,20" fill="{{COLOR}}"/></svg>`,
  "triangle-outline": `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><polygon points="12,3 21,20 3,20" fill="none" stroke="{{COLOR}}" stroke-width="2.5" stroke-linejoin="round"/></svg>`,
  diamond: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><polygon points="12,2 22,12 12,22 2,12" fill="{{COLOR}}"/></svg>`,
}

const DEFAULT_ICON_REF = "circle"
const DEFAULT_COLOR = "#000000"

/** Every icon_ref a symbology_rules row can legally reference -- kept
 * exported so a future admin-facing rule editor has a real list to
 * offer instead of a free-text field. */
export const KNOWN_ICON_REFS = Object.keys(ICONS)

/** Data-URI for an icon_ref tinted to a given color, suitable for OL
 * Icon's `src` (map-layers.ts) or a plain `<img src>` (layers-panel.tsx's
 * legend) alike. Unknown/missing refs fall back to the default shape
 * rather than failing the whole style function -- a typo'd icon_ref in
 * a symbology_rules row shouldn't make a feature invisible. */
export function iconDataUri(iconRef: string | undefined, color: string = DEFAULT_COLOR): string {
  const template = ICONS[iconRef ?? DEFAULT_ICON_REF] ?? ICONS[DEFAULT_ICON_REF]
  const svg = template.replaceAll("{{COLOR}}", color)
  return `data:image/svg+xml;utf8,${encodeURIComponent(svg)}`
}
