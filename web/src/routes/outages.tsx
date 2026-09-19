import { useMemo, useState } from "react"
import { useNavigate } from "react-router"
import {
  CalendarClock,
  CheckCircle2,
  Gauge,
  ListChecks,
  Loader2,
  MapPin,
  MapPinned,
  Plus,
  RotateCcw,
  Siren,
  XCircle,
  Zap,
} from "lucide-react"

import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
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
  type SwitchingOrder,
  type SwitchingStep,
  type TroubleCall,
  useAssociateTroubleCall,
  useCancelSwitchingOrder,
  useCreatePlannedOutage,
  useCreateTroubleCall,
  useExecuteStep,
  useGenerateSwitchingOrder,
  useIncidentAction,
  useIncidents,
  usePredictIncidents,
  useReliabilityIndices,
  useSwitchingOrders,
  useTroubleCalls,
} from "@/hooks/use-oms"
import { ApiError } from "@/lib/api"
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

  return (
    <div className="border-border rounded-md border p-2">
      <div className="mb-1 flex items-center gap-2">
        <span className={`rounded px-1.5 py-0.5 text-xs font-medium capitalize ${SWITCHING_BADGE[order.status]}`}>
          {order.status.replace("_", " ")}
        </span>
        <span className="text-sm font-medium">{order.reference ?? "Switching order"}</span>
        {canWrite && !isTerminal && (
          <Button
            variant="ghost"
            size="sm"
            className="text-destructive ml-auto"
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

function IncidentRow({ inc, canWrite }: { inc: Incident; canWrite: boolean }) {
  const action = useIncidentAction()
  const raiseFault = useRaiseFaultFromIncident()
  const canRaiseFault = usePermission()("faults:write")
  const navigate = useNavigate()
  const [showSwitching, setShowSwitching] = useState(false)
  const pending = action.isPending

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

  const hasArea = (inc.affected_service_points ?? 0) > 0
  const hasDevice = inc.predicted_device_class != null && inc.predicted_device_id != null
  const isPlanned = inc.outage_type === "planned"
  const isTerminal = inc.status === "restored" || inc.status === "cancelled"
  // A predicted incident awaits confirm; a scheduled (planned) one awaits its
  // execution window, then "Begin outage" starts the interruption clock.
  const canConfirm = inc.status === "predicted" || inc.status === "scheduled"

  return (
    <div className="flex flex-col">
      <div className="hover:bg-muted/40 flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md px-2 py-2">
        <StatusBadge status={inc.status} />
        <span className="text-sm font-medium">{deviceLabel(inc)}</span>
        <span className="text-muted-foreground text-xs">
          {inc.affected_customers ?? 0} cust · {inc.affected_service_points ?? 0} SP
          {inc.confidence != null && <> · {Math.round(inc.confidence * 100)}% conf</>}
        </span>
        <span className="text-muted-foreground ml-auto text-xs">
          {isPlanned && inc.planned_start
            ? `Planned ${formatWhen(inc.planned_start)}${inc.planned_end ? ` – ${formatWhen(inc.planned_end)}` : ""}`
            : formatWhen(inc.started_at)}
        </span>

        {hasArea && (
          <Button variant="ghost" size="sm" className="shrink-0" onClick={showOnMap}>
            <MapPinned className="size-4" />
            Show on map
          </Button>
        )}

        <Button
          variant="ghost"
          size="sm"
          className="shrink-0"
          onClick={() => setShowSwitching((s) => !s)}
        >
          <ListChecks className="size-4" />
          Switching
        </Button>

        {canRaiseFault && inc.status !== "cancelled" && (
          <Button
            variant="ghost"
            size="sm"
            className="shrink-0"
            disabled={raiseFault.isPending}
            onClick={raiseFaultForIncident}
          >
            <Zap className="size-4" />
            Raise fault
          </Button>
        )}

        {canWrite && !isTerminal && (
          <div className="flex shrink-0 items-center gap-1">
            {canConfirm && (
              <Button variant="outline" size="sm" disabled={pending} onClick={() => run("confirm")}>
                <CheckCircle2 className="size-4" />
                {isPlanned ? "Begin outage" : "Confirm"}
              </Button>
            )}
            {(inc.status === "confirmed" || inc.status === "dispatched") && (
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
            >
              <XCircle className="size-4" />
              Cancel
            </Button>
          </div>
        )}
      </div>
      {showSwitching && (
        <div className="px-2 pb-2">
          <SwitchingOrdersPanel incidentId={inc.id} hasDevice={hasDevice} canWrite={canWrite} />
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
          </>
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

  const incidentRows = useMemo(() => incidents.data?.incidents ?? [], [incidents.data])
  const callRows = useMemo(() => calls.data?.trouble_calls ?? [], [calls.data])

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
      <div className="mx-auto flex max-w-4xl flex-col gap-4">
        <div className="flex items-center justify-between gap-2">
          <div>
            <h1 className="flex items-center gap-2 text-lg font-semibold">
              <Siren className="size-5" />
              Outages
            </h1>
            <p className="text-muted-foreground text-sm">
              Log trouble calls, roll them up to the protective device that likely operated, and manage each incident
              through to restoration.
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

        <ReliabilityCard />

        <Card>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm">Incidents</CardTitle>
            <CardDescription>
              {incidentRows.length === 0 ? "No incidents." : `${incidentRows.length} incident${incidentRows.length === 1 ? "" : "s"}.`}
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
                  <IncidentRow key={inc.id} inc={inc} canWrite={canWrite} />
                ))}
              </div>
            ) : (
              <p className="text-muted-foreground text-sm">Run prediction once open calls are logged and resolved.</p>
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
