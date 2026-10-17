import { useMemo, useState } from "react"
import { Eye, Loader2, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react"

import { ConfirmDeleteDialog } from "@/components/admin/confirm-delete-dialog"
import { FormDialog } from "@/components/admin/form-dialog"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  type Circuit,
  type CircuitNetwork,
  useAssignCircuit,
  useAssignStale,
  useCircuits,
  useCreateCircuit,
  useDeleteCircuit,
  usePreviewCircuit,
  useUpdateCircuit,
} from "@/hooks/use-circuits"
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const labelClass = "text-xs font-medium"
const NETWORKS: CircuitNetwork[] = ["MV", "LV"]

function toastError(err: unknown, fallback: string) {
  useToastStore.getState().pushError(err instanceof ApiError ? err.message : fallback)
}

/** Parses a voltage input into a number, empty/blank -> undefined (unknown). */
function parseVoltage(raw: string): number | undefined {
  const trimmed = raw.trim()
  if (trimmed === "") return undefined
  const n = Number(trimmed)
  return Number.isFinite(n) ? n : undefined
}

/**
 * Create form. Network is chosen here and is fixed after creation (it's the
 * circuit's identity namespace) -- so an MV "Old Age 2" and an LV one are
 * independent, and the name only has to be unique within the chosen network.
 * Voltage is optional (an MV circuit "could be 11 or 33", unknown until known).
 */
