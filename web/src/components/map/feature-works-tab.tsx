import { useState } from "react"
import { ChevronDown, ChevronRight, ClipboardCheck, Loader2, Plus, TriangleAlert, Wrench } from "lucide-react"

import { RecordInspectionDialog } from "@/components/wam/record-inspection-dialog"
import { Button } from "@/components/ui/button"
import { usePermission } from "@/hooks/use-auth"
import {
  type AssignableUser,
  type Defect,
  type DefectStatus,
  type Inspection,
  type InspectionResult,
  type Priority,
  type Severity,
  type WorkOrder,
  type WorkStatus,
  type WorkType,
  useAssignableUsers,
  useCreateDefect,
  useCreateWorkOrder,
  useDefects,
  useInspection,
  useInspections,
  useRaiseWorkOrder,
  useSetDefectStatus,
  useUpdateWorkOrder,
  useWorkOrders,
} from "@/hooks/use-wam"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

// The map attribute panel's "Works" tab -- the Work & Asset Management loop
// (api/internal/wam) brought to the asset itself, so a crew selecting a feature
// on the map sees its open defects, its work orders, and its inspection history
// right there, and can log a defect, raise/start/complete/cancel a work order, or
// resolve a defect without leaving the map. Everything here scopes to the
// selected feature via asset_class + asset_id (the same bare-ref pair /works
// filters on); the full cross-asset boards + the inspection-template authoring
// live on the /works page. Self-gated on wam:write, mirroring WorksPage -- a
// reader sees the lists but no action controls.

const inputClass = "border-input bg-background w-full rounded-sm border px-2 py-1 text-sm"

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

function humanize(s: string): string {
  return s.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase())
}

function shortRef(o: WorkOrder): string {
  return o.reference || o.id.slice(0, 8)
}

function formatWhen(iso: string | null): string {
  if (!iso) return "—"
  return new Date(iso).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" })
}

const WORK_STATUS_BADGE: Record<WorkStatus, string> = {
  draft: "bg-muted text-muted-foreground",
  scheduled: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
  assigned: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
  in_progress: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  completed: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  cancelled: "bg-muted text-muted-foreground line-through",
}

const DEFECT_STATUS_BADGE: Record<DefectStatus, string> = {
  open: "bg-red-500/15 text-red-600 dark:text-red-400",
  work_ordered: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  resolved: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  dismissed: "bg-muted text-muted-foreground",
}

const SEVERITY_BADGE: Record<Severity, string> = {
  low: "bg-muted text-muted-foreground",
  medium: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
  high: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  critical: "bg-red-500/15 text-red-600 dark:text-red-400",
}

const RESULT_BADGE: Record<InspectionResult, string> = {
  pass: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
  fail: "bg-red-500/15 text-red-600 dark:text-red-400",
  conditional: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
}

function Badge({ className, children }: { className?: string; children: React.ReactNode }) {
  return (
    <span
      className={cn("inline-flex shrink-0 items-center rounded-md px-1.5 py-0.5 text-xs font-medium capitalize", className)}
    >
      {children}
    </span>
  )
}

/** A section heading with an optional trailing action (the add button). */
function SectionHeader({
  icon: Icon,
  title,
  count,
  action,
}: {
  icon: typeof Wrench
  title: string
  count: number
  action?: React.ReactNode
}) {
  return (
    <div className="flex items-center justify-between gap-2">
      <div className="flex items-center gap-1.5">
        <Icon className="text-muted-foreground size-3.5" />
        <span className="text-xs font-semibold tracking-wide uppercase">{title}</span>
        <span className="text-muted-foreground text-xs">({count})</span>
      </div>
      {action}
    </div>
  )
}

// --- Work orders -----------------------------------------------------------

const WORK_TYPES: WorkType[] = ["corrective", "preventive", "inspection", "construction"]
const PRIORITIES: Priority[] = ["low", "medium", "high", "urgent"]

/** The label for a work order's assignee: the internal field worker's name when
 * one is set (resolved from the assignable-users list), else the free-text crew
 * label, else "Unassigned". */
