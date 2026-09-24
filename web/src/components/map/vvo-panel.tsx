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
import type { VvoAction, VvoResult } from "@/stores/vvo-store"
import { useVvoStore } from "@/stores/vvo-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/** Title-case a control kind for the read-out (capacitor -> Capacitor). */
function kindLabel(kind: string): string {
  return kind.length === 0 ? kind : kind[0].toUpperCase() + kind.slice(1)
}

/**
 * The dynamic "how this was calculated" body for a VVO run. Every figure is read
 * off the actual result -- the feeder slack / band, the number of controls and
 * how many moved, the coordinate-descent rounds and convergence, and the base vs
 * optimised worst voltage / band violations / loss -- so it verifies THIS study.
 */
function VvoExplanation({ result }: { result: VvoResult }) {
  const moved = result.actions.filter((a) => a.changed).length
  const baseViol = result.base.under_count + result.base.over_count
  const optViol = result.optimized.under_count + result.optimized.over_count
  const formula = `voltage  ${formatNumber(result.base.min_voltage_pu, 3)}–${formatNumber(result.base.max_voltage_pu, 3)} → ${formatNumber(result.optimized.min_voltage_pu, 3)}–${formatNumber(result.optimized.max_voltage_pu, 3)} pu
out-of-band  ${baseViol} → ${optViol} node(s)
series loss  ${formatNumber(result.base.total_loss_kw)} → ${formatNumber(result.optimized.total_loss_kw)} kW
source draw  ${formatNumber(result.base.source_kw, 0)} → ${formatNumber(result.optimized.source_kw, 0)} kW`
  return (
    <AnalysisExplanation>
      <p>
        <b>Volt-VAR Optimization (VVO).</b> Where the volt-VAR, tap-optimization and storage-dispatch studies each tune{" "}
        <i>one</i> class of control, VVO co-optimizes <b>all</b> of them together -- every switched capacitor, step
        voltage regulator and battery on the feeder -- against one objective: hold every node inside the band{" "}
        <b>
          {formatNumber(result.min_voltage_pu, 2)}–{formatNumber(result.max_voltage_pu, 2)} pu
        </b>{" "}
        first, then minimize series loss. The feeder (slack at <b>{formatNumber(result.nominal_voltage_v, 0)} V</b>) is
        resolved once and the controls are swept by <b>coordinate descent</b> -- each control tuned in turn with the
        others held fixed, repeated until a full pass changes nothing.
      </p>
      <p>
        <b>{result.actions.length}</b> control{result.actions.length === 1 ? "" : "s"} on the feeder, <b>{moved}</b>{" "}
        moved, over <b>{result.rounds}</b> round{result.rounds === 1 ? "" : "s"}{" "}
        {result.converged ? <>(converged)</> : <>(hit the round cap before converging)</>}.{" "}
        {baseViol > 0 && optViol === 0 ? (
          <>The optimization clears every band violation.</>
        ) : optViol < baseViol ? (
          <>
            It takes <b>{baseViol}</b> node{baseViol === 1 ? "" : "s"} out of band down to <b>{optViol}</b>.
          </>
        ) : result.optimized.total_loss_kw < result.base.total_loss_kw ? (
          <>
            The band was already met; it trims series loss by{" "}
            <b>{formatNumber(result.base.total_loss_kw - result.optimized.total_loss_kw)} kW</b>.
          </>
        ) : (
          <>The feeder is already at its best -- no control move improves it.</>
        )}
      </p>
      <Formula>{formula}</Formula>
    </AnalysisExplanation>
  )
}

/**
 * Floating panel for api/internal/powerflow's combined Volt-VAR Optimization
 * (POST /analysis/vvo). Armed on a feeder's source, it co-optimizes every
 * capacitor, regulator and battery on the feeder at once and reports the base vs
 * optimized feeder state plus the setting it chose for each control -- the
 * synthesis of the three single-control panels (volt-VAR, tap-optimization,
 * storage-dispatch). Same combined prompt+result shape and bottom-center slot as
 * the other analysis panels; the analysis workflows are mutually exclusive, so
 * feature-panel.tsx's triggers clear each other's stores.
 */
