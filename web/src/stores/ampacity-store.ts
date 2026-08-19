import { create } from "zustand"

import type { TraceFeatureRef } from "@/stores/trace-store"

/** Mirrors api/internal/powerflow's ampacityResponse exactly -- see
 * docs/phase-8-ampacity.md. Unlike load-flow/fault-current this is a
 * single-feature result, not a per-node list: ampacity is a standalone
 * calc on one conductor, it never walks the connectivity graph. */
export interface AmpacityResult {
  conductor: TraceFeatureRef
  material_spec_code: string
  max_operating_temp_c: number
  ambient_temp_c: number
  convective_loss_w_per_m: number
  radiative_loss_w_per_m: number
  solar_gain_w_per_m: number
  resistance_ohm_per_m: number
  ampacity_amps: number
  static_ampacity_amps?: number
}

/**
 * Same "not persisted, several siblings read one store" shape as
 * load-flow-store.ts/fault-current-store.ts -- the trigger
 * (feature-panel.tsx) and the combined prompt/results panel
 * (ampacity-panel.tsx) are the only two siblings here (unlike the
 * other two engines, there's no map highlight to wire up: ampacity
 * never reaches beyond the one conductor already selected, so there's
 * nothing new for voltedge-map.tsx to draw that the plain "selected"
 * highlight doesn't already cover). `armed` (panel open, prompting for
 * environmental conditions) is distinct from `result` (showing a
 * completed calc), same reasoning the other two engines' stores give.
 */
interface AmpacityState {
  armed: boolean
  armedConductor: TraceFeatureRef | null
  result: AmpacityResult | null
  loading: boolean
  arm: (conductor: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: AmpacityResult) => void
  clear: () => void
}

export const useAmpacityStore = create<AmpacityState>((set) => ({
  armed: false,
  armedConductor: null,
  result: null,
  loading: false,
  arm: (conductor) => set({ armed: true, armedConductor: conductor, result: null, loading: false }),
  rearm: () => set({ armed: true, result: null, loading: false }),
  setLoading: (loading) => set({ loading }),
  setResult: (result) => set({ result, loading: false }),
  clear: () => set({ armed: false, armedConductor: null, result: null, loading: false }),
}))