function CreateCircuitDialog({
  initialNetwork,
  onClose,
}: {
  initialNetwork: CircuitNetwork
  onClose: () => void
}) {
  const [name, setName] = useState("")
  const [network, setNetwork] = useState<CircuitNetwork>(initialNetwork)
  const [voltage, setVoltage] = useState("")
  const create = useCreateCircuit()
  const canSave = name.trim().length > 0 && !create.isPending

  function handleSave() {
    create.mutate(
      { name: name.trim(), network, voltage_kv: parseVoltage(voltage) ?? null },
      {
        onSuccess: onClose,
        onError: (err) => toastError(err, "Could not create the circuit."),
      },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="New circuit"
      description={`Adds a ${network} circuit. Its name only has to be unique within ${network}.`}
      onSubmit={handleSave}
      submitLabel="Create"
      canSubmit={canSave}
      saving={create.isPending}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Network</label>
        <select
          className={inputClass}
          value={network}
          onChange={(e) => setNetwork(e.target.value as CircuitNetwork)}
        >
          {NETWORKS.map((n) => (
            <option key={n} value={n}>
              {n}
            </option>
          ))}
        </select>
        <span className="text-muted-foreground text-xs">Fixed once created -- delete and recreate to change it.</span>
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>
          Name <span className="text-destructive">*</span>
        </label>
        <input
          className={inputClass}
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. Old Age 2"
          autoFocus
        />
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Voltage (kV)</label>
        <input
          className={inputClass}
          type="number"
          min="0"
          step="any"
          value={voltage}
          onChange={(e) => setVoltage(e.target.value)}
          placeholder="e.g. 11"
        />
        <span className="text-muted-foreground text-xs">Optional -- leave blank if not yet known.</span>
      </div>
    </FormDialog>
  )
}

/** Edit form -- name and voltage (network is immutable). A blank voltage
 * clears it back to unknown. */
function EditCircuitDialog({ circuit, onClose }: { circuit: Circuit; onClose: () => void }) {
  const [name, setName] = useState(circuit.name)
  const [voltage, setVoltage] = useState(circuit.voltage_kv == null ? "" : String(circuit.voltage_kv))
  const update = useUpdateCircuit()

  const nameChanged = name.trim() !== circuit.name && name.trim().length > 0
  const newVoltage = parseVoltage(voltage)
  const voltageChanged = (newVoltage ?? null) !== circuit.voltage_kv
  const canSave = (nameChanged || voltageChanged) && name.trim().length > 0 && !update.isPending

  function handleSave() {
    const body: { name?: string; voltage_kv?: number; clear_voltage?: boolean } = {}
    if (nameChanged) body.name = name.trim()
    if (voltageChanged) {
      if (newVoltage === undefined) body.clear_voltage = true
      else body.voltage_kv = newVoltage
    }
    update.mutate(
      { id: circuit.id, body },
      {
        onSuccess: onClose,
        onError: (err) => toastError(err, "Could not update the circuit."),
      },
    )
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Edit circuit"
      description={`${circuit.network} circuit "${circuit.name}". Network is fixed.`}
      onSubmit={handleSave}
      submitLabel="Save"
      canSubmit={canSave}
      saving={update.isPending}
    >
      <div className="flex flex-col gap-1">
        <label className={labelClass}>Name</label>
        <input className={inputClass} value={name} onChange={(e) => setName(e.target.value)} autoFocus />
      </div>

      <div className="flex flex-col gap-1">
        <label className={labelClass}>Voltage (kV)</label>
        <input
          className={inputClass}
          type="number"
          min="0"
          step="any"
          value={voltage}
          onChange={(e) => setVoltage(e.target.value)}
          placeholder="unknown"
        />
        <span className="text-muted-foreground text-xs">Blank clears it back to unknown.</span>
      </div>
    </FormDialog>
  )
}

/** One circuit row with its actions. */
function CircuitRow({
  circuit,
  onEdit,
  onDelete,
  onAssign,
  isAssigning,
}: {
  circuit: Circuit
  onEdit: (c: Circuit) => void
  onDelete: (c: Circuit) => void
  onAssign: (c: Circuit) => void
  isAssigning: boolean
}) {
  const [previewOpen, setPreviewOpen] = useState(false)
  const preview = usePreviewCircuit(circuit.id, previewOpen)
  const hasSource = Boolean(circuit.source_feature_class)
  return (
    <div className="flex flex-col">
      <div className="hover:bg-muted/50 group flex items-center gap-2 rounded-md px-2 py-1.5">
        <span className="truncate text-sm font-medium">{circuit.name}</span>
        {circuit.voltage_kv != null && (
          <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">{circuit.voltage_kv} kV</span>
        )}
        {circuit.members_synced_at == null ? (
          <span
            className="text-muted-foreground rounded px-1.5 py-0.5 text-xs"
            title="Membership has never been assigned from this circuit's source"
          >
            not assigned
          </span>
        ) : circuit.stale ? (
          <span
            className="rounded bg-amber-500/15 px-1.5 py-0.5 text-xs font-medium text-amber-700 dark:text-amber-500"
            title="A member feature changed since this circuit was last assigned — re-assign from its source to refresh membership"
          >
            stale — re-assign
          </span>
        ) : null}
        <div className="ml-auto flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            disabled={!hasSource}
            aria-pressed={previewOpen}
            title={
              hasSource
                ? "Preview: trace from the source and show what membership an assign would add / drop — writes nothing"
                : "No source set — nothing to preview"
            }
            onClick={() => setPreviewOpen((o) => !o)}
          >
            <Eye className="size-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="size-7"
            disabled={!hasSource || isAssigning}
            title={
              hasSource
                ? "Assign / re-sync: trace downstream from the source and re-stamp circuit_id across the feeder"
                : "No source set — on the map, select this circuit's source device and use “Set as circuit source” first"
            }
            onClick={() => onAssign(circuit)}
          >
            {isAssigning ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
          </Button>
          <Button variant="ghost" size="icon" className="size-7" title="Edit" onClick={() => onEdit(circuit)}>
            <Pencil className="size-4" />
          </Button>
          <Button
            variant="ghost"
            size="icon"
            className="text-destructive size-7"
            title="Delete"
            onClick={() => onDelete(circuit)}
          >
            <Trash2 className="size-4" />
          </Button>
        </div>
      </div>
      {previewOpen && (
        <div className="bg-muted/30 mx-2 mb-1 ml-2 rounded-md border px-3 py-2 text-xs">
          {preview.isPending ? (
            <span className="text-muted-foreground flex items-center gap-2">
              <Loader2 className="size-3.5 animate-spin" /> Tracing…
            </span>
          ) : preview.isError ? (
            <span className="text-destructive">{preview.error.message}</span>
          ) : preview.data ? (
            <div className="flex flex-col gap-1">
              <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                <span className="font-medium">If you assign now:</span>
                <span className="text-emerald-700 dark:text-emerald-500">+{preview.data.will_add} join</span>
                <span className="text-destructive">−{preview.data.will_remove} drop</span>
                <span className="text-muted-foreground">={preview.data.unchanged} unchanged</span>
                <span className="text-muted-foreground">· {preview.data.reached} reached</span>
                {preview.data.will_add === 0 && preview.data.will_remove === 0 && (
                  <span className="text-muted-foreground italic">already in sync</span>
                )}
              </div>
              {preview.data.per_class.length > 0 && (
                <div className="text-muted-foreground font-mono">
                  {preview.data.per_class
                    .map((p) => `${p.feature_class}: +${p.add}/−${p.remove}/=${p.keep}`)
                    .join("  ·  ")}
                </div>
              )}
              {preview.data.skipped > 0 && (
                <div className="text-amber-700 dark:text-amber-500">
                  {preview.data.skipped} reached feature{preview.data.skipped === 1 ? "" : "s"} can’t be stamped (class
                  missing an active circuit_id — recompile):{" "}
                  {(preview.data.skipped_per_class ?? []).map((c) => `${c.feature_class} ×${c.stamped}`).join(", ")}
                </div>
              )}
              <div>
                <Button
                  size="sm"
                  className="h-7"
                  disabled={isAssigning}
                  onClick={() => {
                    onAssign(circuit)
                    setPreviewOpen(false)
                  }}
                >
                  {isAssigning ? <Loader2 className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}
                  Apply assign
                </Button>
              </div>
            </div>
          ) : null}
        </div>
      )}
    </div>
  )
}

export function AdminCircuitsPage() {
  const { data, isPending, isError } = useCircuits()
  const [creating, setCreating] = useState<{ network: CircuitNetwork } | null>(null)
  const [editing, setEditing] = useState<Circuit | null>(null)
  const [deleting, setDeleting] = useState<Circuit | null>(null)
  const del = useDeleteCircuit()
  const assign = useAssignCircuit()
  const assignStale = useAssignStale()
  const [assigningId, setAssigningId] = useState<string | null>(null)

  function handleAssign(c: Circuit) {
    setAssigningId(c.id)
    assign.mutate(
      { id: c.id },
      {
        onSuccess: (res) => {
          setAssigningId(null)
          const base = `Assigned "${res.circuit_name}": stamped ${res.stamped} feature${res.stamped === 1 ? "" : "s"} (${res.reached} reached).`
          if (res.skipped > 0) {
            const classes = (res.skipped_per_class ?? [])
              .map((c) => `${c.feature_class} ×${c.stamped}`)
              .join(", ")
            useToastStore
              .getState()
              .pushWarning(
                `${base} Skipped ${res.skipped} reached feature${res.skipped === 1 ? "" : "s"} whose class has no active circuit_id — recompile then re-assign: ${classes}.`,
              )
          } else {
            useToastStore.getState().pushSuccess(base)
          }
        },
        onError: (err) => {
          setAssigningId(null)
          toastError(err, "Could not assign the circuit.")
        },
      },
    )
  }

  const circuits = useMemo(() => data?.circuits ?? [], [data])
  const staleCount = useMemo(() => circuits.filter((c) => c.stale).length, [circuits])

  function handleAssignStale() {
    assignStale.mutate(undefined, {
      onSuccess: (res) => {
        const base = `Re-synced ${res.circuits} stale circuit${res.circuits === 1 ? "" : "s"}: stamped ${res.stamped}, cleared ${res.cleared}.`
        if (res.failures && res.failures.length > 0) {
          useToastStore
            .getState()
            .pushWarning(`${base} ${res.failures.length} could not be assigned: ${res.failures.map((f) => f.circuit_name).join(", ")}.`)
        } else {
          useToastStore.getState().pushSuccess(res.circuits === 0 ? "No stale circuits to re-sync." : base)
        }
      },
      onError: (err) => toastError(err, "Could not assign stale circuits."),
    })
  }

  const byNetwork = useMemo(() => {
    const map = new Map<CircuitNetwork, Circuit[]>()
    for (const n of NETWORKS) map.set(n, [])
    for (const c of circuits) map.get(c.network)?.push(c)
    return map
  }, [circuits])

  return (
    <div className="h-full overflow-y-auto p-4">
      <div className="flex w-full flex-col gap-4">
        <div className="flex items-center justify-between">
          <div>
            <h1 className="text-lg font-semibold">Circuits</h1>
            <p className="text-muted-foreground text-sm">
              Named, network-wide electrical circuits (e.g. the MV feeder "Old Age 2") that features across many classes
              belong to. MV and LV are separate namespaces.
            </p>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {staleCount > 0 && (
              <Button
                size="sm"
                variant="outline"
                disabled={assignStale.isPending}
                onClick={handleAssignStale}
                title="Re-sync every stale circuit from its source in one run"
              >
                {assignStale.isPending ? (
                  <Loader2 className="size-4 animate-spin" />
                ) : (
                  <RefreshCw className="size-4" />
                )}
                Assign all stale ({staleCount})
              </Button>
            )}
            <Button size="sm" onClick={() => setCreating({ network: "MV" })}>
              <Plus className="size-4" />
              New circuit
            </Button>
          </div>
        </div>

        {isPending ? (
          <div className="text-muted-foreground flex items-center gap-2 text-sm">
            <Loader2 className="size-4 animate-spin" />
            Loading circuits...
          </div>
        ) : isError ? (
          <p className="text-destructive text-sm">Could not load circuits.</p>
        ) : (
          NETWORKS.map((network) => {
            const rows = byNetwork.get(network) ?? []
            return (
              <Card key={network}>
                <CardHeader className="flex-row items-center justify-between pb-2">
                  <div>
                    <CardTitle className="text-sm">{network}</CardTitle>
                    <CardDescription>
                      {rows.length === 0 ? "No circuits yet." : `${rows.length} circuit${rows.length === 1 ? "" : "s"}.`}
                    </CardDescription>
                  </div>
                  <Button variant="outline" size="sm" onClick={() => setCreating({ network })}>
                    <Plus className="size-4" />
                    Add {network}
                  </Button>
                </CardHeader>
                {rows.length > 0 && (
                  <CardContent className="pt-0">
                    <div className="flex flex-col">
                      {rows.map((c) => (
                        <CircuitRow
                          key={c.id}
                          circuit={c}
                          onEdit={setEditing}
                          onDelete={setDeleting}
                          onAssign={handleAssign}
                          isAssigning={assigningId === c.id}
                        />
                      ))}
                    </div>
                  </CardContent>
                )}
              </Card>
            )
          })
        )}
      </div>

      {creating !== null && (
        <CreateCircuitDialog
          key={creating.network}
          initialNetwork={creating.network}
          onClose={() => setCreating(null)}
        />
      )}
      {editing && <EditCircuitDialog key={editing.id} circuit={editing} onClose={() => setEditing(null)} />}

      <ConfirmDeleteDialog
        open={deleting !== null}
        title={`Delete ${deleting?.name ?? ""}?`}
        description="This can't be undone. Features stamped onto this circuit keep their stored circuit_id until reassigned."
        confirmLabel="Delete"
        onOpenChange={(open) => !open && setDeleting(null)}
        onConfirm={async () => {
          if (!deleting) return
          await del.mutateAsync({ id: deleting.id })
        }}
      />
    </div>
  )
}
