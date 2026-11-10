import { useState } from "react"
import { X } from "lucide-react"

import { FeatureInfoButton } from "@/components/map/feature-info-button"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { LoadAllocationResult } from "@/stores/load-allocation-store"
import { useLoadAllocationStore } from "@/stores/load-allocation-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function fmt(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

const BASIS_LABEL: Record<string, string> = {
  kva: "connected kVA",
  customers: "customers",
  as_designed: "as-designed kW",
}

/**
 * Floating panel for the load-allocation study (POST /analysis/load-allocation):
 * distribute a MEASURED feeder-head load across the downstream consumer leaves by a
 * weight basis, so the per-transformer loads reflect reality rather than the model's
 * as-designed demand_kw. Armed on a feeder's start; the panel takes the measured kW
 * (and optional power factor) + the basis, then shows the per-leaf allocation and the
 * model-vs-measured scale factor. Same armed/result shape and bottom-center slot as
 * the other analysis panels.
 */
export function LoadAllocationPanel() {
  const armed = useLoadAllocationStore((s) => s.armed)
  const loading = useLoadAllocationStore((s) => s.loading)
  const result = useLoadAllocationStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [measuredInput, setMeasuredInput] = useState("")
  const [pfInput, setPfInput] = useState("")
  const [basis, setBasis] = useState("kva")
  const fp = useFloatingPanel(680, "max-h-[24rem]")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  async function runAllocation() {
    const source = useLoadAllocationStore.getState().armedStart
    if (!source) return
    const measuredKW = Number(measuredInput)
    if (!Number.isFinite(measuredKW) || measuredKW < 0) {
      useToastStore.getState().pushError("Measured load must be a non-negative number of kW.")
      return
    }
    let powerFactor: number | undefined
    if (pfInput.trim() !== "") {
      powerFactor = Number(pfInput)
      if (!(powerFactor > 0 && powerFactor <= 1)) {
        useToastStore.getState().pushError("Power factor must be between 0 and 1.")
        return
      }
    }

    useLoadAllocationStore.getState().setLoading(true)
    try {
      const response = await apiFetch<LoadAllocationResult>("/analysis/load-allocation", {
        method: "POST",
        body: JSON.stringify({
          start: source,
          measured_kw: measuredKW,
          power_factor: powerFactor,
          basis,
        }),
      })
      useLoadAllocationStore.getState().setResult(response)
    } catch (err) {
      useLoadAllocationStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run this load allocation.")
    }
  }

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <p className="truncate text-sm font-medium">Load allocation</p>
        </div>
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={() => useLoadAllocationStore.getState().clear()}>
          <X className="size-3.5" />
          <span className="sr-only">Close</span>
        </Button>
      </div>

      {!result ? (
        <div className="flex flex-col gap-3 p-3">
          <p className="text-muted-foreground text-xs">
            Spread a measured feeder-head load over the consumer points downstream of this feature — so the
            per-transformer loads match what the feeder is actually carrying.
          </p>
          <div className="flex flex-wrap items-end gap-3">
            <div className="flex flex-col gap-1">
              <label className="text-muted-foreground text-xs">Measured load (kW)</label>
              <input
                type="number"
                className={cn(inputClass, "w-32")}
                value={measuredInput}
                onChange={(e) => setMeasuredInput(e.target.value)}
                autoFocus
              />
            </div>
            <div className="flex flex-col gap-1">
              <label className="text-muted-foreground text-xs">Power factor (opt.)</label>
              <input type="number" step="0.01" className={cn(inputClass, "w-24")} value={pfInput} onChange={(e) => setPfInput(e.target.value)} />
            </div>
            <div className="flex flex-col gap-1">
              <label className="text-muted-foreground text-xs">Weight by</label>
              <select className={cn(inputClass, "w-40")} value={basis} onChange={(e) => setBasis(e.target.value)}>
                <option value="kva">Connected kVA</option>
                <option value="customers">Customers</option>
                <option value="as_designed">As-designed kW</option>
              </select>
            </div>
            <Button size="sm" className="ml-auto" onClick={runAllocation} disabled={loading}>
              {loading ? "Running…" : "Run"}
            </Button>
          </div>
        </div>
      ) : (
        <div className={cn("flex flex-col overflow-hidden", fp.resized ? "min-h-0 flex-1" : "max-h-[21rem]")}>
          <div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 border-b px-3 py-1.5 text-xs">
            <span>
              <b className="text-foreground">{fmt(result.measured_kw, 0)}</b> kW measured
            </span>
            <span>
              by {BASIS_LABEL[result.basis] ?? result.basis}
              {result.basis_fallback && ` → ${BASIS_LABEL[result.basis_fallback] ?? result.basis_fallback}`}
            </span>
            <span>{result.leaf_count} leaves</span>
            <span>{result.customers_total.toLocaleString()} customers</span>
            {result.as_designed_kw > 0 && (
              <span title="measured ÷ Σ as-designed demand_kw">
                model ×<b className="text-foreground">{fmt(result.scale_factor, 2)}</b>
              </span>
            )}
            <Button variant="ghost" size="sm" className="ml-auto h-6 px-2 text-xs" onClick={() => useLoadAllocationStore.getState().rearm()}>
              Run again
            </Button>
          </div>
          {result.note && <p className="text-muted-foreground border-b px-3 py-1.5 text-xs">{result.note}</p>}
          <div className={cn("overflow-y-auto", fp.resized && "min-h-0 flex-1")}>
            <table className="w-full text-sm">
              <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
                <tr>
                  <th className="bg-popover px-3 py-1.5 font-medium">Leaf</th>
                  <th className="bg-popover px-3 py-1.5 text-right font-medium">Cust.</th>
                  <th className="bg-popover px-3 py-1.5 text-right font-medium">As-designed</th>
                  <th className="bg-popover px-3 py-1.5 text-right font-medium">Allocated</th>
                </tr>
              </thead>
              <tbody>
                {result.allocations.map((a) => (
                  <tr
                    key={`${a.feature_class}:${a.feature_id}`}
                    onClick={() => useSelectionStore.getState().locate(a.feature_class, a.feature_id, { focus: true })}
                    onDoubleClick={() => useSelectionStore.getState().select(a.feature_class, a.feature_id, { focus: true })}
                    className="hover:bg-accent cursor-pointer border-b align-top select-none last:border-b-0"
                  >
                    <td className="px-3 py-1.5">
                      <div className="flex items-start gap-1">
                        <FeatureInfoButton featureClass={a.feature_class} featureId={a.feature_id} className="-mt-0.5" />
                        <div className="min-w-0">
                          <div>{a.label || classNameFor(a.feature_class)}</div>
                          <div className="text-muted-foreground font-mono text-xs">{shortId(a.feature_id)}</div>
                        </div>
                      </div>
                    </td>
                    <td className="px-3 py-1.5 text-right tabular-nums">{a.customers.toLocaleString()}</td>
                    <td className="text-muted-foreground px-3 py-1.5 text-right tabular-nums">{fmt(a.as_designed_kw)}</td>
                    <td className="px-3 py-1.5 text-right font-medium whitespace-nowrap tabular-nums">
                      {fmt(a.allocated_kw)} kW
                      {a.allocated_kvar !== 0 && <span className="text-muted-foreground"> · {fmt(a.allocated_kvar)} kVAR</span>}
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
