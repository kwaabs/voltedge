import { useEffect, useState } from "react"
import { Crosshair, X } from "lucide-react"

import { AnalysisExplanation, Formula } from "@/components/map/analysis-explanation"
import { NOMINAL_VOLTAGE_HINT, resolveNominalVoltage, useDeriveVoltageDefault } from "@/components/map/analysis-voltage"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { CoordinationDevice, CoordinationResult } from "@/stores/coordination-store"
import { useCoordinationStore } from "@/stores/coordination-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/** Amps → a compact A/kA string, matching fault-current-panel.tsx. */
function formatCurrent(amps: number): string {
  return amps >= 1000 ? `${formatNumber(amps / 1000, 2)}kA` : `${formatNumber(amps, 0)}A`
}

/** Seconds → a compact time label for an axis tick or a readout: sub-second
 * values keep enough decimals to read, whole seconds drop them. */
function formatTime(seconds: number): string {
  if (seconds <= 0) return "0s"
  if (seconds < 1) return `${formatNumber(seconds, seconds < 0.1 ? 3 : 2)}s`
  return `${formatNumber(seconds, seconds % 1 === 0 ? 0 : 1)}s`
}

function formatVoltageLevel(volts: number): string {
  if (volts >= 1000) return `${formatNumber(volts / 1000, volts % 1000 === 0 ? 0 : 1)} kV`
  return `${formatNumber(volts, 0)} V`
}

/** The device's voltage + circuit subtitle line ("11 kV · FEEDER-3"), each part
 * shown only when known. Voltage prefers the device's own coded nominal
 * (nominal_voltage_v) and falls back to its operating base (nominal_base_v,
 * always resolved by the impedance walk) so a switchgear class with no coded
 * voltage of its own still shows one; circuit prefers the human circuit_ref,
 * falling back to a shortened circuit_id. Empty string when neither is known. */
function deviceMeta(d: CoordinationDevice): string {
  const volts = d.nominal_voltage_v ?? (d.nominal_base_v > 0 ? d.nominal_base_v : undefined)
  const circuit = d.circuit_ref || (d.circuit_id ? shortId(d.circuit_id) : undefined)
  return [volts !== undefined ? formatVoltageLevel(volts) : null, circuit ?? null].filter(Boolean).join(" · ")
}

/** A fixed palette of distinct, popover-legible line colours, assigned to
 * devices by index (there are rarely more than a handful of graded devices on
 * one path, so a fixed palette beats generating colours). */
const DEVICE_COLORS = ["#2563eb", "#dc2626", "#16a34a", "#d97706", "#7c3aed", "#0891b2", "#db2777", "#65a30d"]

function deviceColor(index: number): string {
  return DEVICE_COLORS[index % DEVICE_COLORS.length]
}

const log10 = (x: number) => Math.log10(x)

/** A power-of-ten "nice" floor / ceiling, so the axes land on decade
 * boundaries (10, 100, 1000…) the log grid is drawn at. */
function decadeFloor(x: number): number {
  return Math.pow(10, Math.floor(log10(x)))
}
function decadeCeil(x: number): number {
  return Math.pow(10, Math.ceil(log10(x)))
}

const CHART = { w: 660, h: 400, top: 16, right: 140, bottom: 44, left: 60 }

/**
 * The log-log time-current (TCC) chart, hand-rolled SVG (the app carries no
 * charting library -- see diagram-canvas.tsx for the same
 * viewBox-in-data-space, CSS-var-themed approach). Current is the x axis
 * (log, increasing right), time the y axis (log, increasing up) -- the
 * industry-standard TCC layout, where an inverse-time curve slopes down to the
 * right (more current, faster trip). Every device's curve is drawn on the run's
 * common base, so the prospective fault current is a single vertical line and
 * each device's curve crosses it at that device's operate time (also marked as
 * a dot). A device with no plottable curve (unresolved settings) contributes a
 * legend row but no line.
 */
