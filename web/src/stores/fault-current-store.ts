import { create } from "zustand"

import type { TraceFeatureRef } from "@/stores/trace-store"

/** One node's symmetrical (3-phase bolted) fault level -- mirrors
 * api/internal/powerflow's faultNodeResult exactly. resistance_ohm/
 * reactance_ohm are the accumulated series R and X from the source down
 * to this node; impedance_ohm is their magnitude. fault_current_amps is
 * absent only when |Z| is zero (an ideal source with no conductor yet
 * between it and this node -- an unbounded, un-reportable figure).
 * material_resolved is false when any conductor branch on the path lacked
 * resolved R/X, in which case the reported current is an optimistic upper
 * bound (a missing branch impedance can only lower the true fault
 * current). */
export interface FaultCurrentNodeResult {
  feature_class: string
  feature_id: string
  resistance_ohm: number
  reactance_ohm: number
  impedance_ohm: number
  fault_current_amps?: number
  material_resolved: boolean
}

/** Mirrors api/internal/powerflow's faultCurrentResponse -- see
 * docs/phase-8-load-flow.md. `barriers` is the subset of node references
 * the walk stopped at because of a live, open device -- a bolted fault
 * downstream of an open point draws no current from this source, so those
 * nodes never appear in `nodes`, same distinction the load-flow result
 * already draws. */
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
 * (fault-current-panel.tsx) all read this store. Kept as its own store
 * rather than folded into load-flow-store.ts: a fault-current result is a
 * genuinely different shape (per-node impedance + prospective current,
 * not voltage/loading), even though the input prompt (nominal_voltage_v)
 * and the armed/result lifecycle are identical. Fault current joins
 * trace/isolate/load-flow as a fourth mutually-exclusive UI workflow --
 * arming or running one clears the other three. */
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
