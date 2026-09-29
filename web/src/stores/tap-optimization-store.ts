import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"
import type { TraceFeatureRef } from "@/stores/trace-store"

/** The feeder-level state after one sweep -- mirrors api/internal/powerflow's
 * tapMetrics. min_voltage_pu / max_voltage_pu are the worst-low and worst-high
 * per-unit voltages (at worst_low_node / worst_high_node); total_loss_kw is the
 * summed series I²R loss (a lower bound when losses_complete is false);
 * source_kw / source_kvar is the slack draw and source_pf the power factor it
 * implies; under_count / over_count are how many nodes sit below / above the
 * band. A regulator study tracks BOTH voltage extremes, since a regulator can
 * buck as well as boost. */
export interface TapMetrics {
  min_voltage_pu: number
  worst_low_node?: TraceFeatureRef
  max_voltage_pu: number
  worst_high_node?: TraceFeatureRef
  total_loss_kw: number
  source_kw: number
  source_kvar: number
  source_pf: number
  under_count: number
  over_count: number
  losses_complete: boolean
}

/** One step voltage regulator found on the feeder -- mirrors regulatorInfo.
 * step_pct is the per-step boost percentage; current_tap is the live tap and
 * ratio_now the ratio it implies; bypassed is true when the regulator's
 * device_state is anything other than CLOSED (out of regulation). */
export interface RegulatorInfo {
  node: TraceFeatureRef
  step_pct: number
  current_tap: number
  ratio_now: number
  bypassed: boolean
}

/** One regulator evaluated across its tap range -- mirrors tapRecommendation.
 * tap_from / tap_to (and ratio_from / ratio_to) are the live and recommended
 * tap; the *_from / *_to pairs are the feeder's worst-low and worst-high
 * voltage, total loss, and source power factor before and after moving to the
 * recommended tap; violations_from / violations_to are the band-violation counts
 * and resolves_violations is true when the move clears every violation. */
export interface TapRecommendation {
  node: TraceFeatureRef
  step_pct: number
  tap_from: number
  tap_to: number
  ratio_from: number
  ratio_to: number
  min_voltage_pu_from: number
  min_voltage_pu_to: number
  max_voltage_pu_from: number
  max_voltage_pu_to: number
  total_loss_kw_from: number
  total_loss_kw_to: number
  source_pf_from: number
  source_pf_to: number
  violations_from: number
  violations_to: number
  resolves_violations: boolean
}

/** Mirrors api/internal/powerflow's tapOptResponse -- the feeder slack / band /
 * swept tap range assumed, the base-case metrics, every regulator found, and one
 * recommendation per regulator. */
export interface TapOptResult {
  source: TraceFeatureRef
  nominal_voltage_v: number
  min_voltage_pu: number
  max_voltage_pu: number
  max_tap_steps: number
  base: TapMetrics
  regulators: RegulatorInfo[]
  recommendations: TapRecommendation[]
}

/**
 * Same "not persisted, siblings read one store" arm/result shape as the other
 * analysis stores (load-flow, hosting, ev-hosting, volt-var). The trigger
 * (feature-panel.tsx) arms with the feeder's source; the combined prompt/result
 * panel (tap-optimization-panel.tsx) reads this store. Mutually exclusive with
 * the other analysis workflows, so it clears and is cleared by them exactly as
 * they clear each other.
 */
interface TapOptState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  result: TapOptResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: TapOptResult) => void
  clear: () => void
}

export const useTapOptimizationStore = create<TapOptState>()(
  persist(
    (set) => ({
      armed: false,
      armedStart: null,
      result: null,
      loading: false,
      arm: (start) => set({ armed: true, armedStart: start, result: null, loading: false }),
      rearm: () => set({ armed: true, result: null, loading: false }),
      setLoading: (loading) => set({ loading }),
      setResult: (result) => set({ result, loading: false }),
      clear: () => set({ armed: false, armedStart: null, result: null, loading: false }),
    }),
    resultPersist("voltedge-tap-optimization-result"),
  ),
)
