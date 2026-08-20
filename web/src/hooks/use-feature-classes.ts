import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

export interface FeatureClassSummary {
  code: string
  name: string
  geometry_type: string
  network_role: string
  is_diagram_container: boolean
  is_diagram_placeable: boolean
}

interface FeatureClassesResponse {
  feature_classes: FeatureClassSummary[]
}

/**
 * Server state, so Query -- same reasoning as useHealth/useCatalog.
 * Martin's catalog only ever knows a bare table/tile-source id (e.g.
 * "mv_conductor"); this is what lets the UI show the real, admin-set
 * display name ("MV Conductor") instead. Same long staleTime as
 * useCatalog -- which feature classes exist and what they're named
 * changes rarely (an admin action), not something worth polling.
 */
export function useFeatureClasses() {
  return useQuery({
    queryKey: ["feature-classes"],
    queryFn: () => apiFetch<FeatureClassesResponse>("/feature-classes"),
    staleTime: 5 * 60_000,
    retry: 1,
  })
}
