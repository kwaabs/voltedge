import { useState } from "react"
import { AlertTriangle, Loader2, Trash2 } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { useClassCounts, usePurgeDeletedClass, useWipeClass } from "@/hooks/use-class-wipe"
import { ApiError } from "@/lib/api"
import { useToastStore } from "@/stores/toast-store"

interface ClassWipeDialogProps {
  featureClassCode: string
  featureClassName: string
  open: boolean
  onOpenChange: (open: boolean) => void
}

/**
 * The "nuke" dialog behind the Features browser's Danger zone: bulk soft-delete
 * and permanent purge of a whole feature class, gated features:wipe (admin /
 * break-glass) and self-gated the same way where it's rendered. It is two-step
 * by design, mirroring the single-feature delete/purge:
 *
 *   - "Soft-delete all live" flips deleted_at on every live, in-scope row. It is
 *     reversible -- each feature can be restored from its panel until it is
 *     purged -- so it is the safer, first step.
 *   - "Purge deleted permanently" physically removes every already-soft-deleted,
 *     in-scope row and cleans its topology/relationship references. Irreversible.
 *
 * Both respect territory scope on the server (a scoped admin only touches their
 * own territory's rows; the usual unscoped admin wipes the whole class). Each
 * action requires typing the class code, so neither fires on a stray click, and
 * the counts show exactly how many rows each step would touch before you commit.
 */
export function ClassWipeDialog({
  featureClassCode,
  featureClassName,
  open,
  onOpenChange,
}: ClassWipeDialogProps) {
  const [confirmText, setConfirmText] = useState("")
  const counts = useClassCounts(featureClassCode, open)
  const wipe = useWipeClass()
  const purge = usePurgeDeletedClass()
  const busy = wipe.isPending || purge.isPending

  const confirmed = confirmText.trim() === featureClassCode
  const liveCount = counts.data?.live ?? 0
  const deletedCount = counts.data?.deleted ?? 0

  function close() {
    if (busy) return
    setConfirmText("")
    onOpenChange(false)
  }

  async function handleWipe() {
    try {
      const res = await wipe.mutateAsync({ featureClassCode })
      useToastStore
        .getState()
        .pushSuccess(
          res.soft_deleted === 0
            ? "No live features to soft-delete."
            : `Soft-deleted ${res.soft_deleted} feature${res.soft_deleted === 1 ? "" : "s"}. They can still be restored until purged.`,
        )
      setConfirmText("")
    } catch (err) {
      useToastStore
        .getState()
        .pushError(err instanceof ApiError ? err.message : "Could not soft-delete this class.")
    }
  }

  async function handlePurge() {
    try {
      const res = await purge.mutateAsync({ featureClassCode })
      useToastStore
        .getState()
        .pushSuccess(
          res.purged === 0
            ? "No soft-deleted features to purge."
            : `Permanently purged ${res.purged} feature${res.purged === 1 ? "" : "s"}.`,
        )
      setConfirmText("")
    } catch (err) {
      useToastStore
        .getState()
        .pushError(err instanceof ApiError ? err.message : "Could not purge this class.")
    }
  }

  return (
    <Dialog open={open} onOpenChange={(next) => (next ? onOpenChange(true) : close())}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <AlertTriangle className="text-destructive size-5" />
            Wipe {featureClassName}
          </DialogTitle>
          <DialogDescription>
            This affects every feature in{" "}
            <span className="text-foreground font-mono text-xs">{featureClassCode}</span> that you
            have access to. Under territory scope you only wipe rows in your own territories.
          </DialogDescription>
        </DialogHeader>

        {/* Counts */}
        <div className="grid grid-cols-2 gap-3 text-sm">
          <div className="rounded-md border p-3">
            <div className="text-muted-foreground text-xs">Live features</div>
            <div className="text-lg font-semibold">
              {counts.isPending ? "…" : liveCount}
            </div>
          </div>
          <div className="rounded-md border p-3">
            <div className="text-muted-foreground text-xs">Soft-deleted</div>
            <div className="text-lg font-semibold">
              {counts.isPending ? "…" : deletedCount}
            </div>
          </div>
        </div>
        {counts.isError && (
          <p className="text-destructive text-xs">Could not load counts. Try reopening.</p>
        )}

        {/* Type-to-confirm */}
        <div className="space-y-1.5">
          <label className="text-muted-foreground text-xs" htmlFor="wipe-confirm">
            Type the class code{" "}
            <span className="text-foreground font-mono">{featureClassCode}</span> to confirm:
          </label>
          <input
            id="wipe-confirm"
            className={inputClass}
            value={confirmText}
            onChange={(e) => setConfirmText(e.target.value)}
            placeholder={featureClassCode}
            autoComplete="off"
            spellCheck={false}
            disabled={busy}
          />
        </div>

        <DialogFooter className="flex-col gap-2 sm:flex-col sm:space-x-0">
          {/* Step 1: reversible soft-delete of every live row. */}
          <Button
            variant="outline"
            className="border-destructive/40 text-destructive hover:bg-destructive/10 w-full"
            disabled={!confirmed || busy || liveCount === 0}
            onClick={handleWipe}
          >
            {wipe.isPending ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <Trash2 className="size-4" />
            )}
            Soft-delete all {liveCount > 0 ? `${liveCount} ` : ""}live (reversible)
          </Button>

          {/* Step 2: irreversible purge of everything already soft-deleted. */}
          <Button
            className="bg-destructive text-destructive-foreground hover:bg-destructive/90 focus-visible:ring-destructive/20 w-full"
            disabled={!confirmed || busy || deletedCount === 0}
            onClick={handlePurge}
          >
            {purge.isPending ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <AlertTriangle className="size-4" />
            )}
            Purge {deletedCount > 0 ? `${deletedCount} ` : ""}deleted permanently
          </Button>

          <Button variant="ghost" className="w-full" onClick={close} disabled={busy}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
