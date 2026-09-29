import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"
import type { TraceFeatureRef } from "@/stores/trace-store"

/** The feeder-level state after one sweep -- mirrors api/internal/powerflow's
 * tapMetrics, the same both-bound feeder metrics the VVO reads for its base and
 * optimised cases. min_voltage_pu / max_voltage_pu are the worst-low and
 * worst-high per-unit voltages (at worst_low_node / worst_high_node);
 * total_loss_kw is the summed series I²R loss (a lower bound when losses_complete
 * is false); source_kw / source_kvar is the slack draw and source_pf the power
 * factor; under_count / over_count are how many nodes sit below / above the
 * band. */
export interface VvoMetrics {
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

/** One control the optimisation touched -- mirrors vvoAction. kind is
 * capacitor / regulator / battery; setting_from / setting_to are the human
 * setting held before and the one VVO chose; changed is whether that is a move. */
export interface VvoAction {
  node: TraceFeatureRef
  kind: string
  setting_from: string
  setting_to: string
  changed: boolean
}

/** Mirrors api/internal/powerflow's vvoResponse -- the feeder slack / band
 * assumed, how many coordinate-descent rounds ran and whether it converged, the
 * base-case and optimised feeder metrics, and one action per control. */
export interface VvoResult {
  source: TraceFeatureRef
  nominal_voltage_v: number
  min_voltage_pu: number
  max_voltage_pu: number
  rounds: number
  converged: boolean
  base: VvoMetrics
  optimized: VvoMetrics
  actions: VvoAction[]
}

/**
 * Same "not persisted, siblings read one store" arm/result shape as the other
 * analysis stores (load-flow, hosting, volt-var, tap-optimization,
 * storage-dispatch). The trigger (feature-panel.tsx) arms with the feeder's
 * source; the combined prompt/result panel (vvo-panel.tsx) reads this store.
 * Mutually exclusive with the other analysis workflows, so it clears and is
 * cleared by them exactly as they clear each other.
 */
interface VvoState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  result: VvoResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: VvoResult) => void
  clear: () => void
}

export const useVvoStore = create<VvoState>()(
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
    resultPersist("voltedge-vvo-result"),
  ),
)
