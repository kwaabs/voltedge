import { create } from "zustand"

import type { TraceFeatureRef } from "@/stores/trace-store"

/** One candidate location for the fault -- a conductor span the measured current
 * could place it on. Mirrors api/internal/powerflow's faultLocationCandidate.
 * `segment` is the faulted conductor, `upstream` the feature feeding it;
 * distances are metres; `fraction_along_segment` is 0 at the upstream end, 1 at
 * the downstream end; lon/lat is the interpolated point (absent when the geometry
 * didn't resolve); `resolved` is false when some impedance on the path was
 * missing, making the distance an optimistic estimate. */
export interface FaultLocationCandidate {
  segment: TraceFeatureRef
  upstream: TraceFeatureRef
  distance_from_source_m: number
  distance_along_segment_m: number
  segment_length_m: number
  fraction_along_segment: number
  longitude?: number
  latitude?: number
  resolved: boolean
}

/** Mirrors api/internal/powerflow's faultLocationResponse. `note` explains an
 * empty candidate set; `source_fault_current_amps` is the prospective current
 * right at the source (the ceiling a measurement can't exceed). */
export interface FaultLocationResult {
  start: TraceFeatureRef
  nominal_voltage_v: number
  fault_type: string
  measured_current_amps: number
  target_impedance_ohm: number
  candidates: FaultLocationCandidate[]
  source_fault_current_amps: number
  note?: string
}

/**
 * Fault Location (distance-to-fault) is an OPERATIONAL tool, not one of the
 * mutually-exclusive planning studies, so -- unlike fault-current-store.ts and
 * its siblings that share the bottom-centre slot -- it keeps its own
 * independent floating panel and does not clear (or get cleared by) the other
 * analysis workflows. `start` is the feeder source the panel was armed from
 * (the feature-panel's "Locate fault from here" action); the panel then collects
 * the measured current and runs. Not persisted: a fault location is a live,
 * after-the-trip query, not a saved study.
 */
interface FaultLocationState {
  open: boolean
  start: TraceFeatureRef | null
  result: FaultLocationResult | null
  loading: boolean
  // prefillCurrentAmps carries a measured fault current handed over from a logged
  // fault's "Locate" shortcut (its fault_current_a), so the panel pre-fills the
  // current instead of the operator re-typing it. Set before the panel is armed
  // from the feeder source; consumed by the panel; cleared on close.
  prefillCurrentAmps: number | null
  arm: (start: TraceFeatureRef) => void
  setLoading: (loading: boolean) => void
  setResult: (result: FaultLocationResult) => void
  setPrefillCurrent: (amps: number | null) => void
  close: () => void
}

export const useFaultLocationStore = create<FaultLocationState>((set) => ({
  open: false,
  start: null,
  result: null,
  loading: false,
  prefillCurrentAmps: null,
  arm: (start) => set({ open: true, start, result: null, loading: false }),
  setLoading: (loading) => set({ loading }),
  setResult: (result) => set({ result, loading: false }),
  setPrefillCurrent: (amps) => set({ prefillCurrentAmps: amps }),
  close: () => set({ open: false, start: null, result: null, loading: false, prefillCurrentAmps: null }),
}))
