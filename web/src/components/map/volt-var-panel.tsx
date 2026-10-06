import { useState } from "react"
import { X } from "lucide-react"

import { AnalysisExplanation, Formula } from "@/components/map/analysis-explanation"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"
import type { VoltVarRecommendation, VoltVarResult } from "@/stores/volt-var-store"
import { useVoltVarStore } from "@/stores/volt-var-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/** A signed delta with an explicit + on a gain, so a voltage rise / loss cut
 * reads unambiguously against an over-compensating bank's disbenefit. */
function formatSigned(n: number, digits = 1): string {
  const s = formatNumber(Math.abs(n), digits)
  if (n > 0) return `+${s}`
  if (n < 0) return `−${s}`
  return s
}

/**
 * The dynamic "how this was calculated" body for a volt-VAR run. Every figure is
 * read off the actual result -- the feeder slack/floor assumed, the base worst
 * voltage / loss / source pf, the count of switchable banks, and the best
 * candidate's voltage lift -- so it verifies THIS study, not a generic method
 * description. Mirrors the other analysis explanations.
 */
function VoltVarExplanation({
  result,
  classNameFor,
}: {
  result: VoltVarResult
  classNameFor: (code: string) => string
}) {
  const best = result.recommendations.reduce<VoltVarRecommendation | null>(
    (b, r) => (b === null || r.voltage_rise_pu > b.voltage_rise_pu ? r : b),
    null,
  )
  const bestFormula = (() => {
    if (!best) return null
    const node = classNameFor(best.node.feature_class)
    return `switch IN ${formatNumber(best.rated_kvar, 0)} kVAr at ${node}
worst V  ${formatNumber(best.min_voltage_pu_from, 3)} → ${formatNumber(best.min_voltage_pu_to, 3)} pu   (${formatSigned(best.voltage_rise_pu, 3)} pu)
losses   ${formatNumber(best.total_loss_kw_from)} → ${formatNumber(best.total_loss_kw_to)} kW   (${formatSigned(best.loss_reduction_kw)} kW)
src pf   ${formatNumber(best.source_pf_from, 3)} → ${formatNumber(best.source_pf_to, 3)}`
  })()
  return (
    <AnalysisExplanation study="volt-var">
      <p>
        <b>Volt-VAR (shunt-capacitor switching).</b> A capacitor bank is modelled as <b>leading reactive injection</b> --
        negative reactive demand at its node, the reactive twin of DER hosting. The feeder (slack at{" "}
        <b>{formatNumber(result.nominal_voltage_v, 0)} V</b>) is resolved once and swept as it stands (the <b>base case</b>
        , which already includes every fixed bank and any bank already switched in). Then each <b>switchable</b> bank that
        is currently <b>out of circuit</b> is re-swept <b>in memory</b> as though switched in, and the change it buys is
        read off.
      </p>
      <p>
        Base case: worst voltage <b>{formatNumber(result.base.min_voltage_pu, 3)} pu</b> (floor{" "}
        {formatNumber(result.min_voltage_pu, 2)} pu, <b>{result.base.violation_count}</b> node
        {result.base.violation_count === 1 ? "" : "s"} below it), series loss{" "}
        <b>{formatNumber(result.base.total_loss_kw)} kW</b>
        {result.base.losses_complete ? "" : "+ (lower bound -- some branch had no R/X)"}, source pf{" "}
        <b>{formatNumber(result.base.source_pf, 3)}</b>. <b>{result.banks.length}</b> capacitor bank
        {result.banks.length === 1 ? "" : "s"} on the feeder; <b>{result.recommendations.length}</b> switchable and out of
        circuit.
      </p>
      {best && (
        <>
          <p>
            Best switch-in: <b>{classNameFor(best.node.feature_class)}</b> lifts the worst voltage by{" "}
            <b>{formatSigned(best.voltage_rise_pu, 3)} pu</b> and changes losses by{" "}
            <b>{formatSigned(best.loss_reduction_kw)} kW</b>
            {best.resolves_under_voltage ? <> -- and clears every under-voltage violation</> : null}.
          </p>
          {bestFormula && <Formula>{bestFormula}</Formula>}
        </>
      )}
    </AnalysisExplanation>
  )
}

