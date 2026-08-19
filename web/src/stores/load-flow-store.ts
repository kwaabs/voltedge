import { create } from "zustand"

import type { TraceFeatureRef } from "@/stores/trace-store"

/** One node's resolved electrical state -- mirrors api/internal/powerflow's
 * nodeResult exactly. VoltageV/VoltageDropPct are always present; the
 * conductor-only fields (CurrentAmps/AmpacityAmps/LoadingPct) are only
 * set for a network_role = 'edge' node, omitted entirely for a
 * junction/container node that carries no impedance of its own. */
export interface LoadFlowNodeResult {
  feature_class: string
  feature_id: string
  voltage_v: number
  voltage_drop_pct: number
  cumulative_kw: number
  cumulative_kvar: number
  current_amps?: number
  ampacity_amps?: number
  loading_pct?: number
  material_resolved: boolean
}

/** Mirrors api/internal/powerflow's loadFlowResponse -- see
 * docs/phase-8-load-flow.md. `barriers` is the subset of node
 * references the walk actually stopped at because of a live, open
 * device -- those nodes never appear in `nodes` at all, same
 * "stopped here, not just ran out of network" distinction
 * trace-store.ts's own `barriers` already draws. `overloaded` is the
 * subset of `nodes` whose `loading_pct` exceeds 100. */
export interface LoadFlowResult {
  start: TraceFeatureRef
  nominal_voltage_v: number
  iterations: number
  nodes: LoadFlowNodeResult[]
  barriers: TraceFeatureRef[]
  overloaded: TraceFeatureRef[]
}

/**
 * Same "not persisted, several siblings read one store" shape as
 * trace-store.ts/isolate-store.ts -- the trigger (feature-panel.tsx),
 * the map (voltedge-map.tsx), and the combined prompt/results panel
 * (load-flow-panel.tsx) are three siblings reading this store rather
 * than prop-drilling a result down through components that don't
 * otherwise share a parent. Kept as its own store rather than folded
 * into trace-store.ts: a load-flow result is a genuinely different
 * shape (per-node electrical state, not a flat feature list), and the
 * workflow itself needs a real (P,Q)-unlike-trace input before it can
 * run at all -- so unlike a trace's fire-and-wait, this store has an
 * `armed` state (panel open, prompting for nominal_voltage_v) distinct
 * from `result` (panel showing a completed sweep). All three of trace/
 * isolate/load-flow are mutually exclusive in the UI, not simultaneous
 * -- running one clears the other two, same as trace and isolate
 * already clear each other.
 */
interface LoadFlowState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  result: LoadFlowResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: LoadFlowResult) => void
  clear: () => void
}

export const useLoadFlowStore = create<LoadFlowState>((set) => ({
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
