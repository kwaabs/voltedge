import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"
import type { TraceFeatureRef } from "@/stores/trace-store"

/** One plotted (current, time) point on the run's common voltage base --
 * mirrors api/internal/powerflow's curveSample. */
export interface CoordinationCurveSample {
  current_amps: number
  time_s: number
}

/** One labelled polyline of a device's time-current characteristic (a relay
 * "operate" curve, or a fuse's "minimum melt" / "total clear" bounds) --
 * mirrors curveTrace. Its points are already referred to common_base_v. */
export interface CoordinationCurveTrace {
  label: string
  points: CoordinationCurveSample[]
}

/** One time-graded protective device on the source->fault path -- mirrors
 * api/internal/powerflow's coordinationDevice. role is "primary" for the
 * device nearest the fault that operates and "backup" for those upstream (empty
 * when the device has no resolved operate time). through_current_amps and
 * nominal_base_v are on the device's OWN base; operate_time_s is how long it
 * takes to clear the requested fault (0 for an instantaneous trip, absent when
 * unresolved or the current is at/below pickup -- note says which). curves is
 * the device's full characteristic for plotting, referred to common_base_v so
 * every device shares one current axis. nominal_voltage_v (the device's OWN
 * coded voltage_class / primary_voltage, distinct from nominal_base_v) and
 * circuit_id / circuit_ref are the same display extras an enriched trace
 * surfaces; each is absent when the device's class carries no such field. */
export interface CoordinationDevice {
  feature_class: string
  feature_id: string
  role: string
  order: number
  through_current_amps: number
  nominal_base_v: number
  nominal_voltage_v?: number
  circuit_id?: string
  circuit_ref?: string
  curve?: string
  operate_time_s?: number
  resolved: boolean
  extrapolated?: boolean
  note?: string
  curves?: CoordinationCurveTrace[]
}

/** One adjacent (downstream primary, upstream backup) pair and its grading --
 * mirrors coordinationPair. margin_s is t_upstream - t_downstream; coordinated
 * is true when margin_s >= cti_seconds. Both are absent when either device's
 * operate time was unresolved (nothing to grade). */
export interface CoordinationPair {
  downstream: TraceFeatureRef
  upstream: TraceFeatureRef
  margin_s?: number
  cti_seconds: number
  coordinated?: boolean
}

/** Mirrors api/internal/powerflow's coordinationResponse. fault_current_amps is
 * the prospective 3-phase bolted-fault current at the fault node, on
 * common_base_v -- the base every device's curves and the fault line are
 * referred to, so the chart shares one current axis and the fault current is a
 * single vertical line every device's curve crosses at its own operate time.
 * coordinated is the AND of every gradeable pair (absent when none could be
 * graded -- fewer than two devices with resolved times). */
export interface CoordinationResult {
  start: TraceFeatureRef
  fault: TraceFeatureRef
  nominal_voltage_v: number
  common_base_v: number
  cti_seconds: number
  fault_current_amps?: number
  devices: CoordinationDevice[]
  pairs: CoordinationPair[]
  coordinated?: boolean
}

/** Mirrors api/internal/powerflow's coordinationSweepResponse -- the
 * feeder-wide version of a coordination study (POST /analysis/coordination-sweep).
 * Instead of grading one source->fault path it faults AT every series protective
 * device below the source and reports each adjacent pair's WORST margin, so one
 * run answers "is the whole feeder coordinated, and which pairs are not?". pairs
 * are the same shape a single study returns (downstream/upstream/margin_s/
 * coordinated), already ordered miscoordinated-first. devices_faulted is how many
 * protective devices were actually gradeable; truncated is set when the feeder has
 * more protective devices than the sweep cap (the report is then not exhaustive). */
export interface CoordinationSweepResult {
  start: TraceFeatureRef
  cti_seconds: number
  devices_faulted: number
  pair_count: number
  miscoordinated_count: number
  ungraded_count: number
  pairs: CoordinationPair[]
  truncated: boolean
}

/** One recloser -> downstream-fuse reclose-scheme evaluation -- mirrors
 * api/internal/powerflow's fuseSavingPair. The four time fields are the operate
 * times at fault_current_amps (each on its own device's base). fuse_saved is the
 * fuse-saving verdict (recloser fast curve trips before the fuse min-melt, so a
 * transient fault doesn't blow the fuse); fuse_clears is the permanent-fault
 * verdict (the fuse totally clears before the recloser's slow/delayed trip locks
 * out the feeder). Each is absent when a curve it needs was unresolved.
 * max_save_current_amps is the fuse-saving crossover (fast == min-melt) on the
 * fuse base -- the upper fault-current bound the fuse is saved over; absent when
 * the two curves never cross over their shared range. */
