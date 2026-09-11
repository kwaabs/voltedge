import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"
import type { AnnotationKind, AnnotationStyle } from "@/lib/map-layers"

/**
 * One map annotation, as GET /map-annotations returns it
 * (api/internal/mapannotation). `geometry` is a raw GeoJSON geometry in
 * EPSG:4326 (typed loosely -- OpenLayers' GeoJSON reader consumes the raw
 * object); its type matches the kind (text->Point, box->Polygon,
 * line/arrow->LineString). `style` is the per-annotation appearance blob the
 * renderer reads (see annotationStyleForFeature). `text` is the note content
 * (required for kind='text', an optional caption otherwise).
 */
export interface MapAnnotation {
  id: string
  kind: AnnotationKind
  geometry: unknown
  text?: string | null
  style: AnnotationStyle
  z_index: number
  territory_id?: string | null
  created_at: string
  updated_at?: string | null
}

interface MapAnnotationsResponse {
  annotations: MapAnnotation[]
}

/**
 * The map's redline annotations for the overlay. Only fetched when the overlay
 * is actually on (`enabled`), mirroring useTerritoryBoundaries -- though
 * annotations are typically sparse, there's no reason to fetch them for a
 * session that has the layer switched off. A short staleTime keeps another
 * user's freshly drawn markup showing up without a hard reload; Slice 4's
 * create/edit mutations will invalidate the ["map-annotations"] key directly.
 */
export function useMapAnnotations(enabled: boolean) {
  return useQuery({
    queryKey: ["map-annotations"],
    queryFn: () => apiFetch<MapAnnotationsResponse>("/map-annotations"),
    enabled,
    staleTime: 30_000,
  })
}

// --- Authoring mutations (Slice 4) -----------------------------------------
// All gated by annotations:write on the server (the method-aware mount in
// cmd/server/main.go); the authoring UI is only shown to callers who hold it
// (usePermission). Each mutation invalidates ["map-annotations"] so the map
// overlay repopulates with the change without a reload.

/** Body for POST /map-annotations. `kind` fixes the geometry type
 * (text->Point, box->Polygon, line/arrow->LineString); `geometry` is a raw
 * GeoJSON geometry in EPSG:4326. `text` is required for kind='text' (an
 * optional caption otherwise); `style`/`z_index` default server-side. */
export interface CreateAnnotationBody {
  kind: AnnotationKind
  geometry: unknown
  text?: string | null
  style?: AnnotationStyle
  z_index?: number
}

/** Body for PATCH /map-annotations/{id}. Every field is optional (absent = left
 * unchanged); `kind` is immutable so it is not sent. A new `geometry` re-runs
 * territory assignment server-side. */
export interface UpdateAnnotationBody {
  geometry?: unknown
  text?: string | null
  style?: AnnotationStyle
  z_index?: number
}

function useAnnotationInvalidation() {
  const queryClient = useQueryClient()
  return () => queryClient.invalidateQueries({ queryKey: ["map-annotations"] })
}

/** Create an annotation; resolves to the created row (with its new id) so the
 * caller can immediately select it for styling/captioning. */
export function useCreateAnnotation() {
  const invalidate = useAnnotationInvalidation()
  return useMutation<MapAnnotation, ApiError, CreateAnnotationBody>({
    mutationFn: (body) =>
      apiFetch<MapAnnotation>("/map-annotations", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: invalidate,
  })
}

export function useUpdateAnnotation() {
  const invalidate = useAnnotationInvalidation()
  return useMutation<MapAnnotation, ApiError, { id: string; body: UpdateAnnotationBody }>({
    mutationFn: ({ id, body }) =>
      apiFetch<MapAnnotation>(`/map-annotations/${id}`, { method: "PATCH", body: JSON.stringify(body) }),
    onSuccess: invalidate,
  })
}

export function useDeleteAnnotation() {
  const invalidate = useAnnotationInvalidation()
  return useMutation<void, ApiError, { id: string }>({
    mutationFn: ({ id }) => apiFetch<void>(`/map-annotations/${id}`, { method: "DELETE" }),
    onSuccess: invalidate,
  })
}
