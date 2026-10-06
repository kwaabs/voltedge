import { useEffect, useState } from "react"
import { X } from "lucide-react"

import { AnalysisExplanation, Formula } from "@/components/map/analysis-explanation"
import { inputClass } from "@/components/map/field-utils"
import { LoadFlowNodesTable, type NodeColumn } from "@/components/map/load-flow-nodes-table"
import { Button } from "@/components/ui/button"
import type { FeatureRecord } from "@/hooks/use-feature"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { AutoLoadFlowResult, LoadFlowEngine } from "@/stores/auto-load-flow-store"
import { activeInner, useAutoLoadFlowStore } from "@/stores/auto-load-flow-store"
import type { LoadFlowNodeResult, LoadFlowResult } from "@/stores/load-flow-store"
import type { MeshedLoadFlowResult } from "@/stores/meshed-load-flow-store"
import type { NodalLoadFlowResult, NodalNodeResult } from "@/stores/nodal-load-flow-store"
import { useToastStore } from "@/stores/toast-store"

/** Node type across the three engines the router may pick: radial/meshed carry
 * LoadFlowNodeResult (with spanning-tree parent pointers), nodal carries
 * NodalNodeResult (no parents -> reads flat even in tree mode). */
type AutoNode = LoadFlowNodeResult | NodalNodeResult

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/** A node's own voltage base as a compact level label -- "11 kV" / "400 V" --
 * so MV nodes and the LV nodes below a transformer read apart at a glance. */
function formatVoltageLevel(volts: number): string {
  if (volts >= 1000) return `${formatNumber(volts / 1000, volts % 1000 === 0 ? 0 : 1)} kV`
  return `${formatNumber(volts, 0)} V`
}

/** Human labels for the engine the router chose, shown as a badge in the result
 * header (the whole point of the dispatch entry -- one item, engine revealed). */
const ENGINE_LABEL: Record<LoadFlowEngine, string> = {
  radial: "Radial ladder",
  meshed: "Mesh compensation",
  nodal: "Nodal Y-bus",
}

/**
 * Floating panel for the load-flow DISPATCH ROUTER (POST /analysis/load-flow-
 * auto, Phase 52 Slice S4 -- see docs/phase-52-nodal-powerflow.md). This is the
 * engine-AGNOSTIC entry: the caller no longer has to know whether the network is
 * a radial tree, a weak ring or a dense mesh. The router classifies the topology
 * once (buildTree's own radial/mesh verdict) and runs the right engine -- the
 * fast forward-backward ladder for a tree, Tier 1 mesh compensation for a few
 * loops, the Tier 2 nodal Y-bus for a dense mesh -- and the result header names
 * which engine actually ran and why.
 *
 * Same combined prompt+result shape and floating slot as every other analysis
 * panel; the analyses are mutually exclusive, so feature-panel.tsx's triggers
 * clear each other's stores. The prompt asks only for nominal_voltage_v (the
 * slack-bus voltage base, the same un-guessable input every load flow needs) --
 * a power user who wants explicit multi-slack / PV control still has the
 * dedicated "Nodal load flow" item. The result renders the columns every engine
 * shares (per-node voltage / drop / current / loading), so one table reads for
 * whichever engine the router picked.
 */
/**
 * The dynamic "how this was calculated" body for an auto (dispatch-router) run:
 * which engine the router auto-selected and WHY (the loop count / topology
 * classification it reported), followed by the key figures of the engine's own
 * result -- the worst (lowest-pu) node and the overload/barrier counts. Every
 * figure is read off THIS result, so it verifies the run rather than describing
 * the router generically.
 */
