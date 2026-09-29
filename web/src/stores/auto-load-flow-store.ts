import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"
import type { LoadFlowResult } from "@/stores/load-flow-store"
import type { MeshedLoadFlowResult } from "@/stores/meshed-load-flow-store"
import type { NodalLoadFlowResult } from "@/stores/nodal-load-flow-store"
import type { TraceFeatureRef } from "@/stores/trace-store"

/** The engine the dispatch router (POST /analysis/load-flow-auto) actually ran:
 * "radial" = the fast forward-backward ladder, "meshed" = Tier 1 weak-mesh
 * compensation, "nodal" = the Tier 2 Y-bus (Gauss-Seidel) solver. */
export type LoadFlowEngine = "radial" | "meshed" | "nodal"

/** Mirrors api/internal/powerflow's dispatchLoadFlowResponse (Phase 52, S4 --
 * see docs/phase-52-nodal-powerflow.md). The router classifies the network once
 * (buildTree's radial/mesh verdict) and auto-selects the engine, then wraps that
 * engine's own native response: exactly one of `radial` / `meshed` / `nodal` is
 * populated, and `engine` names which. `engine_reason` is a one-line
 * human-readable justification; `loop_count` is the independent loop count the
 * classifier found (0 for a tree). A client reads `engine` to know which
 * sub-result -- and which existing result view -- to render. */
export interface AutoLoadFlowResult {
  engine: LoadFlowEngine
  engine_reason: string
  loop_count: number
  radial?: LoadFlowResult
  meshed?: MeshedLoadFlowResult
  nodal?: NodalLoadFlowResult
}

/** The active engine's inner result -- whichever of radial/meshed/nodal the
 * router populated. All three share the {nodes, overloaded, barriers} shape the
 * map highlight and the result table read, so callers that only need those can
 * treat the return uniformly. Returns null before a run completes. */
export function activeInner(
  result: AutoLoadFlowResult | null,
): LoadFlowResult | MeshedLoadFlowResult | NodalLoadFlowResult | null {
  if (!result) return null
  return result.radial ?? result.meshed ?? result.nodal ?? null
}

/**
 * Same "not persisted, several siblings read one store" shape as
 * load-flow-store.ts / nodal-load-flow-store.ts -- the trigger
 * (feature-panel.tsx), the map (voltedge-map.tsx) and the combined
 * prompt/result panel (auto-load-flow-panel.tsx) are siblings reading this store
 * rather than prop-drilling a result down. Its own store (not folded into any
 * one engine's) because this is the ENGINE-AGNOSTIC entry: it POSTs to the
 * dispatch router and renders whichever engine came back. Mutually exclusive
 * with every other analysis in the UI -- arming one clears the others, same as
 * every sibling analysis already does.
 */
interface AutoLoadFlowState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  result: AutoLoadFlowResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: AutoLoadFlowResult) => void
  clear: () => void
}

export const useAutoLoadFlowStore = create<AutoLoadFlowState>()(
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
    resultPersist("voltedge-auto-load-flow-result"),
  ),
)
