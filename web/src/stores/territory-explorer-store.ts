import { create } from "zustand"

/**
 * The Territory Explorer panel's view state -- a floating, browse-only tool
 * (territory-explorer-panel.tsx) that lists the territories the current user
 * may access and, for a selected one, the features inside it. Purely a lens
 * over existing data (no authoring -- that lives in Admin -> Territories), so
 * like query-store.ts / trace-store.ts this is ephemeral and NOT persisted: an
 * open panel and a selected node have no meaning across a reload.
 *
 * `open` is the only piece the map toolbar toggles (map.tsx's button); the
 * FloatingPanel owns its own position/size. Selecting a territory bumps
 * `focusToken` so the map fits its boundary exactly once per selection -- the
 * same one-shot focus idiom the fault/OMS overlay stores use, rather than a
 * boolean the map would have to debounce.
 */
interface TerritoryExplorerState {
  open: boolean
  /** The territory whose feature breakdown is shown, or null (nothing picked). */
  selectedId: string | null
  /** Bumped on every select so voltedge-map fits the boundary once per click. */
  focusToken: number
  openPanel: () => void
  closePanel: () => void
  toggle: () => void
  select: (id: string) => void
}

export const useTerritoryExplorerStore = create<TerritoryExplorerState>((set) => ({
  open: false,
  selectedId: null,
  focusToken: 0,
  openPanel: () => set({ open: true }),
  closePanel: () => set({ open: false }),
  toggle: () => set((s) => ({ open: !s.open })),
  select: (id) => set((s) => ({ selectedId: id, focusToken: s.focusToken + 1 })),
}))
