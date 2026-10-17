import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/**
 * The external-systems catalog (api/internal/externalsystem, mounted at
 * /external-systems behind metadata:read/metadata:write). A row describes
 * an external system a feature's kind='api' links (Phase 32 Slice 3) will
 * fetch live data from. The stored credential is write-only -- the API
 * returns only `has_credential`, never the secret itself.
 */
export interface ExternalSystem {
  id: string
  code: string
  name: string
  base_url: string
  auth_type: "none" | "api_key" | "basic" | "bearer"
  auth_header?: string
  auth_username?: string
  has_credential: boolean
  response_format: "json" | "xml"
  // The outbound live-fetch path template (migration 0145), with {placeholder}
  // fields filled from a feature's external_ref value, e.g. /meters/{id}/consumption.
  // Absent for push/link-only systems.
  path_template?: string
  created_at: string
  updated_at: string
}

// enabled lets a caller without metadata:read (e.g. the feature panel's
// api-link picker) skip the request entirely rather than fire a doomed
// 403 -- the admin page just leaves it defaulted on.
export function useExternalSystems(enabled = true) {
  return useQuery({
    queryKey: ["external-systems"],
    queryFn: () => apiFetch<{ external_systems: ExternalSystem[] }>("/external-systems"),
    staleTime: 30_000,
    enabled,
  })
}
