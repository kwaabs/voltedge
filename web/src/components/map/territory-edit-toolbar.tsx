import { Eraser, PencilRuler, Save, Waypoints, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { useDraggableToolbar } from "@/hooks/use-draggable-toolbar"
import { useUpdateTerritory } from "@/hooks/use-territories"
import { cn } from "@/lib/utils"
import { useTerritoryEditStore } from "@/stores/territory-edit-store"
import { useToastStore } from "@/stores/toast-store"

/**
 * Boundary authoring toolbar (Phase 11 Slice I), floating top-center like
 * geometry-edit-toolbar.tsx -- shown for as long as
 * territory-edit-store.ts's `editing` is set (entered from the admin tree
 * or the territory panel). Self-contained: reads `editing` itself and
 * returns null otherwise, same pattern as the feature geometry toolbar.
 *
 * Draw and Edit vertices are mutually exclusive map interactions
 * (voltedge-map.tsx attaches one Draw or one Modify), so clicking the
 * active one again turns it off. Clear removes the whole boundary
 * (staged as `cleared`; Save then sends clear_geom). Save PATCHes
 * whatever the session produced -- a replaced/extended MultiPolygon or a
 * clear -- and closes; the server re-checks parent containment and
 * surfaces a rejection as a toast, so an out-of-parent boundary never
 * silently saves.
 */
export function TerritoryEditToolbar() {
  const editing = useTerritoryEditStore((s) => s.editing)
  const tool = useTerritoryEditStore((s) => s.tool)
  const dirtyGeometry = useTerritoryEditStore((s) => s.dirtyGeometry)
  const cleared = useTerritoryEditStore((s) => s.cleared)
  const update = useUpdateTerritory()
  const drag = useDraggableToolbar()

  if (!editing) return null

  const hasChange = dirtyGeometry !== null || cleared
  const canSave = hasChange && !update.isPending

  function handleSave() {
    if (!editing || !hasChange) return
    const body = cleared ? { clear_geom: true } : { geometry: dirtyGeometry ?? undefined }
    update.mutate(
      { id: editing.id, body },
      {
        onSuccess: () => useTerritoryEditStore.getState().stop(),
        onError: (err) =>
          useToastStore.getState().pushError(err.message || "Could not save the boundary."),
      },
    )
  }

  // `absolute`, not `fixed`: rendered inside the map column (map.tsx) so it sits
  // below the app header and centers over the map, not the viewport (a `fixed`
  // top-3 overlapped the header).
  return (
    <div
      ref={drag.ref}
      style={drag.style}
      className={cn(
        "bg-popover text-popover-foreground absolute z-40 flex items-center gap-1 rounded-md border p-1 shadow-lg",
        // Until dragged, stay centered near the top of the map (nudged down a
        // little so it clears the top control cluster); a drag pins an explicit
        // left/top (drag.style) and drops this translate anchor.
        !drag.positioned && "top-8 left-1/2 -translate-x-1/2",
      )}
    >
      {drag.grip}
      <span className="max-w-40 truncate px-1.5 text-xs font-medium" title={editing.name}>
        {editing.name}
      </span>
      <span className="bg-border h-5 w-px" />

      <Button
        variant="ghost"
        size="sm"
        className={cn(tool === "draw" && "bg-accent text-accent-foreground")}
        onClick={() => useTerritoryEditStore.getState().setTool(tool === "draw" ? null : "draw")}
      >
        <PencilRuler className="size-4" />
        Draw
      </Button>
      <Button
        variant="ghost"
        size="sm"
        className={cn(tool === "vertices" && "bg-accent text-accent-foreground")}
        onClick={() => useTerritoryEditStore.getState().setTool(tool === "vertices" ? null : "vertices")}
      >
        <Waypoints className="size-4" />
        Edit vertices
      </Button>
      <Button
        variant="ghost"
        size="sm"
        className="text-destructive"
        title="Remove this territory's boundary"
        onClick={() => useTerritoryEditStore.getState().clearGeometry()}
      >
        <Eraser className="size-4" />
        Clear
      </Button>

      <span className="bg-border h-5 w-px" />
      <Button variant="ghost" size="sm" onClick={() => useTerritoryEditStore.getState().stop()}>
        <X className="size-4" />
        Cancel
      </Button>
      <Button size="sm" disabled={!canSave} onClick={handleSave}>
        <Save className="size-4" />
        Save
      </Button>

      <p className="text-muted-foreground border-l pl-2 text-xs whitespace-nowrap">
        {tool === "draw"
          ? "Click to add polygon vertices, double-click to finish -- each shape adds to the boundary"
          : tool === "vertices"
            ? "Drag a vertex to move it, a segment to add one, Alt+click to delete one"
            : "Pick Draw to add area, or Edit vertices to reshape. Must stay within the parent."}
      </p>
    </div>
  )
}
