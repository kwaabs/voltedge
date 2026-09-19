import { useMemo, useState } from "react"
import { CheckCircle2, History, Loader2, Plus, RotateCcw, Search, TriangleAlert, Wrench, Zap, ZapOff } from "lucide-react"

import { FormDialog } from "@/components/admin/form-dialog"
import { AssetIdField } from "@/components/map/asset-id-field"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import {
  type FaultEvent,
  type FaultStatus,
  type FaultType,
  type FaultHistoryEntry,
  useBookDefectFromFault,
  useCreateFault,
  useFaultHistory,
  useFaults,
  useSetFaultStatus,
} from "@/hooks/use-faults"
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"
const FAULT_TYPES: FaultType[] = ["SLG", "LL", "LLG", "LLL", "LLLG", "earth", "unknown"]

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

function shortId(id: string): string {
  return id.slice(0, 8)
}

function formatWhen(iso: string): string {
  return new Date(iso).toLocaleString()
}

function humanize(s: string): string {
  return s.replace(/_/g, " ")
}

function assetLabel(cls: string | null, id: string | null): string | null {
  if (!cls || !id) return null
  return `${cls} · ${shortId(id)}`
}

// --- badges ----------------------------------------------------------------

const FAULT_STATUS_BADGE: Record<FaultStatus, string> = {
  detected: "bg-rose-100 text-rose-800 dark:bg-rose-950 dark:text-rose-300",
  investigating: "bg-amber-100 text-amber-800 dark:bg-amber-950 dark:text-amber-300",
  cleared: "bg-blue-100 text-blue-800 dark:bg-blue-950 dark:text-blue-300",
  closed: "bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300",
}

function Badge({ text, cls }: { text: string; cls: string }) {
  return <span className={`rounded px-1.5 py-0.5 text-xs font-medium capitalize ${cls}`}>{humanize(text)}</span>
}

// --- log-fault dialog ------------------------------------------------------

/** Log a fault event. A title plus an anchor (an asset, a location, or -- via
 * the OMS panel, not here -- an incident) is required; the electrical detail is
 * optional and can be filled in as an investigation proceeds. */
