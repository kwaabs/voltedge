import { useState } from "react"
import { X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"
import type { VoltageProfileResult } from "@/stores/voltage-profile-store"
import { useVoltageProfileStore } from "@/stores/voltage-profile-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/** A node's own voltage base as a compact level label -- "11 kV" / "400 V"
 * -- so MV nodes and the LV nodes below a DIST_XFMR read apart. */
function formatVoltageLevel(volts: number): string {
  if (volts >= 1000) return `${formatNumber(volts / 1000, volts % 1000 === 0 ? 0 : 1)} kV`
  return `${formatNumber(volts, 0)} V`
}

/**
 * Floating panel for api/internal/powerflow's voltage-profile report
 * (POST /analysis/voltage-profile) -- the same radial sweep as load flow,
 * read as a voltage profile (per-node per-unit voltage judged against an
 * ANSI C84.1 band) plus a feeder-level series-loss (I²R) summary. Same
 * combined prompt+result shape and bottom-center slot as
 * load-flow-panel.tsx; the seven analysis workflows are mutually
 * exclusive, so feature-panel.tsx's triggers clear each other's stores.
 *
 * The prompt asks for nominal_voltage_v (the one genuinely un-guessable
 * input, same as load flow) and optionally the acceptable per-unit
 * voltage band; blank band fields fall back to the server default
 * (0.95..1.05).
 */
export function VoltageProfilePanel() {
  const armed = useVoltageProfileStore((s) => s.armed)
  const loading = useVoltageProfileStore((s) => s.loading)
  const result = useVoltageProfileStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("230")
  const [minPuInput, setMinPuInput] = useState("0.95")
  const [maxPuInput, setMaxPuInput] = useState("1.05")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code
  const barrierKeys = new Set((result?.barriers ?? []).map((b) => `${b.feature_class}:${b.feature_id}`))
  const violationKeys = new Set((result?.violations ?? []).map((v) => `${v.feature_class}:${v.feature_id}`))

  async function runVoltageProfile() {
    const start = useVoltageProfileStore.getState().armedStart
    if (!start) return
    const nominalVoltageV = Number(voltageInput)
    if (!Number.isFinite(nominalVoltageV) || nominalVoltageV <= 0) {
      useToastStore.getState().pushError("Nominal voltage must be a positive number.")
      return
    }
    // Blank band field => let the server apply its ANSI C84.1 default.
    const minPu = minPuInput.trim() === "" ? undefined : Number(minPuInput)
    const maxPu = maxPuInput.trim() === "" ? undefined : Number(maxPuInput)
    if ((minPu !== undefined && !(minPu > 0)) || (maxPu !== undefined && !(maxPu > 0))) {
      useToastStore.getState().pushError("Voltage limits must be positive numbers.")
      return
    }
    if (minPu !== undefined && maxPu !== undefined && minPu > maxPu) {
      useToastStore.getState().pushError("Min voltage must not exceed max voltage.")
      return
    }

    useVoltageProfileStore.getState().setLoading(true)
    try {
      const response = await apiFetch<VoltageProfileResult>("/analysis/voltage-profile", {
        method: "POST",
        body: JSON.stringify({
          start,
          nominal_voltage_v: nominalVoltageV,
          min_voltage_pu: minPu,
          max_voltage_pu: maxPu,
        }),
      })
      useVoltageProfileStore.getState().setResult(response)
    } catch (err) {
      useVoltageProfileStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run this voltage profile.")
    }
  }

  return (
    <div className="bg-popover text-popover-foreground fixed bottom-10 left-1/2 z-40 max-h-[22rem] w-[40rem] max-w-[calc(100vw-2rem)] -translate-x-1/2 overflow-hidden rounded-md border shadow-lg">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <p className="truncate text-sm font-medium">Voltage profile &amp; losses</p>
        <Button
          variant="ghost"
          size="icon"
          className="size-6 shrink-0"
          onClick={() => useVoltageProfileStore.getState().clear()}
        >
          <X className="size-3.5" />
          <span className="sr-only">Close</span>
        </Button>
      </div>

      {!result ? (
        <div className="flex flex-wrap items-end gap-3 p-3">
          <div className="flex flex-col gap-1">
            <label className="text-muted-foreground text-xs">Nominal voltage (V)</label>
            <input
              type="number"
              className={cn(inputClass, "w-28")}
              value={voltageInput}
              onChange={(e) => setVoltageInput(e.target.value)}
              autoFocus
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-muted-foreground text-xs">Min (pu)</label>
            <input
              type="number"
              step="0.01"
              className={cn(inputClass, "w-20")}
              value={minPuInput}
              onChange={(e) => setMinPuInput(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-muted-foreground text-xs">Max (pu)</label>
            <input
              type="number"
              step="0.01"
              className={cn(inputClass, "w-20")}
              value={maxPuInput}
              onChange={(e) => setMaxPuInput(e.target.value)}
            />
          </div>
          <Button size="sm" className="ml-auto" onClick={runVoltageProfile} disabled={loading}>
            {loading ? "Running…" : "Run"}
          </Button>
        </div>
      ) : (
        <div className="flex max-h-[19rem] flex-col overflow-hidden">
          {/* Feeder summary -- total load, total series losses, loss %,
              worst node's per-unit voltage, and the violation count. */}
          <div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 border-b px-3 py-1.5 text-xs">
            <span>Load {formatNumber(result.summary.total_load_kw)} kW</span>
            <span>
              Losses {formatNumber(result.summary.total_loss_kw, 2)} kW ({formatNumber(result.summary.loss_pct, 1)}%)
              {!result.summary.losses_complete && (
                <span className="text-amber-600" title="Some branch had no resolved R/X -- losses are a lower bound">
                  {" "}
                  ≥
                </span>
              )}
            </span>
            <span className={cn(result.summary.violation_count > 0 && "text-destructive")}>
              Worst {formatNumber(result.summary.min_voltage_pu, 3)} pu
            </span>
            <span className={cn(result.summary.violation_count > 0 && "text-destructive")}>
              {result.summary.violation_count} violation{result.summary.violation_count === 1 ? "" : "s"}
            </span>
            <Button
              variant="ghost"
              size="sm"
              className="ml-auto h-6 px-2 text-xs"
              onClick={() => useVoltageProfileStore.getState().rearm()}
            >
              Run again
            </Button>
          </div>
          <div className="overflow-y-auto">
            <table className="w-full text-sm">
              <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
                <tr>
                  <th className="bg-popover px-3 py-1.5 font-medium">Feature</th>
                  <th className="bg-popover px-3 py-1.5 font-medium">Level</th>
                  <th className="bg-popover px-3 py-1.5 font-medium">Voltage (pu)</th>
                  <th className="bg-popover px-3 py-1.5 font-medium">Drop</th>
                  <th className="bg-popover px-3 py-1.5 font-medium">Branch loss</th>
                </tr>
              </thead>
              <tbody>
                {result.nodes.map((node) => {
                  const key = `${node.feature_class}:${node.feature_id}`
                  const violated = violationKeys.has(key)
                  return (
                    <tr
                      key={key}
                      onClick={() => useSelectionStore.getState().select(node.feature_class, node.feature_id, { focus: true })}
                      className={cn(
                        "hover:bg-accent cursor-pointer border-b last:border-b-0",
                        violated && "bg-destructive/10 text-destructive hover:bg-destructive/15",
                      )}
                      title={violated ? "Outside the acceptable voltage band" : undefined}
                    >
                      <td className="px-3 py-1.5">
                        <div>{classNameFor(node.feature_class)}</div>
                        <div className="text-muted-foreground font-mono text-xs">{shortId(node.feature_id)}</div>
                        {(node.circuit_ref || node.circuit_id) && (
                          <div className="text-muted-foreground text-xs">{node.circuit_ref || shortId(node.circuit_id!)}</div>
                        )}
                      </td>
                      <td className="text-muted-foreground px-3 py-1.5 text-xs whitespace-nowrap">
                        {node.nominal_base_v > 0 ? formatVoltageLevel(node.nominal_base_v) : "—"}
                      </td>
                      <td className="px-3 py-1.5">
                        {node.nominal_base_v > 0 ? formatNumber(node.voltage_pu, 3) : "—"}
                      </td>
                      <td className="text-muted-foreground px-3 py-1.5 text-xs">
                        -{formatNumber(node.voltage_drop_pct, 2)}%
                      </td>
                      <td className="px-3 py-1.5">
                        {node.branch_loss_kw !== undefined ? `${formatNumber(node.branch_loss_kw, 2)} kW` : "—"}
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
                      title="Sweep stopped here (open device)"
                    >
                      <td className="px-3 py-1.5">
                        <div>{classNameFor(b.feature_class)}</div>
                        <div className="font-mono text-xs">{shortId(b.feature_id)}</div>
                      </td>
                      <td className="px-3 py-1.5" colSpan={4}>
                        Stopped here (open device)
                      </td>
                    </tr>
                  ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </div>
  )
}
