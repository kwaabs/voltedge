import { create } from "zustand"
import { persist } from "zustand/middleware"

export type TraceResultView = "table" | "tree"

/**
 * How the trace result panel renders its reached features: a flat "table" or a
 * "tree" (the BFS spanning tree rooted at the start feature, built client-side
 * from each feature's parent_feature_* pointers -- see trace-result-table.tsx).
 * A durable display preference (localStorage, like units/layers/theme), not a
 * per-session one: which shape a user prefers to read a trace in outlives any
 * single trace or tab. Table is the default -- it works for every trace kind,
 * including the feeder/section/service/supply-path ones that don't carry parent
 * pointers yet, where the tree falls back to flat.
 */
interface TraceViewState {
  view: TraceResultView
  setView: (view: TraceResultView) => void
}

export const useTraceViewStore = create<TraceViewState>()(
  persist(
    (set) => ({
      view: "table",
      setView: (view) => set({ view }),
    }),
    { name: "voltedge-trace-view" },
  ),
)
