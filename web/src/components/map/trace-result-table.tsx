import { useEffect, useState } from "react"
import { X } from "lucide-react"

import { Button } from "@/components/ui/button"
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

/**
 * Floating data table listing every feature a trace reached -- feature
 * class, id, and (if the feature has one) its human-assigned name --
 * replacing an earlier count-only summary banner. Fixed bottom-center,
 * same position the banner it replaces used (clear of the floating
 * attribute panel's own default corner and geometry-edit-toolbar.tsx's
 * top-center). Self-contained, same pattern as every other floating
 * overlay in this app -- reads trace-store.ts directly and returns null
 * rather than being conditionally rendered by a parent.
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

  return (
    <div
      className={cn(
        "bg-popover text-popover-foreground fixed bottom-10 left-1/2 z-40 max-h-64 max-w-[calc(100vw-2rem)] -translate-x-1/2 overflow-hidden rounded-md border shadow-lg",
        hasComputed ? "w-[46rem]" : "w-[38rem]",
      )}
    >
      <div className="flex items-center justify-between border-b px-3 py-2">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">{directionLabel} trace</p>
          {result.direction === "SHORTEST_PATH" && (
            <p className="text-muted-foreground text-xs">{result.path_found ? "Path found" : "No path found"}</p>
          )}
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

      <div className="max-h-[13rem] overflow-y-auto">
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
    </div>
  )
}
