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
import { useDeleteDiagramInstance } from "@/hooks/use-diagrams"
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

interface DeleteDiagramDialogProps {
  diagramId: string | null
  diagramName: string | null
  onOpenChange: (open: boolean) => void
  onDeleted: () => void
}

/**
 * Deleting a whole diagram is the most destructive diagram action -- unlike
 * un-placing one feature (remove-placement-dialog.tsx), it throws away the
 * entire schematic and every placement/element on it, and it cannot be undone.
 * So it gets the same can't-dismiss-mid-request AlertDialog guard as the other
 * permanent actions, plus a wording that makes the cascade explicit. Its
 * trigger (the header button in diagram-canvas.tsx) is only rendered when
 * usePermission agrees the user holds diagrams:delete; the server enforces the
 * same permission, so an unprivileged caller can't reach this path anyway.
 */
export function DeleteDiagramDialog({ diagramId, diagramName, onOpenChange, onDeleted }: DeleteDiagramDialogProps) {
  const deleteDiagram = useDeleteDiagramInstance()

  async function handleDelete() {
    if (!diagramId) return
    try {
      await deleteDiagram.mutateAsync(diagramId)
      onOpenChange(false)
      onDeleted()
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not delete this diagram.")
    }
  }

  return (
    <AlertDialog open={diagramId !== null} onOpenChange={(next) => !deleteDiagram.isPending && onOpenChange(next)}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {diagramName ? `"${diagramName}"` : "this diagram"}?</AlertDialogTitle>
          <AlertDialogDescription>
            This permanently deletes the whole diagram and every feature placement and annotation on it. The real
            features and their map geometry are untouched, but the schematic itself cannot be recovered.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={deleteDiagram.isPending}>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={(e) => {
              e.preventDefault()
              handleDelete()
            }}
            disabled={deleteDiagram.isPending}
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90 focus-visible:ring-destructive/20"
          >
            {deleteDiagram.isPending && <Loader2 className="size-4 animate-spin" />}
            Delete diagram
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
