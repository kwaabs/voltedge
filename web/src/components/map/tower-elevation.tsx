import { useEffect, useMemo, useState } from "react"
import { Loader2, TowerControl } from "lucide-react"

import { useAllSymbology } from "@/hooks/use-all-symbology"
import { useFeatures } from "@/hooks/use-feature"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useRelationships } from "@/hooks/use-relationships"
import { resolveSymbology } from "@/lib/symbology"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"

// PoC: the tower/pole ELEVATION — a generated cross-section of a support structure
// showing its crossarms and the circuits it carries, one per side. Driven entirely
// from data: the conductors linked to this structure by spatial_support give the
// distinct circuits (circuit_ref), and the circuit COUNT picks the template
// (1 → single-circuit horizontal, 2 → double-circuit vertical). Per-circuit: phases
// are drawn as generic points, not individually attributed. No new map geometry.

const SUPPORT_KIND = "spatial_support"
const STRUCTURE_GREY = "#64748b"
// Fallback phase colour when a conductor's class has no matching map symbology
// rule (so the elevation can still draw it) -- otherwise the colour comes from the
// SAME symbology the map uses, so two circuits of the same voltage read the same
// (they're told apart by their circuit label, not colour).
const FALLBACK_COLOR = "#94a3b8"

/** A local-frame elevation template (+Y up). `points` carry which circuit index
 * (0 = first/left, 1 = second/right) they belong to. */
interface Elevation {
  peakY: number
  earthWireY?: number
  arms: { y: number; x1: number; x2: number }[]
  /** Y values of faint separators drawn between crossarm levels (arranged layout). */
  levelLines?: number[]
  points: { x: number; y: number; circuit: number }[]
}

type PhaseLayout = "horizontal" | "vertical"

// Single circuit: 3 phases across one crossarm.
const SINGLE_HORIZONTAL: Elevation = {
  peakY: 72,
  arms: [{ y: 58, x1: -28, x2: 28 }],
  points: [
    { x: -28, y: 58, circuit: 0 },
    { x: 0, y: 58, circuit: 0 },
    { x: 28, y: 58, circuit: 0 },
  ],
}

// Double circuit: 3 crossarm levels, left stack = circuit 0, right stack = circuit 1.
const DOUBLE_VERTICAL: Elevation = {
  peakY: 100,
  earthWireY: 100,
  arms: [
    { y: 85, x1: -32, x2: 32 },
    { y: 68, x1: -38, x2: 38 },
    { y: 51, x1: -28, x2: 28 },
  ],
  points: [
    { x: -32, y: 85, circuit: 0 }, { x: 32, y: 85, circuit: 1 },
    { x: -38, y: 68, circuit: 0 }, { x: 38, y: 68, circuit: 1 },
    { x: -28, y: 51, circuit: 0 }, { x: 28, y: 51, circuit: 1 },
  ],
}

function pickTemplate(circuitCount: number): Elevation {
  return circuitCount >= 2 ? DOUBLE_VERTICAL : SINGLE_HORIZONTAL
}

interface Circuit {
  ref: string
  voltage: string | null
  color: string
  /** The conductor feature this circuit resolves to (to open on click). */
  conductorClass: string
  conductorId: string
  name: string
  /** Recorded arrangement on THIS structure, from the spatial_support link's
   * attributes (migration 0190). Present only when the link carries it; when any
   * circuit lacks side/level the view falls back to the inferred template.
   * `orientation` is per-circuit — a pole can run MV vertical over LV horizontal —
   * and defaults to the structure-wide toggle when the link doesn't record it. */
  side?: "L" | "R" | "C"
  level?: number
  orientation?: PhaseLayout
}

const SIDE_SIGN = { L: -1, R: 1, C: 0 } as const

function normSide(v: unknown): "L" | "R" | "C" | null {
  if (typeof v !== "string") return null
  const s = v.trim().toUpperCase()
  return s === "L" || s === "R" || s === "C" ? s : null
}

