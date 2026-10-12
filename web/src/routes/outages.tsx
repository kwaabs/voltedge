import { type ReactNode, useEffect, useMemo, useState } from "react"
import { useNavigate } from "react-router"
import {
  Activity,
  CalendarClock,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Gauge,
  ListChecks,
  Loader2,
  MapPin,
  MapPinned,
  Pencil,
  PhoneCall,
  Plus,
  PowerOff,
  RotateCcw,
  ShieldAlert,
  Siren,
  Users,
  XCircle,
  Zap,
} from "lucide-react"

import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { OutageMap } from "@/components/map/outage-map"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import { useCircuits } from "@/hooks/use-circuits"
import { useRaiseFaultFromIncident } from "@/hooks/use-faults"
import {
  type CallType,
  type Incident,
  type IncidentAction,
  type IncidentStatus,
  type FaultCauseContribution,
  type SwitchingOrder,
  type SwitchingStep,
  type TroubleCall,
  useAssociateTroubleCall,
  useAttributeIncident,
  useCancelSwitchingOrder,
  useCreatePlannedOutage,
  useCreateTroubleCall,
  useExecuteStep,
  useGenerateSwitchingOrder,
  useIncidentAction,
  useIncidentEnergization,
  useIncidents,
  usePredictIncidents,
  useReliabilityIndices,
  useSwitchingOrders,
  useTroubleCalls,
} from "@/hooks/use-oms"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useOmsHighlightStore } from "@/stores/oms-highlight-store"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"
const CALL_TYPES: CallType[] = ["no_supply", "partial", "flicker"]

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

function shortId(id: string): string {
  return id.slice(0, 8)
}

function formatWhen(iso: string): string {
  return new Date(iso).toLocaleString()
}

/** Compact elapsed duration between a start and an end (defaults to now), for
 * the "how long has this been running" column: 47m, 3h 12m, 2d 4h. */
function elapsed(startIso: string, endIso?: string | null, now = Date.now()): string {
  const start = new Date(startIso).getTime()
  const end = endIso ? new Date(endIso).getTime() : now
  const mins = Math.max(0, Math.floor((end - start) / 60_000))
  if (mins < 60) return `${mins}m`
  const hrs = Math.floor(mins / 60)
  if (hrs < 24) return `${hrs}h ${mins % 60}m`
  const days = Math.floor(hrs / 24)
  return `${days}d ${hrs % 24}h`
}

/** A coarse re-render tick so live durations (elapsed time) advance on their own
 * without a refetch. `ms` between ticks; the returned value only changes on the
 * tick, so it's a cheap dependency to read. */
function useTick(ms: number): number {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), ms)
    return () => clearInterval(id)
  }, [ms])
  return now
}

const TERMINAL_STATUSES: IncidentStatus[] = ["restored", "cancelled"]
function isOpenIncident(inc: Incident): boolean {
  return !TERMINAL_STATUSES.includes(inc.status)
}

/** Severity tint for an incident row, by customers affected -- a quick triage
 * read across the board (major/moderate/minor/none). */
function severityAccent(customers: number): string {
  if (customers >= 500) return "border-l-red-500"
  if (customers >= 50) return "border-l-amber-500"
  if (customers > 0) return "border-l-yellow-400"
  return "border-l-transparent"
}

// --- status badges ---------------------------------------------------------

const INCIDENT_BADGE: Record<IncidentStatus, string> = {
  predicted: "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300",
  scheduled: "bg-violet-100 text-violet-800 dark:bg-violet-950 dark:text-violet-300",
  confirmed: "bg-blue-100 text-blue-800 dark:bg-blue-950 dark:text-blue-300",
  dispatched: "bg-indigo-100 text-indigo-800 dark:bg-indigo-950 dark:text-indigo-300",
  restored: "bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300",
  cancelled: "bg-muted text-muted-foreground",
}

function StatusBadge({ status }: { status: string }) {
  const cls = INCIDENT_BADGE[status as IncidentStatus] ?? "bg-muted text-muted-foreground"
  return <span className={`rounded px-1.5 py-0.5 text-xs font-medium capitalize ${cls}`}>{status}</span>
}

// --- log-call dialog -------------------------------------------------------

/** Intake form. SP-optional: enter a known service point, a raw address, a
 * lat/lng, or any mix -- at least one locator is required (server-enforced,
 * mirrored here so the button disables until it's satisfiable). */
