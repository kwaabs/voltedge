import { useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Loader2 } from "lucide-react"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { ApiError, apiFetch } from "@/lib/api"
import { useMapRefreshStore } from "@/stores/map-refresh-store"
import { useToastStore } from "@/stores/toast-store"

interface RemoveGeometryDialogProps {
  featureClassCode: string
  featureId: string
  className: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * Confirms demoting a feature to diagram-only (Phase 26 Slice 3): a
 * `PATCH` with an explicit `geometry: null` clears the map geometry,
 * which crud/update.go accepts only on a geometry_optional class. The
 * feature itself is untouched -- its attributes, terminal connections,
 * relationships, and any diagram placements all survive; it simply
 * leaves the map. Reversible in principle (re-placing it on the map),
 * but this dialog deliberately doesn't promise a one-click undo since
 * there's no re-place-from-here affordance yet.
 *
 * Built on ui/alert-dialog.tsx for the same reason DeleteFeatureDialog is
 * -- a state-changing confirmation must be an explicit choice, and the
 * alert dialog won't close on an outside click; onOpenChange also blocks
 * any close while the request is in flight.
 */
export function RemoveGeometryDialog({
  featureClassCode,
  featureId,
  className,
  open,
  onOpenChange,
}: RemoveGeometryDialogProps) {
  const queryClient = useQueryClient()
  const [removing, setRemoving] = useState(false)

  async function handleRemove() {
    setRemoving(true)
    try {
      await apiFetch(`/features/${featureClassCode}/${featureId}`, {
        method: "PATCH",
        body: JSON.stringify({ geometry: null }),
      })
      // Same refresh pair a geometry edit uses (feature-edit-form.tsx):
      // re-read the feature so the panel drops its Length/Area/Territory
      // rows, and cache-bust the browser's vector tiles so the now
      // geom-null feature -- which the server's tile function already
      // filters out (geom IS NOT NULL) -- disappears from the map without
      // waiting for a pan or reload. Selection is intentionally kept: the
      // feature still exists, and the panel should now show its
      // geometry-less state rather than closing.
      queryClient.invalidateQueries({ queryKey: ["feature", featureClassCode, featureId] })
      useMapRefreshStore.getState().bump()
      onOpenChange(false)
    } catch (err) {
      useToastStore.getState().pushError(
        err instanceof ApiError ? err.message : "Could not remove this feature's map geometry.",
      )
    } finally {
      setRemoving(false)
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={(next) => !removing && onOpenChange(next)}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Remove map geometry from this {className}?</AlertDialogTitle>
          <AlertDialogDescription>
            The feature leaves the map but is not deleted — its attributes, terminal connections, relationships, and
            any diagram placements are kept, and it stays part of the network. It will only appear on diagrams
            afterwards.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={removing}>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={(e) => {
              e.preventDefault()
              handleRemove()
            }}
            disabled={removing}
          >
            {removing && <Loader2 className="size-4 animate-spin" />}
            Remove geometry
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
