import { useEffect, useMemo, useState } from "react"
import { AlertTriangle, ArrowRight, BellOff, CheckCheck, CheckCircle2, ChevronRight, Eye, Filter, Loader2, MapPin, PencilLine, ShieldCheck, UserPlus, X } from "lucide-react"

import { formatFieldLabel, formatValue } from "@/components/map/field-utils"
import { FloatingPanel } from "@/components/ui/floating-panel"
import { usePermission } from "@/hooks/use-auth"
import { useAuditFeatures } from "@/hooks/use-audit-features"
import { type FeatureClassSchema, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { type FeatureHistoryEntry, useFeatureHistory } from "@/hooks/use-feature-history"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useMyActivity } from "@/hooks/use-my-activity"
import { type ValidationIssue, useAssignIssue, useReconcileIssues, useTriageIssue, useValidationIssues } from "@/hooks/use-validation-issues"
import { type AssignableUser, useAssignableUsers } from "@/hooks/use-wam"
import { cn } from "@/lib/utils"
import { useMyEditsStore } from "@/stores/my-edits-store"
import { useSelectionStore } from "@/stores/selection-store"

// The "My edits" floating panel as a NESTED TREE: feature -> each of your changes
// to it -> the fields that change touched. The self-service complement to the
// admin-only edit-session roll-up (/admin/edit-sessions) and activity audit. The
// feature list comes from GET /features/my-activity (lightweight, actor pinned to
// the caller); expanding a feature lazily fetches its full change timeline (GET
// /features/{code}/{id}/history, which carries the stored snapshots) and the field
// diffs are computed here by comparing each change's snapshot to the one before.

/** The actions a user's own edits can carry, for the Action filter. */
const ACTIONS = ["create", "update", "publish", "unpublish", "delete", "restore", "relate", "unrelate"] as const

/** relate/unrelate snapshots hold relationship context, NOT a feature-state
 * snapshot, so they're excluded from the diff chain and rendered specially. */
const RELATION_ACTIONS = new Set(["relate", "unrelate"])
/** The actions whose change actually moves attribute/status values and so earns a
 * field-level diff. create (initial values), delete/restore/purge (status only)
 * and the relation verbs render as a plain leaf instead. */
const DIFF_ACTIONS = new Set(["update", "publish", "unpublish", "stage_edit"])

// Top-level snapshot keys that are bookkeeping, not user-meaningful attributes:
// ids and audit stamps, the publish/territory plumbing (a raw uuid/timestamp diff
// helps no one -- the action chip already says "Published", and a geometry move is
// reported separately), and the geometry/attributes containers handled on their own.
const TOP_SKIP = new Set([
  "id", "created_at", "updated_at", "created_by", "updated_by",
  "published_at", "published_by", "deleted_at", "territory_id",
  "geometry", "attributes",
])

const CLASS_ACRONYMS = new Set([
  "MV", "LV", "HV", "EHV", "UHV", "CT", "VT", "AC", "DC", "EV", "PV", "DER",
  "RMU", "URD", "BESS", "LVAC", "OLTC", "ANSI", "GPS", "CIM", "AMI", "SCADA",
])
function humanizeClass(code: string): string {
  return code
    .split("_")
    .map((w) => (w.length <= 2 || CLASS_ACRONYMS.has(w) ? w : w.charAt(0) + w.slice(1).toLowerCase()))
    .join(" ")
}

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function toRFC3339(local: string): string | undefined {
  if (!local) return undefined
  const d = new Date(local)
  return Number.isNaN(d.getTime()) ? undefined : d.toISOString()
}

/** Local midnight today as a datetime-local value ("YYYY-MM-DDTHH:mm"). The From
 * filter defaults to this, so the panel opens on *today's* edits -- the common
 * case -- and the user widens the range from there (or clears it for all time). */
