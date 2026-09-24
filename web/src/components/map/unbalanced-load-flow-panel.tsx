import { useState } from "react"
import { X } from "lucide-react"

import { AnalysisExplanation, Formula } from "@/components/map/analysis-explanation"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { UnbalancedLoadFlowResult, UnbalancedNodeResult } from "@/stores/unbalanced-load-flow-store"
import { useUnbalancedLoadFlowStore } from "@/stores/unbalanced-load-flow-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/** The three phases in canonical order, so a row always shows A, then B, then C
 * in the same column positions even when a node energizes only a subset. */
const PHASE_ORDER = ["A", "B", "C"] as const

/** The worst (highest) voltage unbalance across every node that reported one --
 * the headline figure for the run, read straight off the result. */
function worstUnbalance(result: UnbalancedLoadFlowResult): { node: UnbalancedNodeResult; pct: number } | null {
  let worst: { node: UnbalancedNodeResult; pct: number } | null = null
  for (const n of result.nodes) {
    if (n.voltage_unbalance_pct === undefined) continue
    if (worst === null || n.voltage_unbalance_pct > worst.pct) worst = { node: n, pct: n.voltage_unbalance_pct }
  }
  return worst
}

/**
 * The dynamic "how this was calculated" body for an unbalanced (three-phase)
 * run. Every figure is read off the actual result -- the nominal base, the count
 * of nodes solved, and the single worst node's phase voltages and unbalance --
 * so it verifies THIS study, not a generic description of the method.
 */
function UnbalancedExplanation({
  result,
  classNameFor,
}: {
  result: UnbalancedLoadFlowResult
  classNameFor: (code: string) => string
}) {
  const worst = worstUnbalance(result)
  const worstFormula = (() => {
    if (!worst) return null
    const byPhase = new Map(worst.node.phases.map((p) => [p.phase, p.voltage_v]))
    const va = byPhase.get("A")
    const vb = byPhase.get("B")
    const vc = byPhase.get("C")
    if (va === undefined || vb === undefined || vc === undefined) return null
    const mean = (va + vb + vc) / 3
    return `Va = ${formatNumber(va)} V,  Vb = ${formatNumber(vb)} V,  Vc = ${formatNumber(vc)} V
mean = ${formatNumber(mean)} V     unbalance = max|Vφ − mean| / mean = ${formatNumber(worst.pct, 2)}%`
  })()
  return (
    <AnalysisExplanation>
      <p>
        <b>Three-phase (unbalanced) load flow.</b> Phases <b>A/B/C</b> are solved as three <b>decoupled</b> ladders on
        the same radial tree the balanced sweep walks. Each conductor's own <code>phase</code> designation narrows the
        energized phase set going down the tree, so a single-phase lateral loads only its one phase; every load splits
        its demand across the phases it is actually connected to (a <code>SERVICE_POINT</code> inherits the phases of the
        conductor feeding it). Per phase, the drop is the same{" "}
        <code>ΔV ≈ (P·R + Q·X) / V</code> the balanced engine uses.
      </p>
      <p>
        <b>{result.nodes.length}</b> node{result.nodes.length === 1 ? "" : "s"} solved at{" "}
        <b>{formatNumber(result.nominal_voltage_v, 0)} V</b> nominal ({result.iterations} passes). Where all three phases
        are present, the <b>neutral return current</b> is the phasor sum of the three phase currents (at their nominal
        0/−120/+120° angles) and the <b>voltage unbalance</b> is the NEMA figure -- max deviation of any phase from the
        three-phase mean. This is a Tier-1 estimate: the three phases carry <b>no mutual coupling</b> (that needs
        conductor geometry the model does not hold), exactly as the balanced sweep drops the branch current angle.
      </p>
      {worst && (
        <>
          <p>
            Worst unbalance: <b>{classNameFor(worst.node.feature_class)}</b> at{" "}
            <b>{formatNumber(worst.pct, 2)}%</b>
            {worst.pct > 2 ? (
              <> -- above the 2% planning limit, so this node is flagged.</>
            ) : (
              <> -- within the 2% planning limit.</>
            )}
          </p>
          {worstFormula && <Formula>{worstFormula}</Formula>}
        </>
      )}
    </AnalysisExplanation>
  )
}

/**
 * Floating panel for api/internal/powerflow's unbalanced (three-phase) load flow
 * (POST /analysis/unbalanced-load-flow, Phase 55 Tier 1). Armed on a feeder's
 * source, it solves phases A/B/C as three decoupled ladders and reports per-node
 * Va/Vb/Vc, the voltage unbalance between them, and the neutral return current --
 * the per-phase twin of the balanced load-flow panel. Same combined prompt+result
 * shape and bottom-center slot as the other analysis panels; the analysis
 * workflows are mutually exclusive, so feature-panel.tsx's triggers clear each
 * other's stores.
 *
 * The prompt asks only for nominal_voltage_v (the same un-guessable input the
 * balanced load flow needs) -- the per-phase split is driven entirely by the
 * model's own conductor `phase` fields. The result lists each node (most-unbalanced
 * first) with its per-phase voltages, unbalance %, and neutral current.
 */
