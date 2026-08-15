import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/**
 * Wire types for /admin/* -- api/internal/admin's own DTOs
 * (featureClassSummary, domainResponse, fieldGroupResponse), mirrored
 * here the same way every other hook in this app keeps its own copy of
 * the API's shape rather than sharing generated types.
 */
export interface AdminFeatureClassSummary {
  code: string
  name: string
  geometry_type: string
  srid?: number
  network_role: string
  status: string
  is_diagram_container: boolean
  physical_table?: string
  subtype_count: number
  field_count: number
}

export interface AdminDomainValue {
  code: string
  label: string
  sort_order: number
}

export interface AdminDomain {
  code: string
  name: string
  domain_type: "coded" | "range"
  values?: AdminDomainValue[]
  range_min?: number
  range_max?: number
}

export interface AdminFieldGroup {
  code: string
  name: string
  sort_order: number
  icon?: string
}

/** Long staleTime, same reasoning useFeatureClasses/useCatalog use --
 * this is admin-authored data that changes rarely (an explicit create
 * action), not something worth polling. Each admin mutation invalidates
 * these queries itself. */
const ADMIN_STALE_TIME = 5 * 60_000

export function useAdminFeatureClasses() {
  return useQuery({
    queryKey: ["admin", "feature-classes"],
    queryFn: () => apiFetch<{ feature_classes: AdminFeatureClassSummary[] }>("/admin/feature-classes"),
    staleTime: ADMIN_STALE_TIME,
  })
}

export function useAdminDomains() {
  return useQuery({
    queryKey: ["admin", "domains"],
    queryFn: () => apiFetch<{ domains: AdminDomain[] }>("/admin/domains"),
    staleTime: ADMIN_STALE_TIME,
  })
}

export function useAdminFieldGroups() {
  return useQuery({
    queryKey: ["admin", "field-groups"],
    queryFn: () => apiFetch<{ field_groups: AdminFieldGroup[] }>("/admin/field-groups"),
    staleTime: ADMIN_STALE_TIME,
  })
}
