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
  result: CoordinationResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  setFault: (fault: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: CoordinationResult) => void
  clear: () => void
}

export const useCoordinationStore = create<CoordinationState>()(
  persist(
    (set) => ({
      armed: false,
      armedStart: null,
      armedFault: null,
      result: null,
      loading: false,
      arm: (start) => set({ armed: true, armedStart: start, armedFault: null, result: null, loading: false }),
      setFault: (fault) => set({ armedFault: fault }),
      rearm: () => set({ armed: true, result: null, loading: false }),
      setLoading: (loading) => set({ loading }),
      setResult: (result) => set({ result, loading: false }),
      clear: () => set({ armed: false, armedStart: null, armedFault: null, result: null, loading: false }),
    }),
    resultPersist("voltedge-coordination-result"),
  ),
)
