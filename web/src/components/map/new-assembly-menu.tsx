import { useState } from "react"
import { Boxes, Search } from "lucide-react"

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
import { useAssemblyOptions } from "@/hooks/use-assemblies"
import { useCouplerOptions } from "@/hooks/use-couplers"
import { useDuctOptions } from "@/hooks/use-ducts"
import { useTieOptions } from "@/hooks/use-ties"
import { cn } from "@/lib/utils"
import { useDrawStore } from "@/stores/draw-store"
import { useEditStore } from "@/stores/edit-store"
import { useToolbarMenuStore } from "@/stores/toolbar-menu-store"

/**
 * Guided-assembly placements, split out of the plain "New feature" menu so the
 * two kinds of action don't share a drawer. Unlike a plain class ("place one
 * feature"), each item here places a feature AND runs a multi-step server
 * composite -- fanning out child features (RMU, cable duct) or opening a picker
 * to wire the device between two buses/feeders you choose (bus coupler, feeder
 * tie). The sections are exactly the ones that used to sit below the scroll
 * in new-feature-menu.tsx; the draw itself is still owned by voltedge-map.tsx,
 * keyed off draw-store, and the in-progress banner is still owned by
 * NewFeatureMenu -- so this button simply hides while any draw is active.
 *
 * The catalog now runs past a screenful (seven sections), so the sections
 * scroll inside a fixed-height region and a search box above filters every
 * section by its item label, hiding sections that go empty. The search only
 * narrows what's shown -- whether the button appears at all still keys off the
 * unfiltered options, so typing a non-match never makes the whole menu vanish.
 */
