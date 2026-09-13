import { useState } from "react"
import { X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
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

/** Amps → a compact A/kA string -- fault currents run to thousands of
 * amps, so a bare A figure is hard to read at a glance. */
function formatCurrent(amps: number): string {
  return amps >= 1000 ? `${formatNumber(amps / 1000, 2)}kA` : `${formatNumber(amps, 0)}A`
}

/** A node's own voltage base (nominal_base_v) as a compact level label --
 * "11 kV" / "400 V" -- so an MV-side fault level reads apart from the LV
 * level below a DIST_XFMR (the accumulation now refers impedance across the
 * transformer and reports current on each node's own base). */
function formatVoltageLevel(volts: number): string {
  if (volts >= 1000) return `${formatNumber(volts / 1000, volts % 1000 === 0 ? 0 : 1)} kV`
  return `${formatNumber(volts, 0)} V`
}

/**
 * Floating panel for api/internal/powerflow's fault-current walk (see
 * docs/phase-8-load-flow.md) -- the symmetrical (3-phase bolted) fault-
 * level counterpart of load-flow-panel.tsx, sharing its exact shape: the
 * same one honestly-can't-be-guessed input (nominal_voltage_v, the pre-
 * fault sending-end voltage), the same fixed bottom-center slot (the four
 * analysis workflows -- trace/isolate/load-flow/fault-current -- are
 * mutually exclusive, so they never occupy it at once), and the same
 * arm-then-run lifecycle. The result table shows the accumulated series
 * impedance to each node and the prospective bolted-fault current there;
 * a node whose path impedance wasn't fully backed by resolved material
 * data is flagged, since its fault current is then an optimistic upper
 * bound.
 */
export function FaultCurrentPanel() {
  const armed = useFaultCurrentStore((s) => s.armed)
  const loading = useFaultCurrentStore((s) => s.loading)
  const result = useFaultCurrentStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("230")
  const fp = useFloatingPanel(576, "max-h-80")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

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
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run this fault-current calc.")
    }
  }

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <p className="truncate text-sm font-medium">Fault current</p>
        </div>
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
        <div className={cn("overflow-y-auto", fp.resized ? "min-h-0 flex-1" : "max-h-[16rem]")}>
          <div className="text-muted-foreground flex items-center justify-between gap-2 border-b px-3 py-1.5 text-xs">
            <span>Nominal {formatNumber(result.nominal_voltage_v, 0)}V (3-phase bolted)</span>
            <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={() => useFaultCurrentStore.getState().rearm()}>
              Run again
            </Button>
          </div>
          <table className="w-full text-sm">
            <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
              <tr>
                <th className="bg-popover px-3 py-1.5 font-medium">Feature</th>
                <th className="bg-popover px-3 py-1.5 font-medium">Level</th>
                <th className="bg-popover px-3 py-1.5 font-medium">Impedance</th>
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
                      {(node.circuit_ref || node.circuit_id) && (
                        <div className="text-muted-foreground text-xs">{node.circuit_ref || shortId(node.circuit_id!)}</div>
                      )}
                    </td>
                    <td className="text-muted-foreground px-3 py-1.5 text-xs whitespace-nowrap">
                      {node.nominal_base_v > 0 ? formatVoltageLevel(node.nominal_base_v) : "—"}
                    </td>
                    <td className="px-3 py-1.5">
                      <div>{formatNumber(node.impedance_ohm, 3)}Ω</div>
                      <div className="text-muted-foreground text-xs">
                        R{formatNumber(node.resistance_ohm, 3)} X{formatNumber(node.reactance_ohm, 3)}
                      </div>
                    </td>
                    <td className="px-3 py-1.5">
                      {node.fault_current_amps !== undefined ? (
                        <span
                          className={cn(!node.material_resolved && "text-muted-foreground")}
                          title={
                            node.material_resolved
                              ? undefined
                              : "Upper bound: a conductor on this path had no resolved impedance, so the true fault current is lower"
                          }
                        >
                          {!node.material_resolved && "≤ "}
                          {formatCurrent(node.fault_current_amps)}
                        </span>
                      ) : (
                        <span title="Unbounded behind an ideal source -- set source_r_ohm/source_x_ohm on the start feature">∞</span>
                      )}
                    </td>
                  </tr>
                )
              })}
              {result.barriers.map((b) => (
                <tr
                  key={`${b.feature_class}:${b.feature_id}`}
                  onClick={() => useSelectionStore.getState().select(b.feature_class, b.feature_id, { focus: true })}
                  className="bg-muted/50 text-muted-foreground hover:bg-muted cursor-pointer border-b last:border-b-0"
                  title="Fault-current walk stopped here (open device)"
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
      {fp.resizeHandle}
    </div>
  )
}
