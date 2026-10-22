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
  /** The WAM work order raised to fix this issue (assignment), or absent if
   * unassigned. Set once assigned; the issue still resolves via the audit
   * reconcile, not the order's completion. */
  work_order_id?: string
  /** Display-only, present once assigned: the work order's reference and a human
   * assignee label (display name / email / free-text crew), so the UI can read
   * "Assigned to Dana · WO-1234" without a second fetch. */
  work_order_ref?: string
  assignee_label?: string
}

export type IssueStatusFilter = "" | "open" | "acknowledged" | "resolved" | "suppressed"
export type IssueSeverityFilter = "" | "error" | "warning"

export interface IssueQueueFilter {
  status?: IssueStatusFilter
  severity?: IssueSeverityFilter
  kind?: string
  /** Scope the queue to issues touching these features (My edits passes the
   * features a user changed). Absent = the whole queue. */
  featureIds?: string[]
}

/** The validation-issue queue. An empty status returns the LIVE queue (open +
 * acknowledged), errors first. Needs connectivity:read. `enabled` lets a caller
 * defer the fetch until it's wanted (e.g. a My-edits "check" button). */
export function useValidationIssues(filter: IssueQueueFilter, enabled = true) {
  const params = new URLSearchParams()
  if (filter.status) params.set("status", filter.status)
  if (filter.severity) params.set("severity", filter.severity)
  if (filter.kind) params.set("kind", filter.kind)
  if (filter.featureIds && filter.featureIds.length > 0) params.set("feature_ids", filter.featureIds.join(","))
  const qs = params.toString()
  return useQuery({
    queryKey: ["validation-issues", filter],
    queryFn: () => apiFetch<{ issues: ValidationIssue[] }>(`/connectivity/issues${qs ? `?${qs}` : ""}`),
    enabled,
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

/** Assign a data-quality issue as a fix: raise a WAM work order for it and link
 * them (POST /validation-issues/{id}/assign). Pass an internal user id (office or
 * field) and/or a free-text crew. Needs wam:write. Invalidates the queue so the
 * row flips to "Assigned". */
export function useAssignIssue() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, assignedToUserId, assignedTo, priority }: { id: string; assignedToUserId?: string; assignedTo?: string; priority?: string }) =>
      apiFetch(`/validation-issues/${id}/assign`, {
        method: "POST",
        body: JSON.stringify({ assigned_to_user_id: assignedToUserId, assigned_to: assignedTo, priority }),
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["validation-issues"] }),
  })
}

/** Assign a BATCH of issues to one user in a single request (atomic, server-side)
 * -- the "Assign N to…" bulk action. Already-assigned / missing ids are skipped,
 * not failed. Needs wam:write. Invalidates the queue. */
export function useAssignIssuesBulk() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ issueIds, assignedToUserId, assignedTo, priority }: { issueIds: string[]; assignedToUserId?: string; assignedTo?: string; priority?: string }) =>
      apiFetch<{ assigned: number; skipped: number }>("/validation-issues/assign-bulk", {
        method: "POST",
        body: JSON.stringify({ issue_ids: issueIds, assigned_to_user_id: assignedToUserId, assigned_to: assignedTo, priority }),
      }),
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
