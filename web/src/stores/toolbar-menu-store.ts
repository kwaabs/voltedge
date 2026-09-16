import { create } from "zustand"

/**
 * Which map-toolbar dropdown is currently open -- and only one may be.
 *
 * The toolbar (routes/map.tsx) carries four independent Radix DropdownMenus
 * (New feature, Assemblies, Annotate, Layers), each of which used to own its
 * own open state. Radix's modal outside-dismiss would normally close menu A
 * when you click trigger B, but the toolbar wrapper sets `pointer-events-auto`
 * (so the controls stay clickable over the map), which lets a click reach
 * trigger B *without* dismissing A -- so two menus could sit open at once
 * (see the reported overlap of "New feature" + "Assemblies").
 *
 * Rather than fight Radix's dismiss layer, we coordinate their open state here:
 * each menu is controlled off `open === <its id>`, so at most one is ever true.
 * That makes single-menu-at-a-time a hard invariant, independent of any Radix
 * pointer-events quirk.
 *
 * Ephemeral view state -- which menu is open has no meaning across a reload, so
 * it's a plain (non-persisted) store like the other map-workflow stores.
 */
export type ToolbarMenuId = "feature" | "assembly" | "annotation" | "layers"

interface ToolbarMenuState {
  open: ToolbarMenuId | null
  /**
   * Drive a menu's Radix `onOpenChange`. Opening sets this menu (implicitly
   * closing whichever was open); closing clears only if THIS menu is the one
   * currently open -- so a stale close from a menu that already lost focus to
   * its sibling can't wipe the sibling's freshly-opened state.
   */
  setOpen: (id: ToolbarMenuId, open: boolean) => void
}

export const useToolbarMenuStore = create<ToolbarMenuState>((set) => ({
  open: null,
  setOpen: (id, open) =>
    set((s) => {
      if (open) return { open: id }
      return s.open === id ? { open: null } : s
    }),
}))
