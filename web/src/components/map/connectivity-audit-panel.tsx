import { useState } from "react"
import { Check, CheckCheck, EyeOff, List, Loader2, RefreshCw, ShieldCheck, Table2, Undo2, X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import {
  type IssueSeverityFilter,
  type IssueStatusFilter,
  type ValidationIssue,
  useReconcileIssues,
  useTriageIssue,
  useValidationIssues,
} from "@/hooks/use-validation-issues"
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

/** A readable label for a finding's kind code, for the table's Issue column. */
function kindLabel(kind: string): string {
  switch (kind) {
    case "orphaned_connectivity":
      return "Orphaned connectivity"
    case "coincident_unconnected":
      return "Coincident, unconnected"
    case "terminal_oversubscribed":
      return "Terminal over-subscribed"
    case "orphaned_relationship":
      return "Orphaned relationship"
    default:
      return kind.replace(/_/g, " ")
  }
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

/** One triage button -- a small icon action on an issue row. */
function TriageBtn({
  title,
  icon,
  onClick,
  disabled,
}: {
  title: string
  icon: React.ReactNode
  onClick: () => void
  disabled?: boolean
}) {
  return (
    <button
      type="button"
      title={title}
      onClick={onClick}
      disabled={disabled}
      className="text-muted-foreground hover:text-foreground hover:bg-muted cursor-pointer rounded p-1 disabled:opacity-50"
    >
      {icon}
      <span className="sr-only">{title}</span>
    </button>
  )
}

/** One issue row: severity, message, the two features (locatable chips), and the
 * triage actions. A live issue can be acknowledged / resolved / suppressed; a
 * resolved or suppressed one can be re-opened. */
function IssueRow({
  issue,
  classNameFor,
  isDiagramOnly,
  onTriage,
  busy,
}: {
  issue: ValidationIssue
  classNameFor: (code: string) => string
  isDiagramOnly: (code?: string) => boolean
  onTriage: (status: ValidationIssue["status"]) => void
  busy: boolean
}) {
  const closed = issue.status === "resolved" || issue.status === "suppressed"
  return (
    <li className="flex flex-col gap-1 p-2">
      <div className="flex items-start gap-2">
        <span className={cn("shrink-0 rounded px-1.5 py-0.5 text-[11px] font-medium", severityClass(issue.severity))}>
          {issue.severity}
        </span>
        <span className="text-sm">
          {issue.message}
          {issue.distance_meters != null && (
            <span className="text-muted-foreground"> ({issue.distance_meters.toFixed(2)} m)</span>
          )}
        </span>
      </div>
      <div className="flex flex-wrap items-center gap-1 pl-1">
        <FeatureChip code={issue.from_class} id={issue.from_id} diagramOnly={isDiagramOnly(issue.from_class)} classNameFor={classNameFor} />
        <FeatureChip code={issue.to_class} id={issue.to_id} diagramOnly={isDiagramOnly(issue.to_class)} classNameFor={classNameFor} />
        <span className="bg-muted/60 text-muted-foreground rounded px-1 py-0.5 text-[10px]">
          {issue.kind.startsWith("attr_") ? "data quality" : "connectivity"}
        </span>
        {issue.status === "acknowledged" && <span className="text-muted-foreground text-[11px]">· acknowledged</span>}
        {issue.status === "suppressed" && <span className="text-muted-foreground text-[11px]">· suppressed</span>}
        <div className="ml-auto flex items-center gap-0.5">
          {closed ? (
            <TriageBtn title="Re-open" icon={<Undo2 className="size-3.5" />} onClick={() => onTriage("open")} disabled={busy} />
          ) : (
            <>
              {issue.status === "open" && (
                <TriageBtn title="Acknowledge" icon={<Check className="size-3.5" />} onClick={() => onTriage("acknowledged")} disabled={busy} />
              )}
              <TriageBtn title="Mark resolved" icon={<CheckCheck className="size-3.5" />} onClick={() => onTriage("resolved")} disabled={busy} />
              <TriageBtn title="Suppress (won't re-surface)" icon={<EyeOff className="size-3.5" />} onClick={() => onTriage("suppressed")} disabled={busy} />
            </>
          )}
        </div>
      </div>
    </li>
  )
}

/** The triage view of the persistent, self-pruning issue queue
 * (ops.validation_issues). The worker keeps it current; here an operator reads the
 * live queue and acknowledges / resolves / suppresses an issue -- rather than
 * re-running a whole-database audit. "Reconcile now" kicks an immediate audit +
 * reconcile (the worker does this continuously). */
function IssueQueue({
  classNameFor,
  isDiagramOnly,
  grow,
}: {
  classNameFor: (code: string) => string
  isDiagramOnly: (code?: string) => boolean
  grow: boolean
}) {
  const [status, setStatus] = useState<IssueStatusFilter>("")
  const [severity, setSeverity] = useState<IssueSeverityFilter>("")
  // Category splits the queue by what the check is about: connectivity findings
  // (orphaned/coincident/…) vs data-quality findings (the attr_* attribute audit).
  const [category, setCategory] = useState<"" | "connectivity" | "quality">("")
  const { data, isLoading, isError } = useValidationIssues({ status, severity })
  const triage = useTriageIssue()
  const reconcile = useReconcileIssues()
  const allIssues = data?.issues ?? []
  const issues = allIssues.filter((it) => {
    const isQuality = it.kind.startsWith("attr_")
    return category === "" || (category === "quality") === isQuality
  })
  const live = status === ""

  return (
    <div className={cn("flex flex-col overflow-hidden", grow ? "min-h-0 flex-1" : "max-h-[24rem]")}>
      <div className="flex flex-wrap items-center gap-1 border-b px-2 py-1.5 text-[11px]">
        <span className="text-muted-foreground mr-0.5">Show</span>
        {(["", "open", "acknowledged", "resolved", "suppressed"] as const).map((s) => (
          <button
            key={s || "live"}
            type="button"
            onClick={() => setStatus(s)}
            className={cn(
              "cursor-pointer rounded px-1.5 py-0.5 capitalize",
              status === s ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground hover:bg-muted/70",
            )}
          >
            {s === "" ? "Live" : s}
          </button>
        ))}
        <span className="text-muted-foreground mr-0.5 ml-2">Severity</span>
        {(["", "error", "warning"] as const).map((s) => (
          <button
            key={s || "any"}
            type="button"
            onClick={() => setSeverity(s)}
            className={cn(
              "cursor-pointer rounded px-1.5 py-0.5 capitalize",
              severity === s ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground hover:bg-muted/70",
            )}
          >
            {s === "" ? "Any" : s}
          </button>
        ))}
        <span className="text-muted-foreground mr-0.5 ml-2">Kind</span>
        {(
          [
            ["", "All"],
            ["connectivity", "Connectivity"],
            ["quality", "Data quality"],
          ] as const
        ).map(([val, label]) => (
          <button
            key={val || "all"}
            type="button"
            onClick={() => setCategory(val)}
            className={cn(
              "cursor-pointer rounded px-1.5 py-0.5",
              category === val ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground hover:bg-muted/70",
            )}
          >
            {label}
          </button>
        ))}
        <Button
          size="sm"
          variant="outline"
          className="ml-auto h-6 gap-1 px-2 text-[11px]"
          onClick={() => reconcile.mutate(undefined)}
          disabled={reconcile.isPending}
          title="Run the audit now and reconcile it into the queue (the worker does this automatically)"
        >
          {reconcile.isPending ? <Loader2 className="size-3 animate-spin" /> : <RefreshCw className="size-3" />}
          Reconcile now
        </Button>
      </div>

      <div className="overflow-auto">
        {isLoading ? (
          <p className="text-muted-foreground flex items-center gap-2 p-3 text-sm">
            <Loader2 className="size-4 animate-spin" /> Loading…
          </p>
        ) : isError ? (
          <p className="text-destructive p-3 text-sm">Could not load the issue queue.</p>
        ) : issues.length === 0 ? (
          <p className="p-3 text-sm text-emerald-700 dark:text-emerald-400">
            {live ? "The live queue is clear — no open issues." : "Nothing here."}
          </p>
        ) : (
          <ul className="divide-y">
            {issues.map((it) => (
              <IssueRow
                key={it.id}
                issue={it}
                classNameFor={classNameFor}
                isDiagramOnly={isDiagramOnly}
                onTriage={(s) => triage.mutate({ id: it.id, status: s })}
                busy={triage.isPending}
              />
            ))}
          </ul>
        )}
      </div>
    </div>
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
  // The persistent issue queue is the primary, ongoing view (the worker keeps it
  // current); the on-demand whole-network audit is the secondary tab.
  const [mode, setMode] = useState<"queue" | "audit">("queue")
  const [toleranceInput, setToleranceInput] = useState("")
  const [filter, setFilter] = useState<FindingFilter>("all")
  const [severityFilter, setSeverityFilter] = useState<"all" | "error" | "warning" | "info">("all")
  // Feature-type filter: narrow to findings that touch one class, so a big result
  // doesn't need scrolling. "" = every type.
  const [typeFilter, setTypeFilter] = useState("")
  // Findings render as a scannable table by default; the flip toggles back to the
  // original card/list layout (which gives each finding's message more room).
  const [layout, setLayout] = useState<"table" | "list">("table")
  const [confirmCleanup, setConfirmCleanup] = useState(false)
  const [cleaning, setCleaning] = useState(false)
  // Anchored bottom-LEFT and stretched to the right (unlike the bottom-centre
  // analysis panels): the audit's findings table is wide, so it reads as a
  // full-width bottom bar until the user drags/resizes it.
  const fp = useFloatingPanel(600, "max-h-80", { positionClass: "bottom-[34px] left-6 right-[614px]", stretch: true })

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

  // The findings narrowed to the active category filter (the severity chips count
  // against this set), then further narrowed to the active severity.
  const categoryFindings = result
    ? result.findings.filter((f) => filter === "all" || findingCategory(f.kind) === filter)
    : []
  const shownFindings = categoryFindings.filter(
    (f) =>
      (severityFilter === "all" || f.severity === severityFilter) &&
      (typeFilter === "" || f.from_class === typeFilter || f.to_class === typeFilter),
  )
  // The feature classes that actually appear in the findings (either endpoint),
  // name-sorted -- the type dropdown's options. Built from the whole result so it
  // stays stable as the category/severity chips change.
  const findingClasses = result
    ? [...new Set(result.findings.flatMap((f) => [f.from_class, f.to_class]).filter((c): c is string => !!c))].sort(
        (a, b) => classNameFor(a).localeCompare(classNameFor(b)),
      )
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
          <p className="truncate text-sm font-medium">Network audit</p>
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

      {/* Mode tabs on the left; in audit mode the tolerance + run/cleanup controls
          sit on the SAME row (right-aligned) rather than their own strip below. */}
      <div className="flex items-center gap-2 border-b px-2 py-1 text-[11px]">
        {(["queue", "audit"] as const).map((m) => (
          <button
            key={m}
            type="button"
            onClick={() => setMode(m)}
            className={cn(
              "cursor-pointer rounded px-2 py-0.5 font-medium",
              mode === m ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground hover:bg-muted/70",
            )}
          >
            {m === "queue" ? "Issue queue" : "Run audit"}
          </button>
        ))}
        {mode === "audit" && (
          <div className="ml-auto flex items-center gap-2">
            <label className="text-muted-foreground shrink-0">Tolerance (m)</label>
            <input
              type="number"
              className={cn(inputClass, "h-6 w-16 py-0")}
              placeholder="1.0"
              value={toleranceInput}
              onChange={(e) => setToleranceInput(e.target.value)}
            />
            {orphanTotal > 0 && (
              <Button size="sm" variant="destructive" onClick={() => setConfirmCleanup(true)} disabled={loading}>
                Clean up {orphanTotal} orphaned
              </Button>
            )}
            <Button size="sm" onClick={runAudit} disabled={loading}>
              {loading ? "Running…" : result ? "Run again" : "Run audit"}
            </Button>
          </div>
        )}
      </div>

      {mode === "queue" && (
        <IssueQueue classNameFor={classNameFor} isDiagramOnly={isDiagramOnly} grow={fp.resized} />
      )}

      {mode === "audit" && (
        <>
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
            {result.summary.terminal_oversubscribed > 0 && (
              <span className="text-destructive">· {result.summary.terminal_oversubscribed} over-subscribed</span>
            )}
            <span className="text-foreground ml-2 font-medium">Relationships</span>
            <span>{result.summary.relationships_total} total</span>
            <span>· {result.summary.orphaned_relationships} orphaned</span>
            <span className="ml-2">
              · {result.summary.class_pairs_scanned} pairs · tol {result.summary.tolerance_meters} m
            </span>
            {result.summary.truncated && <span className="text-destructive">· list truncated</span>}
          </div>

          {/* Category + severity filters share ONE row (separated by a divider) with
              the view toggle pinned right -- keeps the control strip to a single row;
              flex-wrap lets it break gracefully on a narrow panel. */}
          <div className="flex flex-wrap items-center gap-x-1.5 gap-y-1 border-b px-2 py-1 text-[11px]">
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
            <span className="border-border/70 mx-0.5 h-4 border-l" aria-hidden />
            <span className="text-muted-foreground">Severity</span>
            {(["all", "error", "warning", "info"] as const).map((s) => {
              const count =
                s === "all" ? categoryFindings.length : categoryFindings.filter((x) => x.severity === s).length
              return (
                <button
                  key={s}
                  type="button"
                  onClick={() => setSeverityFilter(s)}
                  className={cn(
                    "cursor-pointer rounded px-1.5 py-0.5 capitalize",
                    severityFilter === s
                      ? "bg-primary text-primary-foreground"
                      : "bg-muted text-muted-foreground hover:bg-muted/70",
                  )}
                >
                  {s} ({count})
                </button>
              )
            })}
            {findingClasses.length > 0 && (
              <>
                <span className="border-border/70 mx-0.5 h-4 border-l" aria-hidden />
                <select
                  value={typeFilter}
                  onChange={(e) => setTypeFilter(e.target.value)}
                  className={cn(inputClass, "h-6 w-auto max-w-[11rem] py-0")}
                  title="Filter findings by feature type"
                >
                  <option value="">All types</option>
                  {findingClasses.map((c) => (
                    <option key={c} value={c}>
                      {classNameFor(c)}
                    </option>
                  ))}
                </select>
              </>
            )}
            {/* Flip between the table (default) and the original card list. */}
            <div className="ml-auto flex items-center gap-0.5">
              <button
                type="button"
                onClick={() => setLayout("table")}
                title="Table view"
                className={cn(
                  "cursor-pointer rounded p-1",
                  layout === "table"
                    ? "bg-primary text-primary-foreground"
                    : "bg-muted text-muted-foreground hover:bg-muted/70",
                )}
              >
                <Table2 className="size-3.5" />
                <span className="sr-only">Table view</span>
              </button>
              <button
                type="button"
                onClick={() => setLayout("list")}
                title="Card view"
                className={cn(
                  "cursor-pointer rounded p-1",
                  layout === "list"
                    ? "bg-primary text-primary-foreground"
                    : "bg-muted text-muted-foreground hover:bg-muted/70",
                )}
              >
                <List className="size-3.5" />
                <span className="sr-only">Card view</span>
              </button>
            </div>
          </div>

          <div className="overflow-auto">
            {result.findings.length === 0 ? (
              <p className="p-3 text-sm text-emerald-700 dark:text-emerald-400">No problems found.</p>
            ) : shownFindings.length === 0 ? (
              <p className="text-muted-foreground p-3 text-sm">No findings match these filters.</p>
            ) : layout === "table" ? (
              <table className="w-full border-collapse text-xs">
                <thead className="bg-muted/50 text-muted-foreground sticky top-0">
                  <tr className="text-left">
                    <th className="px-2 py-1.5 font-medium">Severity</th>
                    <th className="px-2 py-1.5 font-medium">Issue</th>
                    <th className="px-2 py-1.5 font-medium">Details</th>
                    <th className="px-2 py-1.5 font-medium">From</th>
                    <th className="px-2 py-1.5 font-medium">To</th>
                  </tr>
                </thead>
                <tbody>
                  {shownFindings.map((f, i) => (
                    <tr key={i} className="border-t align-top">
                      <td className="px-2 py-1.5">
                        <span
                          className={cn(
                            "inline-block rounded px-1.5 py-0.5 text-[11px] font-medium",
                            severityClass(f.severity),
                          )}
                        >
                          {f.severity}
                        </span>
                      </td>
                      <td className="px-2 py-1.5 whitespace-nowrap">{kindLabel(f.kind)}</td>
                      <td className="px-2 py-1.5">
                        {f.message}
                        {f.distance_meters != null && (
                          <span className="text-muted-foreground"> ({f.distance_meters.toFixed(2)} m)</span>
                        )}
                      </td>
                      <td className="px-2 py-1.5">
                        <FeatureChip
                          code={f.from_class}
                          id={f.from_id}
                          status={f.from_status}
                          diagramOnly={isDiagramOnly(f.from_class)}
                          classNameFor={classNameFor}
                        />
                      </td>
                      <td className="px-2 py-1.5">
                        <FeatureChip
                          code={f.to_class}
                          id={f.to_id}
                          status={f.to_status}
                          diagramOnly={isDiagramOnly(f.to_class)}
                          classNameFor={classNameFor}
                        />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
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
        </>
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
