import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"
import type { TraceFeatureRef } from "@/stores/trace-store"

/** The feeder-level state after one sweep -- mirrors api/internal/powerflow's
 * voltVarMetrics. min_voltage_pu is the worst (lowest) per-unit voltage anywhere
 * (at worst_node); total_loss_kw is the summed series I²R loss (a lower bound
 * when losses_complete is false); source_kw / source_kvar is the slack draw and
 * source_pf the power factor it implies; violation_count is how many nodes sit
 * below the lower voltage band. */
export interface VoltVarMetrics {
  min_voltage_pu: number
  worst_node?: TraceFeatureRef
  total_loss_kw: number
  source_kw: number
  source_kvar: number
  source_pf: number
  violation_count: number
  losses_complete: boolean
}

/** One capacitor bank found on the feeder -- mirrors capBank. rated_kvar is the
 * nameplate reactive output; switchable is true for a switched bank (carries a
 * device_state); active is whether it is currently in circuit and injecting. An
 * active bank is already in the base sweep; an inactive switchable bank is a
 * switching candidate. */
export interface CapBank {
  node: TraceFeatureRef
  rated_kvar: number
  switchable: boolean
  active: boolean
}

/** One switchable, currently-out bank evaluated as a candidate to switch IN --
 * mirrors voltVarRecommendation. The *_from / *_to pairs are the feeder's worst
 * voltage, total loss, and source power factor before and after closing it;
 * voltage_rise_pu and loss_reduction_kw are the deltas (either can be negative
 * where a bank over-compensates); resolves_under_voltage is true when closing it
 * clears every under-voltage violation the base case had. */
export interface VoltVarRecommendation {
  node: TraceFeatureRef
  rated_kvar: number
  min_voltage_pu_from: number
  min_voltage_pu_to: number
  voltage_rise_pu: number
  total_loss_kw_from: number
  total_loss_kw_to: number
  loss_reduction_kw: number
  source_pf_from: number
  source_pf_to: number
  resolves_under_voltage: boolean
}

/** Mirrors api/internal/powerflow's voltVarResponse -- the feeder slack / band
 * assumed, the base-case metrics, every capacitor bank found, and one
 * recommendation per switchable out-of-circuit bank. */
export interface VoltVarResult {
  source: TraceFeatureRef
  nominal_voltage_v: number
  min_voltage_pu: number
  max_voltage_pu: number
  base: VoltVarMetrics
  banks: CapBank[]
  recommendations: VoltVarRecommendation[]
}

/**
 * Same "not persisted, siblings read one store" arm/result shape as the other
 * analysis stores (load-flow, hosting, ev-hosting, contingency). The trigger
 * (feature-panel.tsx) arms with the feeder's source; the combined prompt/result
 * panel (volt-var-panel.tsx) reads this store. Mutually exclusive with the other
 * analysis workflows, so it clears and is cleared by them exactly as they clear
 * each other.
 */
interface VoltVarState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  result: VoltVarResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: VoltVarResult) => void
  clear: () => void
}

export const useVoltVarStore = create<VoltVarState>()(
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
    resultPersist("voltedge-volt-var-result"),
  ),
)
