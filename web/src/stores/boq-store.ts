import { create } from "zustand"

/**
 * The single bit of Bill-of-Quantities UI state: whether the floating BoQ panel
 * is open. The map toolbar's "BoQ" button toggles it, mirroring query-store's
 * `open` and export-store's `jobsPanelOpen`. Not persisted -- a half-configured
 * pricing run has no meaning across a reload, and the catalog/prices are
 * server-owned (re-priced fresh each run).
 */
interface BoqState {
  open: boolean
  openPanel: () => void
  closePanel: () => void
  toggle: () => void
}

export const useBoqStore = create<BoqState>((set) => ({
  open: false,
  openPanel: () => set({ open: true }),
  closePanel: () => set({ open: false }),
  toggle: () => set((s) => ({ open: !s.open })),
}))
