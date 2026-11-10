import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"
import type { TraceFeatureRef } from "@/stores/trace-store"

// Load allocation (ADMS): distribute a MEASURED feeder-head load across the
// downstream consumer leaves by a weight basis, so the per-transformer loads
// reflect reality rather than the model's as-designed demand_kw. Backend:
// api/internal/powerflow/allocation.go (POST /analysis/load-allocation). Same
// armed/result store shape as load-flow-store.ts (the study needs a measured
// value before it can run, so it arms the panel first, then the panel's Run
// makes the POST).

/** One consumer leaf's allocated load -- mirrors powerflow.leafAllocation. */
export interface LeafAllocation {
  feature_class: string
  feature_id: string
  label?: string
  customers: number
  weight: number
  as_designed_kw: number
  allocated_kw: number
  allocated_kvar: number
}

/** Mirrors powerflow.loadAllocationResult. `scale_factor` is measured ÷ Σ
 * as-designed demand_kw -- the model-vs-measured calibration. `basis_fallback`
 * is set when the requested `kva` basis had no data and fell back to customers. */
export interface LoadAllocationResult {
  start: TraceFeatureRef
  basis: string
  basis_fallback?: string
  measured_kw: number
  measured_kvar: number
  as_designed_kw: number
  scale_factor: number
  leaf_count: number
  customers_total: number
  allocations: LeafAllocation[]
  note?: string
}

interface LoadAllocationState {
  armed: boolean
  armedStart: TraceFeatureRef | null
  result: LoadAllocationResult | null
  loading: boolean
  arm: (start: TraceFeatureRef) => void
  rearm: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: LoadAllocationResult) => void
  clear: () => void
}

export const useLoadAllocationStore = create<LoadAllocationState>()(
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
    resultPersist("voltedge-load-allocation-result"),
  ),
)