export function UnbalancedLoadFlowPanel() {
  const armed = useUnbalancedLoadFlowStore((s) => s.armed)
  const loading = useUnbalancedLoadFlowStore((s) => s.loading)
  const result = useUnbalancedLoadFlowStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("400")
  const fp = useFloatingPanel(720, "max-h-[24rem]")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  async function runUnbalanced() {
    const start = useUnbalancedLoadFlowStore.getState().armedStart
    if (!start) return
    const nominalVoltageV = Number(voltageInput)
    if (!Number.isFinite(nominalVoltageV) || nominalVoltageV <= 0) {
      useToastStore.getState().pushError("Nominal voltage must be a positive number.")
      return
    }

    useUnbalancedLoadFlowStore.getState().setLoading(true)
    try {
      const response = await apiFetch<UnbalancedLoadFlowResult>("/analysis/unbalanced-load-flow", {
        method: "POST",
        body: JSON.stringify({ start, nominal_voltage_v: nominalVoltageV }),
      })
      useUnbalancedLoadFlowStore.getState().setResult(response)
    } catch (err) {
      useUnbalancedLoadFlowStore.getState().setLoading(false)
      useToastStore
        .getState()
        .pushError(err instanceof ApiError ? err.message : "Could not run this unbalanced load flow.")
    }
  }

  // Most-unbalanced node first -- the whole question is "where does imbalance
  // hurt". Nodes without an unbalance figure (single-/two-phase) sort to the
  // bottom, keeping their tree order among themselves (a stable copy).
  const ranked = result
    ? [...result.nodes].sort((a, b) => (b.voltage_unbalance_pct ?? -1) - (a.voltage_unbalance_pct ?? -1))
    : []

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <p className="truncate text-sm font-medium">Unbalanced load flow</p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          className="size-6 shrink-0"
          onClick={() => useUnbalancedLoadFlowStore.getState().clear()}
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
          <Button size="sm" className="ml-auto" onClick={runUnbalanced} disabled={loading}>
            {loading ? "Running…" : "Run"}
          </Button>
        </div>
      ) : (
        <>
          <div className={cn("flex flex-col overflow-hidden", fp.resized ? "min-h-0 flex-1" : "max-h-[21rem]")}>
            <div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 border-b px-3 py-1.5 text-xs">
              <span>{formatNumber(result.nominal_voltage_v, 0)} V nominal</span>
              <span>
                {result.nodes.length} node{result.nodes.length === 1 ? "" : "s"}
              </span>
              {result.unbalanced.length > 0 && (
                <span className="text-amber-600 dark:text-amber-500">
                  {result.unbalanced.length} over 2%
                </span>
              )}
              <Button
                variant="ghost"
                size="sm"
                className="ml-auto h-6 px-2 text-xs"
                onClick={() => useUnbalancedLoadFlowStore.getState().rearm()}
              >
                Run again
              </Button>
            </div>
            <div className={cn("overflow-y-auto", fp.resized && "min-h-0 flex-1")}>
              {ranked.length === 0 ? (
                <p className="text-muted-foreground px-3 py-4 text-sm">No nodes on this feeder to solve.</p>
              ) : (
                <table className="w-full text-sm">
                  <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
                    <tr>
                      <th className="bg-popover px-3 py-1.5 font-medium">Node</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Va</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Vb</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Vc</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Unbalance</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Neutral</th>
                    </tr>
                  </thead>
                  <tbody>
                    {ranked.map((n) => (
                      <UnbalancedRow key={`${n.feature_class}:${n.feature_id}`} node={n} classNameFor={classNameFor} />
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </div>
          <UnbalancedExplanation result={result} classNameFor={classNameFor} />
        </>
      )}
      {fp.resizeHandle}
    </div>
  )
}

/** One node's row: the node (click to select), its three phase voltages (a blank
 * cell where a phase isn't energized), the voltage unbalance %, and the neutral
 * return current. A node over the 2% planning limit is amber. */
function UnbalancedRow({
  node,
  classNameFor,
}: {
  node: UnbalancedNodeResult
  classNameFor: (code: string) => string
}) {
  const byPhase = new Map(node.phases.map((p) => [p.phase, p]))
  const overLimit = (node.voltage_unbalance_pct ?? 0) > 2
  return (
    <tr
      onClick={() => useSelectionStore.getState().select(node.feature_class, node.feature_id, { focus: true })}
      className={cn(
        "hover:bg-accent cursor-pointer border-b align-top last:border-b-0",
        overLimit && "bg-amber-500/10 hover:bg-amber-500/15",
      )}
    >
      <td className="px-3 py-1.5">
        <div>{classNameFor(node.feature_class)}</div>
        <div className="text-muted-foreground font-mono text-xs">{shortId(node.feature_id)}</div>
      </td>
      {PHASE_ORDER.map((ph) => {
        const p = byPhase.get(ph)
        return (
          <td key={ph} className="px-3 py-1.5 text-xs whitespace-nowrap">
            {p ? (
              <>
                <div>{formatNumber(p.voltage_v)} V</div>
                <div className="text-muted-foreground">{formatNumber(p.voltage_drop_pct)}% drop</div>
              </>
            ) : (
              <span className="text-muted-foreground">—</span>
            )}
          </td>
        )
      })}
      <td className="px-3 py-1.5 text-xs whitespace-nowrap">
        {node.voltage_unbalance_pct === undefined ? (
          <span className="text-muted-foreground">—</span>
        ) : (
          <span className={cn("font-medium", overLimit && "text-amber-600 dark:text-amber-500")}>
            {formatNumber(node.voltage_unbalance_pct, 2)}%
          </span>
        )}
      </td>
      <td className="px-3 py-1.5 text-xs whitespace-nowrap">
        {node.neutral_current_amps === undefined ? (
          <span className="text-muted-foreground">—</span>
        ) : (
          <span>{formatNumber(node.neutral_current_amps)} A</span>
        )}
      </td>
    </tr>
  )
}
