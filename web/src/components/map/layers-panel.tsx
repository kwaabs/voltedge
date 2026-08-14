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
import { useAllSymbology } from "@/hooks/use-all-symbology"
import { useCatalog } from "@/hooks/use-catalog"
import { useFeatureClasses } from "@/hooks/use-feature-classes"
import { iconDataUri } from "@/lib/symbology-icons"
import { resolveSymbology } from "@/lib/symbology"
import { useLayersStore } from "@/stores/layers-store"

/** The mandatory class-level default rule's preview -- an icon for a
 * point class (icon_ref set), or a plain color swatch for a line/
 * polygon class (no icon_ref concept for a geometry that isn't a
 * discrete point, see symbology-icons.ts). Resolved with no subtype/
 * attributes, same as asking "what does this class look like with
 * nothing more specific known" -- exactly a legend entry's job. `null`
 * when the class has no symbology_rules at all yet (not seeded), so
 * the row just shows its name with no swatch rather than a broken one. */
function LegendSwatch({ classCode, rules }: { classCode: string; rules: ReturnType<typeof useAllSymbology> }) {
  const styleDef = resolveSymbology(rules[classCode], undefined, {}, "map")
  if (!styleDef) return null
  if (styleDef.icon_ref) {
    return <img src={iconDataUri(styleDef.icon_ref, styleDef.color)} alt="" className="size-4 shrink-0" />
  }
  const color = styleDef.fill_color ?? styleDef.stroke_color ?? styleDef.color
  if (!color) return null
  return <span className="size-3 shrink-0 rounded-sm" style={{ backgroundColor: color }} />
}

/**
 * A real layer visibility control, not the read-only "N layers" count
 * this replaced -- one checkbox per Martin catalog entry (i.e. per
 * compiled feature class), independent of whichever page/route you're
 * on. onSelect is prevented on each item so toggling one layer doesn't
 * close the menu, letting several be flipped in one go. Each row also
 * shows that class's resolved default symbology (see LegendSwatch) --
 * the map's own legend, piggybacked on this existing control instead
 * of a separate standalone panel.
 */
export function LayersPanel() {
  const { data: catalog } = useCatalog()
  const { data: featureClasses } = useFeatureClasses()
  const symbologyByClassCode = useAllSymbology()
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
              <span className="flex min-w-0 items-center gap-1.5">
                <LegendSwatch classCode={id.toUpperCase()} rules={symbologyByClassCode} />
                <span className="truncate">{nameByLowercaseCode.get(id) ?? id}</span>
              </span>
            </DropdownMenuCheckboxItem>
          ))
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
