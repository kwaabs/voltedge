import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/**
 * Wire types for the field-sync review queue (api/internal/fieldpack/review.go),
 * behind sync:review (view) / sync:post (act). When a feature class is set to
 * 'review' mode, its device ops land 'pending_review' and the drain worker skips
 * them until a supervisor posts them here. Mirrored per this app's convention of
 * each hook holding its own copy of the API shape. See docs/phase-23-sync-inbox.md.
 */
export type ReviewStatus = "pending_review" | "rejected" | "applied" | "conflict"

export type SyncOpKind =
  | "create"
  | "update_attrs"
  | "delete"
  | "establish_connection"
  | "establish_relationship"

export interface ReviewOp {
  op_id: string
  recv_seq: number
  submitted_by: string
  owner_email: string
  owner_name: string
  device_id?: string
  submission_id?: string
  kind: SyncOpKind
  feature_class?: string
  feature_id?: string
  payload?: unknown
  base_version?: string
  status: string
  error?: string
  reviewed_by?: string
  reviewer_name?: string
  reviewed_at?: string
  received_at: string
}

interface ReviewResponse {
  ops: ReviewOp[]
}

/**
 * A conflict op plus the CURRENT production row it targets (GET /sync-review/{id}),
 * so the UI can diff the device's proposed change against the live server value.
 * current is null for a create/establish or a target that no longer exists.
 */
export interface ConflictDetail {
  op: ReviewOp
  current: Record<string, unknown> | null
}

const REVIEW_KEY = ["admin", "sync-review"] as const

/**
 * The ops in a given review status (default pending_review), oldest first -- the
 * order a reviewer works and the order the worker applies posted ones. It self-polls
 * every 4s while the pending queue is non-empty (in auto/QA-mixed setups the worker
 * applies a posted op moments later, so its status flips without a manual refresh)
 * and stops once nothing is pending.
 */
export function useSyncReview(status: ReviewStatus) {
  return useQuery<ReviewResponse, ApiError>({
    queryKey: [...REVIEW_KEY, status],
    queryFn: () => apiFetch<ReviewResponse>(`/sync-review?status=${encodeURIComponent(status)}`),
    retry: false,
    refetchInterval: (query) =>
      query.state.data?.ops.some((o) => o.status === "pending_review") ? 4000 : false,
  })
}

/**
 * Post/reject invalidate the whole review key so every status tab re-reads (a posted
 * op leaves pending_review and a rejected one joins rejected). Prefix invalidation
 * covers the per-status query keys under REVIEW_KEY.
 */
function useReviewMutation<TVars>(fn: (vars: TVars) => Promise<unknown>) {
  const queryClient = useQueryClient()
  return useMutation<unknown, ApiError, TVars>({
    mutationFn: fn,
    onSuccess: () => queryClient.invalidateQueries({ queryKey: REVIEW_KEY }),
  })
}

/** Post a reviewed op -> flips it to 'received' so the worker applies it. */
export function usePostOp() {
  return useReviewMutation<{ opId: string }>(({ opId }) =>
    apiFetch(`/sync-review/${opId}/post`, { method: "POST" }),
  )
}

/** Reject a reviewed op -> quarantined with a reason (surfaces on the device). */
export function useRejectOp() {
  return useReviewMutation<{ opId: string; reason?: string }>(({ opId, reason }) =>
    apiFetch(`/sync-review/${opId}/reject`, {
      method: "POST",
      body: JSON.stringify({ reason: reason ?? "" }),
    }),
  )
}

/**
 * One conflict op + the current production row, for the diff view (Phase 23 Slice 4).
 * Fetched lazily -- only enabled when a conflict card is expanded -- so the queue list
 * stays one cheap query and the per-op production read happens on demand.
 */
export function useConflictDetail(opId: string, enabled: boolean) {
  return useQuery<ConflictDetail, ApiError>({
    queryKey: [...REVIEW_KEY, "detail", opId],
    queryFn: () => apiFetch<ConflictDetail>(`/sync-review/${encodeURIComponent(opId)}`),
    enabled,
    retry: false,
  })
}

/**
 * Resolve a 'conflict' op (Phase 23 Slice 4). action "apply" force-applies the device
 * edit (the worker re-runs it skipping the version check); "keep" discards it and keeps
 * the production value (the op is rejected, surfacing on the device as needs-attention).
 */
export function useResolveOp() {
  return useReviewMutation<{ opId: string; action: "apply" | "keep"; reason?: string }>(
    ({ opId, action, reason }) =>
      apiFetch(`/sync-review/${opId}/resolve`, {
        method: "POST",
        body: JSON.stringify({ action, reason: reason ?? "" }),
      }),
  )
}

/**
 * The per-class + global apply policy (api/internal/fieldpack/policy.go). default_mode
 * is the effective global default (the built-in 'auto' if no '*' row has been set);
 * policies are the configured rows, feature_class "*" being the global one.
 */
export interface SyncPolicyRow {
  feature_class: string
  mode: "auto" | "review"
  detect_conflicts: boolean
  updated_by?: string
  updated_at: string
}

interface PolicyResponse {
  default_mode: "auto" | "review"
  default_detect_conflicts: boolean
  policies: SyncPolicyRow[]
}

const POLICY_KEY = ["admin", "sync-policy"] as const

export function useSyncPolicies() {
  return useQuery<PolicyResponse, ApiError>({
    queryKey: POLICY_KEY,
    queryFn: () => apiFetch<PolicyResponse>("/sync-review/policy"),
    retry: false,
  })
}

/**
 * Set a class's (empty feature_class = global "*") apply mode + conflict-detection flag.
 * mode and detect_conflicts are independent: a class can auto-apply AND be conflict-checked.
 * Both are sent every time (the server upserts the whole row), so callers pass the current
 * value of the dimension they aren't changing.
 */
export function useSetPolicy() {
  const queryClient = useQueryClient()
  return useMutation<
    unknown,
    ApiError,
    { feature_class: string; mode: "auto" | "review"; detect_conflicts: boolean }
  >({
    mutationFn: (body) =>
      apiFetch("/sync-review/policy", { method: "PUT", body: JSON.stringify(body) }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: POLICY_KEY }),
  })
}

/** Clear a class override (or the global "*"), reverting to the inherited mode. */
export function useDeletePolicy() {
  const queryClient = useQueryClient()
  return useMutation<unknown, ApiError, { feature_class: string }>({
    mutationFn: ({ feature_class }) =>
      apiFetch(`/sync-review/policy?feature_class=${encodeURIComponent(feature_class)}`, {
        method: "DELETE",
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: POLICY_KEY }),
  })
}
