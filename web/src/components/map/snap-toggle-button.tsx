import { Magnet } from "lucide-react"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import { useSnapStore } from "@/stores/snap-store"

/**
 * Shared between new-feature-menu.tsx's draw banner and
 * geometry-edit-toolbar.tsx -- both digitizing workflows (drawing a
 * brand new feature, reshaping an existing one) should be able to snap
 * to a nearby feature's vertex or edge, and both toggle the same
 * snap-store.ts flag rather than each keeping a separate on/off state.
 * Same active-state styling convention as geometry-edit-toolbar.tsx's
 * own Move/Edit vertices buttons (bg-accent when on).
 */
export function SnapToggleButton() {
  const enabled = useSnapStore((s) => s.enabled)

  return (
    <Button
      variant="ghost"
      size="sm"
      className={cn(enabled && "bg-accent text-accent-foreground")}
      onClick={() => useSnapStore.getState().toggle()}
      title={enabled ? "Snapping on" : "Snapping off"}
    >
      <Magnet className="size-4" />
      Snap
    </Button>
  )
}
