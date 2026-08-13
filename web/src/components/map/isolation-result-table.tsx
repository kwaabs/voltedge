import { useEffect, useState } from "react"
import { X, Zap } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useIsolateStore } from "@/stores/isolate-store"
import { useSelectionStore } from "@/stores/selection-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

interface ResolvedRow {
  feature_class: string
  feature_id: string
  name: string | null
  isBoundary: boolean
}

/**
 * Floating data table for an isolation result (api/internal/trace/isolate.go,
 * see docs/phase-4-utility-traces.md) -- same fixed bottom-center
 * position and table shape as trace-result-table.tsx, distinguished by
 * its own store (isolate-store.ts) since the two workflows are
 * mutually exclusive (running one clears the other) rather than ever
 * shown together. The upstream boundary device, when one exists, is
 * its own visually distinct row (matching "isolation-boundary"'s map
 * highlight color) -- everything else listed is the de-energized set.
 */
export function IsolationResultTable() {
  const result = useIsolateStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [rows, setRows] = useState<ResolvedRow[] | null>(null)

  useEffect(() => {
    if (!result) {
      setRows(null)
      return
    }

    let cancelled = false
    const isolation: NonNullable<typeof result> = result

    async function loadNames() {
      const refs = isolation.upstream_boundary
        ? [...isolation.de_energized.map((ref) => ({ ref, isBoundary: false })), { ref: isolation.upstream_boundary, isBoundary: true }]
        : isolation.de_energized.map((ref) => ({ ref, isBoundary: false }))

      const resolved = await Promise.all(
        refs.map(async ({ ref, isBoundary }) => {
          let name: string | null = null
          try {
            const feature = await apiFetch<{ name: string | null }>(`/features/${ref.feature_class}/${ref.feature_id}`)
            name = feature.name
          } catch {
            // A row whose name fails to load individually still shows
            // up with its class/id -- not worth failing the whole table
            // over one feature's name.
          }
          return { feature_class: ref.feature_class, feature_id: ref.feature_id, name, isBoundary }
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

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  return (
    <div className="bg-popover text-popover-foreground fixed bottom-10 left-1/2 z-40 max-h-64 w-[28rem] max-w-[calc(100vw-2rem)] -translate-x-1/2 overflow-hidden rounded-md border shadow-lg">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <div className="flex items-center gap-1.5 min-w-0">
          <Zap className="text-muted-foreground size-3.5 shrink-0" />
          <p className="truncate text-sm font-medium">Isolation</p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          className="size-6 shrink-0"
          onClick={() => useIsolateStore.getState().clear()}
        >
          <X className="size-3.5" />
          <span className="sr-only">Clear isolation</span>
        </Button>
      </div>

      <div className="max-h-[13rem] overflow-y-auto">
        {rows === null ? (
          <p className="text-muted-foreground p-3 text-sm">Loading…</p>
        ) : rows.length === 0 ? (
          <p className="text-muted-foreground p-3 text-sm">Nothing would de-energize.</p>
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
                    row.isBoundary && "bg-violet-500/10 text-violet-700 hover:bg-violet-500/15 dark:text-violet-400",
                  )}
                  title={row.isBoundary ? "Nearest upstream protective device" : "Would de-energize"}
                >
                  <td className="px-3 py-1.5">{classNameFor(row.feature_class)}</td>
                  <td className="px-3 py-1.5 font-mono">{shortId(row.feature_id)}</td>
                  <td className="text-muted-foreground px-3 py-1.5">
                    {row.name || "—"}
                    {row.isBoundary && " (boundary)"}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  )
}
