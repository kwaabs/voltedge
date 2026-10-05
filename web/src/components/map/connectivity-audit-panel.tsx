import { useState } from "react"
import { Loader2, ShieldCheck, X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import { type ConnectivityAuditResult, useConnectivityAuditStore } from "@/stores/connectivity-audit-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function severityClass(sev: string): string {
  switch (sev) {
    case "error":
      return "bg-destructive/10 text-destructive"
    case "warning":
      return "bg-yellow-500/10 text-yellow-700 dark:text-yellow-400"
    default:
      return "bg-muted text-muted-foreground"
  }
}

type FindingFilter = "all" | "connectivity" | "relationships"

/** Which graph a finding belongs to -- orphaned_relationship is the relationship
 * graph; orphaned_connectivity and coincident_unconnected are the connectivity
 * graph. Drives the panel's category filter. */
function findingCategory(kind: string): "connectivity" | "relationships" {
  return kind === "orphaned_relationship" ? "relationships" : "connectivity"
}

/** A chip for one referenced feature. A finding can name two features (from/to);
 * each is its own chip so either can be located independently.
 *  - An UNAVAILABLE endpoint (status "deleted"/"missing") renders greyed and
 *    non-clickable with the reason, since it can't be located (it's gone).
 *  - A DIAGRAM-ONLY feature (geometryless class) is clickable but opens the
 *    feature panel without a map zoom -- flagged "(diagram)" so that's clear.
 *  - An on-map feature zooms + opens on click. */
function FeatureChip({
  code,
  id,
  status,
  diagramOnly,
  classNameFor,
}: {
  code?: string
  id?: string
  status?: string
  diagramOnly?: boolean
  classNameFor: (code: string) => string
}) {
  if (!code || !id) return null
  const label = `${classNameFor(code)} ${shortId(id)}`

  if (status) {
    return (
      <span
        className="text-muted-foreground/70 bg-muted/40 cursor-not-allowed rounded px-1.5 py-0.5 text-[11px] font-medium"
        title={`This feature is ${status}; it can no longer be located on the map.`}
      >
        {label} ({status})
      </span>
    )
  }

  return (
    <button
      type="button"
      className="bg-muted text-foreground hover:bg-primary hover:text-primary-foreground ring-border hover:ring-primary cursor-pointer rounded px-1.5 py-0.5 text-[11px] font-medium ring-1 transition-colors"
      title={
        diagramOnly
          ? "Open this feature — it's diagram-only, so it opens in the panel (no map zoom)"
          : "Locate on map"
      }
      onClick={(e) => {
        e.stopPropagation()
        useSelectionStore.getState().select(code, id, { focus: true })
      }}
    >
      {label}
      {diagramOnly && <span className="ml-1 opacity-70">(diagram)</span>}
    </button>
  )
}

/**
 * The read-only connectivity consistency audit panel (Bet 1, Component A).
 * Runs GET /connectivity/audit on demand and lists the findings -- connections
 * pointing at deleted/missing features, and features that look connected on the
 * map but aren't connected in the model. It writes nothing; it only reports.
 */
