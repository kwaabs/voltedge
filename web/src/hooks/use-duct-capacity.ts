import { useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/** One way's occupancy in a per-way capacity answer (Phase 44 Slice 4): the
 * DUCT_WAY feature id, its 1-based bore position, and whether a cable is routed
 * through it. Mirrors api/internal/crud ductWayDetail. */
export interface DuctWayDetail {
  feature_id: string
  way_number: number
  occupied: boolean
}

/** A CABLE_DUCT's spare-way capacity (Phase 44 Slices 3-4,
 * api/internal/crud/capacity.go).
 *
 * - per_way = false (coarse, Slice 3): way_count is the duct's own field; occupied
 *   is the number of cable_routing rows pulled through the duct itself; ways is
 *   absent.
 * - per_way = true (Slice 4): the duct has been fanned out into DUCT_WAY children.
 *   way_count is the number of ways, occupied the number carrying a cable, and ways
 *   carries the per-bore breakdown.
 *
 * spare = way_count - occupied, clamped at zero, in both modes. */
export interface DuctCapacity {
  way_count: number
  occupied: number
  spare: number
  per_way: boolean
  ways?: DuctWayDetail[]
}

/**
 * A duct's spare-way capacity, backed by GET /features/{code}/{id}/capacity.
 * The endpoint 400s for a class with no way_count field, so this hook is meant
 * to be enabled ONLY for a feature that actually carries way_count (the caller
 * passes `enabled`); kept off the ["feature", ...] cache key so it refetches
 * independently of the single-feature read, and re-keyed so establishing a new
 * cable_routing link (which invalidates nothing here on its own) can be picked
 * up by invalidating ["duct-capacity", ...].
 */
export function useDuctCapacity(
  featureClassCode: string | undefined,
  featureId: string | undefined,
  enabled: boolean,
) {
  return useQuery({
    queryKey: ["duct-capacity", featureClassCode, featureId],
    queryFn: () => apiFetch<DuctCapacity>(`/features/${featureClassCode}/${featureId}/capacity`),
    enabled: Boolean(enabled && featureClassCode && featureId),
  })
}
