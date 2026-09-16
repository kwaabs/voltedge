import { useState } from "react"
import { X } from "lucide-react"

import { AnalysisExplanation, Formula } from "@/components/map/analysis-explanation"
import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { useFloatingPanel } from "@/hooks/use-floating-panel"
import { ApiError, apiFetch } from "@/lib/api"
import { cn } from "@/lib/utils"
import { type AmpacityResult, useAmpacityStore } from "@/stores/ampacity-store"
import { useToastStore } from "@/stores/toast-store"

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
}

/**
 * The dynamic "how this was calculated" body for an ampacity run. The
 * heat-balance terms and the resulting rating are read off THIS result -- the
 * governing net cooling, the conductor's own resistance at temperature, and the
 * catalog figure it's checked against -- so an engineer can reconstruct this
 * rating rather than read a generic description of IEEE 738.
 */
function AmpacityExplanation({ result }: { result: AmpacityResult }) {
  const netW = result.convective_loss_w_per_m + result.radiative_loss_w_per_m - result.solar_gain_w_per_m
  const staticAmps = result.static_ampacity_amps
  const deltaVsStatic =
    staticAmps !== undefined && staticAmps > 0 ? (result.ampacity_amps / staticAmps - 1) * 100 : undefined

  return (
    <AnalysisExplanation>
      <p>
        Steady-state <b>IEEE 738</b> heat balance, evaluated directly at the conductor's rated maximum operating
        temperature <b>{formatNumber(result.max_operating_temp_c, 0)}°C</b> against a{" "}
        <b>{formatNumber(result.ambient_temp_c, 0)}°C</b> ambient (closed-form, no iteration -- with constant air
        properties the losses don't depend on current). The conductor sheds heat by convection and radiation and
        gains it from the sun; the ampacity is the current whose <code>I²R</code> exactly balances the net:{" "}
        <code>I = √((qc + qr − qs) / R(Tc))</code>.
      </p>
      <p>
        Convective <code>qc = {formatNumber(result.convective_loss_w_per_m, 1)}</code> W/m, radiative{" "}
        <code>qr = {formatNumber(result.radiative_loss_w_per_m, 1)}</code> W/m, solar gain{" "}
        <code>qs = {formatNumber(result.solar_gain_w_per_m, 1)}</code> W/m, so net cooling{" "}
        <b>{formatNumber(netW, 1)} W/m</b>. With <code>R(Tc) = {formatNumber(result.resistance_ohm_per_m * 1000, 4)}</code>{" "}
        Ω/km the rating comes out to <b>{formatNumber(result.ampacity_amps, 0)}A</b> for material spec{" "}
        <code>{result.material_spec_code}</code>.
      </p>
      <Formula>{`I = √((qc + qr − qs) / R(Tc))
  = √((${formatNumber(result.convective_loss_w_per_m, 1)} + ${formatNumber(result.radiative_loss_w_per_m, 1)} − ${formatNumber(result.solar_gain_w_per_m, 1)}) W/m ÷ ${formatNumber(result.resistance_ohm_per_m, 6)} Ω/m)
  = √(${formatNumber(netW, 1)} / ${formatNumber(result.resistance_ohm_per_m, 6)})
  = ${formatNumber(result.ampacity_amps, 0)} A`}</Formula>
      {staticAmps !== undefined ? (
        <p>
          The material catalog's own static figure is <b>{formatNumber(staticAmps, 0)}A</b> (shown for comparison
          only -- this calc never reads it)
          {deltaVsStatic !== undefined && (
            <>
              , so this first-principles rating is{" "}
              <b>
                {deltaVsStatic >= 0 ? "+" : ""}
                {formatNumber(deltaVsStatic, 0)}%
              </b>{" "}
              {deltaVsStatic >= 0 ? "above" : "below"} catalog under these conditions
            </>
          )}
          .
        </p>
      ) : (
        <p>No catalog static ampacity is set on this spec, so there is no reference figure to check against.</p>
      )}
      {result.solar_gain_w_per_m === 0 && (
        <p>
          Solar gain is <b>0 W/m</b> -- this is a <b>clear-night</b> rating. A daytime rating needs its own solar
          flux; omitting it here does <b>not</b> fall back to a conservative default, so this figure would
          <b> overstate</b> ampacity under sun.
        </p>
      )}
    </AnalysisExplanation>
  )
}

/**
 * Floating panel for api/internal/powerflow's first-principles
 * ampacity engine (see docs/phase-8-ampacity.md) -- same fixed
 * bottom-center slot every other analysis panel uses, prompting for
 * the environmental conditions the calc genuinely can't guess
 * (ambient_temp_c has no safe default at all; wind/solar do, but the
 * solar default is a non-conservative one -- see the tooltip below).
 * Unlike load-flow-panel.tsx/fault-current-panel.tsx there's no
 * per-node table: this engine computes one figure for the one
 * conductor already selected, so the result view is a compact
 * key/value readout instead.
 */
