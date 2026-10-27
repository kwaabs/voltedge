import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/** The current published field-app release (GET /app/latest). Public -- the
 * download page is reachable without a session. A 404 means nothing has been
 * published yet (the query surfaces it as an error, not retried). */
export interface AppRelease {
  version: string
  version_code?: number
  filename: string
  size_bytes: number
  sha256: string
  notes?: string
  released_at: string
  /** Relative to the API; the page builds an absolute URL for the button + QR. */
  download_url: string
}

export function useAppRelease() {
  return useQuery({
    queryKey: ["app-release"],
    queryFn: () => apiFetch<AppRelease>("/app/latest"),
    retry: false,
    staleTime: 60_000,
  })
}