/**
 * Floating panel for api/internal/powerflow's volt-VAR study
 * (POST /analysis/volt-var). Armed on a feeder's source, it sweeps the base case
 * and then, for each switched capacitor bank currently out of circuit, re-sweeps
 * it in and reports the voltage lift and loss reduction switching it on would
 * buy -- the reactive twin of the hosting / EV-headroom panels. Same combined
 * prompt+result shape and bottom-center slot as the other analysis panels; the
 * analysis workflows are mutually exclusive, so feature-panel.tsx's triggers
 * clear each other's stores.
 *
 * The prompt asks for nominal_voltage_v (the same un-guessable input load flow
 * needs) and optionally the voltage floor; blank falls back to the server
 * default (0.95). The result lists each switchable bank (biggest lift first)
 * with the voltage / loss / power-factor change closing it would buy.
 */
export function VoltVarPanel() {
  const armed = useVoltVarStore((s) => s.armed)
  const loading = useVoltVarStore((s) => s.loading)
  const result = useVoltVarStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("11000")
  const [minPuInput, setMinPuInput] = useState("0.95")
  const fp = useFloatingPanel(760, "max-h-[24rem]")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  async function runVoltVar() {
    const source = useVoltVarStore.getState().armedStart
    if (!source) return
    const nominalVoltageV = Number(voltageInput)
    if (!Number.isFinite(nominalVoltageV) || nominalVoltageV <= 0) {
      useToastStore.getState().pushError("Nominal voltage must be a positive number.")
      return
    }
    const minPu = minPuInput.trim() === "" ? undefined : Number(minPuInput)
    if (minPu !== undefined && !(minPu > 0)) {
      useToastStore.getState().pushError("Voltage floor must be a positive number.")
      return
    }

    useVoltVarStore.getState().setLoading(true)
    try {
      const response = await apiFetch<VoltVarResult>("/analysis/volt-var", {
        method: "POST",
        body: JSON.stringify({
          source,
          nominal_voltage_v: nominalVoltageV,
          min_voltage_pu: minPu,
        }),
      })
      useVoltVarStore.getState().setResult(response)
    } catch (err) {
      useVoltVarStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run this volt-VAR study.")
    }
  }

  // Biggest voltage lift first -- the whole question is "which bank helps most",
  // so sort by voltage_rise_pu descending (a stable copy).
  const ranked = result ? [...result.recommendations].sort((a, b) => b.voltage_rise_pu - a.voltage_rise_pu) : []
  const activeBanks = result ? result.banks.filter((b) => b.active) : []

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <p className="truncate text-sm font-medium">Volt-VAR (capacitor switching)</p>
        </div>
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={() => useVoltVarStore.getState().clear()}>
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
            <label className="text-muted-foreground text-xs">Floor (pu)</label>
            <input
              type="number"
              step="0.01"
              className={cn(inputClass, "w-20")}
              value={minPuInput}
              onChange={(e) => setMinPuInput(e.target.value)}
            />
          </div>
          <Button size="sm" className="ml-auto" onClick={runVoltVar} disabled={loading}>
            {loading ? "Running…" : "Run"}
          </Button>
        </div>
      ) : (
        <>
          <div className={cn("flex flex-col overflow-hidden", fp.resized ? "min-h-0 flex-1" : "max-h-[21rem]")}>
            <div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 border-b px-3 py-1.5 text-xs">
              <span>{formatNumber(result.nominal_voltage_v, 0)} V nominal</span>
              <span>base worst {formatNumber(result.base.min_voltage_pu, 3)} pu</span>
              <span>base loss {formatNumber(result.base.total_loss_kw)} kW</span>
              <span>base pf {formatNumber(result.base.source_pf, 3)}</span>
              <span>
                {result.banks.length} bank{result.banks.length === 1 ? "" : "s"}
                {activeBanks.length > 0 ? ` (${activeBanks.length} active)` : ""}
              </span>
              <Button variant="ghost" size="sm" className="ml-auto h-6 px-2 text-xs" onClick={() => useVoltVarStore.getState().rearm()}>
                Run again
              </Button>
            </div>
            <div className={cn("overflow-y-auto", fp.resized && "min-h-0 flex-1")}>
              {result.banks.length === 0 ? (
                <p className="text-muted-foreground px-3 py-4 text-sm">
                  No capacitor banks on this feeder. Place a switched bank (e.g. the pole-mounted capacitor-bank assembly)
                  to size volt-VAR support.
                </p>
              ) : ranked.length === 0 ? (
                <p className="text-muted-foreground px-3 py-4 text-sm">
                  Every capacitor bank on this feeder is already in circuit -- nothing left to switch in. The{" "}
                  {activeBanks.length} active bank{activeBanks.length === 1 ? "" : "s"} are already reflected in the base
                  case above.
                </p>
              ) : (
                <table className="w-full text-sm">
                  <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
                    <tr>
                      <th className="bg-popover px-3 py-1.5 font-medium">Bank</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Rating</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Worst V</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Losses</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Source pf</th>
                    </tr>
                  </thead>
                  <tbody>
                    {ranked.map((r) => (
                      <VoltVarRow key={`${r.node.feature_class}:${r.node.feature_id}`} rec={r} classNameFor={classNameFor} />
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </div>
          <VoltVarExplanation result={result} classNameFor={classNameFor} />
        </>
      )}
      {fp.resizeHandle}
    </div>
  )
}

/** One switchable bank's row: the bank (click to select), its kVAr rating, the
 * worst-voltage before→after switching it in, the loss before→after, and the
 * source power factor before→after. A bank that clears an under-voltage
 * violation is green; one that makes losses worse (over-compensation) is red. */
function VoltVarRow({
  rec,
  classNameFor,
}: {
  rec: VoltVarRecommendation
  classNameFor: (code: string) => string
}) {
  const worsensLoss = rec.loss_reduction_kw < 0
  return (
    <tr
      onClick={() => useSelectionStore.getState().select(rec.node.feature_class, rec.node.feature_id, { focus: true })}
      className={cn(
        "hover:bg-accent cursor-pointer border-b align-top last:border-b-0",
        rec.resolves_under_voltage && "bg-emerald-500/10 hover:bg-emerald-500/15",
        worsensLoss && "bg-destructive/10 hover:bg-destructive/15",
      )}
    >
      <td className="px-3 py-1.5">
        <div>{classNameFor(rec.node.feature_class)}</div>
        <div className="text-muted-foreground font-mono text-xs">{shortId(rec.node.feature_id)}</div>
      </td>
      <td className="px-3 py-1.5 whitespace-nowrap font-medium">{formatNumber(rec.rated_kvar, 0)} kVAr</td>
      <td className="px-3 py-1.5 text-xs whitespace-nowrap">
        <div>
          {formatNumber(rec.min_voltage_pu_from, 3)} → {formatNumber(rec.min_voltage_pu_to, 3)} pu
        </div>
        <div className="text-muted-foreground">{formatSigned(rec.voltage_rise_pu, 3)} pu</div>
      </td>
      <td className="px-3 py-1.5 text-xs whitespace-nowrap">
        <div>
          {formatNumber(rec.total_loss_kw_from)} → {formatNumber(rec.total_loss_kw_to)} kW
        </div>
        <div className="text-muted-foreground">{formatSigned(rec.loss_reduction_kw)} kW</div>
      </td>
      <td className="px-3 py-1.5 text-xs whitespace-nowrap">
        <div>
          {formatNumber(rec.source_pf_from, 3)} → {formatNumber(rec.source_pf_to, 3)}
        </div>
        {rec.resolves_under_voltage && <div className="text-emerald-600 dark:text-emerald-400">clears under-voltage</div>}
      </td>
    </tr>
  )
}