function TccChart({ result }: { result: CoordinationResult }) {
  const currents: number[] = []
  const times: number[] = []
  for (const d of result.devices) {
    for (const trace of d.curves ?? []) {
      for (const p of trace.points) {
        if (p.current_amps > 0) currents.push(p.current_amps)
        if (p.time_s > 0) times.push(p.time_s)
      }
    }
    if (d.operate_time_s !== undefined && d.operate_time_s > 0) times.push(d.operate_time_s)
  }
  const fault = result.fault_current_amps
  if (fault !== undefined && fault > 0) currents.push(fault)

  if (currents.length === 0 || times.length === 0) {
    return (
      <div className="text-muted-foreground p-6 text-center text-sm">
        No plottable device curves — the graded devices have no resolved protection settings.
      </div>
    )
  }

  let xMin = decadeFloor(Math.min(...currents))
  let xMax = decadeCeil(Math.max(...currents))
  if (xMin >= xMax) xMax = xMin * 10
  let yMin = Math.max(decadeFloor(Math.min(...times)), 0.01)
  let yMax = Math.min(decadeCeil(Math.max(...times)), 10000)
  if (yMin >= yMax) yMax = yMin * 10

  const plotW = CHART.w - CHART.left - CHART.right
  const plotH = CHART.h - CHART.top - CHART.bottom
  const lx0 = log10(xMin)
  const lxSpan = log10(xMax) - lx0
  const ly0 = log10(yMin)
  const lySpan = log10(yMax) - ly0

  const sx = (a: number) => CHART.left + ((log10(a) - lx0) / lxSpan) * plotW
  const sy = (t: number) => {
    const clamped = Math.min(Math.max(t, yMin), yMax)
    return CHART.top + ((log10(yMax) - log10(clamped)) / lySpan) * plotH
  }

  // Decade gridlines + labels for each axis.
  const xTicks: number[] = []
  for (let e = Math.round(lx0); e <= Math.round(log10(xMax)) + 1e-9; e++) xTicks.push(Math.pow(10, e))
  const yTicks: number[] = []
  for (let e = Math.round(ly0); e <= Math.round(log10(yMax)) + 1e-9; e++) yTicks.push(Math.pow(10, e))

  return (
    <svg viewBox={`0 0 ${CHART.w} ${CHART.h}`} className="h-auto w-full" role="img" aria-label="Time-current coordination chart">
      {/* plot frame */}
      <rect
        x={CHART.left}
        y={CHART.top}
        width={plotW}
        height={plotH}
        fill="none"
        stroke="var(--border)"
        strokeWidth={1}
      />

      {/* x gridlines + labels (current) */}
      {xTicks.map((v) => (
        <g key={`x${v}`}>
          <line x1={sx(v)} y1={CHART.top} x2={sx(v)} y2={CHART.top + plotH} stroke="var(--border)" strokeWidth={0.5} />
          <text x={sx(v)} y={CHART.top + plotH + 16} textAnchor="middle" fontSize={10} fill="var(--muted-foreground)">
            {formatCurrent(v)}
          </text>
        </g>
      ))}
      <text x={CHART.left + plotW / 2} y={CHART.h - 6} textAnchor="middle" fontSize={11} fill="var(--muted-foreground)">
        Current ({formatVoltageLevel(result.common_base_v)} base)
      </text>

      {/* y gridlines + labels (time) */}
      {yTicks.map((v) => (
        <g key={`y${v}`}>
          <line x1={CHART.left} y1={sy(v)} x2={CHART.left + plotW} y2={sy(v)} stroke="var(--border)" strokeWidth={0.5} />
          <text x={CHART.left - 6} y={sy(v) + 3} textAnchor="end" fontSize={10} fill="var(--muted-foreground)">
            {formatTime(v)}
          </text>
        </g>
      ))}
      <text
        x={14}
        y={CHART.top + plotH / 2}
        textAnchor="middle"
        fontSize={11}
        fill="var(--muted-foreground)"
        transform={`rotate(-90 14 ${CHART.top + plotH / 2})`}
      >
        Time (s)
      </text>

      {/* prospective fault-current line */}
      {fault !== undefined && fault > 0 && (
        <g>
          <line
            x1={sx(fault)}
            y1={CHART.top}
            x2={sx(fault)}
            y2={CHART.top + plotH}
            stroke="var(--destructive)"
            strokeWidth={1.5}
            strokeDasharray="4 3"
          />
          <text x={sx(fault)} y={CHART.top - 4} textAnchor="middle" fontSize={10} fill="var(--destructive)">
            fault {formatCurrent(fault)}
          </text>
        </g>
      )}

      {/* device curves + operating points */}
      {result.devices.map((d, i) => {
        const color = deviceColor(i)
        return (
          <g key={`${d.feature_class}:${d.feature_id}`}>
            {(d.curves ?? []).map((trace, ti) => (
              <polyline
                key={ti}
                points={trace.points.map((p) => `${sx(p.current_amps)},${sy(p.time_s)}`).join(" ")}
                fill="none"
                stroke={color}
                strokeWidth={1.5}
                strokeDasharray={trace.label === "minimum melt" ? "3 2" : undefined}
                opacity={0.9}
              />
            ))}
            {d.operate_time_s !== undefined && fault !== undefined && fault > 0 && (
              <circle cx={sx(fault)} cy={sy(d.operate_time_s)} r={3.5} fill={color} stroke="var(--popover)" strokeWidth={1} />
            )}
          </g>
        )
      })}
    </svg>
  )
}

