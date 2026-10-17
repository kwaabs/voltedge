import { useEffect, useRef, useState } from "react"

import { FormDialog } from "@/components/admin/form-dialog"
import { CircuitPicker } from "@/components/map/circuit-picker"
import { inputClass } from "@/components/map/field-utils"
import {
  type CircuitNetwork,
  useAssignCircuit,
  useCircuits,
  useCreateCircuit,
  useUpdateCircuit,
} from "@/hooks/use-circuits"
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const NETWORKS: CircuitNetwork[] = ["MV", "LV"]

/**
 * Designates the selected feature (e.g. a substation feeder breaker) as a circuit's
 * SOURCE, then runs assign-by-trace so circuit_id is stamped across the whole feeder
 * downstream of it (api/internal/circuit/assign.go). Pick an existing circuit or
 * create one inline; either path ends with one assign run. After this, a supply-path
 * from any feature on the feeder resolves this device as the feeder head
 * (SourceKind "feeder_head") instead of falling back to the nearest protective
 * section head.
 */
export function SetCircuitSourceDialog({
  feature,
  onClose,
}: {
  feature: { featureClassCode: string; featureId: string }
  onClose: () => void
}) {
  const [mode, setMode] = useState<"existing" | "new">("existing")
  const [existingId, setExistingId] = useState<string | undefined>(undefined)
  const [name, setName] = useState("")
  const [network, setNetwork] = useState<CircuitNetwork>("MV")

  const { data: circuitsData } = useCircuits()
  const create = useCreateCircuit()
  const update = useUpdateCircuit()
  const assign = useAssignCircuit()
  const saving = create.isPending || update.isPending || assign.isPending

  // Circuits this feature is ALREADY the source of. A feature carries one circuit_id,
  // so sourcing two circuits would make them contest membership (last assign wins) --
  // so we pre-select the existing one (re-run = safe re-sync) and warn on picking a
  // different circuit.
  const alreadySources = (circuitsData?.circuits ?? []).filter(
    (c) => c.source_feature_class === feature.featureClassCode && c.source_feature_id === feature.featureId,
  )
  const initedRef = useRef(false)
  useEffect(() => {
    if (initedRef.current || !circuitsData) return
    initedRef.current = true
    if (alreadySources.length > 0) setExistingId(alreadySources[0].id)
  }, [circuitsData, alreadySources])

  const isReassign = mode === "existing" && existingId !== undefined && alreadySources.some((c) => c.id === existingId)
  // Picking a DIFFERENT circuit (or creating one) while this feature already sources
  // another would make the two fight over membership.
  const contesting =
    alreadySources.length > 0 &&
    (mode === "new" || (existingId !== undefined && !alreadySources.some((c) => c.id === existingId)))

  const canSubmit = (mode === "existing" ? Boolean(existingId) : name.trim().length > 0) && !saving

  async function handleSubmit() {
    try {
      let circuitId: string
      if (mode === "existing") {
        circuitId = existingId as string
        await update.mutateAsync({
          id: circuitId,
          body: {
            source_feature_class: feature.featureClassCode,
            source_feature_id: feature.featureId,
          },
        })
      } else {
        const created = await create.mutateAsync({
          name: name.trim(),
          network,
          source_feature_class: feature.featureClassCode,
          source_feature_id: feature.featureId,
        })
        circuitId = created.id
      }
      const res = await assign.mutateAsync({ id: circuitId })
      const base = `Assigned "${res.circuit_name}": stamped ${res.stamped} feature${res.stamped === 1 ? "" : "s"} (${res.reached} reached).`
      if (res.skipped > 0) {
        const classes = (res.skipped_per_class ?? []).map((c) => `${c.feature_class} ×${c.stamped}`).join(", ")
        useToastStore
          .getState()
          .pushWarning(
            `${base} Skipped ${res.skipped} reached feature${res.skipped === 1 ? "" : "s"} whose class has no active circuit_id — recompile then re-assign: ${classes}.`,
          )
      } else {
        useToastStore.getState().pushSuccess(base)
      }
      onClose()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not set the circuit source.")
    }
  }

  return (
    <FormDialog
      open
      onOpenChange={(next) => next || onClose()}
      title="Set as circuit source"
      description={`Make this ${feature.featureClassCode} the source of a circuit, then stamp membership (circuit_id) across the feeder downstream of it.`}
      onSubmit={handleSubmit}
      submitLabel={isReassign ? "Re-assign" : "Set source & assign"}
      canSubmit={canSubmit}
      saving={saving}
    >
      {alreadySources.length > 0 && (
        <p className="rounded border border-amber-500/30 bg-amber-500/10 px-2 py-1.5 text-xs text-amber-700 dark:text-amber-400">
          This feature is already the source of <b>{alreadySources.map((c) => c.name).join(", ")}</b>. Re-running on the
          same circuit just re-syncs its membership.
        </p>
      )}
      {contesting && (
        <p className="rounded border border-destructive/40 bg-destructive/10 text-destructive px-2 py-1.5 text-xs">
          Setting it as another circuit’s source will make the two circuits contest membership — a feature carries one
          circuit_id, so whichever is assigned last wins. Usually one source feeds one circuit.
        </p>
      )}
      <div className="flex gap-2 text-sm">
        <button
          type="button"
          className={`flex-1 rounded-md border px-2 py-1.5 ${mode === "existing" ? "border-primary bg-primary/10 font-medium" : "border-border"}`}
          onClick={() => setMode("existing")}
        >
          Existing circuit
        </button>
        <button
          type="button"
          className={`flex-1 rounded-md border px-2 py-1.5 ${mode === "new" ? "border-primary bg-primary/10 font-medium" : "border-border"}`}
          onClick={() => setMode("new")}
        >
          New circuit
        </button>
      </div>

      {mode === "existing" ? (
        <div className="flex flex-col gap-1">
          <label className="text-xs font-medium">Circuit</label>
          <CircuitPicker value={existingId} onChange={(v) => setExistingId(typeof v === "string" ? v : undefined)} />
          <span className="text-muted-foreground text-xs">
            Its source is set to this feature, then membership is re-synced (full sync — replaces the prior members).
          </span>
        </div>
      ) : (
        <>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium">Network</label>
            <select className={inputClass} value={network} onChange={(e) => setNetwork(e.target.value as CircuitNetwork)}>
              {NETWORKS.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
            <span className="text-muted-foreground text-xs">
              MV for a substation feeder breaker — only this network's features are stamped.
            </span>
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-xs font-medium">
              Name <span className="text-destructive">*</span>
            </label>
            <input
              className={inputClass}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="e.g. Feeder 7"
              autoFocus
            />
          </div>
        </>
      )}
    </FormDialog>
  )
}
