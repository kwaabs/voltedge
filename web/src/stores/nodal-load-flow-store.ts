import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"
import type { TraceFeatureRef } from "@/stores/trace-store"

/** One feature's solved electrical state from the nodal (Y-bus) solver --
 * mirrors api/internal/powerflow's nodalNodeResult exactly. `role`
 * distinguishes how to read the row: a "bus"/"slack" node reports the solved
 * voltage of the bus it sits on; a "conductor"/"transformer" node reports the
 * through-current/power flowing through it and its loading. VoltageV /
 * nominal_base_v are always on the node's own voltage base (the run nominal
 * above every transformer, a transformer's secondary below it), the same
 * convention /load-flow uses -- so a single MV -> DIST_XFMR -> LV result reports
 * each node's drop against its own side's nominal. */
export interface NodalNodeResult {
  feature_class: string
  feature_id: string
  role: string
  voltage_v: number
  voltage_drop_pct: number
  nominal_base_v: number
  current_amps?: number
  ampacity_amps?: number
  rated_kva?: number
  loading_pct?: number
  through_kw?: number
  through_kvar?: number
  /** Net power INJECTED into the network at a source (role "slack") or grid-
   * forming (role "pv") bus -- how the load splits across infeeds. Undefined for
   * an ordinary bus or a branch. A PV bus's reactive_kvar is the reactive output
   * the solver found to hold its voltage setpoint (clamped to its limit if any). */
  supplied_kw?: number
  reactive_kvar?: number
  material_resolved: boolean
}

/** One extra grid-infeed (slack) source beyond the start, sent in the request
 * (S3 multi-slack). Just a feature reference; the solver holds it at the run
 * nominal on its own propagated base. */
export type NodalSourceRef = TraceFeatureRef

/** One per-run PV (grid-forming) designation/override sent in the request. A
 * generator is already PV if its own voltage_setpoint_pu asset field is set; an
 * entry here forces PV and/or overrides the setpoint and reactive limits. */
export interface NodalRegulatedGen {
  feature: TraceFeatureRef
  voltage_pu?: number
  q_min_kvar?: number
  q_max_kvar?: number
}

/** Mirrors api/internal/powerflow's nodalLoadFlowResponse (Phase 52, Tier 2 --
 * see docs/phase-52-nodal-powerflow.md). Unlike the radial load-flow response
 * this carries the solver's own convergence telemetry (`solver`/`converged`/
 * `iterations`/`max_mismatch_pu`) and the contracted model it actually solved
 * (`bus_count`/`branch_count`), since a nodal solve is iterative and a
 * non-converged result is returned flagged rather than silently trusted.
 * `barriers` is the subset of node references the walk stopped at because of a
 * live, open device; `overloaded` is the subset of `nodes` over 100% loading. */
export interface NodalLoadFlowResult {
  start: TraceFeatureRef
  nominal_voltage_v: number
  solver: string
  converged: boolean
  iterations: number
  max_mismatch_pu: number
  bus_count: number
  branch_count: number
  slack_count: number
  nodes: NodalNodeResult[]
  barriers: TraceFeatureRef[]
  overloaded: TraceFeatureRef[]
}

/**
 * Same "not persisted, several siblings read one store" shape as
 * load-flow-store.ts -- the trigger (feature-panel.tsx), the map, and the
 * combined prompt/result panel (nodal-load-flow-panel.tsx) are siblings reading
 * this store rather than prop-drilling a result down. Kept as its own store
 * (not folded into load-flow-store) because the nodal solver is a genuinely
 * distinct engine with its own extra telemetry, but it is mutually exclusive
 * with every other analysis in the UI -- running one clears the others, same as
 * the radial analyses already clear each other.
 */
interface NodalLoadFlowState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  result: NodalLoadFlowResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: NodalLoadFlowResult) => void
  clear: () => void
}

export const useNodalLoadFlowStore = create<NodalLoadFlowState>()(
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
    resultPersist("voltedge-nodal-load-flow-result"),
  ),
)
