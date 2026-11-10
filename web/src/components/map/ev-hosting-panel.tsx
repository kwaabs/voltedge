import { useState } from "react"
import { X } from "lucide-react"

import { AnalysisExplanation, Formula } from "@/components/map/analysis-explanation"
import { FeatureInfoButton } from "@/components/map/feature-info-button"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { EvHostingCandidate, EvHostingResult } from "@/stores/ev-hosting-store"
import { useEvHostingStore } from "@/stores/ev-hosting-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/** Human label for the binding constraint the server names. */
function bindingLabel(constraint: string): string {
  switch (constraint) {
    case "undervoltage":
      return "Under-voltage"
    case "thermal":
      return "Thermal"
    case "pre_existing":
      return "Pre-existing"
    case "none":
      return "Unbounded"
    default:
      return constraint
  }
}

/**
 * The dynamic "how this was calculated" body for an EV feeder-headroom run.
 * Every figure is read off the actual result -- the feeder slack/floor/PF
 * assumed, the count of nodes sized, and the best node's headroom and the limit
 * that binds it -- so it verifies THIS study, not a generic description of the
 * method. Mirrors HostingExplanation with the sign and binding flipped.
 */
function EvHostingExplanation({
  result,
  classNameFor,
}: {
  result: EvHostingResult
  classNameFor: (code: string) => string
}) {
  const best = result.candidates.reduce<EvHostingCandidate | null>(
    (b, c) => (b === null || c.ev_hosting_capacity_kw > b.ev_hosting_capacity_kw ? c : b),
    null,
  )
  // The best node's headroom worked with this run's own numbers: the binding
  // constraint and how close it sits to its floor/ceiling, so the result checks
  // out.
  const bestFormula = (() => {
    if (!best) return null
    const feature = best.binding_feature ? classNameFor(best.binding_feature.feature_class) : "—"
    if (best.binding_constraint === "pre_existing") {
      return `headroom = 0 kW     (pre-existing violation at ${feature})
worst V  = ${formatNumber(best.worst_voltage_pu, 3)} pu, loading = ${formatNumber(best.worst_loading_pct)}% — no EV headroom`
    }
    if (best.unbounded) {
      return `headroom ≥ ${formatNumber(best.ev_hosting_capacity_kw, 0)} kW     (no limit binds up to the search cap)`
    }
    const margin =
      best.binding_constraint === "thermal"
        ? `${formatNumber(best.worst_loading_pct)}% vs ceiling 100%`
        : `${formatNumber(best.worst_voltage_pu, 3)} pu vs floor ${formatNumber(result.min_voltage_pu, 2)} pu`
    return `headroom = ${formatNumber(best.ev_hosting_capacity_kw, 0)} kW     (binding: ${bindingLabel(best.binding_constraint)} at ${feature})
found by bisection; ${margin}`
  })()
  return (
    <AnalysisExplanation study="ev-hosting">
      <p>
        <b>EV feeder headroom.</b> Added charging load is modelled as <b>positive demand</b> at a candidate node -- the
        under-voltage mirror of DER hosting. The feeder (slack at <b>{formatNumber(result.nominal_voltage_v, 0)} V</b>,
        load pf <b>{formatNumber(result.power_factor, 2)}</b>) is resolved once, then re-swept <b>in memory</b> as the
        added load grows -- a <b>bisection</b> pins the largest load that keeps every node at or above{" "}
        <b>{formatNumber(result.min_voltage_pu, 2)} pu</b> and every branch/transformer at or below <code>100%</code>{" "}
        loading.
      </p>
      <p>
        <b>
          {result.candidates.length} connection node{result.candidates.length === 1 ? "" : "s"}
        </b>{" "}
        sized. The binding limit at capacity is whichever sits closest to its bound -- <code>undervoltage</code> at the{" "}
        {formatNumber(result.min_voltage_pu, 2)}-pu floor, or <code>thermal</code> (ampacity). This headroom is the
        physical ceiling; <b>managed charging</b> lets an operator install more nameplate and stay within it by capping
        coincident draw.
      </p>
      {best && (
        <>
          <p>
            Best node: <b>{classNameFor(best.node.feature_class)}</b> absorbs{" "}
            <b>
              {formatNumber(best.ev_hosting_capacity_kw, 0)} kW{best.unbounded ? "+" : ""}
            </b>
            {best.binding_constraint === "pre_existing" ? (
              <> -- but {best.note ?? "the feeder already violates a limit, so there is no headroom here"}.</>
            ) : best.unbounded ? (
              <> -- no limit binds up to the search cap, so headroom is at least this.</>
            ) : (
              <>
                , bound by <b>{bindingLabel(best.binding_constraint)}</b>
                {best.binding_feature ? (
                  <>
                    {" "}
                    at <b>{classNameFor(best.binding_feature.feature_class)}</b>
                  </>
                ) : null}{" "}
                (worst <b>{formatNumber(best.worst_voltage_pu, 3)} pu</b>, <b>{formatNumber(best.worst_loading_pct)}%</b>{" "}
                loaded at that headroom).
              </>
            )}
          </p>
          {bestFormula && <Formula>{bestFormula}</Formula>}
        </>
      )}
    </AnalysisExplanation>
  )
}

