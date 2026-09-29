import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"
import type { ServiceRollup, TraceFeatureRef } from "@/stores/trace-store"

/** Mirrors api/internal/trace's isolationResponse -- see
 * docs/phase-4-utility-traces.md. `de_energized` is everything that
 * would lose power if `device` were opened right now; `upstream_boundary`
 * is the nearest protective device upstream of it, absent when none
 * exists in that direction (fed all the way back to an unmodeled
 * source). `critical_facilities` is the subset of `de_energized`
 * flagged critical; `possible_backfeed_paths` is every live connection
 * out of the de-energized set this isolation didn't already account
 * for -- a possibility to verify, not a claimed guarantee either way. */
export interface IsolationResult {
  device: TraceFeatureRef
  de_energized: TraceFeatureRef[]
  upstream_boundary?: TraceFeatureRef
  critical_facilities?: TraceFeatureRef[]
  possible_backfeed_paths?: TraceFeatureRef[]
  /** Phase 62 consumption rollup of the de-energized SERVICE_POINTs -- the load
   * (customers + kW/kVA, with the critical subset and per-tier split) that this
   * isolation would drop. Always present; every tier zero if it reached no load. */
  service?: ServiceRollup
}

/**
 * Same "session-persisted (resultPersist), several siblings read one store"
 * shape as trace-store.ts -- kept as its own store rather than folded into it
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

export const useIsolateStore = create<IsolateState>()(
  persist(
    (set) => ({
      result: null,
      loading: false,
      setLoading: (loading) => set({ loading }),
      setResult: (result) => set({ result, loading: false }),
      clear: () => set({ result: null, loading: false }),
    }),
    resultPersist("voltedge-isolate-result"),
  ),
)
