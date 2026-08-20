import { LandPlot, Layers, Shapes, Trash2 } from "lucide-react"

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
import { useSymbologyIcons } from "@/hooks/use-symbology-icons"
import { useTerritoryLevels } from "@/hooks/use-territories"
import { territoryColorForRank } from "@/lib/map-layers"
import { featureClassSourceIds } from "@/lib/martin"
import { iconDataUri } from "@/lib/symbology-icons"
import { resolveSymbology } from "@/lib/symbology"
import { useImportedOverlaysStore } from "@/stores/imported-overlays-store"
import { useLayersStore } from "@/stores/layers-store"
import { useTerritoryOverlayStore } from "@/stores/territory-overlay-store"

/** The mandatory class-level default rule's preview -- an icon for a
 * point class (icon_ref set), or a plain color swatch for a line/
 * polygon class (no icon_ref concept for a geometry that isn't a
 * discrete point, see symbology-icons.ts). Resolved with no subtype/
 * attributes, same as asking "what does this class look like with
 * nothing more specific known" -- exactly a legend entry's job. `null`
 * when the class has no symbology_rules at all yet (not seeded), so
 * the row just shows its name with no swatch rather than a broken one. */
function LegendSwatch({
  classCode,
  rules,
  icons,
}: {
  classCode: string
  rules: ReturnType<typeof useAllSymbology>
  icons: ReturnType<typeof useSymbologyIcons>
}) {
  const styleDef = resolveSymbology(rules[classCode], undefined, {}, "map")
  if (!styleDef) return null
  if (styleDef.icon_ref) {
    return <img src={iconDataUri(styleDef.icon_ref, styleDef.color, icons)} alt="" className="size-4 shrink-0" />
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
  const symbologyIcons = useSymbologyIcons()
  const hidden = useLayersStore((s) => s.hidden)
  const toggle = useLayersStore((s) => s.toggle)
  const territoryOverlayVisible = useTerritoryOverlayStore((s) => s.visible)
  const toggleTerritoryOverlay = useTerritoryOverlayStore((s) => s.toggle)
  const hiddenLevels = useTerritoryOverlayStore((s) => s.hiddenLevels)
  const toggleTerritoryLevel = useTerritoryOverlayStore((s) => s.toggleLevel)
  const importedOverlays = useImportedOverlaysStore((s) => s.overlays)
  const toggleImported = useImportedOverlaysStore((s) => s.toggle)
  const removeImported = useImportedOverlaysStore((s) => s.remove)
  const { data: territoryLevels } = useTerritoryLevels()
  // Territories are auto-published by Martin but aren't a feature class --
  // they live in the Overlays section below, not this list (see
  // featureClassSourceIds).
  const sourceIds = featureClassSourceIds(catalog)

  // Top-first (utility -> plot), matching the map's own rank ordering.
  const levels = [...(territoryLevels?.levels ?? [])].sort((a, b) => a.rank - b.rank)

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
                <LegendSwatch classCode={id.toUpperCase()} rules={symbologyByClassCode} icons={symbologyIcons} />
                <span className="truncate">{nameByLowercaseCode.get(id) ?? id}</span>
              </span>
            </DropdownMenuCheckboxItem>
          ))
        )}

        {/* The territory boundary overlay (Phase 11 Slice H) -- a single
            non-feature-class layer, so its own section rather than a row
            in the list above. Same no-close-on-toggle behavior. */}
        <DropdownMenuLabel>Overlays</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuCheckboxItem
          checked={territoryOverlayVisible}
          onSelect={(e) => e.preventDefault()}
          onCheckedChange={() => toggleTerritoryOverlay()}
        >
          <span className="flex min-w-0 items-center gap-1.5">
            <LandPlot className="text-primary size-4 shrink-0" />
            <span className="truncate">Territories</span>
          </span>
        </DropdownMenuCheckboxItem>

        {/* Per-level toggles (Phase 11 Slice J) -- only meaningful while the
            overlay is on, so shown only then, indented under the master
            switch. Each hides/shows one level (e.g. districts) without
            touching the others; the swatch matches territoryColorForRank so
            the row reads as the same color drawn on the map. */}
        {territoryOverlayVisible &&
          levels.map((level) => (
            <DropdownMenuCheckboxItem
              key={level.code}
              className="pl-11"
              checked={!hiddenLevels[level.code]}
              onSelect={(e) => e.preventDefault()}
              onCheckedChange={() => toggleTerritoryLevel(level.code)}
            >
              <span className="flex min-w-0 items-center gap-1.5">
                <span
                  className="size-3 shrink-0 rounded-sm"
                  style={{ backgroundColor: territoryColorForRank(level.rank) }}
                />
                <span className="truncate">{level.name}</span>
              </span>
            </DropdownMenuCheckboxItem>
          ))}

        {/* Imported overlays (Phase 15 data import) -- user-supplied reference
            layers, each toggleable and removable. Only shown once at least one
            has been imported. The trash button removes the overlay outright
            (stopPropagation so it doesn't also toggle the row's checkbox). */}
        {importedOverlays.length > 0 && (
          <>
            <DropdownMenuLabel>Imported</DropdownMenuLabel>
            <DropdownMenuSeparator />
            {importedOverlays.map((overlay) => (
              <DropdownMenuCheckboxItem
                key={overlay.id}
                checked={overlay.visible}
                onSelect={(e) => e.preventDefault()}
                onCheckedChange={() => toggleImported(overlay.id)}
              >
                <span className="flex min-w-0 flex-1 items-center gap-1.5">
                  <Shapes className="size-4 shrink-0" style={{ color: "#f59e0b" }} />
                  <span className="truncate" title={`${overlay.name} (${overlay.featureCount})`}>
                    {overlay.name}
                  </span>
                  <span className="text-muted-foreground ml-auto shrink-0 text-xs">
                    {overlay.featureCount}
                  </span>
                  <button
                    type="button"
                    aria-label={`Remove ${overlay.name}`}
                    className="text-muted-foreground hover:text-destructive shrink-0"
                    onClick={(e) => {
                      e.preventDefault()
                      e.stopPropagation()
                      removeImported(overlay.id)
                    }}
                  >
                    <Trash2 className="size-3.5" />
                  </button>
                </span>
              </DropdownMenuCheckboxItem>
            ))}
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
