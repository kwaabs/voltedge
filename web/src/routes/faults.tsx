import { useMemo, useState } from "react"
import { CheckCircle2, History, Loader2, Plus, RotateCcw, Search, TriangleAlert, Wrench, X, Zap, ZapOff } from "lucide-react"

import { FormDialog } from "@/components/admin/form-dialog"
import { AssetIdField } from "@/components/map/asset-id-field"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { usePermission } from "@/hooks/use-auth"
import {
  type FaultEvent,
  FAULT_TYPE_LABELS,
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
import { cn } from "@/lib/utils"
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
                {t} ({FAULT_TYPE_LABELS[t]})
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

function FaultRow({ fault, selected, onSelect }: { fault: FaultEvent; selected: boolean; onSelect: (f: FaultEvent) => void }) {
  const hasLoc = fault.longitude != null && fault.latitude != null
  const asset = assetLabel(fault.asset_class, fault.asset_id)

  return (
    <tr
      onClick={() => onSelect(fault)}
      className={cn(
        "border-border/50 cursor-pointer border-b align-top last:border-0",
        selected ? "bg-primary/5" : "hover:bg-muted/40",
      )}
    >
      <td className="py-2 pr-3">
        <Badge text={fault.status} cls={FAULT_STATUS_BADGE[fault.status]} />
      </td>
      <td className="py-2 pr-3">
        <span className="text-sm font-medium">{fault.title}</span>
        {fault.fault_type && <span className="text-muted-foreground ml-2 font-mono text-xs">{fault.fault_type}</span>}
      </td>
      <td className="text-muted-foreground py-2 pr-3 text-xs">
        {asset ?? (hasLoc ? "pinned" : fault.incident_id ? `outage ${shortId(fault.incident_id)}` : "—")}
        {fault.cause && <> · {humanize(fault.cause)}</>}
        {fault.fault_current_a != null && <> · {fault.fault_current_a} A</>}
        {fault.incident_id && asset && <> · outage {shortId(fault.incident_id)}</>}
        {fault.defect_id && <> · defect {shortId(fault.defect_id)}</>}
      </td>
      <td className="text-muted-foreground py-2 pr-3 text-xs whitespace-nowrap">{formatWhen(fault.detected_at)}</td>
    </tr>
  )
}

// --- detail panel ----------------------------------------------------------

/** The right-hand master-detail panel: the selected fault's meta, its status
 * actions, and its full history timeline. Opens when a fault row is clicked. */
function FaultDetailPanel({ fault, canWrite, onClose }: { fault: FaultEvent; canWrite: boolean; onClose: () => void }) {
  const setStatus = useSetFaultStatus()
  const book = useBookDefectFromFault()
  const pending = setStatus.isPending || book.isPending
  const history = useFaultHistory(fault.id, true)

  const hasAsset = !!fault.asset_class && !!fault.asset_id
  const hasLoc = fault.longitude != null && fault.latitude != null
  const isClosed = fault.status === "closed"
  const canBookDefect = canWrite && !fault.defect_id && (hasAsset || hasLoc) && !isClosed
  const asset = assetLabel(fault.asset_class, fault.asset_id)

  const set = (status: FaultStatus) =>
    setStatus.mutate({ id: fault.id, status }, { onError: (err) => toastError(err, "Could not update the fault.") })
  const bookDefect = () =>
    book.mutate({ faultId: fault.id }, { onError: (err) => toastError(err, "Could not book a defect from this fault.") })

  return (
    <Card className="flex max-h-[calc(100svh-9rem)] flex-col lg:sticky lg:top-4 lg:self-start">
      <CardHeader className="pb-2">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <div className="mb-1 flex flex-wrap items-center gap-2">
              <Badge text={fault.status} cls={FAULT_STATUS_BADGE[fault.status]} />
              {fault.fault_type && (
                <span className="text-muted-foreground font-mono text-xs">
                  {fault.fault_type} · {FAULT_TYPE_LABELS[fault.fault_type]}
                </span>
              )}
            </div>
            <CardTitle className="text-sm">{fault.title}</CardTitle>
          </div>
          <button type="button" onClick={onClose} className="text-muted-foreground hover:text-foreground shrink-0" aria-label="Close">
            <X className="size-4" />
          </button>
        </div>
      </CardHeader>
      <CardContent className="min-h-0 flex-1 space-y-4 overflow-y-auto pt-0 text-sm">
        <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
          <Meta label="Anchor" value={asset ?? (hasLoc ? "Location pin" : fault.incident_id ? `Outage ${shortId(fault.incident_id)}` : "—")} />
          {fault.cause && <Meta label="Cause" value={humanize(fault.cause)} />}
          {fault.fault_current_a != null && <Meta label="Fault current" value={`${fault.fault_current_a} A`} />}
          {fault.clearing_time_ms != null && <Meta label="Clearing time" value={`${fault.clearing_time_ms} ms`} />}
          {fault.detected_by && <Meta label="Detected by" value={fault.detected_by} />}
          <Meta label="Detected" value={formatWhen(fault.detected_at)} />
          {fault.incident_id && <Meta label="Outage" value={shortId(fault.incident_id)} />}
          {fault.defect_id && <Meta label="Defect" value={shortId(fault.defect_id)} />}
        </dl>

        {fault.description && <p className="text-muted-foreground text-xs">{fault.description}</p>}

        {canWrite && (
          <div className="flex flex-wrap gap-1.5">
            {canBookDefect && (
              <Button variant="outline" size="sm" disabled={pending} onClick={bookDefect}>
                <Wrench className="mr-1 size-3.5" />
                Book defect
              </Button>
            )}
            {fault.status === "detected" && (
              <Button variant="outline" size="sm" disabled={pending} onClick={() => set("investigating")}>
                <Search className="mr-1 size-3.5" />
                Investigate
              </Button>
            )}
            {(fault.status === "detected" || fault.status === "investigating") && (
              <Button variant="outline" size="sm" disabled={pending} onClick={() => set("cleared")}>
                <CheckCircle2 className="mr-1 size-3.5" />
                Mark cleared
              </Button>
            )}
            {fault.status === "cleared" && (
              <Button variant="outline" size="sm" disabled={pending} onClick={() => set("closed")}>
                <CheckCircle2 className="mr-1 size-3.5" />
                Close
              </Button>
            )}
            {(fault.status === "cleared" || fault.status === "closed") && (
              <Button variant="ghost" size="sm" disabled={pending} onClick={() => set("investigating")}>
                <RotateCcw className="mr-1 size-3.5" />
                Reopen
              </Button>
            )}
          </div>
        )}

        <div>
          <div className="text-muted-foreground mb-1 flex items-center gap-1.5 text-[11px] font-semibold tracking-wide uppercase">
            <History className="size-3.5" /> Timeline
          </div>
          <FaultHistoryPanel query={history} />
        </div>
      </CardContent>
    </Card>
  )
}

function Meta({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="tabular-nums">{value}</dd>
    </>
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
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const rows = useMemo(() => faults.data?.faults ?? [], [faults.data])
  const openCount = rows.filter((f) => f.status !== "cleared" && f.status !== "closed").length
  // Resolve the open panel from the live rows so it tracks edits and vanishes if
  // the selected fault is filtered out (e.g. toggling "Open only").
  const selected = rows.find((f) => f.id === selectedId) ?? null

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex flex-col gap-4">
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

        <div className={cn("grid items-start gap-4", selected ? "lg:grid-cols-[1fr_28rem]" : "grid-cols-1")}>
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
                <table className="w-full">
                  <thead>
                    <tr className="text-muted-foreground border-border border-b text-left text-xs">
                      <th className="py-1.5 pr-3 font-medium">Status</th>
                      <th className="py-1.5 pr-3 font-medium">Fault</th>
                      <th className="py-1.5 pr-3 font-medium">Asset / cause</th>
                      <th className="py-1.5 pr-3 font-medium">Detected</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((f) => (
                      <FaultRow key={f.id} fault={f} selected={f.id === selectedId} onSelect={(fx) => setSelectedId(fx.id)} />
                    ))}
                  </tbody>
                </table>
              ) : (
                <p className="text-muted-foreground text-sm">
                  No faults{openOnly ? " open" : ""} yet. Log one here, or raise one from an outage on the Outages page.
                </p>
              )}
            </CardContent>
          </Card>

          {selected && <FaultDetailPanel fault={selected} canWrite={canWrite} onClose={() => setSelectedId(null)} />}
        </div>

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
