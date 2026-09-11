import { Layers } from "lucide-react"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { diagramLayerKey, useDiagramLayersStore } from "@/stores/diagram-layers-store"

/** One toggleable feature class present on the open diagram, with a ready
 * swatch: an `icon` data URI for a point class, or a `color` for a line
 * class (a conductor has no glyph). Assembled by diagram-canvas.tsx from the
 * same resolved symbology the canvas draws with. */
export interface DiagramLayerEntry {
  code: string
  name: string
  icon?: string
  color?: string
}

/**
 * A per-diagram layer control -- the diagram's counterpart to the map's
 * Layers/Legend panel (layers-panel.tsx). Lists every feature class actually
 * placed on this diagram and toggles each one's placements (devices and
 * their conductors) on or off, so a busy one-line can be read a class at a
 * time. Visibility is per (diagram, class) and persisted
 * (diagram-layers-store.ts); it never deletes anything, only hides it from
 * this view. Renders nothing when the diagram has no features yet -- there'd
 * be nothing to toggle.
 */
export function DiagramLayersMenu({ diagramId, entries }: { diagramId: string; entries: DiagramLayerEntry[] }) {
  const hidden = useDiagramLayersStore((s) => s.hidden)
  const toggle = useDiagramLayersStore((s) => s.toggle)

  if (entries.length === 0) return null

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" className="size-7" title="Layers — show or hide features">
          <Layers className="size-4" />
          <span className="sr-only">Layers</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="max-h-[70vh] w-52 overflow-y-auto">
        <DropdownMenuLabel>Features</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {entries.map((entry) => (
          <DropdownMenuCheckboxItem
            key={entry.code}
            checked={!hidden[diagramLayerKey(diagramId, entry.code)]}
            // Keep the menu open so several classes can be flipped in one go,
            // matching the map's Layers panel.
            onSelect={(e) => e.preventDefault()}
            onCheckedChange={() => toggle(diagramId, entry.code)}
          >
            <span className="flex min-w-0 items-center gap-1.5">
              {entry.icon ? (
                <img src={entry.icon} alt="" className="size-4 shrink-0" />
              ) : entry.color ? (
                <span className="h-1 w-4 shrink-0 rounded-sm" style={{ backgroundColor: entry.color }} />
              ) : null}
              <span className="truncate">{entry.name}</span>
            </span>
          </DropdownMenuCheckboxItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
