import { svgDashArray } from "@/lib/line-dash"
import { iconDataUri, type IconRegistry } from "@/lib/symbology-icons"
import { cn } from "@/lib/utils"

/** The three geometry kinds a feature class can have -- the same strings
 * schema.geometry_type carries (see use-feature-class-schema.ts). */
export type SwatchGeometry = "point" | "line" | "polygon"

/** The subset of a rule's style_def a swatch needs -- matches
 * AdminStyleDef (use-admin.ts). Every field optional: a rule only fills
 * the keys its geometry uses. */
export interface SwatchStyle {
  icon_ref?: string
  color?: string
  width?: number
  dash?: string
  fill_color?: string
  stroke_color?: string
}

/** Fallbacks mirror map-layers.ts's own per-geometry defaults, so a
 * swatch of a rule that leaves a color unset previews the exact color
 * the map would actually draw rather than an invented stand-in. */
const LINE_FALLBACK = "#3f8fa6"
const POLYGON_FALLBACK = "#64748b"

/**
 * A live swatch of exactly how a symbology rule renders, matched to the
 * class's geometry -- a point shows its icon (tinted through the same
 * iconDataUri path the map uses), a line shows a stroke at its real
 * color/width, a polygon shows a filled+stroked chip at its real fill/
 * stroke colors.
 *
 * Why this exists: lines and polygons carry no icon_ref, so rendering
 * every rule through the point `<img>`/iconDataUri path (as the rule
 * list first did) silently fell back to a generic circle for them -- the
 * preview "worked" only for points. Branching on geometry here is what
 * makes the preview honest for all three, since a line/polygon style is
 * a stroke/fill, not an asset reference.
 */
export function SymbologySwatch({
  geometry,
  style,
  icons,
  className,
}: {
  geometry: SwatchGeometry
  style: SwatchStyle
  icons?: IconRegistry
  className?: string
}) {
  if (geometry === "line") {
    const color = style.color ?? LINE_FALLBACK
    // Clamp so a large authored width still fits the swatch box; the map
    // draws the real value, this only has to read as "thicker/thinner."
    const width = Math.min(Math.max(style.width ?? 3, 1), 8)
    return (
      <svg viewBox="0 0 24 24" className={cn("block", className)} aria-hidden="true">
        <line
          x1="3"
          y1="12"
          x2="21"
          y2="12"
          stroke={color}
          strokeWidth={width}
          strokeLinecap="round"
          strokeDasharray={svgDashArray(style.dash)}
        />
      </svg>
    )
  }

  if (geometry === "polygon") {
    const fill = style.fill_color ?? style.color ?? POLYGON_FALLBACK
    const stroke = style.stroke_color ?? fill
    return (
      <svg viewBox="0 0 24 24" className={cn("block", className)} aria-hidden="true">
        {/* fillOpacity 0.2 echoes map-layers.ts's `${color}33` polygon fill. */}
        <rect x="4" y="6" width="16" height="12" rx="1.5" fill={fill} fillOpacity={0.2} stroke={stroke} strokeWidth={1.5} />
      </svg>
    )
  }

  return <img src={iconDataUri(style.icon_ref, style.color, icons)} alt="" className={className} />
}
