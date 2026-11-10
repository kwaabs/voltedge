import { useState } from "react"
import { X } from "lucide-react"

import { AnalysisExplanation } from "@/components/map/analysis-explanation"
import { FeatureInfoButton } from "@/components/map/feature-info-button"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { ForecastAsset, LoadGrowthResult } from "@/stores/load-growth-store"
import { useLoadGrowthStore } from "@/stores/load-growth-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

function shortId(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id
}

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/** Human label for the thermal limit the server names. */
function limitKindLabel(kind: string): string {
  switch (kind) {
    case "transformer_loading":
      return "Transformer"
    case "ampacity":
      return "Conductor"
    default:
      return kind
  }
}

// The loading-trajectory chart is hand-rolled SVG -- the app carries no charting
// library (mirrors coordination-panel.tsx's TccChart: viewBox-in-data-space,
// CSS-var-themed so it respects light/dark). Linear axes here (year x, loading% y).
const CHART = { w: 660, h: 300, top: 16, right: 14, bottom: 34, left: 44 }
// At most this many trajectories are drawn, so a big feeder's chart stays legible;
// the most-stressed assets (highest peak loading) are kept.
const MAX_CHART_LINES = 8

/**
 * Per-year loading trajectory chart: one line per stressed asset, the 100%-of-limit
 * line marked, over the horizon. A violating asset (crosses 100% within the horizon)
 * is drawn in the destructive colour, a merely-stressed one in amber.
 */
function ForecastChart({ assets, horizon }: { assets: ForecastAsset[]; horizon: number }) {
  if (assets.length === 0 || horizon <= 0) return null
  const shown = [...assets].sort((a, b) => b.peak_loading_pct - a.peak_loading_pct).slice(0, MAX_CHART_LINES)

  let peak = 100
  for (const a of shown) if (a.peak_loading_pct > peak) peak = a.peak_loading_pct
  const yMax = Math.ceil((peak + 5) / 25) * 25

  const plotW = CHART.w - CHART.left - CHART.right
  const plotH = CHART.h - CHART.top - CHART.bottom
  const sx = (year: number) => CHART.left + (year / horizon) * plotW
  const sy = (pct: number) => CHART.top + (1 - Math.min(pct, yMax) / yMax) * plotH

  const xStep = horizon <= 12 ? 1 : Math.ceil(horizon / 10)
  const xTicks: number[] = []
  for (let y = 0; y <= horizon; y += xStep) xTicks.push(y)
  const yTicks: number[] = []
  for (let v = 0; v <= yMax; v += 25) yTicks.push(v)

  return (
    <svg viewBox={`0 0 ${CHART.w} ${CHART.h}`} className="h-auto w-full" role="img" aria-label="Load-growth loading trajectory chart">
      <rect x={CHART.left} y={CHART.top} width={plotW} height={plotH} fill="none" stroke="var(--border)" strokeWidth={1} />

      {/* y gridlines + labels (loading %) */}
      {yTicks.map((v) => (
        <g key={`y${v}`}>
          <line x1={CHART.left} y1={sy(v)} x2={CHART.left + plotW} y2={sy(v)} stroke="var(--border)" strokeWidth={0.5} />
          <text x={CHART.left - 6} y={sy(v) + 3} textAnchor="end" fontSize={10} fill="var(--muted-foreground)">
            {v}%
          </text>
        </g>
      ))}
      {/* the 100%-of-limit line */}
      {yMax >= 100 && (
        <line
          x1={CHART.left}
          y1={sy(100)}
          x2={CHART.left + plotW}
          y2={sy(100)}
          stroke="var(--destructive)"
          strokeWidth={1.25}
          strokeDasharray="4 3"
        />
      )}

      {/* x gridlines + labels (year) */}
      {xTicks.map((y) => (
        <g key={`x${y}`}>
          <line x1={sx(y)} y1={CHART.top} x2={sx(y)} y2={CHART.top + plotH} stroke="var(--border)" strokeWidth={0.5} />
          <text x={sx(y)} y={CHART.top + plotH + 15} textAnchor="middle" fontSize={10} fill="var(--muted-foreground)">
            {y}
          </text>
        </g>
      ))}
      <text x={CHART.left + plotW / 2} y={CHART.h - 4} textAnchor="middle" fontSize={11} fill="var(--muted-foreground)">
        Year
      </text>

      {/* one line per asset */}
      {shown.map((a) => {
        const violating = a.first_violation_year !== null
        const color = violating ? "var(--destructive)" : "#d97706" // amber-600
        const pts = a.trajectory.map((p) => `${sx(p.year)},${sy(p.loading_pct)}`).join(" ")
        return <polyline key={`${a.feature_class}:${a.feature_id}`} points={pts} fill="none" stroke={color} strokeWidth={1.5} opacity={0.9} />
      })}
    </svg>
  )
}

