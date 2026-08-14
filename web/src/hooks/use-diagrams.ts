import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

export interface DiagramFeatureRef {
  feature_class: string
  feature_id: string
}

export interface DiagramInstance {
  id: string
  container: DiagramFeatureRef
  diagram_type: string
  name: string | null
  created_at: string
}

export interface DiagramPlacement {
  id: string
  feature_class: string
  feature_id: string
  x: number
  y: number
  rotation: number
}

interface DiagramsResponse {
  diagrams: DiagramInstance[]
}

interface PlacementsResponse {
  placements: DiagramPlacement[]
}

/**
 * Every diagram instance hosted by one container feature -- a
 * SUBSTATION_YARD can host more than one (a one-line and a
 * control-scheme diagram for the same substation is a real case per
 * docs/metadata-core.md), so this is a list, not a single fetch.
 * Backs the "View diagram" trigger in feature-panel.tsx: only shown
 * when the selected feature's class has is_diagram_container = true,
 * and even then only meaningful once this list is non-empty.
 */
export function useDiagramInstances(featureClassCode: string | undefined, featureId: string | undefined) {
  return useQuery({
    queryKey: ["diagrams", featureClassCode, featureId],
    queryFn: () =>
      apiFetch<DiagramsResponse>(
        `/diagrams?container_feature_class=${encodeURIComponent(featureClassCode ?? "")}&container_feature_id=${encodeURIComponent(featureId ?? "")}`,
      ),
    enabled: Boolean(featureClassCode && featureId),
  })
}

/**
 * Every feature placed on one diagram instance, with its own local
 * x/y/rotation -- entirely independent of whatever real geom the same
 * feature might also have on the map (docs/phase-5-diagrams.md). Backs
 * diagram-canvas.tsx.
 */
export function useDiagramPlacements(diagramId: string | undefined) {
  return useQuery({
    queryKey: ["diagram-placements", diagramId],
    queryFn: () => apiFetch<PlacementsResponse>(`/diagrams/${diagramId}/placements`),
    enabled: Boolean(diagramId),
  })
}