export function AmpacityPanel() {
  const armed = useAmpacityStore((s) => s.armed)
  const loading = useAmpacityStore((s) => s.loading)
  const result = useAmpacityStore((s) => s.result)
  const [ambientInput, setAmbientInput] = useState("25")
  const [windSpeedInput, setWindSpeedInput] = useState("0")
  const [windAngleInput, setWindAngleInput] = useState("90")
  const [solarInput, setSolarInput] = useState("0")
  const fp = useFloatingPanel(448, "max-h-96")

  if (!armed && !result) return null

  async function runAmpacity() {
    const conductor = useAmpacityStore.getState().armedConductor
    if (!conductor) return

    const ambientTempC = Number(ambientInput)
    const windSpeedMps = Number(windSpeedInput)
    const windAngleDeg = Number(windAngleInput)
    const solarHeatFluxWM2 = Number(solarInput)
    if (![ambientTempC, windSpeedMps, windAngleDeg, solarHeatFluxWM2].every(Number.isFinite)) {
      useToastStore.getState().pushError("Every field must be a number.")
      return
    }
    if (windSpeedMps < 0 || solarHeatFluxWM2 < 0) {
      useToastStore.getState().pushError("Wind speed and solar heat flux must not be negative.")
      return
    }

    useAmpacityStore.getState().setLoading(true)
    try {
      const response = await apiFetch<AmpacityResult>("/analysis/ampacity", {
        method: "POST",
        body: JSON.stringify({
          conductor,
          ambient_temp_c: ambientTempC,
          wind_speed_mps: windSpeedMps,
          wind_angle_deg: windAngleDeg,
          solar_heat_flux_w_m2: solarHeatFluxWM2,
        }),
      })
      useAmpacityStore.getState().setResult(response)
    } catch (err) {
      useAmpacityStore.getState().setLoading(false)
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not compute ampacity.")
    }
  }

  return (
    <div ref={fp.panelRef} style={fp.shellStyle} className={fp.shellClassName}>
      <div onPointerDown={fp.onHeaderPointerDown} className={fp.headerClassName}>
        <div className="flex min-w-0 items-center gap-1.5">
          {fp.grip}
          <p className="truncate text-sm font-medium">Ampacity</p>
        </div>
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={() => useAmpacityStore.getState().clear()}>
          <X className="size-3.5" />
          <span className="sr-only">Close</span>
        </Button>
      </div>

      {!result ? (
        <div className="space-y-3 p-3">
          <div className="grid grid-cols-2 gap-x-3 gap-y-2">
            <label className="text-muted-foreground self-center text-sm" htmlFor="ampacity-ambient">
              Ambient temp (°C)
            </label>
            <input
              id="ampacity-ambient"
              type="number"
              className={inputClass}
              value={ambientInput}
              onChange={(e) => setAmbientInput(e.target.value)}
              autoFocus
            />
            <label className="text-muted-foreground self-center text-sm" htmlFor="ampacity-wind-speed">
              Wind speed (m/s)
            </label>
            <input
              id="ampacity-wind-speed"
              type="number"
              className={inputClass}
              value={windSpeedInput}
              onChange={(e) => setWindSpeedInput(e.target.value)}
            />
            <label className="text-muted-foreground self-center text-sm" htmlFor="ampacity-wind-angle">
              Wind angle (deg)
            </label>
            <input
              id="ampacity-wind-angle"
              type="number"
              className={inputClass}
              value={windAngleInput}
              onChange={(e) => setWindAngleInput(e.target.value)}
            />
            <label
              className="text-muted-foreground self-center text-sm"
              htmlFor="ampacity-solar"
              title="Left at 0, this assumes no solar heating (a clear night). A daytime rating needs its own solar figure -- omitting it doesn't fall back to a conservative default."
            >
              Solar flux (W/m²)
            </label>
            <input
              id="ampacity-solar"
              type="number"
              className={inputClass}
              value={solarInput}
              onChange={(e) => setSolarInput(e.target.value)}
            />
          </div>
          <Button size="sm" className="w-full" onClick={runAmpacity} disabled={loading}>
            {loading ? "Running…" : "Run"}
          </Button>
        </div>
      ) : (
        <>
          <div className={cn("space-y-3 overflow-y-auto p-3", fp.resized && "min-h-0 flex-1")}>
          <div className="flex items-center justify-between gap-2">
            <span className="text-muted-foreground text-xs">Material spec {result.material_spec_code}</span>
            <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={() => useAmpacityStore.getState().rearm()}>
              Run again
            </Button>
          </div>

          <div className="bg-muted/40 rounded-md border p-3 text-center">
            <div className="text-3xl font-semibold tabular-nums">{formatNumber(result.ampacity_amps, 0)}A</div>
            <div className="text-muted-foreground text-xs">
              first-principles ampacity at {formatNumber(result.max_operating_temp_c, 0)}°C rated
            </div>
            {result.static_ampacity_amps !== undefined && (
              <div className="text-muted-foreground mt-1 text-xs">
                catalog static figure: {formatNumber(result.static_ampacity_amps, 0)}A
              </div>
            )}
          </div>

          <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 text-sm">
            <dt className="text-muted-foreground">Ambient</dt>
            <dd>{formatNumber(result.ambient_temp_c, 0)}°C</dd>
            <dt className="text-muted-foreground">Convective loss</dt>
            <dd>{formatNumber(result.convective_loss_w_per_m, 1)} W/m</dd>
            <dt className="text-muted-foreground">Radiative loss</dt>
            <dd>{formatNumber(result.radiative_loss_w_per_m, 1)} W/m</dd>
            <dt className="text-muted-foreground">Solar gain</dt>
            <dd>{formatNumber(result.solar_gain_w_per_m, 1)} W/m</dd>
            <dt className="text-muted-foreground">Resistance</dt>
            <dd>{formatNumber(result.resistance_ohm_per_m * 1000, 4)} Ω/km</dd>
          </dl>
          </div>
          <AmpacityExplanation result={result} />
        </>
      )}
      {fp.resizeHandle}
    </div>
  )
}
