import { create } from "zustand"

import type { TraceFeatureRef } from "@/stores/trace-store"

/** One phase (A/B/C) of an unbalanced-load-flow node -- mirrors
 * api/internal/powerflow's phaseResult. Only the phases actually energized at
 * the node are present (a single-phase lateral reports one). current_amps /
 * ampacity_amps / loading_pct are populated only on a conductor
 * (network_role = 'edge') node, exactly as in the balanced result. */
export interface UnbalancedPhaseResult {
  phase: string
  voltage_v: number
  voltage_drop_pct: number
  cumulative_kw: number
  cumulative_kvar: number
  current_amps?: number
  ampacity_amps?: number
  loading_pct?: number
}

/** One node's three-phase state -- mirrors api/internal/powerflow's
 * unbalancedNodeResult. phases holds only the energized phases;
 * neutral_current_amps and voltage_unbalance_pct are populated only when all
 * three phases are present at the node. nominal_base_v is the voltage base the
 * phase voltages sit on (rebased below a transformer). */
export interface UnbalancedNodeResult {
  feature_class: string
  feature_id: string
  circuit_id?: string
  circuit_ref?: string
  nominal_base_v: number
  phases: UnbalancedPhaseResult[]
  neutral_current_amps?: number
  voltage_unbalance_pct?: number
  material_resolved: boolean
  /** True only on a coupled (Carson) run, and only for a conductor whose spec
   * carried enough line geometry to build a 3x3 -- i.e. this branch's drop
   * genuinely included the phase-to-phase mutual terms. */
  mutual_modeled: boolean
}

/** Mirrors api/internal/powerflow's unbalancedResponse. unbalanced is the subset
 * of nodes whose voltage unbalance exceeds the planning limit -- the per-phase
 * analog of the balanced result's overloaded list. coupling_model is which solver
 * ran: "decoupled" (Tier 1) or "carson" (Tier 2, coupled). */
export interface UnbalancedLoadFlowResult {
  start: TraceFeatureRef
  nominal_voltage_v: number
  iterations: number
  coupling_model: string
  nodes: UnbalancedNodeResult[]
  barriers: TraceFeatureRef[]
  unbalanced: TraceFeatureRef[]
}

/**
 * Same "not persisted, siblings read one store" arm/result shape as the other
 * analysis stores (load-flow, ev-hosting, meshed). The trigger
 * (feature-panel.tsx armUnbalancedLoadFlow) arms with the feeder's source; the
 * combined prompt/result panel (unbalanced-load-flow-panel.tsx) reads this
 * store. Mutually exclusive with the other analysis workflows, so it clears and
 * is cleared by them exactly as they clear each other.
 */
interface UnbalancedLoadFlowState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  result: UnbalancedLoadFlowResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: UnbalancedLoadFlowResult) => void
  clear: () => void
}

export const useUnbalancedLoadFlowStore = create<UnbalancedLoadFlowState>((set) => ({
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