/**
 * The dynamic "how this was calculated" body for a coordination run. Every
 * figure -- the fault node and current, the primary clearing device and its
 * operate time, the device count, and the grading verdict -- is read off THIS
 * result, so the strip verifies this study rather than describing the method
 * generically.
 */
function CoordinationExplanation({
  result,
  classNameFor,
}: {
  result: CoordinationResult
  classNameFor: (code: string) => string
}) {
  const primary = result.devices.find((d) => d.role === "primary")
  const resolved = result.devices.filter((d) => d.operate_time_s !== undefined).length
  const gradeable = result.pairs.filter((p) => p.margin_s !== undefined)
  const failing = gradeable.filter((p) => p.coordinated === false)
  const worst =
    gradeable.length > 0
      ? gradeable.reduce((a, b) => (b.margin_s! < a.margin_s! ? b : a))
      : undefined
  const deviceFor = (ref: { feature_class: string; feature_id: string }) =>
    result.devices.find((d) => d.feature_class === ref.feature_class && d.feature_id === ref.feature_id)
  const worstDown = worst ? deviceFor(worst.downstream) : undefined
  const worstUp = worst ? deviceFor(worst.upstream) : undefined

  return (
    <AnalysisExplanation study="coordination">
      <p>
        Along the path from the fault back to the source, each protective device's operate time is read off its
        time-current (TCC) curve by <b>log-log interpolation</b> at the prospective fault current. The device
        nearest the fault that resolves a trip is the <b>primary</b> clearing device; those upstream are
        <b> backup</b>, and each adjacent pair is graded against the coordination time interval <code>CTI</code>.
      </p>
      <p>
        Fault placed at <b>{classNameFor(result.fault.feature_class)} {shortId(result.fault.feature_id)}</b>
        {result.fault_current_amps !== undefined && (
          <>
            {" "}with a prospective 3-phase bolted current of{" "}
            <b>{formatCurrent(result.fault_current_amps)}</b> (referred to {formatVoltageLevel(result.common_base_v)}{" "}
            base). Every device's curve is plotted on that common base, so the fault is a single vertical line each
            curve crosses at its own operate time.
          </>
        )}
        . <b>{result.devices.length}</b> device(s) sit on the path, <b>{resolved}</b> with a resolved operate time.
      </p>
      {primary && (
        <p>
          Primary clearing device: <b>{classNameFor(primary.feature_class)} {shortId(primary.feature_id)}</b>
          {primary.curve && (
            <>
              {" "}(<code>{primary.curve}</code>)
            </>
          )}
          , which operates in{" "}
          <b>{primary.operate_time_s === 0 ? "instant (0 s)" : formatTime(primary.operate_time_s!)}</b>
          {primary.extrapolated && " (≈ clamped off the tabulated range)"} at the fault current.
        </p>
      )}
      <p>
        Grading uses <code>margin = t_backup − t_primary</code> and calls a pair coordinated when{" "}
        <code>margin ≥ CTI</code> (here <b>{formatNumber(result.cti_seconds, 2)}s</b>).{" "}
        {result.coordinated === undefined ? (
          <>Fewer than two devices resolved a trip, so there is nothing to grade.</>
        ) : result.coordinated ? (
          <>
            All <b>{gradeable.length}</b> gradeable pair(s) clear the margin — the protection is{" "}
            <b>coordinated</b>
            {worst?.margin_s !== undefined && (
              <> (tightest margin <b>{formatNumber(worst.margin_s, 2)}s</b>)</>
            )}
            .
          </>
        ) : (
          <>
            <b>Miscoordination:</b> <b>{failing.length}</b> pair(s) fall short of the CTI
            {worst?.margin_s !== undefined && (
              <>
                {" "}— the worst margin is <b>{formatNumber(worst.margin_s, 2)}s</b> between{" "}
                {classNameFor(worst.downstream.feature_class)} {shortId(worst.downstream.feature_id)} →{" "}
                {classNameFor(worst.upstream.feature_class)} {shortId(worst.upstream.feature_id)}
              </>
            )}
            , so the backup may trip before (or too close to) the device it protects.
          </>
        )}
      </p>
      {worst && worstDown?.operate_time_s !== undefined && worstUp?.operate_time_s !== undefined ? (
        <Formula>{`margin = t_backup − t_primary
       = ${formatNumber(worstUp.operate_time_s, 2)} s − ${formatNumber(worstDown.operate_time_s, 2)} s
       = ${formatNumber(worst.margin_s!, 2)} s     ${
         worst.coordinated
           ? `(≥ CTI ${formatNumber(result.cti_seconds, 2)} s ✓)`
           : `(< CTI ${formatNumber(result.cti_seconds, 2)} s ✗ miscoordination)`
       }`}</Formula>
      ) : primary?.operate_time_s !== undefined ? (
        <Formula>{`t_primary(${classNameFor(primary.feature_class)} ${shortId(primary.feature_id)}) = ${
          primary.operate_time_s === 0 ? "0" : formatNumber(primary.operate_time_s, 2)
        } s${result.fault_current_amps !== undefined ? ` at Ibf = ${formatCurrent(result.fault_current_amps)}` : ""}`}</Formula>
      ) : null}
    </AnalysisExplanation>
  )
}

