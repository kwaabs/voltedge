import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/**
 * One export job as GET /export/jobs returns it (api/internal/export/export.go
 * jobView). A job moves queued -> running -> succeeded|failed; the result
 * fields (feature_count, byte_size) and download_url appear only once it has
 * succeeded, and error only on failure. download_url points at this API's own
 * /download route (owner-checked, stable across polls), which 302-redirects to
 * a freshly minted RustFS presigned URL at click time.
 */
export interface ExportJobView {
  id: string
  status: "queued" | "running" | "succeeded" | "failed" | "cancelled"
  format: string
  feature_count?: number
  byte_size?: number
  error?: string
  download_url?: string
  created_at: string
  finished_at?: string
}

interface ExportJobsResponse {
  jobs: ExportJobView[]
}

/** A job the server is still working on -- worth polling for. */
export function isJobPending(status: ExportJobView["status"]): boolean {
  return status === "queued" || status === "running"
}

/**
 * The caller's most recent export jobs, newest first (server caps at 50). This
 * is the whole panel's data source: an export is now an async job, so once the
 * dialog enqueues one the UI just watches this list. It self-polls -- every 2s
 * while any job is still queued or running, and stops entirely once every job
 * has reached a terminal state -- so a worker finishing an export surfaces its
 * download link without a manual refresh, and an idle panel makes no requests.
 */
export function useExportJobs(enabled: boolean) {
  return useQuery({
    queryKey: ["export-jobs"],
    queryFn: () => apiFetch<ExportJobsResponse>("/export/jobs"),
    enabled,
    refetchInterval: (query) => {
      const jobs = query.state.data?.jobs
      return jobs?.some((j) => isJobPending(j.status)) ? 2000 : false
    },
  })
}

/**
 * Enqueues an export (POST /export -> 202 {job_id}). On success it invalidates
 * the jobs list so the freshly queued job shows up immediately, before the
 * poll interval would otherwise pick it up.
 */
export interface EnqueueExportBody {
  format: string
  feature_classes: string[]
  bbox?: number[]
  territories?: string[]
  /**
   * Target CRS (a PostGIS spatial_ref_sys SRID) to reproject geometry into on
   * the way out. Omitted keeps the stored EPSG:4326. The server accepts this
   * only for the reprojectable formats (shp/dxf); it 400s a target on kmz/cim.
   */
  target_srid?: number
}

export function useEnqueueExport() {
  const queryClient = useQueryClient()
  return useMutation<{ job_id: string }, ApiError, EnqueueExportBody>({
    mutationFn: (body) =>
      apiFetch<{ job_id: string }>("/export", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["export-jobs"] })
    },
  })
}

/**
 * Cancels one of the caller's own pending jobs (POST /export/jobs/{id}/cancel,
 * taking the job id). A queued job is cancelled outright; a running one is
 * flipped to cancelled and its worker aborts on the next heartbeat. Returns the
 * job's resulting state and invalidates the list so the panel reflects it at
 * once. Errors (404 gone / 409 already finished) surface to the caller, which
 * toasts them.
 */
export function useCancelExport() {
  const queryClient = useQueryClient()
  return useMutation<ExportJobView, ApiError, string>({
    mutationFn: (id) =>
      apiFetch<ExportJobView>(`/export/jobs/${id}/cancel`, { method: "POST" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["export-jobs"] })
    },
  })
}
