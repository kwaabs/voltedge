import { useQueries, useQuery } from "@tanstack/react-query"

import { apiFetch } from "@/lib/api"

/** GeoJSON's geometry shape, EPSG:4326 -- what `ST_AsGeoJSON` on the
 * backend actually returns (api/internal/crud/sql.go) and what
 * voltedge-map.tsx's geoJSONToGeometry converts into a real OL geometry
 * for the highlight layer. Coordinates are deliberately `unknown` here
 * rather than modeled per geometry type -- a bare pair for a point, a
 * list of pairs for a line, a list of rings of pairs for a polygon --
 * the same shape geometryToGeoJSON's reverse direction already leaves
 * untyped for the same reason. */
export interface FeatureGeometry {
  type: string
  coordinates: unknown
}

export interface FeatureRecord {
  id: string
  name: string | null
  status: string
  deleted_at: string | null
  created_at: string
  updated_at: string
  subtype?: number
  geometry?: FeatureGeometry
  // Present only on geometric feature classes (Phase 11). null = the class
  // has geometry but no territory resolved (unassigned); undefined = a
  // non-geometric class with no territory_id column at all.
  territory_id?: string | null
  // Asset lifecycle: where this physical asset sits in its life. A system axis on
  // every class, distinct from `status` (draft/published). Default IN_SERVICE.
  // Dates are ISO (YYYY-MM-DD) or null. Changed through the dedicated
  // PATCH /features/{code}/{id}/lifecycle path, not the generic attribute edit.
  lifecycle_status?: string
  commissioned_at?: string | null
  retired_at?: string | null
  attributes: Record<string, unknown>
  // Private-drafts Phase B: set by the server's read overlay when the acting
  // user has a private, unpublished edit of this already-published feature that
  // only they can see. The values on this record are the overlaid (would-be)
  // values, not the public row -- an Apply publishes them, a Discard throws them
  // away (see use-pending-edits.ts). pending_private_delete additionally means
  // the private change is a retirement: the public row is still live for
  // everyone else. Both are absent (undefined) on a feature with no pending edit.
  pending_private_edit?: boolean
  pending_private_delete?: boolean
}

/**
 * Deliberately a real API call, not a read of whatever properties the
 * clicked MVT tile feature happened to carry. Vector tiles are cached
 * client-side (and by Martin) and can go stale the moment a feature is
 * edited elsewhere; the panel this backs will also host edit forms in a
 * later slice, where showing stale data would be a real correctness bug,
 * not just a cosmetic one. Same generic, metadata-driven read path
 * everything else in this app already goes through --
 * api/internal/crud's GetFeature, via GET /features/{code}/{id}.
 */
export function useFeature(featureClassCode: string | undefined, featureId: string | undefined) {
  return useQuery({
    queryKey: ["feature", featureClassCode, featureId],
    queryFn: () => apiFetch<FeatureRecord>(`/features/${featureClassCode}/${featureId}`),
    enabled: Boolean(featureClassCode && featureId),
  })
}

/** Fetch several features at once, results index-aligned to `refs`. Shares the
 * ["feature", class, id] cache with useFeature, so a feature already loaded singly
 * is reused. */
export function useFeatures(refs: { featureClassCode: string; featureId: string }[]) {
  return useQueries({
    queries: refs.map((r) => ({
      queryKey: ["feature", r.featureClassCode, r.featureId],
      queryFn: () => apiFetch<FeatureRecord>(`/features/${r.featureClassCode}/${r.featureId}`),
    })),
  })
}
