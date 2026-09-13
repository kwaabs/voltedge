import { useEffect, useState } from "react"
import { X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import type { FeatureRecord } from "@/hooks/use-feature"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { AutoLoadFlowResult, LoadFlowEngine } from "@/stores/auto-load-flow-store"
import { activeInner, useAutoLoadFlowStore } from "@/stores/auto-load-flow-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

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
  const barrierKeys = new Set((inner?.barriers ?? []).map((b) => `${b.feature_class}:${b.feature_id}`))

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
        <div className={cn("flex flex-col overflow-hidden", fp.resized ? "min-h-0 flex-1" : "max-h-[21rem]")}>
          <div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 border-b px-3 py-1.5 text-xs">
            <span className="text-foreground rounded border px-1.5 py-0.5 font-medium">{ENGINE_LABEL[result.engine]}</span>
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
          </div>
          <div className={cn("overflow-y-auto", fp.resized && "min-h-0 flex-1")}>
            <table className="w-full text-sm">
              <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
                <tr>
                  <th className="bg-popover px-3 py-1.5 font-medium">Feature</th>
                  <th className="bg-popover px-3 py-1.5 font-medium">Name</th>
                  <th className="bg-popover px-3 py-1.5 font-medium">Level</th>
                  <th className="bg-popover px-3 py-1.5 font-medium">Voltage</th>
                  <th className="bg-popover px-3 py-1.5 font-medium">Current</th>
                  <th className="bg-popover px-3 py-1.5 font-medium">Loading</th>
                </tr>
              </thead>
              <tbody>
                {inner.nodes.map((node) => {
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
                      title={overloaded ? "Exceeds this element's own rating" : undefined}
                    >
                      <td className="px-3 py-1.5">
                        <div>{classNameFor(node.feature_class)}</div>
                        <div className="text-muted-foreground font-mono text-xs">{shortId(node.feature_id)}</div>
                      </td>
                      <td className="px-3 py-1.5">{names[key] || "—"}</td>
                      <td className="text-muted-foreground px-3 py-1.5 text-xs whitespace-nowrap">
                        {node.nominal_base_v > 0 ? formatVoltageLevel(node.nominal_base_v) : "—"}
                      </td>
                      <td className="px-3 py-1.5">
                        <div>{node.voltage_v > 0 ? `${formatNumber(node.voltage_v)}V` : "—"}</div>
                        <div className="text-muted-foreground text-xs">-{formatNumber(node.voltage_drop_pct, 2)}%</div>
                      </td>
                      <td className="px-3 py-1.5">{node.current_amps !== undefined ? `${formatNumber(node.current_amps)}A` : "—"}</td>
                      <td className="px-3 py-1.5">{node.loading_pct !== undefined ? `${formatNumber(node.loading_pct)}%` : "—"}</td>
                    </tr>
                  )
                })}
                {barrierKeys.size > 0 &&
                  inner.barriers.map((b) => (
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
                      <td className="px-3 py-1.5">{names[`${b.feature_class}:${b.feature_id}`] || "—"}</td>
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
