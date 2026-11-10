import { Crosshair } from "lucide-react"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import { useSelectionStore } from "@/stores/selection-store"

/**
 * The per-row "zoom to feature" control on a study / trace result table. Clicking a
 * result row only HIGHLIGHTS the feature on the map (useSelectionStore.locate, the
 * peek halo) -- it no longer flies the view or opens the attribute panel, so the
 * results stay on screen. This button is the explicit "take me there": it highlights
 * AND flies the map to the feature (locate with focus), still without opening its
 * panel. stopPropagation so it doesn't also fire the row's own highlight onClick.
 */
export function FeatureZoomButton({
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
      title="Zoom to this feature on the map"
      onClick={(e) => {
        e.stopPropagation()
        useSelectionStore.getState().locate(featureClass, featureId, { focus: true })
      }}
    >
      <Crosshair className="size-3.5" />
      <span className="sr-only">Zoom to feature</span>
    </Button>
  )
}