function LogCallDialog({ onClose }: { onClose: () => void }) {
  const [servicePointId, setServicePointId] = useState("")
  const [address, setAddress] = useState("")
  const [lat, setLat] = useState("")
  const [lng, setLng] = useState("")
  const [callerRef, setCallerRef] = useState("")
  const [callType, setCallType] = useState<CallType>("no_supply")
  const [notes, setNotes] = useState("")
  const create = useCreateTroubleCall()

  const latNum = lat.trim() === "" ? undefined : Number(lat)
  const lngNum = lng.trim() === "" ? undefined : Number(lng)
  const hasLatLng = latNum !== undefined && lngNum !== undefined && Number.isFinite(latNum) && Number.isFinite(lngNum)
  const hasLocator = servicePointId.trim() !== "" || address.trim() !== "" || hasLatLng
  const canSave = hasLocator && !create.isPending

  function handleSave() {
    create.mutate(
      {
        service_point_id: servicePointId.trim() || undefined,
        address: address.trim() || undefined,
        longitude: hasLatLng ? lngNum : undefined,
        latitude: hasLatLng ? latNum : undefined,
        caller_ref: callerRef.trim() || undefined,
        call_type: callType,
        notes: notes.trim() || undefined,
      },
      {
        onSuccess: onClose,
        onError: (err) => toastError(err, "Could not log the call."),
      },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Log trouble call"
      description="At least one locator is required: a service point, an address, or a lat/lng."
      onSubmit={handleSave}
      submitLabel="Log call"
      canSubmit={canSave}
      saving={create.isPending}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Service point ID</label>
        <input
          className={inputClass}
          value={servicePointId}
          onChange={(e) => setServicePointId(e.target.value)}
          placeholder="UUID of a known service point"
          autoFocus
        />
        <span className="text-muted-foreground text-xs">A resolved SP lets this call feed the prediction rollup.</span>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Address</label>
        <input className={inputClass} value={address} onChange={(e) => setAddress(e.target.value)} placeholder="Free-text address" />
      </div>

      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Latitude</label>
          <input className={inputClass} type="number" step="any" value={lat} onChange={(e) => setLat(e.target.value)} />
        </div>
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Longitude</label>
          <input className={inputClass} type="number" step="any" value={lng} onChange={(e) => setLng(e.target.value)} />
        </div>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Call type</label>
        <select className={inputClass} value={callType} onChange={(e) => setCallType(e.target.value as CallType)}>
          {CALL_TYPES.map((t) => (
            <option key={t} value={t}>
              {t.replace("_", " ")}
            </option>
          ))}
        </select>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Caller reference</label>
        <input className={inputClass} value={callerRef} onChange={(e) => setCallerRef(e.target.value)} placeholder="Phone / account (optional)" />
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Notes</label>
        <input className={inputClass} value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="Optional" />
      </div>
    </FormDialog>
  )
}

// --- associate (resolve SP) dialog -----------------------------------------

/** Resolve an unlocated call to a service point: type the SP id, or snap to
 * the nearest SP when the call carries a location. */
function AssociateDialog({ call, onClose }: { call: TroubleCall; onClose: () => void }) {
  const [servicePointId, setServicePointId] = useState("")
  const associate = useAssociateTroubleCall()
  const hasLocation = call.longitude != null && call.latitude != null
  const canSave = servicePointId.trim() !== "" && !associate.isPending

  function run(body: { service_point_id?: string; snap?: boolean }) {
    associate.mutate(
      { id: call.id, body },
      { onSuccess: onClose, onError: (err) => toastError(err, "Could not associate the call.") },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Associate call to a service point"
      description="Set the service point this call belongs to so it can feed the prediction rollup."
      onSubmit={() => run({ service_point_id: servicePointId.trim() })}
      submitLabel="Set service point"
      canSubmit={canSave}
      saving={associate.isPending}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Service point ID</label>
        <input
          className={inputClass}
          value={servicePointId}
          onChange={(e) => setServicePointId(e.target.value)}
          placeholder="UUID of a service point"
          autoFocus
        />
      </div>

      {hasLocation && (
        <div className="flex flex-col gap-1">
          <span className="text-muted-foreground text-xs">
            Or snap this call to the nearest service point to its location ({call.latitude?.toFixed(5)},{" "}
            {call.longitude?.toFixed(5)}).
          </span>
          <Button
            type="button"
            variant="outline"
            size="sm"
            disabled={associate.isPending}
            onClick={() => run({ snap: true })}
          >
            <MapPin className="size-4" />
            Snap to nearest
          </Button>
        </div>
      )}
    </FormDialog>
  )
}

/** Name or correct the blamed device of a predicted/scheduled incident. The
 * server recomputes the affected counts from the device and clears the engine
 * confidence (POST /oms/incidents/{id}/attribute). */
function AttributeDialog({ inc, onClose }: { inc: Incident; onClose: () => void }) {
  const [deviceClass, setDeviceClass] = useState(inc.predicted_device_class ?? "")
  const [deviceId, setDeviceId] = useState(inc.predicted_device_id ?? "")
  const attribute = useAttributeIncident()
  const canSave = deviceClass.trim() !== "" && deviceId.trim() !== "" && !attribute.isPending

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Attribute incident to a device"
      description="Set the device this outage is blamed on. Affected customers are recomputed from it and the prediction confidence is cleared."
      onSubmit={() =>
        attribute.mutate(
          { id: inc.id, device_class: deviceClass.trim(), device_id: deviceId.trim() },
          { onSuccess: onClose, onError: (err) => toastError(err, "Could not attribute the incident.") },
        )
      }
      submitLabel="Attribute"
      canSubmit={canSave}
      saving={attribute.isPending}
    >
      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Device class</label>
          <input className={inputClass} value={deviceClass} onChange={(e) => setDeviceClass(e.target.value)} placeholder="e.g. MV_RECLOSER" autoFocus />
        </div>
        <div className="flex flex-[2] flex-col gap-1">
          <label className={labelClass}>Device ID</label>
          <input className={inputClass} value={deviceId} onChange={(e) => setDeviceId(e.target.value)} placeholder="UUID of the device" />
        </div>
      </div>
    </FormDialog>
  )
}

// --- schedule planned outage dialog ----------------------------------------

/** Book a planned outage around operating one device. The device is required
 * (its downstream cone gives the affected snapshot); the window is optional. A
 * datetime-local value is naive, so it's converted to an ISO instant on submit. */
function SchedulePlannedDialog({ onClose }: { onClose: () => void }) {
  const [deviceClass, setDeviceClass] = useState("")
  const [deviceId, setDeviceId] = useState("")
  const [start, setStart] = useState("")
  const [end, setEnd] = useState("")
  const [causeCode, setCauseCode] = useState("")
  const [notes, setNotes] = useState("")
  const create = useCreatePlannedOutage()

  const windowValid = !start || !end || new Date(start) < new Date(end)
  const canSave = deviceClass.trim() !== "" && deviceId.trim() !== "" && windowValid && !create.isPending

  function toIso(local: string): string | undefined {
    if (!local) return undefined
    const d = new Date(local)
    return Number.isNaN(d.getTime()) ? undefined : d.toISOString()
  }

  function handleSave() {
    create.mutate(
      {
        device_class: deviceClass.trim(),
        device_id: deviceId.trim(),
        planned_start: toIso(start),
        planned_end: toIso(end),
        cause_code: causeCode.trim() || undefined,
        notes: notes.trim() || undefined,
      },
      { onSuccess: onClose, onError: (err) => toastError(err, "Could not schedule the outage.") },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Schedule planned outage"
      description="Book an outage around operating a device. Its downstream customers are traced for the impact estimate."
      onSubmit={handleSave}
      submitLabel="Schedule"
      canSubmit={canSave}
      saving={create.isPending}
    >
      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Device class</label>
          <input
            className={inputClass}
            value={deviceClass}
            onChange={(e) => setDeviceClass(e.target.value)}
            placeholder="e.g. MV_RECLOSER"
            autoFocus
          />
        </div>
        <div className="flex flex-[2] flex-col gap-1">
          <label className={labelClass}>Device ID</label>
          <input className={inputClass} value={deviceId} onChange={(e) => setDeviceId(e.target.value)} placeholder="UUID of the device to operate" />
        </div>
      </div>

      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Planned start</label>
          <input className={inputClass} type="datetime-local" value={start} onChange={(e) => setStart(e.target.value)} />
        </div>
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Planned end</label>
          <input className={inputClass} type="datetime-local" value={end} onChange={(e) => setEnd(e.target.value)} />
        </div>
      </div>
      {!windowValid && <span className="text-destructive text-xs">Start must be before end.</span>}

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Cause</label>
        <input className={inputClass} value={causeCode} onChange={(e) => setCauseCode(e.target.value)} placeholder="e.g. transformer replacement (optional)" />
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Notes</label>
        <input className={inputClass} value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="Optional" />
      </div>
    </FormDialog>
  )
}

// --- switching orders ------------------------------------------------------

const STEP_ACTION_LABEL: Record<SwitchingStep["action"], string> = {
  open: "Open",
  close: "Close",
  earth: "Earth",
  verify: "Verify",
  note: "Note",
}

function StepRow({ step, canWrite }: { step: SwitchingStep; canWrite: boolean }) {
  const execute = useExecuteStep()
  const done = step.executed_at != null

  function toggle() {
    execute.mutate(
      { stepId: step.id, executed: !done },
      { onError: (err) => toastError(err, "Could not update the step.") },
    )
  }

  return (
    <div className="flex items-start gap-2 py-1">
      <input
        type="checkbox"
        className="mt-1 size-4 shrink-0"
        checked={done}
        disabled={!canWrite || execute.isPending}
        onChange={toggle}
      />
      <div className="flex flex-col">
        <span className={`text-sm ${done ? "text-muted-foreground line-through" : ""}`}>
          <span className="text-muted-foreground mr-1 tabular-nums">{step.step_no}.</span>
          <span className="font-medium">{STEP_ACTION_LABEL[step.action]}</span> — {step.instruction}
        </span>
        {done && step.executed_at && (
          <span className="text-muted-foreground text-[11px]">Done {formatWhen(step.executed_at)}</span>
        )}
      </div>
    </div>
  )
}

const SWITCHING_BADGE: Record<SwitchingOrder["status"], string> = {
  draft: "bg-muted text-muted-foreground",
  issued: "bg-blue-100 text-blue-800 dark:bg-blue-950 dark:text-blue-300",
  in_progress: "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300",
  completed: "bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300",
  cancelled: "bg-muted text-muted-foreground line-through",
}

function SwitchingOrderCard({ order, canWrite }: { order: SwitchingOrder; canWrite: boolean }) {
  const cancel = useCancelSwitchingOrder()
  const isTerminal = order.status === "completed" || order.status === "cancelled"
  const isolate = order.steps.filter((s) => s.phase === "isolate")
  const restore = order.steps.filter((s) => s.phase === "restore")

  // Terminal orders (cancelled/completed) collapse to their header by default so
  // a history of superseded programs doesn't blow up the panel height; active
  // ones stay open so the crew sees the steps they're executing. Click to toggle.
  const [open, setOpen] = useState(!isTerminal)
  const doneCount = order.steps.filter((s) => s.executed_at != null).length

  return (
    <div className="border-border rounded-md border p-2">
      <div className="flex items-center gap-2">
        <button
          type="button"
          className="flex min-w-0 flex-1 items-center gap-2 text-left"
          onClick={() => setOpen((v) => !v)}
          aria-expanded={open}
        >
          {open ? (
            <ChevronDown className="text-muted-foreground size-4 shrink-0" />
          ) : (
            <ChevronRight className="text-muted-foreground size-4 shrink-0" />
          )}
          <span className={`rounded px-1.5 py-0.5 text-xs font-medium capitalize ${SWITCHING_BADGE[order.status]}`}>
            {order.status.replace("_", " ")}
          </span>
          <span className="truncate text-sm font-medium">{order.reference ?? "Switching order"}</span>
          {!open && (
            <span className="text-muted-foreground shrink-0 text-[11px] tabular-nums">
              {doneCount}/{order.steps.length} steps
            </span>
          )}
        </button>
        {canWrite && !isTerminal && (
          <Button
            variant="ghost"
            size="sm"
            className="text-destructive ml-auto shrink-0"
            disabled={cancel.isPending}
            onClick={() =>
              cancel.mutate(
                { orderId: order.id },
                { onError: (err) => toastError(err, "Could not cancel the order.") },
              )
            }
          >
            <XCircle className="size-4" />
            Cancel
          </Button>
        )}
      </div>
      {open && (
        <>
          {isolate.length > 0 && (
            <>
              <p className="text-muted-foreground mt-1 text-[11px] font-medium uppercase">Isolate</p>
              {isolate.map((s) => (
                <StepRow key={s.id} step={s} canWrite={canWrite} />
              ))}
            </>
          )}
          {restore.length > 0 && (
            <>
              <p className="text-muted-foreground mt-1 text-[11px] font-medium uppercase">Restore</p>
              {restore.map((s) => (
                <StepRow key={s.id} step={s} canWrite={canWrite} />
              ))}
            </>
          )}
        </>
      )}
    </div>
  )
}

/** The switching-order sub-panel under an incident row (expanded on demand).
 * Shows the incident's orders, or a Generate action when none exists and the
 * incident has a device to switch. */
function SwitchingOrdersPanel({
  incidentId,
  hasDevice,
  canWrite,
}: {
  incidentId: string
  hasDevice: boolean
  canWrite: boolean
}) {
  const orders = useSwitchingOrders(incidentId)
  const generate = useGenerateSwitchingOrder()
  const rows = orders.data?.switching_orders ?? []
  const hasActive = rows.some((o) => o.status !== "cancelled")

  return (
    <div className="bg-muted/30 mt-1 flex flex-col gap-2 rounded-md p-2">
      {orders.isPending ? (
        <LoadingRow label="Loading switching orders…" />
      ) : orders.isError ? (
        <p className="text-destructive text-sm">Could not load switching orders.</p>
      ) : (
        <>
          {rows.map((o) => (
            <SwitchingOrderCard key={o.id} order={o} canWrite={canWrite} />
          ))}
          {rows.length === 0 && (
            <p className="text-muted-foreground text-xs">No switching order yet.</p>
          )}
          {canWrite && !hasActive && hasDevice && (
            <Button
              variant="outline"
              size="sm"
              className="self-start"
              disabled={generate.isPending}
              onClick={() =>
                generate.mutate(
                  { incidentId },
                  { onError: (err) => toastError(err, "Could not generate a switching order.") },
                )
              }
            >
              {generate.isPending ? <Loader2 className="size-4 animate-spin" /> : <ListChecks className="size-4" />}
              Generate switching order
            </Button>
          )}
        </>
      )}
    </div>
  )
}

// --- incident row ----------------------------------------------------------

function deviceLabel(inc: Incident): string {
  if (!inc.predicted_device_class || !inc.predicted_device_id) return "Unattributed"
  return `${inc.predicted_device_class} · ${shortId(inc.predicted_device_id)}`
}

/** The live de-energization readout for an active incident: how many customers
 * are off supply RIGHT NOW given the network's current switch positions (which
 * executing switching steps changes). Distinct from the frozen affected count on
 * the incident itself -- this shrinks to "supply on" as the crew closes the
 * device. Only mounted for an active incident that has a device to trace from. */
function LiveDeEnergized({ incidentId }: { incidentId: string }) {
  const energization = useIncidentEnergization(incidentId)
  const d = energization.data

  if (energization.isPending) {
    return <span className="text-muted-foreground text-[11px]">checking supply…</span>
  }
  if (!d) return null

  const off = d.customers
  const open = d.device_state === "OPEN"
  if (!open || off === 0) {
    return (
      <span className="inline-flex items-center gap-1 rounded bg-emerald-100 px-1.5 py-0.5 text-[11px] font-medium text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300">
        <CheckCircle2 className="size-3" />
        Supply on
      </span>
    )
  }
  // Critical customers off supply right now (restoration rank <= P2), surfaced
  // beside the raw count so control-room triage sees the life-support/essential
  // load first (Phase 62 service rollup rides along on the energization state).
  const criticalOff = d.service?.critical.customers ?? 0
  return (
    <span className="inline-flex items-center gap-1">
      <span className="inline-flex items-center gap-1 rounded bg-red-100 px-1.5 py-0.5 text-[11px] font-medium text-red-800 dark:bg-red-950 dark:text-red-300">
        <PowerOff className="size-3" />
        {off.toLocaleString()} off supply now
      </span>
      {criticalOff > 0 && (
        <span
          className="inline-flex items-center gap-1 rounded bg-red-600 px-1.5 py-0.5 text-[11px] font-semibold text-white"
          title="Critical customers (life-support / essential) off supply now"
        >
          <ShieldAlert className="size-3" />
          {criticalOff.toLocaleString()} critical
        </span>
      )}
    </span>
  )
}

function IncidentRow({
  inc,
  canWrite,
  now,
  selected,
  onSelect,
}: {
  inc: Incident
  canWrite: boolean
  now: number
  selected: boolean
  onSelect: (id: string) => void
}) {
  const action = useIncidentAction()
  const raiseFault = useRaiseFaultFromIncident()
  const canRaiseFault = usePermission()("faults:write")
  const navigate = useNavigate()
  const pending = action.isPending
  const [attributing, setAttributing] = useState(false)

  function run(kind: IncidentAction) {
    action.mutate({ id: inc.id, action: kind }, { onError: (err) => toastError(err, `Could not ${kind} the incident.`) })
  }

  // Raise a fault event for this outage -- the electrical cause behind it. The
  // fault inherits the incident's predicted device + cause and links back by
  // incident_id (api/internal/faults RaiseFaultFromIncident). faults:write, kept
  // separate from oms:write: attributing a fault is an engineering act, not an
  // outage-dispatch one.
  function raiseFaultForIncident() {
    raiseFault.mutate(
      { incidentId: inc.id },
      {
        onSuccess: () => useToastStore.getState().pushSuccess("Fault raised for this outage."),
        onError: (err) => toastError(err, "Could not raise a fault for this incident."),
      },
    )
  }

  // Highlight this incident's outage cone on the main map. A read action (no
  // oms:write), offered only when there's an affected set to draw.
  function showOnMap() {
    useOmsHighlightStore.getState().show(inc.id)
    navigate("/")
  }

  const customers = inc.affected_customers ?? 0
  const hasArea = (inc.affected_service_points ?? 0) > 0
  const hasDevice = inc.predicted_device_class != null && inc.predicted_device_id != null
  const isPlanned = inc.outage_type === "planned"
  const isTerminal = inc.status === "restored" || inc.status === "cancelled"
  const isActive = inc.status === "confirmed" || inc.status === "dispatched"
  // A predicted incident awaits confirm; a scheduled (planned) one awaits its
  // execution window, then "Begin outage" starts the interruption clock.
  const canConfirm = inc.status === "predicted" || inc.status === "scheduled"

  return (
    <div
      className={cn(
        "cursor-pointer border-l-2",
        severityAccent(customers),
        isTerminal && "opacity-70",
        selected && "bg-primary/5 ring-primary/40 ring-1 ring-inset",
      )}
      onClick={() => onSelect(inc.id)}
    >
      <div className="hover:bg-muted/40 flex flex-wrap items-center gap-x-3 gap-y-1.5 py-2 pl-3 pr-2">
        {selected ? (
          <ChevronDown className="text-primary size-4 shrink-0" />
        ) : (
          <ChevronRight className="text-muted-foreground size-4 shrink-0" />
        )}
        <StatusBadge status={inc.status} />
        <span className="text-sm font-medium">{deviceLabel(inc)}</span>

        <div className="flex items-center gap-1.5 text-xs">
          <span className="inline-flex items-center gap-1 tabular-nums" title="Customers affected">
            <Users className="text-muted-foreground size-3.5" />
            {customers.toLocaleString()}
          </span>
          <span className="text-muted-foreground tabular-nums" title="Service points affected">
            · {inc.affected_service_points ?? 0} SP
          </span>
          {inc.confidence != null && (
            <span className="text-muted-foreground tabular-nums">· {Math.round(inc.confidence * 100)}%</span>
          )}
        </div>

        {isActive && hasDevice && <LiveDeEnergized incidentId={inc.id} />}

        <div className="text-muted-foreground ml-auto flex items-center gap-3 text-xs">
          {!isPlanned && (
            <span className="tabular-nums" title={`Started ${formatWhen(inc.started_at)}`}>
              {elapsed(inc.started_at, inc.restored_at, now)}
              {inc.restored_at ? " (restored)" : " elapsed"}
            </span>
          )}
          <span title={isPlanned && inc.planned_start ? "Planned window" : "Started"}>
            {isPlanned && inc.planned_start
              ? `${formatWhen(inc.planned_start)}${inc.planned_end ? ` – ${formatWhen(inc.planned_end)}` : ""}`
              : formatWhen(inc.started_at)}
          </span>
        </div>

        <div className="flex shrink-0 items-center gap-0.5" onClick={(e) => e.stopPropagation()}>
          {hasArea && (
            <Button variant="ghost" size="sm" className="shrink-0" onClick={showOnMap} title="Highlight on the main map">
              <MapPinned className="size-4" />
            </Button>
          )}
          {canRaiseFault && inc.status !== "cancelled" && (
            <Button
              variant="ghost"
              size="sm"
              className="shrink-0"
              disabled={raiseFault.isPending}
              onClick={raiseFaultForIncident}
              title="Raise a fault event for this outage"
            >
              <Zap className="size-4" />
            </Button>
          )}
          {canWrite && !isTerminal && (
            <>
              {canConfirm && (
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={pending}
                  onClick={() => setAttributing(true)}
                  title={hasDevice ? "Change the blamed device" : "Attribute to a device"}
                >
                  <Pencil className="size-4" />
                </Button>
              )}
              {canConfirm && (
                <Button variant="outline" size="sm" disabled={pending} onClick={() => run("confirm")}>
                  <CheckCircle2 className="size-4" />
                  {isPlanned ? "Begin" : "Confirm"}
                </Button>
              )}
              {isActive && (
                <Button variant="outline" size="sm" disabled={pending} onClick={() => run("restore")}>
                  <RotateCcw className="size-4" />
                  Restore
                </Button>
              )}
              <Button
                variant="ghost"
                size="sm"
                className="text-destructive"
                disabled={pending}
                onClick={() => run("cancel")}
                title="Cancel this incident"
              >
                <XCircle className="size-4" />
              </Button>
            </>
          )}
        </div>
      </div>
      {attributing && (
        <div onClick={(e) => e.stopPropagation()}>
          <AttributeDialog inc={inc} onClose={() => setAttributing(false)} />
        </div>
      )}
    </div>
  )
}

// --- trouble-call row ------------------------------------------------------

function callLocator(call: TroubleCall): string {
  if (call.service_point_id) return `SP ${shortId(call.service_point_id)}`
  if (call.address) return call.address
  if (call.latitude != null && call.longitude != null) return `${call.latitude.toFixed(5)}, ${call.longitude.toFixed(5)}`
  return "—"
}

function TroubleCallRow({
  call,
  canWrite,
  onAssociate,
}: {
  call: TroubleCall
  canWrite: boolean
  onAssociate: (c: TroubleCall) => void
}) {
  const needsSP = call.service_point_id == null && call.status === "open"
  return (
    <div className="hover:bg-muted/40 flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md px-2 py-1.5">
      <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs font-medium capitalize">
        {call.status}
      </span>
      <span className="text-sm">{callLocator(call)}</span>
      <span className="text-muted-foreground text-xs capitalize">{call.call_type.replace("_", " ")}</span>
      <span className="text-muted-foreground ml-auto text-xs">{formatWhen(call.reported_at)}</span>
      {canWrite && needsSP && (
        <Button variant="outline" size="sm" className="shrink-0" onClick={() => onAssociate(call)}>
          <MapPin className="size-4" />
          Associate
        </Button>
      )}
    </div>
  )
}

// --- reliability indices ---------------------------------------------------

function fmt(n: number, digits = 2): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

function IndexStat({ label, value, unit, hint }: { label: string; value: string; unit: string; hint: string }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-muted-foreground text-xs font-medium">{label}</span>
      <span className="text-lg font-semibold tabular-nums">
        {value} <span className="text-muted-foreground text-xs font-normal">{unit}</span>
      </span>
      <span className="text-muted-foreground text-[11px]">{hint}</span>
    </div>
  )
}

/** System reliability indices (IEEE 1366) over the trailing 12 months, summed
 * over confirmed customer interruptions. ENS is estimated (no measured load).
 * A circuit picker scopes the whole report to one feeder's service points
 * (per-feeder indices); "Whole system" is the default. */
function ReliabilityCard() {
  const [circuitId, setCircuitId] = useState("")
  const circuits = useCircuits()
  const indices = useReliabilityIndices({ circuitId: circuitId || undefined })
  const d = indices.data

  const circuitRows = circuits.data?.circuits ?? []
  const scopeLabel = circuitId
    ? (circuitRows.find((c) => c.id === circuitId)?.name ?? "circuit")
    : "Whole system"
  // A 400 here is almost always the apply-order gap the server flags: the
  // per-circuit denominator needs gis.service_point.circuit_id, which only
  // exists after the Phase 37 circuit-membership seed + a recompile. Surface
  // that message rather than a generic failure so the operator knows the fix.
  const errorMsg =
    indices.error instanceof ApiError ? indices.error.message : "Could not compute reliability indices."

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-start justify-between gap-2">
          <div>
            <CardTitle className="flex items-center gap-2 text-sm">
              <Gauge className="size-4" />
              Reliability indices
            </CardTitle>
            <CardDescription>
              {d
                ? `Trailing 12 months · ${scopeLabel} · ${d.total_customers.toLocaleString()} customers served`
                : "System reliability over confirmed interruptions."}
            </CardDescription>
          </div>
          <select
            className={inputClass}
            value={circuitId}
            onChange={(e) => setCircuitId(e.target.value)}
            aria-label="Scope reliability indices to a circuit"
          >
            <option value="">Whole system</option>
            {circuitRows.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name} ({c.network})
              </option>
            ))}
          </select>
        </div>
      </CardHeader>
      <CardContent className="pt-0">
        {indices.isPending ? (
          <LoadingRow label="Computing indices…" />
        ) : indices.isError || !d ? (
          <p className="text-destructive text-sm">{errorMsg}</p>
        ) : (
          <>
            <div className="grid grid-cols-2 gap-4 sm:grid-cols-4">
              <IndexStat label="SAIFI" value={fmt(d.saifi)} unit="int/cust" hint="interruptions per customer" />
              <IndexStat label="SAIDI" value={fmt(d.saidi, 1)} unit="min/cust" hint="outage minutes per customer" />
              <IndexStat label="CAIDI" value={fmt(d.caidi, 1)} unit="min" hint="avg minutes per interruption" />
              <IndexStat label="ENS" value={fmt(d.ens_kwh, 1)} unit="kWh" hint={`est. @ ${fmt(d.avg_load_kw, 1)} kW/cust`} />
            </div>
            <p className="text-muted-foreground mt-3 text-[11px]">
              {d.customers_interrupted.toLocaleString()} customer interruptions over{" "}
              {d.interruption_events.toLocaleString()} events
              {d.ongoing_interruptions > 0 && ` · ${d.ongoing_interruptions} still ongoing`}. ENS is estimated from an
              assumed average demand; swap in measured load for a true figure.
            </p>
            <FaultCauseBreakdown rows={d.by_fault_cause} saidi={d.saidi} />
          </>
        )}
      </CardContent>
    </Card>
  )
}

