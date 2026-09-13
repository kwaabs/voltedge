import { useState } from "react"
import { X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { MeshedLoadFlowResult } from "@/stores/meshed-load-flow-store"
import { useMeshedLoadFlowStore } from "@/stores/meshed-load-flow-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/** A node's own voltage base (nominal_base_v) as a compact level label. */
function formatVoltageLevel(volts: number): string {
  if (volts >= 1000) return `${formatNumber(volts / 1000, volts % 1000 === 0 ? 0 : 1)} kV`
  return `${formatNumber(volts, 0)} V`
}

/**
 * Floating panel for api/internal/powerflow's weakly-meshed load-flow engine
 * (POST /analysis/meshed-load-flow, Phase 51 Tier 1 -- see
 * docs/phase-51 notes). It solves a radial spanning tree then closes each loop
 * (a ring main / closed link box) by compensation, returning the same per-node
 * voltage/loading as /load-flow plus a report of the loops it closed. Same
 * combined prompt+result shape and bottom-center slot as the other analysis
 * panels; the analyses are mutually exclusive, so feature-panel.tsx's triggers
 * clear each other's stores. On a genuinely radial feeder it degrades to an
 * ordinary load flow (zero loops, zero compensation passes).
 *
 * The prompt asks only for nominal_voltage_v (the same un-guessable input the
 * radial load flow needs); the result lists the closed loops above the node
 * table so a ring's two legs read as one energised feeder.
 */
export function MeshedLoadFlowPanel() {
  const armed = useMeshedLoadFlowStore((s) => s.armed)
  const loading = useMeshedLoadFlowStore((s) => s.loading)
  const result = useMeshedLoadFlowStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("11000")
  const fp = useFloatingPanel(640, "max-h-80")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code
  const barrierKeys = new Set((result?.barriers ?? []).map((b) => `${b.feature_class}:${b.feature_id}`))
  const overloadedKeys = new Set((result?.overloaded ?? []).map((o) => `${o.feature_class}:${o.feature_id}`))

  async function runMeshedLoadFlow() {
    const start = useMeshedLoadFlowStore.getState().armedStart
    if (!start) return
    const nominalVoltageV = Number(voltageInput)
    if (!Number.isFinite(nominalVoltageV) || nominalVoltageV <= 0) {
      useToastStore.getState().pushError("Nominal voltage must be a positive number.")
      return
    }

    useMeshedLoadFlowStore.getState().setLoading(true)
    try {
      const response = await apiFetch<MeshedLoadFlowResult>("/analysis/meshed-load-flow", {
        method: "POST",
        body: JSON.stringify({ start, nominal_voltage_v: nominalVoltageV }),
      })
      useMeshedLoadFlowStore.getState().setResult(response)
    } catch (err) {
      useMeshedLoadFlowStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run this meshed load flow.")
    }
  }

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <p className="truncate text-sm font-medium">Meshed load flow</p>
        </div>
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={() => useMeshedLoadFlowStore.getState().clear()}>
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
          <Button size="sm" className="ml-auto" onClick={runMeshedLoadFlow} disabled={loading}>
            {loading ? "Running…" : "Run"}
          </Button>
        </div>
      ) : (
        <div className={cn("flex flex-col overflow-hidden", fp.resized ? "min-h-0 flex-1" : "max-h-[21rem]")}>
          <div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 border-b px-3 py-1.5 text-xs">
            <span>{formatNumber(result.nominal_voltage_v, 0)} V nominal</span>
            <span>
              {result.loop_count} loop{result.loop_count === 1 ? "" : "s"}
              {result.loop_count > 0 ? ` · ${result.compensation_passes} pass${result.compensation_passes === 1 ? "" : "es"}` : " (radial)"}
            </span>
            <Button
              variant="ghost"
              size="sm"
              className="ml-auto h-6 px-2 text-xs"
              onClick={() => useMeshedLoadFlowStore.getState().rearm()}
            >
              Run again
            </Button>
          </div>

          <div className={cn("overflow-y-auto", fp.resized && "min-h-0 flex-1")}>
            {result.loops.length > 0 && (
              <div className="border-b">
                <table className="w-full text-sm">
                  <thead className="text-muted-foreground border-b text-left text-xs">
                    <tr>
                      <th className="bg-popover px-3 py-1.5 font-medium">Closed loop</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Loop Z (Ω)</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Residual</th>
                    </tr>
                  </thead>
                  <tbody>
                    {result.loops.map((loop, i) => (
                      <tr
                        key={`${loop.closer.feature_class}:${loop.closer.feature_id}:${i}`}
                        onClick={() =>
                          useSelectionStore.getState().select(loop.closer.feature_class, loop.closer.feature_id, { focus: true })
                        }
                        className={cn(
                          "hover:bg-accent cursor-pointer border-b align-top last:border-b-0",
                          !loop.compensated && "bg-muted/40",
                        )}
                        title={loop.note || (loop.compensated ? "Loop closed by compensation" : "Loop left open")}
                      >
                        <td className="px-3 py-1.5">
                          <div>{classNameFor(loop.closer.feature_class)}</div>
                          <div className="text-muted-foreground font-mono text-xs">{shortId(loop.closer.feature_id)}</div>
                          {!loop.compensated && <div className="text-muted-foreground text-xs">{loop.note || "left open"}</div>}
                        </td>
                        <td className="px-3 py-1.5 text-xs whitespace-nowrap">
                          {formatNumber(loop.loop_resistance_ohm, 3)} + j{formatNumber(loop.loop_reactance_ohm, 3)}
                          {!loop.impedance_resolved && <div className="text-muted-foreground">unresolved</div>}
                        </td>
                        <td className="px-3 py-1.5 text-xs whitespace-nowrap">{formatNumber(loop.residual_mismatch_v, 4)} V</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}

            <table className="w-full text-sm">
              <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
                <tr>
                  <th className="bg-popover px-3 py-1.5 font-medium">Feature</th>
                  <th className="bg-popover px-3 py-1.5 font-medium">Level</th>
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
                        {(node.circuit_ref || node.circuit_id) && (
                          <div className="text-muted-foreground text-xs">{node.circuit_ref || shortId(node.circuit_id!)}</div>
                        )}
                      </td>
                      <td className="text-muted-foreground px-3 py-1.5 text-xs whitespace-nowrap">
                        {node.nominal_base_v > 0 ? formatVoltageLevel(node.nominal_base_v) : "—"}
                      </td>
                      <td className="px-3 py-1.5">
                        <div>{formatNumber(node.voltage_v)}V</div>
                        <div className="text-muted-foreground text-xs">-{formatNumber(node.voltage_drop_pct, 2)}%</div>
                      </td>
                      <td className="px-3 py-1.5">{node.current_amps !== undefined ? `${formatNumber(node.current_amps)}A` : "—"}</td>
                      <td className="px-3 py-1.5">{node.loading_pct !== undefined ? `${formatNumber(node.loading_pct)}%` : "—"}</td>
                    </tr>
                  )
                })}
                {barrierKeys.size > 0 &&
                  result.barriers.map((b) => (
                    <tr
                      key={`${b.feature_class}:${b.feature_id}`}
                      onClick={() => useSelectionStore.getState().select(b.feature_class, b.feature_id, { focus: true })}
                      className="bg-muted/50 text-muted-foreground hover:bg-muted cursor-pointer border-b last:border-b-0"
                      title="Meshed load flow stopped here (open device)"
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
      {fp.resizeHandle}
    </div>
  )
}
