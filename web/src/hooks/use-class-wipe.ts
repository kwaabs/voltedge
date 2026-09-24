import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

// Client mirror of the bulk class-wipe API (api/internal/crud, mounted under
// /features). The "nuke" behind the Features browser is two-step and
// soft-then-purge, exactly like the single-feature delete/purge: `wipe`
// soft-deletes every live, in-scope row (reversible per-feature via restore),
// and `purge-deleted` permanently removes every already-soft-deleted, in-scope
// row with its topology cascade (irreversible). Both are gated features:wipe on
// the server (admin / break-glass only); the UI additionally self-gates on the
// same permission. Everything respects the acting user's territory scope, so a
// scoped admin only ever touches their own territory's rows.

/** How many rows each step would touch, scoped to the acting user. `live` is the
 * soft-delete target count; `deleted` is the purge target count. Mirrors
 * crud.ClassCounts. */
export interface ClassCounts {
  live: number
  deleted: number
}

const CLASS_WIPE_KEY = ["class-wipe"] as const

/** The live + soft-deleted counts for a class, fueling the confirm dialog.
 * Only enabled once a class is chosen and the caller has opened the danger
 * dialog (an admin action), never fetched for every class in the browser. */
export function useClassCounts(featureClassCode: string | undefined, enabled: boolean) {
  return useQuery<ClassCounts, ApiError>({
    queryKey: [...CLASS_WIPE_KEY, "counts", featureClassCode ?? ""],
    queryFn: () => apiFetch<ClassCounts>(`/features/${featureClassCode}/counts`),
    enabled: Boolean(featureClassCode) && enabled,
    staleTime: 0,
  })
}

/** Invalidate everything a wipe/purge changes: the class's row list, its counts,
 * and the asset-tag cache (a purge cascades runtime.asset_tags away via trigger).
 * Shared by both mutations below. */
function useInvalidateAfterWipe() {
  const queryClient = useQueryClient()
  return (featureClassCode: string) => {
    queryClient.invalidateQueries({ queryKey: ["feature-rows", featureClassCode] })
    queryClient.invalidateQueries({ queryKey: [...CLASS_WIPE_KEY, "counts", featureClassCode] })
    queryClient.invalidateQueries({ queryKey: ["asset-tags"] })
  }
}

/** Soft-delete every live, in-scope feature in the class. Returns how many rows
 * were soft-deleted. Reversible per-feature via restore until purged. */
export function useWipeClass() {
  const invalidate = useInvalidateAfterWipe()
  return useMutation<{ soft_deleted: number }, ApiError, { featureClassCode: string }>({
    mutationFn: ({ featureClassCode }) =>
      apiFetch(`/features/${featureClassCode}/wipe`, { method: "POST" }),
    onSuccess: (_data, { featureClassCode }) => invalidate(featureClassCode),
  })
}

/** Permanently purge every soft-deleted, in-scope feature in the class, cleaning
 * its polymorphic references. Returns how many rows were purged. Irreversible. */
export function usePurgeDeletedClass() {
  const invalidate = useInvalidateAfterWipe()
  return useMutation<{ purged: number }, ApiError, { featureClassCode: string }>({
    mutationFn: ({ featureClassCode }) =>
      apiFetch(`/features/${featureClassCode}/purge-deleted`, { method: "POST" }),
    onSuccess: (_data, { featureClassCode }) => invalidate(featureClassCode),
  })
}
