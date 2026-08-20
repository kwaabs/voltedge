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
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

interface ConfirmDeleteDialogProps {
  open: boolean
  title: string
  description: string
  onOpenChange: (open: boolean) => void
  onConfirm: () => Promise<void>
  /**
   * Label for the destructive action button (default "Delete"). Set it for
   * confirms that aren't literally deletes -- "Disable", "Unlink" -- so the
   * button reads correctly while reusing this same scaffolding.
   */
  confirmLabel?: string
}

/**
 * One generic AlertDialog-based confirm, shared by all three admin
 * delete actions (feature class, domain, field group) -- same
 * can't-dismiss-mid-request pattern as the map's own delete/revoke/
 * remove-placement dialogs, but those three each carry different data
 * shapes and query-invalidation needs of their own; the three admin
 * deletes don't, they're each just "call this endpoint, then refresh
 * the list," so one parameterized component covers all three instead
 * of copy-pasting the same AlertDialog scaffolding three times.
 * onConfirm does the real DELETE call and cache invalidation; a
 * rejection (e.g. "referenced by at least one field") surfaces through
 * the app's own toast, same as everywhere else, and leaves the dialog
 * open so the message stays visible.
 */
export function ConfirmDeleteDialog({
  open,
  title,
  description,
  onOpenChange,
  onConfirm,
  confirmLabel = "Delete",
}: ConfirmDeleteDialogProps) {
  const [deleting, setDeleting] = useState(false)

  async function handleDelete() {
    setDeleting(true)
    try {
      await onConfirm()
      onOpenChange(false)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not complete this action.")
    } finally {
      setDeleting(false)
    }
  }

  return (
    <AlertDialog open={open} onOpenChange={(next) => !deleting && onOpenChange(next)}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
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
            {confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
