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
import { type DiagramElement } from "@/hooks/use-diagrams"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

export interface PendingElementRemoval {
  id: string
  label: string
}

interface RemoveElementDialogProps {
  diagramId: string | null
  element: PendingElementRemoval | null
  onOpenChange: (open: boolean) => void
}

/**
 * The element analogue of remove-placement-dialog.tsx -- the same
 * AlertDialog-based, can't-dismiss-mid-request confirm every other
 * hover-"x" in this app uses, so deleting a box/text/line annotation
 * can't be triggered by an accidental click on the badge. Even lower
 * stakes than un-placing a feature (an element is pure diagram
 * furniture, backed by nothing), but consistent with the same guard.
 */
export function RemoveElementDialog({ diagramId, element, onOpenChange }: RemoveElementDialogProps) {
  const [removing, setRemoving] = useState(false)
  const queryClient = useQueryClient()

  async function handleRemove() {
    if (!diagramId || !element) return
    setRemoving(true)
    try {
      await apiFetch(`/diagrams/${diagramId}/elements/${element.id}`, { method: "DELETE" })
      queryClient.setQueryData<{ elements: DiagramElement[] }>(["diagram-elements", diagramId], (old) =>
        old ? { elements: old.elements.filter((el) => el.id !== element.id) } : old,
      )
      onOpenChange(false)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not remove this annotation.")
    } finally {
      setRemoving(false)
    }
  }

  return (
    <AlertDialog open={element !== null} onOpenChange={(next) => !removing && onOpenChange(next)}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Remove {element?.label ?? "this annotation"}?</AlertDialogTitle>
          <AlertDialogDescription>
            This deletes the annotation from this diagram. It is not backed by any feature, so nothing else is
            affected. You can add another at any time.
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
