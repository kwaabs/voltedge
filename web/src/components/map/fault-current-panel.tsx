import { useState } from "react"
import { X } from "lucide-react"

import { AnalysisExplanation, Formula } from "@/components/map/analysis-explanation"
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
/**
 * The dynamic "how this was calculated" body for a fault-current run. Every
 * figure below is read off the actual result -- the governing node, its
 * impedance, the reached/flagged counts -- so it verifies THIS answer, not a
 * generic description of the method.
 */
function FaultCurrentExplanation({
  result,
  classNameFor,
}: {
  result: FaultCurrentResult
  classNameFor: (code: string) => string
}) {
  const vLN = result.nominal_voltage_v / Math.sqrt(3)
  const withCurrent = result.nodes.filter((n) => n.fault_current_amps !== undefined)
  const governing = withCurrent.reduce<(typeof withCurrent)[number] | null>(
    (best, n) => (best === null || n.fault_current_amps! > best.fault_current_amps! ? n : best),
    null,
  )
  const flagged = result.nodes.filter((n) => !n.material_resolved).length
  const ideal = result.nodes.length - withCurrent.length

  return (
    <AnalysisExplanation>
      <p>
        Symmetrical <b>3-phase bolted</b> fault (IEC 60909 style, zero fault impedance). The pre-fault line
        voltage is <b>{formatNumber(result.nominal_voltage_v, 0)} V</b>, so the driving phase-to-neutral
        voltage is <code>{formatNumber(result.nominal_voltage_v, 0)}/√3 = {formatNumber(vLN, 0)} V</code>.
      </p>
      <p>
        For each reached node the engine walks the path back to the source, accumulates the series impedance
        <code> Z = R + jX</code>, refers it across any transformer to that node's <b>own</b> voltage base, and
        computes <code>Ibf = V_LN / |Z|</code>.
      </p>
      {governing && (
        <>
          <p>
            <b>{result.nodes.length} node(s)</b> reached. The highest fault level here is{" "}
            <b>{formatCurrent(governing.fault_current_amps!)}</b> at {classNameFor(governing.feature_class)}{" "}
            ({formatVoltageLevel(governing.nominal_base_v)} base). Worked at that node:
          </p>
          <Formula>{`Ibf = V_LN / |Z|
    = ${formatNumber(governing.nominal_base_v / Math.sqrt(3), 0)} V / ${formatNumber(governing.impedance_ohm, 3)} Ω     (V_LN = ${formatNumber(governing.nominal_base_v, 0)} V / √3)
    = ${formatCurrent(governing.fault_current_amps!)}     (|Z| = R ${formatNumber(governing.resistance_ohm, 3)} + jX ${formatNumber(governing.reactance_ohm, 3)})`}</Formula>
          <p>Fault current falls as |Z| grows with distance from the source.</p>
        </>
      )}
      {(flagged > 0 || ideal > 0 || result.barriers.length > 0) && (
        <p>
          Honesty flags:{" "}
          {flagged > 0 && (
            <>
              <b>{flagged}</b> node(s) show <code>≤</code> because a conductor on their path had no resolved
              impedance, so their fault current is an <b>optimistic upper bound</b>.{" "}
            </>
          )}
          {ideal > 0 && (
            <>
              <b>{ideal}</b> node(s) sit behind an ideal source (no <code>source_r_ohm</code>/<code>source_x_ohm</code>{" "}
              on the start feature) so read <code>∞</code>.{" "}
            </>
          )}
          {result.barriers.length > 0 && (
            <>
              <b>{result.barriers.length}</b> open device(s) stopped the walk.
            </>
          )}
        </p>
      )}
    </AnalysisExplanation>
  )
}

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
        <>
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
          <FaultCurrentExplanation result={result} classNameFor={classNameFor} />
        </>
      )}
      {fp.resizeHandle}
    </div>
  )
}