export function ConnectivityAuditPanel() {
  const open = useConnectivityAuditStore((s) => s.open)
  const loading = useConnectivityAuditStore((s) => s.loading)
  const result = useConnectivityAuditStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [toleranceInput, setToleranceInput] = useState("")
  const [filter, setFilter] = useState<FindingFilter>("all")
  const [confirmCleanup, setConfirmCleanup] = useState(false)
  const [cleaning, setCleaning] = useState(false)
  const fp = useFloatingPanel(600, "max-h-80")

  if (!open) return null

  const classNameFor = (code: string) =>
    featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  // A geometryless class only lives in diagrams -- locating opens its panel but
  // can't zoom the map. Flag it so the chip says so.
  const isDiagramOnly = (code?: string) =>
    !!code && featureClasses?.feature_classes.find((fc) => fc.code === code)?.geometry_type === "none"

  // Orphans span both graphs now (connectivity + relationships); the cleanup
  // button and dialog act on the combined total.
  const orphanTotal = result
    ? result.summary.orphaned_connectivity + result.summary.orphaned_relationships
    : 0

  // The findings list, narrowed to the active category filter.
  const shownFindings = result
    ? result.findings.filter((f) => filter === "all" || findingCategory(f.kind) === filter)
    : []

  async function runAudit() {
    useConnectivityAuditStore.getState().setLoading(true)
    try {
      const params = new URLSearchParams()
      const tol = Number.parseFloat(toleranceInput)
      if (!Number.isNaN(tol) && tol > 0) params.set("tolerance_m", String(tol))
      const qs = params.toString()
      const report = await apiFetch<ConnectivityAuditResult>(`/connectivity/audit${qs ? `?${qs}` : ""}`)
      useConnectivityAuditStore.getState().setResult(report)
    } catch (err) {
      useConnectivityAuditStore.getState().setLoading(false)
      useToastStore.getState().pushError(
        err instanceof ApiError ? err.message : "Could not run the connectivity audit.",
      )
    }
  }

  // Removes orphaned connectivity rows (those pointing at deleted/missing
  // features). A write op gated on connectivity:commit server-side; re-runs the
  // audit afterward so the counts update in place. The confirm is an AlertDialog
  // (below), matching revoke-connection-dialog's destructive-confirm pattern.
  async function runCleanup() {
    setCleaning(true)
    try {
      await apiFetch<{ removed: number }>("/connectivity/audit/cleanup", { method: "POST" })
      setConfirmCleanup(false)
      await runAudit()
    } catch (err) {
      useToastStore.getState().pushError(
        err instanceof ApiError ? err.message : "Could not clean up orphaned connections.",
      )
    } finally {
      setCleaning(false)
    }
  }

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <ShieldCheck className="size-3.5 shrink-0 text-rose-600" />
          <p className="truncate text-sm font-medium">Connectivity audit</p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          className="size-6 shrink-0"
          onClick={() => useConnectivityAuditStore.getState().closePanel()}
        >
          <X className="size-3.5" />
          <span className="sr-only">Close</span>
        </Button>
      </div>

      <div className="flex items-center gap-2 border-b p-2">
        <label className="text-muted-foreground shrink-0 text-xs">Tolerance (m)</label>
        <input
          type="number"
          className={cn(inputClass, "w-20")}
          placeholder="1.0"
          value={toleranceInput}
          onChange={(e) => setToleranceInput(e.target.value)}
        />
        <div className="ml-auto flex items-center gap-2">
          {orphanTotal > 0 && (
            <Button
              size="sm"
              variant="destructive"
              onClick={() => setConfirmCleanup(true)}
              disabled={loading}
            >
              Clean up {orphanTotal} orphaned
            </Button>
          )}
          <Button size="sm" onClick={runAudit} disabled={loading}>
            {loading ? "Running…" : result ? "Run again" : "Run audit"}
          </Button>
        </div>
      </div>

      {!result && !loading && (
        <p className="text-muted-foreground p-3 text-sm">
          A read-only check for connectivity problems across the network: connections pointing at
          deleted features, and features that look connected on the map but aren&rsquo;t connected in
          the model.
        </p>
      )}

      {result && (
        <div className={cn("flex flex-col overflow-hidden", fp.resized ? "min-h-0 flex-1" : "max-h-[22rem]")}>
          <div className="text-muted-foreground flex flex-wrap items-center gap-x-2 gap-y-1 border-b px-2 py-1.5 text-[11px]">
            <span className="text-foreground font-medium">Connectivity</span>
            <span>{result.summary.connections_total} total</span>
            <span>· {result.summary.orphaned_connectivity} orphaned</span>
            <span>· {result.summary.coincident_unconnected} coincident</span>
            <span className="text-foreground ml-2 font-medium">Relationships</span>
            <span>{result.summary.relationships_total} total</span>
            <span>· {result.summary.orphaned_relationships} orphaned</span>
            <span className="ml-2">
              · {result.summary.class_pairs_scanned} pairs · tol {result.summary.tolerance_meters} m
            </span>
            {result.summary.truncated && <span className="text-destructive">· list truncated</span>}
          </div>

          <div className="flex items-center gap-1 border-b px-2 py-1 text-[11px]">
            {(["all", "connectivity", "relationships"] as const).map((f) => {
              const count =
                f === "all"
                  ? result.findings.length
                  : result.findings.filter((x) => findingCategory(x.kind) === f).length
              return (
                <button
                  key={f}
                  type="button"
                  onClick={() => setFilter(f)}
                  className={cn(
                    "cursor-pointer rounded px-1.5 py-0.5 capitalize",
                    filter === f
                      ? "bg-primary text-primary-foreground"
                      : "bg-muted text-muted-foreground hover:bg-muted/70",
                  )}
                >
                  {f} ({count})
                </button>
              )
            })}
          </div>

          <div className="overflow-y-auto">
            {result.findings.length === 0 ? (
              <p className="p-3 text-sm text-emerald-700 dark:text-emerald-400">No problems found.</p>
            ) : shownFindings.length === 0 ? (
              <p className="text-muted-foreground p-3 text-sm">No {filter} findings.</p>
            ) : (
              <ul className="divide-y">
                {shownFindings.map((f, i) => (
                  <li key={i} className="flex flex-col gap-1 p-2">
                    <div className="flex items-start gap-2">
                      <span
                        className={cn(
                          "shrink-0 rounded px-1.5 py-0.5 text-[11px] font-medium",
                          severityClass(f.severity),
                        )}
                      >
                        {f.severity}
                      </span>
                      <span className="text-sm">{f.message}</span>
                    </div>
                    <div className="flex flex-wrap gap-1 pl-1">
                      <FeatureChip
                        code={f.from_class}
                        id={f.from_id}
                        status={f.from_status}
                        diagramOnly={isDiagramOnly(f.from_class)}
                        classNameFor={classNameFor}
                      />
                      <FeatureChip
                        code={f.to_class}
                        id={f.to_id}
                        status={f.to_status}
                        diagramOnly={isDiagramOnly(f.to_class)}
                        classNameFor={classNameFor}
                      />
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </div>

          {result.limitations.length > 0 && (
            <details className="text-muted-foreground border-t px-2 py-1.5 text-[11px]">
              <summary className="cursor-pointer select-none">How to read these findings</summary>
              <ul className="mt-1 list-disc pl-4">
                {result.limitations.map((l, i) => (
                  <li key={i}>{l}</li>
                ))}
              </ul>
            </details>
          )}
        </div>
      )}
      {fp.resizeHandle}

      <AlertDialog
        open={confirmCleanup}
        onOpenChange={(next) => !cleaning && setConfirmCleanup(next)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Clean up orphaned records?</AlertDialogTitle>
            <AlertDialogDescription>
              This permanently removes {orphanTotal} orphaned record{orphanTotal === 1 ? "" : "s"}
              &mdash; connections and relationships that point at deleted or missing features. It
              cannot be undone; a soft-deleted feature that is later restored would come back unwired.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={cleaning}>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                e.preventDefault()
                runCleanup()
              }}
              disabled={cleaning}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90 focus-visible:ring-destructive/20"
            >
              {cleaning && <Loader2 className="size-4 animate-spin" />}
              Clean up
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}
