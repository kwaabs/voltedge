import { useState } from "react"
import { X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { LoadFlowResult } from "@/stores/load-flow-store"
import { useLoadFlowStore } from "@/stores/load-flow-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/**
 * Floating panel for api/internal/powerflow's load-flow engine (see
 * docs/phase-8-load-flow.md) -- prompts for the one required, honestly-
 * can't-be-guessed input (nominal_voltage_v, see that engine's own doc
 * comment for why it isn't derived from a per-class field), then shows
 * the swept result as a data table, same fixed bottom-center slot
 * trace-result-table.tsx uses (the two are mutually exclusive in
 * practice -- feature-panel.tsx's trace/isolate/load-flow triggers each
 * clear the other two's stores before running). Combines the prompt and
 * the result into one component, unlike trace's separate trigger
 * (feature-panel.tsx) + display (trace-result-table.tsx) split: a load
 * flow's one input is specific to *this* result surface, not a generic
 * "run and show elsewhere" action, so there's nothing to gain from
 * splitting it across two files the way a zero-input trace click does.
 */
export function LoadFlowPanel() {
  const armed = useLoadFlowStore((s) => s.armed)
  const loading = useLoadFlowStore((s) => s.loading)
  const result = useLoadFlowStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("230")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code
  const barrierKeys = new Set((result?.barriers ?? []).map((b) => `${b.feature_class}:${b.feature_id}`))
  const overloadedKeys = new Set((result?.overloaded ?? []).map((o) => `${o.feature_class}:${o.feature_id}`))

  async function runLoadFlow() {
    const start = useLoadFlowStore.getState().armedStart
    if (!start) return
    const nominalVoltageV = Number(voltageInput)
    if (!Number.isFinite(nominalVoltageV) || nominalVoltageV <= 0) {
      useToastStore.getState().pushError("Nominal voltage must be a positive number.")
      return
    }

    useLoadFlowStore.getState().setLoading(true)
    try {
      const response = await apiFetch<LoadFlowResult>("/analysis/load-flow", {
        method: "POST",
        body: JSON.stringify({ start, nominal_voltage_v: nominalVoltageV }),
      })
      useLoadFlowStore.getState().setResult(response)
    } catch (err) {
      useLoadFlowStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run this load flow.")
    }
  }

  return (
    <div className="bg-popover text-popover-foreground fixed bottom-10 left-1/2 z-40 max-h-80 w-[32rem] max-w-[calc(100vw-2rem)] -translate-x-1/2 overflow-hidden rounded-md border shadow-lg">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <p className="truncate text-sm font-medium">Load flow</p>
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={() => useLoadFlowStore.getState().clear()}>
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
          <Button size="sm" className="ml-auto" onClick={runLoadFlow} disabled={loading}>
            {loading ? "Running…" : "Run"}
          </Button>
        </div>
      ) : (
        <div className="max-h-[16rem] overflow-y-auto">
          <div className="text-muted-foreground flex items-center justify-between gap-2 border-b px-3 py-1.5 text-xs">
            <span>Nominal {formatNumber(result.nominal_voltage_v, 0)}V</span>
            <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={() => useLoadFlowStore.getState().rearm()}>
              Run again
            </Button>
          </div>
          <table className="w-full text-sm">
            <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
              <tr>
                <th className="bg-popover px-3 py-1.5 font-medium">Feature</th>
                <th className="bg-popover px-3 py-1.5 font-medium">Voltage</th>
                <th className="bg-popover px-3 py-1.5 font-medium">Current</th>
                <th className="bg-popover px-3 py-1.5 font-medium">Loading</th>
              </tr>
            </thead>
            <tbody>
              {result.nodes.map((node) => {
                const key = `${node.feature_class}:${node.feature_id}`
                const overloaded = overloadedKeys.has(key)
                return (
                  <tr
                    key={key}
                    onClick={() => useSelectionStore.getState().select(node.feature_class, node.feature_id, { focus: true })}
                    className={cn(
                      "hover:bg-accent cursor-pointer border-b last:border-b-0",
                      overloaded && "bg-destructive/10 text-destructive hover:bg-destructive/15",
                    )}
                    title={overloaded ? "Exceeds this conductor's own ampacity" : undefined}
                  >
                    <td className="px-3 py-1.5">
                      <div>{classNameFor(node.feature_class)}</div>
                      <div className="text-muted-foreground font-mono text-xs">{shortId(node.feature_id)}</div>
                    </td>
                    <td className="px-3 py-1.5">
                      <div>{formatNumber(node.voltage_v)}V</div>
                      <div className="text-muted-foreground text-xs">-{formatNumber(node.voltage_drop_pct, 2)}%</div>
                    </td>
                    <td className="px-3 py-1.5">{node.current_amps !== undefined ? `${formatNumber(node.current_amps)}A` : "—"}</td>
                    <td className="px-3 py-1.5">
                      {node.loading_pct !== undefined ? `${formatNumber(node.loading_pct)}%` : "—"}
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
                    title="Load flow stopped here (open device)"
                  >
                    <td className="px-3 py-1.5">
                      <div>{classNameFor(b.feature_class)}</div>
                      <div className="font-mono text-xs">{shortId(b.feature_id)}</div>
                    </td>
                    <td className="px-3 py-1.5" colSpan={3}>
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
