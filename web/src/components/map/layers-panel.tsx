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
import { useFeatureClasses } from "@/hooks/use-feature-classes"
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
  const { data: featureClasses } = useFeatureClasses()
  const hidden = useLayersStore((s) => s.hidden)
  const toggle = useLayersStore((s) => s.toggle)
  const sourceIds = catalog ? Object.keys(catalog.tiles) : []

  // Martin's source id is always the lowercased feature class code
  // (see api/internal/compiler/compiler.go: tableName :=
  // strings.ToLower(fc.Code)) -- this is what maps a bare tile source
  // id like "mv_conductor" back to its real display name.
  const nameByLowercaseCode = new Map(
    (featureClasses?.feature_classes ?? []).map((fc) => [fc.code.toLowerCase(), fc.name]),
  )

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="bg-background/90 gap-1.5 shadow-sm">
          <Layers className="size-4" />
          Layers
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
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
              {nameByLowercaseCode.get(id) ?? id}
            </DropdownMenuCheckboxItem>
          ))
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