/**
 * The dynamic "how this was calculated" body for a load-growth run. Every figure is
 * read off the actual result -- the growth assumption, the first asset to overload and
 * its year -- so it verifies THIS projection, with the Derived + "scenario, not
 * prediction" caveat the validation note carries.
 */
function ForecastExplanation({ result, classNameFor }: { result: LoadGrowthResult; classNameFor: (code: string) => string }) {
  const soonest = result.assets
    .filter((a) => a.first_violation_year !== null)
    .sort((a, b) => (a.first_violation_year ?? 0) - (b.first_violation_year ?? 0))[0]
  const uvSoonest = result.voltage_violations
    .filter((v) => v.first_violation_year !== null)
    .sort((a, b) => (a.first_violation_year ?? 0) - (b.first_violation_year ?? 0))[0]
  const uvCount = result.voltage_violations.filter((v) => v.first_violation_year !== null).length
  return (
    <AnalysisExplanation study="load-growth">
      <p>
        <b>Load-growth forecast.</b> The feeder (slack at <b>{formatNumber(result.nominal_voltage_v, 0)} V</b>) is resolved once,
        then its demand is scaled <b>{formatNumber(result.base_growth_pct, 2)}%/yr</b> and re-swept{" "}
        <b>in memory</b> for each of <b>{result.horizon_years}</b> year(s). Each year checks the same two limits load flow does
        -- thermal (transformer loading vs rating, conductor current vs ampacity) and <b>under-voltage</b> (below{" "}
        <b>{formatNumber(result.min_voltage_pu, 2)} pu</b>) -- so an asset is flagged the moment it crosses either.
      </p>
      <p>
        <b>{result.assets.length}</b> asset(s) reach ≥ 80% loading within the horizon;{" "}
        <b>{result.reinforcement_order.length}</b> overload (cross 100%).{" "}
        {soonest ? (
          <>
            First to overload: <b>{classNameFor(soonest.feature_class)}</b> ({limitKindLabel(soonest.limit_kind)},{" "}
            {formatNumber(soonest.rating, 0)} {soonest.unit}) in <b>year {soonest.first_violation_year}</b>.
          </>
        ) : (
          <>No asset overloads within the horizon under this assumption.</>
        )}
      </p>
      {uvCount > 0 && (
        <p>
          <b>{uvCount}</b> node(s) sag below the <b>{formatNumber(result.min_voltage_pu, 2)} pu</b> floor within the horizon
          {uvSoonest && (
            <>
              {" "}
              (first: <b>{classNameFor(uvSoonest.feature_class)}</b> in <b>year {uvSoonest.first_violation_year}</b>, worst{" "}
              <b>{formatNumber(uvSoonest.worst_voltage_pu, 3)} pu</b>)
            </>
          )}
          . On a long feeder this is often the first limit to bind -- before anything thermally overloads.
        </p>
      )}
      {result.assumptions.length > 0 && (
        <p className="text-muted-foreground">
          <b>Assumptions:</b> {result.assumptions.join("; ")}.
        </p>
      )}
    </AnalysisExplanation>
  )
}

/**
 * Floating panel for api/internal/powerflow's load-growth forecast
 * (POST /analysis/load-growth) -- a scenario projection of the radial load-flow across
 * a horizon. Armed on a feeder's start, it scales demand year by year and reports which
 * assets overload and when (the capex-timing answer), plus an optional DER-headroom
 * curve. Same combined prompt+result shape and bottom-center slot as the other
 * analysis panels. This panel exposes the core scalar inputs + the include-planned and
 * DER-headroom toggles; the richer scenario inputs (per-feeder/class growth, block
 * loads, planned-year overrides) are available on the endpoint directly.
 */
