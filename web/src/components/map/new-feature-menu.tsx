import { Plus, X } from "lucide-react"

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
import { useCouplerOptions } from "@/hooks/use-couplers"
import { useDuctOptions } from "@/hooks/use-ducts"
import { type FeatureClassSummary, useFeatureClasses } from "@/hooks/use-feature-classes"
import { useRMUOptions } from "@/hooks/use-rmus"
import { useTieOptions } from "@/hooks/use-ties"
import { type DrawGeometryType, useDrawStore } from "@/stores/draw-store"

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
 */
export function NewFeatureMenu() {
  const { data: featureClasses } = useFeatureClasses()
  const { data: rmuOptions } = useRMUOptions()
  const { data: ductOptions } = useDuctOptions()
  const { data: couplerOptions } = useCouplerOptions()
  const { data: tieOptions } = useTieOptions()
  const startDrawing = useDrawStore((s) => s.startDrawing)
  const startRMUDrawing = useDrawStore((s) => s.startRMUDrawing)
  const startDuctDrawing = useDrawStore((s) => s.startDuctDrawing)
  const startCouplerDrawing = useDrawStore((s) => s.startCouplerDrawing)
  const startTieDrawing = useDrawStore((s) => s.startTieDrawing)
  const cancelDrawing = useDrawStore((s) => s.cancelDrawing)
  const drawing = useDrawStore((s) => s.drawingFeatureClassCode)
  const drawingGeometryType = useDrawStore((s) => s.drawingGeometryType)
  // RMU classes are placed through the dedicated "Ring Main Unit" section below
  // (which POSTs /rmus to fan out the ways + internal bus), so keep them out of
  // the generic list -- drawing one there would POST a bare RMU with no
  // assembly. A class only counts as an RMU once it has a fan-out template
  // (i.e. appears in the options), so an RMU class with no seeded template still
  // shows in the generic list rather than vanishing entirely.
  const rmuClassCodes = new Set((rmuOptions ?? []).map((o) => o.rmu_feature_class))
  // Cable-duct classes fan out DUCT_WAY children (Phase 44 Slice 4), so they're
  // placed through the dedicated "Cable duct" section below (POSTs /ducts) and
  // kept out of the generic list -- drawing one there would create a bare duct
  // with no ways. Same data-driven gate as RMUs: a duct class only counts once it
  // has a `contains DUCT_WAY` containment class (i.e. appears in the options).
  const ductClassCodes = new Set((ductOptions ?? []).map((o) => o.duct_feature_class))
  const drawableClasses = (featureClasses?.feature_classes ?? []).filter(
    (fc) => isDrawGeometryType(fc.geometry_type) && !rmuClassCodes.has(fc.code) && !ductClassCodes.has(fc.code),
  )
  // Section the flat list by the admin-defined category (migration 0074).
  // Uncategorised classes fall under a single "Other" bucket, always pinned
  // last; every named category sorts alphabetically above it. A category with
  // one class still gets its own header -- the point is a scannable structure,
  // not the shortest possible list.
  const groupedClasses = groupByCategory(drawableClasses)

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
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="bg-background/90 gap-1.5 shadow-sm">
          <Plus className="text-primary size-4" />
          New feature
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-72">
        <DropdownMenuLabel>Feature class</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {drawableClasses.length === 0 ? (
          <p className="text-muted-foreground px-2 py-1.5 text-sm">No feature classes compiled yet</p>
        ) : (
          // Cap the list and scroll -- with every MV/LV class compiled this
          // runs well past a screenful, so pin the top label above and let
          // only the grouped items scroll.
          <div className="max-h-72 overflow-x-hidden overflow-y-auto">
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
        )}
        {rmuOptions && rmuOptions.length > 0 && (
          // Ring Main Units place as a point but fan out into an assembly, so
          // each seeded (class, config) is its own menu item that starts an RMU
          // draw carrying that config -- the Save then POSTs /rmus.
          <>
            <DropdownMenuSeparator />
            <DropdownMenuLabel>Ring Main Unit</DropdownMenuLabel>
            {rmuOptions.map((o) => (
              <DropdownMenuItem
                key={`${o.rmu_feature_class}:${o.config}`}
                onSelect={() => startRMUDrawing(o.rmu_feature_class, o.config)}
              >
                {o.rmu_feature_class_name} — {o.config_label}
              </DropdownMenuItem>
            ))}
          </>
        )}
        {ductOptions && ductOptions.length > 0 && (
          // Cable ducts draw as a line but fan out into DUCT_WAY children, so each
          // duct class is its own menu item that starts a duct draw -- the Save
          // then POSTs /ducts, creating the duct plus the ways its way_count sets.
          <>
            <DropdownMenuSeparator />
            <DropdownMenuLabel>Cable duct</DropdownMenuLabel>
            {ductOptions.map((o) => (
              <DropdownMenuItem
                key={o.duct_feature_class}
                onSelect={() => startDuctDrawing(o.duct_feature_class)}
              >
                {o.duct_feature_class_name}
              </DropdownMenuItem>
            ))}
          </>
        )}
        {couplerOptions && couplerOptions.length > 0 && (
          // A bus coupler places as a point (the breaker) but its Save wires the
          // breaker between two picked buses (POST /couplers). Unlike RMUs/ducts the
          // breaker class is NOT removed from the generic list above -- you still
          // place ordinary feeder breakers there; this section is the extra "wired
          // between two buses" affordance, passing the option's eligible bus classes
          // so the create form knows which classes to pick the two buses from.
          <>
            <DropdownMenuSeparator />
            <DropdownMenuLabel>Bus coupler</DropdownMenuLabel>
            {couplerOptions.map((o) => (
              <DropdownMenuItem
                key={o.breaker_feature_class}
                onSelect={() => startCouplerDrawing(o.breaker_feature_class, o.bus_feature_classes)}
              >
                {o.breaker_feature_class_name}
              </DropdownMenuItem>
            ))}
          </>
        )}
        {tieOptions && tieOptions.length > 0 && (
          // A feeder tie places as a point (the switch) but its Save wires the
          // switch between two picked feeders and forces it normally-open (POST
          // /ties). Like the coupler the switch class stays in the generic list
          // above -- you still place ordinary switches there; this is the extra
          // "wired across two feeders, born NORMALLY_OPEN" affordance, passing the
          // option's eligible feeder classes so the create form knows which
          // classes to pick the two conductors from.
          <>
            <DropdownMenuSeparator />
            <DropdownMenuLabel>Feeder tie (NOP)</DropdownMenuLabel>
            {tieOptions.map((o) => (
              <DropdownMenuItem
                key={o.switch_feature_class}
                onSelect={() => startTieDrawing(o.switch_feature_class, o.feeder_feature_classes)}
              >
                {o.switch_feature_class_name}
              </DropdownMenuItem>
            ))}
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
