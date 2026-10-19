import { useState } from "react"
import { Activity, Loader2, Plus } from "lucide-react"

import { OP_SOURCES, OP_TYPES } from "@/components/map/log-operation-dialog"
import { Button } from "@/components/ui/button"
import { usePermission } from "@/hooks/use-auth"
import { type OpSource, type OpType, useDeviceOperations, useRecordOperation } from "@/hooks/use-operations"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

// The map attribute panel's "Operations" tab -- the device-operation log
// (api/internal/deviceops) for the selected device, so an engineer sees a
// breaker/recloser/fuse/switch's own trip/open-close history right on the map and
// can log one inline (no modal). The full cross-device board lives on the
// /operations page. Shown only for operable (junction-role) classes; self-gated
// on operations:write, mirroring the Faults tab.

const inputClass = "border-input bg-background w-full rounded-sm border px-2 py-1 text-sm"

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

function formatWhen(iso: string): string {
  return new Date(iso).toLocaleString()
}

function humanize(s: string): string {
  return s.replace(/_/g, " ")
}

const OP_BADGE: Record<string, string> = {
  trip: "bg-destructive/10 text-destructive",
  lockout: "bg-destructive/15 text-destructive",
  reclose: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  open: "bg-blue-500/10 text-blue-600 dark:text-blue-400",
  close: "bg-green-600/10 text-green-700 dark:text-green-400",
}

/** Log an operation on the selected device, inline (device is implicit). When
 * and cause are optional; occurred_at defaults to now server-side. */
function NewOperationForm({
  deviceClass,
  deviceId,
  onDone,
}: {
  deviceClass: string
  deviceId: string
  onDone: () => void
}) {
  const record = useRecordOperation()
  const [opType, setOpType] = useState<OpType>("trip")
  const [source, setSource] = useState<OpSource>("crew")
  const [occurredAt, setOccurredAt] = useState("") // datetime-local; blank = now
  const [cause, setCause] = useState("")

  const canSave = !record.isPending

  function submit() {
    if (!canSave) return
    record.mutate(
      {
        device_class: deviceClass,
        device_id: deviceId,
        op_type: opType,
        source,
        occurred_at: occurredAt ? new Date(occurredAt).toISOString() : undefined,
        cause: cause.trim() || undefined,
      },
      { onSuccess: onDone, onError: (err) => toastError(err, "Could not log this operation.") },
    )
  }

  return (
    <div className="flex flex-col gap-2 rounded-md border p-2">
      <div className="flex gap-2">
        <select value={opType} onChange={(e) => setOpType(e.target.value as OpType)} className={inputClass}>
          {OP_TYPES.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
        <select value={source} onChange={(e) => setSource(e.target.value as OpSource)} className={inputClass}>
          {OP_SOURCES.map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      </div>
      <input
        type="datetime-local"
        value={occurredAt}
        onChange={(e) => setOccurredAt(e.target.value)}
        className={inputClass}
        title="Leave blank for now"
      />
      <input
        value={cause}
        onChange={(e) => setCause(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && submit()}
        placeholder="Cause (optional)"
        className={inputClass}
      />
      <div className="flex items-center gap-2">
        <Button size="sm" onClick={submit} disabled={!canSave}>
          {record.isPending ? <Loader2 className="size-4 animate-spin" /> : <Plus className="size-4" />}
          Log operation
        </Button>
        <Button variant="ghost" size="sm" onClick={onDone}>
          Cancel
        </Button>
      </div>
    </div>
  )
}

/**
 * The per-device Operations section: this device's operation timeline, newest
 * first, plus an inline write-gated log form. Store pattern (reads selection
 * itself, no props), the same shape FaultsTab uses. The panel only shows this tab
 * for an operable (junction-role) device, so there's always a real device here.
 */
export function OperationsTab() {
  const selected = useSelectionStore((s) => s.selected)
  const canWrite = usePermission()("operations:write")
  const [adding, setAdding] = useState(false)
  const deviceClass = selected?.featureClassCode
  const deviceId = selected?.featureId
  const { data, isPending, isError } = useDeviceOperations(deviceClass, deviceId)

  if (!selected || !deviceClass || !deviceId) return null

  const ops = data?.operations ?? []

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between">
        <span className="text-muted-foreground flex items-center gap-1.5 text-xs font-medium">
          <Activity className="size-3.5" />
          {ops.length} operation{ops.length === 1 ? "" : "s"} logged
        </span>
        {canWrite && !adding && (
          <Button variant="outline" size="sm" className="h-7" onClick={() => setAdding(true)}>
            <Plus className="size-3.5" />
            Log operation
          </Button>
        )}
      </div>

      {adding && <NewOperationForm deviceClass={deviceClass} deviceId={deviceId} onDone={() => setAdding(false)} />}

      {isPending ? (
        <div className="text-muted-foreground flex items-center gap-2 text-sm">
          <Loader2 className="size-4 animate-spin" /> Loading…
        </div>
      ) : isError ? (
        <p className="text-destructive text-sm">Could not load this device's operations.</p>
      ) : ops.length === 0 ? (
        <p className="text-muted-foreground text-sm">
          No operations logged for this device yet{canWrite ? " — use Log operation above." : "."}
        </p>
      ) : (
        <ul className="flex flex-col divide-y rounded-md border">
          {ops.map((op) => (
            <li key={op.id} className="flex items-start gap-2 px-3 py-2 text-sm">
              <span
                className={cn(
                  "mt-0.5 rounded px-1.5 py-0.5 text-xs font-medium capitalize",
                  OP_BADGE[op.op_type] ?? "bg-muted text-muted-foreground",
                )}
              >
                {humanize(op.op_type)}
              </span>
              <div className="min-w-0 flex-1">
                <div className="text-muted-foreground text-xs">{formatWhen(op.occurred_at)}</div>
                {op.cause && <div className="truncate">{op.cause}</div>}
                <div className="text-muted-foreground text-xs capitalize">via {humanize(op.source)}</div>
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
