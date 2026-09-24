import { MousePointerClick } from "lucide-react"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import { useMapSelectStore } from "@/stores/map-select-store"

/**
 * Arms the Select tool: click to arm, then either click features on the map to
 * toggle them into a highlight-only set, or drag a box (marquee) to add every
 * feature inside it at once (voltedge-map.tsx owns the click/DragBox handling +
 * halo layer, keyed off map-select-store's `active`). Unlike a normal map click
 * this never opens the attribute panel -- it's purely a visual "highlight
 * these" pick. Same active-state styling convention as box-zoom-button.tsx
 * (bg-accent when on). Click again or Escape to exit (which clears the set).
 * The count shows how many are highlighted.
 */
export function SelectToolButton() {
  const active = useMapSelectStore((s) => s.active)
  const count = useMapSelectStore((s) => s.selected.length)

  return (
    <Button
      variant="secondary"
      size="sm"
      className={cn("shadow-sm", active && "bg-accent text-accent-foreground")}
      onClick={() => useMapSelectStore.getState().toggleActive()}
      title={active ? "Click a feature, or drag a box, to highlight (Esc to exit)" : "Select features (highlight only, no panel)"}
    >
      <MousePointerClick className={cn("size-3.5", !active && "text-sky-600")} />
      Select{active && count > 0 ? ` (${count})` : ""}
    </Button>
  )
}