function assigneeLabel(order: WorkOrder, users: AssignableUser[]): string {
  if (order.assigned_to_user_id) {
    const u = users.find((x) => x.id === order.assigned_to_user_id)
    return u ? u.display_name : "Assigned"
  }
  return order.assigned_to || "Unassigned"
}

function NewWorkOrderForm({
  assetClass,
  assetId,
  assignees,
  onDone,
}: {
  assetClass: string
  assetId: string
  assignees: AssignableUser[]
  onDone: () => void
}) {
  const create = useCreateWorkOrder()
  const [title, setTitle] = useState("")
  const [workType, setWorkType] = useState<WorkType>("corrective")
  const [priority, setPriority] = useState<Priority>("medium")
  const [assignedTo, setAssignedTo] = useState("")

  const canSave = !create.isPending && title.trim() !== ""

  const submit = () => {
    if (!canSave) return
    create.mutate(
      {
        work_type: workType,
        priority,
        title: title.trim(),
        asset_class: assetClass,
        asset_id: assetId,
        ...(assignedTo ? { assigned_to_user_id: assignedTo } : {}),
      },
      { onSuccess: onDone, onError: (err) => toastError(err, "Could not create this work order.") },
    )
  }

  return (
    <div className="flex flex-col gap-2 rounded-md border p-2">
      <input
        autoFocus
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && submit()}
        placeholder="Work order title"
        className={inputClass}
      />
      <div className="flex gap-2">
        <select value={workType} onChange={(e) => setWorkType(e.target.value as WorkType)} className={inputClass}>
          {WORK_TYPES.map((t) => (
            <option key={t} value={t}>
              {humanize(t)}
            </option>
          ))}
        </select>
        <select value={priority} onChange={(e) => setPriority(e.target.value as Priority)} className={inputClass}>
          {PRIORITIES.map((p) => (
            <option key={p} value={p}>
              {humanize(p)}
            </option>
          ))}
        </select>
      </div>
      <select value={assignedTo} onChange={(e) => setAssignedTo(e.target.value)} className={inputClass}>
        <option value="">Unassigned</option>
        {assignees.map((u) => (
          <option key={u.id} value={u.id}>
            {u.display_name}
          </option>
        ))}
      </select>
      <div className="flex items-center gap-2">
        <Button size="sm" onClick={submit} disabled={!canSave}>
          {create.isPending ? <Loader2 className="size-4 animate-spin" /> : <Plus className="size-4" />}
          Create
        </Button>
        <Button variant="ghost" size="sm" onClick={onDone}>
          Cancel
        </Button>
      </div>
    </div>
  )
}

