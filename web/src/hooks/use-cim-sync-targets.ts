import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

// Named CIM sync targets (incremental difference-model export). A target defines a
// published-only scope + profile and tracks the baseline it last sent; "run"
// enqueues an ordinary CIM export job whose status/download reuse the export jobs
// panel. Backend: api/internal/export/synctarget_http.go (/export/sync-targets).

export interface CimSyncTarget {
  id: string
  name: string
  description?: string | null
  active: boolean
  feature_classes: string[]
  bbox?: number[] | null
  territory_ids?: string[] | null
  cim_profile?: string | null
  target_srid?: number | null
  /** Auto-run cadence: "hourly" | "daily" | "weekly"; null/absent = manual. */
  schedule?: string | null
  /** True once the target has sent an initial full model; later runs emit a diff. */
  has_baseline: boolean
  baseline_model_mrid?: string | null
  baseline_feature_count?: number | null
  last_exported_at?: string | null
  last_job_id?: string | null
}

export interface CimSyncTargetInput {
  name: string
  description?: string | null
  active?: boolean
  feature_classes: string[]
  bbox?: number[] | null
  territory_ids?: string[] | null
  cim_profile?: string | null
  target_srid?: number | null
  schedule?: string | null
}

export function useCimSyncTargets() {
  return useQuery({
    queryKey: ["cim-sync-targets"],
    queryFn: () => apiFetch<{ sync_targets: CimSyncTarget[] }>("/export/sync-targets"),
  })
}

export function useSaveCimSyncTarget() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, input }: { id?: string; input: CimSyncTargetInput }) =>
      apiFetch<CimSyncTarget>(id ? `/export/sync-targets/${id}` : "/export/sync-targets", {
        method: id ? "PATCH" : "POST",
        body: JSON.stringify(input),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["cim-sync-targets"] }),
  })
}

export function useDeleteCimSyncTarget() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiFetch(`/export/sync-targets/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["cim-sync-targets"] }),
  })
}

export function useRunCimSyncTarget() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<{ job_id: string }>(`/export/sync-targets/${id}/run`, { method: "POST" }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["cim-sync-targets"] })
      // Wake the shared export-jobs poller so it picks up the just-queued job and
      // starts its 2s poll; when that job goes terminal the sync-targets page
      // invalidates ["cim-sync-targets"] so the row's baseline/last-run refresh live.
      qc.invalidateQueries({ queryKey: ["export-jobs"] })
    },
  })
}
