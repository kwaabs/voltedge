import { useEffect, useRef, useState } from "react"
import { GripHorizontal, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { ServiceRollupSummary } from "@/components/map/service-rollup-summary"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"
import { useTraceStore } from "@/stores/trace-store"

const DIRECTION_LABEL: Record<string, string> = {
  CONNECTED: "Connected",
  DOWNSTREAM: "Downstream",
  UPSTREAM: "Upstream",
  SHORTEST_PATH: "Shortest path",
  // Not real api/internal/trace direction codes -- feature-panel.tsx's
  // feeder/section triggers hit their own dedicated endpoints
  // (POST /trace/feeder, POST /trace/section) and normalize their
  // differently-shaped responses into this same TraceResult display
  // rather than building two more result tables from scratch; these
  // two synthetic labels are what that normalization uses in place of
  // a real trace_configurations direction.
  FEEDER: "Feeder",
  SECTION: "Section",
  // Not a real trace_configurations direction either -- feature-panel.tsx's
  // "Supply path" trigger hits POST /trace/supply-path (a fixed barrier-free
  // upstream walk) and normalizes its response into this same TraceResult
  // display, with the serving transformer / switching devices / source as
  // `targets`.
  SUPPLY_PATH: "Supply path",
  // Not a real trace_configurations direction -- feature-panel.tsx's "Service
  // trace" trigger hits POST /trace/service (a downstream walk that collects
  // the reached SERVICE_POINTs) and normalizes its response into this same
  // display, with the consumption rollup surfaced by ServiceRollupSummary and
  // the reached service points listed as `features`/`targets`.
  SERVICE: "Service",
}

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

/** Renders volts compactly: kV once at/over 1000 (11kV, 0.4kV), plain V
 * below (400 V). Trailing-zero-trimmed so 11000 -> "11 kV", not
 * "11.00 kV". Used for both a feature's stored nominal voltage and its
 * computed load-flow voltage so the two columns read the same way. */
function formatVolts(v: number): string {
  if (v >= 1000) {
    return `${Number((v / 1000).toFixed(2))} kV`
  }
  return `${Number(v.toFixed(1))} V`
}

interface ResolvedRow {
  feature_class: string
  feature_id: string
  name: string | null
  circuitRef: string | null
  nominalVoltageV: number | null
  isBarrier: boolean
  isTarget: boolean
}

/** Default anchor (bottom-center) used until the user drags the panel
 * somewhere else -- the same spot the count-only banner it replaced used,
 * clear of the attribute panel's bottom-right corner and the top-center
 * edit toolbar. Dropped the moment `position` is set (a manual drag). */
const DEFAULT_POSITION_CLASS = "bottom-10 left-1/2 -translate-x-1/2"

/** Floors a manual resize can't shrink past -- small enough to still show the
 * header plus a row or two, not so small the table collapses. The default
 * width is computed per-render (wider when a computed-voltage column shows). */
const MIN_WIDTH = 320
const MIN_HEIGHT = 160

/**
 * Floating data table listing every feature a trace reached -- feature
 * class, id, and (if the feature has one) its human-assigned name --
 * replacing an earlier count-only summary banner. Opens bottom-center
 * (DEFAULT_POSITION_CLASS), but is draggable (grab the header) and
 * resizable (drag the bottom-right grip) -- the same mechanics
 * feature-panel.tsx uses -- so it can be moved clear of whatever the user
 * is working on and grown to show a long trace. `position`/`size` live in
 * this component's own state, which React preserves across re-renders (and
 * across successive traces, since the component stays mounted and only
 * returns null when there's no result), so a panel the user has moved or
 * sized stays put for the next trace rather than snapping back.
 * Self-contained, same pattern as every other floating overlay in this app
 * -- reads trace-store.ts directly and returns null rather than being
 * conditionally rendered by a parent.
 *
 * Resolves each row's name with its own fetch, independent of
 * voltedge-map.tsx's own per-feature fetch for the highlight geometry
 * -- the two components have different jobs (this one only needs
 * `name`, the map only needs `geometry`) and neither depends on the
 * other's result, so there's no shared state worth coordinating; a
 * handful of duplicate GETs for a trace's typically-small feature count
 * is a fine trade against that complexity.
 */
export function TraceResultTable() {
  const result = useTraceStore((s) => s.result)
  const computedVoltages = useTraceStore((s) => s.computedVoltages)
  const { data: featureClasses } = useFeatureClasses()
  const [rows, setRows] = useState<ResolvedRow[] | null>(null)
  const [position, setPosition] = useState<{ x: number; y: number } | null>(null)
  const [size, setSize] = useState<{ width: number; height: number } | null>(null)
  const panelRef = useRef<HTMLDivElement>(null)

  // Grab the header and drag -- pointer-capture mechanics identical to
  // feature-panel.tsx's own header drag. `origX/origY` come from the live
  // bounding rect when the panel is still at its default anchor (position
  // null), so the first drag continues smoothly from wherever the anchor
  // put it rather than jumping.
  function handleHeaderPointerDown(e: React.PointerEvent) {
    const rect = panelRef.current?.getBoundingClientRect()
    if (!rect) return
    const startX = e.clientX
    const startY = e.clientY
    const origX = position?.x ?? rect.left
    const origY = position?.y ?? rect.top

    const handleMove = (moveEvt: PointerEvent) => {
      setPosition({ x: origX + (moveEvt.clientX - startX), y: origY + (moveEvt.clientY - startY) })
    }
    const handleUp = () => {
      window.removeEventListener("pointermove", handleMove)
      window.removeEventListener("pointerup", handleUp)
    }
    window.addEventListener("pointermove", handleMove)
    window.addEventListener("pointerup", handleUp)
  }

  // Same mechanics, writing `size` instead of `position`. Pin the panel to
  // its current top-left first if it's still on the centered default anchor,
  // so a bottom-right resize grows down/right intuitively rather than the
  // centered panel appearing to grow symmetrically off its anchor. Floored
  // at MIN_WIDTH/MIN_HEIGHT.
  function handleResizePointerDown(e: React.PointerEvent) {
    e.stopPropagation()
    const rect = panelRef.current?.getBoundingClientRect()
    if (!rect) return
    if (!position) setPosition({ x: rect.left, y: rect.top })
    const startX = e.clientX
    const startY = e.clientY
    const startWidth = size?.width ?? rect.width
    const startHeight = size?.height ?? rect.height

    const handleMove = (moveEvt: PointerEvent) => {
      setSize({
        width: Math.max(MIN_WIDTH, startWidth + (moveEvt.clientX - startX)),
        height: Math.max(MIN_HEIGHT, startHeight + (moveEvt.clientY - startY)),
      })
    }
    const handleUp = () => {
      window.removeEventListener("pointermove", handleMove)
      window.removeEventListener("pointerup", handleUp)
    }
    window.addEventListener("pointermove", handleMove)
    window.addEventListener("pointerup", handleUp)
  }

  useEffect(() => {
    if (!result) {
      setRows(null)
      return
    }

    let cancelled = false
    // Reassigned into a typed local -- TypeScript's control-flow
    // narrowing from the `if (!result)` check above doesn't propagate
    // into the nested async function below.
    const trace: NonNullable<typeof result> = result
    const barrierKeys = new Set(trace.barriers.map((b) => `${b.feature_class}:${b.feature_id}`))
    const targetKeys = new Set((trace.targets ?? []).map((t) => `${t.feature_class}:${t.feature_id}`))

    async function loadNames() {
      const resolved = await Promise.all(
        trace.features.map(async (ref) => {
          let name: string | null = null
          try {
            const feature = await apiFetch<{ name: string | null }>(`/features/${ref.feature_class}/${ref.feature_id}`)
            name = feature.name
          } catch {
            // A row whose name fails to load individually still shows
            // up with its class/id -- not worth failing the whole table
            // over one feature's name.
          }
          const key = `${ref.feature_class}:${ref.feature_id}`
          return {
            feature_class: ref.feature_class,
            feature_id: ref.feature_id,
            name,
            // circuit_ref / nominal_voltage_v ride along on the trace
            // response's own feature entries (api/internal/trace's
            // tracedFeature) -- no extra fetch; the per-row GET above is
            // only for the name. Absent when the reached class carries
            // neither a circuit_id field nor any voltage-class field.
            circuitRef: ref.circuit_ref ?? null,
            nominalVoltageV: ref.nominal_voltage_v ?? null,
            isBarrier: barrierKeys.has(key),
            isTarget: targetKeys.has(key),
          }
        }),
      )
      if (!cancelled) setRows(resolved)
    }

    setRows(null)
    loadNames()
    return () => {
      cancelled = true
    }
  }, [result])

  if (!result) return null

  const directionLabel = DIRECTION_LABEL[result.direction] ?? result.direction
  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code
  // Only give computed voltage its own column when a load-flow result for
  // this trace's start was snapshotted at trace time (feature-panel.tsx) --
  // otherwise the whole column would be "—" noise. Nominal voltage always
  // shows: it's topology-intrinsic and needs no solve.
  const hasComputed = computedVoltages !== null

  // Default width matches the former fixed w-[46rem]/w-[38rem] classes (px, so
  // a manual resize has a live numeric value to grow from). Once resized the
  // panel takes an explicit height; until then it shrink-wraps under max-h-64.
  const defaultWidth = hasComputed ? 736 : 608
  const resized = size?.height != null
  const shellStyle: React.CSSProperties = {
    ...(position ? { left: position.x, top: position.y } : {}),
    width: size?.width ?? defaultWidth,
    ...(resized ? { height: size?.height } : {}),
  }

  return (
    <div
      ref={panelRef}
      style={shellStyle}
      className={cn(
        "bg-popover text-popover-foreground fixed z-40 flex flex-col max-w-[calc(100vw-2rem)] overflow-hidden rounded-lg border border-border/70 border-t-2 border-t-[oklch(0.45_0.10_252)] dark:border-t-primary shadow-xl ring-1 ring-black/5",
        !resized && "max-h-64",
        !position && DEFAULT_POSITION_CLASS,
      )}
    >
      <div
        onPointerDown={handleHeaderPointerDown}
        className="flex cursor-grab items-center justify-between gap-2 border-b border-[oklch(0.45_0.10_252)]/20 bg-gradient-to-b from-[oklch(0.45_0.10_252)]/15 to-[oklch(0.45_0.10_252)]/5 dark:border-primary/15 dark:from-accent/50 dark:to-accent/20 px-3 py-2 select-none active:cursor-grabbing"
      >
        <div className="flex min-w-0 items-center gap-1.5">
          <GripHorizontal className="text-primary/70 size-3.5 shrink-0" />
          <div className="min-w-0">
            <p className="truncate text-sm font-medium">{directionLabel} trace</p>
            {result.direction === "SHORTEST_PATH" && (
              <p className="text-muted-foreground text-xs">{result.path_found ? "Path found" : "No path found"}</p>
            )}
          </div>
        </div>
        <Button
          variant="ghost"
          size="icon"
          className="size-6 shrink-0"
          onClick={() => useTraceStore.getState().clear()}
        >
          <X className="size-3.5" />
          <span className="sr-only">Clear trace</span>
        </Button>
      </div>

      {/* Phase 62 consumption rollup -- renders nothing when the trace reached
          no SERVICE_POINTs, so purely-topological traces are unaffected. */}
      <ServiceRollupSummary service={result.service} />

      <div className={cn("overflow-y-auto", resized ? "min-h-0 flex-1" : "max-h-[13rem]")}>
        {rows === null ? (
          <p className="text-muted-foreground p-3 text-sm">Loading…</p>
        ) : (
          <table className="w-full text-sm">
            <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
              <tr>
                <th className="bg-popover px-3 py-1.5 font-medium">Feature class</th>
                <th className="bg-popover px-3 py-1.5 font-medium">Feature ID</th>
                <th className="bg-popover px-3 py-1.5 font-medium">Name</th>
                <th className="bg-popover px-3 py-1.5 font-medium">Circuit</th>
                <th className="bg-popover px-3 py-1.5 font-medium">Nominal V</th>
                {hasComputed && <th className="bg-popover px-3 py-1.5 font-medium">Computed V</th>}
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => {
                const computed = computedVoltages?.[`${row.feature_class}:${row.feature_id}`]
                return (
                <tr
                  key={`${row.feature_class}:${row.feature_id}`}
                  onClick={() => useSelectionStore.getState().select(row.feature_class, row.feature_id, { focus: true })}
                  className={cn(
                    "hover:bg-accent cursor-pointer border-b last:border-b-0",
                    row.isBarrier && "bg-destructive/10 text-destructive hover:bg-destructive/15",
                    !row.isBarrier &&
                      row.isTarget &&
                      "bg-yellow-500/10 text-yellow-700 hover:bg-yellow-500/15 dark:text-yellow-400",
                  )}
                  title={
                    row.isBarrier
                      ? "Trace stopped here (open device)"
                      : row.isTarget
                        ? "Notable for this trace (a matched target class, feeder head, or section boundary)"
                        : undefined
                  }
                >
                  <td className="px-3 py-1.5">{classNameFor(row.feature_class)}</td>
                  <td className="px-3 py-1.5 font-mono">{shortId(row.feature_id)}</td>
                  <td className="text-muted-foreground px-3 py-1.5">{row.name || "—"}</td>
                  <td className="text-muted-foreground px-3 py-1.5">{row.circuitRef || "—"}</td>
                  <td className="text-muted-foreground px-3 py-1.5 tabular-nums">
                    {row.nominalVoltageV != null ? formatVolts(row.nominalVoltageV) : "—"}
                  </td>
                  {hasComputed && (
                    <td className="text-muted-foreground px-3 py-1.5 tabular-nums">
                      {computed != null ? formatVolts(computed) : "—"}
                    </td>
                  )}
                </tr>
                )
              })}
            </tbody>
          </table>
        )}
      </div>

      {/* Bottom-right resize grip -- same affordance and mechanics as
          feature-panel.tsx's. stopPropagation in the handler keeps a resize
          drag from also starting a header move. */}
      <div
        onPointerDown={handleResizePointerDown}
        title="Resize"
        style={{ touchAction: "none" }}
        className="absolute right-0 bottom-0 z-50 flex size-4 cursor-nwse-resize items-end justify-end p-0.5"
      >
        <span className="border-muted-foreground/40 size-2 border-r-2 border-b-2" />
      </div>
    </div>
  )
}