function normOrientation(v: unknown): PhaseLayout | null {
  if (typeof v !== "string") return null
  const s = v.trim().toLowerCase()
  return s === "vertical" || s === "horizontal" ? s : null
}

/** Build the elevation from each circuit's recorded side/level when EVERY circuit
 * carries the hint; otherwise fall back to the inferred single/double template.
 * The hint path draws an asymmetric arrangement — a quad tower (2 left / 2 right),
 * or an MV-over-LV pole (MV vertical up top, LV horizontal underbuild) — faithfully
 * instead of guessing. Each circuit sits on its SIDE at its LEVEL, with its three
 * phases laid out by its OWN orientation (horizontal = one crossarm; vertical =
 * three stacked crossarms, one phase each). `defaultLayout` is the structure-wide
 * fallback for a circuit whose link records no orientation. Level rows flex in
 * height so a tall vertical circuit never collides with the row below it. */
function buildElevation(circuits: Circuit[], defaultLayout: PhaseLayout): { template: Elevation; arranged: boolean } {
  const hinted = circuits.length > 0 && circuits.every((c) => c.side != null && c.level != null)
  if (!hinted) return { template: pickTemplate(circuits.length), arranged: false }

  const PHASE_GAP = 12
  const INNER = 8
  const ARM_W = 32 // horizontal: one crossarm holding all three phases
  const ARM_V = 22 // vertical: one short crossarm PER phase
  const V_ROW = 2 * PHASE_GAP // a vertical circuit's phase span
  const LEVEL_PAD = 18 // clear space (carrying the separator) between levels
  const TOP = 92

  const orientOf = (c: Circuit): PhaseLayout => c.orientation ?? defaultLayout

  // Stack the levels top-down, giving each its own height: a level with any
  // vertical circuit needs V_ROW of room, a purely-horizontal one almost none.
  const levels = [...new Set(circuits.map((c) => c.level!))].sort((a, b) => a - b)
  const centerY = new Map<number, number>()
  const levelLines: number[] = []
  let yTop = TOP
  levels.forEach((lvl, idx) => {
    const rowH = circuits.some((c) => c.level === lvl && orientOf(c) === "vertical") ? V_ROW : 0
    centerY.set(lvl, yTop - rowH / 2)
    if (idx < levels.length - 1) levelLines.push(yTop - rowH - LEVEL_PAD / 2)
    yTop = yTop - rowH - LEVEL_PAD
  })

  const arms: Elevation["arms"] = []
  const points: Elevation["points"] = []
  circuits.forEach((c, ci) => {
    const sign = SIDE_SIGN[c.side as "L" | "R" | "C"]
    const cy = centerY.get(c.level!)!
    if (orientOf(c) === "vertical") {
      // Three stacked crossarms, ONE phase each (top phase first → label anchors top).
      for (let k = 0; k < 3; k++) {
        const py = cy + PHASE_GAP - k * PHASE_GAP
        const x1 = sign === 0 ? -ARM_V / 2 : sign * INNER
        const x2 = sign === 0 ? ARM_V / 2 : sign * (INNER + ARM_V)
        arms.push({ y: py, x1, x2 })
        points.push({ x: sign === 0 ? 0 : x2, y: py, circuit: ci })
      }
    } else {
      const x1 = sign === 0 ? -ARM_W / 2 : sign * INNER
      const x2 = sign === 0 ? ARM_W / 2 : sign * (INNER + ARM_W)
      arms.push({ y: cy, x1, x2 })
      // Outer phase first, so the per-stack label (anchored on the top point) sits
      // at the arm's outer end, clear of the mast.
      for (let k = 0; k < 3; k++) {
        const t = 1 - k / 2
        points.push({ x: x1 + (x2 - x1) * t, y: cy, circuit: ci })
      }
    }
  })

  const peakY = Math.max(...points.map((p) => p.y)) + 16
  return { template: { peakY, earthWireY: peakY, arms, levelLines, points }, arranged: true }
}

