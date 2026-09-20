import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiFetch, type ApiError } from "@/lib/api"

// Phase 61 private-drafts rollout switch. A companion to the Phase 58 territory
// enforcement toggle: when on, a feature that has never been published is visible
// only to its author and the author's management chain (and still only within the
// viewer's territory scope). Off by default -- turning it on retroactively
// privatizes every never-published feature, so it is a deliberate admin action.
// Backed by GET/PUT /private-drafts/enforcement (features:read / metadata:write).

/** Current state of the private_drafts_enabled flag. */
export function usePrivateDraftsEnforcement() {
  return useQuery({
    queryKey: ["private-drafts-enforcement"],
    queryFn: () => apiFetch<{ enforcement: boolean }>("/private-drafts/enforcement"),
    staleTime: 30_000,
  })
}

/** Flips private_drafts_enabled (PUT /private-drafts/enforcement), then refreshes
 * the query so the toggle reflects the new value. */
export function useSetPrivateDraftsEnforcement() {
  const queryClient = useQueryClient()
  return useMutation<{ enforcement: boolean }, ApiError, boolean>({
    mutationFn: (enabled) =>
      apiFetch<{ enforcement: boolean }>("/private-drafts/enforcement", {
        method: "PUT",
        body: JSON.stringify({ enabled }),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["private-drafts-enforcement"] })
    },
  })
}
