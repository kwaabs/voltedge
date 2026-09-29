import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"
import type { TraceFeatureRef } from "@/stores/trace-store"

/** Mirrors api/internal/powerflow's ampacityResponse -- see
 * docs/phase-8-ampacity.md. A single-conductor result, not a per-node
 * table like load-flow/fault-current: ampacity is a standalone thermal-
 * equilibrium calc that never touches the connectivity graph. */
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
 * (ampacity-panel.tsx) both read this store. No map-highlight effect of
 * its own reads it: ampacity never reaches beyond the one conductor
 * already selected, so there's nothing new for voltedge-map.tsx to draw.
 * Ampacity joins trace/isolate/load-flow/fault-current as a fifth
 * mutually-exclusive UI workflow -- arming or running one clears the
 * other four. */
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

export const useAmpacityStore = create<AmpacityState>()(
  persist(
    (set) => ({
      armed: false,
      armedConductor: null,
      result: null,
      loading: false,
      arm: (conductor) => set({ armed: true, armedConductor: conductor, result: null, loading: false }),
      rearm: () => set({ armed: true, result: null, loading: false }),
      setLoading: (loading) => set({ loading }),
      setResult: (result) => set({ result, loading: false }),
      clear: () => set({ armed: false, armedConductor: null, result: null, loading: false }),
    }),
    resultPersist("voltedge-ampacity-result"),
  ),
)