function AutoLoadFlowExplanation({
  result,
  inner,
  classNameFor,
}: {
  result: AutoLoadFlowResult
  inner: LoadFlowResult | MeshedLoadFlowResult | NodalLoadFlowResult
  classNameFor: (code: string) => string
}) {
  // All three engine results share these per-node fields, so the worst-node scan
  // reads them uniformly regardless of which engine the router picked.
  const nodes: Array<{ feature_class: string; voltage_v: number; voltage_drop_pct: number; nominal_base_v: number }> =
    inner.nodes
  const worst = nodes.reduce<(typeof nodes)[number] | null>((lo, n) => {
    if (n.nominal_base_v <= 0 || n.voltage_v <= 0) return lo
    if (lo === null || n.voltage_v / n.nominal_base_v < lo.voltage_v / lo.nominal_base_v) return n
    return lo
  }, null)
  const worstPu = worst ? worst.voltage_v / worst.nominal_base_v : 0
  const engineDetail =
    result.engine === "radial"
      ? "a forward/backward ladder sweep on the radial tree"
      : result.engine === "meshed"
        ? "Tier 1 weak-mesh loop compensation (radial spanning tree + circulating-power loop closure)"
        : "the Tier 2 nodal Y-bus solver over the general graph"

  return (
    <AnalysisExplanation study="load-flow-auto">
      <p>
        The <b>dispatch router</b> classifies the network by its independent <b>loop count</b> and auto-selects the
        engine. Here it found <b>{result.loop_count}</b> loop{result.loop_count === 1 ? "" : "s"} and ran the{" "}
        <b>{ENGINE_LABEL[result.engine]}</b> engine (<code>{result.engine}</code>) -- {engineDetail}.
      </p>
      <p>Router reason: {result.engine_reason}</p>
      {worst && (
        <p>
          <b>{inner.nodes.length}</b> node{inner.nodes.length === 1 ? "" : "s"} solved. The lowest voltage is{" "}
          <b>{formatNumber(worstPu, 3)} pu</b> ({formatNumber(worst.voltage_v)} V, -
          {formatNumber(worst.voltage_drop_pct, 2)}%) at {classNameFor(worst.feature_class)} (
          {formatVoltageLevel(worst.nominal_base_v)} base). Worked:
        </p>
      )}
      <Formula>{`routing:      loop_count = ${result.loop_count} → ${result.engine}${
        worst
          ? `\nworst node:   V_pu = V / V_base = ${formatNumber(worst.voltage_v)} V / ${formatNumber(worst.nominal_base_v, 0)} V = ${formatNumber(worstPu, 3)} pu`
          : ""
      }`}</Formula>
      {result.engine === "nodal" && result.nodal && !result.nodal.converged && (
        <p>
          The nodal solve <b>did not converge</b> after <b>{result.nodal.iterations}</b> iteration
          {result.nodal.iterations === 1 ? "" : "s"} (max mismatch{" "}
          <code>{result.nodal.max_mismatch_pu.toExponential(1)} pu</code>), so treat the voltages as unconverged.
        </p>
      )}
      {(inner.overloaded.length > 0 || inner.barriers.length > 0) && (
        <p>
          Honesty flags:{" "}
          {inner.overloaded.length > 0 && (
            <>
              <b>{inner.overloaded.length}</b> element(s) over 100% loading.{" "}
            </>
          )}
          {inner.barriers.length > 0 && (
            <>
              <b>{inner.barriers.length}</b> open device(s) stopped the walk.
            </>
          )}
        </p>
      )}
    </AnalysisExplanation>
  )
}

