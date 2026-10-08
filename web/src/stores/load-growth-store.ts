import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"
import type { TraceFeatureRef } from "@/stores/trace-store"

/** One year's loading for an asset -- mirrors api/internal/powerflow's
 * forecastPoint. */
export interface ForecastPoint {
  year: number
  loading_pct: number
}

/** One stressed asset's projection -- mirrors forecastAsset. first_violation_year
 * is null when the asset never exceeds its limit within the horizon; trajectory is
 * the year-by-year loading. limit_kind is "transformer_loading" | "ampacity". */
export interface ForecastAsset {
  feature_class: string
  feature_id: string
  circuit_ref?: string
  limit_kind: string
  rating: number
  unit: string
  first_violation_year: number | null
  peak_loading_pct: number
  trajectory: ForecastPoint[]
}

/** One year's per-unit voltage at a node -- mirrors voltagePoint. */
export interface VoltagePoint {
  year: number
  voltage_pu: number
}

/** One node that sags toward/below the under-voltage floor as load grows -- mirrors
 * voltageViolation. first_violation_year is null when it only approaches the floor. */
export interface VoltageViolation {
  feature_class: string
  feature_id: string
  circuit_ref?: string
  first_violation_year: number | null
  worst_voltage_pu: number
  trajectory: VoltagePoint[]
}

/** One year's DER hosting capacity at a node -- mirrors derHeadroomPoint. */
export interface DerHeadroomPoint {
  year: number
  hosting_capacity_kw: number
  binding_constraint: string
}

/** One candidate node's DER-headroom-over-time curve -- mirrors derHeadroomAsset. */
export interface DerHeadroomAsset {
  feature_class: string
  feature_id: string
  circuit_ref?: string
  trajectory: DerHeadroomPoint[]
}

/** Mirrors api/internal/powerflow's forecastResponse -- the stressed assets with
 * their loading trajectories, the reinforcement order (violating assets soonest +
 * most severe first), the optional DER-headroom curves, the echoed assumptions, and
 * the Derived validation label. */
export interface LoadGrowthResult {
  start: TraceFeatureRef
  nominal_voltage_v: number
  horizon_years: number
  base_growth_pct: number
  min_voltage_pu: number
  assets: ForecastAsset[]
  voltage_violations: VoltageViolation[]
  reinforcement_order: TraceFeatureRef[]
  der_headroom?: DerHeadroomAsset[]
  assumptions: string[]
  validation: string
}

/**
 * Same "not persisted, siblings read one store" arm/result shape as the other
 * analysis stores (load-flow, hosting, contingency). The trigger (feature-panel.tsx)
 * arms with the feeder's start; the combined prompt/result panel (load-growth-panel.tsx)
 * reads this store. Mutually exclusive with the other analysis workflows, so it clears
 * and is cleared by them exactly as they clear each other.
 */
interface LoadGrowthState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  result: LoadGrowthResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: LoadGrowthResult) => void
  clear: () => void
}

export const useLoadGrowthStore = create<LoadGrowthState>()(
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
    resultPersist("voltedge-load-growth-result"),
  ),
)
