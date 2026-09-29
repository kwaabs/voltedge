import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"
import type { LoadFlowNodeResult } from "@/stores/load-flow-store"
import type { TraceFeatureRef } from "@/stores/trace-store"

/** One closed (or, at an open device, left-open) loop's report -- mirrors
 * api/internal/powerflow's loopResult. `closer`/`existing` are the loop-closing
 * edge's two endpoints, `loop_resistance_ohm`/`loop_reactance_ohm` the loop's
 * own series impedance, `residual_mismatch_v` the voltage mismatch left after
 * compensation (~0 for a converged loop), `impedance_resolved` whether every
 * branch on the loop resolved its impedance, and `compensated` whether the loop
 * was actually closed (false when it meets at an open device or the loop
 * impedance is degenerate -- `note` then says why). */
export interface MeshLoopResult {
  closer: TraceFeatureRef
  existing: TraceFeatureRef
  loop_resistance_ohm: number
  loop_reactance_ohm: number
  residual_mismatch_v: number
  impedance_resolved: boolean
  compensated: boolean
  note?: string
}

/** Mirrors api/internal/powerflow's meshedLoadFlowResponse (Phase 51, Tier 1 --
 * weakly-meshed load flow by loop compensation). The per-node shape is exactly
 * load-flow's (a meshed feeder reads like a radial one), plus the closed loops
 * and how many compensation passes ran (0 when the network turned out radial --
 * the endpoint degrades to an ordinary load flow with no loops). */
export interface MeshedLoadFlowResult {
  start: TraceFeatureRef
  nominal_voltage_v: number
  iterations: number
  compensation_passes: number
  loop_count: number
  nodes: LoadFlowNodeResult[]
  barriers: TraceFeatureRef[]
  overloaded: TraceFeatureRef[]
  loops: MeshLoopResult[]
}

/**
 * Same "not persisted, several siblings read one store" shape as
 * load-flow-store.ts, and mutually exclusive with every other analysis in the
 * UI -- feature-panel.tsx's triggers clear each other's stores. Kept separate
 * from load-flow-store because a meshed result carries the loop reports a
 * radial sweep never has, though the node table itself is identical.
 */
interface MeshedLoadFlowState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  result: MeshedLoadFlowResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: MeshedLoadFlowResult) => void
  clear: () => void
}

export const useMeshedLoadFlowStore = create<MeshedLoadFlowState>()(
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
    resultPersist("voltedge-meshed-load-flow-result"),
  ),
)
