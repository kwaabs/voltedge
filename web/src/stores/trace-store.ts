import { create } from "zustand"

/** Mirrors api/internal/trace's featureRef exactly (feature_class/
 * feature_id, not camelCased) -- same convention every other API
 * response shape in this app already follows (e.g. use-connections.ts's
 * endpoint type), so the fetched JSON can be used as-is. */
export interface TraceFeatureRef {
  feature_class: string
  feature_id: string
}

/** Mirrors api/internal/trace's traceResponse -- see docs/phase-4-trace-engine.md.
 * `barriers` is always a subset of `features`: the nodes a trace
 * actually reached but a live trace_barrier_rules condition stopped it
 * from expanding past, distinct from a node that simply has no further
 * edges. `targets`/`targets_found` are only present when the named
 * trace_configurations row has a target_class set (migrations/0018) --
 * e.g. every transformer a "trace to transformer" run reached. */
export interface TraceResult {
  trace_configuration: string
  direction: string
  start: TraceFeatureRef
  features: TraceFeatureRef[]
  barriers: TraceFeatureRef[]
  path_found?: boolean
  targets?: TraceFeatureRef[]
  targets_found?: boolean
}

/**
 * Not persisted -- a trace result has no meaning across a reload, same
 * as every other in-progress/ephemeral workflow store in this app
 * (draw-store.ts, connect-store.ts, edit-store.ts). voltedge-map.tsx
 * reads `result` to fetch and highlight every reached feature (and,
 * distinctly, every barrier) on the map; trace-result-banner.tsx reads
 * it to show a summary and the Clear action that resets this store --
 * the trigger (feature-panel.tsx), the map, and the banner are three
 * siblings reading the same store, same cross-component pattern this
 * app already uses throughout rather than prop-drilling a result down
 * through components that don't otherwise share a parent.
 */
interface TraceState {
  result: TraceResult | null
  loading: boolean
  setLoading: (loading: boolean) => void
  setResult: (result: TraceResult) => void
  clear: () => void
}

export const useTraceStore = create<TraceState>((set) => ({
  result: null,
  loading: false,
  setLoading: (loading) => set({ loading }),
  setResult: (result) => set({ result, loading: false }),
  clear: () => set({ result: null, loading: false }),
}))
