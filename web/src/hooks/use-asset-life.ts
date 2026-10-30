import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

// Client for the asset-lifecycle aging engine (api/internal/assetlife): the
// expected-useful-life catalog and the remaining-life report. The report/catalog
// read is features:read; editing the catalog is metadata:write.

export interface LifeExpectancy {
  id: string
  feature_class_code: string
  subtype_code: number | null
  expected_years: number
  notes: string | null
  created_at: string
  updated_at: string
}

export function useLifeExpectancies(featureClass: string | undefined) {
  return useQuery({
    queryKey: ["asset-life-catalog", featureClass ?? ""],
    queryFn: () =>
      apiFetch<{ expectancies: LifeExpectancy[] }>(
        `/asset-life/catalog${featureClass ? `?feature_class=${encodeURIComponent(featureClass)}` : ""}`,
      ),
    enabled: !!featureClass,
    staleTime: 60_000,
  })
}

export interface LifeExpectancyBody {
  feature_class_code?: string
  subtype_code?: number | null
  expected_years: number
  notes?: string | null
}

function useCatalogMutation<TBody>(fn: (body: TBody) => Promise<unknown>) {
  const queryClient = useQueryClient()
  return useMutation<unknown, ApiError, TBody>({
    mutationFn: fn,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["asset-life-catalog"] })
      queryClient.invalidateQueries({ queryKey: ["asset-life-report"] })
    },
  })
}

export function useCreateLifeExpectancy() {
  return useCatalogMutation<LifeExpectancyBody>((body) =>
    apiFetch("/asset-life/catalog", { method: "POST", body: JSON.stringify(body) }),
  )
}

export function useUpdateLifeExpectancy() {
  return useCatalogMutation<{ id: string; body: LifeExpectancyBody }>(({ id, body }) =>
    apiFetch(`/asset-life/catalog/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
  )
}

export function useDeleteLifeExpectancy() {
  return useCatalogMutation<string>((id) => apiFetch(`/asset-life/catalog/${id}`, { method: "DELETE" }))
}

export interface AgingItem {
  feature_class: string
  feature_id: string
  name?: string
  subtype?: number
  lifecycle_status: string
  in_service_since: string
  age_years: number
  expected_years: number
  remaining_years: number
  bucket: "overdue" | "due_soon" | "ok"
}

export interface AgingClassSummary {
  feature_class: string
  total: number
  overdue: number
  due_soon: number
  ok: number
}

export interface AgingReport {
  generated_at: string
  due_within_years: number
  total: number
  overdue: number
  due_soon: number
  ok: number
  by_class: AgingClassSummary[]
  items: AgingItem[]
  truncated: boolean
}

export function useAgingReport(featureClass?: string, dueWithinYears?: number) {
  const params = new URLSearchParams()
  if (featureClass) params.set("feature_class", featureClass)
  if (dueWithinYears && dueWithinYears > 0) params.set("due_within_years", String(dueWithinYears))
  const qs = params.toString()
  return useQuery({
    queryKey: ["asset-life-report", featureClass ?? "", dueWithinYears ?? 0],
    queryFn: () => apiFetch<AgingReport>(`/asset-life/report${qs ? `?${qs}` : ""}`),
    staleTime: 30_000,
  })
}

/** One lifecycle transition in the ledger (api/internal/assetlife LifecycleEvent),
 * with the actor's display name resolved. from_status is null for the first row. */
export interface LifecycleEvent {
  id: string
  feature_class_code: string
  feature_id: string
  from_status: string | null
  to_status: string
  actor_id: string | null
  actor_name: string | null
  occurred_at: string
}

/** The network-wide lifecycle-transition ledger ("what got retired / commissioned
 * this quarter"), newest first. Filter by the resulting status and/or feature
 * class; the server caps the result. */
export function useLifecycleEvents(opts: { toStatus?: string; featureClass?: string } = {}) {
  const params = new URLSearchParams()
  if (opts.toStatus) params.set("to_status", opts.toStatus)
  if (opts.featureClass) params.set("feature_class", opts.featureClass)
  const qs = params.toString()
  return useQuery({
    queryKey: ["asset-life-events", opts.toStatus ?? "", opts.featureClass ?? ""],
    queryFn: () => apiFetch<{ events: LifecycleEvent[] }>(`/asset-life/events${qs ? `?${qs}` : ""}`),
    staleTime: 30_000,
  })
}
