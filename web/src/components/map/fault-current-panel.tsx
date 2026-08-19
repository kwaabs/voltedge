import { useState } from "react"
import { X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { FaultCurrentResult } from "@/stores/fault-current-store"
import { useFaultCurrentStore } from "@/stores/fault-current-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/**
 * Floating panel for api/internal/powerflow's fault-current engine
 * (see docs/phase-8-fault-current.md) -- prompts for nominal_voltage_v
 * (same required, honestly-can't-be-guessed input load-flow-panel.tsx
 * already prompts for), then shows the swept result as a data table,
 * same fixed bottom-center slot every other trace/analysis panel uses.
 * Structurally a near-twin of load-flow-panel.tsx, kept as its own
 * component rather than a shared one: the result shape is genuinely
 * different (impedance/fault-duty columns, no loading %/overload
 * concept), so a shared component would need to branch on which kind
 * of result it's showing anyway.
 */
export function FaultCurrentPanel() {
  const armed = useFaultCurrentStore((s) => s.armed)
  const loading = useFaultCurrentStore((s) => s.loading)
  const result = useFaultCurrentStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("230")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code
  const barrierKeys = new Set((result?.barriers ?? []).map((b) => `${b.feature_class}:${b.feature_id}`))

  async function runFaultCurrent() {
    const start = useFaultCurrentStore.getState().armedStart
    if (!start) return
    const nominalVoltageV = Number(voltageInput)
    if (!Number.isFinite(nominalVoltageV) || nominalVoltageV <= 0) {
      useToastStore.getState().pushError("Nominal voltage must be a positive number.")
      return
    }

    useFaultCurrentStore.getState().setLoading(true)
    try {
      const response = await apiFetch<FaultCurrentResult>("/analysis/fault-current", {
        method: "POST",
        body: JSON.stringify({ start, nominal_voltage_v: nominalVoltageV }),
      })
      useFaultCurrentStore.getState().setResult(response)
    } catch (err) {
      useFaultCurrentStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not compute fault current.")
    }
  }

  return (
    <div className="bg-popover text-popover-foreground fixed bottom-10 left-1/2 z-40 max-h-80 w-[32rem] max-w-[calc(100vw-2rem)] -translate-x-1/2 overflow-hidden rounded-md border shadow-lg">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <p className="truncate text-sm font-medium">Fault current</p>
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={() => useFaultCurrentStore.getState().clear()}>
          <X className="size-3.5" />
          <span className="sr-only">Close</span>
        </Button>
      </div>

      {!result ? (
        <div className="flex items-center gap-2 p-3">
          <label className="text-muted-foreground shrink-0 text-sm">Nominal voltage (V)</label>
          <input
            type="number"
            className={cn(inputClass, "w-28")}
            value={voltageInput}
            onChange={(e) => setVoltageInput(e.target.value)}
            autoFocus
          />
          <Button size="sm" className="ml-auto" onClick={runFaultCurrent} disabled={loading}>
            {loading ? "Running…" : "Run"}
          </Button>
        </div>
      ) : (
        <div className="max-h-[16rem] overflow-y-auto">
          <div className="text-muted-foreground flex items-center justify-between gap-2 border-b px-3 py-1.5 text-xs">
            <span>Nominal {formatNumber(result.nominal_voltage_v, 0)}V</span>
            <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={() => useFaultCurrentStore.getState().rearm()}>
              Run again
            </Button>
          </div>
          <table className="w-full text-sm">
            <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
              <tr>
                <th className="bg-popover px-3 py-1.5 font-medium">Feature</th>
                <th className="bg-popover px-3 py-1.5 font-medium">Z total</th>
                <th className="bg-popover px-3 py-1.5 font-medium">Fault current</th>
              </tr>
            </thead>
            <tbody>
              {result.nodes.map((node) => {
                const key = `${node.feature_class}:${node.feature_id}`
                return (
                  <tr
                    key={key}
                    onClick={() => useSelectionStore.getState().select(node.feature_class, node.feature_id, { focus: true })}
                    className="hover:bg-accent cursor-pointer border-b last:border-b-0"
                  >
                    <td className="px-3 py-1.5">
                      <div>{classNameFor(node.feature_class)}</div>
                      <div className="text-muted-foreground font-mono text-xs">{shortId(node.feature_id)}</div>
                    </td>
                    <td className="px-3 py-1.5">
                      <div>{formatNumber(node.z_ohm_total, 4)}Ω</div>
                      <div className="text-muted-foreground text-xs">
                        R {formatNumber(node.r_ohm_total, 4)} / X {formatNumber(node.x_ohm_total, 4)}
                      </div>
                    </td>
                    <td className="px-3 py-1.5">
                      {node.fault_current_amps !== undefined ? `${formatNumber(node.fault_current_amps, 0)}A` : "—"}
                    </td>
                  </tr>
                )
              })}
              {barrierKeys.size > 0 &&
                result.barriers.map((b) => (
                  <tr
                    key={`${b.feature_class}:${b.feature_id}`}
                    onClick={() => useSelectionStore.getState().select(b.feature_class, b.feature_id, { focus: true })}
                    className="bg-muted/50 text-muted-foreground hover:bg-muted cursor-pointer border-b last:border-b-0"
                    title="Fault current walk stopped here (open device)"
                  >
                    <td className="px-3 py-1.5">
                      <div>{classNameFor(b.feature_class)}</div>
                      <div className="font-mono text-xs">{shortId(b.feature_id)}</div>
                    </td>
                    <td className="px-3 py-1.5" colSpan={2}>
                      Stopped here (open device)
                    </td>
                  </tr>
                ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
