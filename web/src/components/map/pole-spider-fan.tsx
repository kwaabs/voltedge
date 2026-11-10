import { useEffect } from "react"
import { Box, Lightbulb, X } from "lucide-react"

import { useFeature } from "@/hooks/use-feature"
import { cn } from "@/lib/utils"
import { type PoleSpiderActive, type SpiderAttachment, usePoleSpiderStore } from "@/stores/pole-spider-store"
import { useSelectionStore } from "@/stores/selection-store"

const STREET_LIGHT = "STREET_LIGHT"

/** One fanned attachment chip, placed at its ring position. Fetches its own
 * feature name (like the list rows) so the chip reads "SW-1", not just the class;
 * hover highlights it on the map, click opens its panel (and collapses the fan). */
function SpiderChip({ att, x, y, onPick }: { att: SpiderAttachment; x: number; y: number; onPick: (a: SpiderAttachment) => void }) {
  const feature = useFeature(att.feature_class, att.feature_id)
  const label = (feature.data?.name as string | undefined) || att.label
  const Icon = att.feature_class === STREET_LIGHT ? Lightbulb : Box
  return (
    <button
      type="button"
      className="bg-popover text-popover-foreground hover:bg-accent pointer-events-auto absolute flex max-w-[160px] items-center gap-1 rounded-full border px-2 py-1 text-xs shadow-md"
      style={{ left: x, top: y, transform: "translate(-50%, -50%)" }}
      onClick={() => onPick(att)}
      onMouseEnter={() => useSelectionStore.getState().locate(att.feature_class, att.feature_id)}
      title={label}
    >
      <Icon className={cn("size-3 shrink-0", att.feature_class === STREET_LIGHT ? "text-amber-500" : "text-muted-foreground")} />
      <span className="truncate">{label}</span>
    </button>
  )
}

/**
 * The pole "spiderfy" fan (pole-attachments v2): the geometry-less features
 * mounted on a pole all share its point, so this splays them into a ring around
 * the pole with leader lines back to it, each individually hoverable (highlights
 * on the map) and clickable (opens its panel). Rendered via a React portal into
 * an ol/Overlay that voltedge-map.tsx anchors at the pole's coordinate, so the
 * fan tracks pan/zoom while staying a fixed pixel-size layout. The root is a
 * zero-size point (the overlay centres it on the pole); everything is positioned
 * relative to that centre. Escape or the × collapses it.
 */
export function PoleSpiderFan({ active }: { active: PoleSpiderActive }) {
  const close = usePoleSpiderStore((s) => s.close)
  const atts = active.attachments
  const n = atts.length

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") close()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [close])

  if (n === 0) return null

  // A single ring; radius grows a little with the count so chips don't crowd.
  // Start off-axis (upper-right) so a single chip never sits dead-vertical or
  // -horizontal over the conductor the pole stands on.
  const radius = Math.max(66, 24 + n * 11)
  const positions = atts.map((_, i) => {
    const angle = (-60 + (360 / n) * i) * (Math.PI / 180)
    return { x: radius * Math.cos(angle), y: radius * Math.sin(angle) }
  })
  const s = radius + 72 // half-size of the leader-line SVG canvas

  function pick(a: SpiderAttachment) {
    useSelectionStore.getState().select(a.feature_class, a.feature_id, { focus: true })
    close()
  }

  return (
    <div className="text-muted-foreground pointer-events-none relative" style={{ width: 0, height: 0 }}>
      {/* Marching-ants keyframe, so the leaders animate and read clearly as
          ephemeral UI connectors (not static network edges). */}
      <style>{`@keyframes pole-spider-march { to { stroke-dashoffset: -16; } }`}</style>
      {/* leader lines: dashed + animated + accent-coloured, so they never read as
          a grey network line (especially over the conductor the pole stands on). */}
      <svg
        className="text-primary absolute"
        style={{ left: -s, top: -s, width: 2 * s, height: 2 * s, overflow: "visible" }}
        aria-hidden
      >
        {positions.map((p, i) => (
          <line
            key={i}
            x1={s}
            y1={s}
            x2={s + p.x}
            y2={s + p.y}
            stroke="currentColor"
            strokeWidth={2}
            strokeDasharray="5 4"
            style={{ animation: "pole-spider-march 0.6s linear infinite" }}
          />
        ))}
      </svg>

      {/* the pole centre */}
      <div
        className="bg-primary absolute size-2.5 rounded-full ring-2 ring-white"
        style={{ left: 0, top: 0, transform: "translate(-50%, -50%)" }}
      />

      {atts.map((a, i) => (
        <SpiderChip key={`${a.feature_class}:${a.feature_id}`} att={a} x={positions[i].x} y={positions[i].y} onPick={pick} />
      ))}

      {/* collapse control, above the ring */}
      <button
        type="button"
        onClick={close}
        className="bg-popover text-muted-foreground hover:text-foreground pointer-events-auto absolute flex size-5 items-center justify-center rounded-full border shadow"
        style={{ left: 0, top: -(radius + 46), transform: "translate(-50%, -50%)" }}
        title="Collapse (Esc)"
      >
        <X className="size-3" />
      </button>
    </div>
  )
}
