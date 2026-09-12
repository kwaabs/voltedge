import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { ApiError, apiFetch } from "@/lib/api"

/**
 * The admin-configurable default map view -- the center lon/lat and zoom the
 * geographic map opens on at app start (GET/PUT /map-settings, backed by three
 * iam.system_settings rows; see api/internal/mapsettings and
 * docs/phase-39-map-settings.md). Replaces the client's old hardcoded
 * DEFAULT_CENTER/DEFAULT_ZOOM placeholder in voltedge-map.tsx.
 */
export interface MapView {
  center_lng: number
  center_lat: number
  zoom: number
  /** Optional global annotation visible-zoom band (api/internal/mapsettings):
   * below min / above max the whole annotations overlay hides. Either bound
   * null = no bound on that side; both null = annotations show at every zoom
   * (the default). Distinct from the default-view zoom above. */
  annotation_min_zoom: number | null
  annotation_max_zoom: number | null
}

/**
 * Server state, so Query. Every map session reads this once at startup to seed
 * the initial View, so it's gated on the map's own features:read (not an admin
 * permission). A long staleTime -- the default view changes rarely (an admin
 * action), and a session that opened on a slightly stale center is harmless.
 */
export function useMapSettings() {
  return useQuery({
    queryKey: ["map-settings"],
    queryFn: () => apiFetch<MapView>("/map-settings"),
    staleTime: 5 * 60_000,
    retry: 1,
  })
}

/**
 * Save the default view (admin, gated metadata:write on the server). Invalidates
 * the ["map-settings"] key so the admin form reflects the saved value; existing
 * open maps keep their current view (the setting only seeds the initial View at
 * mount), which is the intended behaviour -- changing the default must not yank
 * a working session's viewport.
 */
export function useUpdateMapSettings() {
  const queryClient = useQueryClient()
  return useMutation<MapView, ApiError, MapView>({
    mutationFn: (body) => apiFetch<MapView>("/map-settings", { method: "PUT", body: JSON.stringify(body) }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["map-settings"] })
    },
  })
}
