import { create } from "zustand"

import type { TraceFeatureRef } from "@/stores/trace-store"

/** Mirrors api/internal/trace's isolationResponse -- see
 * docs/phase-4-utility-traces.md. `de_energized` is everything that
 * would lose power if `device` were opened right now; `upstream_boundary`
 * is the nearest protective device upstream of it, absent when none
 * exists in that direction (fed all the way back to an unmodeled
 * source). */
export interface IsolationResult {
  device: TraceFeatureRef
  de_energized: TraceFeatureRef[]
  upstream_boundary?: TraceFeatureRef
}

/**
 * Same "not persisted, three siblings read one store" shape as
 * trace-store.ts -- kept as its own store rather than folded into it
 * since an isolation result has a genuinely different shape (two named
 * lists, not one feature list + barriers) and the two workflows are
 * mutually exclusive in the UI (running one clears the other, see
 * feature-panel.tsx), not simultaneous.
 */
interface IsolateState {
  result: IsolationResult | null
  loading: boolean
  setLoading: (loading: boolean) => void
  setResult: (result: IsolationResult) => void
  clear: () => void
}

export const useIsolateStore = create<IsolateState>((set) => ({
  result: null,
  loading: false,
  setLoading: (loading) => set({ loading }),
  setResult: (result) => set({ result, loading: false }),
  clear: () => set({ result: null, loading: false }),
}))