export function AutoLoadFlowPanel() {
  const armed = useAutoLoadFlowStore((s) => s.armed)
  const loading = useAutoLoadFlowStore((s) => s.loading)
  const result = useAutoLoadFlowStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("11000")
  // Each row's human-assigned name, resolved per feature the same way the nodal
  // panel does -- the router response carries only feature_class/feature_id.
  const [names, setNames] = useState<Record<string, string | null>>({})
  const fp = useFloatingPanel(720, "max-h-80")

  const inner = activeInner(result)

  useEffect(() => {
    if (!inner) {
      setNames({})
      return
    }
    let cancelled = false
    const refs = [
      ...inner.nodes.map((n) => ({ feature_class: n.feature_class, feature_id: n.feature_id })),
      ...inner.barriers,
    ]
    async function loadNames() {
      const entries = await Promise.all(
        refs.map(async (ref) => {
          const key = `${ref.feature_class}:${ref.feature_id}`
          try {
            const feature = await apiFetch<FeatureRecord>(`/features/${ref.feature_class}/${ref.feature_id}`)
            return [key, feature.name] as const
          } catch {
            return [key, null] as const
          }
        }),
      )
      if (!cancelled) setNames(Object.fromEntries(entries))
    }
    setNames({})
    loadNames()
    return () => {
      cancelled = true
    }
  }, [inner])

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code
  const overloadedKeys = new Set((inner?.overloaded ?? []).map((o) => `${o.feature_class}:${o.feature_id}`))

  // Trailing columns after the shared Feature column. The auto panel adds a
  // resolved Name column (the router response carries only class/id) ahead of the
  // per-node electrical readout every engine shares.
  const columns: NodeColumn<AutoNode>[] = [
    { header: "Name", cell: (node) => names[`${node.feature_class}:${node.feature_id}`] || "—" },
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
          <div>{node.voltage_v > 0 ? `${formatNumber(node.voltage_v)}V` : "—"}</div>
          <div className="text-muted-foreground text-xs">-{formatNumber(node.voltage_drop_pct, 2)}%</div>
        </>
      ),
    },
    { header: "Current", cell: (node) => (node.current_amps !== undefined ? `${formatNumber(node.current_amps)}A` : "—") },
    { header: "Loading", cell: (node) => (node.loading_pct !== undefined ? `${formatNumber(node.loading_pct)}%` : "—") },
  ]

  async function runAutoLoadFlow() {
    const start = useAutoLoadFlowStore.getState().armedStart
    if (!start) return
    const nominalVoltageV = Number(voltageInput)
    if (!Number.isFinite(nominalVoltageV) || nominalVoltageV <= 0) {
      useToastStore.getState().pushError("Nominal voltage must be a positive number.")
      return
    }

    useAutoLoadFlowStore.getState().setLoading(true)
    try {
      const response = await apiFetch<AutoLoadFlowResult>("/analysis/load-flow-auto", {
        method: "POST",
        body: JSON.stringify({ start, nominal_voltage_v: nominalVoltageV }),
      })
      useAutoLoadFlowStore.getState().setResult(response)
    } catch (err) {
      useAutoLoadFlowStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run this load flow.")
    }
  }

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <p className="truncate text-sm font-medium">Load flow (auto)</p>
        </div>
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={() => useAutoLoadFlowStore.getState().clear()}>
          <X className="size-3.5" />
          <span className="sr-only">Close</span>
        </Button>
      </div>

      {!result || !inner ? (
        <div className="flex flex-col gap-2 p-3">
          <p className="text-muted-foreground text-xs">
            Solves from the selected feature and auto-selects the engine by topology (radial ladder, mesh compensation, or
            nodal Y-bus).
          </p>
          <div className="flex items-center gap-2">
            <label className="text-muted-foreground shrink-0 text-sm">Nominal voltage (V)</label>
            <input
              type="number"
              className={cn(inputClass, "w-28")}
              value={voltageInput}
              onChange={(e) => setVoltageInput(e.target.value)}
              autoFocus
            />
            <Button size="sm" className="ml-auto" onClick={runAutoLoadFlow} disabled={loading}>
              {loading ? "Running…" : "Run"}
            </Button>
          </div>
        </div>
      ) : (
        <>
        <div className={cn("flex flex-col overflow-hidden", fp.resized ? "min-h-0 flex-1" : "max-h-[21rem]")}>
          <LoadFlowNodesTable
            nodes={inner.nodes as AutoNode[]}
            barriers={inner.barriers}
            overloadedKeys={overloadedKeys}
            rootKey={"start" in inner && inner.start ? `${inner.start.feature_class}:${inner.start.feature_id}` : ""}
            classNameFor={classNameFor}
            columns={columns}
            resized={fp.resized}
            toolbar={
              <>
                <span className="text-foreground shrink-0 rounded border px-1.5 py-0.5 font-medium">
                  {ENGINE_LABEL[result.engine]}
                </span>
                <span className="min-w-0 flex-1 truncate" title={result.engine_reason}>
                  {result.engine_reason}
                </span>
                <Button
                  variant="ghost"
                  size="sm"
                  className="ml-auto h-6 px-2 text-xs"
                  onClick={() => useAutoLoadFlowStore.getState().rearm()}
                >
                  Run again
                </Button>
              </>
            }
          />
        </div>
        <AutoLoadFlowExplanation result={result} inner={inner} classNameFor={classNameFor} />
        </>
      )}
      {fp.resizeHandle}
    </div>
  )
}
