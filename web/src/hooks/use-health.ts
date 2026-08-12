import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

interface HealthResponse {
  status: string
}

/**
 * Server state (comes from the API), so this goes through TanStack
 * Query, not a Zustand store -- Zustand holds client/UI state (theme,
 * sidebar), Query owns anything fetched, including its own caching,
 * loading, and refetch behavior. See docs/phase-3-ui-shell.md.
 */
export function useHealth() {
  return useQuery({
    queryKey: ["health"],
    queryFn: () => apiFetch<HealthResponse>("/healthz"),
    refetchInterval: 30_000,
    retry: 1,
  })
}