export function LoadGrowthPanel() {
  const armed = useLoadGrowthStore((s) => s.armed)
  const loading = useLoadGrowthStore((s) => s.loading)
  const result = useLoadGrowthStore((s) => s.result)
  const { data: featureClasses } = useFeatureClasses()
  const [voltageInput, setVoltageInput] = useState("11000")
  const [horizonInput, setHorizonInput] = useState("10")
  const [growthInput, setGrowthInput] = useState("1.5")
  const [minPuInput, setMinPuInput] = useState("0.95")
  const [includePlanned, setIncludePlanned] = useState(false)
  const [derHeadroom, setDerHeadroom] = useState(false)
  const fp = useFloatingPanel(704, "max-h-[24rem]")

  if (!armed && !result) return null

  const classNameFor = (code: string) => featureClasses?.feature_classes.find((fc) => fc.code === code)?.name ?? code

  async function runForecast() {
    const source = useLoadGrowthStore.getState().armedStart
    if (!source) return
    const nominalVoltageV = Number(voltageInput)
    if (!Number.isFinite(nominalVoltageV) || nominalVoltageV <= 0) {
      useToastStore.getState().pushError("Nominal voltage must be a positive number.")
      return
    }
    const horizonYears = Number(horizonInput)
    if (!Number.isInteger(horizonYears) || horizonYears < 1 || horizonYears > 60) {
      useToastStore.getState().pushError("Horizon must be a whole number of years between 1 and 60.")
      return
    }
    const baseGrowthPct = Number(growthInput)
    if (!Number.isFinite(baseGrowthPct) || baseGrowthPct < -50 || baseGrowthPct > 100) {
      useToastStore.getState().pushError("Base growth must be between -50 and 100 %/yr.")
      return
    }
    const minVoltagePu = minPuInput.trim() === "" ? undefined : Number(minPuInput)
    if (minVoltagePu !== undefined && !(minVoltagePu > 0)) {
      useToastStore.getState().pushError("Min voltage must be a positive number.")
      return
    }

    useLoadGrowthStore.getState().setLoading(true)
    try {
      const response = await apiFetch<LoadGrowthResult>("/analysis/load-growth", {
        method: "POST",
        body: JSON.stringify({
          start: source,
          nominal_voltage_v: nominalVoltageV,
          horizon_years: horizonYears,
          base_growth_pct: baseGrowthPct,
          min_voltage_pu: minVoltagePu,
          include_planned: includePlanned,
          der_headroom: derHeadroom,
        }),
      })
      useLoadGrowthStore.getState().setResult(response)
    } catch (err) {
      useLoadGrowthStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not run this load-growth forecast.")
    }
  }

  // Soonest + most severe first -- the reinforcement queue. Non-violating stressed
  // assets follow (they have headroom within the horizon).
  const ranked = result
    ? [...result.assets].sort((a, b) => {
        const av = a.first_violation_year ?? Number.POSITIVE_INFINITY
        const bv = b.first_violation_year ?? Number.POSITIVE_INFINITY
        if (av !== bv) return av - bv
        return b.peak_loading_pct - a.peak_loading_pct
      })
    : []

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <p className="truncate text-sm font-medium">Load-growth forecast</p>
        </div>
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={() => useLoadGrowthStore.getState().clear()}>
          <X className="size-3.5" />
          <span className="sr-only">Close</span>
        </Button>
      </div>

      {!result ? (
        <div className="flex flex-wrap items-end gap-3 p-3">
          <div className="flex flex-col gap-1">
            <label className="text-muted-foreground text-xs">Nominal voltage (V)</label>
            <input type="number" className={cn(inputClass, "w-28")} value={voltageInput} onChange={(e) => setVoltageInput(e.target.value)} autoFocus />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-muted-foreground text-xs">Horizon (years)</label>
            <input type="number" className={cn(inputClass, "w-20")} value={horizonInput} onChange={(e) => setHorizonInput(e.target.value)} />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-muted-foreground text-xs">Growth (%/yr)</label>
            <input type="number" step="0.1" className={cn(inputClass, "w-20")} value={growthInput} onChange={(e) => setGrowthInput(e.target.value)} />
          </div>
          <div className="flex flex-col gap-1">
            <label className="text-muted-foreground text-xs">Min (pu)</label>
            <input type="number" step="0.01" className={cn(inputClass, "w-20")} value={minPuInput} onChange={(e) => setMinPuInput(e.target.value)} />
          </div>
          <label className="flex items-center gap-1.5 text-xs">
            <input type="checkbox" checked={includePlanned} onChange={(e) => setIncludePlanned(e.target.checked)} />
            Include planned assets
          </label>
          <label className="flex items-center gap-1.5 text-xs">
            <input type="checkbox" checked={derHeadroom} onChange={(e) => setDerHeadroom(e.target.checked)} />
            DER headroom
          </label>
          <Button size="sm" className="ml-auto" onClick={runForecast} disabled={loading}>
            {loading ? "Running…" : "Run"}
          </Button>
        </div>
      ) : (
        <>
          <div className={cn("flex flex-col overflow-hidden", fp.resized ? "min-h-0 flex-1" : "max-h-[21rem]")}>
            <div className="text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-1 border-b px-3 py-1.5 text-xs">
              <span>{formatNumber(result.nominal_voltage_v, 0)} V nominal</span>
              <span>{formatNumber(result.base_growth_pct, 2)}%/yr</span>
              <span>{result.horizon_years} yr horizon</span>
              <span>
                {ranked.length} stressed · {result.reinforcement_order.length} overload
              </span>
              {result.voltage_violations.length > 0 && (
                <span>{result.voltage_violations.filter((v) => v.first_violation_year !== null).length} under-voltage</span>
              )}
              <Button variant="ghost" size="sm" className="ml-auto h-6 px-2 text-xs" onClick={() => useLoadGrowthStore.getState().rearm()}>
                Run again
              </Button>
            </div>
            <div className={cn("overflow-y-auto", fp.resized && "min-h-0 flex-1")}>
              {ranked.length === 0 && result.voltage_violations.length === 0 ? (
                <p className="text-muted-foreground px-3 py-4 text-sm">
                  No asset reaches 80% loading and no node approaches the {formatNumber(result.min_voltage_pu, 2)}-pu
                  voltage floor within the horizon under this growth assumption — comfortable headroom.
                </p>
              ) : (
                <>
                  {ranked.length > 0 && (
                    <>
                      <div className="px-3 pt-2">
                        <ForecastChart assets={result.assets} horizon={result.horizon_years} />
                      </div>
                      <table className="w-full text-sm">
                        <thead className="text-muted-foreground sticky top-0 border-b text-left text-xs">
                          <tr>
                            <th className="bg-popover px-3 py-1.5 font-medium">Asset</th>
                            <th className="bg-popover px-3 py-1.5 font-medium">Limit</th>
                            <th className="bg-popover px-3 py-1.5 font-medium">First overload</th>
                            <th className="bg-popover px-3 py-1.5 font-medium">Peak loading</th>
                          </tr>
                        </thead>
                        <tbody>
                          {ranked.map((a) => (
                            <ForecastRow key={`${a.feature_class}:${a.feature_id}`} asset={a} classNameFor={classNameFor} />
                          ))}
                        </tbody>
                      </table>
                    </>
                  )}

                  {result.voltage_violations.length > 0 && <VoltageTable result={result} classNameFor={classNameFor} />}

                  {result.der_headroom && result.der_headroom.length > 0 && (
                    <DerHeadroomTable result={result} classNameFor={classNameFor} />
                  )}
                </>
              )}
            </div>
          </div>
          <ForecastExplanation result={result} classNameFor={classNameFor} />
        </>
      )}
      {fp.resizeHandle}
    </div>
  )
}