/** Human label for a fault-cause bucket. A recorded cause shows as-is; a fault
 * with no cause reads "Uncategorised fault"; interruptions on an incident with no
 * fault event at all read "No fault linked" (told apart from the former by
 * fault_linked, not by the null cause). */
function faultCauseLabel(row: FaultCauseContribution): string {
  if (row.cause && row.cause.trim() !== "") return row.cause
  return row.fault_linked ? "Uncategorised fault" : "No fault linked"
}

/** The fault-caused decomposition of SAIDI: which fault causes drove the outage
 * minutes-per-customer, ranked, as share bars whose SAIDI sums back to the
 * system figure. Hidden unless at least one interruption is actually attributed
 * to a fault (otherwise the single "No fault linked" bucket just restates the
 * headline and adds nothing). */
function FaultCauseBreakdown({ rows, saidi }: { rows?: FaultCauseContribution[]; saidi: number }) {
  if (!rows || rows.length === 0) return null
  const hasFaultAttribution = rows.some((r) => r.fault_linked)
  if (!hasFaultAttribution) return null

  // Share of SAIDI (customer-minutes) per bucket; guard the all-zero window.
  const maxSaidi = Math.max(...rows.map((r) => r.saidi), 0)
  const totalSaidi = saidi > 0 ? saidi : rows.reduce((s, r) => s + r.saidi, 0)

  return (
    <div className="mt-3 border-t pt-3">
      <p className="text-muted-foreground mb-2 text-[11px] font-medium">SAIDI by fault cause</p>
      <div className="flex flex-col gap-1.5">
        {rows.map((r, i) => {
          const share = totalSaidi > 0 ? (r.saidi / totalSaidi) * 100 : 0
          const barPct = maxSaidi > 0 ? (r.saidi / maxSaidi) * 100 : 0
          return (
            <div key={`${r.fault_linked}:${r.cause ?? "∅"}:${i}`} className="flex items-center gap-2 text-xs">
              <span className={cn("w-40 shrink-0 truncate", !r.fault_linked && "text-muted-foreground italic")}>
                {faultCauseLabel(r)}
              </span>
              <div className="bg-muted relative h-3 flex-1 overflow-hidden rounded-sm">
                <div
                  className={cn("h-full rounded-sm", r.fault_linked ? "bg-amber-500" : "bg-muted-foreground/40")}
                  style={{ width: `${barPct}%` }}
                />
              </div>
              <span className="w-24 shrink-0 text-right tabular-nums">
                {fmt(r.saidi, 1)} <span className="text-muted-foreground">min/cust</span>
              </span>
              <span className="text-muted-foreground w-10 shrink-0 text-right tabular-nums">{fmt(share, 0)}%</span>
            </div>
          )
        })}
      </div>
      <p className="text-muted-foreground mt-2 text-[11px]">
        Each interruption is attributed to its incident's linked fault; buckets sum to the system SAIDI above.
      </p>
    </div>
  )
}

