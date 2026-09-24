import { useEffect, useState } from "react"
import { AlertTriangle, ShieldAlert, X, Zap } from "lucide-react"

import { Button } from "@/components/ui/button"
import { ServiceRollupSummary } from "@/components/map/service-rollup-summary"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useIsolateStore } from "@/stores/isolate-store"
import { useSelectionStore } from "@/stores/selection-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

type RowKind = "deenergized" | "boundary" | "backfeed"

interface ResolvedRow {
  feature_class: string
  feature_id: string
  name: string | null
  kind: RowKind
  isCritical: boolean
}

/**
 * Floating data table for an isolation result (api/internal/trace/isolate.go,
 * see docs/phase-4-utility-traces.md) -- same fixed bottom-center
 * position and table shape as trace-result-table.tsx, distinguished by
 * its own store (isolate-store.ts) since the two workflows are
 * mutually exclusive (running one clears the other) rather than ever
 * shown together. Three distinct row treatments: the upstream
 * boundary device (violet, matching "isolation-boundary"'s map
 * highlight color), a critical facility within the de-energized set
 * (flagged inline, not a separate color -- it's still part of what
 * would de-energize, not a different category of node), and a
 * possible backfeed path (red -- an external node this isolation
 * didn't already account for, appended as its own rows since it isn't
 * part of the de-energized set itself). A summary line above the table
 * calls out critical/backfeed counts so neither is easy to miss by
 * only skimming individual rows.
 */
export function IsolationResultTable() {
  const result = useIsolateStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [rows, setRows] = useState<ResolvedRow[] | null>(null)
  const fp = useFloatingPanel(448, "max-h-80")

  useEffect(() => {
    if (!result) {
      setRows(null)
      return
    }

    let cancelled = false
    const isolation: NonNullable<typeof result> = result

    async function loadNames() {
      const criticalKeys = new Set(
        (isolation.critical_facilities ?? []).map((r) => `${r.feature_class}:${r.feature_id}`),
      )
      const refs: { ref: { feature_class: string; feature_id: string }; kind: RowKind }[] = [
        ...isolation.de_energized.map((ref) => ({ ref, kind: "deenergized" as const })),
        ...(isolation.upstream_boundary ? [{ ref: isolation.upstream_boundary, kind: "boundary" as const }] : []),
        ...(isolation.possible_backfeed_paths ?? []).map((ref) => ({ ref, kind: "backfeed" as const })),
      ]

      const resolved = await Promise.all(
        refs.map(async ({ ref, kind }) => {
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
          return { feature_class: ref.feature_class, feature_id: ref.feature_id, name, kind, isCritical: criticalKeys.has(key) }
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
  const criticalCount = result.critical_facilities?.length ?? 0
  const backfeedCount = result.possible_backfeed_paths?.length ?? 0

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
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

      {(criticalCount > 0 || backfeedCount > 0) && (
        <div className="flex flex-col gap-1 border-b px-3 py-2 text-xs">
          {criticalCount > 0 && (
            <p className="text-destructive flex items-center gap-1.5">
              <ShieldAlert className="size-3.5 shrink-0" />
              {criticalCount} critical facilit{criticalCount === 1 ? "y" : "ies"} affected
            </p>
          )}
          {backfeedCount > 0 && (
            <p className="text-destructive flex items-center gap-1.5">
              <AlertTriangle className="size-3.5 shrink-0" />
              {backfeedCount} possible backfeed path{backfeedCount === 1 ? "" : "s"} -- verify before work
            </p>
          )}
        </div>
      )}

      {/* Phase 62: the load this isolation would drop (customers + kW, critical
          subset, per-tier). Renders nothing if no service point de-energizes. */}
      <ServiceRollupSummary service={result.service} variant="deenergized" />

      <div className={cn("overflow-y-auto", fp.resized ? "min-h-0 flex-1" : "max-h-[13rem]")}>
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
                  key={`${row.kind}:${row.feature_class}:${row.feature_id}`}
                  onClick={() => useSelectionStore.getState().select(row.feature_class, row.feature_id, { focus: true })}
                  className={cn(
                    "hover:bg-accent cursor-pointer border-b last:border-b-0",
                    row.kind === "boundary" && "bg-violet-500/10 text-violet-700 hover:bg-violet-500/15 dark:text-violet-400",
                    row.kind === "backfeed" && "bg-destructive/10 text-destructive hover:bg-destructive/15",
                  )}
                  title={
                    row.kind === "boundary"
                      ? "Nearest upstream protective device"
                      : row.kind === "backfeed"
                        ? "Possible backfeed path -- a live connection not accounted for by this isolation"
                        : row.isCritical
                          ? "Would de-energize -- critical facility"
                          : "Would de-energize"
                  }
                >
                  <td className="px-3 py-1.5">{classNameFor(row.feature_class)}</td>
                  <td className="px-3 py-1.5 font-mono">{shortId(row.feature_id)}</td>
                  <td className="text-muted-foreground px-3 py-1.5">
                    <span className="inline-flex items-center gap-1">
                      {row.isCritical && <ShieldAlert className="text-destructive size-3.5 shrink-0" />}
                      {row.name || "—"}
                      {row.kind === "boundary" && " (boundary)"}
                      {row.kind === "backfeed" && " (possible backfeed)"}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
      {fp.resizeHandle}
    </div>
  )
}
