import { create } from "zustand"

/**
 * Whether the data-import dialog is open -- the one bit the map toolbar's
 * Import button toggles, mirroring export-store's `open`. Not persisted: a
 * half-configured import has no meaning across a reload, and the dialog owns
 * its own transient state while mounted. The imported overlays themselves live
 * in imported-overlays-store.ts (which IS persisted).
 */
interface ImportState {
  open: boolean
  openDialog: () => void
  closeDialog: () => void
  toggle: () => void
}

export const useImportStore = create<ImportState>((set) => ({
  open: false,
  openDialog: () => set({ open: true }),
  closeDialog: () => set({ open: false }),
  toggle: () => set((s) => ({ open: !s.open })),
}))
