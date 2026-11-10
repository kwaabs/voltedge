import { useState } from "react"
import { Plus, Search } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useDuctOptions } from "@/hooks/use-ducts"
import { type FeatureClassSummary, useFeatureClasses } from "@/hooks/use-feature-classes"
import { cn } from "@/lib/utils"
import { type DrawGeometryType, useDrawStore } from "@/stores/draw-store"
import { useEditStore } from "@/stores/edit-store"
import { useToolbarMenuStore } from "@/stores/toolbar-menu-store"

const DRAWABLE_GEOMETRY_TYPES: readonly DrawGeometryType[] = ["point", "line", "polygon"]

function isDrawGeometryType(value: string): value is DrawGeometryType {
  return (DRAWABLE_GEOMETRY_TYPES as readonly string[]).includes(value)
}

interface ClassGroup {
  /** null is the uncategorised bucket, rendered as "Other" and pinned last. */
  category: string | null
  classes: FeatureClassSummary[]
}

/**
 * Bucket drawable classes by their admin-defined category (migration 0074).
 * Named categories come first, sorted alphabetically; the uncategorised
 * (null) bucket is always last, so "Other" never pushes a real section down.
 * Class order within a group is left as the API returned it (already the
 * admin catalog's order).
 */
function groupByCategory(classes: FeatureClassSummary[]): ClassGroup[] {
  const byCategory = new Map<string | null, FeatureClassSummary[]>()
  for (const fc of classes) {
    const key = fc.category?.trim() || null
    const bucket = byCategory.get(key)
    if (bucket) bucket.push(fc)
    else byCategory.set(key, [fc])
  }
  return Array.from(byCategory.entries())
    .map(([category, cs]) => ({ category, classes: cs }))
    .sort((a, b) => {
      if (a.category === null) return 1
      if (b.category === null) return -1
      return a.category.localeCompare(b.category)
    })
}

/**
 * Entry point for placing a new feature -- one item per feature class
 * with real geometry (point, line, or polygon; `none` is excluded,
 * since those never appear on the map at all). Picking one starts a
 * draw session; src/components/map/voltedge-map.tsx owns the actual OL
 * Draw interaction, keyed off draw-store's drawingFeatureClassCode/
 * drawingGeometryType.
 *
 * Plain single-feature placement only. The guided-assembly workflows
 * (Assembly kits, Cable duct, Bus coupler, Feeder tie) live in the
 * separate NewAssemblyMenu -- but duct classes are still filtered OUT of
 * this list here (drawing one plainly would skip its fan-out), which is
 * why this component still reads duct options. Kit anchors (POLE, RMU,
 * PACKAGED_SUBSTATION, ...) stay in this list: a bare place is valid, and
 * the "with its kit" affordance lives in NewAssemblyMenu.
 */
