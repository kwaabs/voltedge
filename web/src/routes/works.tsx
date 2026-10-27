import { type ReactNode, useMemo, useState } from "react"
import { Activity, CheckCircle2, ClipboardList, Loader2, MoreHorizontal, Play, Plus, TriangleAlert, Wrench, XCircle } from "lucide-react"

import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { usePermission } from "@/hooks/use-auth"
import { useCrews } from "@/hooks/use-oms"
import {
  type AssignableUser,
  type Defect,
  type DefectSource,
  type Priority,
  type Severity,
  type WorkOrder,
  type WorkType,
  useAssignableUsers,
  useCreateDefect,
  useCreateWorkOrder,
  useDefects,
  useRaiseWorkOrder,
  useSetDefectStatus,
  useUpdateWorkOrder,
  useWorkOrders,
} from "@/hooks/use-wam"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { InspectionsPanel } from "@/routes/works-inspections"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"
const SEVERITIES: Severity[] = ["low", "medium", "high", "critical"]
const PRIORITIES: Priority[] = ["low", "medium", "high", "urgent"]
const SOURCES: DefectSource[] = ["inspection", "outage", "condition_monitoring", "ad_hoc"]
const WORK_TYPES: WorkType[] = ["inspection", "corrective", "preventive", "construction"]

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

function shortId(id: string): string {
  return id.slice(0, 8)
}

function formatWhen(iso: string): string {
  return new Date(iso).toLocaleString()
}

function assetLabel(cls: string | null, id: string | null): string {
  if (!cls || !id) return "—"
  return `${cls} · ${shortId(id)}`
}

function humanize(s: string): string {
  return s.replace(/_/g, " ")
}

/** The assignee label for a work order: the internal worker's name when set
 * (resolved from the assignable-users list), else the assigned crew's name, else
 * the free-text crew label, else nothing. */
function assigneeLabel(order: WorkOrder, users: AssignableUser[]): string | null {
  if (order.assigned_to_user_id) {
    const u = users.find((x) => x.id === order.assigned_to_user_id)
    return u ? u.display_name : "Assigned"
  }
  if (order.crew_id) {
    return order.crew_name ? `Crew: ${order.crew_name}` : "Crew"
  }
  return order.assigned_to || null
}

