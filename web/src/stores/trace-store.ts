import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"

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
  /** True when the reached feature has real map geometry; false when it lives
   * only inside a single-line diagram (a geometry-less conductor stub, an
   * off-map device). Mirrors api/internal/trace's tracedFeature.on_map -- the
   * trace still reached it topologically, it's just not drawn on the canvas.
   * Always present (bool, not omitempty). */
  on_map?: boolean
  /** Every single-line diagram this reached feature is placed on (id + name),
   * mirrors tracedFeature.diagrams. Absent when the feature is on no diagram;
   * name may be empty for an unnamed diagram. trace-result-table.tsx reads
   * on_map + diagrams to render the Placement column (Map / Diagram: <name>). */
  diagrams?: { instance_id: string; name?: string }[]
  /** The reached feature's live switch position -- the coded device_state value
   * ("OPEN" / "CLOSED") that switching devices (MV_SWITCH, MV_BREAKER,
   * MV_ISOLATOR, a bus coupler, ...) carry and everything else omits. Mirrors
   * api/internal/trace's tracedFeature.device_state -- the same value a
   * downstream trace checks live as a barrier (OPEN halts the walk), surfaced so
   * trace-result-table.tsx can badge every switch the trace reached (a CLOSED one
   * it passed through as well as an OPEN one it stopped at). Absent for any
   * feature whose class has no device_state field. */
  device_state?: string
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
  /** The subset of `features` where a downstream walk stopped at a transformer it
   * was not told to cross (the feeder/LV boundary) -- mirrors
   * api/internal/trace's traceResponse.transformer_boundaries. Present only on a
   * downstream trace that stopped at one or more transformers (omitted when the
   * trace crossed them, or reached none); trace-result-table.tsx reads it to hint
   * "tick 'Continue past transformers' to include their LV network". */
  transformer_boundaries?: TraceFeatureRef[]
  /** Echoes the value this downstream walk ran with (api/internal/trace's
   * traceResponse.cross_transformers) -- only meaningful for a DOWNSTREAM trace.
   * trace-result-table.tsx reads it to render the "Continue past transformers"
   * toggle in the right checked state, so one click re-runs the same trace with
   * the transformer boundary crossed (or restored). */
  cross_transformers?: boolean
}

/**
 * Session-persisted (sessionStorage, via resultPersist) -- a completed trace
 * now survives a full page reload and route navigation but clears when the tab
 * closes, so results don't vanish on refresh (they used to: this was ephemeral
 * like draw-store.ts/connect-store.ts/edit-store.ts). Only the data persists;
 * `loading` and the non-serialisable `rerun` thunk are dropped (see
 * result-persist.ts), so after a reload the Re-run button hides until the next
 * run re-stashes its thunk. voltedge-map.tsx reads `result` to fetch and
 * highlight every reached feature (and, distinctly, every barrier) on the map
 * -- its highlight effect re-runs on mount, so it re-draws from the rehydrated
 * result; trace-result-banner.tsx reads it to show a summary and the Clear
 * action that resets this store -- the trigger (feature-panel.tsx), the map,
 * and the banner are siblings reading the same store, same cross-component
 * pattern this app already uses throughout rather than prop-drilling a result
 * down through components that don't otherwise share a parent.
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
  /** Re-runs the exact trace that produced the current result. Set by whichever
   * trigger ran it (feature-panel.tsx) alongside the result, since only the
   * trigger knows which endpoint/shape a given trace used -- the standard
   * POST /trace and the feeder/section/service/supply-path variants all
   * normalize into this same store, so a generic "re-fetch" here couldn't know
   * how. Null when no result, or when a result was set without a re-runner
   * (e.g. an old caller). The trace panel's Re-run button calls it. */
  rerun: (() => void) | null
  setLoading: (loading: boolean) => void
  setResult: (
    result: TraceResult,
    computedVoltages?: Record<string, number> | null,
    rerun?: (() => void) | null,
  ) => void
  clear: () => void
}

export const useTraceStore = create<TraceState>()(
  persist(
    (set) => ({
      result: null,
      computedVoltages: null,
      loading: false,
      rerun: null,
      setLoading: (loading) => set({ loading }),
      setResult: (result, computedVoltages = null, rerun = null) =>
        set({ result, computedVoltages, rerun, loading: false }),
      clear: () => set({ result: null, computedVoltages: null, rerun: null, loading: false }),
    }),
    resultPersist("voltedge-trace-result"),
  ),
)
