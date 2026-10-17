import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/** One row of the self-pruning data-quality queue (ops.validation_issues). The
 * worker keeps it current; this is what a triager works, rather than re-running a
 * whole-database audit. */
export interface ValidationIssue {
  id: string
  kind: string
  severity: "error" | "warning"
  status: "open" | "acknowledged" | "resolved" | "suppressed"
  message: string
  from_class?: string
  from_id?: string
  to_class?: string
  to_id?: string
  connection_id?: string
  distance_meters?: number
  first_seen_at: string
  last_seen_at: string
}

export type IssueStatusFilter = "" | "open" | "acknowledged" | "resolved" | "suppressed"
export type IssueSeverityFilter = "" | "error" | "warning"

export interface IssueQueueFilter {
  status?: IssueStatusFilter
  severity?: IssueSeverityFilter
  kind?: string
}

/** The validation-issue queue. An empty status returns the LIVE queue (open +
 * acknowledged), errors first. Needs connectivity:read. */
export function useValidationIssues(filter: IssueQueueFilter) {
  const params = new URLSearchParams()
  if (filter.status) params.set("status", filter.status)
  if (filter.severity) params.set("severity", filter.severity)
  if (filter.kind) params.set("kind", filter.kind)
  const qs = params.toString()
  return useQuery({
    queryKey: ["validation-issues", filter],
    queryFn: () => apiFetch<{ issues: ValidationIssue[] }>(`/connectivity/issues${qs ? `?${qs}` : ""}`),
  })
}

/** Triage one issue -- acknowledge / resolve / suppress / re-open. Needs
 * connectivity:commit. Invalidates the queue so the list refreshes. */
export function useTriageIssue() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, status }: { id: string; status: ValidationIssue["status"] }) =>
      apiFetch(`/connectivity/issues/${id}`, { method: "PATCH", body: JSON.stringify({ status }) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["validation-issues"] }),
  })
}

/** Run the audit and reconcile it into the queue -- a manual kick (the worker does
 * this continuously). An empty body reconciles the whole database; pass feature_ids
 * to scope it. Needs connectivity:commit. */
export function useReconcileIssues() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body?: { feature_ids?: string[]; tolerance_m?: number }) =>
      apiFetch<{ result: { upserted: number; auto_resolved: number } }>("/connectivity/audit/reconcile", {
        method: "POST",
        body: JSON.stringify(body ?? {}),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["validation-issues"] }),
  })
}