// --- KPI strip -------------------------------------------------------------

/** One headline figure in the control-room strip. `tone` tints the value when it
 * signals something needing attention (customers off supply, calls waiting). */
function KpiTile({
  icon,
  label,
  value,
  sub,
  tone = "default",
}: {
  icon: ReactNode
  label: string
  value: string
  sub?: string
  tone?: "default" | "alert" | "warn"
}) {
  const valueTone =
    tone === "alert"
      ? "text-red-600 dark:text-red-400"
      : tone === "warn"
        ? "text-amber-600 dark:text-amber-400"
        : "text-foreground"
  return (
    <Card className="flex-1">
      <CardContent className="flex items-center gap-3 p-3">
        <div className="text-muted-foreground shrink-0">{icon}</div>
        <div className="flex min-w-0 flex-col">
          <span className="text-muted-foreground text-[11px] font-medium uppercase tracking-wide">{label}</span>
          <span className={cn("text-2xl font-semibold tabular-nums leading-tight", valueTone)}>{value}</span>
          {sub && <span className="text-muted-foreground truncate text-[11px]">{sub}</span>}
        </div>
      </CardContent>
    </Card>
  )
}

/** The at-a-glance operating picture across the top of the console: how many
 * incidents are live, how many customers are off supply, how many calls are
 * waiting to be worked, and the system SAIDI. Derived from the same data the
 * board below shows, so it can never disagree with it. */
