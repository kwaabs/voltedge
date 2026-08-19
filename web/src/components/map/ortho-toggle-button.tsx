import { CornerDownRight } from "lucide-react"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import { useOrthoStore } from "@/stores/ortho-store"

/**
 * Right-angle drawing toggle for new-feature-menu.tsx's draw banner --
 * only rendered there for a line/polygon draw (a point has no segment to
 * constrain). When on, voltedge-map.tsx's Draw geometryFunction snaps
 * each in-progress segment to the nearest 45° off the previous vertex.
 * Same bg-accent-when-on styling convention as SnapToggleButton, which
 * sits right beside it in the same banner.
 */
export function OrthoToggleButton() {
  const enabled = useOrthoStore((s) => s.enabled)

  return (
    <Button
      variant="ghost"
      size="sm"
      className={cn(enabled && "bg-accent text-accent-foreground")}
      onClick={() => useOrthoStore.getState().toggle()}
      title={enabled ? "Right-angle drawing on (45° steps)" : "Right-angle drawing off"}
    >
      <CornerDownRight className="size-4" />
      Ortho
    </Button>
  )
}
