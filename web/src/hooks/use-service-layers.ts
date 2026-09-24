import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"
import type { AttributionLink } from "@/hooks/use-basemaps"

/**
 * The admin-managed service-layer catalog -- external/3rd-party map services the
 * geographic map can overlay on top of the basemap (GET/POST/PATCH/DELETE
 * /service-layers, backed by iam.service_layers; see api/internal/servicelayers
 * and migration 0113). The operational sibling of use-basemaps.ts: a basemap is
 * the single raster source under everything, a service layer is a toggleable
 * overlay that stacks and can be any of several service types.
 *
 * Slice 1 is the catalog + admin CRUD only -- the map does not render these yet.
 * Later slices add the proxy, source factory and layers-panel section.
 */
export type ServiceType =
  | "wms"
  | "wmts"
  | "xyz"
  | "vector_tile"
  | "esri_map"
  | "esri_feature"
  | "wfs"

export type ServiceAuthType = "none" | "api_key" | "basic" | "bearer"

export interface ServiceLayerRecord {
  id: string
  code: string
  label: string
  description?: string
  service_type: ServiceType
  url: string
  /** WMS LAYERS / WMTS-Esri layer id / WFS typeName; absent for a single-layer tile source. */
  layer_ids?: string
  /** Vector-tile (Mapbox/Esri VTS) style JSON URL. */
  style_url?: string
  /** Requested raster image format (image/png, image/jpeg) for wms/esri_map. */
  image_format?: string
  /** Pinned protocol version (WMS 1.3.0, WFS 2.0.0). */
  version?: string
  crs: string
  /** Coverage [minLon,minLat,maxLon,maxLat] in EPSG:4326; absent = unknown/global. */
  extent?: number[]
  max_zoom: number | null
  min_zoom: number
  default_opacity: number
  /** Supports click-to-identify (WMS GetFeatureInfo / Esri identify / vector hit-test). */
  is_queryable: boolean
  attribution: AttributionLink[]
  /** Reached through our same-origin proxy (hides creds, solves CORS) vs. directly. */
  proxied: boolean
  auth_type: ServiceAuthType
  auth_header?: string
  auth_query_param?: string
  auth_username?: string
  /** Whether a secret is stored -- the secret itself is never returned. */
  has_credential: boolean
  sort_order: number
  is_enabled: boolean
  created_at: string
  updated_at: string
  updated_by: string | null
}

interface ServiceLayerListResponse {
  service_layers: ServiceLayerRecord[]
}

/**
 * The enabled service layers the map may overlay, in the admin's chosen order.
 * Gated on the map's own features:read (not an admin permission) since every map
 * session reads it to build the layers panel. Long staleTime -- the catalog
 * changes rarely (an admin action).
 */
export function useServiceLayers() {
  return useQuery({
    queryKey: ["service-layers", "enabled"],
    queryFn: async () => (await apiFetch<ServiceLayerListResponse>("/service-layers")).service_layers,
    staleTime: 5 * 60_000,
    retry: 1,
  })
}

/** Every service layer including disabled ones (?all=true), for the admin table. */
export function useAllServiceLayers() {
  return useQuery({
    queryKey: ["service-layers", "all"],
    queryFn: async () =>
      (await apiFetch<ServiceLayerListResponse>("/service-layers?all=true")).service_layers,
    staleTime: 5 * 60_000,
    retry: 1,
  })
}

/** The POST body -- a new service layer. Credential is the plaintext secret,
 * write-only (sealed server-side, never returned). */
export interface CreateServiceLayerInput {
  code: string
  label: string
  description?: string
  service_type: ServiceType
  url: string
  layer_ids?: string
  style_url?: string
  image_format?: string
  version?: string
  crs?: string
  extent?: number[]
  max_zoom?: number | null
  min_zoom?: number
  default_opacity?: number
  is_queryable?: boolean
  attribution?: AttributionLink[]
  proxied?: boolean
  auth_type?: ServiceAuthType
  auth_header?: string
  auth_query_param?: string
  auth_username?: string
  credential?: string
  sort_order?: number
  is_enabled?: boolean
}

/** The PATCH body -- every field optional; only what's sent changes. clear_max_zoom
 * removes the zoom cap; credential is three-state (absent=keep, ""=clear, set=replace). */
export interface UpdateServiceLayerInput {
  label?: string
  description?: string
  service_type?: ServiceType
  url?: string
  layer_ids?: string
  style_url?: string
  image_format?: string
  version?: string
  crs?: string
  extent?: number[]
  max_zoom?: number | null
  clear_max_zoom?: boolean
  min_zoom?: number
  default_opacity?: number
  is_queryable?: boolean
  attribution?: AttributionLink[]
  proxied?: boolean
  auth_type?: ServiceAuthType
  auth_header?: string
  auth_query_param?: string
  auth_username?: string
  credential?: string
  sort_order?: number
  is_enabled?: boolean
}

/** Invalidate both list keys so the map layers panel and admin table both refresh. */
function invalidate(queryClient: ReturnType<typeof useQueryClient>) {
  queryClient.invalidateQueries({ queryKey: ["service-layers"] })
}

export function useCreateServiceLayer() {
  const queryClient = useQueryClient()
  return useMutation<ServiceLayerRecord, ApiError, CreateServiceLayerInput>({
    mutationFn: (body) =>
      apiFetch<ServiceLayerRecord>("/service-layers", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => invalidate(queryClient),
  })
}

export function useUpdateServiceLayer() {
  const queryClient = useQueryClient()
  return useMutation<ServiceLayerRecord, ApiError, { id: string; body: UpdateServiceLayerInput }>({
    mutationFn: ({ id, body }) =>
      apiFetch<ServiceLayerRecord>(`/service-layers/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
    onSuccess: () => invalidate(queryClient),
  })
}

export function useDeleteServiceLayer() {
  const queryClient = useQueryClient()
  return useMutation<void, ApiError, string>({
    mutationFn: (id) => apiFetch<void>(`/service-layers/${id}`, { method: "DELETE" }),
    onSuccess: () => invalidate(queryClient),
  })
}

/** Inline credential passed to discovery when the admin has typed a secret into
 * the form (create, or an edit that re-typed it). Omitted otherwise, in which
 * case an edit's stored credential is used server-side via the layer id. */
export interface DiscoverAuthInput {
  type: ServiceAuthType
  header?: string
  query_param?: string
  username?: string
  secret?: string
}

export interface DiscoverServiceLayerInput {
  /** Full GetCapabilities / ?f=json URL, built by service-layers-capabilities.ts. */
  url: string
  /** Existing layer id (edit) so the server may use its stored credential. */
  id?: string
  auth?: DiscoverAuthInput
}

export interface DiscoverServiceLayerResponse {
  content_type: string
  /** Raw capabilities document (XML or JSON) for the browser to parse. */
  body: string
}

/** Fetches a capabilities document server-side (SSRF-vetted) so the admin form
 * can auto-fill a layer's fields from what the service advertises (Slice 5). */
export function useDiscoverServiceLayer() {
  return useMutation<DiscoverServiceLayerResponse, ApiError, DiscoverServiceLayerInput>({
    mutationFn: (body) =>
      apiFetch<DiscoverServiceLayerResponse>("/service-layers/discover", {
        method: "POST",
        body: JSON.stringify(body),
      }),
  })
}
