import { useState } from "react"
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
import { useDeletedFeaturesStore } from "@/stores/deleted-features-store"
import { useSelectionStore } from "@/stores/selection-store"
import { useToastStore } from "@/stores/toast-store"

interface DeleteFeatureDialogProps {
  featureClassCode: string
  featureId: string
  className: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * A destructive confirmation must always be an explicit choice, never
 * an accidental one -- built on ui/alert-dialog.tsx specifically because
 * (unlike ui/sheet.tsx's plain Dialog) it does not close on an outside
 * click by Radix's own default. `onOpenChange` additionally ignores any
 * close attempt while `deleting` is in flight, so Escape can't abandon
 * the dialog mid-request either.
 */
export function DeleteFeatureDialog({
  featureClassCode,
  featureId,
  className,
  open,
  onOpenChange,
}: DeleteFeatureDialogProps) {
  const [deleting, setDeleting] = useState(false)

  async function handleDelete() {
    setDeleting(true)
    try {
      await apiFetch(`/features/${featureClassCode}/${featureId}`, { method: "DELETE" })
      // Soft-deleted server-side, but Martin's tiles keep the feature's
      // bytes regardless (see deleted-features-store.ts) -- this is
      // what actually makes it disappear from the map.
      useDeletedFeaturesStore.getState().markDeleted(featureId)
      useSelectionStore.getState().clear()
      onOpenChange(false)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not delete this feature.")
    } finally {
      setDeleting(false)
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={(next) => !deleting && onOpenChange(next)}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {className}?</AlertDialogTitle>
          <AlertDialogDescription>
            This soft-deletes the feature — it disappears from the map immediately. It can be restored later, but
            not from here.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={deleting}>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={(e) => {
              e.preventDefault()
              handleDelete()
            }}
            disabled={deleting}
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90 focus-visible:ring-destructive/20"
          >
            {deleting && <Loader2 className="size-4 animate-spin" />}
            Delete
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
