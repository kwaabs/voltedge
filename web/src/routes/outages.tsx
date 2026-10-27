import { type ReactNode, useEffect, useMemo, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useNavigate, useParams } from "react-router"
import {
  Activity,
  ArrowLeft,
  Building2,
  CalendarClock,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Gauge,
  ListChecks,
  Loader2,
  MapPin,
  MapPinned,
  PhoneCall,
  PlugZap,
  Plus,
  PowerOff,
  Radio,
  RotateCcw,
  Search,
  ShieldAlert,
  Siren,
  Truck,
  UserCog,
  Users,
  Wrench,
  XCircle,
  Zap,
} from "lucide-react"

import { FormDialog } from "@/components/admin/form-dialog"
import { FeaturePicker, type PickedFeature } from "@/components/feature-picker"
import { inputClass } from "@/components/map/field-utils"
import { OutageMap } from "@/components/map/outage-map"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import { useCircuits } from "@/hooks/use-circuits"
import { type TerritorySummary, useMyTerritories, useTerritories } from "@/hooks/use-territories"
import { useFeature } from "@/hooks/use-feature"
import { useRaiseFaultFromIncident } from "@/hooks/use-faults"
import {
  type AffectedServicePoint,
  type CallType,
  type DeviceCandidate,
  type Incident,
  type IncidentAction,
  type IncidentStatus,
  type FaultCauseContribution,
  type RestorationPlan,
  type SwitchingOrder,
  type SwitchingStep,
  type TimelineEvent,
  type TroubleCall,
  type TroubleSpot,
  type AreaImpactLevel,
  useAdvanceAssignment,
  useAffectedArea,
  useAreaImpact,
  useAttributeIncidentDevice,
  useCallTimeline,
  useCrews,
  useDetectionMetrics,
  useDispatchCrew,
  useIncidentCrews,
  useSetIncidentETR,
  useTaskSubcontractor,
  useAssociateTroubleCall,
  useCancelSwitchingOrder,
  useCreatePlannedOutage,
  useCreateTroubleCall,
  useExecuteStep,
  useGenerateRestorationOrder,
  useGenerateSwitchingOrder,
  useIncidentAction,
  useIncidentDeviceCandidates,
  useIncidentEnergization,
  useIncidentTimeline,
  useIncidents,
  useOutageCauses,
  usePredictIncidents,
  useRaiseIncidentFromSpot,
  useReliabilityIndices,
  useSetIncidentAttribution,
  useTroubleSpots,
  useRestorationSuggestion,
  useSwitchingOrders,
  useTroubleCalls,
} from "@/hooks/use-oms"
import { useIssueClearanceFromOrder } from "@/hooks/use-safety"
import { useSubcontractors, useWorkOrders } from "@/hooks/use-wam"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useOmsHighlightStore } from "@/stores/oms-highlight-store"
import { useSelectionStore } from "@/stores/selection-store"
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

// --- telemetry source (field ground-truth) ---------------------------------

/** The caller_ref an AMI "last gasp" trouble call is tagged with (api/internal/
 * oms RecordLastGasp) -- lets the board tell a meter's dying power-down frame
 * from a phoned-in report. */
const AMI_LAST_GASP_REF = "AMI:last-gasp"
function isAmiCall(call: TroubleCall): boolean {
  return call.caller_ref === AMI_LAST_GASP_REF
}

// How the utility learned of the outage (migration 0168 detection_source) — a
// STORED fact now, not the old inferred heuristic. `machine` = we knew without a
// human calling (telemetry / analytics), styled as ground truth.
const DETECTION_SOURCE_META: Record<string, { label: string; machine: boolean }> = {
  customer: { label: "Customer", machine: false },
  ami: { label: "AMI", machine: true },
  scada: { label: "SCADA", machine: true },
  crew: { label: "Crew", machine: false },
  planned: { label: "Planned", machine: false },
  upstream: { label: "Upstream", machine: true },
  analytics: { label: "Analytics", machine: true },
}

/** Human label for a detection source code (or "Unknown" for a pre-0168 null). */
function detectionSourceLabel(source?: string | null): string {
  if (!source) return "Unknown"
  return DETECTION_SOURCE_META[source]?.label ?? source
}

/** A chip for how the utility learned of the outage. On the board it surfaces only
 * the NON-routine sources (machine telemetry, crew, upstream) — a plain customer
 * call is the implicit default and would just add noise — while the detail page
 * shows the source explicitly regardless. */
function SourceBadge({ source }: { source?: string | null }) {
  const meta = source ? DETECTION_SOURCE_META[source] : undefined
  if (!source || !meta || source === "customer" || source === "planned") return null
  const cls = meta.machine
    ? "bg-sky-100 text-sky-800 dark:bg-sky-950 dark:text-sky-300"
    : "bg-muted text-muted-foreground"
  return (
    <span
      className={cn("inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] font-medium", cls)}
      title={`Detected via ${meta.label}`}
    >
      <Radio className="size-3" />
      {meta.label}
    </span>
  )
}

/** A small telemetry-source chip (SCADA trip / AMI last-gasp) -- machine ground
 * truth, visually distinct from a phoned-in report. */
function TelemetryBadge({ label }: { label: string }) {
  return (
    <span
      className="inline-flex items-center gap-1 rounded bg-sky-100 px-1.5 py-0.5 text-[11px] font-medium text-sky-800 dark:bg-sky-950 dark:text-sky-300"
      title="From field telemetry, not a phoned-in report"
    >
      <Radio className="size-3" />
      {label}
    </span>
  )
}

// --- territory scoping -----------------------------------------------------

/** Order territories roots-first, each followed by its descendants (depth-first),
 * with a depth for indenting an option list. A node whose parent isn't in the set
 * is treated as a root. Mirrors the export-dialog / boq-panel pattern. */
function orderedTerritories(territories: TerritorySummary[]): { t: TerritorySummary; depth: number }[] {
  const ids = new Set(territories.map((t) => t.id))
  const byParent = new Map<string | null, TerritorySummary[]>()
  for (const t of territories) {
    const key = t.parent_id && ids.has(t.parent_id) ? t.parent_id : null
    const arr = byParent.get(key) ?? []
    arr.push(t)
    byParent.set(key, arr)
  }
  for (const arr of byParent.values()) {
    arr.sort((a, b) => a.level_rank - b.level_rank || a.name.localeCompare(b.name))
  }
  const out: { t: TerritorySummary; depth: number }[] = []
  const walk = (parent: string | null, depth: number) => {
    for (const t of byParent.get(parent) ?? []) {
      out.push({ t, depth })
      walk(t.id, depth + 1)
    }
  }
  walk(null, 0)
  return out
}

/** Resolve a territory_id up to its top-level (region) ancestor name — the direct
 * child of the root — for the by-region rollup. Null when unassigned/unknown. */
function regionNameFor(territoryId: string | null, byId: Map<string, TerritorySummary>): string | null {
  if (!territoryId) return null
  let node = byId.get(territoryId)
  if (!node) return null
  while (node.parent_id && byId.has(node.parent_id)) {
    const parent = byId.get(node.parent_id)
    if (!parent || !parent.parent_id || !byId.has(parent.parent_id)) break
    node = parent
  }
  return node.name
}

// --- filter chips ----------------------------------------------------------

const STATUS_CHIP_ORDER: IncidentStatus[] = [
  "predicted",
  "scheduled",
  "confirmed",
  "dispatched",
  "restored",
  "cancelled",
]

/** A pill toggle for the board filter bars, with an optional trailing count. */
function FilterChip({
  active,
  onClick,
  children,
  count,
}: {
  active: boolean
  onClick: () => void
  children: ReactNode
  count?: number
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "inline-flex items-center gap-1 rounded-full border px-2.5 py-0.5 text-xs font-medium capitalize transition-colors",
        active
          ? "border-primary bg-primary text-primary-foreground"
          : "border-border text-muted-foreground hover:bg-muted",
      )}
    >
      {children}
      {count != null && <span className="tabular-nums opacity-70">{count}</span>}
    </button>
  )
}

// --- log-call dialog -------------------------------------------------------

/** Intake form. SP-optional: enter a known service point, a raw address, a
 * lat/lng, or any mix -- at least one locator is required (server-enforced,
 * mirrored here so the button disables until it's satisfiable). */
// A trouble call is rung in from a SERVICE_POINT (it feeds the prediction
// rollup); a planned outage is booked around a SWITCHABLE device. Scoping each
// picker's fan-out to the right classes keeps the dropdown relevant.
const SUPPLY_CLASSES = ["SERVICE_POINT"]
const SWITCHABLE_DEVICE_CLASSES = ["MV_SWITCH", "MV_ISOLATOR", "MV_SECTIONALIZER", "MV_RECLOSER", "MV_BREAKER", "MV_FUSE"]

// The two load-bearing supply classes (mirrors api/internal/oms) — used to select
// a supply on the map and to label it in the affected-supplies table.
const ServicePointClass = "SERVICE_POINT"
const MeterClass = "METER"

