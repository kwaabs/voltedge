import { useQuery } from "@tanstack/react-query"

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
  status: string
  deleted_at: string | null
  created_at: string
  updated_at: string
  subtype?: number
  geometry?: FeatureGeometry
  attributes: Record<string, unknown>
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
