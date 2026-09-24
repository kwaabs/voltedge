import { useMutation, useQueryClient } from "@tanstack/react-query"

import { type FeatureRecord } from "@/hooks/use-feature"
import { apiFetch, type ApiError } from "@/lib/api"
import { useMapRefreshStore } from "@/stores/map-refresh-store"

// Private-drafts Phase B client actions: apply or discard the acting user's
// private, unpublished edit/delete of an already-published feature.
//
// A pending edit is staged server-side when a scoped user edits or deletes a
// published feature while the private_drafts rollout is on (see the backend
// crud/pending.go). The author's own reads show the overlaid values with a
// pending_private_edit marker (use-feature.ts); these two mutations are how the
// author resolves that pending state:
//
//   - Apply (POST /features/{code}/{id}/apply-pending) publishes the staged
//     change onto the public row -- it becomes visible to everyone. Blocked with
//     a 409 if someone else changed the base row since it was staged (the caller
//     surfaces the server's "discard and redo" message).
//   - Discard (DELETE /features/{code}/{id}/pending) throws the staged change
//     away, leaving the public row untouched.
//
// Both refresh the same caches an edit does: the single-feature read (so the
// panel drops the overlay/marker), the class row list (so the browser reflects
// it), and the map tiles (a staged geometry move / delete overlay is per-author,
// so applying or discarding changes what the author's own tiles render).

interface PendingEditVars {
  featureClassCode: string
  featureId: string
}

function refreshAfterPending(
  queryClient: ReturnType<typeof useQueryClient>,
  { featureClassCode, featureId }: PendingEditVars,
) {
  queryClient.invalidateQueries({ queryKey: ["feature", featureClassCode, featureId] })
  queryClient.invalidateQueries({ queryKey: ["feature-rows", featureClassCode] })
  // The author's own tiles carry the private geom-move / delete overlay, so a
  // fresh tile is needed after either action -- same cache-busting bump the edit
  // form uses (feature-edit-form.tsx).
  useMapRefreshStore.getState().bump()
}

/** Applies (publishes) the acting user's staged private edit onto the public
 * feature. Returns the now-public feature record; a 409 ApiError means the base
 * row moved since staging (surface err.message and let the user discard/redo). */
export function useApplyPendingEdit() {
  const queryClient = useQueryClient()
  return useMutation<FeatureRecord, ApiError, PendingEditVars>({
    mutationFn: ({ featureClassCode, featureId }) =>
      apiFetch<FeatureRecord>(`/features/${featureClassCode}/${featureId}/apply-pending`, {
        method: "POST",
      }),
    onSuccess: (_data, vars) => refreshAfterPending(queryClient, vars),
  })
}

/** Discards the acting user's staged private edit, leaving the public feature
 * untouched. Resolves to void (the endpoint returns 204). */
export function useDiscardPendingEdit() {
  const queryClient = useQueryClient()
  return useMutation<void, ApiError, PendingEditVars>({
    mutationFn: ({ featureClassCode, featureId }) =>
      apiFetch<void>(`/features/${featureClassCode}/${featureId}/pending`, {
        method: "DELETE",
      }),
    onSuccess: (_data, vars) => refreshAfterPending(queryClient, vars),
  })
}
