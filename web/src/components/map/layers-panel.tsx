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
import { useCatalog } from "@/hooks/use-catalog"
import { useLayersStore } from "@/stores/layers-store"

/**
 * A real layer visibility control, not the read-only "N layers" count
 * this replaced -- one checkbox per Martin catalog entry (i.e. per
 * compiled feature class), independent of whichever page/route you're
 * on. onSelect is prevented on each item so toggling one layer doesn't
 * close the menu, letting several be flipped in one go.
 */
export function LayersPanel() {
  const { data: catalog } = useCatalog()
  const hidden = useLayersStore((s) => s.hidden)
  const toggle = useLayersStore((s) => s.toggle)
  const sourceIds = catalog ? Object.keys(catalog.tiles) : []

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="bg-background/90 gap-1.5 shadow-sm">
          <Layers className="size-4" />
          Layers
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="w-52">
        <DropdownMenuLabel>Feature classes</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {sourceIds.length === 0 ? (
          <p className="text-muted-foreground px-2 py-1.5 text-sm">No layers compiled yet</p>
        ) : (
          sourceIds.map((id) => (
            <DropdownMenuCheckboxItem
              key={id}
              checked={!hidden[id]}
              onSelect={(e) => e.preventDefault()}
              onCheckedChange={() => toggle(id)}
            >
              {id}
            </DropdownMenuCheckboxItem>
          ))
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