function KpiStrip({ incidents, openCalls }: { incidents: Incident[]; openCalls: number }) {
  const indices = useReliabilityIndices()
  const active = incidents.filter(isOpenIncident)
  const customersOff = active.reduce((sum, i) => sum + (i.affected_customers ?? 0), 0)
  const saidi = indices.data ? fmt(indices.data.saidi, 1) : "—"

  return (
    <div className="flex flex-col gap-3 sm:flex-row">
      <KpiTile
        icon={<Activity className="size-5" />}
        label="Active incidents"
        value={active.length.toLocaleString()}
        sub={`${incidents.length.toLocaleString()} total on record`}
        tone={active.length > 0 ? "warn" : "default"}
      />
      <KpiTile
        icon={<Users className="size-5" />}
        label="Customers off supply"
        value={customersOff.toLocaleString()}
        sub="across live incidents"
        tone={customersOff > 0 ? "alert" : "default"}
      />
      <KpiTile
        icon={<PhoneCall className="size-5" />}
        label="Calls in queue"
        value={openCalls.toLocaleString()}
        sub="open trouble calls"
        tone={openCalls > 0 ? "warn" : "default"}
      />
      <KpiTile
        icon={<Gauge className="size-5" />}
        label="SAIDI (12 mo)"
        value={saidi}
        sub="system minutes / customer"
      />
    </div>
  )
}

