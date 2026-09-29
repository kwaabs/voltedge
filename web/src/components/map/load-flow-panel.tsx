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
import type { LoadFlowNodeResult, LoadFlowResult } from "@/stores/load-flow-store"
import { useLoadFlowStore } from "@/stores/load-flow-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/** A node's own voltage base (nominal_base_v) as a compact level label --
 * "11 kV" / "400 V" -- so MV nodes and the LV nodes below a DIST_XFMR read
 * apart at a glance (the sweep now rebases voltage across the transformer,
 * so a single result spans both levels). */
function formatVoltageLevel(volts: number): string {
  if (volts >= 1000) return `${formatNumber(volts / 1000, volts % 1000 === 0 ? 0 : 1)} kV`
  return `${formatNumber(volts, 0)} V`
}

/**
 * The dynamic "how this was calculated" body for a load-flow run. Every figure
 * below (the slack voltage, the worst node's per-unit voltage, the overload and
 * barrier counts) is read off the actual result, so it verifies THIS sweep, not
 * a generic description of the backward-forward method.
 */
function LoadFlowExplanation({
  result,
  classNameFor,
}: {
  result: LoadFlowResult
  classNameFor: (code: string) => string
}) {
  // Worst node = lowest per-unit voltage on its own base (voltage_v /
  // nominal_base_v), the one the operator cares about first.
  const rated = result.nodes.filter((n) => n.nominal_base_v > 0)
  const worst = rated.reduce<(typeof rated)[number] | null>(
    (lo, n) => (lo === null || n.voltage_v / n.nominal_base_v < lo.voltage_v / lo.nominal_base_v ? n : lo),
    null,
  )
  const worstPu = worst ? worst.voltage_v / worst.nominal_base_v : undefined

  return (
    <AnalysisExplanation>
      <p>
        Radial <b>backward-forward sweep</b> from the slack source at{" "}
        <b>{formatNumber(result.nominal_voltage_v, 0)} V</b> nominal. Each pass accumulates load current
        back to the source (<code>backward</code>), then re-computes the volt-drop <code>ΔV = I·Z</code> forward to
        every node, converging in <b>{result.iterations}</b> iteration{result.iterations === 1 ? "" : "s"}.
      </p>
      <p>
        <b>{result.nodes.length} node(s)</b> reached. Per node the sweep reports voltage (V and pu of its own base),
        volt drop, branch current, and conductor loading.
      </p>
      {worst && worstPu !== undefined && (
        <>
          <p>
            Lowest voltage is <b>{formatNumber(worstPu, 3)} pu</b> (
            <b>{formatNumber(worst.voltage_v)} V</b> on a {formatVoltageLevel(worst.nominal_base_v)} base,{" "}
            -{formatNumber(worst.voltage_drop_pct, 2)}% drop) at {classNameFor(worst.feature_class)}
            {worstPu < 0.95 && (
              <>
                {" "}
                -- <b>below the 0.95 pu</b> normal band
              </>
            )}
            . Worked at that node:
          </p>
          <Formula>{`V_pu = V / V_base
     = ${formatNumber(worst.voltage_v)} V / ${formatNumber(worst.nominal_base_v, 0)} V
     = ${formatNumber(worstPu, 3)} pu
drop = V_base − V = ${formatNumber(worst.nominal_base_v, 0)} − ${formatNumber(worst.voltage_v)} = ${formatNumber(worst.nominal_base_v - worst.voltage_v)} V   (-${formatNumber(worst.voltage_drop_pct, 2)}%)`}</Formula>
        </>
      )}
      {(result.overloaded.length > 0 || result.barriers.length > 0) && (
        <p>
          Honesty flags:{" "}
          {result.overloaded.length > 0 && (
            <>
              <b>{result.overloaded.length}</b> conductor(s) exceed their own <code>ampacity</code> (loading &gt;
              100%).{" "}
            </>
          )}
          {result.barriers.length > 0 && (
            <>
              <b>{result.barriers.length}</b> open device(s) stopped the sweep -- nodes beyond them are un-energised
              and absent from the table.
            </>
          )}
        </p>
      )}
    </AnalysisExplanation>
  )
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
  const fp = useFloatingPanel(576, "max-h-80")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code
  const overloadedKeys = new Set((result?.overloaded ?? []).map((o) => `${o.feature_class}:${o.feature_id}`))

  // Trailing columns after the shared Feature column -- the load-flow family's
  // per-node electrical readout. LoadFlowNodesTable renders these identically in
  // both the flat and tree views.
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
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <p className="truncate text-sm font-medium">Load flow</p>
        </div>
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
        <>
        <div className={cn("flex flex-col overflow-hidden", fp.resized ? "min-h-0 flex-1" : "max-h-[19rem]")}>
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
            barrierLabel="Stopped here (open device)"
            resized={fp.resized}
            toolbar={
              <>
                <span>Nominal {formatNumber(result.nominal_voltage_v, 0)}V</span>
                <Button
                  variant="ghost"
                  size="sm"
                  className="ml-auto h-6 px-2 text-xs"
                  onClick={() => useLoadFlowStore.getState().rearm()}
                >
                  Run again
                </Button>
              </>
            }
          />
        </div>
        <LoadFlowExplanation result={result} classNameFor={classNameFor} />
        </>
      )}
      {fp.resizeHandle}
    </div>
  )
}
