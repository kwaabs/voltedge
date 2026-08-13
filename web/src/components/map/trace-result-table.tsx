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
}

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

interface ResolvedRow {
  feature_class: string
  feature_id: string
  name: string | null
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

  return (
    <div className="bg-popover text-popover-foreground fixed bottom-10 left-1/2 z-40 max-h-64 w-[28rem] max-w-[calc(100vw-2rem)] -translate-x-1/2 overflow-hidden rounded-md border shadow-lg">
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
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
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
                        ? "Matched the trace's target class"
                        : undefined
                  }
                >
                  <td className="px-3 py-1.5">{classNameFor(row.feature_class)}</td>
                  <td className="px-3 py-1.5 font-mono">{shortId(row.feature_id)}</td>
                  <td className="text-muted-foreground px-3 py-1.5">{row.name || "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