/**
 * Floating panel for api/internal/powerflow's EV feeder-headroom study
 * (POST /analysis/ev-hosting, Phase 54). Armed on a feeder's source, it
 * binary-searches the largest additional charging load each connection node can
 * absorb before the first under-voltage or thermal overload binds -- the
 * under-voltage mirror of the DER hosting panel. Same combined prompt+result
 * shape and bottom-center slot as the other analysis panels; the analysis
 * workflows are mutually exclusive, so feature-panel.tsx's triggers clear each
 * other's stores.
 *
 * The prompt asks for nominal_voltage_v (the same un-guessable input load flow
 * needs), the added charging load's power factor (default unity), and
 * optionally the voltage floor; blank falls back to the server default (0.95).
 * The result lists each node (most-headroom first) with its capacity, the
 * binding constraint, and how close each limit sits at that capacity.
 */
export function EvHostingPanel() {
  const armed = useEvHostingStore((s) => s.armed)
  const loading = useEvHostingStore((s) => s.loading)
  const result = useEvHostingStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("400")
  const [pfInput, setPfInput] = useState("1.0")
  const [minPuInput, setMinPuInput] = useState("0.95")
  const fp = useFloatingPanel(704, "max-h-[24rem]")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  async function runEvHosting() {
    const source = useEvHostingStore.getState().armedStart
    if (!source) return
    const nominalVoltageV = Number(voltageInput)
    if (!Number.isFinite(nominalVoltageV) || nominalVoltageV <= 0) {
      useToastStore.getState().pushError("Nominal voltage must be a positive number.")
      return
    }
    const powerFactor = pfInput.trim() === "" ? undefined : Number(pfInput)
    if (powerFactor !== undefined && !(powerFactor > 0 && powerFactor <= 1)) {
      useToastStore.getState().pushError("Power factor must be between 0 and 1.")
      return
    }
    const minPu = minPuInput.trim() === "" ? undefined : Number(minPuInput)
    if (minPu !== undefined && !(minPu > 0)) {
      useToastStore.getState().pushError("Voltage floor must be a positive number.")
      return
    }

    useEvHostingStore.getState().setLoading(true)
    try {
      const response = await apiFetch<EvHostingResult>("/analysis/ev-hosting", {
        method: "POST",
        body: JSON.stringify({
          source,
          nominal_voltage_v: nominalVoltageV,
          power_factor: powerFactor,
          min_voltage_pu: minPu,
        }),
      })
      useEvHostingStore.getState().setResult(response)
    } catch (err) {
      useEvHostingStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run this EV headroom study.")
    }
  }

  // Most-headroom connection point first -- the whole question is "where absorbs
  // the most EV load", so sort by capacity descending (a stable copy).
  const ranked = result ? [...result.candidates].sort((a, b) => b.ev_hosting_capacity_kw - a.ev_hosting_capacity_kw) : []

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <p className="truncate text-sm font-medium">EV feeder headroom</p>
        </div>
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={() => useEvHostingStore.getState().clear()}>
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
            <label className="text-muted-foreground text-xs">Load pf</label>
            <input
              type="number"
              step="0.01"
              className={cn(inputClass, "w-20")}
              value={pfInput}
              onChange={(e) => setPfInput(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-muted-foreground text-xs">Floor (pu)</label>
            <input
              type="number"
              step="0.01"
              className={cn(inputClass, "w-20")}
              value={minPuInput}
              onChange={(e) => setMinPuInput(e.target.value)}
            />
          </div>
          <Button size="sm" className="ml-auto" onClick={runEvHosting} disabled={loading}>
            {loading ? "Running…" : "Run"}
          </Button>
        </div>
      ) : (
        <>
          <div className={cn("flex flex-col overflow-hidden", fp.resized ? "min-h-0 flex-1" : "max-h-[21rem]")}>
            <div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 border-b px-3 py-1.5 text-xs">
              <span>{formatNumber(result.nominal_voltage_v, 0)} V nominal</span>
              <span>pf {formatNumber(result.power_factor, 2)}</span>
              <span>floor {formatNumber(result.min_voltage_pu, 2)} pu</span>
              <span>
                {ranked.length} node{ranked.length === 1 ? "" : "s"}
              </span>
              <Button variant="ghost" size="sm" className="ml-auto h-6 px-2 text-xs" onClick={() => useEvHostingStore.getState().rearm()}>
                Run again
              </Button>
            </div>
            <div className={cn("overflow-y-auto", fp.resized && "min-h-0 flex-1")}>
              {ranked.length === 0 ? (
                <p className="text-muted-foreground px-3 py-4 text-sm">No connection nodes on this feeder to size.</p>
              ) : (
                <table className="w-full text-sm">
                  <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
                    <tr>
                      <th className="bg-popover px-3 py-1.5 font-medium">Node</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">EV headroom</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Binds on</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">At capacity</th>
                    </tr>
                  </thead>
                  <tbody>
                    {ranked.map((c) => (
                      <EvHostingRow key={`${c.node.feature_class}:${c.node.feature_id}`} candidate={c} classNameFor={classNameFor} />
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </div>
          <EvHostingExplanation result={result} classNameFor={classNameFor} />
        </>
      )}
      {fp.resizeHandle}
    </div>
  )
}

/** One node's row: the node (click to select), its EV headroom kW, the binding
 * constraint (with the feature it acts on), and how close each limit sits at
 * that capacity. A pre-existing violation (0 headroom) is red; a healthy node
 * with headroom is green. */
function EvHostingRow({
  candidate,
  classNameFor,
}: {
  candidate: EvHostingCandidate
  classNameFor: (code: string) => string
}) {
  const preExisting = candidate.binding_constraint === "pre_existing"
  const hostable = candidate.ev_hosting_capacity_kw > 0
  return (
    <tr
      onClick={() => useSelectionStore.getState().locate(candidate.node.feature_class, candidate.node.feature_id, { focus: true })}
      onDoubleClick={() => useSelectionStore.getState().select(candidate.node.feature_class, candidate.node.feature_id, { focus: true })}
      className={cn(
        "hover:bg-accent cursor-pointer border-b align-top last:border-b-0 select-none",
        hostable && "bg-emerald-500/10 hover:bg-emerald-500/15",
        preExisting && "bg-destructive/10 hover:bg-destructive/15",
      )}
    >
      <td className="px-3 py-1.5">
        <div className="flex items-start gap-1">
          <FeatureInfoButton featureClass={candidate.node.feature_class} featureId={candidate.node.feature_id} className="-mt-0.5" />
          <div className="min-w-0">
            <div>{classNameFor(candidate.node.feature_class)}</div>
            <div className="text-muted-foreground font-mono text-xs">{shortId(candidate.node.feature_id)}</div>
          </div>
        </div>
      </td>
      <td className="px-3 py-1.5 whitespace-nowrap">
        <div className="font-medium">
          {formatNumber(candidate.ev_hosting_capacity_kw, 0)} kW{candidate.unbounded ? "+" : ""}
        </div>
      </td>
      <td className="px-3 py-1.5 text-xs">
        <div>{bindingLabel(candidate.binding_constraint)}</div>
        {candidate.binding_feature && (
          <div className="text-muted-foreground font-mono">{shortId(candidate.binding_feature.feature_id)}</div>
        )}
      </td>
      <td className="px-3 py-1.5 text-xs whitespace-nowrap">
        <div>{formatNumber(candidate.worst_voltage_pu, 3)} pu</div>
        <div className="text-muted-foreground">{formatNumber(candidate.worst_loading_pct)}% loaded</div>
      </td>
    </tr>
  )
}
