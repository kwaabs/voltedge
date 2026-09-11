import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/** The verbs the backend writes to gis.feature_history.action (Phase 41,
 * api/internal/crud/history.go). One per distinct write the CRUD engine
 * performs, so the UI can label a publish differently from an edit without
 * diffing snapshots. Kept as a string union for the label/colour lookup, but
 * an unrecognized value still renders (falls back to the raw verb) rather than
 * being dropped. */
export type FeatureHistoryAction =
  | "create"
  | "update"
  | "publish"
  | "unpublish"
  | "delete"
  | "restore"
  | "purge"

/** One row of a feature's change timeline. actor_* are null for a system/CLI
 * write or a since-deleted user (the backend LEFT JOINs iam.users). snapshot is
 * the full post-change feature state (pre-delete state for a purge) -- the same
 * envelope a GET returns, passed straight through as the stored jsonb, so it's
 * left `unknown` here rather than re-modeled. */
export interface FeatureHistoryEntry {
  id: number
  action: FeatureHistoryAction | string
  actor_id: string | null
  actor_name: string | null
  actor_email: string | null
  at: string
  snapshot: unknown
}

interface FeatureHistoryResponse {
  history: FeatureHistoryEntry[]
}

/**
 * A feature's append-only change timeline, newest first (Phase 41). Backed by
 * GET /features/{code}/{id}/history, which never 404s on a purged feature --
 * the history outlives the row -- so an empty list is a valid answer. Kept off
 * the ["feature", ...] cache key so browsing the History tab never disturbs the
 * single-feature read the rest of the panel shares; it refetches on its own key.
 */
export function useFeatureHistory(featureClassCode: string | undefined, featureId: string | undefined) {
  return useQuery({
    queryKey: ["feature-history", featureClassCode, featureId],
    queryFn: () =>
      apiFetch<FeatureHistoryResponse>(`/features/${featureClassCode}/${featureId}/history`),
    enabled: Boolean(featureClassCode && featureId),
  })
}