export interface FuseSavingPair {
  recloser: TraceFeatureRef
  fuse: TraceFeatureRef
  fault_current_amps?: number
  fast_time_s?: number
  slow_time_s?: number
  fuse_min_melt_s?: number
  fuse_total_clear_s?: number
  fuse_saved?: boolean
  fuse_clears?: boolean
  max_save_current_amps?: number
  note?: string
}

/** Mirrors api/internal/powerflow's fuseSavingResponse (POST
 * /analysis/fuse-saving) -- the recloser/fuse-saving scheme check over the whole
 * feeder below the source. For each lateral fuse and its nearest upstream
 * recloser it reports both scheme verdicts. fuse_saved_count / fuse_clears_count
 * are how many pairs pass each check; issue_count is how many fail EITHER (a
 * fuse that melts on a transient, or one that doesn't clear before lockout).
 * pairs are ordered issues-first. truncated is set when the feeder has more
 * recloser/fuse laterals than the sweep cap. */
export interface FuseSavingResult {
  start: TraceFeatureRef
  pair_count: number
  fuse_saved_count: number
  fuse_clears_count: number
  issue_count: number
  pairs: FuseSavingPair[]
  truncated: boolean
}

/**
 * Same "not persisted, several siblings read one store" shape as
 * fault-current-store.ts, with one extra input: coordination needs a *second*
 * node (the fault location) beyond the source, so the armed state carries both
 * armedStart and armedFault -- the source is armed from the selected feature
 * (feature-panel.tsx), the fault is set from a second map selection
 * (coordination-panel.tsx's "Set fault" step). Coordination joins
 * trace/isolate/load-flow/fault-current/ampacity as a mutually-exclusive UI
 * workflow -- arming or running one clears the others.
 */
interface CoordinationState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  armedFault: TraceFeatureRef | null
  // True while the panel's "Pick fault" button has armed the map to take the
  // NEXT feature click as the fault location (voltedge-map.tsx's click handler /
  // peer-picker.tsx route to setFault while this is set) -- the arm-then-click
  // gesture, same shape as trace-path-store.ts's `active`. NOT persisted.
  pickingFault: boolean
  result: CoordinationResult | null
  loading: boolean
  // The feeder-wide sweep shares the same armed source as the single study (it
  // needs no fault pick) and the same transient `loading` flag (the two never run
  // at once from one panel), so it rides this store rather than a sibling: only
  // its result is separate, so the panel can show either the single TCC study or
  // the sweep report without one clobbering the other's armed state.
  sweepResult: CoordinationSweepResult | null
  // The recloser/fuse-saving check rides this store for the same reason the sweep
  // does: same armed source, no fault pick, mutually exclusive with the other two
  // views. Only its result is separate.
  fuseSavingResult: FuseSavingResult | null
  arm: (start: TraceFeatureRef) => void
  startPickFault: () => void
  cancelPickFault: () => void
  setFault: (fault: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: CoordinationResult) => void
  setSweepResult: (result: CoordinationSweepResult) => void
  setFuseSavingResult: (result: FuseSavingResult) => void
  clear: () => void
}

export const useCoordinationStore = create<CoordinationState>()(
  persist(
    (set) => ({
      armed: false,
      armedStart: null,
      armedFault: null,
      pickingFault: false,
      result: null,
      loading: false,
      sweepResult: null,
      fuseSavingResult: null,
      arm: (start) =>
        set({ armed: true, armedStart: start, armedFault: null, pickingFault: false, result: null, loading: false, sweepResult: null, fuseSavingResult: null }),
      startPickFault: () => set({ pickingFault: true }),
      cancelPickFault: () => set({ pickingFault: false }),
      // Setting the fault (from a map click while picking, or the peer-picker)
      // also ends the pick gesture.
      setFault: (fault) => set({ armedFault: fault, pickingFault: false }),
      rearm: () => set({ armed: true, pickingFault: false, result: null, loading: false, sweepResult: null, fuseSavingResult: null }),
      setLoading: (loading) => set({ loading }),
      setResult: (result) => set({ result, loading: false }),
      setSweepResult: (sweepResult) => set({ sweepResult, loading: false }),
      setFuseSavingResult: (fuseSavingResult) => set({ fuseSavingResult, loading: false }),
      clear: () =>
        set({ armed: false, armedStart: null, armedFault: null, pickingFault: false, result: null, loading: false, sweepResult: null, fuseSavingResult: null }),
    }),
    resultPersist("voltedge-coordination-result"),
  ),
)