function circuitCountLabel(n: number): string {
  return n >= 4 ? "Quad circuit" : n === 3 ? "Triple circuit" : n === 2 ? "Double circuit" : n === 1 ? "Single circuit" : "Structure"
}

/** The generated SVG: mast, crossarms, earth wire, and a coloured phase point per
 * attachment. Flips +Y-up local coords into SVG's y-down frame via the viewBox.
 * A phase point is clickable — it opens the conductor of the circuit it belongs to. */
function ElevationSvg({ template, circuits, onPick }: { template: Elevation; circuits: Circuit[]; onPick: (c: Circuit) => void }) {
  const legY = -12
  const xs = [0, ...template.arms.flatMap((a) => [a.x1, a.x2]), ...template.points.map((p) => p.x), -10, 10]
  const ys = [legY, template.peakY, ...(template.earthWireY != null ? [template.earthWireY] : []), ...template.points.map((p) => p.y), ...template.arms.map((a) => a.y)]
  const pad = 16
  const minX = Math.min(...xs) - pad
  const maxX = Math.max(...xs) + pad
  const minY = Math.min(...ys) - pad
  const maxY = Math.max(...ys) + pad
  const w = maxX - minX
  const h = maxY - minY
  const tx = (x: number) => x - minX
  const ty = (y: number) => maxY - y // flip

  return (
    <svg viewBox={`0 0 ${w} ${h}`} className="bg-muted/20 h-72 w-full rounded-md border" role="img" aria-label="Structure elevation">
      {/* faint separators between crossarm levels, drawn first so they sit behind
          the structure — a quiet visual grouping of each level's circuits */}
      {template.levelLines?.map((y, i) => (
        <line
          key={`lvl-${i}`}
          x1={tx(minX + 2)}
          y1={ty(y)}
          x2={tx(maxX - 2)}
          y2={ty(y)}
          stroke={STRUCTURE_GREY}
          strokeWidth={1}
          strokeDasharray="2 4"
          opacity={0.25}
        />
      ))}
      {/* base legs */}
      <line x1={tx(0)} y1={ty(0)} x2={tx(-10)} y2={ty(legY)} stroke={STRUCTURE_GREY} strokeWidth={2} />
      <line x1={tx(0)} y1={ty(0)} x2={tx(10)} y2={ty(legY)} stroke={STRUCTURE_GREY} strokeWidth={2} />
      {/* mast */}
      <line x1={tx(0)} y1={ty(0)} x2={tx(0)} y2={ty(template.peakY)} stroke={STRUCTURE_GREY} strokeWidth={2.5} />
      {/* crossarms */}
      {template.arms.map((a, i) => (
        <line key={i} x1={tx(a.x1)} y1={ty(a.y)} x2={tx(a.x2)} y2={ty(a.y)} stroke={STRUCTURE_GREY} strokeWidth={2} />
      ))}
      {/* earth / shield wire marker at the peak */}
      {template.earthWireY != null && (
        <g className="cursor-help">
          <title>Earth wire (shield / overhead ground wire) — runs above the phases to intercept lightning; carries no load.</title>
          <circle cx={tx(0)} cy={ty(template.earthWireY)} r={3} fill="none" stroke={STRUCTURE_GREY} strokeWidth={1.5} />
          <text x={tx(0) + 6} y={ty(template.earthWireY) + 3} fontSize={8} fill={STRUCTURE_GREY}>
            E
          </text>
        </g>
      )}
      {/* phase points, coloured by circuit — click to open that conductor */}
      {template.points.map((p, i) => {
        const c = circuits[p.circuit]
        return (
          <circle
            key={i}
            cx={tx(p.x)}
            cy={ty(p.y)}
            r={5}
            fill={c?.color ?? "#cbd5e1"}
            stroke="#ffffff"
            strokeWidth={1.5}
            className={c ? "cursor-pointer" : undefined}
            onClick={c ? () => onPick(c) : undefined}
          >
            {c && <title>{`${c.ref}${c.voltage ? ` · ${c.voltage}` : ""} — ${c.name} (highlight on map)`}</title>}
          </circle>
        )
      })}

      {/* Per-stack circuit label — colour follows voltage symbology, so the LABEL
          is what tells the two circuits apart. */}
      {circuits.map((c, ci) => {
        const pts = template.points.filter((p) => p.circuit === ci)
        if (pts.length === 0) return null
        const top = pts.reduce((a, b) => (b.y > a.y ? b : a))
        const anchor = top.x < 0 ? "end" : top.x > 0 ? "start" : "middle"
        const dx = top.x < 0 ? -9 : top.x > 0 ? 9 : 0
        return (
          <text key={c.ref} x={tx(top.x) + dx} y={ty(top.y) - 9} fontSize={8} textAnchor={anchor} fill={c.color} fontWeight={600}>
            {c.ref}
          </text>
        )
      })}
    </svg>
  )
}

