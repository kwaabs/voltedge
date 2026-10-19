import { useEffect } from "react"
import { useQueryClient } from "@tanstack/react-query"

import { useDrawStore } from "@/stores/draw-store"
import { ApiError, apiFetch } from "@/lib/api"
import { useMapRefreshStore } from "@/stores/map-refresh-store"
import { useToastStore } from "@/stores/toast-store"

/**
 * Commits a "Place on map" draw: when draw-store's `geometryUpdate` is set (the
 * user finished drawing the footprint for an existing diagram-only feature), this
 * PATCHes that geometry onto the feature -- giving a previously geometry-less
 * feature a real map footprint while keeping its id, attributes, connectivity and
 * diagram placement. The mirror of the diagram's "Add from map". Headless: it
 * renders nothing, it only reacts to the store. Mounted once on the map (map.tsx).
 *
 * The PATCH lives here (a React component with react-query) rather than in the
 * store or the OpenLayers drawend handler, so the write, cache invalidation and
 * tile refresh follow the same path feature-edit-form.tsx's geometry edit uses.
 */
export function DigitizeCommitter() {
  const geometryUpdate = useDrawStore((s) => s.geometryUpdate)
  const queryClient = useQueryClient()

  useEffect(() => {
    if (!geometryUpdate) return
    const { featureClassCode, featureId, geometry } = geometryUpdate
    let cancelled = false
    ;(async () => {
      try {
        await apiFetch(`/features/${featureClassCode}/${featureId}`, {
          method: "PATCH",
          body: JSON.stringify({ geometry }),
        })
        if (cancelled) return
        // Same refresh the edit form does after a geometry change: re-read the
        // feature and bust the browser's stale vector-tile cache.
        queryClient.invalidateQueries({ queryKey: ["feature", featureClassCode, featureId] })
        useMapRefreshStore.getState().bump()
        useToastStore.getState().pushSuccess("Placed on the map.")
      } catch (err) {
        if (cancelled) return
        useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not place this feature on the map.")
      } finally {
        // Consume the one-shot update so this doesn't re-fire; a new draw makes a
        // fresh object and re-triggers.
        if (!cancelled) useDrawStore.getState().clearGeometryUpdate()
      }
    })()
    return () => {
      cancelled = true
    }
  }, [geometryUpdate, queryClient])

  return null
}
