import { useMemo, useState } from "react"
import { Loader2, Pencil, Plus, Trash2, TriangleAlert } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import {
  type FaultDimension,
  type FaultMode,
  type FaultSeverity,
  useCreateFaultMode,
  useDeleteFaultMode,
  useFaultModes,
  useUpdateFaultMode,
} from "@/hooks/use-faults"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"
const DIMENSIONS: FaultDimension[] = [
  "electrical",
  "mechanical",
  "thermal",
  "environmental",
  "ageing",
  "operational",
  "other",
]
const SEVERITIES: FaultSeverity[] = ["low", "medium", "high", "critical"]

const SEVERITY_BADGE: Record<FaultSeverity, string> = {
  low: "bg-slate-500/15 text-slate-600 dark:text-slate-400",
  medium: "bg-amber-500/15 text-amber-600 dark:text-amber-400",
  high: "bg-orange-500/15 text-orange-600 dark:text-orange-400",
  critical: "bg-red-500/15 text-red-600 dark:text-red-400",
}

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

function FaultModeDialog({
  featureClass,
  existing,
  onClose,
}: {
  featureClass: string
  existing: FaultMode | null
  onClose: () => void
}) {
  const isEdit = existing !== null
  const [code, setCode] = useState(existing?.code ?? "")
  const [label, setLabel] = useState(existing?.label ?? "")
  const [dimension, setDimension] = useState<FaultDimension>(existing?.dimension ?? "electrical")
  const [severity, setSeverity] = useState<FaultSeverity>(existing?.severity ?? "medium")
  const [typicalCause, setTypicalCause] = useState(existing?.typical_cause ?? "")
  const [effect, setEffect] = useState(existing?.effect ?? "")
  const [sortOrder, setSortOrder] = useState(String(existing?.sort_order ?? 0))
  const [active, setActive] = useState(existing?.active ?? true)

  const create = useCreateFaultMode()
  const update = useUpdateFaultMode()
  const saving = create.isPending || update.isPending
  const canSave = label.trim().length > 0 && (isEdit || code.trim().length > 0) && !saving

  function handleSave() {
    const body = {
      label: label.trim(),
      dimension,
      severity,
      typical_cause: typicalCause.trim() || null,
      effect: effect.trim() || null,
      sort_order: Number(sortOrder) || 0,
      active,
    }
    if (isEdit) {
      update.mutate(
        { id: existing.id, body },
        { onSuccess: onClose, onError: (err) => toastError(err, "Could not update the mode.") },
      )
    } else {
      create.mutate(
        { ...body, feature_class_code: featureClass, code: code.trim().toUpperCase() },
        { onSuccess: onClose, onError: (err) => toastError(err, "Could not create the mode.") },
      )
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title={isEdit ? "Edit failure mode" : "New failure mode"}
      description={`A failure mode for ${featureClass}. Code is the stored value; the rest is the FMEA payload shown in the fault form.`}
      onSubmit={handleSave}
      submitLabel={isEdit ? "Save" : "Create"}
      canSubmit={canSave}
      saving={saving}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Code <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={code}
          onChange={(e) => setCode(e.target.value)}
          placeholder="e.g. WINDING_INSULATION_FAILURE"
          disabled={isEdit}
          autoFocus={!isEdit}
        />
        {isEdit && <span className="text-muted-foreground text-xs">Fixed once created.</span>}
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Label <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={label}
          onChange={(e) => setLabel(e.target.value)}
          placeholder="e.g. Winding insulation failure"
          autoFocus={isEdit}
        />
      </div>

      <div className="grid grid-cols-2 gap-2">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Dimension</label>
          <select className={inputClass} value={dimension} onChange={(e) => setDimension(e.target.value as FaultDimension)}>
            {DIMENSIONS.map((d) => (
              <option key={d} value={d}>
                {d}
              </option>
            ))}
          </select>
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Severity</label>
          <select className={inputClass} value={severity} onChange={(e) => setSeverity(e.target.value as FaultSeverity)}>
            {SEVERITIES.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
        </div>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Typical cause</label>
        <input
          className={inputClass}
          value={typicalCause}
          onChange={(e) => setTypicalCause(e.target.value)}
          placeholder="What usually brings it about"
        />
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Effect</label>
        <input
          className={inputClass}
          value={effect}
          onChange={(e) => setEffect(e.target.value)}
          placeholder="What it does to the asset / network"
        />
      </div>

      <div className="flex items-end gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Sort order</label>
          <input
            className={cn(inputClass, "w-24")}
            type="number"
            value={sortOrder}
            onChange={(e) => setSortOrder(e.target.value)}
          />
        </div>
        <label className="flex items-center gap-2 pb-1.5 text-sm">
          <input type="checkbox" checked={active} onChange={(e) => setActive(e.target.checked)} />
          Active (shown in the picker)
        </label>
      </div>
    </FormDialog>
  )
}

/**
 * Admin editor for the per-feature-class failure-mode (FMEA) catalog -- the
 * taxonomy behind the fault form's class-aware "cause" picker. Pick a feature
 * class, then add/edit/retire the faults that can happen on it. Gated on
 * metadata:write (the route guards it); the GET is a plain faults:read.
 */
export function AdminFaultModesPage() {
  const { data: classesData } = useFeatureClasses()
  const classes = useMemo(
    () => [...(classesData?.feature_classes ?? [])].sort((a, b) => a.name.localeCompare(b.name)),
    [classesData],
  )
  const [featureClass, setFeatureClass] = useState("")
  const modes = useFaultModes(featureClass || undefined, true)
  const [dialog, setDialog] = useState<FaultMode | "new" | null>(null)
  const [deleting, setDeleting] = useState<FaultMode | null>(null)
  const del = useDeleteFaultMode()

  const rows = modes.data?.fault_modes ?? []
  const nameFor = (code: string) => classes.find((c) => c.code === code)?.name ?? code

  return (
    <div className="mx-auto flex w-full max-w-4xl flex-col gap-4 p-4">
      <div>
        <h1 className="flex items-center gap-2 text-lg font-semibold">
          <TriangleAlert className="size-5" /> Fault-mode catalog
        </h1>
        <p className="text-muted-foreground text-sm">
          The failure modes that can happen on each asset class — the fault form's class-aware cause picker. Each
          mode carries a failure dimension, a typical cause, the effect and a severity.
        </p>
      </div>

      <Card>
        <CardHeader className="flex flex-row items-end justify-between gap-2">
          <div className="flex flex-col gap-1">
            <CardTitle className="text-base">Feature class</CardTitle>
            <CardDescription>Choose a class to view and edit its modes.</CardDescription>
          </div>
          <div className="flex items-end gap-2">
            <select className={cn(inputClass, "w-64")} value={featureClass} onChange={(e) => setFeatureClass(e.target.value)}>
              <option value="">Select a class…</option>
              {classes.map((c) => (
                <option key={c.code} value={c.code}>
                  {c.name}
                </option>
              ))}
            </select>
            {featureClass && (
              <Button size="sm" onClick={() => setDialog("new")}>
                <Plus className="size-4" /> Add mode
              </Button>
            )}
          </div>
        </CardHeader>
        {featureClass && (
          <CardContent>
            {modes.isPending ? (
              <div className="text-muted-foreground flex items-center gap-2 text-sm">
                <Loader2 className="size-4 animate-spin" /> Loading…
              </div>
            ) : rows.length === 0 ? (
              <p className="text-muted-foreground text-sm">
                No modes catalogued for {nameFor(featureClass)} yet. Add the first one.
              </p>
            ) : (
              <div className="overflow-hidden rounded-md border">
                <table className="w-full border-collapse text-sm">
                  <thead className="bg-muted/50 text-muted-foreground text-xs">
                    <tr>
                      <th className="px-2 py-1.5 text-left font-medium">Label</th>
                      <th className="px-2 py-1.5 text-left font-medium">Dimension</th>
                      <th className="px-2 py-1.5 text-left font-medium">Severity</th>
                      <th className="px-2 py-1.5 text-left font-medium">Effect</th>
                      <th className="px-2 py-1.5 text-right font-medium">#</th>
                      <th className="px-2 py-1.5"></th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((m) => (
                      <tr key={m.id} className={cn("border-t", !m.active && "opacity-50")}>
                        <td className="px-2 py-1.5">
                          <div className="font-medium">{m.label}</div>
                          <div className="text-muted-foreground font-mono text-xs">{m.code}</div>
                        </td>
                        <td className="px-2 py-1.5">{m.dimension}</td>
                        <td className="px-2 py-1.5">
                          <span className={cn("rounded px-1.5 py-0.5 text-xs font-medium capitalize", SEVERITY_BADGE[m.severity])}>
                            {m.severity}
                          </span>
                        </td>
                        <td className="text-muted-foreground px-2 py-1.5 text-xs">{m.effect ?? "—"}</td>
                        <td className="px-2 py-1.5 text-right tabular-nums">{m.sort_order}</td>
                        <td className="px-2 py-1.5">
                          <div className="flex justify-end gap-1">
                            <Button variant="ghost" size="icon" className="size-7" onClick={() => setDialog(m)}>
                              <Pencil className="size-3.5" />
                            </Button>
                            <Button variant="ghost" size="icon" className="size-7" onClick={() => setDeleting(m)}>
                              <Trash2 className="size-3.5" />
                            </Button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </CardContent>
        )}
      </Card>

      {dialog && featureClass && (
        <FaultModeDialog
          featureClass={featureClass}
          existing={dialog === "new" ? null : dialog}
          onClose={() => setDialog(null)}
        />
      )}

      <ConfirmDeleteDialog
        open={deleting !== null}
        title={`Delete ${deleting?.label ?? ""}?`}
        description="This removes the mode from the picker. Faults already logged with it keep their value."
        confirmLabel="Delete"
        onOpenChange={(open) => !open && setDeleting(null)}
        onConfirm={async () => {
          if (!deleting) return
          try {
            await del.mutateAsync(deleting.id)
            setDeleting(null)
          } catch (err) {
            toastError(err, "Could not delete the mode.")
          }
        }}
      />
    </div>
  )
}
