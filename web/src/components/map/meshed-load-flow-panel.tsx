import { useState } from "react"
import { X } from "lucide-react"

import { AnalysisExplanation, Formula } from "@/components/map/analysis-explanation"
import { inputClass } from "@/components/map/field-utils"
import { LoadFlowNodesTable, type NodeColumn } from "@/components/map/load-flow-nodes-table"
import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { LoadFlowNodeResult } from "@/stores/load-flow-store"
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
/**
 * The dynamic "how this was calculated" body for a meshed-load-flow run: how
 * many loops were closed by compensation (vs left open), over how many passes,
 * the largest residual mismatch left across a break, and the worst (lowest-pu)
 * node -- every figure read off THIS result, so it verifies this answer rather
 * than describing the method generically. On a radial network (0 loops) it
 * states the solve degraded to an ordinary radial sweep.
 */
function MeshedLoadFlowExplanation({
  result,
  classNameFor,
}: {
  result: MeshedLoadFlowResult
  classNameFor: (code: string) => string
}) {
  const compensated = result.loops.filter((l) => l.compensated)
  const openLoops = result.loops.filter((l) => !l.compensated)
  const unresolved = result.loops.filter((l) => !l.impedance_resolved).length
  const worstResidual = compensated.reduce((m, l) => Math.max(m, Math.abs(l.residual_mismatch_v)), 0)
  const worst = result.nodes.reduce<(typeof result.nodes)[number] | null>((lo, n) => {
    if (n.nominal_base_v <= 0) return lo
    if (lo === null || n.voltage_v / n.nominal_base_v < lo.voltage_v / lo.nominal_base_v) return n
    return lo
  }, null)
  const worstPu = worst ? worst.voltage_v / worst.nominal_base_v : 0

  return (
    <AnalysisExplanation study="meshed-load-flow">
      <p>
        A weak-mesh solve: the network is first reduced to a <b>radial spanning tree</b> and swept
        forward/backward, then each loop is closed by <b>loop compensation</b> -- a circulating power is injected
        at the loop break so the voltage mismatch across it vanishes (<code>V_closer − V_existing → 0</code>).
      </p>
      {result.loop_count === 0 ? (
        <p>
          This network is <b>radial</b> -- <b>0</b> loops were found, so the solve degraded to an ordinary radial
          load flow (no compensation passes).
        </p>
      ) : (
        <p>
          <b>{result.loop_count}</b> loop{result.loop_count === 1 ? "" : "s"} found; <b>{compensated.length}</b>{" "}
          closed by compensation over <b>{result.compensation_passes}</b> pass
          {result.compensation_passes === 1 ? "" : "es"}
          {openLoops.length > 0 && (
            <>
              {" "}
              and <b>{openLoops.length}</b> left open (met at an open device / ideal-wire loop)
            </>
          )}
          .
          {worstResidual > 0 && (
            <>
              {" "}
              The largest residual mismatch left across a break is <b>{formatNumber(worstResidual, 4)} V</b>{" "}
              (<code>≈0</code> = the loop closed cleanly).
            </>
          )}
        </p>
      )}
      {worst && (
        <>
          <p>
            <b>{result.nodes.length}</b> node{result.nodes.length === 1 ? "" : "s"} solved. The lowest voltage is{" "}
            <b>{formatNumber(worstPu, 3)} pu</b> ({formatNumber(worst.voltage_v)} V, -
            {formatNumber(worst.voltage_drop_pct, 2)}%) at {classNameFor(worst.feature_class)} (
            {formatVoltageLevel(worst.nominal_base_v)} base). Worked:
          </p>
          <Formula>{`${
            result.loop_count > 0
              ? `compensation:  max |V_existing − V_closer| = ${formatNumber(worstResidual, 4)} V   after ${result.compensation_passes} pass${result.compensation_passes === 1 ? "" : "es"}   (target → 0)\n`
              : ""
          }worst node:    V_pu = V / V_base = ${formatNumber(worst.voltage_v)} V / ${formatNumber(worst.nominal_base_v, 0)} V = ${formatNumber(worstPu, 3)} pu`}</Formula>
        </>
      )}
      {(result.overloaded.length > 0 || unresolved > 0 || result.barriers.length > 0) && (
        <p>
          Honesty flags:{" "}
          {result.overloaded.length > 0 && (
            <>
              <b>{result.overloaded.length}</b> conductor(s) over 100% loading.{" "}
            </>
          )}
          {unresolved > 0 && (
            <>
              <b>{unresolved}</b> loop(s) had a branch with no resolved impedance, so their compensation is
              approximate.{" "}
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

export function MeshedLoadFlowPanel() {
  const armed = useMeshedLoadFlowStore((s) => s.armed)
  const loading = useMeshedLoadFlowStore((s) => s.loading)
  const result = useMeshedLoadFlowStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("11000")
  const fp = useFloatingPanel(640, "max-h-80")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code
  const overloadedKeys = new Set((result?.overloaded ?? []).map((o) => `${o.feature_class}:${o.feature_id}`))

  // Trailing columns after the shared Feature column -- same per-node readout the
  // basic load-flow panel shows; the meshed panel only adds the loops table above.
  const columns: NodeColumn<LoadFlowNodeResult>[] = [
    {
      header: "Level",
      cell: (node) => (
        <span className="text-muted-foreground text-xs whitespace-nowrap">
          {node.nominal_base_v > 0 ? formatVoltageLevel(node.nominal_base_v) : "—"}
        </span>
      ),
    },
    {
      header: "Voltage",
      cell: (node) => (
        <>
          <div>{formatNumber(node.voltage_v)}V</div>
          <div className="text-muted-foreground text-xs">-{formatNumber(node.voltage_drop_pct, 2)}%</div>
        </>
      ),
    },
    { header: "Current", cell: (node) => (node.current_amps !== undefined ? `${formatNumber(node.current_amps)}A` : "—") },
    { header: "Loading", cell: (node) => (node.loading_pct !== undefined ? `${formatNumber(node.loading_pct)}%` : "—") },
  ]

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
        <>
        <div className={cn("flex flex-col overflow-hidden", fp.resized ? "min-h-0 flex-1" : "max-h-[21rem]")}>
          <LoadFlowNodesTable
            nodes={result.nodes}
            barriers={result.barriers}
            overloadedKeys={overloadedKeys}
            rootKey={`${result.start.feature_class}:${result.start.feature_id}`}
            classNameFor={classNameFor}
            columns={columns}
            featureSubline={(node) =>
              node.circuit_ref || node.circuit_id ? node.circuit_ref || shortId(node.circuit_id!) : null
            }
            overloadTitle="Exceeds this conductor's own ampacity"
            resized={fp.resized}
            toolbar={
              <>
                <span>{formatNumber(result.nominal_voltage_v, 0)} V nominal</span>
                <span>
                  {result.loop_count} loop{result.loop_count === 1 ? "" : "s"}
                  {result.loop_count > 0
                    ? ` · ${result.compensation_passes} pass${result.compensation_passes === 1 ? "" : "es"}`
                    : " (radial)"}
                </span>
                <Button
                  variant="ghost"
                  size="sm"
                  className="ml-auto h-6 px-2 text-xs"
                  onClick={() => useMeshedLoadFlowStore.getState().rearm()}
                >
                  Run again
                </Button>
              </>
            }
            preTable={
              result.loops.length > 0 ? (
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
              ) : null
            }
          />
        </div>
        <MeshedLoadFlowExplanation result={result} classNameFor={classNameFor} />
        </>
      )}
      {fp.resizeHandle}
    </div>
  )
}
