import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

export interface ConnectionEndpoint {
  feature_class: string
  feature_id: string
  terminal?: string | null
}

export interface Connection {
  id: string
  network_rule_id: string
  network_rule_name: string
  from: ConnectionEndpoint
  to: ConnectionEndpoint
  established_at: string
}

interface ConnectionsResponse {
  connections: Connection[]
}

/**
 * Every established electrical connection touching one feature, on
 * either side -- a junction like DIST_XFMR is typically the "to" side
 * of several incoming conductors, but a feature can appear as "from"
 * elsewhere too, and this returns both. Backs the feature panel's
 * Terminals tab. Same generic list-by-feature path api/internal/connectivity
 * exposes for any feature class, not something feature-panel.tsx has to
 * know the shape of ahead of time.
 */
export function useConnections(featureClassCode: string | undefined, featureId: string | undefined) {
  return useQuery({
    queryKey: ["connections", featureClassCode, featureId],
    queryFn: () =>
      apiFetch<ConnectionsResponse>(
        `/connectivity?feature_class=${encodeURIComponent(featureClassCode ?? "")}&feature_id=${encodeURIComponent(featureId ?? "")}`,
      ),
    enabled: Boolean(featureClassCode && featureId),
  })
}
