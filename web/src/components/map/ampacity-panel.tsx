import { useState } from "react"
import { X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import { ApiError, apiFetch } from "@/lib/api"
import { type AmpacityResult, useAmpacityStore } from "@/stores/ampacity-store"
import { useToastStore } from "@/stores/toast-store"

function formatNumber(n: number, digits = 1): string {
  return n.toLocaleString(undefined, { minimumFractionDigits: digits, maximumFractionDigits: digits })
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
    <div className="bg-popover text-popover-foreground fixed bottom-10 left-1/2 z-40 max-h-96 w-[28rem] max-w-[calc(100vw-2rem)] -translate-x-1/2 overflow-hidden rounded-md border shadow-lg">
      <div className="flex items-center justify-between border-b px-3 py-2">
        <p className="truncate text-sm font-medium">Ampacity</p>
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
        <div className="space-y-3 overflow-y-auto p-3">
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
      )}
    </div>
  )
}
