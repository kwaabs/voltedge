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
import { type DiagramPlacement } from "@/hooks/use-diagrams"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

export interface PendingRemoval {
  id: string
  label: string
}

interface RemovePlacementDialogProps {
  diagramId: string | null
  placement: PendingRemoval | null
  onOpenChange: (open: boolean) => void
}

/**
 * Same AlertDialog-based, can't-dismiss-mid-request pattern as
 * revoke-connection-dialog.tsx and delete-feature-dialog.tsx -- a
 * hover-revealed "x" alone turned out to be too easy to hit by
 * accident (a real user complaint about this exact control), so
 * un-placing a feature from a diagram gets the same explicit-choice
 * guard those two other permanent-ish actions already have, even
 * though this one is the most reversible of the three (the feature
 * itself, and any real map geometry it has, are completely untouched
 * -- only this one diagram's x/y/rotation record is deleted).
 */
export function RemovePlacementDialog({ diagramId, placement, onOpenChange }: RemovePlacementDialogProps) {
  const [removing, setRemoving] = useState(false)
  const queryClient = useQueryClient()

  async function handleRemove() {
    if (!diagramId || !placement) return
    setRemoving(true)
    try {
      await apiFetch(`/diagrams/${diagramId}/placements/${placement.id}`, { method: "DELETE" })
      queryClient.setQueryData<{ placements: DiagramPlacement[] }>(["diagram-placements", diagramId], (old) =>
        old ? { placements: old.placements.filter((p) => p.id !== placement.id) } : old,
      )
      onOpenChange(false)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not remove this feature from the diagram.")
    } finally {
      setRemoving(false)
    }
  }

  return (
    <AlertDialog open={placement !== null} onOpenChange={(next) => !removing && onOpenChange(next)}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Remove {placement?.label ?? "this feature"} from the diagram?</AlertDialogTitle>
          <AlertDialogDescription>
            This only un-places it from this one diagram -- the feature itself, and any real map geometry it has,
            are untouched. It can be added back at any time.
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
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90 focus-visible:ring-destructive/20"
          >
            {removing && <Loader2 className="size-4 animate-spin" />}
            Remove
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