// --- switching drawer ------------------------------------------------------

/** The right-hand control drawer beside the embedded map: the selected
 * incident's live de-energization readout and its switching program. Executing
 * a step here flips the device's live state, which the map redraws -- the
 * operator watches the cone shrink as they close the device. */
function SwitchingDrawer({ incident, canWrite }: { incident: Incident | null; canWrite: boolean }) {
  if (!incident) {
    return (
      <Card>
        <CardContent className="text-muted-foreground p-4 text-sm">
          Select an incident to drive its switching.
        </CardContent>
      </Card>
    )
  }

  const hasDevice = incident.predicted_device_class != null && incident.predicted_device_id != null
  const isActive = incident.status === "confirmed" || incident.status === "dispatched"

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-center gap-2">
          <StatusBadge status={incident.status} />
          <CardTitle className="text-sm">{deviceLabel(incident)}</CardTitle>
          {isActive && hasDevice && (
            <span className="ml-auto">
              <LiveDeEnergized incidentId={incident.id} />
            </span>
          )}
        </div>
        <CardDescription>Switching program</CardDescription>
      </CardHeader>
      <CardContent className="pt-0">
        {hasDevice ? (
          <SwitchingOrdersPanel incidentId={incident.id} hasDevice={hasDevice} canWrite={canWrite} />
        ) : (
          <p className="text-muted-foreground text-sm">
            Unattributed incident — no device to switch. Attribute a device to build a switching order.
          </p>
        )}
      </CardContent>
    </Card>
  )
}

