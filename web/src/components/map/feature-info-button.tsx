import { Info } from "lucide-react"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"

/**
 * The per-row "open attributes" (ⓘ) control on a study / trace result table.
 * Clicking a result ROW highlights AND zooms to the feature (useSelectionStore.locate
 * with focus) but does NOT open its attribute panel -- so the results stay on screen.
 * This button (or a double-click on the row) is the explicit "show me its attributes":
 * it opens the feature's attribute panel via select(..., { focus: true }). stopPropagation
 * so it doesn't also fire the row's own highlight/zoom onClick.
 */
export function FeatureInfoButton({
  featureClass,
  featureId,
  className,
}: {
  featureClass: string
  featureId: string
  className?: string
}) {
  return (
    <Button
      variant="ghost"
      size="icon"
      className={cn("text-muted-foreground size-6 shrink-0", className)}
      title="Open this feature's attributes"
      onClick={(e) => {
        e.stopPropagation()
        useSelectionStore.getState().select(featureClass, featureId, { focus: true })
      }}
    >
      <Info className="size-3.5" />
      <span className="sr-only">Open feature attributes</span>
    </Button>
  )
}
