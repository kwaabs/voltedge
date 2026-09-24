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
import type { StorageDispatchResult, StorageRecommendation } from "@/stores/storage-dispatch-store"
import { useStorageDispatchStore } from "@/stores/storage-dispatch-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/** A signed kW with an explicit + on a discharge, so charge/discharge reads
 * clearly at a glance (− is charging, + is discharging). */
function formatSignedKW(n: number): string {
  const s = formatNumber(n, 0)
  return n > 0 ? `+${s}` : s
}

/** The violation reduction a recommendation buys, the primary ranking key. */
function violationGain(r: StorageRecommendation): number {
  return r.violations_from - r.violations_to
}

/**
 * The dynamic "how this was calculated" body for a storage-dispatch run. Every
 * figure is read off the actual result -- the feeder slack / band assumed, the
 * base worst-low and worst-high voltage and band-violation count, the number of
 * batteries, and the best battery's recommended set-point and what it clears /
 * shaves -- so it verifies THIS study, not a generic method description.
 */
function StorageDispatchExplanation({
  result,
  classNameFor,
}: {
  result: StorageDispatchResult
  classNameFor: (code: string) => string
}) {
  // The battery whose move most reduces band violations, then most peak-shaves.
  const best = result.recommendations.reduce<StorageRecommendation | null>((b, r) => {
    if (b === null) return r
    const g = violationGain(r) - violationGain(b)
    if (g !== 0) return g > 0 ? r : b
    return r.peak_shave_kw > b.peak_shave_kw ? r : b
  }, null)
  const bestFormula = (() => {
    if (!best) return null
    const node = classNameFor(best.node.feature_class)
    return `dispatch ${formatSignedKW(best.dispatch_kw_from)} → ${formatSignedKW(best.dispatch_kw_to)} kW at ${node}   (${best.mode_to}, ±${formatNumber(best.rated_power_kw, 0)} kW rated)
worst lo ${formatNumber(best.min_voltage_pu_from, 3)} → ${formatNumber(best.min_voltage_pu_to, 3)} pu
worst hi ${formatNumber(best.max_voltage_pu_from, 3)} → ${formatNumber(best.max_voltage_pu_to, 3)} pu
source   ${formatNumber(best.source_kw_from, 0)} → ${formatNumber(best.source_kw_to, 0)} kW   (shave ${formatNumber(best.peak_shave_kw, 0)} kW)
in-band  ${best.violations_from} → ${best.violations_to} node(s) out`
  })()
  return (
    <AnalysisExplanation>
      <p>
        <b>Storage dispatch (battery-storage units).</b> A battery is modelled as a <b>signed injection</b> at its node --
        <b> discharging</b> lowers net demand (like a generator), <b>charging</b> raises it (an extra load). The feeder
        (slack at <b>{formatNumber(result.nominal_voltage_v, 0)} V</b>) is resolved once and swept as it stands (the{" "}
        <b>base case</b>, every battery at its live set-point). Then each battery's set-point is swept from full charge to
        full discharge in <b>{result.dispatch_steps}</b> steps a side (bounded by its PCS rating and gated by state of
        charge), and the set-point that leaves the fewest nodes out of band -- then, tie-broken, most shaves the source
        draw toward net-zero import -- is recommended (a lexicographic objective, so voltage is never traded for a smaller
        import).
      </p>
      <p>
        Base case: worst voltage <b>{formatNumber(result.base.min_voltage_pu, 3)} pu</b> low /{" "}
        <b>{formatNumber(result.base.max_voltage_pu, 3)} pu</b> high (band {formatNumber(result.min_voltage_pu, 2)}–
        {formatNumber(result.max_voltage_pu, 2)} pu, <b>{result.base.under_count + result.base.over_count}</b> node
        {result.base.under_count + result.base.over_count === 1 ? "" : "s"} out), source draw{" "}
        <b>{formatNumber(result.base.source_kw, 0)} kW</b>, series loss <b>{formatNumber(result.base.total_loss_kw)} kW</b>
        {result.base.losses_complete ? "" : " + (lower bound -- some branch had no R/X)"}. <b>{result.units.length}</b>{" "}
        batter{result.units.length === 1 ? "y" : "ies"} on the feeder.
      </p>
      {best && (
        <>
          <p>
            Best move: <b>{classNameFor(best.node.feature_class)}</b> to{" "}
            <b>
              {formatSignedKW(best.dispatch_kw_to)} kW ({best.mode_to})
            </b>{" "}
            {best.resolves_violations ? (
              <>clears every band violation</>
            ) : violationGain(best) > 0 ? (
              <>
                takes <b>{best.violations_from}</b> node{best.violations_from === 1 ? "" : "s"} out of band down to{" "}
                <b>{best.violations_to}</b>
              </>
            ) : (
              <>
                shaves the source draw by <b>{formatNumber(best.peak_shave_kw, 0)} kW</b>
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
 * Floating panel for api/internal/powerflow's storage-dispatch study
 * (POST /analysis/storage-dispatch). Armed on a feeder's source, it sweeps the
 * base case and then, for each battery-storage unit, tries every set-point from
 * full charge to full discharge and reports the one that best holds the feeder in
 * band and shaves the source draw -- the bidirectional-injection twin of the
 * tap-optimization (series-regulator) and volt-VAR (shunt-capacitor) panels. Same
 * combined prompt+result shape and bottom-center slot as the other analysis
 * panels; the analysis workflows are mutually exclusive, so feature-panel.tsx's
 * triggers clear each other's stores.
 *
 * The prompt asks for nominal_voltage_v (the same un-guessable input load flow
 * needs) and optionally the voltage floor / ceiling; blank falls back to the
 * server default band (0.95–1.05). The result lists each battery (biggest
 * violation reduction, then peak-shave, first) with the set-point / mode / voltage
 * and source-draw change it buys.
 */
export function StorageDispatchPanel() {
  const armed = useStorageDispatchStore((s) => s.armed)
  const loading = useStorageDispatchStore((s) => s.loading)
  const result = useStorageDispatchStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("11000")
  const [minPuInput, setMinPuInput] = useState("0.95")
  const [maxPuInput, setMaxPuInput] = useState("1.05")
  const fp = useFloatingPanel(860, "max-h-[24rem]")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  async function runStorageDispatch() {
    const source = useStorageDispatchStore.getState().armedStart
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

    useStorageDispatchStore.getState().setLoading(true)
    try {
      const response = await apiFetch<StorageDispatchResult>("/analysis/storage-dispatch", {
        method: "POST",
        body: JSON.stringify({
          source,
          nominal_voltage_v: nominalVoltageV,
          min_voltage_pu: minPu,
          max_voltage_pu: maxPu,
        }),
      })
      useStorageDispatchStore.getState().setResult(response)
    } catch (err) {
      useStorageDispatchStore.getState().setLoading(false)
      useToastStore
        .getState()
        .pushError(err instanceof ApiError ? err.message : "Could not run this storage-dispatch study.")
    }
  }

  // Biggest violation reduction first, then biggest peak-shave -- the same
  // lexicographic order the server's own set-point pick uses.
  const ranked = result
    ? [...result.recommendations].sort((a, b) => {
        const g = violationGain(b) - violationGain(a)
        if (g !== 0) return g
        return b.peak_shave_kw - a.peak_shave_kw
      })
    : []

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <p className="truncate text-sm font-medium">Storage dispatch (batteries)</p>
        </div>
        <Button
          variant="ghost"
          size="icon"
          className="size-6 shrink-0"
          onClick={() => useStorageDispatchStore.getState().clear()}
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
          <Button size="sm" className="ml-auto" onClick={runStorageDispatch} disabled={loading}>
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
              <span>base source {formatNumber(result.base.source_kw, 0)} kW</span>
              <span>{result.base.under_count + result.base.over_count} out of band</span>
              <span>
                {result.units.length} batter{result.units.length === 1 ? "y" : "ies"}
              </span>
              <Button
                variant="ghost"
                size="sm"
                className="ml-auto h-6 px-2 text-xs"
                onClick={() => useStorageDispatchStore.getState().rearm()}
              >
                Run again
              </Button>
            </div>
            <div className={cn("overflow-y-auto", fp.resized && "min-h-0 flex-1")}>
              {result.units.length === 0 ? (
                <p className="text-muted-foreground px-3 py-4 text-sm">
                  No battery-storage units on this feeder. Place a battery (e.g. the pole-mounted battery-storage
                  assembly) to dispatch for voltage support or peak-shaving.
                </p>
              ) : ranked.length === 0 ? (
                <p className="text-muted-foreground px-3 py-4 text-sm">
                  Every battery is already at its best set-point -- no dispatch improves the voltage band or source draw.
                </p>
              ) : (
                <table className="w-full text-sm">
                  <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
                    <tr>
                      <th className="bg-popover px-3 py-1.5 font-medium">Battery</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Dispatch (kW)</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Worst V (lo / hi)</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Out of band</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Source kW (shave)</th>
                    </tr>
                  </thead>
                  <tbody>
                    {ranked.map((r) => (
                      <StorageDispatchRow
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
          <StorageDispatchExplanation result={result} classNameFor={classNameFor} />
        </>
      )}
      {fp.resizeHandle}
    </div>
  )
}

/** One battery's row: the unit (click to select), the set-point from→to (and the
 * recommended mode), the worst-low/worst-high voltage before→after, the
 * out-of-band count before→after, and the source draw before→after with the
 * peak-shave it buys. A battery that clears every band violation is green; one
 * whose recommended move leaves it no better (or is unchanged) is neutral. */
function StorageDispatchRow({
  rec,
  classNameFor,
}: {
  rec: StorageRecommendation
  classNameFor: (code: string) => string
}) {
  const unchanged = rec.dispatch_kw_from === rec.dispatch_kw_to
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
          {formatSignedKW(rec.dispatch_kw_from)} → {formatSignedKW(rec.dispatch_kw_to)}
        </div>
        <div className="text-muted-foreground">{rec.mode_to}</div>
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
          {formatNumber(rec.source_kw_from, 0)} → {formatNumber(rec.source_kw_to, 0)}
        </div>
        <div className={cn("text-muted-foreground", rec.peak_shave_kw > 0 && "text-emerald-600 dark:text-emerald-400")}>
          {rec.peak_shave_kw > 0 ? `shave ${formatNumber(rec.peak_shave_kw, 0)} kW` : "no shave"}
        </div>
      </td>
    </tr>
  )
}