export function VvoPanel() {
  const armed = useVvoStore((s) => s.armed)
  const loading = useVvoStore((s) => s.loading)
  const result = useVvoStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("11000")
  const [minPuInput, setMinPuInput] = useState("0.95")
  const [maxPuInput, setMaxPuInput] = useState("1.05")
  const fp = useFloatingPanel(880, "max-h-[24rem]")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  async function runVvo() {
    const source = useVvoStore.getState().armedStart
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

    useVvoStore.getState().setLoading(true)
    try {
      const response = await apiFetch<VvoResult>("/analysis/vvo", {
        method: "POST",
        body: JSON.stringify({
          source,
          nominal_voltage_v: nominalVoltageV,
          min_voltage_pu: minPu,
          max_voltage_pu: maxPu,
        }),
      })
      useVvoStore.getState().setResult(response)
    } catch (err) {
      useVvoStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run this VVO study.")
    }
  }

  const baseViol = result ? result.base.under_count + result.base.over_count : 0
  const optViol = result ? result.optimized.under_count + result.optimized.over_count : 0

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <p className="truncate text-sm font-medium">Volt-VAR Optimization</p>
        </div>
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={() => useVvoStore.getState().clear()}>
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
          <Button size="sm" className="ml-auto" onClick={runVvo} disabled={loading}>
            {loading ? "Running…" : "Run"}
          </Button>
        </div>
      ) : (
        <>
          <div className={cn("flex flex-col overflow-hidden", fp.resized ? "min-h-0 flex-1" : "max-h-[21rem]")}>
            <div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 border-b px-3 py-1.5 text-xs">
              <span>{formatNumber(result.nominal_voltage_v, 0)} V nominal</span>
              <span>
                {formatNumber(result.base.min_voltage_pu, 3)}–{formatNumber(result.base.max_voltage_pu, 3)} →{" "}
                {formatNumber(result.optimized.min_voltage_pu, 3)}–{formatNumber(result.optimized.max_voltage_pu, 3)} pu
              </span>
              <span>
                out of band {baseViol} → {optViol}
              </span>
              <span>
                loss {formatNumber(result.base.total_loss_kw)} → {formatNumber(result.optimized.total_loss_kw)} kW
              </span>
              <span>
                {result.rounds} round{result.rounds === 1 ? "" : "s"} {result.converged ? "(converged)" : "(capped)"}
              </span>
              <Button
                variant="ghost"
                size="sm"
                className="ml-auto h-6 px-2 text-xs"
                onClick={() => useVvoStore.getState().rearm()}
              >
                Run again
              </Button>
            </div>
            <div className={cn("overflow-y-auto", fp.resized && "min-h-0 flex-1")}>
              {result.actions.length === 0 ? (
                <p className="text-muted-foreground px-3 py-4 text-sm">
                  No controllable Volt-VAR assets on this feeder. Place a switched capacitor bank, a voltage regulator or
                  a battery-storage unit to optimize.
                </p>
              ) : (
                <table className="w-full text-sm">
                  <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
                    <tr>
                      <th className="bg-popover px-3 py-1.5 font-medium">Control</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Type</th>
                      <th className="bg-popover px-3 py-1.5 font-medium">Setting</th>
                    </tr>
                  </thead>
                  <tbody>
                    {result.actions.map((a) => (
                      <VvoRow key={`${a.node.feature_class}:${a.node.feature_id}`} action={a} classNameFor={classNameFor} />
                    ))}
                  </tbody>
                </table>
              )}
            </div>
          </div>
          <VvoExplanation result={result} />
        </>
      )}
      {fp.resizeHandle}
    </div>
  )
}

/** One control's row: the device (click to select), its type, and the setting
 * from→to. A control the optimization moved is highlighted; one left unchanged is
 * neutral and shows a single setting. */
function VvoRow({
  action,
  classNameFor,
}: {
  action: VvoAction
  classNameFor: (code: string) => string
}) {
  return (
    <tr
      onClick={() =>
        useSelectionStore.getState().select(action.node.feature_class, action.node.feature_id, { focus: true })
      }
      className={cn(
        "hover:bg-accent cursor-pointer border-b align-top last:border-b-0",
        action.changed && "bg-emerald-500/10 hover:bg-emerald-500/15",
      )}
    >
      <td className="px-3 py-1.5">
        <div>{classNameFor(action.node.feature_class)}</div>
        <div className="text-muted-foreground font-mono text-xs">{shortId(action.node.feature_id)}</div>
      </td>
      <td className="px-3 py-1.5 text-xs">{kindLabel(action.kind)}</td>
      <td className="px-3 py-1.5 text-xs whitespace-nowrap">
        {action.changed ? (
          <div className="font-medium">
            {action.setting_from} → {action.setting_to}
          </div>
        ) : (
          <div className="text-muted-foreground">{action.setting_to} (unchanged)</div>
        )}
      </td>
    </tr>
  )
}
