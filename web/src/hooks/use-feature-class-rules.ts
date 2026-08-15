import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/**
 * Wire types for GET /feature-classes/{code}/rules
 * (api/internal/rules) -- every network, relationship, and snapping
 * rule where the class appears on either side, already resolved to
 * readable codes/names server-side rather than raw metadata ids.
 * Class-scoped, not feature-instance-scoped: unlike useConnections/
 * useRelationships (which fetch what a specific feature has actually
 * established), this answers "what's allowed for this class," the same
 * for every feature of it -- so it's cached by featureClassCode alone.
 */
export interface NetworkRuleSchema {
  name: string
  direction: "outgoing" | "incoming"
  from_class: string
  from_subtype?: string
  from_terminal?: string
  to_class: string
  to_subtype?: string
  to_terminal?: string
  phase_rule?: string
  is_valid: boolean
}

export interface RelationshipRuleSchema {
  name: string
  direction: "origin" | "destination"
  origin_class: string
  destination_class: string
  kind: string
  cardinality: string
}

export interface SnappingRuleSchema {
  direction: "from" | "to"
  from_class: string
  from_subtype?: string
  to_class: string
  to_subtype?: string
  to_terminal?: string
  target_type: string
  tolerance_m: number
  priority: number
  is_required: boolean
}

export interface FeatureClassRules {
  code: string
  network_rules: NetworkRuleSchema[]
  relationship_rules: RelationshipRuleSchema[]
  snapping_rules: SnappingRuleSchema[]
}

export function useFeatureClassRules(featureClassCode: string | undefined) {
  return useQuery({
    queryKey: ["feature-class-rules", featureClassCode],
    queryFn: () => apiFetch<FeatureClassRules>(`/feature-classes/${featureClassCode}/rules`),
    enabled: Boolean(featureClassCode),
    staleTime: 5 * 60_000,
  })
}
