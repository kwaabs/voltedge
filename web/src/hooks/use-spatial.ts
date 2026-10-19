import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

// Client mirror of the spatial engine (api/internal/spatial, mounted at /spatial,
// gated features:read). Geometry-derived overlap/proximity across the gis.* tables:
// "what assets are inside this easement?", "what land covers this asset?", "what
// vegetation is within N m of this line?". No authored link needed — it reads the
// shapes.

export type SpatialMode = "intersects" | "contains" | "contained_by" | "within"

/** One feature found by a spatial query. distance_m is set only for a within query. */
export interface SpatialMatch {
  feature_class: string
  feature_id: string
  distance_m?: number
}

export interface SpatialQueryOpts {
  /** The reference feature. */
  featureClass?: string
  featureId?: string
  /** intersects (default) · contains · contained_by · within. */
  mode?: SpatialMode
  /** Metres; required for mode=within. */
  distance?: number
  /** Limit the search to these class codes (default: every geometric class). */
  classes?: string[]
  limit?: number
  /** Skip the query until true (e.g. a tab is opened). */
  enabled?: boolean
}

/** Features spatially related to one reference feature. Skipped until a feature
 * (and, for within, a distance) is supplied and `enabled`. */
export function useSpatialRelated(opts: SpatialQueryOpts) {
  const { featureClass, featureId, mode = "intersects", distance, classes, limit, enabled = true } = opts
  const params = new URLSearchParams()
  if (featureClass) params.set("feature_class", featureClass)
  if (featureId) params.set("feature_id", featureId)
  if (mode) params.set("mode", mode)
  if (distance != null) params.set("distance", String(distance))
  if (classes && classes.length > 0) params.set("classes", classes.join(","))
  if (limit != null) params.set("limit", String(limit))

  const ready = !!featureClass && !!featureId && (mode !== "within" || (distance != null && distance > 0))

  return useQuery({
    queryKey: ["spatial", "related", featureClass ?? "", featureId ?? "", mode, distance ?? "", (classes ?? []).join(","), limit ?? ""],
    queryFn: () => apiFetch<{ matches: SpatialMatch[]; count: number }>(`/spatial/related?${params.toString()}`),
    enabled: enabled && ready,
    staleTime: 15_000,
  })
}

/** One conductor-vs-vegetation clearance breach. */
export interface Encroachment {
  conductor_class: string
  conductor_id: string
  voltage_class?: string
  vegetation_class: string
  vegetation_id: string
  distance_m: number
  clearance_m: number
  breach_m: number
  species?: string
  height_m?: number
  veg_type?: string
}

/** Vegetation too close to a conductor (within the clearance its voltage requires).
 * Scope by a single conductor (featureClass+featureId) or a whole feeder (circuitId).
 * `clearance` sets a uniform override; omit for per-voltage defaults. */
export function useVegetationEncroachments(opts: {
  featureClass?: string
  featureId?: string
  circuitId?: string
  clearance?: number
  enabled?: boolean
}) {
  const { featureClass, featureId, circuitId, clearance, enabled = true } = opts
  const params = new URLSearchParams()
  if (featureClass) params.set("feature_class", featureClass)
  if (featureId) params.set("feature_id", featureId)
  if (circuitId) params.set("circuit_id", circuitId)
  if (clearance != null && clearance > 0) params.set("clearance", String(clearance))
  const ready = (!!featureClass && !!featureId) || !!circuitId
  return useQuery({
    queryKey: ["spatial", "encroachments", featureClass ?? "", featureId ?? "", circuitId ?? "", clearance ?? ""],
    queryFn: () => apiFetch<{ encroachments: Encroachment[]; count: number }>(`/spatial/vegetation-encroachments?${params.toString()}`),
    enabled: enabled && ready,
    staleTime: 15_000,
  })
}

/** One easement at or near expiry (days_until_expiry negative = already lapsed). */
export interface EasementDue {
  feature_class: string
  feature_id: string
  easement_ref?: string
  easement_type?: string
  easement_status?: string
  grantor?: string
  expiry_date: string
  days_until_expiry: number
  expired: boolean
}

/** One vegetation zone due/overdue for its trim cycle (days_overdue positive = overdue). */
export interface TrimDue {
  feature_class: string
  feature_id: string
  species?: string
  veg_type?: string
  condition?: string
  last_trimmed: string
  trim_cycle_months: number
  due_date: string
  days_overdue: number
}

/** Easements expired or expiring within the window (default 90 days), soonest first. */
export function useExpiringEasements(opts: { withinDays?: number; enabled?: boolean } = {}) {
  const q = opts.withinDays != null ? `?within_days=${opts.withinDays}` : ""
  return useQuery({
    queryKey: ["spatial", "easements-expiring", opts.withinDays ?? ""],
    queryFn: () => apiFetch<{ easements: EasementDue[]; count: number }>(`/spatial/easements/expiring${q}`),
    enabled: opts.enabled ?? true,
    staleTime: 60_000,
  })
}

/** Vegetation zones due/overdue for their trim cycle (default 30 days), most overdue first. */
export function useVegetationTrimDue(opts: { withinDays?: number; enabled?: boolean } = {}) {
  const q = opts.withinDays != null ? `?within_days=${opts.withinDays}` : ""
  return useQuery({
    queryKey: ["spatial", "trim-due", opts.withinDays ?? ""],
    queryFn: () => apiFetch<{ vegetation: TrimDue[]; count: number }>(`/spatial/vegetation/trim-due${q}`),
    enabled: opts.enabled ?? true,
    staleTime: 60_000,
  })
}

/** Raise a WAM trim work order for one encroachment (needs wam:write). Pass the
 * fields straight from an Encroachment row. Invalidates the WAM board. */
export function useRaiseTrimOrder() {
  const queryClient = useQueryClient()
  return useMutation<unknown, ApiError, Encroachment>({
    mutationFn: (e) =>
      apiFetch("/spatial/vegetation-encroachments/work-order", {
        method: "POST",
        body: JSON.stringify({
          conductor_class: e.conductor_class,
          conductor_id: e.conductor_id,
          vegetation_class: e.vegetation_class,
          vegetation_id: e.vegetation_id,
          distance_m: e.distance_m,
          clearance_m: e.clearance_m,
          species: e.species,
        }),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["wam"] })
    },
  })
}
