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
import type { TapOptResult, TapRecommendation } from "@/stores/tap-optimization-store"
import { useTapOptimizationStore } from "@/stores/tap-optimization-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/** A signed tap with an explicit + on a boost, so a boost/buck reads clearly. */
function formatSignedInt(n: number): string {
  if (n > 0) return `+${n}`
  return `${n}`
}

/**
 * The dynamic "how this was calculated" body for a tap-optimization run. Every
 * figure is read off the actual result -- the feeder slack / band assumed, the
 * base worst-low and worst-high voltage and band-violation count, the number of
 * regulators, and the best regulator's recommended tap and the violations it
 * clears -- so it verifies THIS study, not a generic method description.
 */
function TapOptExplanation({
  result,
  classNameFor,
}: {
  result: TapOptResult
  classNameFor: (code: string) => string
}) {
  // The regulator whose move most reduces band violations (tie-broken by lift).
  const best = result.recommendations.reduce<TapRecommendation | null>(
    (b, r) => {
      if (b === null) return r
      const rGain = r.violations_from - r.violations_to
      const bGain = b.violations_from - b.violations_to
      return rGain > bGain ? r : b
    },
    null,
  )
  const bestFormula = (() => {
    if (!best) return null
    const node = classNameFor(best.node.feature_class)
    return `tap ${formatSignedInt(best.tap_from)} → ${formatSignedInt(best.tap_to)} at ${node}   (${formatNumber(best.step_pct, 3)}%/step)
ratio    ${formatNumber(best.ratio_from, 4)} → ${formatNumber(best.ratio_to, 4)}
worst lo ${formatNumber(best.min_voltage_pu_from, 3)} → ${formatNumber(best.min_voltage_pu_to, 3)} pu
worst hi ${formatNumber(best.max_voltage_pu_from, 3)} → ${formatNumber(best.max_voltage_pu_to, 3)} pu
in-band  ${best.violations_from} → ${best.violations_to} node(s) out`
  })()
  return (
    <AnalysisExplanation study="tap-optimization">
      <p>
        <b>Tap optimization (step voltage regulators).</b> A regulator is modelled as a <b>series autotransformer</b> --
        a turns ratio <b>1 + tap·step/100</b> applied to the voltage while keeping the <b>same voltage base</b> (a
        regulator does not change voltage class). The feeder (slack at <b>{formatNumber(result.nominal_voltage_v, 0)} V</b>
        ) is resolved once and swept as it stands (the <b>base case</b>, every regulator at its live tap). Then each
        regulator's tap is swept over <b>±{result.max_tap_steps} steps</b> in memory, and the tap that leaves the fewest
        nodes outside the band (tie-broken by the most centred profile) is recommended.
      </p>
      <p>
        Base case: worst voltage <b>{formatNumber(result.base.min_voltage_pu, 3)} pu</b> low /{" "}
        <b>{formatNumber(result.base.max_voltage_pu, 3)} pu</b> high (band {formatNumber(result.min_voltage_pu, 2)}–
        {formatNumber(result.max_voltage_pu, 2)} pu, <b>{result.base.under_count + result.base.over_count}</b> node
        {result.base.under_count + result.base.over_count === 1 ? "" : "s"} out), series loss{" "}
        <b>{formatNumber(result.base.total_loss_kw)} kW</b>
        {result.base.losses_complete ? "" : "+ (lower bound -- some branch had no R/X)"}. <b>{result.regulators.length}</b>{" "}
        regulator{result.regulators.length === 1 ? "" : "s"} on the feeder.
      </p>
      {best && (
        <>
          <p>
            Best move: <b>{classNameFor(best.node.feature_class)}</b> to tap <b>{formatSignedInt(best.tap_to)}</b>{" "}
            {best.resolves_violations ? (
              <>clears every band violation</>
            ) : (
              <>
                takes <b>{best.violations_from}</b> node{best.violations_from === 1 ? "" : "s"} out of band down to{" "}
                <b>{best.violations_to}</b>
              </>
            )}
            .
          </p>
          {bestFormula && <Formula>{bestFormula}</Formula>}
        </>
      )}
    </AnalysisExplanation>
  )
}