export function NewFeatureMenu() {
  const { data: featureClasses } = useFeatureClasses()
  const { data: ductOptions } = useDuctOptions()
  const startDrawing = useDrawStore((s) => s.startDrawing)
  const drawing = useDrawStore((s) => s.drawingFeatureClassCode)
  // Modifying an existing feature's geometry and drawing a brand-new one are
  // mutually exclusive workflows: both would attach their own OL interaction to
  // the same map and fight over clicks (the same reason voltedge-map blocks
  // click-to-select while editing). So while an edit is in progress, this menu
  // is disabled -- the user must finish or cancel the edit (geometry-edit-toolbar)
  // before starting a new feature.
  const editing = useEditStore((s) => s.editing)
  // Only one toolbar dropdown open at a time -- see toolbar-menu-store.ts.
  const menuOpen = useToolbarMenuStore((s) => s.open === "feature")
  const setMenuOpen = useToolbarMenuStore((s) => s.setOpen)
  const [query, setQuery] = useState("")
  // Cable-duct classes fan out DUCT_WAY children (Phase 44 Slice 4), so they're
  // placed through the dedicated "Cable duct" section of NewAssemblyMenu (POSTs
  // /ducts) and kept out of the generic list -- drawing one there would create a
  // bare duct with no ways. Data-driven gate: a duct class only counts once it has
  // a `contains DUCT_WAY` containment class (i.e. appears in the options).
  // (RMU and PACKAGED_SUBSTATION are NOT excluded: they are ordinary kit anchors
  // now the fan-out engines were consolidated, so a bare place is valid -- the kit
  // affordance lives in NewAssemblyMenu, exactly like POLE / DIST_XFMR.)
  const ductClassCodes = new Set((ductOptions ?? []).map((o) => o.duct_feature_class))
  const drawableClasses = (featureClasses?.feature_classes ?? []).filter(
    (fc) => isDrawGeometryType(fc.geometry_type) && !ductClassCodes.has(fc.code),
  )
  // Search filter -- match on class name or code, so a code-savvy user can type
  // "MV_" and a plain user can type "busbar". Narrows what's grouped/shown; the
  // menu's presence keys off the unfiltered list (below), so a non-match only
  // empties the list, never hides the trigger.
  const q = query.trim().toLowerCase()
  const filteredClasses =
    q === ""
      ? drawableClasses
      : drawableClasses.filter(
          (fc) => fc.name.toLowerCase().includes(q) || fc.code.toLowerCase().includes(q),
        )
  // Section the flat list by the admin-defined category (migration 0074).
  // Uncategorised classes fall under a single "Other" bucket, always pinned
  // last; every named category sorts alphabetically above it. A category with
  // one class still gets its own header -- the point is a scannable structure,
  // not the shortest possible list.
  const groupedClasses = groupByCategory(filteredClasses)

  // While a draw is in flight the floating, draggable DrawBanner (rendered at the
  // map level in map.tsx) takes over the "how to draw"/Ortho/Snap/cancel UI, so
  // this menu's trigger simply steps aside rather than turning into an inline
  // banner that crowded the top control cluster.
  if (drawing) return null

  return (
    <DropdownMenu open={menuOpen && !editing} onOpenChange={(o) => setMenuOpen("feature", o)}>
      <DropdownMenuTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="bg-background/90 gap-1.5 shadow-sm"
          disabled={Boolean(editing)}
          title={editing ? "Finish or cancel the current geometry edit first" : undefined}
        >
          <Plus className="text-primary size-4" />
          New feature
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-72">
        <DropdownMenuLabel>Feature class</DropdownMenuLabel>
        {drawableClasses.length === 0 ? (
          <>
            <DropdownMenuSeparator />
            <p className="text-muted-foreground px-2 py-1.5 text-sm">No feature classes compiled yet</p>
          </>
        ) : (
          <>
            {/* Search box, pinned above the scroll. stopPropagation on keydown
                keeps Radix's menu typeahead from stealing what you type. */}
            <div className="px-2 pb-1.5">
              <div className="relative">
                <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-4 -translate-y-1/2" />
                <input
                  className={cn(inputClass, "pl-8")}
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  onKeyDown={(e) => e.stopPropagation()}
                  placeholder="Search classes…"
                />
              </div>
            </div>
            <DropdownMenuSeparator />
            {/* Cap the list and scroll -- with every MV/LV class compiled this
                runs well past a screenful, so pin the label/search above and let
                only the grouped items scroll. */}
            <div className="max-h-72 overflow-x-hidden overflow-y-auto">
              {groupedClasses.length === 0 && (
                <p className="text-muted-foreground px-2 py-3 text-center text-sm">No matching classes</p>
              )}
              {groupedClasses.map((group, i) => (
              <div key={group.category ?? "__other__"}>
                {/* A separator only between groups, not above the first --
                    the DropdownMenuLabel above already opens the list. */}
                {i > 0 && <DropdownMenuSeparator />}
                <DropdownMenuLabel className="text-muted-foreground text-xs font-medium">
                  {group.category ?? "Other"}
                </DropdownMenuLabel>
                {group.classes.map((fc) => (
                  <DropdownMenuItem
                    key={fc.code}
                    onSelect={() => {
                      if (isDrawGeometryType(fc.geometry_type)) startDrawing(fc.code, fc.geometry_type)
                    }}
                  >
                    {fc.name}
                  </DropdownMenuItem>
                ))}
                </div>
              ))}
            </div>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
