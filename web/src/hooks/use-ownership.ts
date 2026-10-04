import { useMutation, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"
import { useMapRefreshStore } from "@/stores/map-refresh-store"

/**
 * Asset-ownership propagation (api/internal/ownership): declare a transformer
 * private and stamp ownership=private onto everything downstream, plus a global
 * re-sync. Both bulk-write features, so the endpoints are gated features:write.
 * On success we invalidate the touched feature queries AND bump the map refresh
 * so the `private` symbology (muted/dashed) appears without a manual reload.
 */

export interface OwnershipClassCount {
  feature_class: string
  stamped: number
}

export interface OwnershipPropagateResult {
  source: { feature_class: string; feature_id: string }
  reached: number
  stamped: number
  per_class: OwnershipClassCount[]
}

export interface OwnershipResyncResult {
  boundaries: number
  cleared: number
  stamped: number
  per_class: OwnershipClassCount[]
}

export function useMarkDownstreamPrivate() {
  const queryClient = useQueryClient()
  return useMutation<OwnershipPropagateResult, ApiError, { featureClass: string; featureId: string }>({
    mutationFn: ({ featureClass, featureId }) =>
      apiFetch<OwnershipPropagateResult>("/ownership/mark-private", {
        method: "POST",
        body: JSON.stringify({ feature_class: featureClass, feature_id: featureId }),
      }),
    onSuccess: (_data, vars) => {
      queryClient.invalidateQueries({ queryKey: ["feature", vars.featureClass, vars.featureId] })
      queryClient.invalidateQueries({ queryKey: ["features"] })
      useMapRefreshStore.getState().bump()
    },
  })
}

export function useResyncOwnership() {
  const queryClient = useQueryClient()
  return useMutation<OwnershipResyncResult, ApiError, void>({
    mutationFn: () => apiFetch<OwnershipResyncResult>("/ownership/resync", { method: "POST" }),
    onSuccess: () => {
      // Ownership can change across many features -- invalidate the feature
      // queries broadly (prefix match) and refresh the tiles.
      queryClient.invalidateQueries({ queryKey: ["feature"] })
      queryClient.invalidateQueries({ queryKey: ["features"] })
      useMapRefreshStore.getState().bump()
    },
  })
}
