import { Ruler, X } from "lucide-react"

import { UnitsToggleButton } from "@/components/map/units-toggle-button"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { type MeasureMode, useMeasureStore } from "@/stores/measure-store"

const BANNER_TEXT: Record<MeasureMode, string> = {
  distance: "Click to add points, double-click to finish — measuring distance",
  area: "Click to add points, double-click to finish — measuring area",
}

/**
 * Entry point for the ad-hoc measure tool, mirroring new-feature-menu.tsx
 * exactly: a dropdown (Distance / Area) when idle, an active banner with
 * the current mode, a units toggle, and a stop button while measuring.
 * voltedge-map.tsx owns the OL Draw interaction and live tooltips keyed
 * off measure-store's `mode`; this component only sets it. Measurements
 * are ephemeral -- nothing is created, and switching the mode off clears
 * them. To measure an existing feature instead, there's nothing to arm:
 * the feature panel shows its Length/Area directly.
 */
export function MeasureToolbar() {
  const mode = useMeasureStore((s) => s.mode)
  const setMode = useMeasureStore((s) => s.setMode)

  if (mode) {
    return (
      <div className="bg-background/90 flex items-center gap-2 rounded-md border py-1.5 pr-1.5 pl-3 text-sm shadow-sm">
        <span className="bg-primary/70 size-2 shrink-0 animate-pulse rounded-full" />
        {BANNER_TEXT[mode]}
        <UnitsToggleButton />
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={() => setMode(null)}>
          <X className="size-3.5" />
          <span className="sr-only">Stop measuring</span>
        </Button>
      </div>
    )
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="bg-background/90 gap-1.5 shadow-sm">
          <Ruler className="size-4 text-teal-600" />
          Measure
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-44">
        <DropdownMenuItem onSelect={() => setMode("distance")}>Distance</DropdownMenuItem>
        <DropdownMenuItem onSelect={() => setMode("area")}>Area</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
