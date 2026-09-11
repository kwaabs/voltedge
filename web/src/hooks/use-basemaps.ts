import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/**
 * The admin-managed basemap catalog -- the raster tile sources the geographic
 * map can show (GET/POST/PATCH/DELETE /basemaps, backed by iam.basemaps; see
 * api/internal/basemap and migration 0077). Replaces the client's old hardcoded
 * BASEMAP_OPTIONS list: an admin now adds/edits/reorders/disables basemaps from
 * the Map settings page with no rebuild, and the map reads the list here.
 *
 * The blank/no-basemap choice is NOT a record -- it has no tile source and is
 * always offered client-side (see basemap-store.ts's BLANK_BASEMAP).
 */
export interface AttributionLink {
  label: string
  href: string
}

export interface BasemapRecord {
  id: string
  code: string
  label: string
  url_template: string
  attribution: AttributionLink[]
  /** Provider zoom ceiling; null = no cap (OL over-zooms the last level). */
  max_zoom: number | null
  min_zoom: number
  /** Flipped-Y TMS tile scheme; false = standard XYZ (slippy). */
  tms: boolean
  sort_order: number
  is_enabled: boolean
  /** A migration-seeded default -- editable/disableable but not deletable. */
  is_builtin: boolean
  created_at: string
  updated_at: string
  updated_by: string | null
}

interface BasemapListResponse {
  basemaps: BasemapRecord[]
}

/**
 * The enabled basemaps the map may show, in the admin's chosen order. Every map
 * session reads this to build its picker and resolve the active tile source, so
 * it's gated on the map's own features:read (not an admin permission). A long
 * staleTime -- the catalog changes rarely (an admin action) and a session on a
 * slightly stale list is harmless.
 */
export function useBasemaps() {
  return useQuery({
    queryKey: ["basemaps", "enabled"],
    queryFn: async () => (await apiFetch<BasemapListResponse>("/basemaps")).basemaps,
    staleTime: 5 * 60_000,
    retry: 1,
  })
}

/**
 * Every basemap including disabled ones (?all=true), for the admin management
 * table. Same features:read gate; the admin just wants the fuller view.
 */
export function useAllBasemaps() {
  return useQuery({
    queryKey: ["basemaps", "all"],
    queryFn: async () =>
      (await apiFetch<BasemapListResponse>("/basemaps?all=true")).basemaps,
    staleTime: 5 * 60_000,
    retry: 1,
  })
}

/** The POST body -- a new (always custom) basemap. */
export interface CreateBasemapInput {
  code: string
  label: string
  url_template: string
  attribution: AttributionLink[]
  max_zoom: number | null
  min_zoom: number
  tms: boolean
  sort_order: number
  is_enabled: boolean
}

/** The PATCH body -- every field optional; only what's sent changes. */
export interface UpdateBasemapInput {
  label?: string
  url_template?: string
  attribution?: AttributionLink[]
  max_zoom?: number | null
  clear_max_zoom?: boolean
  min_zoom?: number
  tms?: boolean
  sort_order?: number
  is_enabled?: boolean
}

/** Invalidate both list keys so the map picker and the admin table both refresh
 * after any write. */
function invalidateBasemaps(queryClient: ReturnType<typeof useQueryClient>) {
  queryClient.invalidateQueries({ queryKey: ["basemaps"] })
}

export function useCreateBasemap() {
  const queryClient = useQueryClient()
  return useMutation<BasemapRecord, ApiError, CreateBasemapInput>({
    mutationFn: (body) =>
      apiFetch<BasemapRecord>("/basemaps", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => invalidateBasemaps(queryClient),
  })
}

export function useUpdateBasemap() {
  const queryClient = useQueryClient()
  return useMutation<BasemapRecord, ApiError, { id: string; body: UpdateBasemapInput }>({
    mutationFn: ({ id, body }) =>
      apiFetch<BasemapRecord>(`/basemaps/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
    onSuccess: () => invalidateBasemaps(queryClient),
  })
}

export function useDeleteBasemap() {
  const queryClient = useQueryClient()
  return useMutation<void, ApiError, string>({
    mutationFn: (id) => apiFetch<void>(`/basemaps/${id}`, { method: "DELETE" }),
    onSuccess: () => invalidateBasemaps(queryClient),
  })
}
