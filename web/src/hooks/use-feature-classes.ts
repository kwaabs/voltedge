import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

export interface FeatureClassSummary {
  code: string
  name: string
  geometry_type: string
  network_role: string
  /** Migration 0129: the class's network-sectioning function --
   * source/protective/switching/transformer, or absent for a non-boundary class.
   * The result panels derive each feature's "section" by climbing its trace /
   * load-flow parent tree to the nearest class with a boundary_role. */
  boundary_role?: string
  is_diagram_container: boolean
  is_diagram_placeable: boolean
  /** Phase 26: features of this class may exist without map geometry, so
   * the class is creatable from inside a diagram (diagram-only feature)
   * even though it has a real geom column. */
  geometry_optional: boolean
  /** Migration 0053: overrides how this class is drawn on a DIAGRAM
   * ("line" makes a point junction like a busbar render as a spanning
   * bar). Absent/undefined = inherit geometry_type (the map shape). */
  diagram_geometry_type?: "point" | "line" | null
  /** Migration 0074: admin-defined grouping for the "New feature" menu
   * (e.g. "MV plant", "LV service"). Absent/null = uncategorised, bucketed
   * under an "Other" section. */
  category?: string | null
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
