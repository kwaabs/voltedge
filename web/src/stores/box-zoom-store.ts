import { create } from "zustand"

/**
 * The box-zoom tool's armed state. When `active`, voltedge-map.tsx arms a
 * throwaway Draw interaction that rubber-bands a rectangle over the map; on
 * completion it fits the view to that box and disarms (single-shot), so the
 * flag goes back to false on its own. Like measure-store.ts this owns no
 * geometry itself -- the map owns the interaction and scratch layer keyed off
 * this flag, and nothing is ever created. Session-only: an armed tool has no
 * meaning across a reload.
 */
interface BoxZoomState {
  active: boolean
  activate: () => void
  deactivate: () => void
  toggle: () => void
}

export const useBoxZoomStore = create<BoxZoomState>((set) => ({
  active: false,
  activate: () => set({ active: true }),
  deactivate: () => set({ active: false }),
  toggle: () => set((s) => ({ active: !s.active })),
}))
