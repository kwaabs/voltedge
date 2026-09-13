import { useState } from "react"
import { X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { HostingCandidate, HostingResult } from "@/stores/hosting-store"
import { useHostingStore } from "@/stores/hosting-store"
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
    case "overvoltage":
      return "Over-voltage"
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
 * Floating panel for api/internal/powerflow's DER hosting-capacity study
 * (POST /analysis/hosting-capacity). Armed on a feeder's source, it
 * binary-searches the largest generator injection each interconnection node
 * can accept before the first over-voltage or thermal overload binds. Same
 * combined prompt+result shape and bottom-center slot as the other analysis
 * panels; the analysis workflows are mutually exclusive, so
 * feature-panel.tsx's triggers clear each other's stores.
 *
 * The prompt asks for nominal_voltage_v (the same un-guessable input load
 * flow needs), the DER injection power factor (default unity), and
 * optionally the acceptable per-unit voltage band; blank band fields fall
 * back to the server default (0.95..1.05). The result lists each node
 * (best-hosting first) with its capacity, the binding constraint, and how
 * close each limit sits at that capacity.
 */
export function HostingPanel() {
  const armed = useHostingStore((s) => s.armed)
  const loading = useHostingStore((s) => s.loading)
  const result = useHostingStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("11000")
  const [pfInput, setPfInput] = useState("1.0")
  const [minPuInput, setMinPuInput] = useState("0.95")
  const [maxPuInput, setMaxPuInput] = useState("1.05")
  const fp = useFloatingPanel(704, "max-h-[24rem]")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  async function runHosting() {
    const source = useHostingStore.getState().armedStart
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
    const maxPu = maxPuInput.trim() === "" ? undefined : Number(maxPuInput)
    if ((minPu !== undefined && !(minPu > 0)) || (maxPu !== undefined && !(maxPu > 0))) {
      useToastStore.getState().pushError("Voltage limits must be positive numbers.")
      return
    }
    if (minPu !== undefined && maxPu !== undefined && minPu > maxPu) {
      useToastStore.getState().pushError("Min voltage must not exceed max voltage.")
      return
    }

    useHostingStore.getState().setLoading(true)
    try {
      const response = await apiFetch<HostingResult>("/analysis/hosting-capacity", {
        method: "POST",
        body: JSON.stringify({
          source,
          nominal_voltage_v: nominalVoltageV,
          power_factor: powerFactor,
          min_voltage_pu: minPu,
          max_voltage_pu: maxPu,
        }),
      })
      useHostingStore.getState().setResult(response)
    } catch (err) {
      useHostingStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run this hosting-capacity study.")
    }
  }

  // Best interconnection point first -- the whole question is "where hosts the
  // most DER", so sort by capacity descending (a stable copy, not in place).
  const ranked = result ? [...result.candidates].sort((a, b) => b.hosting_capacity_kw - a.hosting_capacity_kw) : []

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <p className="truncate text-sm font-medium">DER hosting capacity</p>
        </div>
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={() => useHostingStore.getState().clear()}>
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
            <label className="text-muted-foreground text-xs">Power factor</label>
            <input
              type="number"
              step="0.01"
              className={cn(inputClass, "w-20")}
              value={pfInput}
              onChange={(e) => setPfInput(e.target.value)}
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
          <Button size="sm" className="ml-auto" onClick={runHosting} disabled={loading}>
            {loading ? "Running…" : "Run"}
          </Button>
        </div>
      ) : (
        <div className={cn("flex flex-col overflow-hidden", fp.resized ? "min-h-0 flex-1" : "max-h-[21rem]")}>
          <div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 border-b px-3 py-1.5 text-xs">
            <span>{formatNumber(result.nominal_voltage_v, 0)} V nominal</span>
            <span>pf {formatNumber(result.power_factor, 2)}</span>
            <span>
              band {formatNumber(result.min_voltage_pu, 2)}–{formatNumber(result.max_voltage_pu, 2)} pu
            </span>
            <span>
              {ranked.length} node{ranked.length === 1 ? "" : "s"}
            </span>
            <Button variant="ghost" size="sm" className="ml-auto h-6 px-2 text-xs" onClick={() => useHostingStore.getState().rearm()}>
              Run again
            </Button>
          </div>
          <div className={cn("overflow-y-auto", fp.resized && "min-h-0 flex-1")}>
            {ranked.length === 0 ? (
              <p className="text-muted-foreground px-3 py-4 text-sm">
                No interconnection nodes on this feeder to size.
              </p>
            ) : (
              <table className="w-full text-sm">
                <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
                  <tr>
                    <th className="bg-popover px-3 py-1.5 font-medium">Node</th>
                    <th className="bg-popover px-3 py-1.5 font-medium">Hosting capacity</th>
                    <th className="bg-popover px-3 py-1.5 font-medium">Binds on</th>
                    <th className="bg-popover px-3 py-1.5 font-medium">At capacity</th>
                  </tr>
                </thead>
                <tbody>
                  {ranked.map((c) => (
                    <HostingRow key={`${c.node.feature_class}:${c.node.feature_id}`} candidate={c} classNameFor={classNameFor} />
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>
      )}
      {fp.resizeHandle}
    </div>
  )
}

/** One node's row: the node (click to select), its hosting capacity kW, the
 * binding constraint (with the feature it acts on), and how close each limit
 * sits at that capacity. A pre-existing violation (0 capacity) is red; a
 * healthy hostable node is green. */
function HostingRow({
  candidate,
  classNameFor,
}: {
  candidate: HostingCandidate
  classNameFor: (code: string) => string
}) {
  const preExisting = candidate.binding_constraint === "pre_existing"
  const hostable = candidate.hosting_capacity_kw > 0
  return (
    <tr
      onClick={() => useSelectionStore.getState().select(candidate.node.feature_class, candidate.node.feature_id, { focus: true })}
      className={cn(
        "hover:bg-accent cursor-pointer border-b align-top last:border-b-0",
        hostable && "bg-emerald-500/10 hover:bg-emerald-500/15",
        preExisting && "bg-destructive/10 hover:bg-destructive/15",
      )}
    >
      <td className="px-3 py-1.5">
        <div>{classNameFor(candidate.node.feature_class)}</div>
        <div className="text-muted-foreground font-mono text-xs">{shortId(candidate.node.feature_id)}</div>
      </td>
      <td className="px-3 py-1.5 whitespace-nowrap">
        <div className="font-medium">
          {formatNumber(candidate.hosting_capacity_kw, 0)} kW{candidate.unbounded ? "+" : ""}
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
