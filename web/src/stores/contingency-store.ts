import { create } from "zustand"

import type { TraceFeatureRef } from "@/stores/trace-store"

/** One candidate tie's restoration result -- mirrors
 * api/internal/powerflow's restorationOption. Close this tie and the donor
 * feeder picks up restored_nodes (restored_load_kw of new demand), reaching
 * a worst post-transfer per-unit voltage of min_voltage_pu at worst_node.
 * violations are the newly-swept nodes outside the voltage band; overloaded
 * are the branches/transformers past 100 % loading once load transfers.
 * feasible is true when the transfer restores load with no violation or
 * overload. normal_state is the tie's declared posture (NORMALLY_OPEN ties
 * rank first). note carries why an option is infeasible or trivial. */
export interface RestorationOption {
  tie: TraceFeatureRef
  normal_state?: string
  restored_load_kw: number
  restored_load_kvar: number
  restored_node_count: number
  restored_nodes: TraceFeatureRef[]
  min_voltage_pu: number
  worst_node?: TraceFeatureRef
  violations: TraceFeatureRef[]
  overloaded: TraceFeatureRef[]
  feasible: boolean
  note?: string
}

/** Mirrors api/internal/powerflow's contingencyResponse -- the donor
 * feeder's base load/reach, the judging band, and one ranked restoration
 * option per candidate tie. */
export interface ContingencyResult {
  source: TraceFeatureRef
  nominal_voltage_v: number
  min_voltage_pu: number
  max_voltage_pu: number
  base_load_kw: number
  base_load_kvar: number
  base_node_count: number
  options: RestorationOption[]
}

/**
 * Same "not persisted, siblings read one store" arm/result shape as the
 * other analysis stores (load-flow, voltage-profile). The trigger
 * (feature-panel.tsx) arms with the donor feeder's source; the combined
 * prompt/result panel (contingency-panel.tsx) reads this store. Mutually
 * exclusive with the other analysis workflows, so it clears and is cleared
 * by them exactly as they clear each other.
 */
interface ContingencyState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  result: ContingencyResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: ContingencyResult) => void
  clear: () => void
}

export const useContingencyStore = create<ContingencyState>((set) => ({
  armed: false,
  armedStart: null,
  result: null,
  loading: false,
  arm: (start) => set({ armed: true, armedStart: start, result: null, loading: false }),
  rearm: () => set({ armed: true, result: null, loading: false }),
  setLoading: (loading) => set({ loading }),
  setResult: (result) => set({ result, loading: false }),
  clear: () => set({ armed: false, armedStart: null, result: null, loading: false }),
}))