/** One asset's row: the asset (click to select on the map), its thermal limit, the
 * first year it overloads (or "—" if it never does within the horizon), and its peak
 * loading. A violating asset is red; a merely-stressed one amber. */
function ForecastRow({ asset, classNameFor }: { asset: ForecastAsset; classNameFor: (code: string) => string }) {
  const violating = asset.first_violation_year !== null
  return (
    <tr
      onClick={() => useSelectionStore.getState().locate(asset.feature_class, asset.feature_id, { focus: true })}
      onDoubleClick={() => useSelectionStore.getState().select(asset.feature_class, asset.feature_id, { focus: true })}
      className={cn(
        "hover:bg-accent cursor-pointer border-b align-top last:border-b-0 select-none",
        violating ? "bg-destructive/10 hover:bg-destructive/15" : "bg-amber-500/10 hover:bg-amber-500/15",
      )}
    >
      <td className="px-3 py-1.5">
        <div className="flex items-start gap-1">
          <FeatureInfoButton featureClass={asset.feature_class} featureId={asset.feature_id} className="-mt-0.5" />
          <div className="min-w-0">
            <div>{classNameFor(asset.feature_class)}</div>
            <div className="text-muted-foreground font-mono text-xs">{shortId(asset.feature_id)}</div>
          </div>
        </div>
      </td>
      <td className="px-3 py-1.5 text-xs">
        <div>{limitKindLabel(asset.limit_kind)}</div>
        <div className="text-muted-foreground">
          {formatNumber(asset.rating, 0)} {asset.unit}
        </div>
      </td>
      <td className="px-3 py-1.5 whitespace-nowrap">
        {violating ? <span className="font-medium">year {asset.first_violation_year}</span> : <span className="text-muted-foreground">—</span>}
      </td>
      <td className="px-3 py-1.5 whitespace-nowrap">
        <span className={cn("font-medium", violating && "text-destructive")}>{formatNumber(asset.peak_loading_pct)}%</span>
      </td>
    </tr>
  )
}

