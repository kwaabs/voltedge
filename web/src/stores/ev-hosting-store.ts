import { create } from "zustand"

import type { TraceFeatureRef } from "@/stores/trace-store"

/** One connection node's EV feeder-headroom result -- mirrors
 * api/internal/powerflow's evHostingCandidate. ev_hosting_capacity_kw is the
 * largest ADDITIONAL charging load at this node that introduces no
 * under-voltage and no thermal overload. binding_constraint names the limit
 * that stops more ("undervoltage" | "thermal" | "pre_existing" | "none");
 * binding_feature is the node/branch it acts on. worst_voltage_pu /
 * worst_loading_pct are the feeder's worst values AT that capacity (how close
 * each limit sits). unbounded is true when the search cap was reached without a
 * limit binding. note explains a 0 (pre-existing violation) or an unbounded
 * result. */
export interface EvHostingCandidate {
  node: TraceFeatureRef
  ev_hosting_capacity_kw: number
  binding_constraint: string
  binding_feature?: TraceFeatureRef
  worst_voltage_pu: number
  worst_loading_pct: number
  unbounded?: boolean
  note?: string
}

/** Mirrors api/internal/powerflow's evHostingResponse -- the feeder slack /
 * voltage floor / added-load power factor the study assumed, and one sized
 * candidate per connection node. */
export interface EvHostingResult {
  source: TraceFeatureRef
  nominal_voltage_v: number
  min_voltage_pu: number
  power_factor: number
  candidates: EvHostingCandidate[]
}

/**
 * Same "not persisted, siblings read one store" arm/result shape as the other
 * analysis stores (load-flow, hosting, contingency). The trigger
 * (feature-panel.tsx) arms with the feeder's source; the combined prompt/result
 * panel (ev-hosting-panel.tsx) reads this store. Mutually exclusive with the
 * other analysis workflows, so it clears and is cleared by them exactly as they
 * clear each other.
 */
interface EvHostingState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  result: EvHostingResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: EvHostingResult) => void
  clear: () => void
}

export const useEvHostingStore = create<EvHostingState>((set) => ({
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