// --- page ------------------------------------------------------------------

export function OutagesPage() {
  const can = usePermission()
  const canWrite = can("oms:write")

  const incidents = useIncidents()
  const calls = useTroubleCalls()
  const predict = usePredictIncidents()

  const [logging, setLogging] = useState(false)
  const [scheduling, setScheduling] = useState(false)
  const [associating, setAssociating] = useState<TroubleCall | null>(null)
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const now = useTick(30_000)

  // Open incidents first (the ones a controller acts on), each group newest
  // first -- the board reads top-to-bottom as "what needs attention now".
  const incidentRows = useMemo(() => {
    const rows = incidents.data?.incidents ?? []
    return [...rows].sort((a, b) => {
      const aOpen = isOpenIncident(a)
      const bOpen = isOpenIncident(b)
      if (aOpen !== bOpen) return aOpen ? -1 : 1
      return new Date(b.started_at).getTime() - new Date(a.started_at).getTime()
    })
  }, [incidents.data])
  const activeCount = useMemo(() => incidentRows.filter(isOpenIncident).length, [incidentRows])
  const callRows = useMemo(() => calls.data?.trouble_calls ?? [], [calls.data])
  const openCallCount = useMemo(() => callRows.filter((c) => c.status === "open").length, [callRows])

  // Keep a valid incident selected for the map/drawer: default to the first row
  // (open incidents sort to the top) and drop a selection that has disappeared.
  const selected = useMemo(
    () => incidentRows.find((i) => i.id === selectedId) ?? null,
    [incidentRows, selectedId],
  )
  useEffect(() => {
    if (!selected && incidentRows.length > 0) setSelectedId(incidentRows[0].id)
  }, [selected, incidentRows])

  function runPredict() {
    predict.mutate(undefined, {
      onSuccess: (res) => {
        const created = (res as { incidents?: unknown[] })?.incidents?.length ?? 0
        const toast = useToastStore.getState()
        if (created === 0) toast.pushWarning("No open, SP-resolved calls to predict from.")
        else toast.pushSuccess(`Predicted ${created} incident${created === 1 ? "" : "s"}.`)
      },
      onError: (err) => toastError(err, "Prediction failed."),
    })
  }

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex flex-col gap-4">
        <div className="flex items-center justify-between gap-2">
          <div>
            <h1 className="flex items-center gap-2 text-lg font-semibold">
              <Siren className="size-5" />
              Outage control
            </h1>
            <p className="text-muted-foreground text-sm">
              Log trouble calls, roll them up to the protective device that likely operated, and drive each incident
              through switching to restoration.
            </p>
          </div>
          {canWrite && (
            <div className="flex shrink-0 items-center gap-2">
              <Button variant="outline" size="sm" disabled={predict.isPending} onClick={runPredict}>
                {predict.isPending ? <Loader2 className="size-4 animate-spin" /> : <Zap className="size-4" />}
                Run prediction
              </Button>
              <Button variant="outline" size="sm" onClick={() => setScheduling(true)}>
                <CalendarClock className="size-4" />
                Schedule outage
              </Button>
              <Button size="sm" onClick={() => setLogging(true)}>
                <Plus className="size-4" />
                Log call
              </Button>
            </div>
          )}
        </div>

        <KpiStrip incidents={incidentRows} openCalls={openCallCount} />

        <div className="grid gap-4 xl:grid-cols-[minmax(0,1fr)_26rem]">
          <div className="flex min-w-0 flex-col gap-4">
            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm">Incidents</CardTitle>
                <CardDescription>
                  {incidentRows.length === 0
                    ? "No incidents."
                    : `${activeCount} active · ${incidentRows.length} total`}
                </CardDescription>
              </CardHeader>
              <CardContent className="pt-0">
                {incidents.isPending ? (
                  <LoadingRow label="Loading incidents…" />
                ) : incidents.isError ? (
                  <p className="text-destructive text-sm">Could not load incidents.</p>
                ) : incidentRows.length > 0 ? (
                  <div className="flex flex-col">
                    {incidentRows.map((inc) => (
                      <IncidentRow
                        key={inc.id}
                        inc={inc}
                        canWrite={canWrite}
                        now={now}
                        selected={inc.id === selected?.id}
                        onSelect={setSelectedId}
                      />
                    ))}
                  </div>
                ) : (
                  <p className="text-muted-foreground text-sm">
                    Run prediction once open calls are logged and resolved.
                  </p>
                )}
              </CardContent>
            </Card>

            <Card>
              <CardHeader className="pb-2">
                <CardTitle className="text-sm">Trouble calls</CardTitle>
                <CardDescription>
                  {callRows.length === 0 ? "No calls." : `${callRows.length} call${callRows.length === 1 ? "" : "s"}.`}
                </CardDescription>
              </CardHeader>
              <CardContent className="pt-0">
                {calls.isPending ? (
                  <LoadingRow label="Loading calls…" />
                ) : calls.isError ? (
                  <p className="text-destructive text-sm">Could not load trouble calls.</p>
                ) : callRows.length > 0 ? (
                  <div className="flex flex-col">
                    {callRows.map((call) => (
                      <TroubleCallRow key={call.id} call={call} canWrite={canWrite} onAssociate={setAssociating} />
                    ))}
                  </div>
                ) : (
                  <p className="text-muted-foreground text-sm">No trouble calls logged yet.</p>
                )}
              </CardContent>
            </Card>

            <details className="group rounded-lg border bg-card">
              <summary className="flex cursor-pointer list-none items-center gap-2 px-4 py-3 text-sm font-medium select-none">
                <ChevronRight className="size-4 transition-transform group-open:rotate-90" />
                Reliability indices
                <span className="text-muted-foreground font-normal">— IEEE 1366 over the last 12 months</span>
              </summary>
              <div className="border-t px-4 py-3">
                <ReliabilityCard />
              </div>
            </details>
          </div>

          <div className="flex flex-col gap-3 xl:sticky xl:top-4 xl:self-start">
            <OutageMap incidentId={selected?.id ?? null} />
            <SwitchingDrawer incident={selected} canWrite={canWrite} />
          </div>
        </div>
      </div>

      {logging && <LogCallDialog onClose={() => setLogging(false)} />}
      {scheduling && <SchedulePlannedDialog onClose={() => setScheduling(false)} />}
      {associating && (
        <AssociateDialog key={associating.id} call={associating} onClose={() => setAssociating(null)} />
      )}
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
