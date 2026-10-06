import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"
import type { TraceFeatureRef } from "@/stores/trace-store"

/** One node's symmetrical (3-phase bolted) fault level -- mirrors
 * api/internal/powerflow's faultNodeResult exactly. resistance_ohm/
 * reactance_ohm are the accumulated series R and X from the source down
 * to this node, referred to nominal_base_v; impedance_ohm is their
 * magnitude. nominal_base_v is the voltage base this node sits on -- the
 * run's nominal above every transformer, a transformer's secondary below
 * it -- so an MV-side and an LV-side fault level are each measured on their
 * own base and the UI can tell them apart. fault_current_amps is absent
 * only when |Z| is zero (an ideal source with no conductor yet between it
 * and this node -- an unbounded, un-reportable figure). material_resolved
 * is false when any conductor branch on the path lacked resolved R/X, or a
 * transformer on the path contributed no series impedance (no nameplate
 * %Z/rating), in which case the reported current is an optimistic upper
 * bound (a missing series impedance can only lower the true fault
 * current). circuit_id / circuit_ref are the node's stored circuit
 * membership (Phase 37), absent when its class carries no circuit field;
 * the node's voltage base is nominal_base_v. */
export interface FaultCurrentNodeResult {
  feature_class: string
  feature_id: string
  circuit_id?: string
  circuit_ref?: string
  resistance_ohm: number
  reactance_ohm: number
  impedance_ohm: number
  nominal_base_v: number
  fault_current_amps?: number
  material_resolved: boolean
  /** Peak (crest) asymmetrical duty, from the path's X/R (IEC 60909): xr_ratio =
   * X/R, peak_asymmetry_kappa = κ = 1.02 + 0.98·e^(−3·R/X), peak_fault_current_amps
   * = i_p = κ·√2·I_sym -- the making/peak-withstand current, which the symmetrical
   * fault_current_amps alone understates. Absent when the path has no resistance
   * (X/R undefined) or no fault current. */
  xr_ratio?: number
  peak_asymmetry_kappa?: number
  peak_fault_current_amps?: number
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

export const useFaultCurrentStore = create<FaultCurrentState>()(
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
    resultPersist("voltedge-fault-current-result"),
  ),
)
