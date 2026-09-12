import { useQuery } from "@tanstack/react-query"

import { type FeatureRecord } from "@/hooks/use-feature"
import { apiFetch } from "@/lib/api"

interface FeatureRowsResponse {
  features: FeatureRecord[]
}

/**
 * Every (non-deleted) row of one feature class -- the same generic,
 * metadata-driven read path as useFeature, but the list endpoint
 * (GET /features/{code}) rather than the single-row get. This is what the
 * feature-classes browser (routes/features.tsx) pages a class's rows from,
 * and the ONLY way to see a geometry 'none' class's instances (relays,
 * bays, panels, the P&C devices) -- they never appear on the map.
 *
 * Only fetched once a class is actually selected in the browser (enabled
 * gate), never for every class in the sidebar.
 */
export function useFeatureRows(featureClassCode: string | undefined) {
  return useQuery({
    queryKey: ["feature-rows", featureClassCode],
    queryFn: () => apiFetch<FeatureRowsResponse>(`/features/${featureClassCode}`),
    enabled: Boolean(featureClassCode),
    staleTime: 30_000,
  })
}
