import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"
import type { TraceFeatureRef } from "@/stores/trace-store"

/** The feeder-level state after one sweep -- mirrors api/internal/powerflow's
 * tapMetrics (the storage-dispatch study reads the same both-bound feeder
 * metrics). min_voltage_pu / max_voltage_pu are the worst-low and worst-high
 * per-unit voltages (at worst_low_node / worst_high_node); total_loss_kw is the
 * summed series I²R loss (a lower bound when losses_complete is false);
 * source_kw / source_kvar is the slack draw and source_pf the power factor it
 * implies; under_count / over_count are how many nodes sit below / above the
 * band. Both extremes matter, since discharging pushes voltage up as charging
 * pulls it down. */
export interface StorageMetrics {
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

/** One battery-storage unit found on the feeder -- mirrors storageUnit.
 * rated_power_kw is the PCS power bound the sweep explores; dispatch_kw_now is
 * the live signed set-point (+ discharge, − charge); energy_capacity_kwh and
 * state_of_charge_pct size and fill the store; min_dispatch_kw / max_dispatch_kw
 * are the SoC-feasible window actually swept (an empty battery is charge-only, a
 * full one discharge-only). has_state_of_charge says whether SoC was known. */
export interface StorageUnit {
  node: TraceFeatureRef
  rated_power_kw: number
  dispatch_kw_now: number
  energy_capacity_kwh: number
  state_of_charge_pct: number
  has_state_of_charge: boolean
  min_dispatch_kw: number
  max_dispatch_kw: number
}

/** One battery evaluated across its dispatch range -- mirrors
 * storageRecommendation. dispatch_kw_from / dispatch_kw_to are the live and
 * recommended signed set-point, mode_to labels the recommended direction
 * (discharge / charge / idle); the *_from / *_to pairs are the feeder's
 * worst-low and worst-high voltage, source real-power draw, loss and source
 * power factor before and after; peak_shave_kw is the reduction in source draw
 * magnitude; violations_from / violations_to are the band-violation counts and
 * resolves_violations is true when the move clears every violation. */
export interface StorageRecommendation {
  node: TraceFeatureRef
  rated_power_kw: number
  dispatch_kw_from: number
  dispatch_kw_to: number
  mode_to: string
  min_voltage_pu_from: number
  min_voltage_pu_to: number
  max_voltage_pu_from: number
  max_voltage_pu_to: number
  source_kw_from: number
  source_kw_to: number
  peak_shave_kw: number
  total_loss_kw_from: number
  total_loss_kw_to: number
  source_pf_from: number
  source_pf_to: number
  violations_from: number
  violations_to: number
  resolves_violations: boolean
}

/** Mirrors api/internal/powerflow's storageResponse -- the feeder slack / band /
 * swept resolution assumed, the base-case metrics, every battery found, and one
 * recommendation per battery. */
export interface StorageDispatchResult {
  source: TraceFeatureRef
  nominal_voltage_v: number
  min_voltage_pu: number
  max_voltage_pu: number
  dispatch_steps: number
  base: StorageMetrics
  units: StorageUnit[]
  recommendations: StorageRecommendation[]
}

/**
 * Same "not persisted, siblings read one store" arm/result shape as the other
 * analysis stores (load-flow, hosting, ev-hosting, volt-var, tap-optimization).
 * The trigger (feature-panel.tsx) arms with the feeder's source; the combined
 * prompt/result panel (storage-dispatch-panel.tsx) reads this store. Mutually
 * exclusive with the other analysis workflows, so it clears and is cleared by
 * them exactly as they clear each other.
 */
interface StorageDispatchState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  result: StorageDispatchResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: StorageDispatchResult) => void
  clear: () => void
}

export const useStorageDispatchStore = create<StorageDispatchState>()(
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
    resultPersist("voltedge-storage-dispatch-result"),
  ),
)