/** The under-voltage table (shown when any node sags toward the floor): per node, the
 * first year it drops below the floor (or "approaching" if it only nears it) and its
 * worst per-unit voltage. Rows click to select the node. A node that actually crosses
 * below the floor is red; one merely approaching is amber. On long rural/LV feeders
 * this is often the FIRST limit to bind as load grows. */
function VoltageTable({ result, classNameFor }: { result: LoadGrowthResult; classNameFor: (code: string) => string }) {
  return (
    <div className="border-t">
      <p className="text-muted-foreground px-3 py-1.5 text-xs font-medium">Under-voltage (below {formatNumber(result.min_voltage_pu, 2)} pu)</p>
      <table className="w-full text-sm">
        <thead className="text-muted-foreground border-b text-left text-xs">
          <tr>
            <th className="px-3 py-1.5 font-medium">Node</th>
            <th className="px-3 py-1.5 font-medium">First under-voltage</th>
            <th className="px-3 py-1.5 font-medium">Worst voltage</th>
          </tr>
        </thead>
        <tbody>
          {result.voltage_violations.map((v) => {
            const violating = v.first_violation_year !== null
            return (
              <tr
                key={`${v.feature_class}:${v.feature_id}`}
                onClick={() => useSelectionStore.getState().locate(v.feature_class, v.feature_id, { focus: true })}
                onDoubleClick={() => useSelectionStore.getState().select(v.feature_class, v.feature_id, { focus: true })}
                className={cn(
                  "hover:bg-accent cursor-pointer border-b align-top last:border-b-0 select-none",
                  violating ? "bg-destructive/10 hover:bg-destructive/15" : "bg-amber-500/10 hover:bg-amber-500/15",
                )}
              >
                <td className="px-3 py-1.5">
                  <div className="flex items-start gap-1">
                    <FeatureInfoButton featureClass={v.feature_class} featureId={v.feature_id} className="-mt-0.5" />
                    <div className="min-w-0">
                      <div>{classNameFor(v.feature_class)}</div>
                      <div className="text-muted-foreground font-mono text-xs">{shortId(v.feature_id)}</div>
                    </div>
                  </div>
                </td>
                <td className="px-3 py-1.5 whitespace-nowrap">
                  {violating ? <span className="font-medium">year {v.first_violation_year}</span> : <span className="text-muted-foreground">approaching</span>}
                </td>
                <td className="px-3 py-1.5 whitespace-nowrap">
                  <span className={cn("font-medium", violating && "text-destructive")}>{formatNumber(v.worst_voltage_pu, 3)} pu</span>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

/** The DER-headroom-over-time table (shown only when der_headroom was requested):
 * per candidate node, its hosting capacity at year 0 vs the final year, so the shift
 * as load grows is visible at a glance. Rows click to select the node. */
function DerHeadroomTable({ result, classNameFor }: { result: LoadGrowthResult; classNameFor: (code: string) => string }) {
  const rows = result.der_headroom ?? []
  return (
    <div className="border-t">
      <p className="text-muted-foreground px-3 py-1.5 text-xs font-medium">DER headroom (hosting capacity, kW)</p>
      <table className="w-full text-sm">
        <thead className="text-muted-foreground border-b text-left text-xs">
          <tr>
            <th className="px-3 py-1.5 font-medium">Node</th>
            <th className="px-3 py-1.5 font-medium">Year 0</th>
            <th className="px-3 py-1.5 font-medium">Year {result.horizon_years}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => {
            const first = r.trajectory[0]
            const last = r.trajectory[r.trajectory.length - 1]
            return (
              <tr
                key={`${r.feature_class}:${r.feature_id}`}
                onClick={() => useSelectionStore.getState().locate(r.feature_class, r.feature_id, { focus: true })}
                onDoubleClick={() => useSelectionStore.getState().select(r.feature_class, r.feature_id, { focus: true })}
                className="hover:bg-accent select-none cursor-pointer border-b align-top last:border-b-0"
              >
                <td className="px-3 py-1.5">
                  <div className="flex items-start gap-1">
                    <FeatureInfoButton featureClass={r.feature_class} featureId={r.feature_id} className="-mt-0.5" />
                    <div className="min-w-0">
                      <div>{classNameFor(r.feature_class)}</div>
                      <div className="text-muted-foreground font-mono text-xs">{shortId(r.feature_id)}</div>
                    </div>
                  </div>
                </td>
                <td className="px-3 py-1.5 whitespace-nowrap">{first ? formatNumber(first.hosting_capacity_kw, 0) : "—"}</td>
                <td className="px-3 py-1.5 whitespace-nowrap">{last ? formatNumber(last.hosting_capacity_kw, 0) : "—"}</td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
