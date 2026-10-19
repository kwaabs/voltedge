import { useState } from "react"

import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { type OpSource, type OpType, useRecordOperation } from "@/hooks/use-operations"
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"

export const OP_TYPES: { value: OpType; label: string }[] = [
  { value: "trip", label: "Trip" },
  { value: "lockout", label: "Lockout" },
  { value: "reclose", label: "Reclose" },
  { value: "open", label: "Open" },
  { value: "close", label: "Close" },
  { value: "earth", label: "Earth" },
  { value: "alarm", label: "Alarm" },
  { value: "other", label: "Other" },
]

export const OP_SOURCES: { value: OpSource; label: string }[] = [
  { value: "crew", label: "Crew" },
  { value: "scada", label: "SCADA" },
  { value: "relay", label: "Relay" },
  { value: "switching_order", label: "Switching order" },
  { value: "derived", label: "Derived" },
  { value: "other", label: "Other" },
]

/**
 * Record one device operation (POST /operations). When opened from a device's
 * panel the class/id are preset and locked (device given); from the operations
 * board they're entered. occurred_at defaults to now (a quick "it just operated"
 * log). On success the dialog closes and every operations query refreshes.
 */
export function LogOperationDialog({
  device,
  onClose,
}: {
  // Preset device (from a feature panel); omit to let the user type class + id.
  device?: { featureClass: string; featureId: string }
  onClose: () => void
}) {
  const record = useRecordOperation()
  const [deviceClass, setDeviceClass] = useState(device?.featureClass ?? "")
  const [deviceId, setDeviceId] = useState(device?.featureId ?? "")
  const [opType, setOpType] = useState<OpType>("trip")
  const [source, setSource] = useState<OpSource>("crew")
  const [occurredAt, setOccurredAt] = useState("") // datetime-local; blank = now
  const [cause, setCause] = useState("")
  const [notes, setNotes] = useState("")

  const canSave = deviceClass.trim().length > 0 && deviceId.trim().length > 0 && !record.isPending

  function handleSave() {
    record.mutate(
      {
        device_class: deviceClass.trim(),
        device_id: deviceId.trim(),
        op_type: opType,
        source,
        occurred_at: occurredAt ? new Date(occurredAt).toISOString() : undefined,
        cause: cause.trim() || undefined,
        notes: notes.trim() || undefined,
      },
      {
        onSuccess: onClose,
        onError: (err) =>
          useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not log the operation."),
      },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Log device operation"
      description="Record that a protective/switching device operated — a trip, lockout, reclose, or manual open/close."
      onSubmit={handleSave}
      submitLabel="Log"
      canSubmit={canSave}
      saving={record.isPending}
    >
      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Device class <span className="text-destructive">*</span>
          </label>
          <input
            className={inputClass}
            value={deviceClass}
            onChange={(e) => setDeviceClass(e.target.value)}
            placeholder="e.g. MV_RECLOSER"
            disabled={Boolean(device)}
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Device id <span className="text-destructive">*</span>
          </label>
          <input
            className={inputClass}
            value={deviceId}
            onChange={(e) => setDeviceId(e.target.value)}
            placeholder="feature id"
            disabled={Boolean(device)}
          />
        </div>
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Operation</label>
          <select className={inputClass} value={opType} onChange={(e) => setOpType(e.target.value as OpType)}>
            {OP_TYPES.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Source</label>
          <select className={inputClass} value={source} onChange={(e) => setSource(e.target.value as OpSource)}>
            {OP_SOURCES.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </div>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>When</label>
        <input
          type="datetime-local"
          className={inputClass}
          value={occurredAt}
          onChange={(e) => setOccurredAt(e.target.value)}
        />
        <span className="text-muted-foreground text-xs">Leave blank for now.</span>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Cause</label>
        <input
          className={inputClass}
          value={cause}
          onChange={(e) => setCause(e.target.value)}
          placeholder="e.g. overcurrent, earth fault, manual"
        />
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Notes</label>
        <input className={inputClass} value={notes} onChange={(e) => setNotes(e.target.value)} />
      </div>
    </FormDialog>
  )
}