function LogCallDialog({ onClose }: { onClose: () => void }) {
  const [sp, setSp] = useState<PickedFeature | null>(null)
  const [address, setAddress] = useState("")
  const [lat, setLat] = useState("")
  const [lng, setLng] = useState("")
  const [callerRef, setCallerRef] = useState("")
  const [callType, setCallType] = useState<CallType>("no_supply")
  const [notes, setNotes] = useState("")
  const create = useCreateTroubleCall()

  // When a known service point is picked, derive its coordinates and fill the
  // lat/lng -- so the call is pinned at the SP on the map, and the operator sees
  // where it is. Fetched lazily, only while an SP is selected.
  const spFeature = useFeature(sp?.featureClass, sp?.featureId)
  const spPoint = sp ? spFeature.data?.geometry : undefined
  useEffect(() => {
    if (!sp || spPoint?.type !== "Point" || !Array.isArray(spPoint.coordinates)) return
    const [lon, la] = spPoint.coordinates as number[]
    if (typeof lon === "number" && typeof la === "number") {
      setLng(String(lon))
      setLat(String(la))
    }
  }, [sp, spPoint])

  const latNum = lat.trim() === "" ? undefined : Number(lat)
  const lngNum = lng.trim() === "" ? undefined : Number(lng)
  const hasLatLng = latNum !== undefined && lngNum !== undefined && Number.isFinite(latNum) && Number.isFinite(lngNum)
  const hasLocator = sp != null || address.trim() !== "" || hasLatLng
  const canSave = hasLocator && !create.isPending

  function handleSave() {
    create.mutate(
      {
        service_point_id: sp?.featureId || undefined,
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
        <label className={labelClass}>Service point</label>
        <FeaturePicker
          placeholder="Search a service point by name…"
          classCodes={SUPPLY_CLASSES}
          selected={sp}
          onPick={setSp}
          onClear={() => {
            setSp(null)
            setLat("")
            setLng("")
          }}
        />
        <span className="text-muted-foreground text-xs">
          A resolved SP lets this call feed the prediction rollup
          {sp ? (spFeature.isPending ? " · locating…" : spPoint ? " · coordinates filled below" : "") : ""}.
        </span>
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
  const [sp, setSp] = useState<PickedFeature | null>(null)
  const associate = useAssociateTroubleCall()
  const hasLocation = call.longitude != null && call.latitude != null
  const canSave = sp != null && !associate.isPending

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
      onSubmit={() => run({ service_point_id: sp?.featureId })}
      submitLabel="Set service point"
      canSubmit={canSave}
      saving={associate.isPending}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Service point</label>
        <FeaturePicker
          placeholder="Search a service point by name…"
          classCodes={SUPPLY_CLASSES}
          selected={sp}
          onPick={setSp}
          onClear={() => setSp(null)}
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
  const [device, setDevice] = useState<PickedFeature | null>(null)
  const [start, setStart] = useState("")
  const [end, setEnd] = useState("")
  const [causeCode, setCauseCode] = useState("")
  const [notes, setNotes] = useState("")
  const create = useCreatePlannedOutage()

  const windowValid = !start || !end || new Date(start) < new Date(end)
  const canSave = device != null && windowValid && !create.isPending

  function toIso(local: string): string | undefined {
    if (!local) return undefined
    const d = new Date(local)
    return Number.isNaN(d.getTime()) ? undefined : d.toISOString()
  }

  function handleSave() {
    if (!device) return
    create.mutate(
      {
        device_class: device.featureClass,
        device_id: device.featureId,
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
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Device to operate</label>
        <FeaturePicker
          placeholder="Search a switch / recloser / breaker by name…"
          classCodes={SWITCHABLE_DEVICE_CLASSES}
          selected={device}
          onPick={setDevice}
          onClear={() => setDevice(null)}
        />
        <span className="text-muted-foreground text-xs">The switchable device whose operation causes the outage.</span>
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

// --- attribute device ------------------------------------------------------

/** Attribute (or change) the protective device an incident is blamed on. The
 * picker is scoped to the switchable/protective classes; on save the backend
 * re-traces the device's cone and re-stamps the affected set + territory (and
 * re-freezes a confirmed incident's interruptions). This turns an Unattributed
 * incident into a switchable one. */
/** One suggested device as a quick-pick chip; resolves its name for display. A
 * dot marks the nearest protective device (prediction's own first choice). */
function DeviceCandidateChip({ candidate, onPick }: { candidate: DeviceCandidate; onPick: (f: PickedFeature) => void }) {
  const feature = useFeature(candidate.feature_class, candidate.feature_id)
  const name = feature.data?.name ?? shortId(candidate.feature_id)
  return (
    <button
      type="button"
      onClick={() => onPick({ featureClass: candidate.feature_class, featureId: candidate.feature_id, name })}
      className="border-border hover:bg-muted inline-flex items-center gap-1.5 rounded-full border px-2 py-1 text-xs"
      title={candidate.nearest ? "Nearest protective device for this incident's calls" : "An upstream protective device"}
    >
      {candidate.nearest && <span className="bg-primary size-1.5 shrink-0 rounded-full" />}
      <span className="font-medium">{name}</span>
      <span className="text-muted-foreground">{candidate.feature_class.replace(/^MV_/, "")}</span>
    </button>
  )
}

function AttributeDeviceDialog({ incidentId, onClose }: { incidentId: string; onClose: () => void }) {
  const [device, setDevice] = useState<PickedFeature | null>(null)
  const attribute = useAttributeIncidentDevice()
  const candidates = useIncidentDeviceCandidates(incidentId)
  const candidateRows = candidates.data?.candidates ?? []
  const canSave = device != null && !attribute.isPending

  function handleSave() {
    if (!device) return
    attribute.mutate(
      { id: incidentId, device_class: device.featureClass, device_id: device.featureId },
      { onSuccess: onClose, onError: (err) => toastError(err, "Could not attribute the device.") },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Attribute a device"
      description="Name the protective device that operated. Its downstream cone becomes the incident's affected set."
      onSubmit={handleSave}
      submitLabel="Attribute"
      canSubmit={canSave}
      saving={attribute.isPending}
    >
      {candidateRows.length > 0 && (
        <div className="flex flex-col gap-1.5">
          <label className={labelClass}>Likely devices</label>
          <div className="flex flex-wrap gap-1.5">
            {candidateRows.map((c) => (
              <DeviceCandidateChip key={`${c.feature_class}:${c.feature_id}`} candidate={c} onPick={setDevice} />
            ))}
          </div>
          <span className="text-muted-foreground text-xs">
            The protective devices upstream of this incident's calls — pick one, or search below.
          </span>
        </div>
      )}

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Device</label>
        <FeaturePicker
          placeholder="Search a switch / recloser / breaker / fuse by name…"
          classCodes={SWITCHABLE_DEVICE_CLASSES}
          selected={device}
          onPick={setDevice}
          onClear={() => setDevice(null)}
        />
        <span className="text-muted-foreground text-xs">
          The incident is re-traced from this device: affected customers, territory and (if confirmed) its frozen
          interruptions are recomputed. Confidence clears (a human attribution isn't a prediction).
        </span>
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
  const canSafety = usePermission()("safety:write")
  const issueClearance = useIssueClearanceFromOrder()
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
          {/* Issue a permit-to-work from this order: danger-tag the devices it
              opens and ground the points it earths, so they can't be operated
              until the clearance is surrendered. Needs safety:write. */}
          {canSafety && !isTerminal && isolate.length > 0 && (
            <Button
              variant="outline"
              size="sm"
              className="mt-2"
              disabled={issueClearance.isPending}
              title="Create a permit-to-work: tags the opened devices and grounds the earthed points"
              onClick={() =>
                issueClearance.mutate(
                  { orderId: order.id },
                  { onError: (err) => toastError(err, "Could not issue a clearance.") },
                )
              }
            >
              {issueClearance.isPending ? <Loader2 className="size-4 animate-spin" /> : <ShieldAlert className="size-4" />}
              Issue clearance
            </Button>
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

// --- FLISR restoration -----------------------------------------------------

/** Capacity verdict pill for a restoration plan: whether the alternate feeder can
 * carry the picked-up load. `basis` says whether the verdict came from a real
 * load-flow sweep (authoritative) or the nameplate estimate -- shown in the tooltip
 * and, for a sweep, with a small check mark. */
function CapacityBadge({
  verdict,
  basis,
}: {
  verdict: RestorationPlan["capacity_verdict"]
  basis: RestorationPlan["verdict_basis"]
}) {
  const sweep = basis === "load_flow"
  if (verdict === "ok") {
    return (
      <span
        className="inline-flex items-center gap-1 rounded bg-emerald-100 px-1.5 py-0.5 text-[11px] font-medium text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300"
        title={
          sweep
            ? "Load-flow sweep: the donor feeder can pick up this section with no voltage violation or overload."
            : "Nameplate estimate: the restored load fits the alternate transformer's spare rating."
        }
      >
        <CheckCircle2 className="size-3" />
        Capacity OK{sweep && " · load-flow"}
      </span>
    )
  }
  if (verdict === "exceeds") {
    return (
      <span
        className="inline-flex items-center gap-1 rounded bg-red-100 px-1.5 py-0.5 text-[11px] font-medium text-red-800 dark:bg-red-950 dark:text-red-300"
        title={
          sweep
            ? "Load-flow sweep: this back-feed causes a voltage violation or thermal overload on the donor feeder."
            : "Nameplate estimate: the restored load exceeds the alternate transformer's spare rating."
        }
      >
        <ShieldAlert className="size-3" />
        {sweep ? "Overloads feeder" : "May overload"}
      </span>
    )
  }
  return (
    <span
      className="bg-muted text-muted-foreground inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-[11px] font-medium"
      title="Neither a load-flow sweep nor a transformer nameplate was available for the alternate feeder, so the fit can't be judged here."
    >
      <Gauge className="size-3" />
      Capacity unverified
    </span>
  )
}

/** One suggested back-feed plan: the tie to close, what it restores, its capacity
 * verdict, and the action to draft the close-tie switching order. */
function RestorationPlanCard({
  plan,
  canWrite,
  pending,
  onBuild,
}: {
  plan: RestorationPlan
  canWrite: boolean
  pending: boolean
  onBuild: () => void
}) {
  return (
    <div className="bg-muted/30 rounded-md p-2 text-xs">
      <div className="flex items-center justify-between gap-2">
        <span className="inline-flex items-center gap-1 font-medium">
          <Zap className="size-3.5 text-amber-600" />
          Close {plan.tie_label}
        </span>
        <CapacityBadge verdict={plan.capacity_verdict} basis={plan.verdict_basis} />
      </div>
      <div className="text-muted-foreground mt-1 space-y-0.5">
        <div>Back-feed from {plan.alternate_source_label || "an alternate feeder"}</div>
        <div>
          Restores {plan.restored_customers.toLocaleString()} customer(s) · {plan.restored_service_points}{" "}
          supply(ies) · {plan.restored_load_kva.toFixed(0)} kVA
        </div>
        {plan.alternate_capacity_kva != null && (
          <div>
            Alt feeder load {plan.alternate_feeder_load_kva.toFixed(0)} / {plan.alternate_capacity_kva.toFixed(0)} kVA
            {plan.spare_headroom_kva != null && ` · ${plan.spare_headroom_kva.toFixed(0)} kVA spare`}
          </div>
        )}
        {plan.load_flow_checked && (
          <div>
            Load-flow:{" "}
            {plan.load_flow_feasible
              ? "feasible"
              : `${plan.violations} voltage violation(s), ${plan.overloaded} overload(s)`}
            {plan.worst_voltage_pu != null && ` · worst ${plan.worst_voltage_pu.toFixed(3)} pu`}
          </div>
        )}
        {plan.load_flow_note && <div className="italic">{plan.load_flow_note}</div>}
      </div>
      {canWrite && (
        <Button variant="outline" size="sm" className="mt-2" disabled={pending} onClick={onBuild}>
          {pending ? <Loader2 className="size-4 animate-spin" /> : <ListChecks className="size-4" />}
          Build switching order
        </Button>
      )}
    </div>
  )
}

/** FLISR restoration suggestions for an active incident: the normally-open ties
 * that could back-feed its dead section from an alternate source. Each plan shows
 * what it restores plus a capacity verdict; "Build switching order" drafts the
 * close-tie program (it then appears in the switching list above). Only mounted
 * for an active incident with a device. */
function RestorationPanel({ incidentId, canWrite }: { incidentId: string; canWrite: boolean }) {
  const suggestion = useRestorationSuggestion(incidentId)
  const generate = useGenerateRestorationOrder()
  const d = suggestion.data

  const header = (
    <div className="mb-1 flex items-center gap-1.5">
      <PlugZap className="size-3.5 text-amber-600" />
      <span className="text-sm font-medium">Restoration (FLISR)</span>
    </div>
  )

  if (suggestion.isPending) {
    return (
      <div className="mt-3">
        {header}
        <LoadingRow label="Finding restoration options…" />
      </div>
    )
  }
  if (suggestion.isError || !d) {
    return (
      <div className="mt-3">
        {header}
        <p className="text-destructive text-sm">Could not load restoration options.</p>
      </div>
    )
  }
  const plans = d.plans ?? []

  return (
    <div className="mt-3">
      {header}
      {plans.length === 0 ? (
        <p className="text-muted-foreground text-xs">
          {d.note || "No back-feed restoration path found."}
        </p>
      ) : (
        <div className="flex flex-col gap-2">
          {plans.map((p) => (
            <RestorationPlanCard
              key={`${p.tie.feature_class}:${p.tie.feature_id}`}
              plan={p}
              canWrite={canWrite}
              pending={generate.isPending}
              onBuild={() =>
                generate.mutate(
                  { incidentId, tieClass: p.tie.feature_class, tieId: p.tie.feature_id },
                  { onError: (err) => toastError(err, "Could not build a restoration order.") },
                )
              }
            />
          ))}
        </div>
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
}: {
  inc: Incident
  canWrite: boolean
  now: number
}) {
  const action = useIncidentAction()
  const raiseFault = useRaiseFaultFromIncident()
  const canRaiseFault = usePermission()("faults:write")
  const navigate = useNavigate()
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

  const customers = inc.affected_customers ?? 0
  const hasArea = (inc.affected_service_points ?? 0) > 0
  const hasDevice = inc.predicted_device_class != null && inc.predicted_device_id != null
  const isPlanned = inc.outage_type === "planned"
  const isTerminal = inc.status === "restored" || inc.status === "cancelled"
  const isActive = inc.status === "confirmed" || inc.status === "dispatched"
  // A predicted incident awaits confirm; a scheduled (planned) one awaits its
  // execution window, then "Begin outage" starts the interruption clock.
  const canConfirm = inc.status === "predicted" || inc.status === "scheduled"
  // Once confirmed, a crew can be dispatched (confirmed -> dispatched); a
  // dispatched or restored incident has already moved past this step.
  const canDispatch = inc.status === "confirmed"

  return (
    <div
      className={cn("cursor-pointer border-l-2", severityAccent(customers), isTerminal && "opacity-70")}
      onClick={() => navigate(`/outages/${inc.id}`)}
      title="Open incident detail"
    >
      <div className="hover:bg-muted/40 flex flex-wrap items-center gap-x-3 gap-y-1.5 py-2 pl-3 pr-2">
        <ChevronRight className="text-muted-foreground size-4 shrink-0" />
        <StatusBadge status={inc.status} />
        <SourceBadge source={inc.detection_source} />
        <span className="text-sm font-medium">{deviceLabel(inc)}</span>
        {inc.territory_name && (
          <span
            className="text-muted-foreground inline-flex items-center gap-1 text-xs"
            title="Dominant territory of the affected set"
          >
            <MapPin className="size-3" />
            {inc.territory_name}
          </span>
        )}

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
          {inc.last_actor_name && (
            <span className="inline-flex items-center gap-1" title="Last advanced by">
              <UserCog className="size-3.5" />
              {inc.last_actor_name}
            </span>
          )}
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
                <Button variant="outline" size="sm" disabled={pending} onClick={() => run("confirm")}>
                  <CheckCircle2 className="size-4" />
                  {isPlanned ? "Begin" : "Confirm"}
                </Button>
              )}
              {canDispatch && (
                <Button
                  variant="outline"
                  size="sm"
                  disabled={pending}
                  onClick={() => run("dispatch")}
                  title="Mark a crew as dispatched to this incident"
                >
                  <Truck className="size-4" />
                  Dispatch
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

// Human labels for the oms.events types (api/internal/oms/events.go). An unknown
// type falls back to the raw code so a new event never renders blank.
const EVENT_LABEL: Record<string, string> = {
  "call.received": "Received",
  "call.located": "Located",
  "call.attributed": "Attributed",
  "incident.predicted": "Predicted",
  "incident.planned": "Planned outage booked",
  "incident.scada_raised": "Raised from SCADA",
  "incident.confirmed": "Confirmed",
  "incident.from_spot": "Raised from trouble spot",
  "incident.attributed": "Device attributed",
  "incident.cause": "Cause / source updated",
  "incident.dispatched": "Crew dispatched",
  "incident.restored": "Restored",
  "incident.cancelled": "Cancelled",
}

/** A vertical lifecycle timeline — the call/incident events in order, each with
 * its time, actor and note. The dashed rule separates the call's own events from
 * the incident it joined (the first incident.* event onward). */
function Timeline({ events }: { events: TimelineEvent[] }) {
  if (events.length === 0) return <p className="text-muted-foreground text-xs">No timeline yet.</p>
  const firstIncidentIdx = events.findIndex((e) => e.entity_kind === "incident")
  return (
    <ol className="border-border relative ml-1 flex flex-col gap-2 border-l pl-4">
      {events.map((e, i) => (
        <li key={e.id} className="relative">
          <span className="bg-primary ring-background absolute top-1 -left-[1.3rem] size-2 rounded-full ring-2" />
          {i === firstIncidentIdx && firstIncidentIdx > 0 && (
            <div className="text-muted-foreground mb-1 text-[10px] tracking-wide uppercase">incident</div>
          )}
          <div className="flex flex-wrap items-baseline gap-x-2 text-xs">
            <span className="font-medium">{EVENT_LABEL[e.event_type] ?? e.event_type}</span>
            <span className="text-muted-foreground tabular-nums">{formatWhen(e.occurred_at)}</span>
            {e.actor_name && <span className="text-muted-foreground">by {e.actor_name}</span>}
          </div>
          {e.note && <div className="text-muted-foreground text-xs">{e.note}</div>}
        </li>
      ))}
    </ol>
  )
}

/** Lazily loads and renders a trouble call's timeline (its events + the incident
 * it fed). Mounted only when a call row is expanded, so it costs nothing closed. */
function CallTimelinePanel({ callId }: { callId: string }) {
  const timeline = useCallTimeline(callId)
  if (timeline.isPending) return <p className="text-muted-foreground text-xs">Loading timeline…</p>
  if (timeline.isError) return <p className="text-destructive text-xs">Could not load timeline.</p>
  return <Timeline events={timeline.data?.events ?? []} />
}

function TroubleCallRow({
  call,
  canWrite,
  onAssociate,
  onJumpToIncident,
  highlighted,
}: {
  call: TroubleCall
  canWrite: boolean
  onAssociate: (c: TroubleCall) => void
  onJumpToIncident: (incidentId: string) => void
  highlighted?: boolean
}) {
  const [expanded, setExpanded] = useState(false)
  const navigate = useNavigate()
  const needsSP = call.service_point_id == null && call.status === "open"

  // Locate the call on the main map: a resolved SP selects that feature (and the
  // panel opens); a bare pin just centres there. Only shown when there's
  // something to point at.
  const hasSP = call.service_point_id != null
  function locateOnMap() {
    if (call.service_point_id) {
      useSelectionStore.getState().select(ServicePointClass, call.service_point_id, { focus: true })
    }
    navigate("/")
  }

  return (
    <div
      id={`tc-${call.id}`}
      className={cn(
        "border-border/60 border-t first:border-t-0",
        highlighted && "ring-primary bg-primary/5 rounded-md ring-1 ring-inset",
      )}
    >
      <div
        className="hover:bg-muted/40 flex cursor-pointer flex-wrap items-center gap-x-3 gap-y-1 rounded-md px-2 py-1.5"
        onClick={() => setExpanded((v) => !v)}
      >
        {expanded ? (
          <ChevronDown className="text-muted-foreground size-4 shrink-0" />
        ) : (
          <ChevronRight className="text-muted-foreground size-4 shrink-0" />
        )}
        <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs font-medium capitalize">
          {call.status}
        </span>
        {isAmiCall(call) && <TelemetryBadge label="AMI" />}
        <span className="text-sm">{callLocator(call)}</span>
        <span className="text-muted-foreground text-xs capitalize">{call.call_type.replace("_", " ")}</span>
        <span className="text-muted-foreground ml-auto text-xs">{formatWhen(call.reported_at)}</span>
        {canWrite && needsSP && (
          <Button
            variant="outline"
            size="sm"
            className="shrink-0"
            onClick={(e) => {
              e.stopPropagation()
              onAssociate(call)
            }}
          >
            <MapPin className="size-4" />
            Associate
          </Button>
        )}
      </div>

      {expanded && (
        <div className="bg-muted/30 mx-2 mb-1.5 rounded-md px-3 py-2 text-xs">
          <dl className="grid grid-cols-[7rem_1fr] gap-x-3 gap-y-1">
            <dt className="text-muted-foreground">Service point</dt>
            <dd className="font-mono">{call.service_point_id ?? "—"}</dd>
            <dt className="text-muted-foreground">Address</dt>
            <dd>{call.address ?? "—"}</dd>
            <dt className="text-muted-foreground">Location</dt>
            <dd>
              {call.latitude != null && call.longitude != null
                ? `${call.latitude.toFixed(5)}, ${call.longitude.toFixed(5)}`
                : "—"}
            </dd>
            <dt className="text-muted-foreground">Call type</dt>
            <dd className="capitalize">{call.call_type.replace("_", " ")}</dd>
            <dt className="text-muted-foreground">Caller ref</dt>
            <dd>{call.caller_ref ?? "—"}</dd>
            <dt className="text-muted-foreground">Reported</dt>
            <dd>{formatWhen(call.reported_at)}</dd>
            <dt className="text-muted-foreground">Incident</dt>
            <dd>
              {call.incident_id ? (
                <button
                  type="button"
                  className="text-primary hover:underline"
                  onClick={() => onJumpToIncident(call.incident_id as string)}
                >
                  View incident
                </button>
              ) : (
                "—"
              )}
            </dd>
            {call.notes && (
              <>
                <dt className="text-muted-foreground">Notes</dt>
                <dd>{call.notes}</dd>
              </>
            )}
          </dl>
          {(hasSP || (call.latitude != null && call.longitude != null)) && (
            <Button variant="outline" size="sm" className="mt-2" onClick={locateOnMap}>
              <MapPinned className="size-4" />
              Locate on map
            </Button>
          )}

          <div className="border-border/60 mt-3 border-t pt-2">
            <div className="text-muted-foreground mb-2 text-[11px] font-medium tracking-wide uppercase">Timeline</div>
            <CallTimelinePanel callId={call.id} />
          </div>
        </div>
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

// --- area impact (network-wide, by territory) ------------------------------

/** One territory row in the area-impact panel: a share bar (by customers off)
 * with its counts; clicking it scopes the whole board to that territory. */
function AreaImpactRowBar({
  name,
  customers,
  servicePoints,
  incidents,
  max,
  onClick,
}: {
  name: string
  customers: number
  servicePoints: number
  incidents: number
  max: number
  onClick?: () => void
}) {
  const pct = max > 0 ? (customers / max) * 100 : 0
  const inner = (
    <>
      <span className="w-28 shrink-0 truncate text-left">{name}</span>
      <div className="bg-muted relative h-3 flex-1 overflow-hidden rounded-sm">
        <div className="h-full rounded-sm bg-red-500/70" style={{ width: `${pct}%` }} />
      </div>
      <span className="w-32 shrink-0 text-right tabular-nums">
        {customers.toLocaleString()} off · {servicePoints} SP · {incidents} inc
      </span>
    </>
  )
  return onClick ? (
    <button type="button" onClick={onClick} className="hover:bg-muted/40 flex items-center gap-2 rounded px-1 text-xs" title="Scope the board to this territory">
      {inner}
    </button>
  ) : (
    <div className="flex items-center gap-2 px-1 text-xs">{inner}</div>
  )
}

/** Network-wide AREA IMPACT: customers off supply rolled up by territory (region
 * → district → …) across all open incidents — "where is it worst now". Each row
 * scopes the board to that territory. */
function AreaImpactCard({ onScope }: { onScope: (territoryId: string) => void }) {
  const impact = useAreaImpact()
  const d = impact.data

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <div>
            <CardTitle className="flex items-center gap-2 text-sm">
              <MapPinned className="size-4" />
              Area impact
            </CardTitle>
            <CardDescription>Customers off supply by territory, across all open incidents</CardDescription>
          </div>
          {d && (
            <span
              className={cn(
                "rounded px-2 py-0.5 text-sm font-semibold tabular-nums",
                d.total_customers > 0
                  ? "bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-300"
                  : "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300",
              )}
            >
              {d.total_customers.toLocaleString()} off · {d.open_incidents} incident{d.open_incidents === 1 ? "" : "s"}
            </span>
          )}
        </div>
      </CardHeader>
      <CardContent className="flex flex-col gap-4 pt-0">
        {impact.isPending ? (
          <LoadingRow label="Computing area impact…" />
        ) : impact.isError || !d ? (
          <p className="text-destructive text-sm">Could not load area impact.</p>
        ) : d.total_customers === 0 ? (
          <p className="text-muted-foreground text-sm">No customers currently off supply.</p>
        ) : d.levels.length === 0 ? (
          <p className="text-muted-foreground text-sm">
            {d.total_customers.toLocaleString()} off supply, but no territory is assigned to break it down.
          </p>
        ) : (
          <>
            {d.levels.map((lvl: AreaImpactLevel) => {
              const max = Math.max(1, ...lvl.rows.map((r) => r.customers))
              return (
                <div key={lvl.level_code} className="flex flex-col gap-1">
                  <div className="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
                    By {lvl.level_name.toLowerCase()}
                  </div>
                  {lvl.rows.map((r) => (
                    <AreaImpactRowBar
                      key={r.territory_id}
                      name={r.territory_name}
                      customers={r.customers}
                      servicePoints={r.service_points}
                      incidents={r.incidents}
                      max={max}
                      onClick={() => onScope(r.territory_id)}
                    />
                  ))}
                </div>
              )
            })}
            {d.unassigned.customers > 0 && (
              <div className="flex flex-col gap-1">
                <div className="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">Unassigned</div>
                <AreaImpactRowBar
                  name="No territory"
                  customers={d.unassigned.customers}
                  servicePoints={d.unassigned.service_points}
                  incidents={0}
                  max={Math.max(1, d.unassigned.customers)}
                />
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}

// --- trouble spots (geographic lens) ---------------------------------------

/** The geographic lens: open calls clustered by location into "trouble spots" a
 * crew can be dispatched to BEFORE/without prediction finding a device.
 * "Dispatch" raises one unattributed incident per spot (so separate
 * neighbourhoods become separate incidents, not one lump); "Locate" centres the
 * spot on the main map via its first supply. Hidden when there are no clusters. */
function TroubleSpotsCard({ canWrite }: { canWrite: boolean }) {
  const spots = useTroubleSpots()
  const raise = useRaiseIncidentFromSpot()
  const navigate = useNavigate()
  const now = useTick(30_000)
  const rows = spots.data?.trouble_spots ?? []

  if (spots.isPending || spots.isError || rows.length === 0) return null

  function dispatch(spot: TroubleSpot) {
    raise.mutate(
      { call_ids: spot.call_ids },
      {
        onSuccess: (inc) => {
          useToastStore.getState().pushSuccess("Incident raised from the spot — dispatch a crew.")
          navigate(`/outages/${(inc as Incident).id}`)
        },
        onError: (err) => toastError(err, "Could not raise an incident from the spot."),
      },
    )
  }
  function locate(spot: TroubleSpot) {
    const sp = spot.service_point_ids?.[0]
    if (sp) {
      useSelectionStore.getState().select(ServicePointClass, sp, { focus: true })
    }
    navigate("/")
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-sm">
          <MapPinned className="size-4" />
          Trouble spots
        </CardTitle>
        <CardDescription>
          {rows.length} cluster{rows.length === 1 ? "" : "s"} of open calls — dispatch a crew before prediction
        </CardDescription>
      </CardHeader>
      <CardContent className="pt-0">
        <div className="flex flex-col">
          {rows.map((s, i) => (
            <div
              key={s.call_ids?.[0] ?? i}
              className="border-border/60 hover:bg-muted/40 flex flex-wrap items-center gap-x-3 gap-y-1 border-t px-2 py-1.5 first:border-t-0"
            >
              <span className="inline-flex items-center gap-1 text-sm font-medium">
                <PhoneCall className="text-muted-foreground size-3.5" />
                {s.call_count} call{s.call_count === 1 ? "" : "s"}
              </span>
              <span className="text-muted-foreground text-xs capitalize">{s.dominant_call_type.replace("_", " ")}</span>
              {s.territory_name && (
                <span className="text-muted-foreground inline-flex items-center gap-1 text-xs">
                  <MapPin className="size-3" />
                  {s.territory_name}
                </span>
              )}
              {s.has_ami && <TelemetryBadge label="AMI" />}
              <span
                className="text-muted-foreground ml-auto text-xs tabular-nums"
                title={`Earliest report ${formatWhen(s.earliest_reported_at)}`}
              >
                {elapsed(s.earliest_reported_at, null, now)} old
              </span>
              {s.service_point_ids?.[0] && (
                <Button variant="ghost" size="sm" className="shrink-0" onClick={() => locate(s)} title="Locate on the map">
                  <MapPinned className="size-4" />
                </Button>
              )}
              {canWrite && (
                <Button
                  variant="outline"
                  size="sm"
                  className="shrink-0"
                  disabled={raise.isPending}
                  onClick={() => dispatch(s)}
                  title="Raise an incident and dispatch a crew to this spot"
                >
                  <Truck className="size-4" />
                  Dispatch
                </Button>
              )}
            </div>
          ))}
        </div>
        <p className="text-muted-foreground mt-2 text-[11px]">
          Open calls grouped by location (~250 m). Dispatching raises one unattributed incident per spot — attribute a
          device later, or run prediction.
        </p>
      </CardContent>
    </Card>
  )
}

// --- situational awareness (source & cause) --------------------------------

/** Minutes -> a compact duration (47m, 3h 12m, 2d 4h). */
function fmtMinutes(mins: number): string {
  const m = Math.round(mins)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${m % 60}m`
  return `${Math.floor(h / 24)}d ${h % 24}h`
}

/** Title-case a cause-family code ("third_party" -> "Third Party"). */
function causeFamilyLabel(family: string): string {
  if (family === "unknown") return "Unknown"
  return family.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase())
}

/** One horizontal share bar in the source/cause breakdowns. */
function MetricBar({
  label,
  value,
  max,
  right,
  machine = false,
}: {
  label: string
  value: number
  max: number
  right: string
  machine?: boolean
}) {
  const pct = max > 0 ? (value / max) * 100 : 0
  return (
    <div className="flex items-center gap-2 text-xs">
      <span className="w-24 shrink-0 truncate">{label}</span>
      <div className="bg-muted relative h-3 flex-1 overflow-hidden rounded-sm">
        <div className={cn("h-full rounded-sm", machine ? "bg-sky-500" : "bg-primary/60")} style={{ width: `${pct}%` }} />
      </div>
      <span className="text-muted-foreground w-28 shrink-0 text-right tabular-nums">{right}</span>
    </div>
  )
}

/** The "source & cause" payoff: HOW we learned of our outages (detection-source
 * mix + the machine-detected awareness share) and WHY they happened (cause-family
 * mix), plus the mean detection->restore time, over the trailing 12 months. */
function DetectionMetricsCard() {
  const metrics = useDetectionMetrics()
  const d = metrics.data

  if (metrics.isPending) return <LoadingRow label="Computing source & cause metrics…" />
  if (metrics.isError || !d) return <p className="text-destructive text-sm">Could not load source & cause metrics.</p>
  if (d.total_incidents === 0) {
    return <p className="text-muted-foreground text-sm">No unplanned incidents in the last 12 months.</p>
  }

  const sharePct = Math.round(d.machine_detected_share * 100)
  const maxSrc = Math.max(1, ...d.by_source.map((s) => s.incidents))
  const maxCause = Math.max(1, ...d.by_cause.map((c) => c.incidents))
  const avg = d.avg_detection_to_restore_minutes

  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
        <StatTile
          icon={<Radio className="size-5" />}
          label="Machine-detected"
          value={`${sharePct}%`}
          sub={`${d.machine_detected_incidents} of ${d.total_incidents} · knew without a call`}
          tone={sharePct >= 50 ? "ok" : "warn"}
        />
        <StatTile
          icon={<Activity className="size-5" />}
          label="Unplanned incidents"
          value={d.total_incidents.toLocaleString()}
          sub="last 12 months"
        />
        <StatTile
          icon={<CalendarClock className="size-5" />}
          label="Avg detect→restore"
          value={avg != null ? fmtMinutes(avg) : "—"}
          sub={`${d.restored_incidents} restored`}
        />
      </div>

      <div className="flex flex-col gap-1.5">
        <p className="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
          How we learned (detection source)
        </p>
        {d.by_source.map((s) => (
          <MetricBar
            key={s.source}
            label={detectionSourceLabel(s.source)}
            value={s.incidents}
            max={maxSrc}
            right={`${s.incidents} · ${s.customers_affected.toLocaleString()} cust`}
            machine={DETECTION_SOURCE_META[s.source]?.machine ?? false}
          />
        ))}
      </div>

      <div className="flex flex-col gap-1.5">
        <p className="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
          Why it happened (cause family)
        </p>
        {d.by_cause.map((c) => (
          <MetricBar
            key={c.family}
            label={causeFamilyLabel(c.family)}
            value={c.incidents}
            max={maxCause}
            right={`${c.incidents} · ${c.customers_affected.toLocaleString()} cust`}
          />
        ))}
      </div>

      <p className="text-muted-foreground text-[11px]">
        Machine-detected = SCADA / AMI / analytics — outages known without a customer call (the situational-awareness
        measure). Unplanned incidents only.
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

/** The selected incident's lifecycle timeline (predicted/confirmed/dispatched/
 * restored + the calls that fed it), in a collapsible so it's there when wanted
 * without crowding the drawer. Hidden until an incident is selected. */
function IncidentTimelineCard({ incidentId }: { incidentId: string | null }) {
  const timeline = useIncidentTimeline(incidentId)
  if (!incidentId) return null
  const events = timeline.data?.events ?? []
  return (
    <details className="group bg-card rounded-lg border">
      <summary className="flex cursor-pointer list-none items-center gap-2 px-4 py-3 text-sm font-medium select-none">
        <ChevronRight className="size-4 transition-transform group-open:rotate-90" />
        Timeline
        {events.length > 0 && <span className="text-muted-foreground font-normal">{events.length} events</span>}
      </summary>
      <div className="max-h-[28rem] overflow-y-auto border-t px-4 py-3">
        {timeline.isPending ? (
          <p className="text-muted-foreground text-xs">Loading timeline…</p>
        ) : timeline.isError ? (
          <p className="text-destructive text-xs">Could not load timeline.</p>
        ) : (
          <Timeline events={events} />
        )}
      </div>
    </details>
  )
}

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
          <>
            <SwitchingOrdersPanel incidentId={incident.id} hasDevice={hasDevice} canWrite={canWrite} />
            {/* FLISR back-feed options -- only while the outage is live (a device
                that is actually open, with a dead section to restore). */}
            {isActive && <RestorationPanel incidentId={incident.id} canWrite={canWrite} />}
          </>
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
  const navigate = useNavigate()

  const [territoryId, setTerritoryId] = useState("")
  const incidents = useIncidents({ territoryId: territoryId || undefined })
  const predict = usePredictIncidents()
  // The operator's accessible territories drive the scope picker; the full tree
  // resolves each incident's dominant territory to its region for the rollup.
  const myTerritories = useMyTerritories()
  const allTerritories = useTerritories()
  const territoryOptions = useMemo(
    () => orderedTerritories(myTerritories.data?.territories ?? []),
    [myTerritories.data],
  )
  const territoryById = useMemo(() => {
    const m = new Map<string, TerritorySummary>()
    for (const t of allTerritories.data?.territories ?? []) m.set(t.id, t)
    return m
  }, [allTerritories.data])

  const [logging, setLogging] = useState(false)
  const [scheduling, setScheduling] = useState(false)
  const [associating, setAssociating] = useState<TroubleCall | null>(null)
  const [incidentFilter, setIncidentFilter] = useState<"open" | "all" | IncidentStatus>("open")
  const [incidentQuery, setIncidentQuery] = useState("")
  const [incidentPageSize, setIncidentPageSize] = useState(50)
  const [incidentOffset, setIncidentOffset] = useState(0)
  const [callFilter, setCallFilter] = useState<"all" | "open" | "associated">("all")
  const [callPageSize, setCallPageSize] = useState(50)
  const [callOffset, setCallOffset] = useState(0)
  const [callFind, setCallFind] = useState("")

  // The call queue, paginated from the source (a storm's volume a page at a time);
  // status filters server-side. A separate 1-row query gives the open-queue total
  // for the KPI without loading the whole queue.
  const calls = useTroubleCalls({
    status: callFilter === "all" ? "" : callFilter,
    limit: callPageSize,
    offset: callOffset,
  })
  const openCallsTotal = useTroubleCalls({ status: "open", limit: 1 })

  // Reset to the first page whenever the filter or page size changes.
  useEffect(() => {
    setCallOffset(0)
  }, [callFilter, callPageSize])

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

  // Per-status tallies drive the filter chips (and let us hide a chip for a
  // status nothing is in).
  const statusCounts = useMemo(() => {
    const c = {} as Record<IncidentStatus, number>
    for (const i of incidentRows) c[i.status] = (c[i.status] ?? 0) + 1
    return c
  }, [incidentRows])

  // The board filters client-side over the already-fetched set, so the KPI strip
  // (fed the full list) can never disagree with it while the view narrows.
  const visibleIncidents = useMemo(() => {
    const q = incidentQuery.trim().toLowerCase()
    return incidentRows.filter((inc) => {
      const statusOk =
        incidentFilter === "all"
          ? true
          : incidentFilter === "open"
            ? isOpenIncident(inc)
            : inc.status === incidentFilter
      if (!statusOk) return false
      if (!q) return true
      const hay =
        `${inc.predicted_device_class ?? ""} ${inc.predicted_device_id ?? ""} ${inc.circuit_id ?? ""} ${inc.cause_code ?? ""}`.toLowerCase()
      return hay.includes(q)
    })
  }, [incidentRows, incidentFilter, incidentQuery])

  // The current page of the filtered set (client-side — incidents are modest in
  // number; the cap keeps the list from pushing the board off the screen). KPIs
  // and counts still read the full set.
  useEffect(() => {
    setIncidentOffset(0)
  }, [incidentFilter, incidentQuery, incidentPageSize])
  const pagedIncidents = useMemo(
    () => visibleIncidents.slice(incidentOffset, incidentOffset + incidentPageSize),
    [visibleIncidents, incidentOffset, incidentPageSize],
  )

  // Visible incidents rolled up by their dominant territory's region, biggest
  // first — "where the outages are" at a glance (0169). Unassigned grouped last.
  const regionRollup = useMemo(() => {
    const by = new Map<string, { region: string; incidents: number; customers: number }>()
    for (const inc of visibleIncidents) {
      const region = regionNameFor(inc.territory_id, territoryById) ?? "Unassigned"
      const row = by.get(region) ?? { region, incidents: 0, customers: 0 }
      row.incidents += 1
      row.customers += inc.affected_customers ?? 0
      by.set(region, row)
    }
    return [...by.values()].sort(
      (a, b) =>
        (a.region === "Unassigned" ? 1 : 0) - (b.region === "Unassigned" ? 1 : 0) ||
        b.customers - a.customers ||
        b.incidents - a.incidents,
    )
  }, [visibleIncidents, territoryById])

  // The current page (already status-filtered server-side); total is the count
  // under the filter, for the page controls.
  const visibleCalls = calls.data?.trouble_calls ?? []
  const callTotal = calls.data?.total ?? 0
  const openCallCount = openCallsTotal.data?.total ?? 0

  // FIND (not a filter): the ids of loaded rows matching the query — they get a
  // ring and the first is scrolled into view; the rows themselves are NOT removed.
  const findLower = callFind.trim().toLowerCase()
  const findMatchIds = useMemo(() => {
    const s = new Set<string>()
    if (!findLower) return s
    for (const c of visibleCalls) {
      const hay = `${c.service_point_id ?? ""} ${c.address ?? ""} ${c.caller_ref ?? ""} ${c.call_type}`.toLowerCase()
      if (hay.includes(findLower)) s.add(c.id)
    }
    return s
  }, [visibleCalls, findLower])

  // Scroll to the first match when the find query changes.
  useEffect(() => {
    if (findMatchIds.size === 0) return
    const first = visibleCalls.find((c) => findMatchIds.has(c.id))
    if (first) document.getElementById(`tc-${first.id}`)?.scrollIntoView({ block: "nearest" })
  }, [findMatchIds, visibleCalls])

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
              Outage Management
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

        <div className="flex flex-col gap-4">
            <AreaImpactCard onScope={setTerritoryId} />

            <Card>
              <CardHeader className="gap-3 pb-3">
                <div className="flex flex-wrap items-start justify-between gap-2">
                  <div>
                    <CardTitle className="text-sm">Incidents</CardTitle>
                    <CardDescription>
                      {incidentRows.length === 0
                        ? "No incidents."
                        : `Showing ${visibleIncidents.length} of ${incidentRows.length} · ${activeCount} active`}
                    </CardDescription>
                  </div>
                  <div className="flex items-center gap-2">
                    {territoryOptions.length > 0 && (
                      <select
                        className={cn(inputClass, "w-40")}
                        value={territoryId}
                        onChange={(e) => setTerritoryId(e.target.value)}
                        aria-label="Scope incidents to a territory"
                        title="Scope the board to a region / district"
                      >
                        <option value="">All territories</option>
                        {territoryOptions.map(({ t, depth }) => (
                          <option key={t.id} value={t.id}>
                            {" ".repeat(depth * 2)}
                            {t.name}
                          </option>
                        ))}
                      </select>
                    )}
                    <div className="relative">
                      <Search className="text-muted-foreground pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2" />
                      <input
                        className={cn(inputClass, "w-44 pl-7")}
                        placeholder="Search device / circuit…"
                        value={incidentQuery}
                        onChange={(e) => setIncidentQuery(e.target.value)}
                        aria-label="Search incidents"
                      />
                    </div>
                  </div>
                </div>
                {incidentRows.length > 0 && (
                  <div className="flex flex-wrap items-center gap-1.5">
                    <FilterChip
                      active={incidentFilter === "open"}
                      onClick={() => setIncidentFilter("open")}
                      count={activeCount}
                    >
                      Open
                    </FilterChip>
                    <FilterChip
                      active={incidentFilter === "all"}
                      onClick={() => setIncidentFilter("all")}
                      count={incidentRows.length}
                    >
                      All
                    </FilterChip>
                    {STATUS_CHIP_ORDER.filter((s) => (statusCounts[s] ?? 0) > 0).map((s) => (
                      <FilterChip
                        key={s}
                        active={incidentFilter === s}
                        onClick={() => setIncidentFilter(s)}
                        count={statusCounts[s]}
                      >
                        {s}
                      </FilterChip>
                    ))}
                  </div>
                )}
              </CardHeader>
              <CardContent className="pt-0">
                {/* Where the outages are, rolled up by region — a quick read when
                    viewing the whole board (redundant once scoped to one). */}
                {!territoryId && regionRollup.length > 1 && (
                  <div className="mb-3 flex flex-wrap items-center gap-1.5">
                    <span className="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
                      By region
                    </span>
                    {regionRollup.map((r) => (
                      <span
                        key={r.region}
                        className="border-border inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs"
                      >
                        <span className="font-medium">{r.region}</span>
                        <span className="text-muted-foreground tabular-nums">
                          {r.incidents} · {r.customers.toLocaleString()} cust
                        </span>
                      </span>
                    ))}
                  </div>
                )}
                {incidents.isPending ? (
                  <LoadingRow label="Loading incidents…" />
                ) : incidents.isError ? (
                  <p className="text-destructive text-sm">Could not load incidents.</p>
                ) : visibleIncidents.length > 0 ? (
                  <>
                    {/* Capped height + internal scroll so a storm's incidents don't
                        push the trouble calls / panels below off the screen. */}
                    <div className="flex max-h-[32rem] flex-col overflow-y-auto">
                      {pagedIncidents.map((inc) => (
                        <IncidentRow key={inc.id} inc={inc} canWrite={canWrite} now={now} />
                      ))}
                    </div>
                    {visibleIncidents.length > incidentPageSize && (
                      <div className="mt-2 flex flex-wrap items-center justify-between gap-2 text-xs">
                        <span className="text-muted-foreground tabular-nums">
                          {incidentOffset + 1}–{incidentOffset + pagedIncidents.length} of{" "}
                          {visibleIncidents.length.toLocaleString()}
                        </span>
                        <div className="flex items-center gap-1.5">
                          <label className="text-muted-foreground" htmlFor="incident-page-size">
                            Per page
                          </label>
                          <select
                            id="incident-page-size"
                            className={cn(inputClass, "h-7 w-16 py-0")}
                            value={incidentPageSize}
                            onChange={(e) => setIncidentPageSize(Number(e.target.value))}
                          >
                            <option value={20}>20</option>
                            <option value={50}>50</option>
                            <option value={100}>100</option>
                          </select>
                          <Button
                            variant="outline"
                            size="sm"
                            disabled={incidentOffset === 0}
                            onClick={() => setIncidentOffset((o) => Math.max(0, o - incidentPageSize))}
                          >
                            Prev
                          </Button>
                          <Button
                            variant="outline"
                            size="sm"
                            disabled={incidentOffset + incidentPageSize >= visibleIncidents.length}
                            onClick={() => setIncidentOffset((o) => o + incidentPageSize)}
                          >
                            Next
                          </Button>
                        </div>
                      </div>
                    )}
                  </>
                ) : incidentRows.length > 0 ? (
                  <p className="text-muted-foreground text-sm">No incidents match these filters.</p>
                ) : (
                  <p className="text-muted-foreground text-sm">
                    Run prediction once open calls are logged and resolved, or wire a SCADA feed to raise them
                    automatically.
                  </p>
                )}
              </CardContent>
            </Card>

            <TroubleSpotsCard canWrite={canWrite} />

            <Card>
              <CardHeader className="gap-3 pb-3">
                <div className="flex flex-wrap items-start justify-between gap-2">
                  <div>
                    <CardTitle className="text-sm">Trouble calls</CardTitle>
                    <CardDescription>
                      {callTotal === 0
                        ? "No calls."
                        : `Showing ${callOffset + 1}–${callOffset + visibleCalls.length} of ${callTotal.toLocaleString()} · ${openCallCount.toLocaleString()} open`}
                    </CardDescription>
                  </div>
                  {/* Find (not a filter): highlights matching rows on the loaded page. */}
                  <div className="relative">
                    <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2" />
                    <input
                      className={cn(inputClass, "w-44 pr-7 pl-7")}
                      placeholder="Find in page…"
                      value={callFind}
                      onChange={(e) => setCallFind(e.target.value)}
                      aria-label="Find a call on the loaded page"
                    />
                    {findLower && (
                      <span className="text-muted-foreground absolute top-1/2 right-2 -translate-y-1/2 text-[11px] tabular-nums">
                        {findMatchIds.size}
                      </span>
                    )}
                  </div>
                </div>
                <div className="flex flex-wrap items-center gap-1.5">
                  <FilterChip active={callFilter === "all"} onClick={() => setCallFilter("all")}>
                    All
                  </FilterChip>
                  <FilterChip active={callFilter === "open"} onClick={() => setCallFilter("open")} count={openCallCount}>
                    Open
                  </FilterChip>
                  <FilterChip active={callFilter === "associated"} onClick={() => setCallFilter("associated")}>
                    Associated
                  </FilterChip>
                </div>
              </CardHeader>
              <CardContent className="pt-0">
                {calls.isPending ? (
                  <LoadingRow label="Loading calls…" />
                ) : calls.isError ? (
                  <p className="text-destructive text-sm">Could not load trouble calls.</p>
                ) : visibleCalls.length > 0 ? (
                  // Cap the height and scroll internally so a page of calls never
                  // pushes the rest of the board off the screen.
                  <div className="flex max-h-[28rem] flex-col overflow-y-auto">
                    {visibleCalls.map((call) => (
                      <TroubleCallRow
                        key={call.id}
                        call={call}
                        canWrite={canWrite}
                        onAssociate={setAssociating}
                        onJumpToIncident={(id) => navigate(`/outages/${id}`)}
                        highlighted={findMatchIds.has(call.id)}
                      />
                    ))}
                  </div>
                ) : callTotal > 0 ? (
                  <p className="text-muted-foreground text-sm">No calls on this page.</p>
                ) : (
                  <p className="text-muted-foreground text-sm">No trouble calls logged yet.</p>
                )}

                {/* Pagination — fetched a page at a time from the source. */}
                {callTotal > callPageSize && (
                  <div className="mt-2 flex flex-wrap items-center justify-between gap-2 text-xs">
                    <span className="text-muted-foreground tabular-nums">
                      {callOffset + 1}–{callOffset + visibleCalls.length} of {callTotal.toLocaleString()}
                    </span>
                    <div className="flex items-center gap-1.5">
                      <label className="text-muted-foreground" htmlFor="call-page-size">
                        Per page
                      </label>
                      <select
                        id="call-page-size"
                        className={cn(inputClass, "h-7 w-16 py-0")}
                        value={callPageSize}
                        onChange={(e) => setCallPageSize(Number(e.target.value))}
                      >
                        <option value={20}>20</option>
                        <option value={50}>50</option>
                        <option value={100}>100</option>
                      </select>
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={callOffset === 0}
                        onClick={() => setCallOffset((o) => Math.max(0, o - callPageSize))}
                      >
                        Prev
                      </Button>
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={callOffset + callPageSize >= callTotal}
                        onClick={() => setCallOffset((o) => o + callPageSize)}
                      >
                        Next
                      </Button>
                    </div>
                  </div>
                )}
              </CardContent>
            </Card>

            <details className="group rounded-lg border bg-card">
              <summary className="flex cursor-pointer list-none items-center gap-2 px-4 py-3 text-sm font-medium select-none">
                <ChevronRight className="size-4 transition-transform group-open:rotate-90" />
                Situational awareness
                <span className="text-muted-foreground font-normal">— source &amp; cause, last 12 months</span>
              </summary>
              <div className="border-t px-4 py-3">
                <DetectionMetricsCard />
              </div>
            </details>

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
      </div>

      {logging && <LogCallDialog onClose={() => setLogging(false)} />}
      {scheduling && <SchedulePlannedDialog onClose={() => setScheduling(false)} />}
      {associating && (
        <AssociateDialog key={associating.id} call={associating} onClose={() => setAssociating(null)} />
      )}
    </div>
  )
}

// --- incident detail page --------------------------------------------------

/** Full-page detail for one incident (/outages/:incidentId): its outage cone on
 * the map, the affected-area geographic rollup, the switching program + FLISR
 * restoration, the lifecycle timeline, and the trouble calls that fed it -- plus
 * the lifecycle actions (confirm/dispatch/restore/cancel, raise fault, show on
 * map) in the header. Reached by clicking a row on the board; Back returns there.
 * The incident is read from the already-cached board list (it refetches on a
 * direct/bookmarked load). */
export function OutageDetailPage() {
  const { incidentId = "" } = useParams()
  const navigate = useNavigate()
  const can = usePermission()
  const canWrite = can("oms:write")
  const canRaiseFault = can("faults:write")

  const incidents = useIncidents()
  // Just this incident's calls (server-scoped by incident_id, not the whole queue),
  // paginated so a mass-outage incident with thousands of calls stays manageable.
  const [callsPageSize, setCallsPageSize] = useState(50)
  const [callsOffset, setCallsOffset] = useState(0)
  const calls = useTroubleCalls({ incidentId, limit: callsPageSize, offset: callsOffset })
  const action = useIncidentAction()
  const raiseFault = useRaiseFaultFromIncident()
  const now = useTick(30_000)
  const [attributing, setAttributing] = useState(false)

  const inc = useMemo(
    () => (incidents.data?.incidents ?? []).find((i) => i.id === incidentId) ?? null,
    [incidents.data, incidentId],
  )
  const feedingCalls = calls.data?.trouble_calls ?? []
  const feedingCallsTotal = calls.data?.total ?? feedingCalls.length

  if (incidents.isPending) {
    return (
      <div className="p-4">
        <LoadingRow label="Loading incident…" />
      </div>
    )
  }
  if (!inc) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 p-4 text-center">
        <p className="text-muted-foreground text-sm">This incident doesn’t exist, or it’s no longer on record.</p>
        <Button variant="outline" size="sm" onClick={() => navigate("/outages")}>
          <ArrowLeft className="size-4" />
          Back to Outage Management
        </Button>
      </div>
    )
  }

  const customers = inc.affected_customers ?? 0
  const hasArea = (inc.affected_service_points ?? 0) > 0
  const hasDevice = inc.predicted_device_class != null && inc.predicted_device_id != null
  const isPlanned = inc.outage_type === "planned"
  const isTerminal = inc.status === "restored" || inc.status === "cancelled"
  const isActive = inc.status === "confirmed" || inc.status === "dispatched"
  const canConfirm = inc.status === "predicted" || inc.status === "scheduled"
  const canDispatch = inc.status === "confirmed"
  const pending = action.isPending

  function run(kind: IncidentAction) {
    if (!inc) return
    action.mutate({ id: inc.id, action: kind }, { onError: (err) => toastError(err, `Could not ${kind} the incident.`) })
  }
  function raiseFaultForIncident() {
    if (!inc) return
    raiseFault.mutate(
      { incidentId: inc.id },
      {
        onSuccess: () => useToastStore.getState().pushSuccess("Fault raised for this outage."),
        onError: (err) => toastError(err, "Could not raise a fault for this incident."),
      },
    )
  }
  function showOnMap() {
    if (!inc) return
    useOmsHighlightStore.getState().show(inc.id)
    navigate("/")
  }

  const confidencePct = inc.confidence != null ? `${Math.round(inc.confidence * 100)}%` : "—"
  const ageLabel = inc.restored_at ? "Duration" : isPlanned ? "Age" : "Elapsed"
  const ageValue = elapsed(inc.started_at, inc.restored_at, now)

  // The body cards, extracted so the layout can arrange them two ways: two
  // columns when there's a map/affected set to show on the left, or a single
  // focused column when there isn't (so a bare incident has no empty gutter).
  const overviewCard = (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm">Overview</CardTitle>
        <CardDescription>Incident details</CardDescription>
      </CardHeader>
      <CardContent className="pt-0">
        <dl className="grid grid-cols-[7.5rem_1fr] gap-x-3 gap-y-1.5 text-sm">
          <DetailRow label="Type" value={<span className="capitalize">{inc.outage_type}</span>} />
          <DetailRow label="Device" value={hasDevice ? deviceLabel(inc) : "Unattributed"} />
          <DetailRow
            label="Detected via"
            value={
              <span className="inline-flex items-center gap-1.5">
                {detectionSourceLabel(inc.detection_source)}
                <SourceBadge source={inc.detection_source} />
                {inc.detection_detail && <span className="text-muted-foreground">· {inc.detection_detail}</span>}
              </span>
            }
          />
          {inc.detected_at && <DetailRow label="Detected at" value={formatWhen(inc.detected_at)} />}
          {inc.territory_name && <DetailRow label="Territory" value={inc.territory_name} />}
          {inc.circuit_id && (
            <DetailRow label="Circuit" value={<span className="font-mono text-xs">{shortId(inc.circuit_id)}</span>} />
          )}
          <DetailRow label="Started" value={formatWhen(inc.started_at)} />
          {isPlanned && inc.planned_start && (
            <DetailRow
              label="Planned"
              value={`${formatWhen(inc.planned_start)}${inc.planned_end ? ` – ${formatWhen(inc.planned_end)}` : ""}`}
            />
          )}
          {inc.etr && <DetailRow label="ETR" value={formatWhen(inc.etr)} />}
          {inc.restored_at && <DetailRow label="Restored" value={formatWhen(inc.restored_at)} />}
          {inc.crew_ref && <DetailRow label="Crew" value={inc.crew_ref} />}
          {inc.last_actor_name && <DetailRow label="Last actor" value={inc.last_actor_name} />}
          {inc.notes && <DetailRow label="Notes" value={inc.notes} />}
          <DetailRow label="Incident ID" value={<span className="font-mono text-xs">{shortId(inc.id)}</span>} />
        </dl>
      </CardContent>
    </Card>
  )
  const callsFrom = feedingCallsTotal === 0 ? 0 : callsOffset + 1
  const callsTo = Math.min(callsOffset + feedingCalls.length, feedingCallsTotal)
  const callsCard = (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="text-sm">Trouble calls</CardTitle>
        <CardDescription>
          {feedingCallsTotal === 0
            ? "No calls linked to this incident."
            : `${feedingCallsTotal} call${feedingCallsTotal === 1 ? "" : "s"} fed this incident`}
        </CardDescription>
      </CardHeader>
      {feedingCallsTotal > 0 && (
        <CardContent className="flex flex-col gap-2 pt-0">
          <div className="flex max-h-[32rem] flex-col overflow-y-auto">
            {feedingCalls.map((c) => (
              <TroubleCallRow
                key={c.id}
                call={c}
                canWrite={canWrite}
                onAssociate={() => {}}
                onJumpToIncident={(id) => navigate(`/outages/${id}`)}
              />
            ))}
          </div>
          {feedingCallsTotal > callsPageSize && (
            <div className="text-muted-foreground flex flex-wrap items-center gap-2 text-xs">
              <span className="tabular-nums">
                Showing {callsFrom}–{callsTo} of {feedingCallsTotal}
              </span>
              <div className="ml-auto flex items-center gap-1">
                <select
                  className={cn(inputClass, "h-7 w-auto py-0")}
                  value={callsPageSize}
                  onChange={(e) => {
                    setCallsPageSize(Number(e.target.value))
                    setCallsOffset(0)
                  }}
                  aria-label="Calls per page"
                >
                  {[50, 100, 200].map((n) => (
                    <option key={n} value={n}>
                      {n} / page
                    </option>
                  ))}
                </select>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={callsOffset === 0}
                  onClick={() => setCallsOffset(Math.max(0, callsOffset - callsPageSize))}
                >
                  Prev
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  disabled={callsOffset + callsPageSize >= feedingCallsTotal}
                  onClick={() => setCallsOffset(callsOffset + callsPageSize)}
                >
                  Next
                </Button>
              </div>
            </div>
          )}
        </CardContent>
      )}
    </Card>
  )
  const causeCard = <IncidentCauseCard inc={inc} canWrite={canWrite} />
  const crewsCard = <IncidentCrewsCard inc={inc} canWrite={canWrite} />
  const switchingCard = <SwitchingDrawer incident={inc} canWrite={canWrite} />
  const timelineCard = <IncidentTimelineCard incidentId={inc.id} />

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-6xl flex-col gap-4">
        <button
          type="button"
          onClick={() => navigate("/outages")}
          className="text-muted-foreground hover:text-foreground -mb-1 inline-flex w-fit items-center gap-1 text-sm"
        >
          <ArrowLeft className="size-4" />
          Outage Management
        </button>

        {/* Header band — severity-accented so the page leads with how bad it is. */}
        <div className={cn("bg-card rounded-xl border border-l-4 p-4 shadow-sm", severityAccent(customers))}>
          <div className="flex flex-wrap items-start justify-between gap-3">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <StatusBadge status={inc.status} />
                <SourceBadge source={inc.detection_source} />
                {isActive && hasDevice && <LiveDeEnergized incidentId={inc.id} />}
              </div>
              <h1 className="mt-1.5 truncate text-xl font-semibold tracking-tight">{deviceLabel(inc)}</h1>
              <p className="text-muted-foreground text-sm">
                {isPlanned ? "Planned outage" : "Unplanned outage"}
                {inc.cause_code ? ` · ${inc.cause_code}` : ""}
                {inc.last_actor_name ? ` · last by ${inc.last_actor_name}` : ""}
              </p>
            </div>

            <div className="flex flex-wrap items-center justify-end gap-2">
              {hasArea && (
                <Button variant="outline" size="sm" onClick={showOnMap} title="Highlight the outage cone on the main map">
                  <MapPinned className="size-4" />
                  Show on map
                </Button>
              )}
              {canWrite && !isTerminal && (
                <Button
                  variant={hasDevice ? "outline" : "default"}
                  size="sm"
                  onClick={() => setAttributing(true)}
                  title="Set or change the protective device this incident is blamed on"
                >
                  <PlugZap className="size-4" />
                  {hasDevice ? "Change device" : "Attribute device"}
                </Button>
              )}
              {canRaiseFault && inc.status !== "cancelled" && (
                <Button variant="outline" size="sm" disabled={raiseFault.isPending} onClick={raiseFaultForIncident}>
                  <Zap className="size-4" />
                  Raise fault
                </Button>
              )}
              {canWrite && !isTerminal && (
                <>
                  {canConfirm && (
                    <Button size="sm" disabled={pending} onClick={() => run("confirm")}>
                      <CheckCircle2 className="size-4" />
                      {isPlanned ? "Begin outage" : "Confirm"}
                    </Button>
                  )}
                  {canDispatch && (
                    <Button variant="outline" size="sm" disabled={pending} onClick={() => run("dispatch")}>
                      <Truck className="size-4" />
                      Dispatch
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
                    Cancel
                  </Button>
                </>
              )}
            </div>
          </div>
        </div>

        {/* Headline figures for this one incident. */}
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          <StatTile
            icon={<Users className="size-5" />}
            label="Customers off"
            value={customers.toLocaleString()}
            sub="affected"
            tone={customers > 0 ? "alert" : "ok"}
          />
          <StatTile
            icon={<MapPin className="size-5" />}
            label="Service points"
            value={(inc.affected_service_points ?? 0).toLocaleString()}
            sub="supplies out"
            tone={(inc.affected_service_points ?? 0) > 0 ? "warn" : "ok"}
          />
          <StatTile
            icon={<CalendarClock className="size-5" />}
            label={ageLabel}
            value={ageValue}
            sub={inc.restored_at ? "restored" : "since start"}
          />
          <StatTile
            icon={<Gauge className="size-5" />}
            label="Confidence"
            value={confidencePct}
            sub={hasDevice ? "device blamed" : "unattributed"}
          />
        </div>

        {hasArea ? (
          // There's a cone to draw: two columns — the "who's out" picture
          // (overview, supplies, map) beside the "what to do" lane (switching,
          // timeline, calls).
          <div className="grid gap-4 lg:grid-cols-2">
            <div className="flex min-w-0 flex-col gap-4">
              {overviewCard}
              {causeCard}
              <AffectedSuppliesCard incidentId={inc.id} onLocate={() => navigate("/")} />
              <OutageMap incidentId={inc.id} />
            </div>
            <div className="flex min-w-0 flex-col gap-4">
              {crewsCard}
              {switchingCard}
              {timelineCard}
              {callsCard}
            </div>
          </div>
        ) : (
          // Nothing to map (unattributed / zero-SP): a single focused column
          // reads far better than a 2-col grid with a tall empty gutter.
          <div className="mx-auto flex w-full max-w-3xl flex-col gap-4">
            {overviewCard}
            {causeCard}
            {crewsCard}
            {switchingCard}
            {timelineCard}
            {callsCard}
          </div>
        )}
      </div>

      {attributing && <AttributeDeviceDialog incidentId={inc.id} onClose={() => setAttributing(false)} />}
    </div>
  )
}

/** One headline figure on the incident detail page. `tone` tints the value +
 * the icon chip (alert red / warn amber / ok emerald / neutral). */
function StatTile({
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
  tone?: "default" | "alert" | "warn" | "ok"
}) {
  const chip =
    tone === "alert"
      ? "bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-300"
      : tone === "warn"
        ? "bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300"
        : tone === "ok"
          ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300"
          : "bg-muted text-muted-foreground"
  const valueTone =
    tone === "alert"
      ? "text-red-600 dark:text-red-400"
      : tone === "warn"
        ? "text-amber-600 dark:text-amber-400"
        : "text-foreground"
  return (
    <div className="bg-card flex items-center gap-3 rounded-lg border p-3">
      <div className={cn("flex size-9 shrink-0 items-center justify-center rounded-lg", chip)}>{icon}</div>
      <div className="min-w-0">
        <div className="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">{label}</div>
        <div className={cn("text-xl leading-tight font-semibold tabular-nums", valueTone)}>{value}</div>
        {sub && <div className="text-muted-foreground truncate text-[11px]">{sub}</div>}
      </div>
    </div>
  )
}

/** Human label for an affected supply's feature class in the supplies table. */
function supplyTypeLabel(cls?: string): string {
  if (cls === ServicePointClass) return "Service point"
  if (cls === MeterClass) return "Meter"
  return cls ? cls.replace(/_/g, " ").toLowerCase() : "Supply"
}

/** Short, friendly label for a restoration-tier priority code (P1..P4). */
const TIER_TONE: Record<string, string> = {
  P1: "bg-red-500",
  P2: "bg-amber-500",
  P3: "bg-sky-500",
  P4: "bg-muted-foreground/40",
}

/** "Who's out" in detail: the affected supplies broken down three ways — by
 * restoration tier (so critical load jumps out), by territory, and the actual
 * service-point names (click one to find it on the map). Reads the same
 * /affected response the map draws, so the numbers match exactly. */
function AffectedSuppliesCard({ incidentId, onLocate }: { incidentId: string; onLocate: () => void }) {
  const affected = useAffectedArea(incidentId)
  const [showAllSps, setShowAllSps] = useState(false)
  const d = affected.data

  // Biggest supplies first, so a table that's truncated to a preview still leads
  // with the most customers.
  const sps = [...(d?.service_points ?? [])].sort((a, b) => (b.customers ?? 0) - (a.customers ?? 0))
  const rollup = d?.service
  const byTerritory = d?.by_territory ?? []
  const missing = d?.missing_geometry ?? 0
  const criticalCustomers = rollup?.critical.customers ?? 0
  const tiers = (rollup?.by_priority ?? []).filter((t) => t.service_points > 0)
  const maxTierCust = Math.max(1, ...tiers.map((t) => t.customers))
  const SP_PREVIEW = 12
  const shownSps = showAllSps ? sps : sps.slice(0, SP_PREVIEW)

  function locateSp(sp: AffectedServicePoint) {
    useSelectionStore.getState().select(ServicePointClass, sp.id, { focus: true })
    onLocate()
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-start justify-between gap-2">
          <div>
            <CardTitle className="text-sm">Affected supplies</CardTitle>
            <CardDescription>Who is out — by tier, territory and point</CardDescription>
          </div>
          {criticalCustomers > 0 && (
            <span
              className="inline-flex shrink-0 items-center gap-1 rounded bg-red-600 px-1.5 py-0.5 text-[11px] font-semibold text-white"
              title="Critical customers (life-support / essential) in the affected set"
            >
              <ShieldAlert className="size-3" />
              {criticalCustomers.toLocaleString()} critical
            </span>
          )}
        </div>
      </CardHeader>
      <CardContent className="flex flex-col gap-4 pt-0">
        {affected.isPending ? (
          <LoadingRow label="Loading affected supplies…" />
        ) : affected.isError ? (
          <p className="text-destructive text-sm">Could not load the affected set.</p>
        ) : (
          <>
            {missing > 0 && (
              <div className="flex items-center gap-2 rounded-md border border-amber-300 bg-amber-50 px-2 py-1.5 text-xs text-amber-800 dark:border-amber-900 dark:bg-amber-950 dark:text-amber-300">
                <ShieldAlert className="size-3.5 shrink-0" />
                {missing} affected suppl{missing === 1 ? "y is" : "ies are"} no longer on the map (deleted / removed) —
                still counted.
              </div>
            )}

            {/* By restoration tier — share bars, critical tiers tinted hot. */}
            {tiers.length > 0 && (
              <div className="flex flex-col gap-1.5">
                <div className="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
                  By restoration priority
                </div>
                {tiers.map((t) => (
                  <div key={t.priority} className="flex items-center gap-2 text-xs">
                    <span className="w-6 shrink-0 font-medium tabular-nums">{t.priority}</span>
                    <div className="bg-muted relative h-3 flex-1 overflow-hidden rounded-sm">
                      <div
                        className={cn("h-full rounded-sm", TIER_TONE[t.priority] ?? "bg-muted-foreground/40")}
                        style={{ width: `${(t.customers / maxTierCust) * 100}%` }}
                      />
                    </div>
                    <span className="w-28 shrink-0 text-right tabular-nums">
                      {t.customers.toLocaleString()} cust · {t.service_points} SP
                    </span>
                  </div>
                ))}
              </div>
            )}

            {/* By territory — region / district rollup. */}
            {byTerritory.map((lvl) => (
              <div key={lvl.level_code} className="flex flex-col gap-1">
                <div className="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
                  By {lvl.level_name.toLowerCase()}
                </div>
                {lvl.rows.map((r) => (
                  <div key={r.territory_id} className="flex items-center justify-between gap-2 text-xs">
                    <span className="truncate">{r.territory_name}</span>
                    <span className="text-muted-foreground shrink-0 tabular-nums">
                      {r.customers.toLocaleString()} cust · {r.service_points} SP
                    </span>
                  </div>
                ))}
              </div>
            ))}

            {/* The actual supplies, as a table — which points are out, their
                type and customer count; click a row to find it on the map. */}
            {sps.length > 0 && (
              <div className="flex flex-col gap-1.5">
                <div className="text-muted-foreground text-[11px] font-medium tracking-wide uppercase">
                  Service points ({sps.length})
                </div>
                <div className="overflow-hidden rounded-md border">
                  <table className="w-full text-xs">
                    <thead className="bg-muted/50 text-muted-foreground">
                      <tr className="[&>th]:px-2 [&>th]:py-1.5 [&>th]:text-left [&>th]:font-medium">
                        <th>Supply</th>
                        <th>Type</th>
                        <th className="text-right!">Customers</th>
                        <th className="w-8"></th>
                      </tr>
                    </thead>
                    <tbody>
                      {shownSps.map((sp) => (
                        <tr
                          key={sp.id}
                          onClick={() => locateSp(sp)}
                          title="Find this supply on the map"
                          className="hover:bg-muted/40 border-border/60 cursor-pointer border-t [&>td]:px-2 [&>td]:py-1.5"
                        >
                          <td className="font-medium">{sp.name || shortId(sp.id)}</td>
                          <td className="text-muted-foreground">{supplyTypeLabel(sp.feature_class)}</td>
                          <td className="text-right tabular-nums">
                            {sp.customers != null ? sp.customers.toLocaleString() : "—"}
                          </td>
                          <td className="text-muted-foreground">
                            <MapPin className="size-3.5" />
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                {sps.length > SP_PREVIEW && (
                  <button
                    type="button"
                    onClick={() => setShowAllSps((v) => !v)}
                    className="text-primary w-fit text-xs hover:underline"
                  >
                    {showAllSps ? "Show fewer" : `Show all ${sps.length}`}
                  </button>
                )}
              </div>
            )}
          </>
        )}
      </CardContent>
    </Card>
  )
}

// Cause certainty (migration 0168): unknown -> suspected -> confirmed.
const CAUSE_STATUS_TONE: Record<string, string> = {
  unknown: "bg-muted text-muted-foreground",
  suspected: "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300",
  confirmed: "bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300",
}
const CAUSE_STATUSES = ["unknown", "suspected", "confirmed"] as const

function CauseStatusBadge({ status }: { status: string }) {
  return (
    <span
      className={cn(
        "rounded px-1.5 py-0.5 text-[11px] font-medium capitalize",
        CAUSE_STATUS_TONE[status] ?? "bg-muted text-muted-foreground",
      )}
    >
      {status}
    </span>
  )
}

/** The incident's coded cause + certainty — the coarse "why", editable in place
 * (writers only). Picking a cause with no prior certainty bumps it to 'suspected'
 * server-side; "Unknown / not set" clears it. The detailed per-asset failure mode
 * is recorded by raising a fault, which also confirms this cause (Slice D). */
function IncidentCauseCard({ inc, canWrite }: { inc: Incident; canWrite: boolean }) {
  const causes = useOutageCauses()
  const setAttr = useSetIncidentAttribution()
  const rows = causes.data?.outage_causes ?? []
  const current = rows.find((c) => c.code === inc.cause_code)
  const label = current?.label ?? inc.cause_code ?? "Unknown"

  function patch(body: { cause_code?: string; cause_status?: string }) {
    setAttr.mutate({ id: inc.id, body }, { onError: (e) => toastError(e, "Could not update the cause.") })
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <div className="flex items-start justify-between gap-2">
          <div>
            <CardTitle className="text-sm">Cause</CardTitle>
            <CardDescription>Why the outage happened</CardDescription>
          </div>
          <CauseStatusBadge status={inc.cause_status} />
        </div>
      </CardHeader>
      <CardContent className="flex flex-col gap-2 pt-0">
        {canWrite ? (
          <>
            <select
              className={inputClass}
              value={inc.cause_code ?? ""}
              disabled={setAttr.isPending}
              onChange={(e) => patch({ cause_code: e.target.value })}
              aria-label="Outage cause"
            >
              <option value="">Unknown / not set</option>
              {rows.map((c) => (
                <option key={c.code} value={c.code}>
                  {c.label}
                </option>
              ))}
            </select>
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="text-muted-foreground text-xs">Certainty</span>
              {CAUSE_STATUSES.map((s) => (
                <button
                  key={s}
                  type="button"
                  disabled={setAttr.isPending}
                  onClick={() => patch({ cause_status: s })}
                  className={cn(
                    "rounded-full border px-2 py-0.5 text-xs capitalize transition-colors",
                    inc.cause_status === s
                      ? "border-primary bg-primary text-primary-foreground"
                      : "border-border text-muted-foreground hover:bg-muted",
                  )}
                >
                  {s}
                </button>
              ))}
            </div>
          </>
        ) : (
          <div className="text-sm">{label}</div>
        )}
        <p className="text-muted-foreground text-[11px]">
          Coarse outage cause. Raise a fault to record the detailed asset failure mode.
        </p>
      </CardContent>
    </Card>
  )
}

// Crew assignment status tones (dispatched → on_site → cleared).
const ASSIGN_TONE: Record<string, string> = {
  dispatched: "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300",
  on_site: "bg-indigo-100 text-indigo-800 dark:bg-indigo-950 dark:text-indigo-300",
  cleared: "bg-muted text-muted-foreground",
}

/** A local datetime-input value (YYYY-MM-DDTHH:mm) for a Date, in local time. */
function toLocalInput(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0")
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`
}

/** Crews assigned to an incident + its ETR — the operational "who's on it, and
 * when will it be back". Dispatch an available crew, advance it (on-site →
 * cleared), and set/clear the ETR (with a quick +2h estimate). Replaces the
 * free-text crew_ref and the hand-typed ETR. */
function IncidentCrewsCard({ inc, canWrite }: { inc: Incident; canWrite: boolean }) {
  const crews = useCrews()
  const subcontractors = useSubcontractors()
  const assignments = useIncidentCrews(inc.id)
  const workOrders = useWorkOrders({ incidentId: inc.id })
  const dispatch = useDispatchCrew()
  const taskSub = useTaskSubcontractor()
  const advance = useAdvanceAssignment()
  const setEtr = useSetIncidentETR()
  const queryClient = useQueryClient()
  const [pickCrew, setPickCrew] = useState("")
  const [pickSub, setPickSub] = useState("")
  const [etrLocal, setEtrLocal] = useState("")
  const [raiseWO, setRaiseWO] = useState(true)

  const rows = assignments.data?.assignments ?? []
  const woRows = workOrders.data?.work_orders ?? []
  const subRows = subcontractors.data?.subcontractors ?? []
  // A crew may run several incidents at once, so the picker offers every active
  // crew (annotated with its load), minus any already active on THIS incident.
  const onThisIncident = new Set(rows.filter((a) => a.status !== "cleared").map((a) => a.crew_id))
  const dispatchableCrews = (crews.data?.crews ?? []).filter((c) => c.active && !onThisIncident.has(c.id))
  const isTerminal = inc.status === "restored" || inc.status === "cancelled"
  const busy = dispatch.isPending || advance.isPending || setEtr.isPending

  function doDispatch() {
    if (!pickCrew) return
    dispatch.mutate(
      { incidentId: inc.id, crew_id: pickCrew, raise_work_order: raiseWO },
      {
        onSuccess: (res) => {
          setPickCrew("")
          const woId = (res as { work_order_id?: string } | undefined)?.work_order_id
          if (woId) useToastStore.getState().pushSuccess("Crew dispatched and a restoration work order raised.")
        },
        onError: (e) => toastError(e, "Could not dispatch the crew."),
      },
    )
  }
  function doTaskSub() {
    if (!pickSub) return
    taskSub.mutate(
      { incidentId: inc.id, subcontractor_id: pickSub },
      {
        onSuccess: () => {
          setPickSub("")
          queryClient.invalidateQueries({ queryKey: ["wam"] })
          useToastStore.getState().pushSuccess("Subcontractor tasked; a restoration work order was raised.")
        },
        onError: (e) => toastError(e, "Could not task the subcontractor."),
      },
    )
  }
  function adv(id: string, status: string) {
    advance.mutate(
      { id, status },
      {
        // Going on-site starts the crew's restoration work orders server-side;
        // refresh the WAM cache so the Field work list reflects it.
        onSuccess: () => {
          if (status === "on_site") queryClient.invalidateQueries({ queryKey: ["wam"] })
        },
        onError: (e) => toastError(e, "Could not update the crew."),
      },
    )
  }
  function saveEtr() {
    const iso = etrLocal ? new Date(etrLocal).toISOString() : null
    setEtr.mutate({ id: inc.id, etr: iso }, { onError: (e) => toastError(e, "Could not set the ETR.") })
  }

  return (
    <Card>
      <CardHeader className="pb-2">
        <CardTitle className="flex items-center gap-2 text-sm">
          <Truck className="size-4" />
          Crews &amp; ETR
        </CardTitle>
        <CardDescription>Who's on it, and when it should be back</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3 pt-0">
        {/* ETR */}
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="text-muted-foreground w-12">ETR</span>
          <span className="font-medium tabular-nums">{inc.etr ? formatWhen(inc.etr) : "— not set"}</span>
          {canWrite && !isTerminal && (
            <div className="flex flex-wrap items-center gap-1.5">
              <input
                type="datetime-local"
                className={cn(inputClass, "w-48")}
                value={etrLocal}
                onChange={(e) => setEtrLocal(e.target.value)}
              />
              <Button variant="ghost" size="sm" onClick={() => setEtrLocal(toLocalInput(new Date(Date.now() + 2 * 3600_000)))}>
                +2h
              </Button>
              <Button variant="outline" size="sm" disabled={busy || !etrLocal} onClick={saveEtr}>
                Set
              </Button>
              {inc.etr && (
                <Button
                  variant="ghost"
                  size="sm"
                  className="text-muted-foreground"
                  disabled={busy}
                  onClick={() => {
                    setEtrLocal("")
                    setEtr.mutate({ id: inc.id, etr: null }, { onError: (e) => toastError(e, "Could not clear the ETR.") })
                  }}
                >
                  Clear
                </Button>
              )}
            </div>
          )}
        </div>

        {/* Assigned crews */}
        <div className="flex flex-col gap-1.5">
          {rows.length === 0 ? (
            <p className="text-muted-foreground text-sm">No crew assigned.</p>
          ) : (
            rows.map((a) => (
              <div key={a.id} className="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm">
                <span className="font-medium">{a.crew_name ?? "Crew"}</span>
                <span
                  className={cn(
                    "rounded px-1.5 py-0.5 text-[11px] font-medium capitalize",
                    ASSIGN_TONE[a.status] ?? "bg-muted text-muted-foreground",
                  )}
                >
                  {a.status.replace("_", " ")}
                </span>
                <span className="text-muted-foreground text-xs tabular-nums">
                  disp {formatWhen(a.dispatched_at)}
                  {a.on_site_at ? ` · on-site ${formatWhen(a.on_site_at)}` : ""}
                  {a.cleared_at ? ` · cleared ${formatWhen(a.cleared_at)}` : ""}
                </span>
                {canWrite && a.status !== "cleared" && (
                  <span className="ml-auto flex items-center gap-1">
                    {a.status === "dispatched" && (
                      <Button variant="outline" size="sm" disabled={busy} onClick={() => adv(a.id, "on_site")}>
                        <MapPin className="size-4" />
                        On-site
                      </Button>
                    )}
                    <Button variant="ghost" size="sm" disabled={busy} onClick={() => adv(a.id, "cleared")}>
                      <CheckCircle2 className="size-4" />
                      Clear
                    </Button>
                  </span>
                )}
              </div>
            ))
          )}
        </div>

        {/* Dispatch a crew */}
        {canWrite && !isTerminal && (
          <div className="flex flex-wrap items-center gap-2">
            <select
              className={cn(inputClass, "min-w-48 flex-1")}
              value={pickCrew}
              onChange={(e) => setPickCrew(e.target.value)}
              aria-label="Assign an available crew"
            >
              <option value="">Assign a crew…</option>
              {dispatchableCrews.map((c) => {
                const load: string[] = []
                if (c.status !== "available") load.push(c.status.replace("_", " "))
                if (c.active_assignment_count) load.push(`${c.active_assignment_count} incident${c.active_assignment_count === 1 ? "" : "s"}`)
                if (c.open_work_order_count) load.push(`${c.open_work_order_count} open`)
                return (
                  <option key={c.id} value={c.id}>
                    {c.name}
                    {load.length > 0 ? ` — ${load.join(" · ")}` : ""}
                  </option>
                )
              })}
            </select>
            <Button variant="outline" size="sm" disabled={!pickCrew || busy} onClick={doDispatch}>
              <Truck className="size-4" />
              Dispatch
            </Button>
            <label className="text-muted-foreground flex items-center gap-1.5 text-xs">
              <input type="checkbox" checked={raiseWO} onChange={(e) => setRaiseWO(e.target.checked)} />
              Raise restoration work order
            </label>
          </div>
        )}
        {canWrite && !isTerminal && dispatchableCrews.length === 0 && (
          <p className="text-muted-foreground text-[11px]">
            {(crews.data?.crews ?? []).some((c) => c.active)
              ? "All active crews are already on this incident."
              : "No crews yet. Create crews in "}
            {!(crews.data?.crews ?? []).some((c) => c.active) && <span className="font-medium">Admin → Crews</span>}
          </p>
        )}

        {/* Task an external subcontractor (no crew, no fan-out) */}
        {canWrite && !isTerminal && subRows.length > 0 && (
          <div className="flex flex-wrap items-center gap-2">
            <select
              className={cn(inputClass, "min-w-48 flex-1")}
              value={pickSub}
              onChange={(e) => setPickSub(e.target.value)}
              aria-label="Task an external subcontractor"
            >
              <option value="">Task a subcontractor…</option>
              {subRows.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                  {s.open_work_order_count ? ` — ${s.open_work_order_count} open` : ""}
                </option>
              ))}
            </select>
            <Button variant="outline" size="sm" disabled={!pickSub || taskSub.isPending} onClick={doTaskSub}>
              <Building2 className="size-4" />
              Task
            </Button>
          </div>
        )}

        {/* Field work raised for this incident */}
        {woRows.length > 0 && (
          <div className="flex flex-col gap-1 border-t pt-2">
            <span className="text-muted-foreground text-[11px] font-medium uppercase tracking-wide">Field work</span>
            {woRows.map((o) => (
              <div key={o.id} className="flex flex-wrap items-center gap-x-2 text-sm">
                <Wrench className="text-muted-foreground size-3.5" />
                <span className="font-medium">{o.title}</span>
                <span className="text-muted-foreground text-xs capitalize">{o.status.replace("_", " ")}</span>
                {o.crew_name && <span className="text-muted-foreground text-xs">· {o.crew_name}</span>}
              </div>
            ))}
          </div>
        )}
      </CardContent>
    </Card>
  )
}

/** One label/value pair in the incident Overview definition list. Rendered as a
 * fragment so each cell lands in the parent's two-column grid. */
function DetailRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words">{value}</dd>
    </>
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
