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
import { type Relationship } from "@/hooks/use-relationships"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

interface RevokeRelationshipDialogProps {
  relationship: Relationship | null
  onOpenChange: (open: boolean) => void
}

/**
 * Twin of revoke-connection-dialog.tsx, for structural/spatial relationships
 * instead of electrical connections: `DELETE /relationships/{id}` (
 * api/internal/relationship's revokeRelationship) is a hard, permanent remove
 * with no soft-delete/restore pair, so it gets the same explicit-choice guard.
 * Re-establishing is a separate deliberate action (the "New relationship" flow),
 * hence "not from here".
 */
export function RevokeRelationshipDialog({ relationship, onOpenChange }: RevokeRelationshipDialogProps) {
  const [revoking, setRevoking] = useState(false)
  const queryClient = useQueryClient()

  async function handleRevoke() {
    if (!relationship) return
    setRevoking(true)
    try {
      await apiFetch(`/relationships/${relationship.id}`, { method: "DELETE" })
      // Broad, prefix-matched invalidation (no featureClassCode/featureId
      // suffix) -- a revoke changes both ends' relationship lists, and the
      // counterpart's own Relationships tab may already be cached from an
      // earlier selection, not just the currently-selected feature's.
      queryClient.invalidateQueries({ queryKey: ["relationships"] })
      onOpenChange(false)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not revoke this relationship.")
    } finally {
      setRevoking(false)
    }
  }

  return (
    <AlertDialog open={relationship !== null} onOpenChange={(next) => !revoking && onOpenChange(next)}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Revoke this relationship?</AlertDialogTitle>
          <AlertDialogDescription>
            This permanently removes the structural link between these two features. It can be
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
