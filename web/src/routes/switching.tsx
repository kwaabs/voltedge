import { useState } from "react"
import { ChevronDown, ChevronUp, ListChecks, Loader2, Pencil, Plus, Trash2 } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { OperationsTabs } from "@/components/layout/operations-tabs"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { usePermission } from "@/hooks/use-auth"
import { type FeatureSearchResult, useFeatureNameSearch } from "@/hooks/use-feature-name-search"
import {
  type CreatePlannedSwitchingBody,
  type StepAction,
  type StepPhase,
  type SwitchingOrder,
  type SwitchingStep,
  type SwitchingStepBody,
  useAddSwitchingStep,
  useCancelSwitchingOrder,
  useCreatePlannedSwitching,
  useDeleteSwitchingStep,
  useExecuteStep,
  usePlannedSwitching,
  useReorderSwitchingSteps,
  useUpdatePlannedSwitching,
  useUpdateSwitchingStep,
} from "@/hooks/use-oms"
import { ApiError } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useToastStore } from "@/stores/toast-store"

const PHASES: StepPhase[] = ["isolate", "restore"]
const ACTIONS: StepAction[] = ["open", "close", "earth", "verify", "note"]

const labelClass = "text-xs font-medium"

function toastErr(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

function shortId(id: string): string {
  return id.length > 8 ? id.slice(0, 8) : id
}

function statusClass(status: SwitchingOrder["status"]): string {
  switch (status) {
    case "completed":
      return "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400"
    case "in_progress":
      return "bg-amber-500/15 text-amber-700 dark:text-amber-400"
    case "cancelled":
      return "bg-muted text-muted-foreground"
    default:
      return "bg-primary/10 text-primary"
  }
}

const actionClass: Record<StepAction, string> = {
  open: "bg-destructive/10 text-destructive",
  close: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400",
  earth: "bg-amber-500/15 text-amber-700 dark:text-amber-400",
  verify: "bg-sky-500/15 text-sky-700 dark:text-sky-400",
  note: "bg-muted text-muted-foreground",
}

/** Find the device a step operates (optional unless the action is open/close). */
function DevicePicker({ onPick }: { onPick: (r: FeatureSearchResult) => void }) {
  const [q, setQ] = useState("")
  const { results, loading } = useFeatureNameSearch(q)
  return (
    <div className="flex flex-col gap-1">
      <input
        className={inputClass}
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder="Search a device by name…"
      />
      {q.trim().length >= 2 && (
        <div className="max-h-40 overflow-auto rounded-md border">
          {loading ? (
            <div className="text-muted-foreground p-2 text-xs">Searching…</div>
          ) : results.length === 0 ? (
            <div className="text-muted-foreground p-2 text-xs">No matches.</div>
          ) : (
            results.map((r) => (
              <button
                key={`${r.featureClassCode}:${r.featureId}`}
                type="button"
                onClick={() => onPick(r)}
                className="hover:bg-muted/60 block w-full px-2 py-1.5 text-left text-xs"
              >
                <span className="font-medium">{r.name}</span>{" "}
                <span className="text-muted-foreground">({r.className})</span>
              </button>
            ))
          )}
        </div>
      )}
    </div>
  )
}

/** Create-program dialog: a title, an optional booked window, and an optional
 * target device that seeds the standard isolate+restore steps. */
function CreateDialog({ onClose }: { onClose: () => void }) {
  const [reference, setReference] = useState("")
  const [purpose, setPurpose] = useState("")
  const [plannedStart, setPlannedStart] = useState("")
  const [plannedEnd, setPlannedEnd] = useState("")
  const [device, setDevice] = useState<{ cls: string; id: string; label: string } | null>(null)
  const [saving, setSaving] = useState(false)
  const create = useCreatePlannedSwitching()

  async function handleSave() {
    setSaving(true)
    try {
      const body: CreatePlannedSwitchingBody = {
        reference: reference.trim(),
        purpose: purpose.trim() || null,
        planned_start: plannedStart ? new Date(plannedStart).toISOString() : null,
        planned_end: plannedEnd ? new Date(plannedEnd).toISOString() : null,
      }
      if (device) body.target_device = { feature_class: device.cls, feature_id: device.id }
      await create.mutateAsync(body)
      onClose()
    } catch (err) {
      toastErr(err, "Could not create this switching program.")
    } finally {
      setSaving(false)
    }
  }

  // A title is required unless a device is chosen (the server titles it then).
  const canSubmit = reference.trim() !== "" || device !== null

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="New switching program"
      description="A planned open/verify/earth/close program, not tied to any incident."
      onSubmit={handleSave}
      submitLabel="Create"
      canSubmit={canSubmit}
      saving={saving}
      className="max-w-lg"
    >
      <div className="flex flex-col gap-3">
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Title / reference</label>
          <input
            className={inputClass}
            value={reference}
            onChange={(e) => setReference(e.target.value)}
            placeholder="e.g. Feeder 3 maintenance isolation"
            autoFocus
          />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Purpose</label>
          <input className={inputClass} value={purpose} onChange={(e) => setPurpose(e.target.value)} placeholder="Why this switching is planned" />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Planned start</label>
            <input type="datetime-local" className={inputClass} value={plannedStart} onChange={(e) => setPlannedStart(e.target.value)} />
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Planned end</label>
            <input type="datetime-local" className={inputClass} value={plannedEnd} onChange={(e) => setPlannedEnd(e.target.value)} />
          </div>
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Seed from a device (optional)</label>
          {device ? (
            <div className="bg-muted/40 flex items-center justify-between gap-2 rounded-md border px-3 py-1.5 text-sm">
              <span>
                Isolate <span className="font-medium">{device.label}</span>
              </span>
              <button type="button" className="text-primary text-xs hover:underline" onClick={() => setDevice(null)}>
                Clear
              </button>
            </div>
          ) : (
            <DevicePicker onPick={(r) => setDevice({ cls: r.featureClassCode, id: r.featureId, label: `${r.name} (${r.className})` })} />
          )}
          <p className="text-muted-foreground text-xs">
            Picking a device pre-fills the standard open → verify → earth → close steps; leave blank to start from an empty program.
          </p>
        </div>
      </div>
    </FormDialog>
  )
}

