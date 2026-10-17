import { create } from "zustand"

import type { FeatureRef } from "@/stores/selection-store"

/**
 * In-progress "Trace path to…" gesture -- the two-feature pick that drives a
 * SHORTEST_PATH trace (api/internal/trace, the one trace_configurations direction
 * that takes a target feature). Not persisted: a half-finished pick has no meaning
 * across a reload. `source` is locked when the action is chosen from the trace
 * dropdown (feature-panel.tsx, the feature selected at that moment); `target` is set
 * once the user clicks a second feature on the map (voltedge-map.tsx's click handler,
 * with peer-picker.tsx disambiguating a stacked click). When both are set,
 * feature-panel.tsx's effect fires the POST /trace {SHORTEST_PATH, start, target} and
 * cancels the gesture -- same source/target-in-a-store shape connect-store.ts uses,
 * minus the confirm step (a read-only trace needs no confirmation).
 */
interface TracePathState {
  active: boolean
  source: FeatureRef | null
  target: FeatureRef | null
  begin: (source: FeatureRef) => void
  pickTarget: (target: FeatureRef) => void
  cancel: () => void
}

export const useTracePathStore = create<TracePathState>((set) => ({
  active: false,
  source: null,
  target: null,
  begin: (source) => set({ active: true, source, target: null }),
  pickTarget: (target) => set({ target }),
  cancel: () => set({ active: false, source: null, target: null }),
}))
