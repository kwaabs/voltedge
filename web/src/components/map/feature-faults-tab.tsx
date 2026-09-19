import { useState } from "react"
import { CheckCircle2, Loader2, Plus, RotateCcw, Search, Wrench, Zap } from "lucide-react"

import { Button } from "@/components/ui/button"
import { usePermission } from "@/hooks/use-auth"
import {
  type FaultEvent,
  type FaultStatus,
  type FaultType,
  useBookDefectFromFault,
  useCreateFault,
  useFaults,
  useSetFaultStatus,
} from "@/hooks/use-faults"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

// The map attribute panel's "Faults" tab -- the Faults subsystem
// (api/internal/faults) brought to the asset itself, so an engineer selecting a
// feature on the map sees its fault history right there, and can log a fault,
// track it to cleared, and book the asset defect it left behind without leaving
// the map. Everything scopes to the selected feature via asset_class + asset_id
// (the same bare-ref pair /faults filters on). The full cross-asset board lives
// on the /faults page. Self-gated on faults:write, mirroring the Works tab.

const inputClass = "border-input bg-background w-full rounded-sm border px-2 py-1 text-sm"
const FAULT_TYPES: FaultType[] = ["SLG", "LL", "LLG", "LLL", "LLLG", "earth", "unknown"]

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

function humanize(s: string): string {
  return s.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase())
}

function shortId(id: string): string {
  return id.slice(0, 8)
}

function formatWhen(iso: string | null): string {
  if (!iso) return "—"
  return new Date(iso).toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" })
}