export function NewAssemblyMenu() {
  const { data: ductOptions } = useDuctOptions()
  const { data: couplerOptions } = useCouplerOptions()
  const { data: tieOptions } = useTieOptions()
  const { data: assemblyOptions } = useAssemblyOptions()
  const startDuctDrawing = useDrawStore((s) => s.startDuctDrawing)
  const startCouplerDrawing = useDrawStore((s) => s.startCouplerDrawing)
  const startTieDrawing = useDrawStore((s) => s.startTieDrawing)
  const startAssemblyDrawing = useDrawStore((s) => s.startAssemblyDrawing)
  const drawing = useDrawStore((s) => s.drawingFeatureClassCode)
  // Guided placement draws a new feature too, so it's blocked mid-edit for the
  // same reason as the plain New feature menu -- see new-feature-menu.tsx.
  const editing = useEditStore((s) => s.editing)
  // Only one toolbar dropdown open at a time -- see toolbar-menu-store.ts.
  const menuOpen = useToolbarMenuStore((s) => s.open === "assembly")
  const setMenuOpen = useToolbarMenuStore((s) => s.setOpen)

  const [query, setQuery] = useState("")

  // Whether ANYTHING is seeded per section -- this drives the button's presence,
  // so it must stay on the unfiltered options (a non-matching search must not
  // hide the trigger, only empty the list).
  const hasDucts = !!ductOptions && ductOptions.length > 0
  const hasCouplers = !!couplerOptions && couplerOptions.length > 0
  const hasTies = !!tieOptions && tieOptions.length > 0
  const hasAssemblies = !!assemblyOptions && assemblyOptions.length > 0

  // Search filter -- match on the visible label so what you type is what you see.
  const q = query.trim().toLowerCase()
  const matches = (label: string) => q === "" || label.toLowerCase().includes(q)
  const assemblyMatches = (assemblyOptions ?? []).filter((o) => matches(o.name))
  const ductMatches = (ductOptions ?? []).filter((o) => matches(o.duct_feature_class_name))
  const couplerMatches = (couplerOptions ?? []).filter((o) => matches(o.breaker_feature_class_name))
  const tieMatches = (tieOptions ?? []).filter((o) => matches(o.switch_feature_class_name))
  const anyMatches =
    assemblyMatches.length + ductMatches.length + couplerMatches.length + tieMatches.length > 0

  // Nothing seeded to place, or a draw is already in progress (NewFeatureMenu
  // renders the placement banner in that case) -- keep the toolbar quiet.
  if (drawing || (!hasDucts && !hasCouplers && !hasTies && !hasAssemblies)) return null

  return (
    <DropdownMenu open={menuOpen && !editing} onOpenChange={(o) => setMenuOpen("assembly", o)}>
      <DropdownMenuTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className="bg-background/90 gap-1.5 shadow-sm"
          disabled={Boolean(editing)}
          title={editing ? "Finish or cancel the current geometry edit first" : undefined}
        >
          <Boxes className="text-primary size-4" />
          Assemblies
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-72">
        <DropdownMenuLabel>Guided placement</DropdownMenuLabel>
        <p className="text-muted-foreground px-2 pb-1.5 text-xs">
          Places a feature and wires or fans out its assembly.
        </p>
        {/* Search box, pinned above the scroll. stopPropagation on keydown keeps
            Radix's menu typeahead from stealing what you type into the box. */}
        <div className="px-2 pb-1.5">
          <div className="relative">
            <Search className="text-muted-foreground pointer-events-none absolute top-1/2 left-2 size-4 -translate-y-1/2" />
            <input
              className={cn(inputClass, "pl-8")}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => e.stopPropagation()}
              placeholder="Search assemblies…"
            />
          </div>
        </div>
        <DropdownMenuSeparator />
        {/* Fixed height + scroll -- seven sections run past a screenful, so pin
            the label/description/search above and let only the sections scroll. */}
        <div className="max-h-72 overflow-x-hidden overflow-y-auto">
          {!anyMatches && (
            <p className="text-muted-foreground px-2 py-3 text-center text-sm">No matching assemblies</p>
          )}
          {assemblyMatches.length > 0 && (
            // Kit assemblies place as a point (the anchor -- a pole, a transformer, an
            // RMU, a packaged-substation enclosure) but fan out into the kit around it,
            // so each seeded (anchor class, config) is its own menu item that starts an
            // assembly draw carrying that config + optional anchor subtype -- the Save
            // then POSTs /assemblies. This is the single path for every kit (RMU and
            // packaged substations included, since the engines were consolidated); the
            // plain anchor classes still stay in the "New feature" list for a bare place.
            <>
              <DropdownMenuLabel>Assembly kits</DropdownMenuLabel>
              {assemblyMatches.map((o) => (
                <DropdownMenuItem
                  key={`${o.anchor_feature_class}:${o.config}`}
                  onSelect={() =>
                    startAssemblyDrawing(
                      o.anchor_feature_class,
                      o.config,
                      o.anchor_subtype ?? undefined,
                      o.anchor_attributes ?? undefined,
                    )
                  }
                >
                  {o.name}
                </DropdownMenuItem>
              ))}
            </>
          )}
          {ductMatches.length > 0 && (
            // Cable ducts draw as a line but fan out into DUCT_WAY children, so each
            // duct class is its own menu item that starts a duct draw -- the Save
            // then POSTs /ducts, creating the duct plus the ways its way_count sets.
            <>
              <DropdownMenuSeparator />
              <DropdownMenuLabel>Cable duct</DropdownMenuLabel>
              {ductMatches.map((o) => (
                <DropdownMenuItem
                  key={o.duct_feature_class}
                  onSelect={() => startDuctDrawing(o.duct_feature_class)}
                >
                  {o.duct_feature_class_name}
                </DropdownMenuItem>
              ))}
            </>
          )}
          {couplerMatches.length > 0 && (
            // A bus coupler places as a point (the breaker) but its Save wires the
            // breaker between two picked buses (POST /couplers). The breaker class
            // is NOT removed from the plain "New feature" list -- you still place
            // ordinary feeder breakers there; this is the extra "wired between two
            // buses" affordance, passing the option's eligible bus classes so the
            // create form knows which classes to pick the two buses from.
            <>
              <DropdownMenuSeparator />
              <DropdownMenuLabel>Bus coupler</DropdownMenuLabel>
              {couplerMatches.map((o) => (
                <DropdownMenuItem
                  key={o.breaker_feature_class}
                  onSelect={() => startCouplerDrawing(o.breaker_feature_class, o.bus_feature_classes)}
                >
                  {o.breaker_feature_class_name}
                </DropdownMenuItem>
              ))}
            </>
          )}
          {tieMatches.length > 0 && (
            // A feeder tie places as a point (the switch) but its Save wires the
            // switch between two picked feeders and forces it normally-open (POST
            // /ties). Like the coupler the switch class stays in the plain list --
            // you still place ordinary switches there; this is the extra "wired
            // across two feeders, born NORMALLY_OPEN" affordance, passing the
            // option's eligible feeder classes so the create form knows which
            // classes to pick the two conductors from.
            <>
              <DropdownMenuSeparator />
              <DropdownMenuLabel>Feeder tie (NOP)</DropdownMenuLabel>
              {tieMatches.map((o) => (
                <DropdownMenuItem
                  key={o.switch_feature_class}
                  onSelect={() => startTieDrawing(o.switch_feature_class, o.feeder_feature_classes)}
                >
                  {o.switch_feature_class_name}
                </DropdownMenuItem>
              ))}
            </>
          )}
        </div>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