/**
 * Floating panel for api/internal/powerflow's protection-coordination engine
 * (POST /analysis/coordination) -- the protection counterpart of the
 * fault-current panel, sharing its fixed bottom-centre slot and arm-then-run
 * lifecycle, but wider (it renders a chart, not a table) and needing a *second*
 * node: coordination grades the devices on the path from a source to a specific
 * fault location, so the user arms the source (from the selected feature), then
 * clicks "Pick fault" to arm the map and clicks the faulted feature (an
 * arm-then-click gesture, like "Trace path to…"). The result is a log-log TCC
 * chart of every graded device's curve, the prospective fault current as a
 * vertical line, and the pairwise coordination margins.
 */
export function CoordinationPanel() {
  const armed = useCoordinationStore((s) => s.armed)
  const loading = useCoordinationStore((s) => s.loading)
  const result = useCoordinationStore((s) => s.result)
  const armedStart = useCoordinationStore((s) => s.armedStart)
  const armedFault = useCoordinationStore((s) => s.armedFault)
  const pickingFault = useCoordinationStore((s) => s.pickingFault)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("11000")
  // Blank the voltage when armed from a transformer so its secondary is derived.
  useDeriveVoltageDefault(armedStart, "11000", setVoltageInput)
  const [ctiInput, setCtiInput] = useState("0.3")
  const fp = useFloatingPanel(736, "max-h-[80vh]")

  // While the fault pick is armed, Escape cancels it (mirrors the trace-path
  // gesture). Only bound while actually picking, so it never swallows Escape
  // elsewhere.
  useEffect(() => {
    if (!pickingFault) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") useCoordinationStore.getState().cancelPickFault()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [pickingFault])

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  async function runCoordination() {
    const start = useCoordinationStore.getState().armedStart
    const fault = useCoordinationStore.getState().armedFault
    if (!start || !fault) return
    const nv = resolveNominalVoltage(voltageInput)
    if (!nv.ok) {
      useToastStore.getState().pushError(nv.message)
      return
    }
    const cti = Number(ctiInput)
    if (!Number.isFinite(cti) || cti < 0) {
      useToastStore.getState().pushError("CTI must be a non-negative number.")
      return
    }

    useCoordinationStore.getState().setLoading(true)
    try {
      const response = await apiFetch<CoordinationResult>("/analysis/coordination", {
        method: "POST",
        body: JSON.stringify({ start, fault, ...(nv.value !== undefined && { nominal_voltage_v: nv.value }), cti_seconds: cti }),
      })
      useCoordinationStore.getState().setResult(response)
    } catch (err) {
      useCoordinationStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run this coordination study.")
    }
  }

  const deviceLabel = (d: CoordinationDevice) => `${classNameFor(d.feature_class)} ${shortId(d.feature_id)}`

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <p className="truncate text-sm font-medium">Protection coordination</p>
        </div>
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={() => useCoordinationStore.getState().clear()}>
          <X className="size-3.5" />
          <span className="sr-only">Close</span>
        </Button>
      </div>

      {!result ? (
        <div className="flex flex-col gap-3 p-3">
          <div className="flex items-center gap-2 text-sm">
            <span className="text-muted-foreground w-16 shrink-0">Source</span>
            <span className="truncate">
              {armedStart ? `${classNameFor(armedStart.feature_class)} ` : "—"}
              {armedStart && <span className="text-muted-foreground font-mono text-xs">{shortId(armedStart.feature_id)}</span>}
            </span>
          </div>

          <div className="flex items-center gap-2 text-sm">
            <span className="text-muted-foreground w-16 shrink-0">Fault at</span>
            {pickingFault ? (
              <span className="text-primary text-xs">Click the faulted feature on the map… (Esc to cancel)</span>
            ) : armedFault ? (
              <span className="truncate">
                {classNameFor(armedFault.feature_class)}{" "}
                <span className="text-muted-foreground font-mono text-xs">{shortId(armedFault.feature_id)}</span>
              </span>
            ) : (
              <span className="text-muted-foreground text-xs">Pick fault, then click the faulted feature on the map</span>
            )}
            <Button
              variant={pickingFault ? "default" : "outline"}
              size="sm"
              className="ml-auto h-7 gap-1.5 px-2 text-xs"
              onClick={() =>
                pickingFault
                  ? useCoordinationStore.getState().cancelPickFault()
                  : useCoordinationStore.getState().startPickFault()
              }
              title={
                pickingFault
                  ? "Cancel — stop waiting for a map click"
                  : "Arm the map: the next feature you click becomes the fault location"
              }
            >
              <Crosshair className="size-3.5" />
              {pickingFault ? "Cancel" : armedFault ? "Change fault" : "Pick fault"}
            </Button>
          </div>

          <div className="flex items-center gap-2">
            <label className="text-muted-foreground shrink-0 text-sm">Nominal (V)</label>
            <input
              type="number"
              className={cn(inputClass, "w-24")}
              value={voltageInput}
              onChange={(e) => setVoltageInput(e.target.value)}
              placeholder={NOMINAL_VOLTAGE_HINT}
              title="Leave blank to use a transformer start's secondary voltage (LV coordination)."
              autoFocus
            />
            <label className="text-muted-foreground ml-2 shrink-0 text-sm">CTI (s)</label>
            <input
              type="number"
              step="0.05"
              className={cn(inputClass, "w-20")}
              value={ctiInput}
              onChange={(e) => setCtiInput(e.target.value)}
            />
            <Button size="sm" className="ml-auto" onClick={runCoordination} disabled={loading || !armedFault}>
              {loading ? "Running…" : "Run"}
            </Button>
          </div>
        </div>
      ) : (
        <>
          <div className={cn("overflow-y-auto", fp.resized ? "min-h-0 flex-1" : "max-h-[calc(80vh-2.5rem)]")}>
          <div className="text-muted-foreground flex items-center justify-between gap-2 border-b px-3 py-1.5 text-xs">
            <div className="flex items-center gap-2">
              {result.coordinated === undefined ? (
                <span>Not enough graded devices to check</span>
              ) : result.coordinated ? (
                <span className="rounded bg-green-600/15 px-1.5 py-0.5 font-medium text-green-700 dark:text-green-400">Coordinated</span>
              ) : (
                <span className="text-destructive bg-destructive/10 rounded px-1.5 py-0.5 font-medium">Miscoordination</span>
              )}
              {result.fault_current_amps !== undefined && (
                <span>
                  Fault {formatCurrent(result.fault_current_amps)} @ {formatVoltageLevel(result.common_base_v)}
                </span>
              )}
              <span>CTI {formatNumber(result.cti_seconds, 2)}s</span>
            </div>
            <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={() => useCoordinationStore.getState().rearm()}>
              Run again
            </Button>
          </div>

          <div className="p-3">
            <TccChart result={result} />
          </div>

          {/* device legend */}
          <div className="border-t px-3 py-2">
            <table className="w-full text-sm">
              <tbody>
                {result.devices.map((d, i) => (
                  <tr
                    key={`${d.feature_class}:${d.feature_id}`}
                    onClick={() => useSelectionStore.getState().select(d.feature_class, d.feature_id, { focus: true })}
                    className="hover:bg-accent cursor-pointer"
                  >
                    <td className="py-1 pr-2 align-top">
                      <span className="inline-block size-3 rounded-sm" style={{ backgroundColor: deviceColor(i) }} />
                    </td>
                    <td className="py-1 pr-2 align-top">
                      <div>{deviceLabel(d)}</div>
                      {d.role && <div className="text-muted-foreground text-xs capitalize">{d.role}</div>}
                      {deviceMeta(d) && <div className="text-muted-foreground text-xs">{deviceMeta(d)}</div>}
                    </td>
                    <td className="text-muted-foreground py-1 pr-2 align-top text-xs whitespace-nowrap">
                      {d.curve ?? "—"}
                    </td>
                    <td className="py-1 align-top text-right whitespace-nowrap">
                      {d.operate_time_s !== undefined ? (
                        <span className={cn(d.extrapolated && "text-muted-foreground")} title={d.extrapolated ? "Off the tabulated curve range — clamped to the nearest point" : undefined}>
                          {d.operate_time_s === 0 ? "instant" : formatTime(d.operate_time_s)}
                          {d.extrapolated && " ≈"}
                        </span>
                      ) : (
                        <span className="text-muted-foreground text-xs" title={d.note}>
                          no trip
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {/* pairwise margins */}
          {result.pairs.length > 0 && (
            <div className="border-t px-3 py-2">
              <p className="text-muted-foreground mb-1 text-xs font-medium">Coordination margins</p>
              <ul className="space-y-0.5 text-sm">
                {result.pairs.map((p, i) => (
                  <li key={i} className="flex items-center gap-2">
                    <span
                      className={cn(
                        "size-1.5 shrink-0 rounded-full",
                        p.coordinated === undefined ? "bg-muted-foreground" : p.coordinated ? "bg-green-600" : "bg-destructive",
                      )}
                    />
                    <span className="truncate">
                      {classNameFor(p.downstream.feature_class)} {shortId(p.downstream.feature_id)}
                      <span className="text-muted-foreground"> → </span>
                      {classNameFor(p.upstream.feature_class)} {shortId(p.upstream.feature_id)}
                    </span>
                    {p.margin_s !== undefined && (
                      <span className={cn("ml-auto whitespace-nowrap", p.coordinated ? "text-muted-foreground" : "text-destructive")}>
                        {formatNumber(p.margin_s, 2)}s
                      </span>
                    )}
                  </li>
                ))}
              </ul>
            </div>
          )}
          </div>
          <CoordinationExplanation result={result} classNameFor={classNameFor} />
        </>
      )}
      {fp.resizeHandle}
    </div>
  )
}