const FAULT_STATUS_BADGE: Record<FaultStatus, string> = {
  detected: "bg-red-500/15 text-red-600 dark:text-red-400",
  investigating: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  cleared: "bg-blue-500/15 text-blue-600 dark:text-blue-400",
  closed: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400",
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

function SectionHeader({ title, count, action }: { title: string; count: number; action?: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-2">
      <div className="flex items-center gap-1.5">
        <Zap className="text-muted-foreground size-3.5" />
        <span className="text-xs font-semibold tracking-wide uppercase">{title}</span>
        <span className="text-muted-foreground text-xs">({count})</span>
      </div>
      {action}
    </div>
  )
}

// --- new fault -------------------------------------------------------------

/** Log a fault on the selected asset (auto-anchored by asset_class + asset_id).
 * The electrical detail is optional and can be filled in as an investigation
 * proceeds via the /faults page. */
function NewFaultForm({
  assetClass,
  assetId,
  onDone,
}: {
  assetClass: string
  assetId: string
  onDone: () => void
}) {
  const create = useCreateFault()
  const [title, setTitle] = useState("")
  const [faultType, setFaultType] = useState<FaultType>("unknown")
  const [cause, setCause] = useState("")

  const canSave = !create.isPending && title.trim() !== ""

  const submit = () => {
    if (!canSave) return
    create.mutate(
      {
        asset_class: assetClass,
        asset_id: assetId,
        title: title.trim(),
        fault_type: faultType,
        ...(cause.trim() ? { cause: cause.trim() } : {}),
      },
      { onSuccess: onDone, onError: (err) => toastError(err, "Could not log this fault.") },
    )
  }

  return (
    <div className="flex flex-col gap-2 rounded-md border p-2">
      <input
        autoFocus
        value={title}
        onChange={(e) => setTitle(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && submit()}
        placeholder="What faulted on this asset?"
        className={inputClass}
      />
      <div className="flex gap-2">
        <select value={faultType} onChange={(e) => setFaultType(e.target.value as FaultType)} className={inputClass}>
          {FAULT_TYPES.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </select>
        <input
          value={cause}
          onChange={(e) => setCause(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
          placeholder="Cause (optional)"
          className={inputClass}
        />
      </div>
      <div className="flex items-center gap-2">
        <Button size="sm" onClick={submit} disabled={!canSave}>
          {create.isPending ? <Loader2 className="size-4 animate-spin" /> : <Plus className="size-4" />}
          Log fault
        </Button>
        <Button variant="ghost" size="sm" onClick={onDone}>
          Cancel
        </Button>
      </div>
    </div>
  )
}

function FaultRow({ fault, canWrite }: { fault: FaultEvent; canWrite: boolean }) {
  const setStatus = useSetFaultStatus()
  const book = useBookDefectFromFault()
  const busy = setStatus.isPending || book.isPending

  const set = (status: FaultStatus) =>
    setStatus.mutate({ id: fault.id, status }, { onError: (err) => toastError(err, "Could not update this fault.") })

  const bookDefect = () =>
    book.mutate({ faultId: fault.id }, { onError: (err) => toastError(err, "Could not book a defect from this fault.") })

  const isClosed = fault.status === "closed"
  const canBookDefect = canWrite && !fault.defect_id && !isClosed

  return (
    <div className="rounded-md border p-2 text-sm">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="truncate font-medium">{fault.title}</p>
          <p className="text-muted-foreground text-xs">
            {fault.fault_type ? `${fault.fault_type} · ` : ""}
            {fault.cause ? `${humanize(fault.cause)} · ` : ""}
            {formatWhen(fault.detected_at)}
            {fault.incident_id && <> · outage {shortId(fault.incident_id)}</>}
            {fault.defect_id && <> · defect {shortId(fault.defect_id)}</>}
          </p>
        </div>
        <Badge className={FAULT_STATUS_BADGE[fault.status]}>{humanize(fault.status)}</Badge>
      </div>
      {canWrite && (
        <div className="mt-2 flex flex-wrap gap-1">
          {canBookDefect && (
            <Button variant="outline" size="sm" className="h-6 px-2 text-xs" disabled={busy} onClick={bookDefect}>
              <Wrench className="size-3" />
              Book defect
            </Button>
          )}
          {fault.status === "detected" && (
            <Button variant="outline" size="sm" className="h-6 px-2 text-xs" disabled={busy} onClick={() => set("investigating")}>
              <Search className="size-3" />
              Investigate
            </Button>
          )}
          {(fault.status === "detected" || fault.status === "investigating") && (
            <Button variant="outline" size="sm" className="h-6 px-2 text-xs" disabled={busy} onClick={() => set("cleared")}>
              <CheckCircle2 className="size-3" />
              Clear
            </Button>
          )}
          {fault.status === "cleared" && (
            <Button variant="outline" size="sm" className="h-6 px-2 text-xs" disabled={busy} onClick={() => set("closed")}>
              <CheckCircle2 className="size-3" />
              Close
            </Button>
          )}
          {(fault.status === "cleared" || fault.status === "closed") && (
            <Button
              variant="ghost"
              size="sm"
              className="text-muted-foreground h-6 px-2 text-xs"
              disabled={busy}
              onClick={() => set("investigating")}
            >
              <RotateCcw className="size-3" />
              Reopen
            </Button>
          )}
        </div>
      )}
    </div>
  )
}

// --- the tab ---------------------------------------------------------------

export function FaultsTab() {
  const selected = useSelectionStore((s) => s.selected)
  const canWrite = usePermission()("faults:write")
  const [adding, setAdding] = useState(false)

  const assetClass = selected?.featureClassCode
  const assetId = selected?.featureId

  const faults = useFaults({ assetClass, assetId })

  if (!selected || !assetClass || !assetId) return null

  const rows = faults.data?.faults ?? []

  return (
    <div className="flex flex-col gap-2">
      <SectionHeader
        title="Faults"
        count={rows.length}
        action={
          canWrite && !adding ? (
            <Button variant="outline" size="sm" className="h-6 gap-1 px-2 text-xs" onClick={() => setAdding(true)}>
              <Plus className="size-3" />
              Log
            </Button>
          ) : undefined
        }
      />
      {adding && <NewFaultForm assetClass={assetClass} assetId={assetId} onDone={() => setAdding(false)} />}
      {faults.isPending ? (
        <div className="text-muted-foreground flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" />
          Loading…
        </div>
      ) : faults.isError ? (
        <p className="text-destructive text-sm">Could not load faults.</p>
      ) : rows.length === 0 ? (
        <p className="text-muted-foreground text-sm">No faults recorded on this asset.</p>
      ) : (
        rows.map((fault) => <FaultRow key={fault.id} fault={fault} canWrite={canWrite} />)
      )}
    </div>
  )
}