function startOfTodayInput(): string {
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T00:00`
}

function timeOfDay(d: Date): string {
  return d.toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })
}

/** A compact "latest touched" stamp for a feature header: time-of-day if today,
 * else a short date. */
function shortWhen(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ""
  if (d.toDateString() === new Date().toDateString()) return timeOfDay(d)
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" })
}

function actionLabel(action: string): { label: string; className: string } {
  switch (action) {
    case "create":
      return { label: "Created", className: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400" }
    case "update":
      return { label: "Edited", className: "bg-blue-500/15 text-blue-600 dark:text-blue-400" }
    case "publish":
      return { label: "Published", className: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400" }
    case "unpublish":
      return { label: "Unpublished", className: "bg-amber-500/15 text-amber-600 dark:text-amber-400" }
    case "delete":
      return { label: "Deleted", className: "bg-destructive/15 text-destructive" }
    case "restore":
      return { label: "Restored", className: "bg-blue-500/15 text-blue-600 dark:text-blue-400" }
    case "relate":
      return { label: "Linked", className: "bg-indigo-500/15 text-indigo-600 dark:text-indigo-400" }
    case "unrelate":
      return { label: "Unlinked", className: "bg-amber-500/15 text-amber-600 dark:text-amber-400" }
    case "stage_edit":
      return { label: "Private edit", className: "bg-amber-500/15 text-amber-600 dark:text-amber-400" }
    case "stage_delete":
      return { label: "Private delete", className: "bg-amber-500/15 text-amber-600 dark:text-amber-400" }
    case "discard":
      return { label: "Discarded", className: "bg-muted text-muted-foreground" }
    default:
      return { label: action, className: "bg-muted text-muted-foreground" }
  }
}

// --- snapshot diffing -------------------------------------------------------

interface FieldChange {
  field: string
  from: unknown
  to: unknown
}
interface SnapshotDiff {
  fields: FieldChange[]
  geomChanged: boolean
}

function isScalarish(v: unknown): boolean {
  return v === null || ["string", "number", "boolean"].includes(typeof v)
}

/** Flatten a feature-state snapshot to its displayable scalar fields: the coded
 * attributes (under `attributes`) plus top-level scalars like name/status/
 * lifecycle_status, minus bookkeeping columns. */
function flattenSnapshot(snap: unknown): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  if (!snap || typeof snap !== "object") return out
  const s = snap as Record<string, unknown>
  for (const [k, v] of Object.entries(s)) {
    if (k === "attributes" && v && typeof v === "object") {
      for (const [ak, av] of Object.entries(v as Record<string, unknown>)) out[ak] = av
    } else if (!TOP_SKIP.has(k) && isScalarish(v)) {
      out[k] = v
    }
  }
  return out
}

function norm(v: unknown): string {
  return v === null || v === undefined ? "" : String(v)
}

/** The fields that differ between a change's snapshot and the previous one. */
function diffSnapshots(prev: unknown, cur: unknown): SnapshotDiff {
  const a = flattenSnapshot(prev)
  const b = flattenSnapshot(cur)
  const keys = new Set([...Object.keys(a), ...Object.keys(b)])
  const fields: FieldChange[] = []
  for (const k of keys) {
    if (norm(a[k]) !== norm(b[k])) fields.push({ field: k, from: a[k], to: b[k] })
  }
  fields.sort((x, y) => x.field.localeCompare(y.field))
  const pg = (prev as { geometry?: unknown } | null)?.geometry
  const cg = (cur as { geometry?: unknown } | null)?.geometry
  return { fields, geomChanged: JSON.stringify(pg) !== JSON.stringify(cg) }
}

/** Map each change's history id to its field diff vs the previous feature-state
 * snapshot. Relation changes are skipped in the chain (their snapshot is a
 * relationship, not a feature state) so an edit after a link still diffs against
 * the real previous attributes. */
function buildDiffMap(history: FeatureHistoryEntry[]): Map<number, SnapshotDiff> {
  const map = new Map<number, SnapshotDiff>()
  const ordered = [...history].reverse().filter((e) => !RELATION_ACTIONS.has(e.action)) // oldest -> newest
  let prev: unknown = undefined
  for (const e of ordered) {
    map.set(e.id, diffSnapshots(prev, e.snapshot))
    prev = e.snapshot
  }
  return map
}

// --- tree nodes -------------------------------------------------------------

/** The field-diff body of a change: one "label  old → new" row per changed field,
 * with coded values resolved to their domain labels when the schema is known. */
function FieldDiffList({ diff, schema }: { diff: SnapshotDiff | undefined; schema: FeatureClassSchema | undefined }) {
  if (!diff || (diff.fields.length === 0 && !diff.geomChanged)) {
    return <div className="text-muted-foreground border-t px-2 py-1 text-xs">No attribute changes recorded.</div>
  }
  const valueText = (field: string, v: unknown): string => {
    if (v === null || v === undefined || v === "") return "—"
    const f = schema?.fields.find((x) => x.name === field)
    return f?.domain_value_labels?.[String(v)] ?? formatValue(v)
  }
  return (
    <ul className="flex flex-col gap-0.5 border-t px-2 py-1 text-xs">
      {diff.fields.map(({ field, from, to }) => (
        <li key={field} className="flex flex-wrap items-baseline gap-1">
          <span className="text-muted-foreground">{formatFieldLabel(field)}</span>
          <span className="font-mono">{valueText(field, from)}</span>
          <ArrowRight className="text-muted-foreground size-3 shrink-0 self-center" />
          <span className="font-mono font-medium">{valueText(field, to)}</span>
        </li>
      ))}
      {diff.geomChanged && <li className="text-muted-foreground">Geometry changed</li>}
    </ul>
  )
}

/** The body of a relate/unrelate change: the other feature it linked/unlinked,
 * clickable to open it. */
function RelationBody({ entry }: { entry: FeatureHistoryEntry }) {
  const select = useSelectionStore((s) => s.select)
  const s = (entry.snapshot ?? {}) as Record<string, unknown>
  const relClass = typeof s.related_feature_class === "string" ? s.related_feature_class : ""
  const relId = typeof s.related_feature_id === "string" ? s.related_feature_id : ""
  const relName = typeof s.relationship_class === "string" ? s.relationship_class : ""
  return (
    <div className="border-t px-2 py-1 text-xs">
      <button
        type="button"
        disabled={!relClass || !relId}
        onClick={() => relClass && relId && select(relClass, relId)}
        className="text-left hover:underline disabled:no-underline"
      >
        {entry.action === "relate" ? "Linked to " : "Unlinked from "}
        <span className="font-medium">{humanizeClass(relClass || "?")}</span>{" "}
        <span className="text-muted-foreground">{shortId(relId)}</span>
      </button>
      {relName && <div className="text-muted-foreground">{relName}</div>}
    </div>
  )
}

/** One change under a feature: its action + time, expanding to the field diff
 * (edits) or the linked feature (relate/unrelate). create/delete/restore have no
 * body -- the chip says it all. */
function ChangeNode({ entry, diff, schema }: { entry: FeatureHistoryEntry; diff: SnapshotDiff | undefined; schema: FeatureClassSchema | undefined }) {
  const [open, setOpen] = useState(false)
  const { label, className } = actionLabel(entry.action)
  const isRelation = RELATION_ACTIONS.has(entry.action)
  const showsDiff = DIFF_ACTIONS.has(entry.action)
  const hasBody = isRelation || showsDiff

  return (
    <li className="overflow-hidden rounded border">
      <button
        type="button"
        disabled={!hasBody}
        onClick={() => hasBody && setOpen((o) => !o)}
        className={cn("flex w-full items-center gap-1.5 px-1.5 py-1 text-left", hasBody && "hover:bg-muted/60")}
      >
        {hasBody ? (
          <ChevronRight className={cn("text-muted-foreground size-3 shrink-0 transition-transform", open && "rotate-90")} />
        ) : (
          <span className="size-3 shrink-0" />
        )}
        <span className={cn("rounded px-1.5 py-0.5 text-[11px] font-medium", className)}>{label}</span>
        <span className="flex-1" />
        <span className="text-muted-foreground shrink-0 text-xs tabular-nums">{timeOfDay(new Date(entry.at))}</span>
      </button>
      {open && isRelation && <RelationBody entry={entry} />}
      {open && showsDiff && <FieldDiffList diff={diff} schema={schema} />}
    </li>
  )
}

/** One feature you've changed: expands to your changes to it (the diff chain and
 * field labels are fetched lazily, only once expanded). */
function FeatureGroup({
  classCode,
  featureId,
  ids,
  latestAt,
  label,
}: {
  classCode: string
  featureId: string
  ids: Set<number>
  latestAt: string
  label: string
}) {
  const [open, setOpen] = useState(false)
  const select = useSelectionStore((s) => s.select)
  const locate = useSelectionStore((s) => s.locate)
  const clearLocated = useSelectionStore((s) => s.clearLocated)

  // Lazily loaded: both queries are disabled (undefined args) until expanded.
  const history = useFeatureHistory(open ? classCode : undefined, open ? featureId : undefined)
  const { data: schema } = useFeatureClassSchema(open ? classCode : undefined)

  const diffMap = useMemo(() => buildDiffMap(history.data?.history ?? []), [history.data])
  // My changes to this feature = the history rows in my activity feed's id set,
  // newest first (history is already newest-first).
  const myEntries = (history.data?.history ?? []).filter((e) => ids.has(e.id))

  return (
    <div className="rounded-md border">
      <div className="flex items-stretch">
        <button
          type="button"
          onClick={() => setOpen((o) => !o)}
          onMouseEnter={() => locate(classCode, featureId)}
          onMouseLeave={clearLocated}
          className="hover:bg-muted/60 flex min-w-0 flex-1 items-center gap-1.5 rounded-l-md px-2 py-1.5 text-left"
        >
          <ChevronRight className={cn("text-muted-foreground size-3.5 shrink-0 transition-transform", open && "rotate-90")} />
          <span className="min-w-0 flex-1 truncate text-sm">
            <span className="font-medium">{label}</span> <span className="text-muted-foreground text-xs">{shortId(featureId)}</span>
          </span>
          <span className="text-muted-foreground shrink-0 text-xs tabular-nums">{ids.size}×</span>
          <span className="text-muted-foreground shrink-0 text-xs tabular-nums">{shortWhen(latestAt)}</span>
        </button>
        <button
          type="button"
          onClick={() => select(classCode, featureId)}
          title="Open this feature on the map"
          className="text-muted-foreground hover:bg-muted/60 hover:text-foreground shrink-0 rounded-r-md px-2"
        >
          <MapPin className="size-3.5" />
        </button>
      </div>
      {open && (
        <div className="border-t p-1.5">
          {history.isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 px-1 py-1 text-xs">
              <Loader2 className="size-3.5 animate-spin" /> Loading changes…
            </div>
          ) : myEntries.length === 0 ? (
            <p className="text-muted-foreground px-1 py-1 text-xs">No changes to show.</p>
          ) : (
            <ul className="flex flex-col gap-1">
              {myEntries.map((e) => (
                <ChangeNode key={e.id} entry={e} diff={diffMap.get(e.id)} schema={schema} />
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  )
}

/** A problem to show in My edits -- either a persisted, triageable queue issue or
 * a read-only audit finding (no id/status). Normalised so one row renders both. */
interface Problem {
  key: string
  severity: string
  message: string
  fromClass?: string
  fromId?: string
  issueId?: string
  status?: string
  workOrderId?: string
  assigneeLabel?: string
  workOrderRef?: string
}

/** One problem row: a severity dot + message, an "Open" link to jump to the
 * feature, and -- when it's a queue issue -- the act-in-place controls:
 * acknowledge / resolve / suppress (triage), and ASSIGN the fix to an office or
 * field user (raises a WAM work order). So the whole loop -- fix it, hand it off,
 * or dismiss it -- happens here instead of bouncing to the Network Audit panel. */
function ProblemRow({
  problem,
  canTriage,
  onTriage,
  busy,
  canAssign,
  assignees,
  onAssign,
  assignBusy,
}: {
  problem: Problem
  canTriage: boolean
  onTriage: (id: string, status: ValidationIssue["status"]) => void
  busy: boolean
  canAssign: boolean
  assignees: AssignableUser[]
  onAssign: (id: string, userId: string) => void
  assignBusy: boolean
}) {
  const select = useSelectionStore((s) => s.select)
  const locate = useSelectionStore((s) => s.locate)
  const clearLocated = useSelectionStore((s) => s.clearLocated)
  const [assigning, setAssigning] = useState(false)
  const cls = problem.fromClass
  const id = problem.fromId
  const canOpen = Boolean(cls && id)
  const tone = problem.severity === "error" ? "bg-destructive" : problem.severity === "warning" ? "bg-amber-500" : "bg-sky-500"
  const triageable = canTriage && !!problem.issueId
  const acknowledged = problem.status === "acknowledged"
  const assigned = !!problem.workOrderId
  const assignable = canAssign && !!problem.issueId && !assigned

  return (
    <li className="rounded border">
      <div className="flex items-start gap-1.5 px-1.5 py-1">
        <span className={cn("mt-1 size-1.5 shrink-0 rounded-full", tone)} />
        <div className="min-w-0 flex-1">
          <p className="text-xs">{problem.message}</p>
          <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5">
            {canOpen && (
              <button
                type="button"
                onClick={() => cls && id && select(cls, id)}
                onMouseEnter={() => cls && id && locate(cls, id)}
                onMouseLeave={clearLocated}
                className="text-primary flex items-center gap-0.5 text-[11px] hover:underline"
              >
                <Eye className="size-3" /> Open
              </button>
            )}
            {assigned && (
              <span className="text-[11px] text-indigo-600 dark:text-indigo-400" title={problem.workOrderRef ? `Work order ${problem.workOrderRef}` : undefined}>
                {problem.assigneeLabel ? `Assigned to ${problem.assigneeLabel}` : "Assigned"}
              </span>
            )}
            {assignable && !assigning && (
              <button
                type="button"
                onClick={() => setAssigning(true)}
                className="text-muted-foreground hover:text-foreground flex items-center gap-0.5 text-[11px]"
              >
                <UserPlus className="size-3" /> Assign
              </button>
            )}
            {triageable && !acknowledged && !assigned && (
              <button
                type="button"
                disabled={busy}
                onClick={() => onTriage(problem.issueId!, "acknowledged")}
                className="text-muted-foreground hover:text-foreground text-[11px] disabled:opacity-50"
              >
                Acknowledge
              </button>
            )}
            {triageable && acknowledged && !assigned && <span className="text-muted-foreground text-[11px]">Acknowledged</span>}
            {triageable && (
              <>
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => onTriage(problem.issueId!, "resolved")}
                  title="Mark resolved"
                  className="text-muted-foreground hover:text-emerald-600 flex items-center gap-0.5 text-[11px] disabled:opacity-50"
                >
                  <CheckCheck className="size-3" /> Resolve
                </button>
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => onTriage(problem.issueId!, "suppressed")}
                  title="Suppress — a false positive, don't show again"
                  className="text-muted-foreground hover:text-foreground flex items-center gap-0.5 text-[11px] disabled:opacity-50"
                >
                  <BellOff className="size-3" />
                </button>
              </>
            )}
          </div>
          {assignable && assigning && (
            <div className="mt-1 flex items-center gap-1">
              <select
                autoFocus
                disabled={assignBusy}
                defaultValue=""
                onChange={(e) => {
                  if (e.target.value) {
                    onAssign(problem.issueId!, e.target.value)
                    setAssigning(false)
                  }
                }}
                className="border-input bg-background h-7 min-w-0 flex-1 rounded-md border px-1.5 text-xs"
              >
                <option value="" disabled>
                  Assign to…
                </option>
                {assignees.map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.display_name || u.email}
                  </option>
                ))}
              </select>
              <button
                type="button"
                onClick={() => setAssigning(false)}
                className="text-muted-foreground hover:text-foreground rounded p-1"
                title="Cancel"
              >
                <X className="size-3" />
              </button>
            </div>
          )}
        </div>
      </div>
    </li>
  )
}

const selectClass = "border-input bg-background h-7 min-w-0 flex-1 rounded-md border px-1.5 text-xs"
const dateClass = "border-input bg-background h-7 min-w-0 flex-1 rounded-md border px-1.5 text-xs"

export function MyEditsPanel() {
  const open = useMyEditsStore((s) => s.open)
  const [featureClass, setFeatureClass] = useState("")
  const [action, setAction] = useState("")
  // Default From to the start of today, so every user opens on today's edits; the
  // range is theirs to change (widen the From, set a To, or clear for all time).
  const [from, setFrom] = useState(startOfTodayInput)
  const [to, setTo] = useState("")

  const filter = useMemo(
    () => ({
      featureClass: featureClass || undefined,
      action: action || undefined,
      from: toRFC3339(from),
      to: toRFC3339(to),
    }),
    [featureClass, action, from, to],
  )
  const { data, isLoading, isError } = useMyActivity(open, filter)
  const classes = useFeatureClasses()
  const classNameByCode = useMemo(() => {
    const m = new Map<string, string>()
    for (const c of classes.data?.feature_classes ?? []) m.set(c.code, c.name)
    return m
  }, [classes.data])

  // Group the flat feed into one node per feature, each carrying the set of my
  // change ids and the most-recent change time, newest feature first.
  const features = useMemo(() => {
    const map = new Map<string, { classCode: string; featureId: string; ids: Set<number>; latestAt: string }>()
    for (const e of data?.activity ?? []) {
      const key = `${e.feature_class_code}:${e.feature_id}`
      let g = map.get(key)
      if (!g) {
        g = { classCode: e.feature_class_code, featureId: e.feature_id, ids: new Set(), latestAt: e.at }
        map.set(key, g)
      }
      g.ids.add(e.id)
      if (e.at > g.latestAt) g.latestAt = e.at
    }
    return [...map.values()].sort((a, b) => b.latestAt.localeCompare(a.latestAt))
  }, [data])

  // Scoped problem check over just the features in view -- the "did my edits break
  // anything, and let me fix/acknowledge it here" loop. Someone who can triage
  // (connectivity:commit) reconciles their features into the issue queue and gets
  // the real, triageable issues; everyone else gets a read-only audit (detect +
  // open-to-fix, no acknowledge). The result is dropped when the scope changes.
  const perm = usePermission()
  const canCommit = perm("connectivity:commit")
  const canAssign = perm("wam:write")
  const audit = useAuditFeatures()
  const reconcile = useReconcileIssues()
  const triage = useTriageIssue()
  const assign = useAssignIssue()
  const assignableUsers = useAssignableUsers()
  const scopeIds = useMemo(() => features.map((f) => f.featureId), [features])
  const scopeSig = scopeIds.join(",")
  const [checked, setChecked] = useState(false)
  const issues = useValidationIssues({ featureIds: scopeIds }, checked && canCommit && scopeIds.length > 0)

  const auditReset = audit.reset
  useEffect(() => {
    setChecked(false)
    auditReset()
  }, [scopeSig, auditReset])

  const runCheck = () => {
    if (canCommit) reconcile.mutate({ feature_ids: scopeIds }, { onSuccess: () => setChecked(true) })
    else audit.mutate(scopeIds)
  }
  const onTriage = (id: string, status: ValidationIssue["status"]) => triage.mutate({ id, status })
  const onAssign = (id: string, userId: string) => assign.mutate({ id, assignedToUserId: userId })

  if (!open) return null

  // The baseline the panel opens on: today's edits, no other narrowing. "Reset to
  // today" returns here; the From field is still the user's to clear for all time.
  const defaultFrom = startOfTodayInput()
  const isDefault = from === defaultFrom && to === "" && !featureClass && !action
  const resetToToday = () => {
    setFeatureClass("")
    setAction("")
    setFrom(defaultFrom)
    setTo("")
  }

  return (
    <FloatingPanel
      title="My edits"
      onClose={() => useMyEditsStore.getState().closePanel()}
      initialRect={{ x: window.innerWidth - 450, y: 72, width: 410, height: 500 }}
      minWidth={320}
      minHeight={260}
      persistKey="my-edits"
    >
      <div className="flex h-full flex-col">
        {/* Filters */}
        <div className="flex flex-col gap-1.5 border-b p-2">
          <div className="flex items-center gap-1.5">
            <select value={featureClass} onChange={(e) => setFeatureClass(e.target.value)} className={selectClass} title="Filter by feature class">
              <option value="">Any class</option>
              {(classes.data?.feature_classes ?? []).map((c) => (
                <option key={c.code} value={c.code}>
                  {c.name}
                </option>
              ))}
            </select>
            <select value={action} onChange={(e) => setAction(e.target.value)} className={selectClass} title="Filter by action">
              <option value="">Any action</option>
              {ACTIONS.map((a) => (
                <option key={a} value={a}>
                  {actionLabel(a).label}
                </option>
              ))}
            </select>
          </div>
          <div className="flex items-center gap-1.5">
            <label className="text-muted-foreground flex min-w-0 flex-1 items-center gap-1 text-[11px]">
              From
              <input type="datetime-local" value={from} onChange={(e) => setFrom(e.target.value)} className={dateClass} />
            </label>
            <label className="text-muted-foreground flex min-w-0 flex-1 items-center gap-1 text-[11px]">
              To
              <input type="datetime-local" value={to} onChange={(e) => setTo(e.target.value)} className={dateClass} />
            </label>
          </div>
          {!isDefault && (
            <button
              type="button"
              onClick={resetToToday}
              className="text-muted-foreground hover:text-foreground flex items-center gap-1 self-start text-[11px]"
            >
              <X className="size-3" /> Reset to today
            </button>
          )}
        </div>

        {/* Scoped problem check: audit just the features in view and act on what
            it finds right here (open to fix, or acknowledge/resolve/suppress) --
            not the whole-database sweep the Network Audit panel runs. */}
        {scopeIds.length > 0 &&
          (() => {
            const checking = canCommit ? reconcile.isPending || (checked && issues.isFetching) : audit.isPending
            const errored = canCommit ? reconcile.isError || issues.isError : audit.isError
            const hasRun = canCommit ? checked : !!audit.data
            const truncated = canCommit ? false : (audit.data?.truncated ?? false)
            const problems: Problem[] = canCommit
              ? (issues.data?.issues ?? []).map((iss) => ({
                  key: iss.id,
                  severity: iss.severity,
                  message: iss.message,
                  fromClass: iss.from_class,
                  fromId: iss.from_id,
                  issueId: iss.id,
                  status: iss.status,
                  workOrderId: iss.work_order_id,
                  assigneeLabel: iss.assignee_label,
                  workOrderRef: iss.work_order_ref,
                }))
              : (audit.data?.findings ?? []).map((f, i) => ({
                  key: `${f.kind}:${f.connection_id ?? f.from_id ?? i}`,
                  severity: f.severity,
                  message: f.message,
                  fromClass: f.from_class,
                  fromId: f.from_id,
                }))
            return (
              <div className="border-b p-2">
                <button
                  type="button"
                  disabled={checking}
                  onClick={runCheck}
                  className="border-input text-muted-foreground hover:bg-muted/60 flex w-full items-center justify-center gap-1.5 rounded-md border px-2 py-1.5 text-xs disabled:opacity-50"
                >
                  {checking ? <Loader2 className="size-3.5 animate-spin" /> : <ShieldCheck className="size-3.5 text-rose-600" />}
                  Check {scopeIds.length} feature{scopeIds.length === 1 ? "" : "s"} for problems
                </button>
                {errored && <p className="text-destructive mt-1.5 text-xs">Could not run the check.</p>}
                {hasRun &&
                  !checking &&
                  (problems.length === 0 ? (
                    <p className="mt-1.5 flex items-center gap-1.5 text-xs text-emerald-600 dark:text-emerald-400">
                      <CheckCircle2 className="size-3.5 shrink-0" /> No problems in your {scopeIds.length} edited feature
                      {scopeIds.length === 1 ? "" : "s"}.
                    </p>
                  ) : (
                    <div className="mt-1.5">
                      <p className="flex items-center gap-1.5 text-xs font-medium text-amber-700 dark:text-amber-400">
                        <AlertTriangle className="size-3.5 shrink-0" /> {problems.length} issue{problems.length === 1 ? "" : "s"} across your edits
                      </p>
                      <ul className="mt-1 flex max-h-48 flex-col gap-1 overflow-y-auto">
                        {problems.map((p) => (
                          <ProblemRow
                            key={p.key}
                            problem={p}
                            canTriage={canCommit}
                            onTriage={onTriage}
                            busy={triage.isPending}
                            canAssign={canAssign}
                            assignees={assignableUsers.data?.users ?? []}
                            onAssign={onAssign}
                            assignBusy={assign.isPending}
                          />
                        ))}
                      </ul>
                      {truncated && (
                        <p className="text-muted-foreground mt-1 text-[11px]">Some results were capped — narrow the range for the full list.</p>
                      )}
                      {!canCommit && (
                        <p className="text-muted-foreground mt-1 text-[11px]">Open a feature to fix it; acknowledging needs the connectivity permission.</p>
                      )}
                    </div>
                  ))}
              </div>
            )
          })()}

        {/* Tree */}
        <div className="min-h-0 flex-1 overflow-y-auto p-2">
          {isLoading ? (
            <p className="text-muted-foreground flex items-center gap-2 p-3 text-sm">
              <Loader2 className="size-4 animate-spin" /> Loading your changes…
            </p>
          ) : isError ? (
            <p className="text-destructive p-3 text-sm">Could not load your edits.</p>
          ) : features.length === 0 ? (
            <div className="text-muted-foreground flex flex-col items-center gap-2 p-6 text-center text-sm">
              {isDefault ? (
                <>
                  <PencilLine className="size-5 opacity-60" />
                  <p>No edits today yet. Your creates, edits, links and deletes show up here as you make them — widen the date range to see earlier.</p>
                </>
              ) : (
                <>
                  <Filter className="size-5 opacity-60" />
                  <p>No changes of yours match these filters.</p>
                </>
              )}
            </div>
          ) : (
            <ul className="flex flex-col gap-1.5">
              {features.map((f) => (
                <li key={`${f.classCode}:${f.featureId}`}>
                  <FeatureGroup
                    classCode={f.classCode}
                    featureId={f.featureId}
                    ids={f.ids}
                    latestAt={f.latestAt}
                    label={classNameByCode.get(f.classCode) ?? humanizeClass(f.classCode)}
                  />
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </FloatingPanel>
  )
}
