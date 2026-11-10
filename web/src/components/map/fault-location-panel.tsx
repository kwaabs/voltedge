import { useEffect, useState } from "react"

import {
  NOMINAL_VOLTAGE_HINT,
  resolveNominalVoltage,
  useDeriveVoltageDefault,
} from "@/components/map/analysis-voltage"
import { FeatureInfoButton } from "@/components/map/feature-info-button"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { FloatingPanel } from "@/components/ui/floating-panel"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { FaultLocationCandidate, FaultLocationResult } from "@/stores/fault-location-store"
import { useFaultLocationStore } from "@/stores/fault-location-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

/** Metres → a compact m/km string: spans run to kilometres, so a bare metre
 * figure is hard to scan. */
function formatDistance(m: number): string {
  if (m >= 1000) return `${(m / 1000).toLocaleString(undefined, { maximumFractionDigits: 2 })} km`
  return `${m.toLocaleString(undefined, { maximumFractionDigits: 0 })} m`
}

function formatCurrent(amps: number): string {
  return amps >= 1000
    ? `${(amps / 1000).toLocaleString(undefined, { maximumFractionDigits: 2 })} kA`
    : `${amps.toLocaleString(undefined, { maximumFractionDigits: 0 })} A`
}

/**
 * Fault Location (distance-to-fault) panel -- the operational inverse of the
 * fault-current study. Armed from a feeder SOURCE (the feature panel's "Locate
 * fault from here"), it takes the fault current the relay/SCADA measured and
 * returns every conductor span that current could place the fault on, ranked by
 * distance from the source, each with an interpolated point drawn on the map.
 * Unlike the planning studies it is an independent floating panel, so it can sit
 * open alongside a fault-current or trace result rather than replacing it.
 */
export function FaultLocationPanel() {
  const open = useFaultLocationStore((s) => s.open)
  const start = useFaultLocationStore((s) => s.start)
  const loading = useFaultLocationStore((s) => s.loading)
  const result = useFaultLocationStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()

  const prefillCurrent = useFaultLocationStore((s) => s.prefillCurrentAmps)
  const [currentInput, setCurrentInput] = useState("")
  const [voltageInput, setVoltageInput] = useState("11000")
  // Blank the voltage when armed from a transformer so its secondary is derived.
  useDeriveVoltageDefault(start, "11000", setVoltageInput)

  // Pre-fill the measured current when the panel was reached from a logged
  // fault's "Locate" shortcut (its fault_current_a), so the operator doesn't
  // re-type it. Only seeds the field; they can still edit it.
  useEffect(() => {
    if (open && prefillCurrent != null) setCurrentInput(String(prefillCurrent))
  }, [open, prefillCurrent])

  if (!open) return null

  const classNameFor = (code: string) =>
    featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  async function run() {
    const s = useFaultLocationStore.getState().start
    if (!s) return
    const amps = Number(currentInput)
    if (!Number.isFinite(amps) || amps <= 0) {
      useToastStore.getState().pushError("Enter the measured fault current in amperes (greater than zero).")
      return
    }
    const nv = resolveNominalVoltage(voltageInput)
    if (!nv.ok) {
      useToastStore.getState().pushError(nv.message)
      return
    }

    useFaultLocationStore.getState().setLoading(true)
    try {
      const response = await apiFetch<FaultLocationResult>("/analysis/fault-location", {
        method: "POST",
        body: JSON.stringify({
          start: s,
          measured_current_amps: amps,
          ...(nv.value !== undefined && { nominal_voltage_v: nv.value }),
        }),
      })
      useFaultLocationStore.getState().setResult(response)
    } catch (err) {
      useFaultLocationStore.getState().setLoading(false)
      useToastStore
        .getState()
        .pushError(err instanceof ApiError ? err.message : "Could not locate the fault.")
    }
  }

  return (
    <FloatingPanel
      title="Fault location"
      onClose={() => useFaultLocationStore.getState().close()}
      initialRect={{ x: window.innerWidth - 460, y: 120, width: 420, height: 440 }}
      minWidth={340}
      minHeight={240}
    >
      <div className="flex h-full flex-col">
        <div className="space-y-2.5 border-b p-3">
          <p className="text-muted-foreground text-xs">
            From the feeder source{" "}
            {start ? <span className="font-medium">{classNameFor(start.feature_class)}</span> : null}, the
            measured fault current pins the distance to the fault. Three-phase.
          </p>
          <div className="flex items-end gap-2">
            <div className="space-y-1">
              <label className="text-sm font-medium">Measured current (A)</label>
              <input
                type="number"
                className={cn(inputClass, "w-32")}
                value={currentInput}
                onChange={(e) => setCurrentInput(e.target.value)}
                placeholder="e.g. 4180"
                autoFocus
              />
            </div>
            <div className="space-y-1">
              <label className="text-sm font-medium">Nominal (V)</label>
              <input
                type="number"
                className={cn(inputClass, "w-28")}
                value={voltageInput}
                onChange={(e) => setVoltageInput(e.target.value)}
                placeholder={NOMINAL_VOLTAGE_HINT}
                title="Leave blank when the source is a transformer (its secondary voltage is used)."
              />
            </div>
            <Button size="sm" className="ml-auto shadow-sm" onClick={run} disabled={loading}>
              {loading ? "Locating…" : "Locate"}
            </Button>
          </div>
        </div>

        <div className="min-h-0 flex-1 overflow-auto p-3">
          {loading && !result ? (
            <p className="text-muted-foreground text-sm">Locating…</p>
          ) : !result ? (
            <p className="text-muted-foreground text-sm">
              Enter the current the relay measured and press Locate.
            </p>
          ) : (
            <FaultLocationResults result={result} classNameFor={classNameFor} />
          )}
        </div>
      </div>
    </FloatingPanel>
  )
}

