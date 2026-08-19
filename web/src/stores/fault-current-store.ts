import { create } from "zustand"

import type { TraceFeatureRef } from "@/stores/trace-store"

/** One node's resolved fault-duty state -- mirrors api/internal/powerflow's
 * faultNodeResult exactly. ROhmTotal/XOhmTotal/ZOhmTotal are always
 * present (zero for the start node itself when no source impedance is
 * set). FaultCurrentAmps is omitted -- not zero, not present as null --
 * when ZOhmTotal is zero: an ideal, zero-impedance path makes fault
 * current mathematically unbounded, not a real number to report. */
export interface FaultCurrentNodeResult {
  feature_class: string
  feature_id: string
  r_ohm_total: number
  x_ohm_total: number
  z_ohm_total: number
  fault_current_amps?: number
  material_resolved: boolean
}

/** Mirrors api/internal/powerflow's faultCurrentResponse -- see
 * docs/phase-8-fault-current.md. `barriers` is the subset of node
 * references the walk actually stopped at because of a live, open
 * device, same "stopped here, not just ran out of network" distinction
 * every other trace/analysis store in this app already draws. Unlike
 * LoadFlowResult there's no `overloaded` -- fault current has no
 * capacity-vs-rating concept of its own, it's a raw available-current
 * reading. */
export interface FaultCurrentResult {
  start: TraceFeatureRef
  nominal_voltage_v: number
  nodes: FaultCurrentNodeResult[]
  barriers: TraceFeatureRef[]
}

/**
 * Same "not persisted, several siblings read one store" shape as
 * load-flow-store.ts -- the trigger (feature-panel.tsx), the map
 * (voltedge-map.tsx), and the combined prompt/results panel
 * (fault-current-panel.tsx) are three siblings reading this store.
 * Kept as its own store rather than folded into load-flow-store.ts: a
 * genuinely different result shape (impedance/fault-duty, not voltage/
 * demand), and trace/isolate/load-flow/fault-current are all mutually
 * exclusive UI workflows -- running one clears the other three.
 */
interface FaultCurrentState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  result: FaultCurrentResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: FaultCurrentResult) => void
  clear: () => void
}

export const useFaultCurrentStore = create<FaultCurrentState>((set) => ({
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