/**
 * Floating panel for api/internal/powerflow's tap-optimization study
 * (POST /analysis/tap-optimization). Armed on a feeder's source, it sweeps the
 * base case and then, for each series step voltage regulator, tries every tap in
 * ±max_tap_steps and reports the tap that best holds the feeder in band -- the
 * series-voltage twin of the volt-VAR (shunt-capacitor) panel. Same combined
 * prompt+result shape and bottom-center slot as the other analysis panels; the
 * analysis workflows are mutually exclusive, so feature-panel.tsx's triggers
 * clear each other's stores.
 *
 * The prompt asks for nominal_voltage_v (the same un-guessable input load flow
 * needs) and optionally the voltage floor / ceiling; blank falls back to the
 * server default band (0.95–1.05). The result lists each regulator (biggest
 * violation reduction first) with the tap / ratio / voltage change it buys.
 */
export function TapOptimizationPanel() {
  const armed = useTapOptimizationStore((s) => s.armed)
  const loading = useTapOptimizationStore((s) => s.loading)
  const result = useTapOptimizationStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("11000")
  const [minPuInput, setMinPuInput] = useState("0.95")
  const [maxPuInput, setMaxPuInput] = useState("1.05")
  const fp = useFloatingPanel(820, "max-h-[24rem]")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  async function runTapOptimization() {
    const source = useTapOptimizationStore.getState().armedStart
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
    const maxPu = maxPuInput.trim() === "" ? undefined : Number(maxPuInput)
    if (maxPu !== undefined && !(maxPu > 0)) {
      useToastStore.getState().pushError("Voltage ceiling must be a positive number.")
      return
    }

    useTapOptimizationStore.getState().setLoading(true)
    try {
      const response = await apiFetch<TapOptResult>("/analysis/tap-optimization", {
        method: "POST",
        body: JSON.stringify({
          source,
          nominal_voltage_v: nominalVoltageV,
          min_voltage_pu: minPu,
          max_voltage_pu: maxPu,
        }),
      })
      useTapOptimizationStore.getState().setResult(response)
    } catch (err) {
      useTapOptimizationStore.getState().setLoading(false)
      useToastStore
        .getState()
        .pushError(err instanceof ApiError ? err.message : "Could not run this tap-optimization study.")
    }
  }

  // Biggest violation reduction first (tie-broken by voltage centring is left to
  // the server's own tap pick; here we just order regulators by how much their
  // recommended move helps).
  const ranked = result
    ? [...result.recommendations].sort(
        (a, b) => b.violations_from - b.violations_to - (a.violations_from - a.violations_to),
      )
    : []

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <p className="truncate text-sm font-medium">Tap optimization (voltage regulators)</p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          className="size-6 shrink-0"
          onClick={() => useTapOptimizationStore.getState().clear()}
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
          <div className="flex flex-col gap-1">
            <label className="text-muted-foreground text-xs">Ceiling (pu)</label>
            <input
              type="number"
              step="0.01"
              className={cn(inputClass, "w-20")}
              value={maxPuInput}
              onChange={(e) => setMaxPuInput(e.target.value)}
            />
          </div>
          <Button size="sm" className="ml-auto" onClick={runTapOptimization} disabled={loading}>
            {loading ? "Running…" : "Run"}
          </Button>
        </div>
      ) : (
        <>
          <div className={cn("flex flex-col overflow-hidden", fp.resized ? "min-h-0 flex-1" : "max-h-[21rem]")}>
            <div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 border-b px-3 py-1.5 text-xs">
              <span>{formatNumber(result.nominal_voltage_v, 0)} V nominal</span>
              <span>
                base {formatNumber(result.base.min_voltage_pu, 3)}–{formatNumber(result.base.max_voltage_pu, 3)} pu
              </span>
              <span>base loss {formatNumber(result.base.total_loss_kw)} kW</span>
              <span>{result.base.under_count + result.base.over_count} out of band</span>
              <span>
                {result.regulators.length} regulator{result.regulators.length === 1 ? "" : "s"}
              </span>
              <Button
                variant="ghost"
                size="sm"
                className="ml-auto h-6 px-2 text-xs"
                onClick={() => useTapOptimizationStore.getState().rearm()}
              >
                Run again
              </Button>
            </div>
            <div className={cn("overflow-y-auto", fp.resized && "min-h-0 flex-1")}>
              {result.regulators.length === 0 ? (
                <p className="text-muted-foreground px-3 py-4 text-sm">
                  No voltage regulators on this feeder. Place a regulator (e.g. the pole-mounted voltage-regulator
                  assembly) to optimize tap settings.
                </p>
              ) : ranked.length === 0 ? (
                <p className="text-muted-foreground px-3 py-4 text-sm">
                  Every regulator is already at its best tap -- no move improves the voltage band.
                </p>
              ) : (
                <table className="w-full text-sm">
                  <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
                    <tr>
                      <th className="bg-popover px-3 py-1.5 font-medium">Regulator</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Tap</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Worst V (lo / hi)</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Out of band</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Source pf</th>
                    </tr>
                  </thead>
                  <tbody>
                    {ranked.map((r) => (
                      <TapOptRow
                        key={`${r.node.feature_class}:${r.node.feature_id}`}
                        rec={r}
                        classNameFor={classNameFor}
                      />
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </div>
          <TapOptExplanation result={result} classNameFor={classNameFor} />
        </>
      )}
      {fp.resizeHandle}
    </div>
  )
}

/** One regulator's row: the regulator (click to select), the tap from→to (and
 * ratio), the worst-low/worst-high voltage before→after, the out-of-band count
 * before→after, and the source power factor before→after. A regulator that
 * clears every band violation is green; one whose recommended move leaves it no
 * better (or is unchanged) is neutral. */
function TapOptRow({
  rec,
  classNameFor,
}: {
  rec: TapRecommendation
  classNameFor: (code: string) => string
}) {
  const unchanged = rec.tap_from === rec.tap_to
  return (
    <tr
      onClick={() => useSelectionStore.getState().select(rec.node.feature_class, rec.node.feature_id, { focus: true })}
      className={cn(
        "hover:bg-accent cursor-pointer border-b align-top last:border-b-0",
        rec.resolves_violations && "bg-emerald-500/10 hover:bg-emerald-500/15",
      )}
    >
      <td className="px-3 py-1.5">
        <div>{classNameFor(rec.node.feature_class)}</div>
        <div className="text-muted-foreground font-mono text-xs">{shortId(rec.node.feature_id)}</div>
      </td>
      <td className="px-3 py-1.5 text-xs whitespace-nowrap">
        <div className="font-medium">
          {formatSignedInt(rec.tap_from)} → {formatSignedInt(rec.tap_to)}
        </div>
        <div className="text-muted-foreground">
          {formatNumber(rec.ratio_from, 4)} → {formatNumber(rec.ratio_to, 4)}
        </div>
      </td>
      <td className="px-3 py-1.5 text-xs whitespace-nowrap">
        <div>
          {formatNumber(rec.min_voltage_pu_from, 3)} → {formatNumber(rec.min_voltage_pu_to, 3)}
        </div>
        <div className="text-muted-foreground">
          {formatNumber(rec.max_voltage_pu_from, 3)} → {formatNumber(rec.max_voltage_pu_to, 3)}
        </div>
      </td>
      <td className="px-3 py-1.5 text-xs whitespace-nowrap">
        <div className="font-medium">
          {rec.violations_from} → {rec.violations_to}
        </div>
        {unchanged && <div className="text-muted-foreground">no change</div>}
      </td>
      <td className="px-3 py-1.5 text-xs whitespace-nowrap">
        <div>
          {formatNumber(rec.source_pf_from, 3)} → {formatNumber(rec.source_pf_to, 3)}
        </div>
        {rec.resolves_violations && <div className="text-emerald-600 dark:text-emerald-400">clears violations</div>}
      </td>
    </tr>
  )
}
