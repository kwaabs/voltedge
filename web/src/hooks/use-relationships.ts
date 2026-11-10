import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

export interface RelationshipEndpoint {
  feature_class: string
  feature_id: string
}

export interface Relationship {
  id: string
  relationship_class_id: string
  relationship_class_name: string
  kind: string
  cardinality: string
  origin: RelationshipEndpoint
  destination: RelationshipEndpoint
  established_at: string
  // Optional free-form metadata about this specific link (absent for most). A
  // spatial_support link carries the circuit's arrangement on the structure,
  // { side: "L"|"R"|"C", level: number }, read by the tower/pole elevation.
  attributes?: Record<string, unknown> | null
}

interface RelationshipsResponse {
  relationships: Relationship[]
}

/**
 * Every established structural/spatial relationship touching one
 * feature, on either side (origin or destination) -- backs the feature
 * panel's Relationships tab. Same generic list-by-feature path
 * api/internal/relationship exposes for any feature class.
 */
export function useRelationships(featureClassCode: string | undefined, featureId: string | undefined) {
  return useQuery({
    queryKey: ["relationships", featureClassCode, featureId],
    queryFn: () =>
      apiFetch<RelationshipsResponse>(
        `/relationships?feature_class=${encodeURIComponent(featureClassCode ?? "")}&feature_id=${encodeURIComponent(featureId ?? "")}`,
      ),
    enabled: Boolean(featureClassCode && featureId),
  })
}

/** One loaded support structure: its id, map position (lon/lat, WGS84) and how
 * many POINT features it carries. */
export interface SupportCount {
  feature_id: string
  count: number
  lon: number
  lat: number
}

interface SupportCountsResponse {
  counts: SupportCount[]
}

/** The loaded poles (those carrying ≥1 point attachment) with their position and
 * count — feeds the map's "loaded pole" count-badge overlay. Loaded once, cached
 * a minute; invalidated (queryKey `pole-attachment-counts`) when a pole attachment
 * is added or removed. */
export function usePoleAttachmentCounts() {
  return useQuery({
    queryKey: ["pole-attachment-counts"],
    queryFn: () => apiFetch<SupportCountsResponse>("/relationships/support-counts?origin_class=POLE"),
    staleTime: 60_000,
  })
}
