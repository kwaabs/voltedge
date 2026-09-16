import { useState } from "react"
import { Plus, Search, X } from "lucide-react"

import { inputClass } from "@/components/map/field-utils"
import { OrthoToggleButton } from "@/components/map/ortho-toggle-button"
import { SnapToggleButton } from "@/components/map/snap-toggle-button"
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
import { usePackagedSubstationOptions } from "@/hooks/use-packaged-substations"
import { useRMUOptions } from "@/hooks/use-rmus"
import { cn } from "@/lib/utils"
import { type DrawGeometryType, useDrawStore } from "@/stores/draw-store"
import { useToolbarMenuStore } from "@/stores/toolbar-menu-store"

const DRAWABLE_GEOMETRY_TYPES: readonly DrawGeometryType[] = ["point", "line", "polygon"]

function isDrawGeometryType(value: string): value is DrawGeometryType {
  return (DRAWABLE_GEOMETRY_TYPES as readonly string[]).includes(value)
}

const BANNER_TEXT: Record<DrawGeometryType, string> = {
  point: "Click the map to place the new feature",
  line: "Click to add points, double-click to finish the line",
  polygon: "Click to add points, double-click to finish the polygon",
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
 * (Ring Main Unit, Cable duct, Bus coupler, Feeder tie) live in the
 * separate NewAssemblyMenu -- but RMU/duct classes are still filtered
 * OUT of this list here (drawing one plainly would skip its fan-out),
 * which is why this component still reads their options.
 */
export function NewFeatureMenu() {
  const { data: featureClasses } = useFeatureClasses()
  const { data: rmuOptions } = useRMUOptions()
  const { data: packagedOptions } = usePackagedSubstationOptions()
  const { data: ductOptions } = useDuctOptions()
  const startDrawing = useDrawStore((s) => s.startDrawing)
  const cancelDrawing = useDrawStore((s) => s.cancelDrawing)
  const drawing = useDrawStore((s) => s.drawingFeatureClassCode)
  const drawingGeometryType = useDrawStore((s) => s.drawingGeometryType)
  // Only one toolbar dropdown open at a time -- see toolbar-menu-store.ts.
  const menuOpen = useToolbarMenuStore((s) => s.open === "feature")
  const setMenuOpen = useToolbarMenuStore((s) => s.setOpen)
  const [query, setQuery] = useState("")
  // RMU classes are placed through the dedicated "Ring Main Unit" section below
  // (which POSTs /rmus to fan out the ways + internal bus), so keep them out of
  // the generic list -- drawing one there would POST a bare RMU with no
  // assembly. A class only counts as an RMU once it has a fan-out template
  // (i.e. appears in the options), so an RMU class with no seeded template still
  // shows in the generic list rather than vanishing entirely.
  const rmuClassCodes = new Set((rmuOptions ?? []).map((o) => o.rmu_feature_class))
  // Packaged-substation classes place through the dedicated "Packaged substation"
  // section (which POSTs /packaged-substations to fan out the components + wiring),
  // so keep them out of the generic list -- drawing one there would POST a bare unit
  // with no assembly. Same data-driven gate as RMUs: a class only counts once it has
  // a fan-out template (i.e. appears in the options), so one with no seeded template
  // still shows in the generic list rather than vanishing entirely.
  const packagedClassCodes = new Set((packagedOptions ?? []).map((o) => o.packaged_feature_class))
  // Cable-duct classes fan out DUCT_WAY children (Phase 44 Slice 4), so they're
  // placed through the dedicated "Cable duct" section below (POSTs /ducts) and
  // kept out of the generic list -- drawing one there would create a bare duct
  // with no ways. Same data-driven gate as RMUs: a duct class only counts once it
  // has a `contains DUCT_WAY` containment class (i.e. appears in the options).
  const ductClassCodes = new Set((ductOptions ?? []).map((o) => o.duct_feature_class))
  const drawableClasses = (featureClasses?.feature_classes ?? []).filter(
    (fc) =>
      isDrawGeometryType(fc.geometry_type) &&
      !rmuClassCodes.has(fc.code) &&
      !packagedClassCodes.has(fc.code) &&
      !ductClassCodes.has(fc.code),
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

  if (drawing) {
    return (
      <div className="bg-background/90 flex items-center gap-2 rounded-md border py-1.5 pr-1.5 pl-3 text-sm shadow-sm">
        <span className="bg-primary/70 size-2 shrink-0 animate-pulse rounded-full" />
        {drawingGeometryType ? BANNER_TEXT[drawingGeometryType] : BANNER_TEXT.point}
        {(drawingGeometryType === "line" || drawingGeometryType === "polygon") && <OrthoToggleButton />}
        <SnapToggleButton />
        <Button variant="ghost" size="icon" className="size-6 shrink-0" onClick={cancelDrawing}>
          <X className="size-3.5" />
          <span className="sr-only">Cancel</span>
        </Button>
      </div>
    )
  }

  return (
    <DropdownMenu open={menuOpen} onOpenChange={(o) => setMenuOpen("feature", o)}>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="bg-background/90 gap-1.5 shadow-sm">
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
