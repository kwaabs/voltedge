import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"
import type { TraceFeatureRef } from "@/stores/trace-store"

/** One interconnection node's DER hosting result -- mirrors
 * api/internal/powerflow's hostingCandidate. hosting_capacity_kw is the
 * largest generator injection at this node that introduces no over-voltage
 * and no thermal overload. binding_constraint names the limit that stops
 * more ("overvoltage" | "thermal" | "pre_existing" | "none");
 * binding_feature is the node/branch it acts on. worst_voltage_pu /
 * worst_loading_pct are the feeder's worst values AT that capacity (how
 * close each limit sits). unbounded is true when the search cap was reached
 * without a limit binding. note explains a 0 (pre-existing violation) or an
 * unbounded result. */
export interface HostingCandidate {
  node: TraceFeatureRef
  hosting_capacity_kw: number
  binding_constraint: string
  binding_feature?: TraceFeatureRef
  worst_voltage_pu: number
  worst_loading_pct: number
  unbounded?: boolean
  note?: string
}

/** Mirrors api/internal/powerflow's hostingResponse -- the feeder slack /
 * band / injection power factor the study assumed, and one sized candidate
 * per interconnection node. */
export interface HostingResult {
  source: TraceFeatureRef
  nominal_voltage_v: number
  min_voltage_pu: number
  max_voltage_pu: number
  power_factor: number
  candidates: HostingCandidate[]
}

/**
 * Same "not persisted, siblings read one store" arm/result shape as the
 * other analysis stores (load-flow, voltage-profile, contingency). The
 * trigger (feature-panel.tsx) arms with the feeder's source; the combined
 * prompt/result panel (hosting-panel.tsx) reads this store. Mutually
 * exclusive with the other analysis workflows, so it clears and is cleared
 * by them exactly as they clear each other.
 */
interface HostingState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  result: HostingResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: HostingResult) => void
  clear: () => void
}

export const useHostingStore = create<HostingState>()(
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
    resultPersist("voltedge-hosting-result"),
  ),
)