/** The feature panel's "Elevation" tab for a support structure (POLE / TOWER). */
export function TowerElevationTab() {
  const selected = useSelectionStore((s) => s.selected)
  const cls = selected?.featureClassCode
  const id = selected?.featureId
  const [phaseLayout, setPhaseLayout] = useState<PhaseLayout>("horizontal")

  // Drop any map highlight this tab left behind when it unmounts (tab/feature
  // switch), same as the Terminals / On-this-pole tabs.
  useEffect(() => () => useSelectionStore.getState().clearLocated(), [])

  const rels = useRelationships(cls, id)
  const classes = useFeatureClasses()

  const lineClasses = useMemo(
    () => new Set((classes.data?.feature_classes ?? []).filter((c) => c.geometry_type === "line").map((c) => c.code)),
    [classes.data],
  )

  // The conductors this structure supports (the "other" end of each spatial_support
  // relationship whose class is a line class), each keeping its link's attributes —
  // the arrangement hint (side/level) lives on the relationship, not the conductor.
  const supports = useMemo(() => {
    const list = rels.data?.relationships ?? []
    return list
      .filter((r) => r.kind === SUPPORT_KIND)
      .map((r) => {
        const other = r.origin.feature_class === cls && r.origin.feature_id === id ? r.destination : r.origin
        return { feature_class: other.feature_class, feature_id: other.feature_id, attributes: r.attributes ?? null }
      })
      .filter((e) => lineClasses.has(e.feature_class))
  }, [rels.data, cls, id, lineClasses])

  const conductorRefs = useMemo(
    () => supports.map((s) => ({ featureClassCode: s.feature_class, featureId: s.feature_id })),
    [supports],
  )

  const conductors = useFeatures(conductorRefs)
  const allSymbology = useAllSymbology()

  // Distinct circuits carried, in first-seen order. Colour comes from the conductor's
  // own MAP symbology (same rule the map draws it with — voltage-driven), so two
  // circuits of the same voltage share the colour; the circuit label differentiates
  // them. Each keeps the conductor it resolves to so a click can open it.
  const circuits = useMemo<Circuit[]>(() => {
    const seen = new Map<string, Circuit>()
    supports.forEach((sup, i) => {
      const rec = conductors[i]?.data
      const a = rec?.attributes ?? {}
      const cref = typeof a.circuit_ref === "string" && a.circuit_ref ? a.circuit_ref : null
      if (!cref || seen.has(cref)) return
      const style = resolveSymbology(allSymbology[sup.feature_class], rec?.subtype, a, "map")
      // Arrangement hint from the support link (migration 0190), if recorded.
      const side = normSide(sup.attributes?.side)
      const level = Number(sup.attributes?.level)
      seen.set(cref, {
        ref: cref,
        voltage: typeof a.voltage_class === "string" ? a.voltage_class : null,
        color: style?.color ?? FALLBACK_COLOR,
        conductorClass: sup.feature_class,
        conductorId: sup.feature_id,
        name: rec?.name ?? sup.feature_id,
        side: side ?? undefined,
        level: Number.isFinite(level) && level > 0 ? level : undefined,
        orientation: normOrientation(sup.attributes?.orientation) ?? undefined,
      })
    })
    return [...seen.values()]
  }, [supports, conductors, allSymbology])

  // Legend rows OPEN the conductor's panel; clicking a phase point in the SVG only
  // HIGHLIGHTS it on the map (locate), without leaving this structure's panel.
  function openConductor(c: Circuit) {
    useSelectionStore.getState().select(c.conductorClass, c.conductorId, { focus: true })
  }
  function highlightConductor(c: Circuit) {
    useSelectionStore.getState().locate(c.conductorClass, c.conductorId)
  }

  if (!selected) return null

  const loading = rels.isPending || classes.isPending || conductors.some((q) => q.isPending)
  if (loading) {
    return (
      <div className="text-muted-foreground flex items-center gap-2 p-2 text-sm">
        <Loader2 className="size-4 animate-spin" /> Loading structure…
      </div>
    )
  }

  if (conductorRefs.length === 0) {
    return (
      <p className="text-muted-foreground p-2 text-sm">
        No conductors are linked to this structure yet. Link the circuit(s) it carries (a spatial_support relationship)
        and the elevation will draw them.
      </p>
    )
  }

  const { template, arranged } = buildElevation(circuits, phaseLayout)

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2 text-sm font-medium">
        <TowerControl className="size-4" />
        {circuitCountLabel(circuits.length)}
        <span className="text-muted-foreground text-xs font-normal">
          ({conductorRefs.length} conductor{conductorRefs.length === 1 ? "" : "s"} carried)
        </span>
      </div>

      {circuits.length > 0 && (
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs">
          {circuits.map((c) => (
            <button
              key={c.ref}
              type="button"
              onClick={() => openConductor(c)}
              className="hover:text-foreground flex items-center gap-1.5"
              title={`Open ${c.name}`}
            >
              <span className="size-3 rounded-full" style={{ backgroundColor: c.color }} />
              <span className="font-medium">{c.ref}</span>
              {c.voltage && <span className="text-muted-foreground">{c.voltage}</span>}
            </button>
          ))}
        </div>
      )}

      {/* The structure-wide toggle only sets the DEFAULT for circuits whose link
          records no orientation — so it's hidden once every circuit specifies its
          own (e.g. an MV-over-LV pole, where the arrangement is fully recorded). */}
      {arranged && circuits.some((c) => !c.orientation) && (
        <div className="flex items-center gap-1.5 text-xs">
          <span className="text-muted-foreground">Phases</span>
          <div className="inline-flex overflow-hidden rounded-md border">
            {(["horizontal", "vertical"] as const).map((opt) => (
              <button
                key={opt}
                type="button"
                onClick={() => setPhaseLayout(opt)}
                className={cn(
                  "px-2 py-0.5 capitalize",
                  phaseLayout === opt ? "bg-primary text-primary-foreground" : "hover:bg-muted/60",
                )}
              >
                {opt}
              </button>
            ))}
          </div>
        </div>
      )}

      <ElevationSvg template={template} circuits={circuits} onPick={highlightConductor} />

      <p className="text-muted-foreground text-xs">
        {arranged
          ? "Arranged from each circuit's recorded side, level and phase orientation on this structure. Click a phase to highlight its conductor on the map; phases are shown generically (not individually positioned)."
          : "Inferred from the circuits this structure carries — a standard crossarm type, not the exact drafted geometry. Record each circuit's side/level on its support link to arrange it faithfully. Phases are shown generically."}
      </p>
    </div>
  )
}
