import { Scan, ScanSearch } from "lucide-react"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import { type BoxZoomMode, useBoxZoomStore } from "@/stores/box-zoom-store"

/**
 * Arms the box-zoom tool in one direction: click to arm, then drag a rectangle
 * on the map. `in` (the classic) zooms the view to the box; `out` zooms out,
 * fitting the current view into the box (a smaller box zooms out further).
 * voltedge-map.tsx owns the OL Draw interaction and the fit, keyed off
 * box-zoom-store's `mode`; it disarms after one box. On desktop, Shift+drag
 * inverts whichever direction is armed. Same active-state styling convention as
 * snap-toggle-button.tsx (bg-accent when on). Escape cancels while armed.
 */
function BoxZoomButtonBase({
  mode,
  label,
  Icon,
  idleClass,
}: {
  mode: BoxZoomMode
  label: string
  Icon: typeof Scan
  idleClass: string
}) {
  const active = useBoxZoomStore((s) => s.mode === mode)

  return (
    <Button
      variant="secondary"
      size="sm"
      className={cn("shadow-sm", active && "bg-accent text-accent-foreground")}
      onClick={() => useBoxZoomStore.getState().toggle(mode)}
      title={active ? "Draw a box (Shift+drag inverts · Esc to cancel)" : label}
    >
      <Icon className={cn("size-3.5", !active && idleClass)} /> {label}
    </Button>
  )
}

export function BoxZoomButton() {
  return <BoxZoomButtonBase mode="in" label="Box zoom" Icon={Scan} idleClass="text-sky-600" />
}

export function BoxZoomOutButton() {
  return <BoxZoomButtonBase mode="out" label="Box zoom out" Icon={ScanSearch} idleClass="text-sky-600" />
}
