import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

export interface TraceConfigurationSummary {
  code: string
  name: string
  direction: string
}

interface TraceConfigurationsResponse {
  trace_configurations: TraceConfigurationSummary[]
}

/**
 * Server state, so Query -- same reasoning as useFeatureClasses. Lets a
 * caller resolve a trace_configurations row by its raw `direction`
 * (e.g. "DOWNSTREAM") instead of assuming a specific seeded code like
 * "DOWNSTREAM_TRACE" exists -- feature-panel.tsx's trace trigger
 * buttons use this so they stay correct even if the seeded config's
 * code ever changes, not just its current name.
 */
export function useTraceConfigurations() {
  return useQuery({
    queryKey: ["trace-configurations"],
    queryFn: () => apiFetch<TraceConfigurationsResponse>("/trace/configurations"),
    staleTime: 5 * 60_000,
    retry: 1,
  })
}