function WorkOrderRow({ order, canWrite, assignees }: { order: WorkOrder; canWrite: boolean; assignees: AssignableUser[] }) {
  const update = useUpdateWorkOrder()
  // For an order raised from a defect, completing it offers to resolve that defect
  // too (default on). Cancelling reopens it server-side (no opt-out needed).
  const [resolveDefect, setResolveDefect] = useState(true)

  const setStatus = (status: WorkStatus) =>
    update.mutate({ id: order.id, body: { status } }, { onError: (err) => toastError(err, "Could not update this work order.") })

  const complete = () =>
    update.mutate(
      { id: order.id, body: { status: "completed", ...(order.defect_id ? { resolve_defect: resolveDefect } : {}) } },
      { onError: (err) => toastError(err, "Could not update this work order.") },
    )

  const assign = (userId: string) =>
    update.mutate(
      { id: order.id, body: { assigned_to_user_id: userId } },
      { onError: (err) => toastError(err, "Could not reassign this work order.") },
    )

  const isClosed = order.status === "completed" || order.status === "cancelled"
  const canStart = order.status === "draft" || order.status === "scheduled" || order.status === "assigned"

  return (
    <div className="rounded-md border p-2 text-sm">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="truncate font-medium">{order.title}</p>
          <p className="text-muted-foreground font-mono text-xs">
            {shortRef(order)} · {humanize(order.work_type)}
          </p>
        </div>
        <Badge className={WORK_STATUS_BADGE[order.status]}>{humanize(order.status)}</Badge>
      </div>
      {canWrite && !isClosed ? (
        <label className="text-muted-foreground mt-2 flex items-center gap-1.5 text-xs">
          <span className="shrink-0">Assignee</span>
          <select
            value={order.assigned_to_user_id ?? ""}
            disabled={update.isPending}
            onChange={(e) => assign(e.target.value)}
            className={cn(inputClass, "py-0.5 text-xs")}
          >
            <option value="">Unassigned</option>
            {order.assigned_to && !order.assigned_to_user_id && (
              <option value="" disabled>
                (crew: {order.assigned_to})
              </option>
            )}
            {assignees.map((u) => (
              <option key={u.id} value={u.id}>
                {u.display_name}
              </option>
            ))}
          </select>
        </label>
      ) : (
        <p className="text-muted-foreground mt-1 text-xs">Assignee: {assigneeLabel(order, assignees)}</p>
      )}
      {canWrite && !isClosed && (
        <div className="mt-2 flex flex-col gap-1.5">
          {order.status === "in_progress" && order.defect_id && (
            <label className="text-muted-foreground flex items-center gap-1.5 text-xs">
              <input type="checkbox" checked={resolveDefect} onChange={(e) => setResolveDefect(e.target.checked)} />
              Resolve the originating defect on completion
            </label>
          )}
          <div className="flex flex-wrap gap-1">
            {canStart && (
              <Button variant="outline" size="sm" className="h-6 px-2 text-xs" disabled={update.isPending} onClick={() => setStatus("in_progress")}>
                Start
              </Button>
            )}
            {order.status === "in_progress" && (
              <Button variant="outline" size="sm" className="h-6 px-2 text-xs" disabled={update.isPending} onClick={complete}>
                Complete
              </Button>
            )}
            <Button
              variant="ghost"
              size="sm"
              className="text-muted-foreground hover:text-destructive h-6 px-2 text-xs"
              disabled={update.isPending}
              onClick={() => setStatus("cancelled")}
            >
              Cancel
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

// --- Defects ---------------------------------------------------------------

const SEVERITIES: Severity[] = ["low", "medium", "high", "critical"]

function NewDefectForm({
  assetClass,
  assetId,
  onDone,
}: {
  assetClass: string
  assetId: string
  onDone: () => void
}) {
  const create = useCreateDefect()
  const [title, setTitle] = useState("")
  const [severity, setSeverity] = useState<Severity>("medium")
  const [defectType, setDefectType] = useState("")

  const canSave = !create.isPending && title.trim() !== ""

  const submit = () => {
    if (!canSave) return
    create.mutate(
      {
        asset_class: assetClass,
        asset_id: assetId,
        title: title.trim(),
        severity,
        source: "ad_hoc",
        ...(defectType.trim() ? { defect_type: defectType.trim() } : {}),
      },
      { onSuccess: onDone, onError: (err) => toastError(err, "Could not log this defect.") },
    )
  }

  return (
    <div className="flex flex-col gap-2 rounded-md border p-2">
      <input
        autoFocus
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && submit()}
        placeholder="What's wrong with this asset?"
        className={inputClass}
      />
      <div className="flex gap-2">
        <select value={severity} onChange={(e) => setSeverity(e.target.value as Severity)} className={inputClass}>
          {SEVERITIES.map((s) => (
            <option key={s} value={s}>
              {humanize(s)}
            </option>
          ))}
        </select>
        <input
          value={defectType}
          onChange={(e) => setDefectType(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
          placeholder="Type (optional)"
          className={inputClass}
        />
      </div>
      <div className="flex items-center gap-2">
        <Button size="sm" onClick={submit} disabled={!canSave}>
          {create.isPending ? <Loader2 className="size-4 animate-spin" /> : <Plus className="size-4" />}
          Log defect
        </Button>
        <Button variant="ghost" size="sm" onClick={onDone}>
          Cancel
        </Button>
      </div>
    </div>
  )
}

function DefectRow({ defect, canWrite }: { defect: Defect; canWrite: boolean }) {
  const raise = useRaiseWorkOrder()
  const setStatus = useSetDefectStatus()
  const busy = raise.isPending || setStatus.isPending

  const transition = (status: DefectStatus) =>
    setStatus.mutate({ id: defect.id, status }, { onError: (err) => toastError(err, "Could not update this defect.") })

  const raiseWorkOrder = () =>
    raise.mutate({ defectId: defect.id }, { onError: (err) => toastError(err, "Could not raise a work order.") })

  const isClosed = defect.status === "resolved" || defect.status === "dismissed"

  return (
    <div className="rounded-md border p-2 text-sm">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="truncate font-medium">{defect.title}</p>
          <p className="text-muted-foreground text-xs">
            {defect.defect_type ? `${humanize(defect.defect_type)} · ` : ""}
            {humanize(defect.source)}
          </p>
        </div>
        <div className="flex shrink-0 flex-col items-end gap-1">
          <Badge className={SEVERITY_BADGE[defect.severity]}>{humanize(defect.severity)}</Badge>
          <Badge className={DEFECT_STATUS_BADGE[defect.status]}>{humanize(defect.status)}</Badge>
        </div>
      </div>
      {canWrite && !isClosed && (
        <div className="mt-2 flex flex-wrap gap-1">
          {defect.status === "open" && (
            <Button variant="outline" size="sm" className="h-6 px-2 text-xs" disabled={busy} onClick={raiseWorkOrder}>
              <Wrench className="size-3" />
              Raise work order
            </Button>
          )}
          <Button variant="outline" size="sm" className="h-6 px-2 text-xs" disabled={busy} onClick={() => transition("resolved")}>
            Resolve
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="text-muted-foreground h-6 px-2 text-xs"
            disabled={busy}
            onClick={() => transition("dismissed")}
          >
            Dismiss
          </Button>
        </div>
      )}
      {canWrite && isClosed && (
        <div className="mt-2">
          <Button variant="ghost" size="sm" className="text-muted-foreground h-6 px-2 text-xs" disabled={busy} onClick={() => transition("open")}>
            Reopen
          </Button>
        </div>
      )}
    </div>
  )
}

// --- Inspections (read-only history) ---------------------------------------
// Recording a fresh inspection needs a checklist template picker (the /works
// page's InspectionsPanel owns that); here we show the asset's inspection
// history read-only, each row expandable to its per-item pass/fail results and
// any auto-raised defect.

function InspectionRow({ inspection }: { inspection: Inspection }) {
  const [open, setOpen] = useState(false)
  const detail = useInspection(open ? inspection.id : null)

  return (
    <div className="rounded-md border text-sm">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className="hover:bg-accent/50 flex w-full items-center gap-2 rounded-t-md p-2 text-left transition-colors"
      >
        {open ? <ChevronDown className="size-3.5 shrink-0" /> : <ChevronRight className="size-3.5 shrink-0" />}
        <div className="min-w-0 flex-1">
          <p className="text-muted-foreground text-xs">{formatWhen(inspection.inspected_at)}</p>
          {inspection.inspected_by && <p className="text-muted-foreground truncate text-xs">{inspection.inspected_by}</p>}
        </div>
        <Badge className={RESULT_BADGE[inspection.overall_result]}>{humanize(inspection.overall_result)}</Badge>
      </button>
      {open && (
        <div className="border-t p-2">
          {detail.isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 text-xs">
              <Loader2 className="size-3.5 animate-spin" />
              Loading…
            </div>
          ) : detail.isError || !detail.data ? (
            <p className="text-destructive text-xs">Could not load this inspection.</p>
          ) : (
            <ul className="flex flex-col gap-1">
              {(detail.data.items ?? []).map((item) => (
                <li key={item.id} className="flex items-start gap-1.5 text-xs">
                  <span className="mt-px shrink-0">
                    {item.passed === false ? "✗" : item.passed === true ? "✓" : "—"}
                  </span>
                  <div className="min-w-0">
                    <span className={cn(item.passed === false && "text-destructive")}>{item.prompt}</span>
                    {item.value_text && <span className="text-muted-foreground"> · {item.value_text}</span>}
                    {item.value_numeric != null && <span className="text-muted-foreground"> · {item.value_numeric}</span>}
                    {item.defect_id && (
                      <span className="text-muted-foreground"> · defect {item.defect_id.slice(0, 8)}</span>
                    )}
                  </div>
                </li>
              ))}
              {(detail.data.items ?? []).length === 0 && <li className="text-muted-foreground text-xs">No recorded items.</li>}
            </ul>
          )}
        </div>
      )}
    </div>
  )
}

// --- The tab ---------------------------------------------------------------

export function WorksTab() {
  const selected = useSelectionStore((s) => s.selected)
  const canWrite = usePermission()("wam:write")
  const [addingOrder, setAddingOrder] = useState(false)
  const [addingDefect, setAddingDefect] = useState(false)
  const [recordingInspection, setRecordingInspection] = useState(false)

  const assetClass = selected?.featureClassCode
  const assetId = selected?.featureId

  const orders = useWorkOrders({ assetClass, assetId })
  const defects = useDefects({ assetClass, assetId })
  const inspections = useInspections({ assetClass, assetId })
  const assignees = useAssignableUsers().data?.users ?? []

  if (!selected || !assetClass || !assetId) return null

  const orderRows = orders.data?.work_orders ?? []
  const defectRows = defects.data?.defects ?? []
  const inspectionRows = inspections.data?.inspections ?? []

  return (
    <div className="flex flex-col gap-4">
      {/* Work orders */}
      <div className="flex flex-col gap-2">
        <SectionHeader
          icon={Wrench}
          title="Work orders"
          count={orderRows.length}
          action={
            canWrite && !addingOrder ? (
              <Button variant="outline" size="sm" className="h-6 gap-1 px-2 text-xs" onClick={() => setAddingOrder(true)}>
                <Plus className="size-3" />
                New
              </Button>
            ) : undefined
          }
        />
        {addingOrder && (
          <NewWorkOrderForm
            assetClass={assetClass}
            assetId={assetId}
            assignees={assignees}
            onDone={() => setAddingOrder(false)}
          />
        )}
        {orders.isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading…
          </div>
        ) : orderRows.length === 0 ? (
          <p className="text-muted-foreground text-sm">No work orders on this asset.</p>
        ) : (
          orderRows.map((order) => <WorkOrderRow key={order.id} order={order} canWrite={canWrite} assignees={assignees} />)
        )}
      </div>

      {/* Defects */}
      <div className="flex flex-col gap-2">
        <SectionHeader
          icon={TriangleAlert}
          title="Defects"
          count={defectRows.length}
          action={
            canWrite && !addingDefect ? (
              <Button variant="outline" size="sm" className="h-6 gap-1 px-2 text-xs" onClick={() => setAddingDefect(true)}>
                <Plus className="size-3" />
                Log
              </Button>
            ) : undefined
          }
        />
        {addingDefect && (
          <NewDefectForm assetClass={assetClass} assetId={assetId} onDone={() => setAddingDefect(false)} />
        )}
        {defects.isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading…
          </div>
        ) : defectRows.length === 0 ? (
          <p className="text-muted-foreground text-sm">No defects on this asset.</p>
        ) : (
          defectRows.map((defect) => <DefectRow key={defect.id} defect={defect} canWrite={canWrite} />)
        )}
      </div>

      {/* Inspections */}
      <div className="flex flex-col gap-2">
        <SectionHeader
          icon={ClipboardCheck}
          title="Inspections"
          count={inspectionRows.length}
          action={
            canWrite ? (
              <Button variant="outline" size="sm" className="h-6 gap-1 px-2 text-xs" onClick={() => setRecordingInspection(true)}>
                <ClipboardCheck className="size-3" />
                Record
              </Button>
            ) : undefined
          }
        />
        {inspections.isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading…
          </div>
        ) : inspectionRows.length === 0 ? (
          <p className="text-muted-foreground text-sm">
            No inspections on this asset.{canWrite && " Use Record to inspect it against a checklist."}
          </p>
        ) : (
          inspectionRows.map((inspection) => <InspectionRow key={inspection.id} inspection={inspection} />)
        )}
      </div>

      {recordingInspection && (
        <RecordInspectionDialog
          workOrders={orderRows}
          initialAsset={{ assetClass, assetId }}
          onClose={() => setRecordingInspection(false)}
        />
      )}
    </div>
  )
}
