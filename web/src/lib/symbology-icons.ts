/**
 * Resolves a symbology rule's `icon_ref` to real SVG markup and a tinted
 * `data:` URI. The markup itself now lives in the DB
 * (metadata.symbology_icons, migrations/0023), fetched once via
 * use-symbology-icons.ts and passed in here as an `icons` map -- so an
 * icon can be added or changed without a frontend deploy. The hardcoded
 * set below is a *fallback* only: it's what renders before that fetch
 * lands, or when a rule references an `icon_ref` the DB doesn't have, so a
 * typo'd ref or an in-flight fetch never makes a feature invisible.
 *
 * Each template uses `{{COLOR}}` in place of a real fill/stroke value --
 * iconDataUri substitutes a rule's resolved color in directly, rather
 * than relying on OpenLayers' Icon `color` option (a canvas recolor trick
 * that only works for OL's own rendering, not a plain `<img>` --
 * layers-panel.tsx's legend needs the exact same icon to render correctly
 * outside OL entirely). An icon whose markup has NO `{{COLOR}}` placeholder
 * simply renders as-is: that's how full-color artwork opts out of tinting
 * (the substitution is a harmless no-op on it), so a single code path
 * serves both single-color and multi-color symbols.
 *
 * "triangle-outline" is the one deliberate shape exception -- a stroke
 * with no fill, so its center stays transparent, giving DIST_SWITCH's open
 * state a genuinely hollow silhouette instead of just a different color on
 * the same solid shape (the "shape vs. modifier" distinction
 * docs/metadata-core.md draws for exactly this example).
 *
 * NOTE: every template carries explicit width/height, not just a viewBox
 * -- Chromium resolves a missing intrinsic size to 150x150 for a detached
 * Image(), which silently broke vector-tile icon rendering the one time it
 * shipped without them (docs/phase-3-symbology.md). Any DB-authored icon
 * must do the same.
 */
const FALLBACK_ICONS: Record<string, string> = {
  circle: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" fill="{{COLOR}}"/></svg>`,
  square: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="2" fill="{{COLOR}}"/></svg>`,
  triangle: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><polygon points="12,3 21,20 3,20" fill="{{COLOR}}"/></svg>`,
  "triangle-outline": `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><polygon points="12,3 21,20 3,20" fill="none" stroke="{{COLOR}}" stroke-width="2.5" stroke-linejoin="round"/></svg>`,
  diamond: `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24"><polygon points="12,2 22,12 12,22 2,12" fill="{{COLOR}}"/></svg>`,
}

const DEFAULT_ICON_REF = "circle"
const DEFAULT_COLOR = "#000000"

/** A resolved icon set -- icon_ref -> raw SVG markup. What
 * use-symbology-icons.ts returns (the DB set merged over FALLBACK_ICONS)
 * and what iconDataUri/createSymbologyStyle take as their `icons`
 * argument. */
export type IconRegistry = Record<string, string>

/** The hardcoded fallback refs -- every icon guaranteed to resolve even
 * with no DB icons loaded. use-symbology-icons.ts unions these with the
 * DB's own refs so an admin picker offers the full set. */
export const FALLBACK_ICON_REFS = Object.keys(FALLBACK_ICONS)

/** The raw SVG markup an icon_ref resolves to, with `{{COLOR}}` still
 * unsubstituted -- the DB set first, then the hardcoded fallback, then the
 * default shape, exactly the chain iconDataUri uses. Exposed for consumers
 * that need the geometry itself rather than a tinted data URI (the DXF
 * exporter parses this to real glyph outlines, diagram-dxf.ts). Never
 * returns undefined: an unknown/missing ref still yields the default shape,
 * so a caller always has something to draw. */
export function resolveIconMarkup(iconRef: string | undefined, icons?: IconRegistry): string {
  const ref = iconRef ?? DEFAULT_ICON_REF
  return icons?.[ref] ?? FALLBACK_ICONS[ref] ?? FALLBACK_ICONS[DEFAULT_ICON_REF]
}

/** Data-URI for an icon_ref tinted to a given color, suitable for OL
 * Icon's `src` (map-layers.ts) or a plain `<img src>` (layers-panel.tsx's
 * legend) alike. Resolves the markup from `icons` (the DB set) first,
 * falling back to the hardcoded set, then to the default shape -- so an
 * unknown/missing ref, or a not-yet-loaded fetch, renders a real shape
 * rather than failing the whole style. The `{{COLOR}}` substitution is a
 * no-op on full-color artwork that has no placeholder (see the module
 * comment). */
export function iconDataUri(
  iconRef: string | undefined,
  color: string = DEFAULT_COLOR,
  icons?: IconRegistry,
): string {
  const svg = resolveIconMarkup(iconRef, icons).replaceAll("{{COLOR}}", color)
  return `data:image/svg+xml;utf8,${encodeURIComponent(svg)}`
}
