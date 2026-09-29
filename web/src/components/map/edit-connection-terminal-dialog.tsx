import { useEffect, useState } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { Loader2 } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { type Connection } from "@/hooks/use-connections"
import { useFeature } from "@/hooks/use-feature"
import { terminalsForSubtype, useFeatureClassSchema } from "@/hooks/use-feature-class-schema"
import { ApiError, apiFetch } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

const selectClass =
  "border-input bg-background h-9 w-full rounded-md border px-2 text-sm outline-none focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px]"

interface EditConnectionTerminalDialogProps {
  connection: Connection | null
  // Whether the viewed feature is the connection's `from` side -- decides which
  // side's terminal this dialog edits (from_terminal vs to_terminal) and which
  // feature's schema/subtype drives the available terminals.
  isFrom: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * Correct which terminal a connection lands on for the viewed feature -- the
 * "I connected to the wrong terminal" fix -- without deleting and re-establishing
 * it. PATCHes /connectivity/{id} (api/internal/connectivity's
 * updateConnectionTerminals), which re-validates the corrected pairing against
 * network_rules exactly as an establish does; a terminal that isn't defined for
 * the feature, or a pairing no active rule authorizes, comes back as an error
 * surfaced via toast with the panel left open. The terminal dropdown mirrors
 * connect-confirm-panel.tsx's EndpointPicker: scoped to the feature's actual
 * subtype via terminalsForSubtype.
 */
export function EditConnectionTerminalDialog({ connection, isFrom, onOpenChange }: EditConnectionTerminalDialogProps) {
  const queryClient = useQueryClient()
  const [saving, setSaving] = useState(false)
  const [terminal, setTerminal] = useState("")

  // The viewed feature's own side of this connection -- its class/id (for the
  // schema + subtype) and the terminal it currently lands on.
  const thisSide = connection ? (isFrom ? connection.from : connection.to) : null
  const { data: schema } = useFeatureClassSchema(thisSide?.feature_class)
  const { data: feature } = useFeature(thisSide?.feature_class, thisSide?.feature_id)
  const terminals = schema ? terminalsForSubtype(schema, feature?.subtype) : []

  // Seed the picker with the current terminal each time the dialog opens on a
  // (possibly different) connection.
  useEffect(() => {
    setTerminal(thisSide?.terminal ?? "")
  }, [connection?.id, thisSide?.terminal])

  async function handleSave() {
    if (!connection) return
    setSaving(true)
    try {
      await apiFetch(`/connectivity/${connection.id}`, {
        method: "PATCH",
        // Send only the edited side; "" clears the terminal, a code sets it.
        body: JSON.stringify(isFrom ? { from_terminal: terminal } : { to_terminal: terminal }),
      })
      // Broad, prefix-matched invalidation -- an edit changes both ends' cached
      // connection lists, not just the currently-selected feature's.
      queryClient.invalidateQueries({ queryKey: ["connections"] })
      onOpenChange(false)
    } catch (err) {
      useToastStore.getState().pushError(err instanceof ApiError ? err.message : "Could not change this terminal.")
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={connection !== null} onOpenChange={(next) => !saving && onOpenChange(next)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Change terminal</DialogTitle>
          <DialogDescription>
            Move this connection to a different terminal on this feature. It stays the same connection —
            it isn't deleted and re-made — and the new terminal is re-checked against the network rules.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-1.5">
          <label className="text-sm font-medium">Terminal</label>
          {terminals.length > 0 ? (
            <select className={selectClass} value={terminal} onChange={(e) => setTerminal(e.target.value)}>
              <option value="">No terminal</option>
              {terminals.map((t) => (
                <option key={t.code} value={t.code}>
                  {t.code} ({t.role})
                </option>
              ))}
            </select>
          ) : (
            <p className="text-muted-foreground text-sm">This feature has no terminals to choose from.</p>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => !saving && onOpenChange(false)} disabled={saving}>
            Cancel
          </Button>
          <Button
            size="sm"
            onClick={handleSave}
            disabled={saving || terminals.length === 0 || terminal === (thisSide?.terminal ?? "")}
          >
            {saving && <Loader2 className="size-4 animate-spin" />}
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
