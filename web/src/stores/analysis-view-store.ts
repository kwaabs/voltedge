import { create } from "zustand"
import { persist } from "zustand/middleware"

export type AnalysisResultView = "table" | "tree"

/**
 * How the load-flow family panels (basic / meshed / auto) render their solved
 * nodes: a flat "table" or the "tree" the sweep actually produced -- the radial
 * spanning tree rooted at the start feature, built client-side from each node's
 * parent_feature_* pointers (see load-flow-nodes-table.tsx). A durable display
 * preference (localStorage, like units/layers/theme and the trace panel's own
 * toggle), not a per-run one: which shape a user prefers to read a load flow in
 * outlives any single run. Shared by all three panels because they are mutually
 * exclusive in the UI (running one clears the others), so there's one live
 * result at a time and one preference reads naturally across them. Kept separate
 * from the trace panel's voltedge-trace-view so a user can prefer a tree for
 * traces but a table for load flows (different columns, different reading habit).
 *
 * Table is the default -- it works for every engine, including the nodal (Y-bus)
 * result, which is a mesh with no single-parent tree and so renders flat even in
 * tree mode.
 */
interface AnalysisViewState {
  view: AnalysisResultView
  setView: (view: AnalysisResultView) => void
}

export const useAnalysisViewStore = create<AnalysisViewState>()(
  persist(
    (set) => ({
      view: "table",
      setView: (view) => set({ view }),
    }),
    { name: "voltedge-analysis-view" },
  ),
)
