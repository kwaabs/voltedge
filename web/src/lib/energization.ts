import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/** A feature reference in the shape the energization endpoint takes/returns. */
export interface EnergizationRef {
  feature_class: string
  feature_id: string
}

/** A stable key for a feature in the de-energised set. */
export function refKey(featureClass: string, featureId: string): string {
  return `${featureClass}|${featureId}`
}

/**
 * Classifies a set of features live vs de-energised via POST /trace/energization.
 * Returns a Set of `class|id` keys for the DE-ENERGISED features (below an open
 * device); anything not in the set is live. The server walks the whole network
 * (open devices are usually off-screen), so this can't be computed client-side.
 *
 * `cacheKey` scopes the query cache (the caller passes something that changes
 * when the feature set does -- a viewport bbox key, or a diagram id); `refs` is
 * the set to classify. keepPreviousData holds the last shading through a refetch.
 */
export function useEnergization(cacheKey: string | null, refs: EnergizationRef[], enabled: boolean) {
  return useQuery({
    queryKey: ["energization", cacheKey],
    queryFn: async () => {
      const res = await apiFetch<{ de_energized: EnergizationRef[] }>("/trace/energization", {
        method: "POST",
        body: JSON.stringify({ feature_refs: refs }),
      })
      return new Set(res.de_energized.map((r) => refKey(r.feature_class, r.feature_id)))
    },
    enabled: enabled && cacheKey != null && refs.length > 0,
    staleTime: 15_000,
    placeholderData: (prev) => prev,
  })
}
