import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"
import type { TraceFeatureRef } from "@/stores/trace-store"

/** One node's entry in the voltage-profile report -- mirrors
 * api/internal/powerflow's profileNode exactly. voltage_pu is
 * voltage_v / nominal_base_v, the normalized figure that reads the same
 * across MV and LV (an LV node is ~1.0 pu of its own LV base, not a
 * spurious fraction of the MV nominal). within_limits is voltage_pu
 * inside the requested band. branch_loss_kw / length_m / current_amps are
 * set only for a conductor (network_role = 'edge') node -- the series I²R
 * loss this one branch dissipates, its length, and its current. */
export interface VoltageProfileNode {
  feature_class: string
  feature_id: string
  circuit_id?: string
  circuit_ref?: string
  voltage_v: number
  nominal_base_v: number
  voltage_pu: number
  voltage_drop_pct: number
  within_limits: boolean
  branch_loss_kw?: number
  length_m?: number
  current_amps?: number
}

/** The feeder-level roll-up -- mirrors api/internal/powerflow's
 * profileSummary. total_loss_kw is the summed I²R series loss; loss_pct is
 * that as a percentage of total_load_kw. min_voltage_pu is the worst
 * (lowest) per-unit voltage in the tree, at worst_node. losses_complete is
 * false when some branch had no resolved R/X, so the total is a lower
 * bound. */
export interface VoltageProfileSummary {
  total_load_kw: number
  total_load_kvar: number
  total_loss_kw: number
  loss_pct: number
  min_voltage_pu: number
  worst_node?: TraceFeatureRef
  violation_count: number
  losses_complete: boolean
}

/** Mirrors api/internal/powerflow's voltageProfileResponse. `violations`
 * is the subset of `nodes` whose voltage_pu fell outside [min,max];
 * `barriers` is where the walk stopped at a live open device, same
 * meaning as load-flow's. */
export interface VoltageProfileResult {
  start: TraceFeatureRef
  nominal_voltage_v: number
  min_voltage_pu: number
  max_voltage_pu: number
  nodes: VoltageProfileNode[]
  violations: TraceFeatureRef[]
  summary: VoltageProfileSummary
  barriers: TraceFeatureRef[]
}

/**
 * Same "not persisted, several siblings read one store" arm/result shape
 * as load-flow-store.ts -- the trigger (feature-panel.tsx) and the
 * combined prompt/result panel (voltage-profile-panel.tsx) read this
 * store rather than prop-drilling. Kept separate from load-flow-store.ts:
 * although both run the same sweep server-side, this report's result is a
 * different shape (per-unit voltages + a loss summary, not per-node
 * loading) and the two are mutually exclusive in the UI, so it clears and
 * is cleared by the other analysis workflows exactly as they clear each
 * other.
 */
interface VoltageProfileState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  result: VoltageProfileResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: VoltageProfileResult) => void
  clear: () => void
}

export const useVoltageProfileStore = create<VoltageProfileState>()(
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
    resultPersist("voltedge-voltage-profile-result"),
  ),
)
