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
import { useFeatureClasses } from "@/hooks/use-feature-classes"
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
  const startDrawing = useDrawStore((s) => s.startDrawing)
  const cancelDrawing = useDrawStore((s) => s.cancelDrawing)
  const drawing = useDrawStore((s) => s.drawingFeatureClassCode)
  const drawingGeometryType = useDrawStore((s) => s.drawingGeometryType)
  const drawableClasses = (featureClasses?.feature_classes ?? []).filter((fc) => isDrawGeometryType(fc.geometry_type))

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
          <Plus className="size-4" />
          New feature
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        <DropdownMenuLabel>Feature class</DropdownMenuLabel>
        <DropdownMenuSeparator />
        {drawableClasses.length === 0 ? (
          <p className="text-muted-foreground px-2 py-1.5 text-sm">No feature classes compiled yet</p>
        ) : (
          drawableClasses.map((fc) => (
            <DropdownMenuItem
              key={fc.code}
              onSelect={() => {
                if (isDrawGeometryType(fc.geometry_type)) startDrawing(fc.code, fc.geometry_type)
              }}
            >
              {fc.name}
            </DropdownMenuItem>
          ))
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
