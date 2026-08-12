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
