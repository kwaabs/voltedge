import { create } from "zustand"
import { persist } from "zustand/middleware"

import { resultPersist } from "@/stores/result-persist"

// Mirrors api/internal/connectivity/audit.go's AuditReport. The connectivity
// consistency audit is read-only: it reports divergences between the logical
// connectivity graph and geometry/features, it never writes.

export interface ConnectivityAuditFinding {
  kind: string // "orphaned_connectivity" | "coincident_unconnected"
  severity: "error" | "warning" | "info"
  message: string
  connection_id?: string
  from_class?: string
  from_id?: string
  to_class?: string
  to_id?: string
  /** Set when an endpoint can't be located because it's gone: "deleted"
   * (soft-deleted) or "missing" (purged). Empty/absent = live and locatable. */
  from_status?: string
  to_status?: string
  distance_meters?: number
}

export interface ConnectivityAuditSummary {
  connections_total: number
  orphaned_connectivity: number
  coincident_unconnected: number
  relationships_total: number
  orphaned_relationships: number
  class_pairs_scanned: number
  tolerance_meters: number
  truncated: boolean
}

export interface ConnectivityAuditResult {
  summary: ConnectivityAuditSummary
  findings: ConnectivityAuditFinding[]
  limitations: string[]
}

interface ConnectivityAuditState {
  // open is a toolbar toggle (the audit is network-wide, not feature-scoped),
  // so the panel shows even before a run; result/loading hold the run state.
  open: boolean
  loading: boolean
  result: ConnectivityAuditResult | null
  openPanel: () => void
  closePanel: () => void
  toggle: () => void
  setLoading: (loading: boolean) => void
  setResult: (result: ConnectivityAuditResult) => void
  clear: () => void
}

export const useConnectivityAuditStore = create<ConnectivityAuditState>()(
  persist(
    (set) => ({
      open: false,
      loading: false,
      result: null,
      openPanel: () => set({ open: true }),
      // Closing clears the last run so reopening starts fresh rather than showing a
      // stale report (the result otherwise lingers in the store / its sessionStorage
      // mirror). A run is cheap to re-trigger; a stale one is misleading.
      closePanel: () => set({ open: false, result: null, loading: false }),
      toggle: () => set((s) => (s.open ? { open: false, result: null, loading: false } : { open: true })),
      setLoading: (loading) => set({ loading }),
      setResult: (result) => set({ result, loading: false }),
      clear: () => set({ result: null, loading: false }),
    }),
    // Persist the last result in sessionStorage so it survives a reload but
    // clears when the tab closes; loading and functions are never rehydrated.
    resultPersist("voltedge-connectivity-audit-result"),
  ),
)