/** A field-worker picker (assigned_to_user_id). "" = unassigned. */
function AssigneeSelect({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const users = useAssignableUsers()
  return (
    <select className={inputClass} value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">Unassigned</option>
      {(users.data?.users ?? []).map((u) => (
        <option key={u.id} value={u.id}>
          {u.display_name}
        </option>
      ))}
    </select>
  )
}

/** A crew picker (crew_id). "" = no crew. Lists active crews with their current
 * load (members · open orders · live incidents) so an assigner can see a crew's
 * backlog before piling on more. A crew-assigned order reaches every member's
 * field pack. */
function CrewSelect({ value, onChange }: { value: string; onChange: (v: string) => void }) {
  const crews = useCrews()
  return (
    <select className={inputClass} value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">No crew</option>
      {(crews.data?.crews ?? []).map((c) => {
        const load: string[] = [`${c.member_count} member${c.member_count === 1 ? "" : "s"}`]
        if (c.open_work_order_count) load.push(`${c.open_work_order_count} open`)
        if (c.active_assignment_count) load.push(`${c.active_assignment_count} incident${c.active_assignment_count === 1 ? "" : "s"}`)
        return (
          <option key={c.id} value={c.id}>
            {c.name} — {load.join(" · ")}
          </option>
        )
      })}
    </select>
  )
}

// --- badges ----------------------------------------------------------------

const SEVERITY_BADGE: Record<Severity, string> = {
  low: "bg-muted text-muted-foreground",
  medium: "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300",
  high: "bg-orange-100 text-orange-800 dark:bg-orange-950 dark:text-orange-300",
  critical: "bg-rose-100 text-rose-800 dark:bg-rose-950 dark:text-rose-300",
}

const WORK_STATUS_BADGE: Record<WorkOrder["status"], string> = {
  draft: "bg-muted text-muted-foreground",
  scheduled: "bg-violet-100 text-violet-800 dark:bg-violet-950 dark:text-violet-300",
  assigned: "bg-blue-100 text-blue-800 dark:bg-blue-950 dark:text-blue-300",
  in_progress: "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300",
  completed: "bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300",
  cancelled: "bg-muted text-muted-foreground line-through",
}

const DEFECT_STATUS_BADGE: Record<Defect["status"], string> = {
  open: "bg-rose-100 text-rose-800 dark:bg-rose-950 dark:text-rose-300",
  work_ordered: "bg-blue-100 text-blue-800 dark:bg-blue-950 dark:text-blue-300",
  resolved: "bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300",
  dismissed: "bg-muted text-muted-foreground line-through",
}

function Badge({ text, cls }: { text: string; cls: string }) {
  return <span className={`rounded px-1.5 py-0.5 text-xs font-medium capitalize ${cls}`}>{humanize(text)}</span>
}

// --- log-defect dialog -----------------------------------------------------

/** Log an asset-health finding. The asset (class + id) and a title are required
 * (server-enforced, mirrored here so the button disables until satisfiable). */
function LogDefectDialog({ onClose }: { onClose: () => void }) {
  const [assetClass, setAssetClass] = useState("")
  const [assetId, setAssetId] = useState("")
  const [title, setTitle] = useState("")
  const [defectType, setDefectType] = useState("")
  const [severity, setSeverity] = useState<Severity>("medium")
  const [source, setSource] = useState<DefectSource>("ad_hoc")
  const [description, setDescription] = useState("")
  const create = useCreateDefect()

  const canSave = assetClass.trim() !== "" && assetId.trim() !== "" && title.trim() !== "" && !create.isPending

  function handleSave() {
    create.mutate(
      {
        asset_class: assetClass.trim(),
        asset_id: assetId.trim(),
        title: title.trim(),
        defect_type: defectType.trim() || undefined,
        severity,
        source,
        description: description.trim() || undefined,
      },
      { onSuccess: onClose, onError: (err) => toastError(err, "Could not log the defect.") },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Log defect"
      description="Record a problem found on an asset. Fixing it is a corrective work order."
      onSubmit={handleSave}
      submitLabel="Log defect"
      canSubmit={canSave}
      saving={create.isPending}
    >
      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Asset class</label>
          <input className={inputClass} value={assetClass} onChange={(e) => setAssetClass(e.target.value)} placeholder="e.g. DIST_XFMR" autoFocus />
        </div>
        <div className="flex flex-[2] flex-col gap-1">
          <label className={labelClass}>Asset ID</label>
          <input className={inputClass} value={assetId} onChange={(e) => setAssetId(e.target.value)} placeholder="UUID of the affected asset" />
        </div>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Title</label>
        <input className={inputClass} value={title} onChange={(e) => setTitle(e.target.value)} placeholder="e.g. Oil leak at bushing" />
      </div>

      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Defect type</label>
          <input className={inputClass} value={defectType} onChange={(e) => setDefectType(e.target.value)} placeholder="e.g. oil_leak (optional)" />
        </div>
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Severity</label>
          <select className={inputClass} value={severity} onChange={(e) => setSeverity(e.target.value as Severity)}>
            {SEVERITIES.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </div>
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Source</label>
          <select className={inputClass} value={source} onChange={(e) => setSource(e.target.value as DefectSource)}>
            {SOURCES.map((s) => (
              <option key={s} value={s}>
                {humanize(s)}
              </option>
            ))}
          </select>
        </div>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Description</label>
        <input className={inputClass} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="Optional" />
      </div>
    </FormDialog>
  )
}

// --- new work-order dialog -------------------------------------------------

/** Create a work order directly (not from a defect) -- e.g. a scheduled
 * inspection or a construction job. work_type and title are required. */
function NewWorkOrderDialog({ onClose }: { onClose: () => void }) {
  const [workType, setWorkType] = useState<WorkType>("inspection")
  const [title, setTitle] = useState("")
  const [priority, setPriority] = useState<Priority>("medium")
  const [assetClass, setAssetClass] = useState("")
  const [assetId, setAssetId] = useState("")
  const [assignedToUserId, setAssignedToUserId] = useState("")
  const [assignedCrewId, setAssignedCrewId] = useState("")
  const [assignedTo, setAssignedTo] = useState("")
  const [scheduledFor, setScheduledFor] = useState("")
  const [description, setDescription] = useState("")
  const create = useCreateWorkOrder()

  const canSave = title.trim() !== "" && !create.isPending

  function toIso(local: string): string | undefined {
    if (!local) return undefined
    const d = new Date(local)
    return Number.isNaN(d.getTime()) ? undefined : d.toISOString()
  }

  function handleSave() {
    create.mutate(
      {
        work_type: workType,
        title: title.trim(),
        priority,
        asset_class: assetClass.trim() || undefined,
        asset_id: assetId.trim() || undefined,
        assigned_to_user_id: assignedToUserId || undefined,
        crew_id: assignedCrewId || undefined,
        assigned_to: assignedTo.trim() || undefined,
        scheduled_for: toIso(scheduledFor),
        description: description.trim() || undefined,
      },
      { onSuccess: onClose, onError: (err) => toastError(err, "Could not create the work order.") },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="New work order"
      description="One typed unit of work: inspection, corrective, preventive, or construction."
      onSubmit={handleSave}
      submitLabel="Create"
      canSubmit={canSave}
      saving={create.isPending}
    >
      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Work type</label>
          <select className={inputClass} value={workType} onChange={(e) => setWorkType(e.target.value as WorkType)}>
            {WORK_TYPES.map((t) => (
              <option key={t} value={t}>
                {humanize(t)}
              </option>
            ))}
          </select>
        </div>
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Priority</label>
          <select className={inputClass} value={priority} onChange={(e) => setPriority(e.target.value as Priority)}>
            {PRIORITIES.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </select>
        </div>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Title</label>
        <input className={inputClass} value={title} onChange={(e) => setTitle(e.target.value)} placeholder="e.g. Annual pole inspection" autoFocus />
      </div>

      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Asset class</label>
          <input className={inputClass} value={assetClass} onChange={(e) => setAssetClass(e.target.value)} placeholder="Optional" />
        </div>
        <div className="flex flex-[2] flex-col gap-1">
          <label className={labelClass}>Asset ID</label>
          <input className={inputClass} value={assetId} onChange={(e) => setAssetId(e.target.value)} placeholder="UUID (optional)" />
        </div>
      </div>

      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Assign to worker</label>
          <AssigneeSelect value={assignedToUserId} onChange={setAssignedToUserId} />
        </div>
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Assign to crew</label>
          <CrewSelect value={assignedCrewId} onChange={setAssignedCrewId} />
        </div>
      </div>

      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Scheduled for</label>
          <input className={inputClass} type="datetime-local" value={scheduledFor} onChange={(e) => setScheduledFor(e.target.value)} />
        </div>
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>External crew</label>
          <input className={inputClass} value={assignedTo} onChange={(e) => setAssignedTo(e.target.value)} placeholder="Free-text label (optional)" />
        </div>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Description</label>
        <input className={inputClass} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="Optional" />
      </div>
    </FormDialog>
  )
}

// --- raise-from-defect dialog ----------------------------------------------

/** Raise a corrective work order for a defect. Inherits the defect's asset and
 * a priority derived from its severity; the crew and schedule are optional
 * overrides. */
function RaiseWorkOrderDialog({ defect, onClose }: { defect: Defect; onClose: () => void }) {
  const [assignedToUserId, setAssignedToUserId] = useState("")
  const [assignedCrewId, setAssignedCrewId] = useState("")
  const [scheduledFor, setScheduledFor] = useState("")
  const raise = useRaiseWorkOrder()

  function toIso(local: string): string | undefined {
    if (!local) return undefined
    const d = new Date(local)
    return Number.isNaN(d.getTime()) ? undefined : d.toISOString()
  }

  function handleSave() {
    raise.mutate(
      {
        defectId: defect.id,
        body: {
          assigned_to_user_id: assignedToUserId || undefined,
          crew_id: assignedCrewId || undefined,
          scheduled_for: toIso(scheduledFor),
        },
      },
      { onSuccess: onClose, onError: (err) => toastError(err, "Could not raise the work order.") },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Raise corrective work order"
      description={`For defect: ${defect.title}. The order inherits the asset and a priority from the ${defect.severity} severity.`}
      onSubmit={handleSave}
      submitLabel="Raise work order"
      canSubmit={!raise.isPending}
      saving={raise.isPending}
    >
      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Assign to worker</label>
          <AssigneeSelect value={assignedToUserId} onChange={setAssignedToUserId} />
        </div>
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Assign to crew</label>
          <CrewSelect value={assignedCrewId} onChange={setAssignedCrewId} />
        </div>
      </div>
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Scheduled for</label>
        <input className={inputClass} type="datetime-local" value={scheduledFor} onChange={(e) => setScheduledFor(e.target.value)} />
      </div>
    </FormDialog>
  )
}

// --- work-order row --------------------------------------------------------

function WorkOrderRow({ order, canWrite, assignees }: { order: WorkOrder; canWrite: boolean; assignees: AssignableUser[] }) {
  const update = useUpdateWorkOrder()
  const pending = update.isPending
  const isTerminal = order.status === "completed" || order.status === "cancelled"
  const canStart = order.status !== "in_progress" && !isTerminal

  function set(status: WorkOrder["status"]) {
    update.mutate(
      { id: order.id, body: { status } },
      { onError: (err) => toastError(err, "Could not update the work order.") },
    )
  }

  const assignee = assigneeLabel(order, assignees)

  return (
    <div className="hover:bg-muted/40 border-border/50 flex items-start gap-2 border-b px-2 py-2 last:border-0">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <Badge text={order.status} cls={WORK_STATUS_BADGE[order.status]} />
          <span className="text-muted-foreground text-xs capitalize">{humanize(order.work_type)}</span>
          {order.source && (
            <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-[10px] capitalize" title="Where this order came from">
              {order.source}
            </span>
          )}
          <span className="text-sm font-medium">{order.title}</span>
        </div>
        <div className="text-muted-foreground mt-0.5 text-xs">
          <span className="capitalize">{order.priority}</span> · {assetLabel(order.asset_class, order.asset_id)}
          {assignee && <> · {assignee}</>}
          {" · "}
          {order.scheduled_for ? `scheduled ${formatWhen(order.scheduled_for)}` : formatWhen(order.created_at)}
        </div>
      </div>

      {canWrite && !isTerminal && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="sm" className="text-muted-foreground size-8 shrink-0 p-0" aria-label="Work-order actions">
              <MoreHorizontal className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-40">
            {canStart && (
              <DropdownMenuItem disabled={pending} onSelect={() => set("in_progress")}>
                <Play className="size-4" />
                Start
              </DropdownMenuItem>
            )}
            {order.status === "in_progress" && (
              <DropdownMenuItem disabled={pending} onSelect={() => set("completed")}>
                <CheckCircle2 className="size-4" />
                Complete
              </DropdownMenuItem>
            )}
            <DropdownMenuSeparator />
            <DropdownMenuItem className="text-destructive" disabled={pending} onSelect={() => set("cancelled")}>
              <XCircle className="size-4" />
              Cancel
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  )
}

// --- defect row ------------------------------------------------------------

function DefectRow({
  defect,
  canWrite,
  onRaise,
}: {
  defect: Defect
  canWrite: boolean
  onRaise: (d: Defect) => void
}) {
  const setStatus = useSetDefectStatus()
  const pending = setStatus.isPending
  const isTerminal = defect.status === "resolved" || defect.status === "dismissed"

  function set(status: Defect["status"]) {
    setStatus.mutate(
      { id: defect.id, status },
      { onError: (err) => toastError(err, "Could not update the defect.") },
    )
  }

  return (
    <div className="hover:bg-muted/40 border-border/50 flex items-start gap-2 border-b px-2 py-2 last:border-0">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <Badge text={defect.severity} cls={SEVERITY_BADGE[defect.severity]} />
          <Badge text={defect.status} cls={DEFECT_STATUS_BADGE[defect.status]} />
          <span className="text-sm font-medium">{defect.title}</span>
        </div>
        <div className="text-muted-foreground mt-0.5 text-xs">
          {assetLabel(defect.asset_class, defect.asset_id)} · {humanize(defect.source)}
          {defect.incident_id && <> · outage {shortId(defect.incident_id)}</>}
          {" · "}
          {formatWhen(defect.reported_at)}
        </div>
      </div>

      {canWrite && defect.status === "open" && (
        <Button variant="outline" size="sm" className="shrink-0" disabled={pending} onClick={() => onRaise(defect)}>
          <Wrench className="size-4" />
          Raise
        </Button>
      )}
      {canWrite && !isTerminal && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="sm" className="text-muted-foreground size-8 shrink-0 p-0" aria-label="Defect actions">
              <MoreHorizontal className="size-4" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-40">
            {defect.status === "open" && (
              <DropdownMenuItem disabled={pending} onSelect={() => onRaise(defect)}>
                <Wrench className="size-4" />
                Raise work order
              </DropdownMenuItem>
            )}
            <DropdownMenuItem disabled={pending} onSelect={() => set("resolved")}>
              <CheckCircle2 className="size-4" />
              Resolve
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem className="text-muted-foreground" disabled={pending} onSelect={() => set("dismissed")}>
              <XCircle className="size-4" />
              Dismiss
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  )
}

// --- KPI strip -------------------------------------------------------------

type KpiTone = "default" | "alert" | "warn"

function KpiTile({
  icon,
  label,
  value,
  sub,
  tone = "default",
}: {
  icon: ReactNode
  label: string
  value: ReactNode
  sub?: string
  tone?: KpiTone
}) {
  const valueTone =
    tone === "alert" ? "text-rose-600 dark:text-rose-400" : tone === "warn" ? "text-amber-600 dark:text-amber-400" : ""
  return (
    <Card className="flex-1">
      <CardContent className="flex items-center gap-3 p-3">
        <div className="text-muted-foreground shrink-0">{icon}</div>
        <div className="flex min-w-0 flex-col">
          <span className="text-muted-foreground text-[11px] font-medium uppercase tracking-wide">{label}</span>
          <span className={cn("text-2xl font-semibold leading-tight tabular-nums", valueTone)}>{value}</span>
          {sub && <span className="text-muted-foreground truncate text-[11px]">{sub}</span>}
        </div>
      </CardContent>
    </Card>
  )
}

function WorksKpiStrip({
  openDefects,
  activeOrders,
  inProgress,
  unassigned,
}: {
  openDefects: number
  activeOrders: number
  inProgress: number
  unassigned: number
}) {
  return (
    <div className="flex flex-col gap-3 sm:flex-row">
      <KpiTile
        icon={<TriangleAlert className="size-5" />}
        label="Open defects"
        value={openDefects}
        sub="awaiting a work order"
        tone={openDefects > 0 ? "alert" : "default"}
      />
      <KpiTile icon={<ClipboardList className="size-5" />} label="Active orders" value={activeOrders} sub="not yet closed" />
      <KpiTile
        icon={<Play className="size-5" />}
        label="In progress"
        value={inProgress}
        sub="crews on the job"
        tone={inProgress > 0 ? "warn" : "default"}
      />
      <KpiTile
        icon={<Activity className="size-5" />}
        label="Unassigned"
        value={unassigned}
        sub="active, no worker"
        tone={unassigned > 0 ? "warn" : "default"}
      />
    </div>
  )
}

// --- page ------------------------------------------------------------------

export function WorksPage() {
  const can = usePermission()
  const canWrite = can("wam:write")

  const [mineOnly, setMineOnly] = useState(false)
  const orders = useWorkOrders({ mine: mineOnly })
  const defects = useDefects()
  const assignees = useAssignableUsers().data?.users ?? []

  const [logging, setLogging] = useState(false)
  const [creating, setCreating] = useState(false)
  const [raising, setRaising] = useState<Defect | null>(null)

  const orderRows = useMemo(() => orders.data?.work_orders ?? [], [orders.data])
  const defectRows = useMemo(() => defects.data?.defects ?? [], [defects.data])
  const openDefects = defectRows.filter((d) => d.status === "open").length
  const activeOrders = orderRows.filter((o) => o.status !== "completed" && o.status !== "cancelled").length
  const inProgress = orderRows.filter((o) => o.status === "in_progress").length
  const unassigned = orderRows.filter(
    (o) =>
      o.status !== "completed" &&
      o.status !== "cancelled" &&
      !o.assigned_to_user_id &&
      !o.crew_id &&
      !o.assigned_to,
  ).length

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex flex-col gap-4">
        <div className="flex items-center justify-between gap-2">
          <div>
            <h1 className="flex items-center gap-2 text-lg font-semibold">
              <Wrench className="size-5" />
              Works
            </h1>
            <p className="text-muted-foreground text-sm">
              Track asset defects and the work orders that resolve them — inspection, corrective, preventive and
              construction — through to completion.
            </p>
          </div>
          {canWrite && (
            <div className="flex shrink-0 items-center gap-2">
              <Button variant="outline" size="sm" onClick={() => setLogging(true)}>
                <TriangleAlert className="size-4" />
                Log defect
              </Button>
              <Button size="sm" onClick={() => setCreating(true)}>
                <Plus className="size-4" />
                New work order
              </Button>
            </div>
          )}
        </div>

        <WorksKpiStrip
          openDefects={openDefects}
          activeOrders={activeOrders}
          inProgress={inProgress}
          unassigned={unassigned}
        />

        {/* defect -> order flow board: incoming defects LEFT, work orders in flight RIGHT */}
        <div className="grid gap-4 xl:grid-cols-2">
          <Card className="flex min-h-0 flex-col">
            <CardHeader className="pb-2">
              <CardTitle className="flex items-center gap-2 text-sm">
                <TriangleAlert className="size-4" />
                Defects
              </CardTitle>
              <CardDescription>
                {defectRows.length === 0
                  ? "No defects."
                  : `${defectRows.length} defect${defectRows.length === 1 ? "" : "s"}${openDefects > 0 ? ` · ${openDefects} open` : ""}.`}
              </CardDescription>
            </CardHeader>
            <CardContent className="pt-0">
              {defects.isPending ? (
                <LoadingRow label="Loading defects…" />
              ) : defects.isError ? (
                <p className="text-destructive text-sm">Could not load defects.</p>
              ) : defectRows.length > 0 ? (
                <div className="flex flex-col">
                  {defectRows.map((d) => (
                    <DefectRow key={d.id} defect={d} canWrite={canWrite} onRaise={setRaising} />
                  ))}
                </div>
              ) : (
                <p className="text-muted-foreground text-sm">No defects logged yet.</p>
              )}
            </CardContent>
          </Card>

          <Card className="flex min-h-0 flex-col">
            <CardHeader className="pb-2">
              <div className="flex items-start justify-between gap-2">
                <div>
                  <CardTitle className="flex items-center gap-2 text-sm">
                    <ClipboardList className="size-4" />
                    Work orders
                  </CardTitle>
                  <CardDescription>
                    {orderRows.length === 0
                      ? mineOnly
                        ? "No work orders assigned to you."
                        : "No work orders."
                      : `${orderRows.length} work order${orderRows.length === 1 ? "" : "s"}${mineOnly ? " assigned to you" : ""}.`}
                  </CardDescription>
                </div>
                <label className="text-muted-foreground flex shrink-0 items-center gap-1.5 text-xs">
                  <input type="checkbox" checked={mineOnly} onChange={(e) => setMineOnly(e.target.checked)} />
                  Mine
                </label>
              </div>
            </CardHeader>
            <CardContent className="pt-0">
              {orders.isPending ? (
                <LoadingRow label="Loading work orders…" />
              ) : orders.isError ? (
                <p className="text-destructive text-sm">Could not load work orders.</p>
              ) : orderRows.length > 0 ? (
                <div className="flex flex-col">
                  {orderRows.map((o) => (
                    <WorkOrderRow key={o.id} order={o} canWrite={canWrite} assignees={assignees} />
                  ))}
                </div>
              ) : (
                <p className="text-muted-foreground text-sm">
                  No work orders yet. Raise one from a defect, or create one directly.
                </p>
              )}
            </CardContent>
          </Card>
        </div>

        <InspectionsPanel canWrite={canWrite} workOrders={orderRows} />
      </div>

      {logging && <LogDefectDialog onClose={() => setLogging(false)} />}
      {creating && <NewWorkOrderDialog onClose={() => setCreating(false)} />}
      {raising && <RaiseWorkOrderDialog key={raising.id} defect={raising} onClose={() => setRaising(null)} />}
    </div>
  )
}

function LoadingRow({ label }: { label: string }) {
  return (
    <div className="text-muted-foreground flex items-center gap-2 text-sm">
      <Loader2 className="size-4 animate-spin" />
      {label}
    </div>
  )
}
