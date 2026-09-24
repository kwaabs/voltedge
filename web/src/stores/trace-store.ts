import { create } from "zustand"

/** Mirrors api/internal/trace's featureRef exactly (feature_class/
 * feature_id, not camelCased) -- same convention every other API
 * response shape in this app already follows (e.g. use-connections.ts's
 * endpoint type), so the fetched JSON can be used as-is. */
export interface TraceFeatureRef {
  feature_class: string
  feature_id: string
}

/** Mirrors api/internal/trace's tracedFeature -- a reached feature (the
 * `features` list of every trace-family response) enriched with its stored
 * circuit membership (Phase 37 circuit_id + human circuit_ref) and nominal
 * operating voltage (a conductor's voltage_class / a transformer's primary
 * side, parsed to volts). All three are optional: a class carrying neither a
 * circuit_id field nor any voltage-class field, or one with no value set,
 * omits them. nominal_voltage_v is the topology-intrinsic voltage the trace
 * engine knows without a load-flow solve -- a *computed* per-node voltage is a
 * separate load-flow artifact the table overlays from load-flow-store.ts. */
export interface TracedFeature extends TraceFeatureRef {
  circuit_id?: string
  circuit_ref?: string
  nominal_voltage_v?: number
  /** The reached feature's one-hop-upstream neighbour in the trace's BFS tree --
   * the feature the walk arrived here from (mirrors api/internal/trace's
   * tracedFeature.parent_feature_*). Absent on the start feature and on any node
   * whose parent fell outside the caller's territory scope. voltedge-map.tsx uses
   * it to orient the directional marching-ants animation: a line's upstream end is
   * whichever endpoint sits nearer this parent's geometry, so the ants crawl from
   * there outward (flipped for an upstream trace). */
  parent_feature_class?: string
  parent_feature_id?: string
}

/** Mirrors api/internal/trace's ServiceTotals -- an additive bag of consumption
 * sums (Phase 62). demand_kva is the sum of each SP's own apparent power
 * (kW / power_factor), not a re-derive off an averaged pf. Used for the whole
 * set, the critical subset, and per tier. */
export interface ServiceTotals {
  service_points: number
  customers: number
  demand_kw: number
  demand_kva: number
}

/** One restoration tier's totals plus its identity (P1..P4). */
export interface ServiceTier extends ServiceTotals {
  priority: string
  label: string
}

/** Mirrors api/internal/trace's ServiceRollup -- the consumption summary that
 * rides along on every trace-family response (and OMS energization/affected):
 * the whole set's totals (inlined), the critical subset (restoration rank <= P2),
 * and the per-tier breakdown (always all four, zeroed if unhit). */
export interface ServiceRollup extends ServiceTotals {
  critical: ServiceTotals
  by_priority: ServiceTier[]
}

/** Mirrors api/internal/trace's ServicePoint -- one SERVICE_POINT's own line in
 * the dedicated service trace's per-point breakdown. */
export interface ServicePoint extends TraceFeatureRef {
  priority: string
  label: string
  customers: number
  demand_kw: number
  demand_kva: number
  critical: boolean
}

/** Mirrors api/internal/trace's serviceResponse -- POST /trace/service. */
export interface ServiceTraceResult {
  start: TraceFeatureRef
  service: ServiceRollup
  service_points: ServicePoint[]
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
  features: TracedFeature[]
  barriers: TraceFeatureRef[]
  path_found?: boolean
  targets?: TraceFeatureRef[]
  targets_found?: boolean
  /** Phase 62 consumption rollup of the reached SERVICE_POINTs -- always present
   * (every tier zero on a trace that reached no load). See ServiceRollup. */
  service?: ServiceRollup
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
  /** Computed per-node voltages overlaid onto the result table, keyed
   * `feature_class:feature_id`, snapshotted from load-flow-store.ts at trace
   * time when a load-flow result for the same start exists (see the trigger in
   * feature-panel.tsx). Null when no matching load-flow result was available --
   * the trace engine itself is decoupled from powerflow and never computes
   * these, so this client-side overlay is the only place computed voltages
   * meet a trace. Cleared alongside the result. */
  computedVoltages: Record<string, number> | null
  loading: boolean
  setLoading: (loading: boolean) => void
  setResult: (result: TraceResult, computedVoltages?: Record<string, number> | null) => void
  clear: () => void
}

export const useTraceStore = create<TraceState>((set) => ({
  result: null,
  computedVoltages: null,
  loading: false,
  setLoading: (loading) => set({ loading }),
  setResult: (result, computedVoltages = null) => set({ result, computedVoltages, loading: false }),
  clear: () => set({ result: null, computedVoltages: null, loading: false }),
}))
