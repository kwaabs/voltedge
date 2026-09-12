import { useState } from "react"
import { X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { ContingencyResult, RestorationOption } from "@/stores/contingency-store"
import { useContingencyStore } from "@/stores/contingency-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/**
 * Floating panel for api/internal/powerflow's N-1 / backfeed restoration
 * study (POST /analysis/contingency). Armed on a donor feeder's source, it
 * sweeps the feeder as it stands and then, for every normally-open tie it
 * halts at, re-sweeps with that tie hypothetically closed to report the
 * outaged load the donor could pick up and any post-transfer voltage
 * violation or thermal overload. Same combined prompt+result shape and
 * bottom-center slot as the other analysis panels; the analysis workflows
 * are mutually exclusive, so feature-panel.tsx's triggers clear each
 * other's stores.
 *
 * The prompt asks for nominal_voltage_v (the same un-guessable input load
 * flow needs) and optionally the acceptable per-unit voltage band; blank
 * band fields fall back to the server default (0.95..1.05). The result
 * lists each candidate tie ranked NORMALLY_OPEN-first, with the restorable
 * load and whether the move is feasible as-is.
 */
export function ContingencyPanel() {
  const armed = useContingencyStore((s) => s.armed)
  const loading = useContingencyStore((s) => s.loading)
  const result = useContingencyStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("11000")
  const [minPuInput, setMinPuInput] = useState("0.95")
  const [maxPuInput, setMaxPuInput] = useState("1.05")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  async function runContingency() {
    const source = useContingencyStore.getState().armedStart
    if (!source) return
    const nominalVoltageV = Number(voltageInput)
    if (!Number.isFinite(nominalVoltageV) || nominalVoltageV <= 0) {
      useToastStore.getState().pushError("Nominal voltage must be a positive number.")
      return
    }
    const minPu = minPuInput.trim() === "" ? undefined : Number(minPuInput)
    const maxPu = maxPuInput.trim() === "" ? undefined : Number(maxPuInput)
    if ((minPu !== undefined && !(minPu > 0)) || (maxPu !== undefined && !(maxPu > 0))) {
      useToastStore.getState().pushError("Voltage limits must be positive numbers.")
      return
    }
    if (minPu !== undefined && maxPu !== undefined && minPu > maxPu) {
      useToastStore.getState().pushError("Min voltage must not exceed max voltage.")
      return
    }

    useContingencyStore.getState().setLoading(true)
    try {
      const response = await apiFetch<ContingencyResult>("/analysis/contingency", {
        method: "POST",
        body: JSON.stringify({
          source,
          nominal_voltage_v: nominalVoltageV,
          min_voltage_pu: minPu,
          max_voltage_pu: maxPu,
        }),
      })
      useContingencyStore.getState().setResult(response)
    } catch (err) {
      useContingencyStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run this contingency study.")
    }
  }

  return (
    <div className="bg-popover text-popover-foreground fixed bottom-10 left-1/2 z-40 max-h-[24rem] w-[42rem] max-w-[calc(100vw-2rem)] -translate-x-1/2 overflow-hidden rounded-md border shadow-lg">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <p className="truncate text-sm font-medium">Contingency &amp; backfeed restoration</p>
        <Button
          variant="ghost"
          size="icon"
          className="size-6 shrink-0"
          onClick={() => useContingencyStore.getState().clear()}
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
            <label className="text-muted-foreground text-xs">Min (pu)</label>
            <input
              type="number"
              step="0.01"
              className={cn(inputClass, "w-20")}
              value={minPuInput}
              onChange={(e) => setMinPuInput(e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-muted-foreground text-xs">Max (pu)</label>
            <input
              type="number"
              step="0.01"
              className={cn(inputClass, "w-20")}
              value={maxPuInput}
              onChange={(e) => setMaxPuInput(e.target.value)}
            />
          </div>
          <Button size="sm" className="ml-auto" onClick={runContingency} disabled={loading}>
            {loading ? "Running…" : "Run"}
          </Button>
        </div>
      ) : (
        <div className="flex max-h-[21rem] flex-col overflow-hidden">
          {/* Base-case summary -- the donor feeder's own load and reach before
              any transfer, plus the candidate-tie count. */}
          <div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 border-b px-3 py-1.5 text-xs">
            <span>Base load {formatNumber(result.base_load_kw)} kW</span>
            <span>{result.base_node_count} nodes</span>
            <span>
              {result.options.length} candidate tie{result.options.length === 1 ? "" : "s"}
            </span>
            <Button
              variant="ghost"
              size="sm"
              className="ml-auto h-6 px-2 text-xs"
              onClick={() => useContingencyStore.getState().rearm()}
            >
              Run again
            </Button>
          </div>
          <div className="overflow-y-auto">
            {result.options.length === 0 ? (
              <p className="text-muted-foreground px-3 py-4 text-sm">
                No open boundary ties on this feeder -- nothing to restore from here.
              </p>
            ) : (
              <table className="w-full text-sm">
                <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
                  <tr>
                    <th className="bg-popover px-3 py-1.5 font-medium">Tie</th>
                    <th className="bg-popover px-3 py-1.5 font-medium">Restores</th>
                    <th className="bg-popover px-3 py-1.5 font-medium">Worst (pu)</th>
                    <th className="bg-popover px-3 py-1.5 font-medium">Result</th>
                  </tr>
                </thead>
                <tbody>
                  {result.options.map((opt) => (
                    <ContingencyRow key={`${opt.tie.feature_class}:${opt.tie.feature_id}`} opt={opt} classNameFor={classNameFor} />
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      )}
    </div>
  )
}

/** One candidate tie's row: the tie feature (click to select), the load and
 * node count it would restore, the worst post-transfer per-unit voltage, and
 * a feasibility verdict -- feasible (green), a violation/overload count
 * (red), or the explanatory note (loop, no gain). */
function ContingencyRow({
  opt,
  classNameFor,
}: {
  opt: RestorationOption
  classNameFor: (code: string) => string
}) {
  const problems = opt.violations.length + opt.overloaded.length
  return (
    <tr
      onClick={() => useSelectionStore.getState().select(opt.tie.feature_class, opt.tie.feature_id, { focus: true })}
      className={cn(
        "hover:bg-accent cursor-pointer border-b align-top last:border-b-0",
        opt.feasible && "bg-emerald-500/10 hover:bg-emerald-500/15",
        problems > 0 && "bg-destructive/10 hover:bg-destructive/15",
      )}
    >
      <td className="px-3 py-1.5">
        <div className="flex items-center gap-1.5">
          <span>{classNameFor(opt.tie.feature_class)}</span>
          {opt.normal_state === "NORMALLY_OPEN" && (
            <span className="rounded bg-amber-500/20 px-1 text-[10px] text-amber-700" title="Declared normally-open point">
              NOP
            </span>
          )}
        </div>
        <div className="text-muted-foreground font-mono text-xs">{shortId(opt.tie.feature_id)}</div>
      </td>
      <td className="px-3 py-1.5 whitespace-nowrap">
        {opt.restored_node_count > 0 ? (
          <>
            <div>{formatNumber(opt.restored_load_kw)} kW</div>
            <div className="text-muted-foreground text-xs">
              {opt.restored_node_count} node{opt.restored_node_count === 1 ? "" : "s"}
            </div>
          </>
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </td>
      <td className="px-3 py-1.5">
        {opt.restored_node_count > 0 && opt.min_voltage_pu > 0 ? (
          <span className={cn(problems > 0 && "text-destructive")}>{formatNumber(opt.min_voltage_pu, 3)}</span>
        ) : (
          <span className="text-muted-foreground">—</span>
        )}
      </td>
      <td className="px-3 py-1.5 text-xs">
        {opt.note ? (
          <span className="text-muted-foreground">{opt.note}</span>
        ) : opt.feasible ? (
          <span className="font-medium text-emerald-700">Feasible</span>
        ) : (
          <span className="text-destructive">
            {opt.violations.length > 0 && `${opt.violations.length} volt. violation${opt.violations.length === 1 ? "" : "s"}`}
            {opt.violations.length > 0 && opt.overloaded.length > 0 && ", "}
            {opt.overloaded.length > 0 && `${opt.overloaded.length} overload${opt.overloaded.length === 1 ? "" : "s"}`}
          </span>
        )}
      </td>
    </tr>
  )
}
