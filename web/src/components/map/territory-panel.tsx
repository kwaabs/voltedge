import { LandPlot, Pencil, X } from "lucide-react"

import { Button } from "@/components/ui/button"
import { usePermission } from "@/hooks/use-auth"
import { useTerritories } from "@/hooks/use-territories"
import { useTerritoryEditStore } from "@/stores/territory-edit-store"
import { useTerritoryOverlayStore } from "@/stores/territory-overlay-store"

/**
 * The read-only inspector for a territory clicked on the boundary
 * overlay (Phase 11 Slice H). Deliberately not the feature panel's
 * draggable/resizable/tabbed shell -- a territory has no attributes to
 * edit here (authoring lives on /admin/territories, gated by
 * territories:write); this just answers "what area did I click, and
 * what's under it". Anchored bottom-left so it never collides with the
 * feature panel's bottom-right corner. Everything it shows comes from
 * the boundary feature's own GeoJSON properties (the store's `selected`)
 * plus the territory list for the parent's name and the immediate
 * children -- no per-territory fetch.
 */
export function TerritoryPanel() {
  const selected = useTerritoryOverlayStore((s) => s.selected)
  const clearSelected = useTerritoryOverlayStore((s) => s.clearSelected)
  const { data } = useTerritories()
  const can = usePermission()

  if (!selected) return null

  // Only an editor (territories:write) gets the in-map authoring entry
  // (Phase 11 Slice I); the API enforces it regardless. Starting the edit
  // closes this read-only panel -- the authoring toolbar takes over.
  const canEditBoundary = can("territories:write")
  const startBoundaryEdit = () => {
    useTerritoryEditStore.getState().start(selected.id, selected.name)
    clearSelected()
  }

  const territories = data?.territories ?? []
  const parent = selected.parent_id
    ? territories.find((t) => t.id === selected.parent_id)
    : undefined
  // Immediate children only -- the panel maps one level down, not the
  // whole subtree; the overlay itself already shows the deeper nesting.
  const children = territories
    .filter((t) => t.parent_id === selected.id)
    .sort((a, b) => a.name.localeCompare(b.name))

  return (
    <div className="bg-popover text-popover-foreground fixed bottom-10 left-4 z-40 flex w-72 flex-col overflow-hidden rounded-lg border border-border/70 border-t-2 border-t-[oklch(0.45_0.10_252)] dark:border-t-primary shadow-xl ring-1 ring-black/5">
      <div className="border-[oklch(0.45_0.10_252)]/20 dark:border-primary/15 flex items-center gap-2 border-b bg-gradient-to-b from-[oklch(0.45_0.10_252)]/15 to-[oklch(0.45_0.10_252)]/5 dark:from-accent/50 dark:to-accent/20 px-3 py-2">
        <LandPlot className="text-primary size-4 shrink-0" />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-semibold">{selected.name}</div>
          <div className="text-muted-foreground truncate text-xs">{selected.level_name}</div>
        </div>
        <Button
          variant="ghost"
          size="icon"
          className="size-7 shrink-0"
          title="Close"
          onClick={clearSelected}
        >
          <X className="size-4" />
          <span className="sr-only">Close territory panel</span>
        </Button>
      </div>

      <div className="flex flex-col gap-3 px-3 py-2.5 text-xs">
        {parent && (
          <div className="flex items-baseline justify-between gap-2">
            <span className="text-muted-foreground">Parent</span>
            <span className="text-foreground truncate font-medium">{parent.name}</span>
          </div>
        )}

        <div>
          <div className="text-muted-foreground mb-1">
            {children.length === 0
              ? "No child territories"
              : `Children (${children.length})`}
          </div>
          {children.length > 0 && (
            <ul className="flex max-h-40 flex-col gap-0.5 overflow-y-auto">
              {children.map((child) => (
                <li
                  key={child.id}
                  className="flex items-baseline justify-between gap-2 py-0.5"
                >
                  <span className="text-foreground truncate">{child.name}</span>
                  <span className="text-muted-foreground shrink-0">{child.level_name}</span>
                </li>
              ))}
            </ul>
          )}
        </div>

        {canEditBoundary && (
          <Button variant="outline" size="sm" className="w-full" onClick={startBoundaryEdit}>
            <Pencil className="size-4" />
            Edit boundary
          </Button>
        )}
      </div>
    </div>
  )
}