/** Add/edit-step dialog. */
function StepDialog({ orderId, existing, onClose }: { orderId: string; existing: SwitchingStep | null; onClose: () => void }) {
  const [phase, setPhase] = useState<StepPhase>(existing?.phase ?? "isolate")
  const [action, setAction] = useState<StepAction>(existing?.action ?? "open")
  const [instruction, setInstruction] = useState(existing?.instruction ?? "")
  const [device, setDevice] = useState<{ cls: string; id: string; label: string } | null>(
    existing?.device_class && existing.device_id
      ? { cls: existing.device_class, id: existing.device_id, label: `${existing.device_class} ${shortId(existing.device_id)}` }
      : null,
  )
  const [saving, setSaving] = useState(false)
  const add = useAddSwitchingStep()
  const update = useUpdateSwitchingStep()

  const needsDevice = action === "open" || action === "close"

  async function handleSave() {
    if (instruction.trim() === "") {
      useToastStore.getState().pushError("An instruction is required.")
      return
    }
    if (needsDevice && !device) {
      useToastStore.getState().pushError(`An ${action} step must name the device it operates.`)
      return
    }
    setSaving(true)
    const step: SwitchingStepBody = {
      phase,
      action,
      instruction: instruction.trim(),
      device_class: device?.cls ?? null,
      device_id: device?.id ?? null,
    }
    try {
      if (existing) await update.mutateAsync({ stepId: existing.id, step })
      else await add.mutateAsync({ orderId, step })
      onClose()
    } catch (err) {
      toastErr(err, "Could not save this step.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title={existing ? `Edit step ${existing.step_no}` : "Add step"}
      onSubmit={handleSave}
      submitLabel={existing ? "Save" : "Add"}
      canSubmit={instruction.trim() !== ""}
      saving={saving}
      className="max-w-lg"
    >
      <div className="flex flex-col gap-3">
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Phase</label>
            <select className={inputClass} value={phase} onChange={(e) => setPhase(e.target.value as StepPhase)}>
              {PHASES.map((p) => (
                <option key={p} value={p}>
                  {p}
                </option>
              ))}
            </select>
          </div>
          <div className="flex flex-col gap-1">
            <label className={labelClass}>Action</label>
            <select className={inputClass} value={action} onChange={(e) => setAction(e.target.value as StepAction)}>
              {ACTIONS.map((a) => (
                <option key={a} value={a}>
                  {a}
                </option>
              ))}
            </select>
          </div>
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>Instruction</label>
          <input className={inputClass} value={instruction} onChange={(e) => setInstruction(e.target.value)} placeholder="The step the crew reads and performs" />
        </div>
        <div className="flex flex-col gap-1">
          <label className={labelClass}>
            Device {needsDevice && <span className="text-destructive">*</span>}
            {!needsDevice && <span className="text-muted-foreground"> (optional)</span>}
          </label>
          {device ? (
            <div className="bg-muted/40 flex items-center justify-between gap-2 rounded-md border px-3 py-1.5 text-sm">
              <span className="font-medium">{device.label}</span>
              <button type="button" className="text-primary text-xs hover:underline" onClick={() => setDevice(null)}>
                Clear
              </button>
            </div>
          ) : (
            <DevicePicker onPick={(r) => setDevice({ cls: r.featureClassCode, id: r.featureId, label: `${r.name} (${r.className})` })} />
          )}
        </div>
      </div>
    </FormDialog>
  )
}

/** One step row in the selected program: number, phase/action badges, the
 * instruction + device, an executed tick, and (when editable) edit/delete and
 * move up/down. */
function StepRow({
  step,
  index,
  total,
  editable,
  canWrite,
  onEdit,
  onDelete,
  onMove,
}: {
  step: SwitchingStep
  index: number
  total: number
  editable: boolean
  canWrite: boolean
  onEdit: () => void
  onDelete: () => void
  onMove: (dir: -1 | 1) => void
}) {
  const execute = useExecuteStep()
  const done = step.executed_at !== null

  return (
    <tr className="border-border/60 border-t [&>td]:px-2 [&>td]:py-1.5 [&>td]:align-top">
      <td className="text-muted-foreground tabular-nums">{step.step_no}</td>
      <td>
        {canWrite ? (
          <input
            type="checkbox"
            checked={done}
            title={done ? "Mark outstanding" : "Mark done"}
            onChange={(e) => execute.mutate({ stepId: step.id, executed: e.target.checked })}
            className="mt-0.5 size-4"
          />
        ) : done ? (
          <span className="text-emerald-600">✓</span>
        ) : null}
      </td>
      <td>
        <span className="text-muted-foreground text-[10px] uppercase">{step.phase}</span>
      </td>
      <td>
        <span className={cn("rounded px-1.5 py-0.5 text-[10px] font-medium uppercase", actionClass[step.action])}>{step.action}</span>
      </td>
      <td className="w-full">
        <div className={cn(done && "text-muted-foreground line-through")}>{step.instruction}</div>
        {step.device_class && step.device_id && (
          <div className="text-muted-foreground font-mono text-[11px]">
            {step.device_class} {shortId(step.device_id)}
          </div>
        )}
      </td>
      {canWrite && (
        <td className="whitespace-nowrap text-right">
          {editable && (
            <div className="flex justify-end">
              <Button variant="ghost" size="icon" className="size-7" title="Move up" disabled={index === 0} onClick={() => onMove(-1)}>
                <ChevronUp className="size-4" />
              </Button>
              <Button variant="ghost" size="icon" className="size-7" title="Move down" disabled={index === total - 1} onClick={() => onMove(1)}>
                <ChevronDown className="size-4" />
              </Button>
              <Button variant="ghost" size="icon" className="size-7" title="Edit" disabled={done} onClick={onEdit}>
                <Pencil className="size-4" />
              </Button>
              <Button variant="ghost" size="icon" className="text-destructive size-7" title="Delete" disabled={done} onClick={onDelete}>
                <Trash2 className="size-4" />
              </Button>
            </div>
          )}
        </td>
      )}
    </tr>
  )
}

/** The selected program: header actions + the step table + add-step. */
function ProgramDetail({ order, canWrite }: { order: SwitchingOrder; canWrite: boolean }) {
  const [addingStep, setAddingStep] = useState(false)
  const [editingStep, setEditingStep] = useState<SwitchingStep | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<SwitchingStep | null>(null)
  const reorder = useReorderSwitchingSteps()
  const del = useDeleteSwitchingStep()
  const issue = useUpdatePlannedSwitching()
  const cancel = useCancelSwitchingOrder()

  const editable = order.status !== "completed" && order.status !== "cancelled"
  const steps = order.steps

  async function move(index: number, dir: -1 | 1) {
    const next = [...steps]
    const target = index + dir
    if (target < 0 || target >= next.length) return
    ;[next[index], next[target]] = [next[target], next[index]]
    try {
      await reorder.mutateAsync({ orderId: order.id, stepIds: next.map((s) => s.id) })
    } catch (err) {
      toastErr(err, "Could not reorder the steps.")
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex items-start justify-between gap-3 border-b px-4 py-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h2 className="truncate font-semibold">{order.reference ?? "Untitled program"}</h2>
            <span className={cn("rounded px-1.5 py-0.5 text-[10px] font-medium uppercase", statusClass(order.status))}>
              {order.status.replace("_", " ")}
            </span>
          </div>
          {order.purpose && <p className="text-muted-foreground text-sm">{order.purpose}</p>}
          {(order.planned_start || order.planned_end) && (
            <p className="text-muted-foreground text-xs">
              {order.planned_start ? new Date(order.planned_start).toLocaleString() : "—"} →{" "}
              {order.planned_end ? new Date(order.planned_end).toLocaleString() : "—"}
            </p>
          )}
        </div>
        {canWrite && editable && (
          <div className="flex shrink-0 gap-2">
            {order.status === "draft" && (
              <Button
                size="sm"
                variant="outline"
                onClick={async () => {
                  try {
                    await issue.mutateAsync({ orderId: order.id, patch: { status: "issued" } })
                  } catch (err) {
                    toastErr(err, "Could not issue this program.")
                  }
                }}
              >
                Issue
              </Button>
            )}
            <Button
              size="sm"
              variant="outline"
              className="text-destructive"
              onClick={async () => {
                try {
                  await cancel.mutateAsync({ orderId: order.id })
                } catch (err) {
                  toastErr(err, "Could not cancel this program.")
                }
              }}
            >
              Cancel
            </Button>
          </div>
        )}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto">
        {steps.length === 0 ? (
          <p className="text-muted-foreground p-4 text-sm">No steps yet. Add the first step of the program.</p>
        ) : (
          <table className="w-full text-xs">
            <thead className="bg-muted/50 text-muted-foreground sticky top-0">
              <tr className="[&>th]:px-2 [&>th]:py-1.5 [&>th]:text-left [&>th]:font-medium">
                <th>#</th>
                <th>Done</th>
                <th>Phase</th>
                <th>Action</th>
                <th>Instruction</th>
                {canWrite && <th></th>}
              </tr>
            </thead>
            <tbody>
              {steps.map((s, i) => (
                <StepRow
                  key={s.id}
                  step={s}
                  index={i}
                  total={steps.length}
                  editable={editable}
                  canWrite={canWrite}
                  onEdit={() => setEditingStep(s)}
                  onDelete={() => setDeleteTarget(s)}
                  onMove={(dir) => move(i, dir)}
                />
              ))}
            </tbody>
          </table>
        )}
      </div>

      {canWrite && editable && (
        <div className="border-t px-4 py-2">
          <Button size="sm" variant="outline" onClick={() => setAddingStep(true)}>
            <Plus className="size-4" />
            Add step
          </Button>
        </div>
      )}

      {addingStep && <StepDialog orderId={order.id} existing={null} onClose={() => setAddingStep(false)} />}
      {editingStep && <StepDialog orderId={order.id} existing={editingStep} onClose={() => setEditingStep(null)} />}
      <ConfirmDeleteDialog
        open={deleteTarget !== null}
        title={`Delete step ${deleteTarget?.step_no ?? ""}?`}
        description="Removes this step; the remaining steps renumber."
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        onConfirm={async () => {
          if (!deleteTarget) return
          await del.mutateAsync({ stepId: deleteTarget.id })
        }}
      />
    </div>
  )
}

/**
 * Standalone planned-switching builder (DU switching-gap #2, migration 0186): a
 * switching PROGRAM authored for planned work, not tied to any incident. The left
 * pane lists the programs; the right builds the selected one — add/edit/delete/
 * reorder its open/verify/earth/close steps, issue it, then tick steps off (which
 * flips the device's live state and logs the operation, exactly as an incident
 * switching order does). View needs oms:read; building needs oms:write.
 */
export function SwitchingPage() {
  const { data, isPending, isError } = usePlannedSwitching()
  const canWrite = usePermission()("oms:write")
  const [creating, setCreating] = useState(false)
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const orders = data?.switching_orders ?? []
  const selected = orders.find((o) => o.id === selectedId) ?? orders[0] ?? null

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="px-4 pt-4">
        <OperationsTabs />
      </div>
      <div className="flex min-h-0 flex-1">
      <div className="flex w-72 shrink-0 flex-col border-r">
        <div className="flex items-center justify-between gap-2 border-b px-3 py-3">
          <h1 className="flex items-center gap-2 font-semibold">
            <ListChecks className="size-5" />
            Switching
          </h1>
          {canWrite && (
            <Button size="sm" onClick={() => setCreating(true)}>
              <Plus className="size-4" />
              New
            </Button>
          )}
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto">
          {isPending ? (
            <div className="text-muted-foreground flex items-center gap-2 p-3 text-sm">
              <Loader2 className="size-4 animate-spin" />
              Loading…
            </div>
          ) : isError ? (
            <p className="text-destructive p-3 text-sm">Could not load switching programs.</p>
          ) : orders.length === 0 ? (
            <p className="text-muted-foreground p-3 text-sm">
              No planned switching programs yet.{canWrite && " Create one to author an open/close program."}
            </p>
          ) : (
            <ul>
              {orders.map((o) => (
                <li key={o.id}>
                  <button
                    type="button"
                    onClick={() => setSelectedId(o.id)}
                    className={cn(
                      "hover:bg-muted/60 block w-full border-b px-3 py-2 text-left",
                      selected?.id === o.id && "bg-muted",
                    )}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate text-sm font-medium">{o.reference ?? "Untitled program"}</span>
                      <span className={cn("rounded px-1.5 py-0.5 text-[10px] font-medium uppercase", statusClass(o.status))}>
                        {o.status.replace("_", " ")}
                      </span>
                    </div>
                    <div className="text-muted-foreground text-xs">
                      {o.steps.length} step{o.steps.length === 1 ? "" : "s"}
                      {o.steps.length > 0 && ` · ${o.steps.filter((s) => s.executed_at !== null).length} done`}
                    </div>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      {selected ? (
        <ProgramDetail key={selected.id} order={selected} canWrite={canWrite} />
      ) : (
        <div className="text-muted-foreground flex flex-1 items-center justify-center text-sm">
          Select a program, or create one.
        </div>
      )}

      {creating && <CreateDialog onClose={() => setCreating(false)} />}
      </div>
    </div>
  )
}