function FaultLocationResults({
  result,
  classNameFor,
}: {
  result: FaultLocationResult
  classNameFor: (code: string) => string
}) {
  return (
    <div className="space-y-3 text-sm">
      <div className="text-muted-foreground text-xs">
        Measured <b className="text-foreground">{formatCurrent(result.measured_current_amps)}</b> ·
        target impedance {result.target_impedance_ohm.toLocaleString(undefined, { maximumFractionDigits: 3 })}Ω ·
        source ceiling {formatCurrent(result.source_fault_current_amps)}
      </div>

      {result.candidates.length === 0 ? (
        <p className="rounded border border-amber-200 bg-amber-50 p-2 text-xs text-amber-800 dark:border-amber-900/50 dark:bg-amber-950/30 dark:text-amber-300">
          {result.note || "No candidate span brackets the measured current."}
        </p>
      ) : (
        <>
          <p className="text-muted-foreground text-xs">
            {result.candidates.length} candidate{result.candidates.length === 1 ? "" : "s"} — the fault is on
            one of these spans (each marked on the map). Click one to zoom to it.
          </p>
          <ol className="space-y-1.5">
            {result.candidates.map((c, i) => (
              <CandidateRow key={`${c.segment.feature_class}:${c.segment.feature_id}:${i}`} c={c} n={i + 1} classNameFor={classNameFor} />
            ))}
          </ol>
        </>
      )}
    </div>
  )
}

function CandidateRow({
  c,
  n,
  classNameFor,
}: {
  c: FaultLocationCandidate
  n: number
  classNameFor: (code: string) => string
}) {
  const pct = Math.round(c.fraction_along_segment * 100)
  return (
    <li className="flex items-start gap-1">
      <FeatureInfoButton featureClass={c.segment.feature_class} featureId={c.segment.feature_id} className="mt-1" />
      <button
        type="button"
        onClick={() => useSelectionStore.getState().locate(c.segment.feature_class, c.segment.feature_id, { focus: true })}
        onDoubleClick={() => useSelectionStore.getState().select(c.segment.feature_class, c.segment.feature_id, { focus: true })}
        className="hover:bg-accent select-none w-full rounded border p-2 text-left"
      >
        <div className="flex items-center justify-between gap-2">
          <span className="font-medium">
            {n}. {formatDistance(c.distance_from_source_m)} from source
            {!c.resolved && (
              <span className="text-amber-700 dark:text-amber-500" title="Some impedance on the path was unresolved — an optimistic estimate">
                {" "}≈
              </span>
            )}
          </span>
          <span className="text-muted-foreground text-xs">{pct}% along</span>
        </div>
        <div className="text-muted-foreground mt-0.5 text-xs">
          on {classNameFor(c.segment.feature_class)}{" "}
          <span className="font-mono">{shortId(c.segment.feature_id)}</span> · {formatDistance(c.distance_along_segment_m)} of{" "}
          {formatDistance(c.segment_length_m)} from {classNameFor(c.upstream.feature_class)}
        </div>
      </button>
    </li>
  )
}
