import type { ReactNode } from "react"
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

/**
 * FormDialog is the shared shell every admin create/edit form now sits in,
 * instead of an in-page card. It bakes in the two behaviors we want on all
 * of them uniformly:
 *
 *  - unsaved input never disappears on an accidental outside-click or Esc
 *    (both are prevented) -- only the explicit Cancel / X / save closes it;
 *  - while a save is in flight the X is hidden and Cancel/close is blocked,
 *    so the row can't vanish mid-request.
 *
 * The parent owns all field state and the save handler; it just passes the
 * fields as children and gets a standard header + Cancel/submit footer. When
 * the dialog is dismissed (Cancel, X, or a successful save calling
 * onOpenChange(false)) the parent is responsible for resetting its own
 * fields, same as the in-page forms did on close.
 */
export function FormDialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  onSubmit,
  submitLabel = "Save",
  canSubmit,
  saving,
  className,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  description?: ReactNode
  children: ReactNode
  onSubmit: () => void
  submitLabel?: string
  canSubmit: boolean
  saving: boolean
  className?: string
}) {
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next && saving) return
        onOpenChange(next)
      }}
    >
      <DialogContent
        className={className}
        showClose={!saving}
        onInteractOutside={(e) => e.preventDefault()}
        onEscapeKeyDown={(e) => e.preventDefault()}
      >
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : null}
        </DialogHeader>
        <div className="flex flex-col gap-3">{children}</div>
        <DialogFooter>
          <Button
            variant="outline"
            size="sm"
            onClick={() => onOpenChange(false)}
            disabled={saving}
          >
            Cancel
          </Button>
          <Button size="sm" onClick={onSubmit} disabled={!canSubmit || saving}>
            {saving && <Loader2 className="size-4 animate-spin" />}
            {submitLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
