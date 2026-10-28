import { create } from "zustand"

/**
 * Which device's detail panel is open on the Operations / Live state surface.
 *
 * The two tabs are separate routes that unmount when you switch between them, so
 * a per-page `useState` would drop the open panel on every tab change. Holding it
 * here keeps the same device selected as you move between Operations and Live
 * state. Not persisted -- an open panel has no meaning across a reload, same as
 * the map's selection-store.
 */
interface DevicePanelState {
  selected: { cls: string; id: string } | null
  open: (cls: string, id: string) => void
  close: () => void
}

export const useDevicePanelStore = create<DevicePanelState>((set) => ({
  selected: null,
  open: (cls, id) => set({ selected: { cls, id } }),
  close: () => set({ selected: null }),
}))
