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
import { type Connection } from "@/hooks/use-connections"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

interface RevokeConnectionDialogProps {
  connection: Connection | null
  onOpenChange: (open: boolean) => void
}

/**
 * Same AlertDialog-based, can't-dismiss-mid-request pattern as
 * delete-feature-dialog.tsx -- `DELETE /connectivity/{id}` (
 * api/internal/connectivity's revokeConnection) is a hard, permanent
 * remove with no soft-delete/restore pair (unlike a feature itself),
 * so this needs the same explicit-choice guard a feature delete does,
 * not a lighter one.
 */
export function RevokeConnectionDialog({ connection, onOpenChange }: RevokeConnectionDialogProps) {
  const [revoking, setRevoking] = useState(false)
  const queryClient = useQueryClient()

  async function handleRevoke() {
    if (!connection) return
    setRevoking(true)
    try {
      await apiFetch(`/connectivity/${connection.id}`, { method: "DELETE" })
      // Broad, prefix-matched invalidation (no featureClassCode/featureId
      // suffix) -- a revoke changes both ends' connection lists, and the
      // counterpart's own Terminals tab may already be cached from an
      // earlier selection, not just the currently-selected feature's.
      queryClient.invalidateQueries({ queryKey: ["connections"] })
      onOpenChange(false)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not revoke this connection.")
    } finally {
      setRevoking(false)
    }
  }

  return (
    <AlertDialog open={connection !== null} onOpenChange={(next) => !revoking && onOpenChange(next)}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Revoke this connection?</AlertDialogTitle>
          <AlertDialogDescription>
            This permanently removes the electrical connection between these two features. It can be
            re-established later, but not from here.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={revoking}>Cancel</AlertDialogCancel>
          <AlertDialogAction
            onClick={(e) => {
              e.preventDefault()
              handleRevoke()
            }}
            disabled={revoking}
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90 focus-visible:ring-destructive/20"
          >
            {revoking && <Loader2 className="size-4 animate-spin" />}
            Revoke
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