function LogFaultDialog({ onClose }: { onClose: () => void }) {
  const [title, setTitle] = useState("")
  const [faultType, setFaultType] = useState<FaultType>("unknown")
  const [cause, setCause] = useState("")
  const [assetClass, setAssetClass] = useState("")
  const [assetId, setAssetId] = useState("")
  const [longitude, setLongitude] = useState("")
  const [latitude, setLatitude] = useState("")
  const [faultCurrent, setFaultCurrent] = useState("")
  const [clearingTime, setClearingTime] = useState("")
  const [detectedBy, setDetectedBy] = useState("")
  const [description, setDescription] = useState("")
  const create = useCreateFault()

  const hasAsset = assetClass.trim() !== "" && assetId.trim() !== ""
  const hasLoc = longitude.trim() !== "" && latitude.trim() !== ""
  const canSave = title.trim() !== "" && (hasAsset || hasLoc) && !create.isPending

  function num(v: string): number | undefined {
    const n = Number(v)
    return v.trim() === "" || Number.isNaN(n) ? undefined : n
  }

  function handleSave() {
    create.mutate(
      {
        title: title.trim(),
        fault_type: faultType,
        cause: cause.trim() || undefined,
        asset_class: assetClass.trim() || undefined,
        asset_id: assetId.trim() || undefined,
        longitude: num(longitude),
        latitude: num(latitude),
        fault_current_a: num(faultCurrent),
        clearing_time_ms: num(clearingTime),
        detected_by: detectedBy.trim() || undefined,
        description: description.trim() || undefined,
      },
      { onSuccess: onClose, onError: (err) => toastError(err, "Could not log the fault.") },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Log fault event"
      description="Record an electrical fault on the network. Anchor it to an asset or a location pin."
      onSubmit={handleSave}
      submitLabel="Log fault"
      canSubmit={canSave}
      saving={create.isPending}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Title</label>
        <input className={inputClass} value={title} onChange={(e) => setTitle(e.target.value)} placeholder="e.g. Earth fault on Feeder 1" autoFocus />
      </div>

      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Fault type</label>
          <select className={inputClass} value={faultType} onChange={(e) => setFaultType(e.target.value as FaultType)}>
            {FAULT_TYPES.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        </div>
        <div className="flex flex-[2] flex-col gap-1">
          <label className={labelClass}>Cause</label>
          <input className={inputClass} value={cause} onChange={(e) => setCause(e.target.value)} placeholder="e.g. vegetation (optional)" />
        </div>
      </div>

      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Asset class</label>
          <input className={inputClass} value={assetClass} onChange={(e) => setAssetClass(e.target.value)} placeholder="e.g. MV_CONDUCTOR" />
        </div>
        <div className="flex flex-[2] flex-col gap-1">
          <label className={labelClass}>Asset ID</label>
          <AssetIdField
            value={assetId}
            onChange={setAssetId}
            onResolveClass={(code) => setAssetClass((cur) => (cur.trim() === "" ? code : cur))}
            placeholder="UUID of the faulted asset"
          />
        </div>
      </div>

      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Longitude</label>
          <input className={inputClass} value={longitude} onChange={(e) => setLongitude(e.target.value)} placeholder="Pin lng (or use an asset)" />
        </div>
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Latitude</label>
          <input className={inputClass} value={latitude} onChange={(e) => setLatitude(e.target.value)} placeholder="Pin lat" />
        </div>
      </div>

      <div className="flex gap-2">
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Fault current (A)</label>
          <input className={inputClass} value={faultCurrent} onChange={(e) => setFaultCurrent(e.target.value)} placeholder="Optional" />
        </div>
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Clearing time (ms)</label>
          <input className={inputClass} value={clearingTime} onChange={(e) => setClearingTime(e.target.value)} placeholder="Optional" />
        </div>
        <div className="flex flex-1 flex-col gap-1">
          <label className={labelClass}>Detected by</label>
          <input className={inputClass} value={detectedBy} onChange={(e) => setDetectedBy(e.target.value)} placeholder="e.g. scada (optional)" />
        </div>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Description</label>
        <input className={inputClass} value={description} onChange={(e) => setDescription(e.target.value)} placeholder="Optional" />
      </div>

      {!hasAsset && !hasLoc && (
        <p className="text-muted-foreground text-xs">Give an asset (class + id) or a location pin to anchor the fault.</p>
      )}
    </FormDialog>
  )
}

// --- fault row -------------------------------------------------------------

function FaultRow({ fault, canWrite }: { fault: FaultEvent; canWrite: boolean }) {
  const setStatus = useSetFaultStatus()
  const book = useBookDefectFromFault()
  const pending = setStatus.isPending || book.isPending
  const [showHistory, setShowHistory] = useState(false)
  const history = useFaultHistory(fault.id, showHistory)

  const hasAsset = !!fault.asset_class && !!fault.asset_id
  const hasLoc = fault.longitude != null && fault.latitude != null
  const isClosed = fault.status === "closed"
  const canBookDefect = canWrite && !fault.defect_id && (hasAsset || hasLoc) && !isClosed

  function set(status: FaultStatus) {
    setStatus.mutate(
      { id: fault.id, status },
      { onError: (err) => toastError(err, "Could not update the fault.") },
    )
  }

  function bookDefect() {
    book.mutate(
      { faultId: fault.id },
      { onError: (err) => toastError(err, "Could not book a defect from this fault.") },
    )
  }

  const asset = assetLabel(fault.asset_class, fault.asset_id)

  return (
    <div className="hover:bg-muted/40 rounded-md">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 px-2 py-2">
      <Badge text={fault.status} cls={FAULT_STATUS_BADGE[fault.status]} />
      {fault.fault_type && <span className="text-muted-foreground font-mono text-xs">{fault.fault_type}</span>}
      <span className="text-sm font-medium">{fault.title}</span>
      <span className="text-muted-foreground text-xs">
        {asset ?? (hasLoc ? "pinned" : fault.incident_id ? `outage ${shortId(fault.incident_id)}` : "—")}
        {fault.cause && <> · {humanize(fault.cause)}</>}
        {fault.fault_current_a != null && <> · {fault.fault_current_a} A</>}
        {fault.incident_id && asset && <> · outage {shortId(fault.incident_id)}</>}
        {fault.defect_id && <> · defect {shortId(fault.defect_id)}</>}
      </span>
      <span className="text-muted-foreground ml-auto text-xs">{formatWhen(fault.detected_at)}</span>

      <Button
        variant="ghost"
        size="sm"
        className="text-muted-foreground shrink-0"
        aria-expanded={showHistory}
        onClick={() => setShowHistory((v) => !v)}
      >
        <History className="size-4" />
        History
      </Button>

      {canWrite && (
        <div className="flex shrink-0 items-center gap-1">
          {canBookDefect && (
            <Button variant="outline" size="sm" disabled={pending} onClick={bookDefect}>
              <Wrench className="size-4" />
              Book defect
            </Button>
          )}
          {fault.status === "detected" && (
            <Button variant="ghost" size="sm" disabled={pending} onClick={() => set("investigating")}>
              <Search className="size-4" />
              Investigate
            </Button>
          )}
          {(fault.status === "detected" || fault.status === "investigating") && (
            <Button variant="ghost" size="sm" disabled={pending} onClick={() => set("cleared")}>
              <CheckCircle2 className="size-4" />
              Mark cleared
            </Button>
          )}
          {fault.status === "cleared" && (
            <Button variant="ghost" size="sm" disabled={pending} onClick={() => set("closed")}>
              <CheckCircle2 className="size-4" />
              Close
            </Button>
          )}
          {(fault.status === "cleared" || fault.status === "closed") && (
            <Button variant="ghost" size="sm" className="text-muted-foreground" disabled={pending} onClick={() => set("investigating")}>
              <RotateCcw className="size-4" />
              Reopen
            </Button>
          )}
        </div>
      )}
      </div>

      {showHistory && (
        <div className="border-border/60 border-t px-3 pb-2">
          <FaultHistoryPanel query={history} />
        </div>
      )}
    </div>
  )
}

// --- history ---------------------------------------------------------------

const HISTORY_ACTION_LABEL: Record<string, string> = {
  created: "Logged",
  status_changed: "Status changed",
  updated: "Edited",
  defect_booked: "Booked defect",
}

function FaultHistoryPanel({ query }: { query: ReturnType<typeof useFaultHistory> }) {
  if (query.isLoading) {
    return (
      <p className="text-muted-foreground flex items-center gap-1.5 py-2 text-xs">
        <Loader2 className="size-3 animate-spin" /> Loading history…
      </p>
    )
  }
  const entries = query.data?.history ?? []
  if (entries.length === 0) {
    return <p className="text-muted-foreground py-2 text-xs">No history recorded.</p>
  }
  return (
    <ol className="flex flex-col gap-1 py-2">
      {entries.map((e) => (
        <li key={e.id} className="flex items-baseline gap-2 text-xs">
          <span className="text-muted-foreground w-32 shrink-0">{formatWhen(e.at)}</span>
          <span className="font-medium">{historyActionText(e)}</span>
          <span className="text-muted-foreground">by {actorText(e)}</span>
        </li>
      ))}
    </ol>
  )
}

function historyActionText(e: FaultHistoryEntry): string {
  if (e.action === "status_changed") return `Status → ${e.snapshot?.status ?? "?"}`
  return HISTORY_ACTION_LABEL[e.action] ?? humanize(e.action)
}

function actorText(e: FaultHistoryEntry): string {
  return e.actor_name ?? e.actor_email ?? "system"
}

// --- page ------------------------------------------------------------------

export function FaultsPage() {
  const can = usePermission()
  const canWrite = can("faults:write")

  const [openOnly, setOpenOnly] = useState(true)
  const faults = useFaults({ openOnly })

  const [logging, setLogging] = useState(false)

  const rows = useMemo(() => faults.data?.faults ?? [], [faults.data])
  const openCount = rows.filter((f) => f.status !== "cleared" && f.status !== "closed").length

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="mx-auto flex max-w-4xl flex-col gap-4">
        <div className="flex items-center justify-between gap-2">
          <div>
            <h1 className="flex items-center gap-2 text-lg font-semibold">
              <ZapOff className="size-5" />
              Faults
            </h1>
            <p className="text-muted-foreground text-sm">
              Electrical fault events on the network — the cause behind an outage and the trigger for a repair. Log a
              fault, track it to cleared, and book the asset defect it left behind.
            </p>
          </div>
          {canWrite && (
            <Button size="sm" className="shrink-0" onClick={() => setLogging(true)}>
              <Plus className="size-4" />
              Log fault
            </Button>
          )}
        </div>

        <Card>
          <CardHeader className="pb-2">
            <div className="flex items-start justify-between gap-2">
              <div>
                <CardTitle className="flex items-center gap-2 text-sm">
                  <Zap className="size-4" />
                  Fault events
                </CardTitle>
                <CardDescription>
                  {rows.length === 0
                    ? openOnly
                      ? "No open faults."
                      : "No faults."
                    : `${rows.length} fault${rows.length === 1 ? "" : "s"}${!openOnly && openCount > 0 ? ` · ${openCount} still open` : ""}.`}
                </CardDescription>
              </div>
              <label className="text-muted-foreground flex shrink-0 items-center gap-1.5 text-xs">
                <input type="checkbox" checked={openOnly} onChange={(e) => setOpenOnly(e.target.checked)} />
                Open only
              </label>
            </div>
          </CardHeader>
          <CardContent className="pt-0">
            {faults.isPending ? (
              <div className="text-muted-foreground flex items-center gap-2 text-sm">
                <Loader2 className="size-4 animate-spin" />
                Loading faults…
              </div>
            ) : faults.isError ? (
              <p className="text-destructive text-sm">Could not load faults.</p>
            ) : rows.length > 0 ? (
              <div className="flex flex-col">
                {rows.map((f) => (
                  <FaultRow key={f.id} fault={f} canWrite={canWrite} />
                ))}
              </div>
            ) : (
              <p className="text-muted-foreground text-sm">
                No faults{openOnly ? " open" : ""} yet. Log one here, or raise one from an outage on the Outages page.
              </p>
            )}
          </CardContent>
        </Card>

        <p className="text-muted-foreground flex items-center gap-1.5 text-xs">
          <TriangleAlert className="size-3.5" />
          A fault raised from an outage inherits its predicted device and cause; booking a defect from a fault feeds it
          into the Works corrective-order loop.
        </p>
      </div>

      {logging && <LogFaultDialog onClose={() => setLogging(false)} />}
    </div>
  )
}
